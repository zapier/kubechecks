package vcs_clients

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/zapier/kubechecks/pkg/repo"
)

// Clients need to implement this interface to allow CheckEvents to talk to their respective PR etc
type Client interface {
	// Take in project name in form "owner/repo" (ie zapier/kubechecks), the PR/MR id, and the actual message
	PostMessage(context.Context, string, int, string) *Message
	// Update message with new content
	UpdateMessage(context.Context, *Message, string) error
	// Validate webhook secret (if applicable)
	VerifyHook(string, echo.Context) error
	// Parse webook payload for valid events
	ParseHook(*http.Request, []byte) (interface{}, error)
	// Handle valid events
	CreateRepo(context.Context, interface{}) (*repo.Repo, error)
}
