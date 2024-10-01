package gitlab_client

import (
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/xanzy/go-gitlab"
)

// getBackOff returns a backoff pointer to use to retry requests.
func getBackOff() *backoff.ExponentialBackOff {
	// Lets setup backoff logic to retry this request for 1 minute
	bOff := backoff.NewExponentialBackOff()
	bOff.InitialInterval = 60 * time.Second
	bOff.MaxInterval = 10 * time.Second
	bOff.RandomizationFactor = 0
	bOff.MaxElapsedTime = 180 * time.Second

	return bOff
}

var ErrRateLimited = errors.New("rate limited")

func checkReturnForBackoff(resp *gitlab.Response, err error) error {
	// if the error is nil lets check it out
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		return ErrRateLimited
	}

	if err != nil {
		return &backoff.PermanentError{Err: err}
	}

	return nil
}
