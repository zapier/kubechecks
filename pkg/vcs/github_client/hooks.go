package github_client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v74/github"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

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
		Title:         pullRequest.GetTitle(),
		Description:   pullRequest.GetBody(),

		Config: c.cfg,
	}, nil
}
