package github_client

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/go-github/v74/github"
	"github.com/rs/zerolog/log"
)

// retryConfig holds retry/backoff parameters for polling loops.
// Zero values mean "use defaults".
type retryConfig struct {
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

func (r retryConfig) withDefaults(maxRetries int, initialBackoff, maxBackoff time.Duration) retryConfig {
	// Apply defaults for zero values.
	if r.maxRetries == 0 {
		r.maxRetries = maxRetries
	}
	if r.initialBackoff == 0 {
		r.initialBackoff = initialBackoff
	}
	if r.maxBackoff == 0 {
		r.maxBackoff = maxBackoff
	}
	// Normalize: clamp negative/zero values to safe minimums.
	if r.maxRetries < 0 {
		r.maxRetries = 0
	}
	if r.initialBackoff <= 0 {
		r.initialBackoff = initialBackoff
	}
	if r.maxBackoff <= 0 {
		r.maxBackoff = maxBackoff
	}
	// Ensure maxBackoff is never less than initialBackoff.
	if r.maxBackoff < r.initialBackoff {
		r.maxBackoff = r.initialBackoff
	}
	return r
}

// longer than this and the worker is better off failing the check than sitting on it
const maxRateLimitWait = 2 * time.Minute

func rateLimitWait(err error) (time.Duration, bool) {
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		return abuse.GetRetryAfter(), true
	}
	var rateLimit *github.RateLimitError
	if errors.As(err, &rateLimit) {
		return time.Until(rateLimit.Rate.Reset.Time), true
	}
	return 0, false
}

// retryable reports whether a failed API call is worth repeating. A 4xx other than rate limiting fails the
// same way every time. A 5xx or no response at all may mean the request went through, so only a call that
// is idempotent gets repeated after those.
func retryable(resp *github.Response, err error, idempotent bool) bool {
	if _, limited := rateLimitWait(err); limited {
		return true
	}
	if resp == nil || resp.Response == nil {
		return idempotent
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	return idempotent && resp.StatusCode >= http.StatusInternalServerError
}

// do repeats a call that failed.
func (r retryConfig) do(ctx context.Context, what string, idempotent bool, call func() (*github.Response, error)) error {
	backoff := r.initialBackoff

	for attempt := 0; ; attempt++ {
		resp, err := call()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		if attempt >= r.maxRetries || !retryable(resp, err, idempotent) {
			return err
		}

		wait := backoff
		if asked, limited := rateLimitWait(err); limited {
			if asked > maxRateLimitWait {
				return err
			}
			wait = max(wait, asked)
		}

		log.Warn().Err(err).Int("attempt", attempt+1).Dur("wait", wait).Msgf("%s failed, retrying", what)

		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), err)
		case <-time.After(wait):
			backoff = min(backoff*2, r.maxBackoff)
		}
	}
}
