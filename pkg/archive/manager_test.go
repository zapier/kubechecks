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

func newTestManager(mockClient *vcsmocks.MockClient) *Manager {
	return &Manager{
		vcsClient: mockClient,
		cfg:       config.ServerConfig{ReplanCommentMessage: "kubechecks replan"},
	}
}

func TestPostArchiveErrorMessage(t *testing.T) {
	pr := vcs.PullRequest{FullName: "org/repo", CheckID: 42, BaseRef: "main"}

	tests := []struct {
		name                    string
		ctx                     func() context.Context
		cloneErr                error
		wantMsgContains         []string
		wantMsgExcludes         []string
		wantNonCancelledPostCtx bool // assert PostMessage receives a non-cancelled context
	}{
		{
			name:            "context.DeadlineExceeded in clone error",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        context.DeadlineExceeded,
			wantMsgContains: []string{"timed out", "kubechecks replan"},
		},
		{
			// Canceled ≠ timeout — should say "interrupted", not "timed out"
			name:            "context.Canceled in clone error",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        context.Canceled,
			wantMsgContains: []string{"interrupted", "kubechecks replan"},
			wantMsgExcludes: []string{"timed out"},
		},
		{
			// HTTP error in cloneErr takes priority even when ctx is also cancelled;
			// PostMessage must receive a fresh (non-cancelled) context.
			name: "HTTP 503 with cancelled ctx — HTTP wins, fresh postCtx",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			cloneErr:                &HTTPError{StatusCode: http.StatusServiceUnavailable},
			wantMsgContains:         []string{"503", "kubechecks replan"},
			wantMsgExcludes:         []string{"timed out", "interrupted"},
			wantNonCancelledPostCtx: true,
		},
		{
			// ctx cancelled with DeadlineExceeded, no typed error — ctx.Err() fallback (timeout)
			name: "cancelled ctx (deadline) with generic error — timeout fallback",
			ctx: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 0)
				cancel()
				return ctx
			},
			cloneErr:                fmt.Errorf("some generic failure"),
			wantMsgContains:         []string{"timed out", "kubechecks replan"},
			wantNonCancelledPostCtx: true,
		},
		{
			// ctx cancelled (not deadline), no typed error — ctx.Err() fallback (interrupted)
			name: "cancelled ctx (cancel) with generic error — interrupted fallback",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			cloneErr:                fmt.Errorf("some generic failure"),
			wantMsgContains:         []string{"interrupted", "kubechecks replan"},
			wantMsgExcludes:         []string{"timed out"},
			wantNonCancelledPostCtx: true,
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
			// 401 = bad/missing token — say "authentication", not "authorization"
			name:            "HTTP 401 unauthorized — authentication message, no retry",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusUnauthorized},
			wantMsgContains: []string{"401", "Authentication", "token"},
			wantMsgExcludes: []string{"kubechecks replan", "Authorization", "permissions"},
		},
		{
			// 403 = insufficient scope — say "authorization"/"permissions", not "authentication"
			name:            "HTTP 403 forbidden — authorization message, no retry",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &HTTPError{StatusCode: http.StatusForbidden},
			wantMsgContains: []string{"403", "permissions"},
			wantMsgExcludes: []string{"kubechecks replan", "Authentication"},
		},
		{
			// URL parse failure is a bug — no replan suggestion
			name:            "urlParseError — no retry suggestion",
			ctx:             func() context.Context { return context.Background() },
			cloneErr:        &urlParseError{err: fmt.Errorf("unrecognized archive URL format: https://example.com/unknown")},
			wantMsgContains: []string{"parse", "kubechecks logs"},
			wantMsgExcludes: []string{"kubechecks replan"},
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

			// postCtxErr is captured inside Run so we observe Err() before
			// the deferred cancel() in PostArchiveErrorMessage fires on return.
			var postCtxErr error
			mockClient.On("PostMessage", mock.Anything, pr, mock.AnythingOfType("string")).
				Run(func(args mock.Arguments) {
					postCtxErr = args.Get(0).(context.Context).Err()
				}).
				Return(nil, nil)

			m := newTestManager(mockClient)
			ctx := tt.ctx()

			err := m.PostArchiveErrorMessage(ctx, pr, tt.cloneErr)
			assert.NoError(t, err)

			calls := mockClient.Calls
			assert.Len(t, calls, 1, "expected exactly one PostMessage call")
			postedMsg := calls[0].Arguments.String(2)
			for _, want := range tt.wantMsgContains {
				assert.Contains(t, postedMsg, want)
			}
			for _, excluded := range tt.wantMsgExcludes {
				assert.NotContains(t, postedMsg, excluded)
			}
			if tt.wantNonCancelledPostCtx {
				assert.NoError(t, postCtxErr, "PostMessage should receive a non-cancelled context when original ctx is done")
			}
		})
	}

	t.Run("PostMessage failure propagates error", func(t *testing.T) {
		mockClient := vcsmocks.NewMockClient(t)
		mockClient.On("PostMessage", mock.Anything, pr, mock.AnythingOfType("string")).
			Return(nil, fmt.Errorf("vcs unavailable"))

		m := newTestManager(mockClient)
		err := m.PostArchiveErrorMessage(context.Background(), pr, fmt.Errorf("network error"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "vcs unavailable")
	})
}
