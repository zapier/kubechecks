package vcs

import (
	"context"
	"net/http"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
)

type WebHookConfig struct {
	Url       string
	SecretKey string
	Events    []string
}

// Client represents a VCS client
type Client interface {
	// PostMessage takes in project name in form "owner/repo" (ie zapier/kubechecks), the PR/MR id, and the actual message
	PostMessage(context.Context, PullRequest, string) (*msg.Message, error)
	// UpdateMessage update a message with new content
	UpdateMessage(context.Context, PullRequest, *msg.Message, []string) error
	// VerifyHook validates a webhook secret and return the body; must be called even if no secret
	VerifyHook(*http.Request, string) ([]byte, error)
	// ParseHook parses webook payload for valid events, with context for request-scoped values
	ParseHook(context.Context, *http.Request, []byte) (PullRequest, error)
	// CommitStatus sets a status for a specific commit on the remote VCS
	CommitStatus(context.Context, PullRequest, pkg.CommitState) error
	// GetHookByUrl gets a webhook by url
	GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*WebHookConfig, error)
	// CreateHook creates a webhook that points at kubechecks
	CreateHook(ctx context.Context, repoName, webhookUrl, webhookSecret string) error
	// GetName returns the VCS client name (e.g. "github" or "gitlab")
	GetName() string
	// TidyOutdatedComments either by hiding or deleting them
	TidyOutdatedComments(context.Context, PullRequest) error
	// LoadHook creates an EventRequest from the ID of an actual request
	LoadHook(ctx context.Context, repoAndId string) (PullRequest, error)
	// GetMaxCommentLength returns the maximum length of a comment for the VCS
	GetMaxCommentLength() int
	// GetPrLinkTemplate returns the template for the PR link
	GetPrCommentLinkTemplate(PullRequest) string

	Username() string
	CloneUsername() string
	Email() string
	ToEmoji(pkg.CommitState) string
}
