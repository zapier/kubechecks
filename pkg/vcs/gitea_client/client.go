package gitea_client

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	giturls "github.com/chainguard-dev/git-urls"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"code.gitea.io/sdk/gitea"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

var tracer = otel.Tracer("pkg/vcs/gitea_client")

// Client implements vcs.Client for Gitea.
type Client struct {
	g   *GClient
	cfg config.ServerConfig

	username, email string
}

// CreateGiteaClient creates a new Gitea client using the provided configuration.
func CreateGiteaClient(ctx context.Context, cfg config.ServerConfig) (*Client, error) {
	_, span := tracer.Start(ctx, "CreateGiteaClient")
	defer span.End()

	if cfg.VcsToken == "" {
		return nil, errors.New("gitea token must be set")
	}

	giteaClient, err := gitea.NewClient(
		cfg.VcsBaseUrl,
		gitea.SetToken(cfg.VcsToken),
		gitea.SetContext(ctx),
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create gitea client")
	}

	client := &Client{
		g: &GClient{
			PullRequests: giteaClient,
			Repositories: giteaClient,
			Issues:       giteaClient,
		},
		cfg:      cfg,
		username: cfg.VcsUsername,
		email:    cfg.VcsEmail,
	}

	if client.username == "" || client.email == "" {
		user, _, err := giteaClient.GetMyUserInfo()
		if err == nil {
			if user.UserName != "" && client.username == "" {
				client.username = user.UserName
			}
			if user.Email != "" && client.email == "" {
				client.email = user.Email
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

func (c *Client) Username() string      { return c.username }
func (c *Client) Email() string         { return c.email }
func (c *Client) CloneUsername() string { return c.username }
func (c *Client) GetName() string       { return "gitea" }

func (c *Client) GetAuthHeaders() map[string]string {
	return map[string]string{
		"Authorization": fmt.Sprintf("token %s", c.cfg.VcsToken),
	}
}

func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read request body")
	}

	if secret != "" {
		sig := r.Header.Get("X-Gitea-Signature")
		if sig == "" {
			return nil, errors.New("missing X-Gitea-Signature header")
		}

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expectedSig := hex.EncodeToString(mac.Sum(nil))

		if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
			return nil, errors.New("invalid webhook signature")
		}
	}

	return body, nil
}

// Webhook payload types - the Gitea SDK does not have these defined.

type pullRequestPayload struct {
	Action      string             `json:"action"`
	PullRequest *gitea.PullRequest `json:"pull_request"`
	Repository  *gitea.Repository  `json:"repository"`
}

type issueCommentPayload struct {
	Action     string            `json:"action"`
	Comment    *gitea.Comment    `json:"comment"`
	Issue      *issueInfo        `json:"issue"`
	Repository *gitea.Repository `json:"repository"`
}

type issueInfo struct {
	Number      int64     `json:"number"`
	PullRequest *struct{} `json:"pull_request"`
}

var nilPr vcs.PullRequest

func (c *Client) ParseHook(ctx context.Context, r *http.Request, body []byte) (vcs.PullRequest, error) {
	_, span := tracer.Start(ctx, "ParseHook")
	defer span.End()

	eventType := r.Header.Get("X-Gitea-Event")

	switch eventType {
	case "pull_request":
		var payload pullRequestPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nilPr, errors.Wrap(err, "failed to unmarshal pull request payload")
		}

		switch payload.Action {
		case "opened", "synchronized", "reopened", "edited":
			log.Info().Str("action", payload.Action).Msg("handling Gitea event from PR")
			return c.buildPullRequest(payload.PullRequest, payload.Repository), nil
		default:
			log.Info().Str("action", payload.Action).Msg("ignoring Gitea pull request event due to non-commit based action")
			return nilPr, vcs.ErrInvalidType
		}

	case "issue_comment":
		var payload issueCommentPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nilPr, errors.Wrap(err, "failed to unmarshal issue comment payload")
		}

		if payload.Action != "created" {
			log.Info().Str("action", payload.Action).Msg("ignoring Gitea issue comment due to invalid action")
			return nilPr, vcs.ErrInvalidType
		}

		// Only handle PR comments (issue.pull_request is non-nil for PRs)
		if payload.Issue == nil || payload.Issue.PullRequest == nil {
			log.Info().Msg("ignoring Gitea issue comment: not a pull request")
			return nilPr, vcs.ErrInvalidType
		}

		if payload.Comment == nil || strings.ToLower(payload.Comment.Body) != c.cfg.ReplanCommentMessage {
			log.Info().Msg("ignoring Gitea issue comment: non-matching message")
			return nilPr, vcs.ErrInvalidType
		}

		log.Info().Msgf("Got %s comment, Running again", c.cfg.ReplanCommentMessage)

		owner := payload.Repository.Owner.UserName
		repoName := payload.Repository.Name
		prNumber := payload.Issue.Number

		pr, _, err := c.g.PullRequests.GetPullRequest(owner, repoName, prNumber)
		if err != nil {
			return nilPr, errors.Wrap(err, "failed to get pull request")
		}

		return c.buildPullRequest(pr, payload.Repository), nil

	default:
		log.Error().Str("event", eventType).Msg("invalid event provided to Gitea client")
		return nilPr, vcs.ErrInvalidType
	}
}

func (c *Client) buildPullRequest(pr *gitea.PullRequest, repo *gitea.Repository) vcs.PullRequest {
	var labels []string
	for _, label := range pr.Labels {
		labels = append(labels, label.Name)
	}

	var (
		owner     string
		repoName  string
		fullName  string
		cloneURL  string
		defBranch string
	)

	if repo != nil {
		if repo.Owner != nil {
			owner = repo.Owner.UserName
		}
		repoName = repo.Name
		fullName = repo.FullName
		cloneURL = repo.CloneURL
		defBranch = repo.DefaultBranch
	}

	// Fallback to PR's base repo info if top-level repo is nil
	if owner == "" && pr.Base != nil && pr.Base.Repository != nil {
		if pr.Base.Repository.Owner != nil {
			owner = pr.Base.Repository.Owner.UserName
		}
		repoName = pr.Base.Repository.Name
		fullName = pr.Base.Repository.FullName
		cloneURL = pr.Base.Repository.CloneURL
		defBranch = pr.Base.Repository.DefaultBranch
	}

	var baseRef, headRef, headSHA string
	if pr.Base != nil {
		baseRef = pr.Base.Ref
	}
	if pr.Head != nil {
		headRef = pr.Head.Ref
		headSHA = pr.Head.Sha
	}

	return vcs.PullRequest{
		BaseRef:       baseRef,
		HeadRef:       headRef,
		DefaultBranch: defBranch,
		CloneURL:      cloneURL,
		FullName:      fullName,
		Owner:         owner,
		Name:          repoName,
		CheckID:       int(pr.Index),
		SHA:           headSHA,
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,
		Config:        c.cfg,
	}
}

func toGiteaCommitStatus(state pkg.CommitState) gitea.StatusState {
	switch state {
	case pkg.StateRunning:
		return gitea.StatusPending
	case pkg.StateFailure:
		return gitea.StatusFailure
	case pkg.StateError, pkg.StatePanic:
		return gitea.StatusError
	case pkg.StateSuccess, pkg.StateWarning, pkg.StateNone, pkg.StateSkip:
		return gitea.StatusSuccess
	}

	log.Warn().Str("state", state.BareString()).Msg("failed to convert to a gitea commit status")
	return gitea.StatusFailure
}

func (c *Client) CommitStatus(ctx context.Context, pr vcs.PullRequest, status pkg.CommitState) error {
	_, span := tracer.Start(ctx, "CommitStatus")
	defer span.End()

	log.Info().Str("repo", pr.Name).Str("sha", pr.SHA).Str("status", status.BareString()).Msg("setting Gitea commit status")

	repoStatus, _, err := c.g.Repositories.CreateStatus(pr.Owner, pr.Name, pr.SHA, gitea.CreateStatusOption{
		State:       toGiteaCommitStatus(status),
		Description: status.BareString(),
		Context:     "kubechecks",
	})
	if err != nil {
		log.Err(err).Msg("could not set Gitea commit status")
		return err
	}

	log.Debug().Caller().Interface("status", repoStatus).Msg("Gitea commit status set")
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
	if len(parts) < 2 {
		panic(fmt.Errorf("%s: invalid path, need at least owner/repo", cloneUrl))
	}

	// Take the last two segments to handle sub-folder hosting
	// e.g. "https://example.com/gitea/owner/repo.git" -> owner, repo
	owner := parts[len(parts)-2]
	repoName := strings.TrimSuffix(parts[len(parts)-1], ".git")
	return owner, repoName
}

func (c *Client) GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*vcs.WebHookConfig, error) {
	_, span := tracer.Start(ctx, "GetHookByUrl")
	defer span.End()

	owner, repo := parseRepo(repoName)

	hooks, _, err := c.g.Repositories.ListRepoHooks(owner, repo, gitea.ListHooksOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "failed to list hooks")
	}

	for _, hook := range hooks {
		if hook.Config != nil {
			if hookURL, ok := hook.Config["url"]; ok && hookURL == webhookUrl {
				return &vcs.WebHookConfig{
					Url:    hookURL,
					Events: hook.Events,
				}, nil
			}
		}
	}

	return nil, vcs.ErrHookNotFound
}

func (c *Client) CreateHook(ctx context.Context, repoName, webhookUrl, webhookSecret string) error {
	_, span := tracer.Start(ctx, "CreateHook")
	defer span.End()

	owner, repo := parseRepo(repoName)

	_, _, err := c.g.Repositories.CreateRepoHook(owner, repo, gitea.CreateHookOption{
		Type:   gitea.HookTypeGitea,
		Active: true,
		Config: map[string]string{
			"url":          webhookUrl,
			"content_type": "json",
			"secret":       webhookSecret,
		},
		Events: []string{"pull_request", "issue_comment"},
	})
	if err != nil {
		return errors.Wrap(err, "failed to create hook")
	}

	return nil
}

var rePullRequest = regexp.MustCompile(`(.*)/(.*)#(\d+)`)

func (c *Client) LoadHook(ctx context.Context, id string) (vcs.PullRequest, error) {
	_, span := tracer.Start(ctx, "LoadHook")
	defer span.End()

	m := rePullRequest.FindStringSubmatch(id)
	if len(m) != 4 {
		return nilPr, errors.New("must be in format OWNER/REPO#PR")
	}

	ownerName := m[1]
	repoName := m[2]
	prNumber, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to parse int")
	}

	repoInfo, _, err := c.g.Repositories.GetRepo(ownerName, repoName)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to get repo")
	}

	pr, _, err := c.g.PullRequests.GetPullRequest(ownerName, repoName, prNumber)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to get pull request")
	}

	var labels []string
	for _, label := range pr.Labels {
		labels = append(labels, label.Name)
	}

	var (
		baseRef, headRef, headSHA string
		login                     string
	)

	if pr.Base != nil {
		baseRef = pr.Base.Ref
	}
	if pr.Head != nil {
		headRef = pr.Head.Ref
		headSHA = pr.Head.Sha
	}

	if repoInfo.Owner != nil {
		login = repoInfo.Owner.UserName
	} else {
		login = "kubechecks"
	}

	return vcs.PullRequest{
		BaseRef:       baseRef,
		HeadRef:       headRef,
		DefaultBranch: repoInfo.DefaultBranch,
		CloneURL:      repoInfo.CloneURL,
		FullName:      repoInfo.FullName,
		Owner:         login,
		Name:          repoInfo.Name,
		CheckID:       int(prNumber),
		SHA:           headSHA,
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,
		Config:        c.cfg,
	}, nil
}

func (c *Client) GetPullRequestFiles(ctx context.Context, pr vcs.PullRequest) ([]string, error) {
	_, span := tracer.Start(ctx, "GetPullRequestFiles")
	defer span.End()

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Msg("fetching PR files from Gitea API")

	var allFiles []string
	page := 1

	for {
		files, _, err := c.g.PullRequests.ListPullRequestFiles(pr.Owner, pr.Name, int64(pr.CheckID), gitea.ListPullRequestFilesOptions{
			ListOptions: gitea.ListOptions{
				Page:     page,
				PageSize: 50,
			},
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to list PR files")
		}

		for _, file := range files {
			allFiles = append(allFiles, file.Filename)
		}

		if len(files) < 50 {
			break
		}
		page++
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Int("file_count", len(allFiles)).
		Msg("fetched PR files from Gitea API")

	return allFiles, nil
}

func (c *Client) DownloadArchive(ctx context.Context, pr vcs.PullRequest) (string, error) {
	_, span := tracer.Start(ctx, "DownloadArchive")
	defer span.End()

	baseURL := strings.TrimSuffix(c.cfg.VcsBaseUrl, "/")
	archiveURL := fmt.Sprintf("%s/%s/%s/archive/%s.zip", baseURL, pr.Owner, pr.Name, pr.SHA)

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("pr_number", pr.CheckID).
		Str("sha", pr.SHA).
		Str("archive_url", archiveURL).
		Msg("generated archive URL")

	return archiveURL, nil
}
