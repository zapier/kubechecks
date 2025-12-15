package cmd

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
)

// processLocations processes a list of locations, which can be either file paths or Git repository URLs.
// For Git URLs, it clones the repository and updates the location to point to the local clone.
// For file paths, it leaves them unchanged.
// The function returns an error if any Git repository fails to clone.
func processLocations(ctx context.Context, ctr container.Container, locations []string) error {
	for index, location := range locations {
		if newLocation, err := maybeCloneGitUrl(ctx, ctr.RepoManager, ctr.Config.RepoRefreshInterval, location, ctr.VcsClient.CloneUsername()); err != nil {
			return errors.Wrapf(err, "failed to clone %q", location)
		} else if newLocation != "" {
			locations[index] = newLocation
		}
	}

	log.Debug().
		Caller().
		Strs("locations", locations).
		Msg("locations after processing")

	return nil
}

type cloner interface {
	Clone(ctx context.Context, cloneUrl, branchName string) (*git.Repo, error)
}

var ErrCannotUseQueryWithFilePath = errors.New("relative and absolute file paths cannot have query parameters")

func maybeCloneGitUrl(ctx context.Context, repoManager cloner, repoRefreshDuration time.Duration, location, vcsUsername string) (string, error) {
	result := strings.SplitN(location, "?", 2)
	if !isGitURL(result[0]) {
		if len(result) > 1 {
			return "", ErrCannotUseQueryWithFilePath
		}
		return result[0], nil
	}

	repoUrl, query, err := pkg.NormalizeRepoUrl(location)
	if err != nil {
		return "", errors.Wrapf(err, "invalid git url: %q", location)
	}
	cloneUrl := repoUrl.CloneURL(vcsUsername)

	repo, err := repoManager.Clone(ctx, cloneUrl, query.Get("branch"))
	if err != nil {
		return "", errors.Wrap(err, "failed to clone")
	}

	if repoRefreshDuration != 0 {
		go func() {
			tick := time.Tick(repoRefreshDuration)
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick:
				}

				// If the repo directory was cleaned up (e.g., due to TTL expiration),
				// try to re-clone it to restore functionality
				if _, statErr := os.Stat(repo.Directory); os.IsNotExist(statErr) {
					log.Info().
						Str("path", repo.Directory).
						Str("url", repo.CloneURL).
						Msg("repo directory missing, attempting to re-clone")

					// Attempt to re-clone the repository
					newRepo, cloneErr := repoManager.Clone(ctx, cloneUrl, query.Get("branch"))
					if cloneErr != nil {
						log.Error().
							Err(cloneErr).
							Str("url", cloneUrl).
							Msg("failed to re-clone repo after directory cleanup")
						continue
					}

					// Update the repo reference to point to the new clone
					repo = newRepo
					log.Info().
						Str("path", repo.Directory).
						Str("url", repo.CloneURL).
						Msg("successfully re-cloned repo")
				} else {
					if err := repo.Update(ctx); err != nil {
						log.Warn().
							Err(err).
							Str("path", repo.Directory).
							Str("url", repo.CloneURL).
							Msg("failed to update repo")
					}
				}

			}
		}()
	}

	path := repo.Directory
	subdir := query.Get("subdir")
	if subdir != "" {
		path = filepath.Join(path, subdir)
	}

	return path, nil
}

func isGitURL(url string) bool {
	str := strings.ToLower(url)
	if isValidURL(str) && urlPathWithFragmentSuffix.MatchString(str) {
		return true
	}
	for _, prefix := range []string{"git://", "github.com/", "gitlab.com/", "git@"} {
		if strings.HasPrefix(str, prefix) {
			return true
		}
	}
	return false
}

// urlPathWithFragmentSuffix matches fragments to use as Git reference and build
// context from the Git repository. See IsGitURL for details.
var urlPathWithFragmentSuffix = regexp.MustCompile(`\.git(?:#.+)?$`)

// isValidURL returns true if the provided str is a well-formed HTTP(S) URL.
func isValidURL(str string) bool {
	u, err := url.Parse(str)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
}
