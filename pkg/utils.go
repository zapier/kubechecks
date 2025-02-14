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
	log.Debug().Str("path", dir).Msg("wiping path")
	if err := os.RemoveAll(dir); err != nil {
		log.Error().
			Err(err).
			Str("path", dir).
			Msg("failed to wipe path")
	}
}
