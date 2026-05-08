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
//
// repoRoot anchors the source side and destDir anchors the destination side.
// The function rejects absolute valueFile paths and any expanded match or
// computed destination that escapes its respective root, so a maliciously
// crafted Application spec cannot read or write files outside the repo / temp
// package directory.
func copyGlobValueFiles(repoRoot, destDir, srcAppPath, destAppDir string, source v1alpha1.ApplicationSource, valueFile string) error {
	if filepath.IsAbs(valueFile) {
		return fmt.Errorf("absolute value file paths are not permitted: %q", valueFile)
	}

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

	resolvedRepoRoot, err := resolveExisting(repoRoot)
	if err != nil {
		return errors.Wrap(err, "failed to resolve repo root")
	}
	absDestDir, err := absClean(destDir)
	if err != nil {
		return errors.Wrap(err, "failed to resolve package dir")
	}

	for _, match := range matches {
		resolvedMatch, err := resolveExisting(match)
		if err != nil {
			return errors.Wrapf(err, "failed to resolve %q", match)
		}
		if !isWithin(resolvedMatch, resolvedRepoRoot) {
			return fmt.Errorf("globbed value file %q escapes repo root", match)
		}

		relFromAppPath, err := filepath.Rel(srcAppPath, match)
		if err != nil {
			return errors.Wrapf(err, "failed to compute relative path for %q", match)
		}

		dst := filepath.Join(destAppDir, relFromAppPath)
		absDst, err := absClean(dst)
		if err != nil {
			return errors.Wrapf(err, "failed to resolve destination %q", dst)
		}
		if !isWithin(absDst, absDestDir) {
			return fmt.Errorf("destination for %q escapes package dir", match)
		}

		if err := copyFile(match, dst); err != nil {
			if !ignoreValuesFileCopyError(source, match, err) {
				return errors.Wrapf(err, "failed to copy globbed value file %q", match)
			}
		}
	}
	return nil
}

// resolveExisting returns an absolute, symlink-resolved path. The path must
// exist on disk; this is used on the source side so symlinks planted inside
// the repo cannot redirect us outside it.
func resolveExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// absClean returns the cleaned absolute form of path without resolving
// symlinks. Used on the destination side, where the target path may not exist
// yet and where we control the tree (so symlink redirection is not a concern).
func absClean(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// isWithin reports whether child is identical to parent or sits inside it.
// Both arguments must be cleaned absolute paths.
func isWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
