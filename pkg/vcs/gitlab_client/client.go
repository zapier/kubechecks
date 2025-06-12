package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	giturls "github.com/chainguard-dev/git-urls"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"

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

func CreateGitlabClient(cfg config.ServerConfig) (*Client, error) {
	// Initialize the GitLab client with access token
	gitlabToken := cfg.VcsToken
	if gitlabToken == "" {
		log.Fatal().Msg("gitlab token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(gitlabToken))

	var gitlabOptions []gitlab.ClientOptionFunc

	gitlabUrl := cfg.VcsBaseUrl
	if gitlabUrl != "" {
		gitlabOptions = append(gitlabOptions, gitlab.WithBaseURL(gitlabUrl))
	}

	c, err := gitlab.NewClient(gitlabToken, gitlabOptions...)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Gitlab client")
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
func (c *Client) ParseHook(ctx context.Context, r *http.Request, request []byte) (vcs.PullRequest, error) {
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
	webhooks, _, err := c.c.Projects.ListProjectHooks(pid, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list project webhooks")
	}

	for _, hook := range webhooks {
		if hook.URL == webhookUrl {
			events := []string{}
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
	})

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

	mergeRequest, _, err := c.c.MergeRequests.GetMergeRequest(repoPath, int(mrNumber), nil)
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
