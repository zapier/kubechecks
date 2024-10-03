package vcs

import (
	"errors"
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
