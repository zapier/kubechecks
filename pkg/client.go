package pkg

import (
	"context"
	"errors"
	"net/http"

	"github.com/zapier/kubechecks/pkg/repo"
)

var (
	// ErrInvalidType is a sentinel error for use in client implementations
	ErrInvalidType  = errors.New("invalid event type")
	ErrHookNotFound = errors.New("webhook not found")
)

type WebHookConfig struct {
	Url       string
	SecretKey string
	Events    []string
}

// Client represents a VCS client
type Client interface {
	// PostMessage takes in project name in form "owner/repo" (ie zapier/kubechecks), the PR/MR id, and the actual message
	PostMessage(context.Context, *repo.Repo, int, string) *Message
	// UpdateMessage update a message with new content
	UpdateMessage(context.Context, *Message, string) error
	// VerifyHook validates a webhook secret and return the body; must be called even if no secret
	VerifyHook(*http.Request, string) ([]byte, error)
	// ParseHook parses webook payload for valid events
	ParseHook(*http.Request, []byte) (interface{}, error)
	// CreateRepo handles valid events
	CreateRepo(context.Context, interface{}) (*repo.Repo, error)
	// CommitStatus sets a status for a specific commit on the remote VCS
	CommitStatus(context.Context, *repo.Repo, CommitState) error
	// GetHookByUrl gets a webhook by url
	GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*WebHookConfig, error)
	// CreateHook creates a webhook that points at kubechecks
	CreateHook(ctx context.Context, repoName, webhookUrl, webhookSecret string) error
	// GetName returns the VCS client name (e.g. "github" or "gitlab")
	GetName() string
	// Tidy outdated comments either by hiding or deleting them
	TidyOutdatedComments(context.Context, *repo.Repo) error
}
