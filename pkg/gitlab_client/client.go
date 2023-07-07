package gitlab_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/xanzy/go-gitlab"
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

type Repo struct {
	mr      *gitlab.MergeEvent
	repoDir string
	remote  string
}

func GetGitlabClient() (*Client, string) {
	once.Do(func() {
		gitlabClient = createGitlabClient()
		gitlabTokenUser, gitlabTokenEmail = gitlabClient.getTokenUser()
	})

	return gitlabClient, gitlabTokenUser
}

func createGitlabClient() *Client {
	// Initialize the GitLab client with access token
	t := viper.GetString("vcs-token")
	if t == "" {
		log.Fatal().Msg("gitlab token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(t))
	c, err := gitlab.NewClient(t)
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

// Each client has a different way of verifying their payloads; return an err if this isnt valid
func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	// If we have a secret, and the secret doesn't match, return an error
	if secret != "" && secret != r.Header.Get(GitlabTokenHeader) {
		return nil, fmt.Errorf("invalid secret")
	}

	// Else, download the request body for processing and return it

	return io.ReadAll(r.Body)
}

// Each client has a different way of discerning their webhook events; return an err if this isnt valid
func (c *Client) ParseHook(r *http.Request, payload []byte) (interface{}, error) {
	return gitlab.ParseHook(gitlab.HookEventType(r), payload)
}

// Takes a valid gitlab webhook event request, and determines if we should process it
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
			return nil, fmt.Errorf("unhandled action %s", event.ObjectAttributes.Action)
		}
	default:
		log.Trace().Msgf("Unhandled Event: %T", event)
		return nil, fmt.Errorf("unhandled Event %T", event)
	}
	return nil, fmt.Errorf("unhandled Event %T", eventRequest)
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
		OwnerName:     event.Project.PathWithNamespace,
		CloneURL:      event.Project.GitHTTPURL,
		Name:          event.Project.Name,
		CheckID:       event.ObjectAttributes.IID,
		SHA:           event.ObjectAttributes.LastCommit.ID,
		Username:      gitlabTokenUser,
		Email:         gitlabTokenEmail,
		Labels:        labels,
	}
}
