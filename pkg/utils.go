package pkg

import (
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
	if err := os.RemoveAll(dir); err != nil {
		log.Error().
			Err(err).
			Str("path", dir).
			Msg("failed to wipe path")
	}
}
