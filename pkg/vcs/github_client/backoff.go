package github_client

import (
	"time"

	"github.com/cenkalti/backoff/v4"
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
