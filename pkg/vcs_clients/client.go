package vcs_clients

import (
	"context"
	"errors"
	"net/http"

	"github.com/zapier/kubechecks/pkg/repo"
)

// Enum for represnting the state of a commit for posting via CommitStatus
type CommitState int

const (
	Pending CommitState = iota
	Running
	Failure
	Success
)

// Return literal string representation of this state for use in the request
func (s CommitState) String() string {
	switch s {
	case Pending:
		return "pending"
	case Running:
		return "running"
	case Failure:
		return "error"
	case Success:
		return "success"
	}
	return "unknown"
}

// Return more informative description message based on the enum state
func (s CommitState) StateToDesc() string {
	switch s {
	case Pending:
		return "pending..."
	case Running:
		return "in progress..."
	case Failure:
		return "failed."
	case Success:
		return "succeeded."
	}
	return "unknown"
}

// Sentinel errors for use in client implementations
var ErrInvalidType = errors.New("invalid event type")

// Clients need to implement this interface to allow CheckEvents to talk to their respective PR etc
type Client interface {
	// Take in project name in form "owner/repo" (ie zapier/kubechecks), the PR/MR id, and the actual message
	PostMessage(context.Context, string, int, string) *Message
	// Update message with new content
	UpdateMessage(context.Context, *Message, string) error
	// Validate webhook secret and return the body; must be called even if no secret
	VerifyHook(*http.Request, string) ([]byte, error)
	// Parse webook payload for valid events
	ParseHook(*http.Request, []byte) (interface{}, error)
	// Handle valid events
	CreateRepo(context.Context, interface{}) (*repo.Repo, error)
	// Set status for a specific commit on the remote VCS
	CommitStatus(context.Context, *repo.Repo, CommitState) error
}
