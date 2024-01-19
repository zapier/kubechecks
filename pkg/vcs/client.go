package vcs

import (
	"context"
	"errors"
	"net/http"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/repo"
)

const (
	DefaultVcsUsername = "kubechecks"
	DefaultVcsEmail    = "kubechecks@zapier.com"
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
	PostMessage(context.Context, *repo.Repo, int, string) *pkg.Message
	// UpdateMessage update a message with new content
	UpdateMessage(context.Context, *pkg.Message, string) error
	// VerifyHook validates a webhook secret and return the body; must be called even if no secret
	VerifyHook(*http.Request, string) ([]byte, error)
	// ParseHook parses webook payload for valid events
	ParseHook(*http.Request, []byte) (*repo.Repo, error)
	// CommitStatus sets a status for a specific commit on the remote VCS
	CommitStatus(context.Context, *repo.Repo, pkg.CommitState) error
	// GetHookByUrl gets a webhook by url
	GetHookByUrl(ctx context.Context, repoName, webhookUrl string) (*WebHookConfig, error)
	// CreateHook creates a webhook that points at kubechecks
	CreateHook(ctx context.Context, repoName, webhookUrl, webhookSecret string) error
	// GetName returns the VCS client name (e.g. "github" or "gitlab")
	GetName() string
	// TidyOutdatedComments either by hiding or deleting them
	TidyOutdatedComments(context.Context, *repo.Repo) error
	// LoadHook creates an EventRequest from the ID of an actual request
	LoadHook(ctx context.Context, repoAndId string) (*repo.Repo, error)

	Username() string
	Email() string
	ToEmoji(pkg.CommitState) string
}
