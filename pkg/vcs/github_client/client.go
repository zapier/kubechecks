package github_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/chainguard-dev/git-urls"
	"github.com/google/go-github/v62/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/vcs/github_client")

type Client struct {
	shurcoolClient *githubv4.Client
	googleClient   *GClient
	cfg            config.ServerConfig

	username, email string
}

// GClient is a struct that holds the services for the GitHub client
type GClient struct {
	PullRequests PullRequestsServices
	Repositories RepositoriesServices
	Issues       IssuesServices
}

// CreateGithubClient creates a new GitHub client using the auth token provided. We
// can't validate the token at this point, so if it exists we assume it works
func CreateGithubClient(cfg config.ServerConfig) (*Client, error) {
	var (
		err            error
		googleClient   *github.Client
		shurcoolClient *githubv4.Client
	)

	// Initialize the GitLab client with access token
	t := cfg.VcsToken
	if t == "" {
		log.Fatal().Msg("github token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(t))
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: t},
	)
	tc := oauth2.NewClient(ctx, ts)

	githubUrl := cfg.VcsBaseUrl
	githubUploadUrl := cfg.VcsUploadUrl
	// we need both urls to be set for github enterprise
	if githubUrl == "" || githubUploadUrl == "" {
		googleClient = github.NewClient(tc) // If this has failed, we'll catch it on first call

		// Use the client from shurcooL's githubv4 library for queries.
		shurcoolClient = githubv4.NewClient(tc)
	} else {
		googleClient, err = github.NewClient(tc).WithEnterpriseURLs(githubUrl, githubUploadUrl)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create github enterprise client")
		}
		shurcoolClient = githubv4.NewEnterpriseClient(githubUrl, tc)
	}

	user, _, err := googleClient.Users.Get(ctx, "")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get user")
	}

	client := &Client{
		cfg: cfg,
		googleClient: &GClient{
			PullRequests: PullRequestsService{googleClient.PullRequests},
			Repositories: RepositoriesService{googleClient.Repositories},
			Issues:       IssuesService{googleClient.Issues},
		},
		shurcoolClient: shurcoolClient,
	}
	if user != nil {
		if user.Login != nil {
			client.username = *user.Login
		}
		if user.Email != nil {
			client.email = *user.Email
		}
	}

	if client.username == "" {
		client.username = vcs.DefaultVcsUsername
	}
	if client.email == "" {
		client.email = vcs.DefaultVcsEmail
	}
	return client, nil
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

var nilPr vcs.PullRequest

func (c *Client) ParseHook(r *http.Request, request []byte) (vcs.PullRequest, error) {
	payload, err := github.ParseWebHook(github.WebHookType(r), request)
	if err != nil {
		return nilPr, err
	}

	switch p := payload.(type) {
	case *github.PullRequestEvent:
		switch p.GetAction() {
		case "opened", "synchronize", "reopened", "edited":
			log.Info().Str("action", p.GetAction()).Msg("handling Github event from PR")
			return c.buildRepoFromEvent(p), nil
		default:
			log.Info().Str("action", p.GetAction()).Msg("ignoring Github pull request event due to non commit based action")
			return nilPr, vcs.ErrInvalidType
		}
	default:
		log.Error().Msg("invalid event provided to Github client")
		return nilPr, vcs.ErrInvalidType
	}
}

func (c *Client) buildRepoFromEvent(event *github.PullRequestEvent) vcs.PullRequest {
	var labels []string
	for _, label := range event.PullRequest.Labels {
		labels = append(labels, label.GetName())
	}

	return vcs.PullRequest{
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

		Config: c.cfg,
	}
}

func toGithubCommitStatus(state pkg.CommitState) *string {
	switch state {
	case pkg.StateError, pkg.StatePanic:
		return pkg.Pointer("error")
	case pkg.StateFailure:
		return pkg.Pointer("failure")
	case pkg.StateRunning:
		return pkg.Pointer("pending")
	case pkg.StateSuccess, pkg.StateWarning, pkg.StateNone:
		return pkg.Pointer("success")
	}

	log.Warn().Str("state", state.BareString()).Msg("failed to convert to a github commit status")
	return pkg.Pointer("failure")
}

func (c *Client) CommitStatus(ctx context.Context, pr vcs.PullRequest, status pkg.CommitState) error {
	log.Info().Str("repo", pr.Name).Str("sha", pr.SHA).Str("status", status.BareString()).Msg("setting Github commit status")
	repoStatus, _, err := c.googleClient.Repositories.CreateStatus(ctx, pr.Owner, pr.Name, pr.SHA, &github.RepoStatus{
		State:       toGithubCommitStatus(status),
		Description: pkg.Pointer(status.BareString()),
		ID:          pkg.Pointer(int64(pr.CheckID)),
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
	result, err := giturls.Parse(cloneUrl)
	if err != nil {
		panic(fmt.Errorf("%s: %s", cloneUrl, err.Error()))
	}

	path := result.Path
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		panic(fmt.Errorf("%s: invalid path", cloneUrl))
	}

	owner := parts[0]
	repoName := strings.TrimSuffix(parts[1], ".git")
	return owner, repoName
}

func (c *Client) GetHookByUrl(ctx context.Context, ownerAndRepoName, webhookUrl string) (*vcs.WebHookConfig, error) {
	owner, repoName := parseRepo(ownerAndRepoName)
	items, _, err := c.googleClient.Repositories.ListHooks(ctx, owner, repoName, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list hooks")
	}

	for _, item := range items {
		itemConfig := item.GetConfig()
		// check if the hook's config has a URL
		hookPayloadURL := ""
		if itemConfig != nil {
			hookPayloadURL = itemConfig.GetURL()
		}
		if hookPayloadURL == webhookUrl {
			return &vcs.WebHookConfig{
				Url:    hookPayloadURL,
				Events: item.Events, // TODO: translate GH specific event names to VCS agnostic
			}, nil
		}
	}

	return nil, vcs.ErrHookNotFound
}

func (c *Client) CreateHook(ctx context.Context, ownerAndRepoName, webhookUrl, webhookSecret string) error {
	owner, repoName := parseRepo(ownerAndRepoName)
	_, resp, err := c.googleClient.Repositories.CreateHook(ctx, owner, repoName, &github.Hook{
		Active: pkg.Pointer(true),
		Config: &github.HookConfig{
			ContentType: pkg.Pointer("json"),
			InsecureSSL: pkg.Pointer("0"),
			URL:         pkg.Pointer(webhookUrl),
			Secret:      pkg.Pointer(webhookSecret),
		},
		Events: []string{
			"pull_request",
		},
		Name: pkg.Pointer("web"),
	})
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		return errors.Wrap(err, fmt.Sprintf("failed to create hook, statuscode: %d", statusCode))
	}
	return nil
}

var rePullRequest = regexp.MustCompile(`(.*)/(.*)#(\d+)`)

func (c *Client) LoadHook(ctx context.Context, id string) (vcs.PullRequest, error) {
	m := rePullRequest.FindStringSubmatch(id)
	if len(m) != 4 {
		return nilPr, errors.New("must be in format OWNER/REPO#PR")
	}

	ownerName := m[1]
	repoName := m[2]
	prNumber, err := strconv.ParseInt(m[3], 10, 32)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to parse int")
	}

	repoInfo, _, err := c.googleClient.Repositories.Get(ctx, ownerName, repoName)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to get repo")
	}

	pullRequest, _, err := c.googleClient.PullRequests.Get(ctx, ownerName, repoName, int(prNumber))
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to get pull request")
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

	return vcs.PullRequest{
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

		Config: c.cfg,
	}, nil
}

func unPtr[T interface{ string | int }](ps *T) T {
	if ps == nil {
		var t T
		return t
	}
	return *ps
}
