package argo_client

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
)

// containsGlob reports whether the path includes glob meta-characters that
// filepath.Glob would expand (*, ?, or a character class).
func containsGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// copyGlobValueFiles expands a glob-style Helm value file path (resolved
// relative to srcAppPath) and copies each matching file into destAppDir,
// preserving its location relative to the source app directory. The original
// glob entry is left in source.Helm.ValueFiles so the Argo CD repo server can
// expand it once it receives the package.
func copyGlobValueFiles(srcAppPath, destAppDir string, source v1alpha1.ApplicationSource, valueFile string) error {
	pattern := filepath.Join(srcAppPath, valueFile)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return errors.Wrapf(err, "failed to expand glob %q", valueFile)
	}

	if len(matches) == 0 {
		if source.Helm != nil && source.Helm.IgnoreMissingValueFiles {
			log.Debug().Caller().
				Str("valueFile", valueFile).
				Msg("glob value file matched no files, skipping (IgnoreMissingValueFiles is true)")
			return nil
		}
		return fmt.Errorf("glob value file %q matched no files", valueFile)
	}

	for _, match := range matches {
		relFromAppPath, err := filepath.Rel(srcAppPath, match)
		if err != nil {
			return errors.Wrapf(err, "failed to compute relative path for %q", match)
		}
		dst := filepath.Join(destAppDir, relFromAppPath)
		if err := copyFile(match, dst); err != nil {
			if !ignoreValuesFileCopyError(source, match, err) {
				return errors.Wrapf(err, "failed to copy globbed value file %q", match)
			}
		}
	}
	return nil
}
