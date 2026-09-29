package github_client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitWait(t *testing.T) {
	wait, limited := rateLimitWait(&github.AbuseRateLimitError{RetryAfter: github.Ptr(time.Minute)})
	assert.True(t, limited)
	assert.Equal(t, time.Minute, wait)

	wait, limited = rateLimitWait(&github.RateLimitError{Rate: github.Rate{Reset: github.Timestamp{Time: time.Now().Add(time.Hour)}}})
	assert.True(t, limited)
	assert.InDelta(t, time.Hour, wait, float64(time.Minute))

	_, limited = rateLimitWait(errors.New("boom"))
	assert.False(t, limited)
}

func TestRetry_RateLimit(t *testing.T) {
	calls := 0
	limitedFor := func(d time.Duration) func() (*github.Response, error) {
		return func() (*github.Response, error) {
			calls++
			if calls > 1 {
				return nil, nil
			}
			resp := &http.Response{StatusCode: http.StatusForbidden, Request: &http.Request{Method: http.MethodPost, URL: &url.URL{}}}
			return &github.Response{Response: resp}, &github.AbuseRateLimitError{Response: resp, RetryAfter: &d}
		}
	}

	require.NoError(t, fastRetry.do(context.Background(), "create", false, limitedFor(5*time.Millisecond)))
	assert.Equal(t, 2, calls)

	calls = 0
	require.Error(t, fastRetry.do(context.Background(), "create", false, limitedFor(time.Hour)))
	assert.Equal(t, 1, calls, "a wait beyond maxRateLimitWait is not worth retrying")
}
