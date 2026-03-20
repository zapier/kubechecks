package github_client

import "time"

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
