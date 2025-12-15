package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	giturls "github.com/chainguard-dev/git-urls"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const GitlabTokenHeader = "X-Gitlab-Token"

type Client struct {
	c   *GLClient
	cfg config.ServerConfig

	username, email string
}

type GLClient struct {
	MergeRequests   MergeRequestsServices
	RepositoryFiles RepositoryFilesServices
	Notes           NotesServices
	Pipelines       PipelinesServices
	Projects        ProjectsServices
	Commits         CommitsServices
}

var ErrNoToken = errors.New("gitlab token needs to be set")

func CreateGitlabClient(ctx context.Context, cfg config.ServerConfig) (*Client, error) {
	_, span := tracer.Start(ctx, "CreateGitlabClient")
	defer span.End()

	// Initialize the GitLab client with access token
	gitlabToken := cfg.VcsToken
	if gitlabToken == "" {
		return nil, ErrNoToken
	}
	log.Debug().Caller().Msgf("Token Length - %d", len(gitlabToken))

	var gitlabOptions []gitlab.ClientOptionFunc

	gitlabUrl := cfg.VcsBaseUrl
	if gitlabUrl != "" {
		gitlabOptions = append(gitlabOptions, gitlab.WithBaseURL(gitlabUrl))
	}

	c, err := gitlab.NewClient(gitlabToken, gitlabOptions...)
	if err != nil {
		return nil, errors.Wrapf(err, "could not create Gitlab client")
	}

	user, _, err := c.Users.CurrentUser()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get current user")
	}

	client := &Client{
		c: &GLClient{
			MergeRequests:   &MergeRequestsService{c.MergeRequests},
			RepositoryFiles: &RepositoryFilesService{c.RepositoryFiles},
			Notes:           &NotesService{c.Notes},
			Projects:        &ProjectsService{c.Projects},
			Commits:         &CommitsService{c.Commits},
			Pipelines:       &PipelinesService{c.Pipelines},
		},
		cfg:      cfg,
		username: user.Username,
		email:    user.Email,
	}
	if client.username == "" {
		client.username = vcs.DefaultVcsUsername
	}
	if client.email == "" {
		client.email = vcs.DefaultVcsEmail
	}

	return client, nil
}

func (c *Client) Email() string         { return c.email }
func (c *Client) Username() string      { return c.username }
func (c *Client) CloneUsername() string { return c.username }
func (c *Client) GetName() string       { return "gitlab" }

// GetAuthHeaders returns HTTP headers needed for authenticated archive downloads
func (c *Client) GetAuthHeaders() map[string]string {
	// GitLab uses PRIVATE-TOKEN header for authentication
	return map[string]string{
		"PRIVATE-TOKEN": c.cfg.VcsToken,
	}
}

// VerifyHook returns an err if the webhook isn't valid
func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	// If we have a secret, and the secret doesn't match, return an error
	if secret != "" && secret != r.Header.Get(GitlabTokenHeader) {
		return nil, fmt.Errorf("invalid secret")
	}

	// Else, download the request body for processing and return it

	return io.ReadAll(r.Body)
}

var nilPr vcs.PullRequest

// ParseHook parses and validates a webhook event; return an err if this isn't valid
func (c *Client) ParseHook(_ context.Context, r *http.Request, request []byte) (vcs.PullRequest, error) {
	eventRequest, err := gitlab.ParseHook(gitlab.HookEventType(r), request)
	if err != nil {
		return nilPr, err
	}

	switch event := eventRequest.(type) {
	case *gitlab.MergeEvent:
		switch event.ObjectAttributes.Action {
		case "update":
			if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
				return c.buildRepoFromEvent(event), nil
			}
			log.Trace().Msgf("Skipping update event sha didn't change")
		case "open", "reopen":
			return c.buildRepoFromEvent(event), nil
		default:
			log.Trace().Msgf("Unhandled Action %s", event.ObjectAttributes.Action)
			return nilPr, vcs.ErrInvalidType
		}
	case *gitlab.MergeCommentEvent:
		switch event.ObjectAttributes.Action {
		case gitlab.CommentEventActionCreate:
			if strings.ToLower(event.ObjectAttributes.Note) == c.cfg.ReplanCommentMessage {
				log.Info().Msgf("Got %s comment, Running again", c.cfg.ReplanCommentMessage)
				return c.buildRepoFromComment(event), nil
			} else {
				log.Info().Msg("ignoring Gitlab merge comment event due to non matching string")
				return nilPr, vcs.ErrInvalidType
			}
		default:
			log.Info().Msg("ignoring Gitlab issue comment event due to non matching string")
			return nilPr, vcs.ErrInvalidType
		}
	default:
		log.Trace().Msgf("Unhandled Event: %T", event)
		return nilPr, vcs.ErrInvalidType
	}
	return nilPr, vcs.ErrInvalidType
}

func parseRepoName(url string) (string, error) {
	parsed, err := giturls.Parse(url)
	if err != nil {
		return "", err
	}

	path := parsed.Path
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimPrefix(path, "/")
	return path, nil
}

func (c *Client) GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*vcs.WebHookConfig, error) {
	pid, err := parseRepoName(repoName)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse repo url")
	}
	webhooks, _, err := c.c.Projects.ListProjectHooks(pid, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, errors.Wrap(err, "failed to list project webhooks")
	}

	for _, hook := range webhooks {
		if hook.URL == webhookUrl {
			var events []string
			// TODO: translate GL specific event names to VCS agnostic
			if hook.MergeRequestsEvents {
				events = append(events, string(gitlab.MergeRequestEventTargetType))
			}
			if hook.NoteEvents {
				events = append(events, string(gitlab.NoteEventTargetType))
			}

			return &vcs.WebHookConfig{
				Url:    hook.URL,
				Events: events,
			}, nil
		}
	}

	return nil, vcs.ErrHookNotFound
}

func (c *Client) CreateHook(ctx context.Context, repoName, webhookUrl, webhookSecret string) error {
	pid, err := parseRepoName(repoName)
	if err != nil {
		return errors.Wrap(err, "failed to parse repo name")
	}

	_, glStatus, err := c.c.Projects.AddProjectHook(pid, &gitlab.AddProjectHookOptions{
		URL:                 pkg.Pointer(webhookUrl),
		MergeRequestsEvents: pkg.Pointer(true),
		NoteEvents:          pkg.Pointer(true),
		Token:               pkg.Pointer(webhookSecret),
	}, gitlab.WithContext(ctx))

	if err != nil && glStatus.StatusCode < http.StatusOK || glStatus.StatusCode >= http.StatusMultipleChoices {
		return errors.Wrap(err, "failed to create project webhook")
	}

	return nil
}

var reMergeRequest = regexp.MustCompile(`(.*)!(\d+)`)

func (c *Client) LoadHook(ctx context.Context, id string) (vcs.PullRequest, error) {
	m := reMergeRequest.FindStringSubmatch(id)
	if len(m) != 3 {
		return nilPr, errors.New("must be in format REPOPATH!MR")
	}

	repoPath := m[1]
	mrNumber, err := strconv.ParseInt(m[2], 10, 32)
	if err != nil {
		return nilPr, errors.Wrap(err, "failed to parse merge request number")
	}

	project, _, err := c.c.Projects.GetProject(repoPath, nil)
	if err != nil {
		return nilPr, errors.Wrapf(err, "failed to get project '%s'", repoPath)
	}

	mergeRequest, _, err := c.c.MergeRequests.GetMergeRequest(repoPath, int(mrNumber), nil, gitlab.WithContext(ctx))
	if err != nil {
		return nilPr, errors.Wrapf(err, "failed to get merge request '%d' in project '%s'", mrNumber, repoPath)
	}

	return vcs.PullRequest{
		BaseRef:       mergeRequest.TargetBranch,
		HeadRef:       mergeRequest.SourceBranch,
		DefaultBranch: project.DefaultBranch,
		Remote:        "",
		CloneURL:      project.HTTPURLToRepo,
		Name:          project.Name,
		Owner:         "",
		CheckID:       mergeRequest.IID,
		SHA:           mergeRequest.SHA,
		FullName:      project.PathWithNamespace,
		Username:      c.username,
		Email:         c.email,
		Labels:        mergeRequest.Labels,

		Config: c.cfg,
	}, nil
}

func (c *Client) buildRepoFromEvent(event *gitlab.MergeEvent) vcs.PullRequest {
	// Convert all labels from this MR to a string array of label names
	var labels []string
	for _, label := range event.Labels {
		labels = append(labels, label.Title)
	}

	return vcs.PullRequest{
		BaseRef:       event.ObjectAttributes.TargetBranch,
		HeadRef:       event.ObjectAttributes.SourceBranch,
		DefaultBranch: event.Project.DefaultBranch,
		FullName:      event.Project.PathWithNamespace,
		CloneURL:      event.Project.GitHTTPURL,
		Name:          event.Project.Name,
		CheckID:       event.ObjectAttributes.IID,
		SHA:           event.ObjectAttributes.LastCommit.ID,
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,

		Config: c.cfg,
	}
}

func (c *Client) buildRepoFromComment(event *gitlab.MergeCommentEvent) vcs.PullRequest {
	// Convert all labels from this MR to a string array of label names
	var labels []string
	for _, label := range event.MergeRequest.Labels {
		labels = append(labels, label.Title)
	}
	return vcs.PullRequest{
		BaseRef:       event.MergeRequest.TargetBranch,
		HeadRef:       event.MergeRequest.SourceBranch,
		DefaultBranch: event.Project.DefaultBranch,
		FullName:      event.Project.PathWithNamespace,
		CloneURL:      event.Project.GitHTTPURL,
		Name:          event.Project.Name,
		CheckID:       event.MergeRequest.IID,
		SHA:           event.MergeRequest.LastCommit.ID,
		Username:      c.username,
		Email:         c.email,
		Labels:        labels,

		Config: c.cfg,
	}
}

// GetPullRequestFiles returns the list of files changed in a merge request
func (c *Client) GetPullRequestFiles(ctx context.Context, pr vcs.PullRequest) ([]string, error) {
	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("mr_number", pr.CheckID).
		Msg("fetching MR files from GitLab API")

	// List all diffs for the merge request
	diffs, _, err := c.c.MergeRequests.ListMergeRequestDiffs(pr.FullName, pr.CheckID, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, errors.Wrap(err, "failed to list MR diffs from GitLab")
	}

	// Extract file paths from diffs
	var allFiles []string
	filesSeen := make(map[string]bool) // Deduplicate files

	for _, diff := range diffs {
		// Use NewPath for added/modified files, OldPath for deleted files
		filePath := diff.NewPath
		if filePath == "" || filePath == "/dev/null" {
			filePath = diff.OldPath
		}
		if filePath != "" && filePath != "/dev/null" && !filesSeen[filePath] {
			allFiles = append(allFiles, filePath)
			filesSeen[filePath] = true
		}
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("mr_number", pr.CheckID).
		Int("file_count", len(allFiles)).
		Msg("fetched MR files from GitLab API")

	return allFiles, nil
}

// DownloadArchive returns the archive URL for downloading a repository at a specific commit
func (c *Client) DownloadArchive(ctx context.Context, pr vcs.PullRequest) (string, error) {
	// Retry configuration for waiting on GitLab to check merge status
	const (
		maxRetries     = 5
		initialBackoff = 500 * time.Millisecond
		maxBackoff     = 8 * time.Second
	)

	var mr *gitlab.MergeRequest
	var err error
	backoff := initialBackoff

	// Retry loop: GitLab needs time to check merge status after MR creation/update
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Get merge request details
		mr, _, err = c.c.MergeRequests.GetMergeRequest(pr.FullName, pr.CheckID, nil, gitlab.WithContext(ctx))
		if err != nil {
			return "", errors.Wrap(err, "failed to get MR details from GitLab")
		}

		// Check if MR has conflicts
		if mr.HasConflicts {
			return "", errors.New("MR has conflicts and cannot be merged")
		}

		// Check DetailedMergeStatus
		// Reference: https://docs.gitlab.com/api/merge_requests/#merge-status
		status := mr.DetailedMergeStatus

		// Success: MR is ready to be merged
		// These statuses mean GitLab has finished checking and MR can proceed
		successStatuses := []string{
			"",          // Empty means ready (legacy behavior)
			"mergeable", // The branch can merge cleanly
		}
		if containsStatus(successStatuses, status) {
			log.Debug().
				Caller().
				Str("repo", pr.FullName).
				Int("mr_number", pr.CheckID).
				Str("merge_status", status).
				Msg("MR merge status is ready")
			break
		}

		// Transient statuses: GitLab is still processing, need to retry
		// These are temporary states that will change as GitLab processes the MR
		transientStatuses := []string{
			"unchecked",         // Git has not yet tested if valid merge is possible
			"checking",          // Git is testing if valid merge is possible
			"approvals_syncing", // MR approvals are syncing
			"preparing",         // MR diff is being created
			"ci_still_running",  // CI/CD pipeline is still running
		}
		if containsStatus(transientStatuses, status) {
			// If this is the last attempt, fail
			if attempt == maxRetries {
				log.Warn().
					Caller().
					Str("repo", pr.FullName).
					Int("mr_number", pr.CheckID).
					Int("attempts", attempt+1).
					Str("status", status).
					Msg("MR merge status still transient after retries")
				return "", fmt.Errorf("MR merge status not ready after retries (status: %s, waited ~%v)", status, time.Duration(attempt+1)*initialBackoff)
			}

			// Wait before retrying (exponential backoff)
			log.Debug().
				Caller().
				Str("repo", pr.FullName).
				Int("mr_number", pr.CheckID).
				Int("attempt", attempt+1).
				Dur("backoff", backoff).
				Str("status", status).
				Msg("MR merge status still processing, retrying...")

			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
				// Exponential backoff with cap
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}

		// Permanent failure statuses: cannot be merged, must be fixed by user
		// All other statuses indicate conditions that require user intervention
		return "", fmt.Errorf("MR cannot be merged (status: %s)", status)
	}

	// Use GitLab's special ref for preview merges: refs/merge-requests/<iid>/merge
	// This gives us the merged state without actually merging the MR
	// Note: merge_commit_sha is null until MR is actually merged, so we can't use it
	mergeRef := fmt.Sprintf("refs/merge-requests/%d/merge", pr.CheckID)

	// URL-encode the project path (replace / with %2F)
	projectPathEncoded := strings.ReplaceAll(pr.FullName, "/", "%2F")

	// Construct archive URL using GitLab API
	// Format: https://gitlab.com/api/v4/projects/{project_path_encoded}/repository/archive.zip?sha={ref}
	var archiveURL string
	if c.cfg.VcsBaseUrl != "" {
		// Self-hosted GitLab
		baseURL := strings.TrimSuffix(c.cfg.VcsBaseUrl, "/")
		archiveURL = fmt.Sprintf("%s/api/v4/projects/%s/repository/archive.zip?sha=%s",
			baseURL, projectPathEncoded, mergeRef)
	} else {
		// GitLab.com
		archiveURL = fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/archive.zip?sha=%s",
			projectPathEncoded, mergeRef)
	}

	log.Debug().
		Caller().
		Str("repo", pr.FullName).
		Int("mr_number", pr.CheckID).
		Str("merge_ref", mergeRef).
		Str("archive_url", archiveURL).
		Msg("generated archive URL using merge ref")

	return archiveURL, nil
}

// containsStatus checks if a status string is in the list of statuses
func containsStatus(statuses []string, status string) bool {
	for _, s := range statuses {
		if s == status {
			return true
		}
	}
	return false
}
