package github_client

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	giturls "github.com/chainguard-dev/git-urls"
	"github.com/google/go-github/v74/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel"
	"golang.org/x/oauth2"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/vcs/github_client")

// installationTokenSource fetches a fresh installation token. Implemented by
// *ghinstallation.Transport in production; stubbed in tests to exercise the App auth
// branch of GetAuthHeaders without spinning up a real JWT signer.
type installationTokenSource interface {
	Token(ctx context.Context) (string, error)
}

type Client struct {
	shurcoolClient *githubv4.Client
	googleClient   *GClient
	cfg            config.ServerConfig

	// appTokenSource is set when the client is authenticated as a GitHub App. It is used
	// by GetAuthHeaders to fetch a fresh installation token for archive downloads, since
	// the archive downloader uses a separate http.Client and must attach the auth header
	// itself rather than going through the SDK's authenticated transport.
	appTokenSource installationTokenSource

	// archiveRetry overrides retry parameters for DownloadArchive. Zero value uses defaults.
	archiveRetry retryConfig

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

	githubClient, appTransport, err := createHttpClient(ctx, cfg)
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
	if appTransport != nil {
		client.appTokenSource = appTransport
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

// createHttpClient returns the authenticated http.Client used by the go-github SDK.
// For GitHub App auth it also returns the underlying ghinstallation.Transport so callers
// can fetch installation tokens directly (used by GetAuthHeaders for archive downloads).
// For PAT auth the second return value is nil.
func createHttpClient(ctx context.Context, cfg config.ServerConfig) (*http.Client, *ghinstallation.Transport, error) {
	// Initialize the GitHub client with app key if provided
	if cfg.IsGithubApp() {
		appTransport, err := ghinstallation.New(
			http.DefaultTransport, cfg.GithubAppID, cfg.GithubInstallationID, []byte(cfg.GithubPrivateKey),
		)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to create github app transport")
		}

		return &http.Client{Transport: appTransport}, appTransport, nil
	}

	// Initialize the GitHub client with access token if app key is not provided
	vcsToken := cfg.VcsToken
	if vcsToken != "" {
		log.Debug().Caller().Msgf("Token Length - %d", len(vcsToken))
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: vcsToken},
		)
		return oauth2.NewClient(ctx, ts), nil, nil
	}

	return nil, nil, errors.New("Either GitHub token or GitHub App credentials (App ID, Installation ID, Private Key) must be set")
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

// GetAuthHeaders returns HTTP headers needed for authenticated archive downloads.
// For GitHub App auth it fetches a fresh installation token from the underlying transport;
// for PAT auth it returns the configured token. GitHub accepts both `Bearer <token>` and
// `token <token>`; Bearer is the modern form.
func (c *Client) GetAuthHeaders(ctx context.Context) (map[string]string, error) {
	token := c.cfg.VcsToken
	if c.appTokenSource != nil {
		t, err := c.appTokenSource.Token(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "failed to fetch GitHub App installation token")
		}
		token = t
	}
	if token == "" {
		return nil, errors.New("no GitHub credentials configured for archive download")
	}
	return map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
	}, nil
}

var nilPr vcs.PullRequest

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
		Title:         pullRequest.GetTitle(),
		Description:   pullRequest.GetBody(),

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
