package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// buildTestZip returns a small in-memory zip mirroring the GitHub archive layout: an
// explicit top-level directory entry `{repo}-{sha}/` followed by a file underneath.
// The directory entry is what the extractor uses to detect and strip the wrapper folder.
func buildTestZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	dirHdr := &zip.FileHeader{Name: "repo-deadbeef/"}
	dirHdr.SetMode(0o755 | os.ModeDir)
	_, err := zw.CreateHeader(dirHdr)
	require.NoError(t, err)

	fileHdr := &zip.FileHeader{Name: "repo-deadbeef/README.md"}
	fileHdr.SetMode(0o644)
	w, err := zw.CreateHeader(fileHdr)
	require.NoError(t, err)
	_, err = w.Write([]byte("hello from zip"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// TestDownloader_FollowsRedirect locks in the redirect behavior the GitHub zipball flow
// depends on: api.github.com responds with 302 to a signed codeload URL, and the
// downloader must follow that redirect to retrieve the archive. Guards against a future
// http.Client customization (e.g. CheckRedirect: ErrUseLastResponse) silently breaking
// archive downloads.
//
// Note: this test does NOT assert Authorization-header stripping. httptest servers all
// bind to 127.0.0.1, and Go's net/http compares hostnames (not host:port) for the
// sensitive-header decision, so within httptest the header is always forwarded. The
// cross-host stripping for api.github.com → codeload.github.com is a Go stdlib invariant
// covered by the standard library's own tests.
func TestDownloader_FollowsRedirect(t *testing.T) {
	zipBytes := buildTestZip(t)

	var (
		apiHits      int
		codeloadHits int
	)

	// "codeload" stand-in: serves the archive bytes.
	codeload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		codeloadHits++
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	}))
	defer codeload.Close()

	// "api" stand-in: requires Authorization, 302s to codeload (mirroring how
	// api.github.com/repos/.../zipball/SHA redirects to a signed codeload URL).
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiHits++
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"),
			"original request to API must carry the Bearer token")
		http.Redirect(w, r, codeload.URL+"/signed-archive.zip?token=abc", http.StatusFound)
	}))
	defer api.Close()

	targetDir := filepath.Join(t.TempDir(), "extract")
	d := NewDownloader()
	extractedPath, err := d.DownloadAndExtract(
		context.Background(),
		api.URL+"/repos/owner/repo/zipball/deadbeef",
		targetDir,
		map[string]string{"Authorization": "Bearer test-token"},
	)
	require.NoError(t, err)

	assert.Equal(t, 1, apiHits, "API endpoint should be hit exactly once")
	assert.Equal(t, 1, codeloadHits, "Downloader must follow the 302 and fetch from the redirect target")
	assert.True(t, strings.HasSuffix(extractedPath, "repo-deadbeef"),
		"extractor should strip the top-level wrapper directory: got %q", extractedPath)

	body, err := os.ReadFile(filepath.Join(extractedPath, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello from zip", string(body))
}
