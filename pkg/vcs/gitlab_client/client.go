package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/whilp/git-urls"
	"github.com/xanzy/go-gitlab"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const GitlabTokenHeader = "X-Gitlab-Token"

type Client struct {
	*gitlab.Client

	username, email string
}

var _ vcs.Client = new(Client)

func CreateGitlabClient() (*Client, error) {
	// Initialize the GitLab client with access token
	gitlabToken := viper.GetString("vcs-token")
	if gitlabToken == "" {
		log.Fatal().Msg("gitlab token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(gitlabToken))

	var gitlabOptions []gitlab.ClientOptionFunc

	gitlabUrl := viper.GetString("vcs-base-url")
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

	return &Client{Client: c, username: user.Username, email: user.Email}, nil
}

func (c *Client) getTokenUser() (string, string) {
	user, _, err := c.Users.CurrentUser()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Gitlab token user")
	}

	return user.Username, user.Email
}

func (c *Client) Email() string    { return c.email }
func (c *Client) Username() string { return c.username }
func (c *Client) GetName() string {
	return "gitlab"
}

// Each client has a different way of verifying their payloads; return an err if this isnt valid
func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	// If we have a secret, and the secret doesn't match, return an error
	if secret != "" && secret != r.Header.Get(GitlabTokenHeader) {
		return nil, fmt.Errorf("invalid secret")
	}

	// Else, download the request body for processing and return it

	return io.ReadAll(r.Body)
}

// ParseHook parses and validates a webhook event; return an err if this isn't valid
func (c *Client) ParseHook(r *http.Request, request []byte) (*repo.Repo, error) {
	eventRequest, err := gitlab.ParseHook(gitlab.HookEventType(r), request)
	if err != nil {
		return nil, err
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
			return nil, vcs.ErrInvalidType
		}
	default:
		log.Trace().Msgf("Unhandled Event: %T", event)
		return nil, vcs.ErrInvalidType
	}
	return nil, vcs.ErrInvalidType
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
	webhooks, _, err := c.Client.Projects.ListProjectHooks(pid, nil)
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

	_, _, err = c.Client.Projects.AddProjectHook(pid, &gitlab.AddProjectHookOptions{
		URL:                 pkg.Pointer(webhookUrl),
		MergeRequestsEvents: pkg.Pointer(true),
		Token:               pkg.Pointer(webhookSecret),
	})

	if err != nil {
		return errors.Wrap(err, "failed to create project webhook")
	}

	return nil
}

func (c *Client) LoadHook(ctx context.Context, id string) (*repo.Repo, error) {
	//TODO implement me
	panic("implement me")
}

func (c *Client) buildRepoFromEvent(event *gitlab.MergeEvent) *repo.Repo {
	// Convert all labels from this MR to a string array of label names
	var labels []string
	for _, label := range event.Labels {
		labels = append(labels, label.Name)
	}

	return &repo.Repo{
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
	}
}
