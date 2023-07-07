package github_client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-github/github"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/pkg/vcs_clients"
	"golang.org/x/oauth2"
)

var githubClient *Client
var githubTokenUser string
var once sync.Once // used to ensure we don't reauth this

type Client struct {
	*github.Client
}

func GetGithubClient() (*Client, string) {
	once.Do(func() {
		githubClient = createGithubClient()
		githubTokenUser = getTokenUser()
	})
	return githubClient, githubTokenUser
}

// We require a username to use with git locally, so get the current auth'd user
func getTokenUser() string {
	user, _, err := githubClient.Users.Get(context.Background(), "")
	if err != nil {
		if err != nil {
			log.Fatal().Err(err).Msg("could not create Github token user")
		}
	}
	return *user.Name
}

// Create a new github client using the auth token provided. We
// can't validate the token at this point, so if it exists we assume it works
func createGithubClient() *Client {
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
	c := github.NewClient(tc) // If this has failed, we'll catch it on first call

	return &Client{c}
}

func (c *Client) VerifyHook(r *http.Request, secret string) ([]byte, error) {
	// Github provides the SHA256 of the secret + payload body, so we extract the body and compare
	// We have to split it like this as the ValidatePayload method consumes the request
	if secret != "" {
		return github.ValidatePayload(r, []byte(secret))
	} else {
		// No secret provided, so we just grab the body
		return io.ReadAll(r.Body)
	}
}

func (c *Client) ParseHook(r *http.Request, payload []byte) (interface{}, error) {
	return github.ParseWebHook(github.WebHookType(r), payload)
}

// Creates a new generic repo from the webhook payload. Assumes the secret validation/type validation
// Has already occured previously, so we expect a valid event type for the Github client in the payload arg
func (c *Client) CreateRepo(ctx context.Context, payload interface{}) (*repo.Repo, error) {
	switch p := payload.(type) {
	case *github.PullRequestEvent:
		switch p.GetAction() {
		case "opened", "synchronize", "reopened":
			log.Info().Str("action", p.GetAction()).Msg("handling Github open, sync event from PR")
			return buildRepoFromEvent(p), nil
		default:
			log.Info().Str("action", p.GetAction()).Msg("ignoring Github pull request event due to non commit based action")
			return nil, vcs_clients.ErrInvalidType
		}
	default:
		log.Error().Msg("invalid event provided to Github client")
		return nil, vcs_clients.ErrInvalidType
	}
}

// We need an email and username for authenticating our local git repository
// Grab the current authenticated client login and email
func (c *Client) getUserDetails() (string, string, error) {
	user, _, err := c.Users.Get(context.Background(), "")
	if err != nil {
		return "", "", err
	}

	// Some users on Github don't have an email listed; if so, catch that and return empty string
	if user.Email == nil {
		log.Error().Msg("could not load Github user email")
		return *user.Login, "", nil
	}

	return *user.Login, *user.Email, nil

}

func buildRepoFromEvent(event *github.PullRequestEvent) *repo.Repo {
	username, email, err := githubClient.getUserDetails()
	if err != nil {
		log.Fatal().Err(err).Msg("could not load Github user details")
		username = ""
		email = ""
	}

	var labels []string
	for _, label := range event.PullRequest.Labels {
		labels = append(labels, label.GetName())
	}

	return &repo.Repo{
		BaseRef:       *event.PullRequest.Base.Ref,
		HeadRef:       *event.PullRequest.Head.Ref,
		DefaultBranch: *event.Repo.DefaultBranch,
		CloneURL:      *event.Repo.CloneURL,
		OwnerName:     event.Repo.GetFullName(),
		Name:          event.Repo.GetName(),
		CheckID:       int(*event.PullRequest.Number),
		SHA:           *event.PullRequest.Head.SHA,
		Username:      username,
		Email:         email,
		Labels:        labels,
	}
}

func (c *Client) CommitStatus(ctx context.Context, repo *repo.Repo, status vcs_clients.CommitState) error {
	log.Info().Str("repo", repo.Name).Str("sha", repo.SHA).Str("status", status.String()).Msg("setting Github commit status")
	repoNameComponents := strings.Split(repo.OwnerName, "/")
	repoStatus, _, err := c.Repositories.CreateStatus(ctx, repoNameComponents[0], repoNameComponents[1], repo.SHA, &github.RepoStatus{
		State:       github.String(status.String()),
		Description: github.String(status.StateToDesc()),
		ID:          github.Int64(int64(repo.CheckID)),
		Context:     github.String("kubechecks"),
	})
	if err != nil {
		log.Err(err).Msg("could not set Github commit status")
		return err
	}
	log.Debug().Interface("status", repoStatus).Msg("Github commit status set")
	return nil
}
