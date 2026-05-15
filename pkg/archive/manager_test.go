package archive

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func newTestManager(_ *testing.T, mockClient *vcsmocks.MockClient) *Manager {
	return &Manager{
		vcsClient: mockClient,
		cfg:       config.ServerConfig{ReplanCommentMessage: "kubechecks replan"},
	}
}

func TestPostArchiveErrorMessage(t *testing.T) {
	pr := vcs.PullRequest{FullName: "org/repo", CheckID: 42, BaseRef: "main"}

	tests := []struct {
		name            string
		ctx             func() context.Context
		cloneErr        error
		wantMsgContains []string
	}{
		{
			name: "context timeout",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			cloneErr:        fmt.Errorf("deadline exceeded"),
			wantMsgContains: []string{"timed out", "kubechecks replan"},
		},
		{
			name:            "HTTP 404 not found",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusNotFound},
			wantMsgContains: []string{"404", "kubechecks replan"},
		},
		{
			name:            "HTTP 429 rate limited",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusTooManyRequests},
			wantMsgContains: []string{"429", "kubechecks replan"},
		},
		{
			name:            "HTTP 503 server error",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusServiceUnavailable},
			wantMsgContains: []string{"503", "Service Unavailable", "kubechecks replan"},
		},
		{
			name:            "HTTP 403 other 4xx",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusForbidden},
			wantMsgContains: []string{"403", "Forbidden", "kubechecks replan"},
		},
		{
			name:            "wrapped HTTP 502",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        errors.Wrap(&HTTPError{StatusCode: http.StatusBadGateway}, "failed to download"),
			wantMsgContains: []string{"502", "kubechecks replan"},
		},
		{
			name:            "generic network error",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        fmt.Errorf("connection refused"),
			wantMsgContains: []string{"transient error", "kubechecks replan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := vcsmocks.NewMockClient(t)
			mockClient.On("PostMessage", mock.Anything, pr, mock.AnythingOfType("string")).
				Return(nil, nil)

			m := newTestManager(t, mockClient)
			ctx := tt.ctx()

			err := m.PostArchiveErrorMessage(ctx, pr, tt.cloneErr)
			assert.NoError(t, err)

			// Capture the posted message and assert it contains expected substrings
			calls := mockClient.Calls
			assert.Len(t, calls, 1, "expected exactly one PostMessage call")
			postedMsg := calls[0].Arguments.String(2)
			for _, want := range tt.wantMsgContains {
				assert.Contains(t, postedMsg, want)
			}
		})
	}

	t.Run("PostMessage failure propagates error", func(t *testing.T) {
		mockClient := vcsmocks.NewMockClient(t)
		mockClient.On("PostMessage", mock.Anything, pr, mock.AnythingOfType("string")).
			Return(nil, fmt.Errorf("vcs unavailable"))

		m := newTestManager(t, mockClient)
		err := m.PostArchiveErrorMessage(context.Background(), pr, fmt.Errorf("network error"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vcs unavailable")
	})
}
