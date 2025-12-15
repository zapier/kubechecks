package github_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	giturls "github.com/chainguard-dev/git-urls"
	"github.com/google/go-github/v74/github"
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

// CreateGithubClient creates a new GitHub client using the auth token provided
func CreateGithubClient(ctx context.Context, cfg config.ServerConfig) (*Client, error) {
	ctx, span := tracer.Start(ctx, "CreateGithubClient")
	defer span.End()

	var (
		err            error
		googleClient   *github.Client
		shurcoolClient *githubv4.Client
	)

	githubClient, err := createHttpClient(ctx, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create github http client")
	}

	githubUrl := cfg.VcsBaseUrl
	githubUploadUrl := cfg.VcsUploadUrl
	// we need both urls to be set for github enterprise
	if githubUrl == "" || githubUploadUrl == "" {
		googleClient = github.NewClient(githubClient) // If this has failed, we'll catch it on first call

		shurcoolClient = githubv4.NewClient(githubClient)
	} else {
		googleClient, err = github.NewClient(githubClient).WithEnterpriseURLs(githubUrl, githubUploadUrl)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to create github enterprise client")
		}
		shurcoolClient = githubv4.NewEnterpriseClient(githubUrl, githubClient)
	}

	client := &Client{
		cfg: cfg,
		googleClient: &GClient{
			PullRequests: PullRequestsService{googleClient.PullRequests},
			Repositories: RepositoriesService{googleClient.Repositories},
			Issues:       IssuesService{googleClient.Issues},
		},
		shurcoolClient: shurcoolClient,
		username:       cfg.VcsUsername,
		email:          cfg.VcsEmail,
	}

	if client.username == "" || client.email == "" {
		user, _, err := googleClient.Users.Get(ctx, "")
		if err == nil {
			if user.Login != nil {
				client.username = *user.Login
			}

			if user.Email != nil {
				client.email = *user.Email
			}
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

func createHttpClient(ctx context.Context, cfg config.ServerConfig) (*http.Client, error) {
	// Initialize the GitHub client with app key if provided
	if cfg.IsGithubApp() {
		appTransport, err := ghinstallation.New(
			http.DefaultTransport, cfg.GithubAppID, cfg.GithubInstallationID, []byte(cfg.GithubPrivateKey),
		)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create github app transport")
		}

		return &http.Client{Transport: appTransport}, nil
	}

	// Initialize the GitHub client with access token if app key is not provided
	vcsToken := cfg.VcsToken
	if vcsToken != "" {
		log.Debug().Caller().Msgf("Token Length - %d", len(vcsToken))
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: vcsToken},
		)
		return oauth2.NewClient(ctx, ts), nil
	}

	return nil, errors.New("Either GitHub token or GitHub App credentials (App ID, Installation ID, Private Key) must be set")
}

func (c *Client) Username() string { return c.username }
func (c *Client) Email() string    { return c.email }
func (c *Client) GetName() string {
	return "github"
}

func (c *Client) CloneUsername() string {
	if c.cfg.IsGithubApp() {
		return "x-access-token"
	} else {
		return c.username
	}
}

// GetAuthHeaders returns HTTP headers needed for authenticated archive downloads
func (c *Client) GetAuthHeaders() map[string]string {
	// GitHub accepts: Authorization: Bearer <token> or Authorization: token <token>
	// Using Bearer format as it's the modern standard
	return map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", c.cfg.VcsToken),
	}
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

func (c *Client) ParseHook(ctx context.Context, r *http.Request, request []byte) (vcs.PullRequest, error) {
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
	case *github.IssueCommentEvent:
		switch p.GetAction() {
		case "created":
			if strings.ToLower(p.Comment.GetBody()) == c.cfg.ReplanCommentMessage {
				log.Info().Msgf("Got %s comment, Running again", c.cfg.ReplanCommentMessage)
				return c.buildRepoFromComment(ctx, p)
			} else {
				log.Info().Str("action", p.GetAction()).Msg("ignoring Github issue comment event due to non matching string")
				return nilPr, vcs.ErrInvalidType
			}
		default:
			log.Info().Str("action", p.GetAction()).Msg("ignoring Github issue comment due to invalid action")
			return nilPr, vcs.ErrInvalidType
		}
	default:
		log.Error().Msg("invalid event provided to Github client")
		return nilPr, vcs.ErrInvalidType
	}
}

func (c *Client) buildRepo(pullRequest *github.PullRequest) vcs.PullRequest {
	repo := pullRequest.Head.Repo

	var labels []string
	for _, label := range pullRequest.Labels {
		labels = append(labels, label.GetName())
	}

	return vcs.PullRequest{
		BaseRef:       pullRequest.Base.GetRef(),
		HeadRef:       pullRequest.Head.GetRef(),
		DefaultBranch: repo.GetDefaultBranch(),
		CloneURL:      repo.GetCloneURL(),
		FullName:      repo.GetFullName(),
		Owner:         repo.Owner.GetLogin(),
		Name:          repo.GetName(),
		CheckID:       pullRequest.GetNumber(),
		SHA:           pullRequest.Head.GetSHA(),
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,

		Config: c.cfg,
	}
}

func (c *Client) buildRepoFromEvent(event *github.PullRequestEvent) vcs.PullRequest {
	return c.buildRepo(event.PullRequest)
}

// buildRepoFromComment builds a vcs.PullRequest from a github.IssueCommentEvent
func (c *Client) buildRepoFromComment(context context.Context, comment *github.IssueCommentEvent) (vcs.PullRequest, error) {
	owner := comment.GetRepo().GetOwner().GetLogin()
	repoName := comment.GetRepo().GetName()
	prNumber := comment.GetIssue().GetNumber()

	log.Info().Str("owner", owner).Str("repo", repoName).Int("number", prNumber).Msg("getting pr")
	pr, _, err := c.googleClient.PullRequests.Get(context, owner, repoName, prNumber)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to get pull request")
	}

	return c.buildRepo(pr), nil
}

func toGithubCommitStatus(state pkg.CommitState) *string {
	switch state {
	case pkg.StateError, pkg.StatePanic:
		return pkg.Pointer("error")
	case pkg.StateFailure:
		return pkg.Pointer("failure")
	case pkg.StateRunning:
		return pkg.Pointer("pending")
	case pkg.StateSuccess, pkg.StateWarning, pkg.StateNone, pkg.StateSkip:
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
	log.Debug().Caller().Interface("status", repoStatus).Msg("Github commit status set")
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
			"pull_request", "issue_comment",
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

// GetPullRequestFiles returns the list of files changed in a pull request
func (c *Client) GetPullRequestFiles(ctx context.Context, pr vcs.PullRequest) ([]string, error) {
	ctx, span := tracer.Start(ctx, "GetPullRequestFiles")
	defer span.End()

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Msg("fetching PR files from GitHub API")

	// List files changed in the PR
	opts := &github.ListOptions{PerPage: 100}
	var allFiles []string

	for {
		files, resp, err := c.googleClient.PullRequests.ListFiles(ctx, pr.Owner, pr.Name, pr.CheckID, opts)
		if err != nil {
			return nil, errors.Wrap(err, "failed to list PR files")
		}

		for _, file := range files {
			if file.Filename != nil {
				allFiles = append(allFiles, *file.Filename)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Int("file_count", len(allFiles)).
		Msg("fetched PR files from GitHub API")

	return allFiles, nil
}

// DownloadArchive returns the archive URL for downloading a repository at a specific commit
func (c *Client) DownloadArchive(ctx context.Context, pr vcs.PullRequest) (string, error) {
	ctx, span := tracer.Start(ctx, "DownloadArchive")
	defer span.End()

	// Get PR details to find merge_commit_sha
	ghPR, _, err := c.googleClient.PullRequests.Get(ctx, pr.Owner, pr.Name, pr.CheckID)
	if err != nil {
		return "", errors.Wrap(err, "failed to get PR details")
	}

	// Check if PR is mergeable
	if ghPR.MergeCommitSHA == nil || *ghPR.MergeCommitSHA == "" {
		return "", errors.New("PR does not have a merge commit SHA (may have conflicts)")
	}

	if ghPR.Mergeable != nil && !*ghPR.Mergeable {
		return "", errors.New("PR is not mergeable (has conflicts)")
	}

	mergeCommitSHA := *ghPR.MergeCommitSHA

	// Construct archive URL
	// Format: https://github.com/{owner}/{repo}/archive/{sha}.zip
	// Or for enterprise: https://{base_url}/{owner}/{repo}/archive/{sha}.zip
	var archiveURL string
	if c.cfg.VcsBaseUrl != "" {
		// GitHub Enterprise
		baseURL := strings.TrimSuffix(c.cfg.VcsBaseUrl, "/api/v3")
		baseURL = strings.TrimSuffix(baseURL, "/")
		archiveURL = fmt.Sprintf("%s/%s/%s/archive/%s.zip", baseURL, pr.Owner, pr.Name, mergeCommitSHA)
	} else {
		// GitHub.com
		archiveURL = fmt.Sprintf("https://github.com/%s/%s/archive/%s.zip", pr.Owner, pr.Name, mergeCommitSHA)
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Str("merge_commit_sha", mergeCommitSHA).
		Str("archive_url", archiveURL).
		Msg("generated archive URL")

	return archiveURL, nil
}
