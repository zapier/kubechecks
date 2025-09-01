package pkg

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

var (
	GitTag    = ""
	GitCommit = ""
)

func Pointer[T interface{}](item T) *T {
	return &item
}

func WipeDir(dir string) {
	log.Debug().Str("path", dir).Msg("wiping path")
	if err := os.RemoveAll(dir); err != nil {
		log.Error().
			Err(err).
			Str("path", dir).
			Msg("failed to wipe path")
	}
}

// WithErrorLogging returns a function that will execute the given function and log any errors that occur.
func WithErrorLogging(f func() error, msg string) {
	if err := f(); err != nil {
		log.Error().Err(err).Msg(msg)
	}
}

func GetMessageHeader(identifier string) string {
	return fmt.Sprintf("# Kubechecks %s Report\n", identifier)
}
