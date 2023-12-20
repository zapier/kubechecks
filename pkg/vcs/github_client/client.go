package github_client

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v53/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"
	"github.com/spf13/viper"
	"golang.org/x/oauth2"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type Client struct {
	v4Client        *githubv4.Client
	username, email string

	*github.Client
}

var _ vcs.Client = new(Client)

// CreateGithubClient creates a new GitHub client using the auth token provided. We
// can't validate the token at this point, so if it exists we assume it works
func CreateGithubClient() (*Client, error) {
	var (
		err            error
		googleClient   *github.Client
		shurcoolClient *githubv4.Client
	)

	// Initialize the GitLab client with access token
	t := viper.GetString("vcs-token")
	if t == "" {
		log.Fatal().Msg("github token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(t))
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: t},
	)
	tc := oauth2.NewClient(ctx, ts)

	githubUrl := viper.GetString("vcs-base-url")
	if githubUrl == "" {
		googleClient = github.NewClient(tc) // If this has failed, we'll catch it on first call

		// Use the client from shurcooL's githubv4 library for queries.
		shurcoolClient = githubv4.NewClient(tc)
	} else {
		googleClient, err = github.NewEnterpriseClient(githubUrl, githubUrl, tc)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create github enterprise client")
		}
		shurcoolClient = githubv4.NewEnterpriseClient(githubUrl, tc)
	}

	user, _, err := googleClient.Users.Get(ctx, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user")
	}

	return &Client{
		Client:   googleClient,
		v4Client: shurcoolClient,
		username: *user.Login,
		email:    *user.Email,
	}, nil
}

func (c *Client) Username() string { return c.username }
func (c *Client) Email() string    { return c.email }
func (c *Client) GetName() string {
	return "github"
}

func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	// GitHub provides the SHA256 of the secret + payload body, so we extract the body and compare
	// We have to split it like this as the ValidatePayload method consumes the request
	if secret != "" {
		return github.ValidatePayload(r, []byte(secret))
	} else {
		// No secret provided, so we just grab the body
		return io.ReadAll(r.Body)
	}
}

func (c *Client) ParseHook(r *http.Request, request []byte) (*repo.Repo, error) {
	payload, err := github.ParseWebHook(github.WebHookType(r), request)
	if err != nil {
		return nil, err
	}

	switch p := payload.(type) {
	case *github.PullRequestEvent:
		switch p.GetAction() {
		case "opened", "synchronize", "reopened", "edited":
			log.Info().Str("action", p.GetAction()).Msg("handling Github event from PR")
			return c.buildRepoFromEvent(p), nil
		default:
			log.Info().Str("action", p.GetAction()).Msg("ignoring Github pull request event due to non commit based action")
			return nil, vcs.ErrInvalidType
		}
	default:
		log.Error().Msg("invalid event provided to Github client")
		return nil, vcs.ErrInvalidType
	}
}

// We need an email and username for authenticating our local git repository
// Grab the current authenticated client login and email
func (c *Client) getUserDetails() (string, string, error) {
	user, _, err := c.Users.Get(context.Background(), "")
	if err != nil {
		return "", "", err
	}

	// Some users on GitHub don't have an email listed; if so, catch that and return empty string
	if user.Email == nil {
		log.Error().Msg("could not load Github user email")
		return *user.Login, "", nil
	}

	return *user.Login, *user.Email, nil

}

func (c *Client) buildRepoFromEvent(event *github.PullRequestEvent) *repo.Repo {
	var labels []string
	for _, label := range event.PullRequest.Labels {
		labels = append(labels, label.GetName())
	}

	return &repo.Repo{
		BaseRef:       *event.PullRequest.Base.Ref,
		HeadRef:       *event.PullRequest.Head.Ref,
		DefaultBranch: *event.Repo.DefaultBranch,
		CloneURL:      *event.Repo.CloneURL,
		FullName:      event.Repo.GetFullName(),
		Owner:         *event.Repo.Owner.Login,
		Name:          event.Repo.GetName(),
		CheckID:       *event.PullRequest.Number,
		SHA:           *event.PullRequest.Head.SHA,
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,
	}
}

func toGithubCommitStatus(state pkg.CommitState) *string {
	switch state {
	case pkg.StateError, pkg.StatePanic:
		return pkg.Pointer("error")
	case pkg.StateFailure, pkg.StateWarning:
		return pkg.Pointer("failure")
	case pkg.StateRunning:
		return pkg.Pointer("pending")
	case pkg.StateSuccess:
		return pkg.Pointer("success")

	default: // maybe a different one? panic?
		log.Warn().Str("state", state.String()).Msg("failed to convert to a github commit status")
		return pkg.Pointer("failure")
	}
}

func (c *Client) CommitStatus(ctx context.Context, repo *repo.Repo, status pkg.CommitState) error {
	log.Info().Str("repo", repo.Name).Str("sha", repo.SHA).Str("status", status.String()).Msg("setting Github commit status")
	repoStatus, _, err := c.Repositories.CreateStatus(ctx, repo.Owner, repo.Name, repo.SHA, &github.RepoStatus{
		State:       toGithubCommitStatus(status),
		Description: pkg.Pointer(status.BareString()),
		ID:          pkg.Pointer(int64(repo.CheckID)),
		Context:     pkg.Pointer("kubechecks"),
	})
	if err != nil {
		log.Err(err).Msg("could not set Github commit status")
		return err
	}
	log.Debug().Interface("status", repoStatus).Msg("Github commit status set")
	return nil
}

func parseRepo(cloneUrl string) (string, string) {
	if strings.HasPrefix(cloneUrl, "git@") {
		// parse ssh string
		parts := strings.Split(cloneUrl, ":")
		parts = strings.Split(parts[1], "/")
		owner := parts[0]
		repoName := strings.TrimSuffix(parts[1], ".git")
		return owner, repoName
	}

	panic(cloneUrl)
}

func (c *Client) GetHookByUrl(ctx context.Context, ownerAndRepoName, webhookUrl string) (*vcs.WebHookConfig, error) {
	owner, repoName := parseRepo(ownerAndRepoName)
	items, _, err := c.Repositories.ListHooks(ctx, owner, repoName, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list hooks")
	}

	for _, item := range items {
		if item.URL != nil && *item.URL == webhookUrl {
			return &vcs.WebHookConfig{
				Url:    item.GetURL(),
				Events: item.Events, // TODO: translate GH specific event names to VCS agnostic
			}, nil
		}
	}

	return nil, vcs.ErrHookNotFound
}

func (c *Client) CreateHook(ctx context.Context, ownerAndRepoName, webhookUrl, webhookSecret string) error {
	owner, repoName := parseRepo(ownerAndRepoName)
	_, _, err := c.Repositories.CreateHook(ctx, owner, repoName, &github.Hook{
		Active: pkg.Pointer(true),
		Config: map[string]interface{}{
			"content_type": "json",
			"insecure_ssl": "0",
			"secret":       webhookSecret,
			"url":          webhookUrl,
		},
		Events: []string{
			"pull_request",
		},
		Name: pkg.Pointer("web"),
	})
	if err != nil {
		return errors.Wrap(err, "failed to create hook")
	}

	return nil
}

var rePullRequest = regexp.MustCompile(`(.*)/(.*)#(\d+)`)

func (c *Client) LoadHook(ctx context.Context, id string) (*repo.Repo, error) {
	m := rePullRequest.FindStringSubmatch(id)
	if len(m) != 4 {
		return nil, errors.New("must be in format OWNER/REPO#PR")
	}

	ownerName := m[1]
	repoName := m[2]
	prNumber, err := strconv.ParseInt(m[3], 10, 32)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse int")
	}

	repoInfo, _, err := c.Repositories.Get(ctx, ownerName, repoName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get repo")
	}

	pullRequest, _, err := c.PullRequests.Get(ctx, ownerName, repoName, int(prNumber))
	if err != nil {
		return nil, errors.Wrap(err, "failed to get pull request")
	}

	var labels []string
	for _, label := range pullRequest.Labels {
		labels = append(labels, label.GetName())
	}

	var (
		baseRef                    string
		headRef, headSha           string
		login, userName, userEmail string
	)

	if pullRequest.Base != nil {
		baseRef = unPtr(pullRequest.Base.Ref)
		headRef = unPtr(pullRequest.Head.Ref)
	}

	if repoInfo.Owner != nil {
		login = unPtr(repoInfo.Owner.Login)
	} else {
		login = "kubechecks"
	}

	if pullRequest.Head != nil {
		headSha = unPtr(pullRequest.Head.SHA)
	}

	if pullRequest.User != nil {
		userName = unPtr(pullRequest.User.Name)
		userEmail = unPtr(pullRequest.User.Email)
	}

	// these are required for `git merge` later on
	if userName == "" {
		userName = "kubechecks"
	}
	if userEmail == "" {
		userEmail = "kubechecks@github.com"
	}

	return &repo.Repo{
		BaseRef:       baseRef,
		HeadRef:       headRef,
		DefaultBranch: unPtr(repoInfo.DefaultBranch),
		CloneURL:      unPtr(repoInfo.CloneURL),
		FullName:      repoInfo.GetFullName(),
		Owner:         login,
		Name:          repoInfo.GetName(),
		CheckID:       int(prNumber),
		SHA:           headSha,
		Username:      userName,
		Email:         userEmail,
		Labels:        labels,
	}, nil
}

func unPtr[T interface{ string | int }](ps *T) T {
	if ps == nil {
		var t T
		return t
	}
	return *ps
}