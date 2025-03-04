package gitlab_client

import (
	"fmt"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"
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

func checkReturnForBackoff(resp *gitlab.Response, err error) error {
	// if the error is nil lets check it out
	if resp != nil {
		if resp.StatusCode == http.StatusTooManyRequests {
			log.Warn().Msg("being rate limited doing backoff")
			return fmt.Errorf("%s", "Rate Limited")
		}
	}
	if err != nil {
		// Lets check the error and see if we need to trigger backoff
		switch err.(type) {
		default:
			// If it is not one of the above errors lets skip the backoff logic
			return &backoff.PermanentError{Err: err}
		}
	}

	// Return nil as the error passed in must have been nil as it passed the switch statement
	return nil
}
