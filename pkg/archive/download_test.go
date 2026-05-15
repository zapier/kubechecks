package archive

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
)

func TestIsRetriableDownloadError(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{
			name: "nil error",
			ctx:  context.Background(),
			err:  nil,
			want: false,
		},
		{
			name: "context cancelled",
			ctx:  cancelledCtx,
			err:  fmt.Errorf("some error"),
			want: false,
		},
		// HTTPError — retriable codes
		{
			name: "HTTP 404 not found",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusNotFound},
			want: true,
		},
		{
			name: "HTTP 429 rate limited",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "HTTP 500 internal server error",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusInternalServerError},
			want: true,
		},
		{
			name: "HTTP 502 bad gateway",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusBadGateway},
			want: true,
		},
		{
			name: "HTTP 503 service unavailable",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusServiceUnavailable},
			want: true,
		},
		// HTTPError — permanent codes
		{
			name: "HTTP 400 bad request",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusBadRequest},
			want: false,
		},
		{
			name: "HTTP 401 unauthorized",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusUnauthorized},
			want: false,
		},
		{
			name: "HTTP 403 forbidden",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "HTTP 422 unprocessable entity",
			ctx:  context.Background(),
			err:  &HTTPError{StatusCode: http.StatusUnprocessableEntity},
			want: false,
		},
		// Wrapped HTTPError — errors.As must traverse the chain
		{
			name: "wrapped HTTP 503",
			ctx:  context.Background(),
			err:  errors.Wrap(&HTTPError{StatusCode: http.StatusServiceUnavailable}, "download failed"),
			want: true,
		},
		{
			name: "wrapped HTTP 403",
			ctx:  context.Background(),
			err:  errors.Wrap(&HTTPError{StatusCode: http.StatusForbidden}, "download failed"),
			want: false,
		},
		// Non-HTTP errors are network-level — retriable
		{
			name: "generic network error",
			ctx:  context.Background(),
			err:  fmt.Errorf("connection refused"),
			want: true,
		},
		{
			name: "EOF error",
			ctx:  context.Background(),
			err:  fmt.Errorf("unexpected EOF"),
			want: true,
		},
		// extractError is always permanent — zip corruption/path traversal/disk full won't be fixed by retry
		{
			name: "extract error",
			ctx:  context.Background(),
			err:  &extractError{err: fmt.Errorf("invalid file path (path traversal detected): ../../etc/passwd")},
			want: false,
		},
		{
			name: "wrapped extract error",
			ctx:  context.Background(),
			err:  errors.Wrap(&extractError{err: fmt.Errorf("no space left on device")}, "failed to extract archive"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetriableDownloadError(tt.ctx, tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
