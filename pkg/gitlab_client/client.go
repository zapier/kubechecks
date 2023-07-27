package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	giturls "github.com/whilp/git-urls"
	"github.com/xanzy/go-gitlab"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
)

var gitlabClient *Client
var gitlabTokenUser string
var gitlabTokenEmail string
var once sync.Once

const GitlabTokenHeader = "X-Gitlab-Token"

type Client struct {
	*gitlab.Client
}

var _ pkg.Client = new(Client)

func GetGitlabClient() (*Client, string) {
	once.Do(func() {
		gitlabClient = createGitlabClient()
		gitlabTokenUser, gitlabTokenEmail = gitlabClient.getTokenUser()
	})

	return gitlabClient, gitlabTokenUser
}

func createGitlabClient() *Client {
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

	return &Client{c}
}

func (c *Client) getTokenUser() (string, string) {
	user, _, err := c.Users.CurrentUser()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Gitlab token user")
	}

	return user.Username, user.Email
}

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
func (c *Client) ParseHook(r *http.Request, payload []byte) (interface{}, error) {
	return gitlab.ParseHook(gitlab.HookEventType(r), payload)
}

// CreateRepo takes a valid gitlab webhook event request, and determines if we should process it
// Returns a generic Repo with all info kubechecks needs on success, err if not
func (c *Client) CreateRepo(ctx context.Context, eventRequest interface{}) (*repo.Repo, error) {
	switch event := eventRequest.(type) {
	case *gitlab.MergeEvent:
		switch event.ObjectAttributes.Action {
		case "update":
			if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
				return buildRepoFromEvent(event), nil
			}
			log.Trace().Msgf("Skipping update event sha didn't change")
		case "open", "reopen":
			return buildRepoFromEvent(event), nil
		default:
			log.Trace().Msgf("Unhandled Action %s", event.ObjectAttributes.Action)
			return nil, pkg.ErrInvalidType
		}
	default:
		log.Trace().Msgf("Unhandled Event: %T", event)
		return nil, pkg.ErrInvalidType
	}
	return nil, pkg.ErrInvalidType
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

func (c *Client) GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*pkg.WebHookConfig, error) {
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
			return &pkg.WebHookConfig{
				Url:    hook.URL,
				Events: events,
			}, nil
		}
	}

	return nil, pkg.ErrHookNotFound
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

func buildRepoFromEvent(event *gitlab.MergeEvent) *repo.Repo {
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
		Username:      gitlabTokenUser,
		Email:         gitlabTokenEmail,
		Labels:        labels,
	}
}
