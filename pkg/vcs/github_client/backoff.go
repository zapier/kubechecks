package github_client

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-github/v74/github"
)

// getBackOff returns a backoff pointer to use to retry requests
func getBackOff() *backoff.ExponentialBackOff {

	// Lets setup backoff logic to retry this request for 1 minute
	bOff := backoff.NewExponentialBackOff()
	bOff.InitialInterval = 60 * time.Second
	bOff.MaxInterval = 10 * time.Second
	bOff.RandomizationFactor = 0
	bOff.MaxElapsedTime = 180 * time.Second

	return bOff
}

func checkReturnForBackoff(resp *github.Response, err error) error {
	if resp != nil {
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("%s", "Rate Limited")
		}
	}
	if err != nil {
		return &backoff.PermanentError{Err: err}
	}
	return err
}
