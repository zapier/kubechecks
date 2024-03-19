package cmd

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/builder/remotecontext/urlutil"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
)

func processLocations(ctx context.Context, ctr container.Container, locations []string) error {
	for index, location := range locations {
		if newLocation, err := maybeCloneGitUrl(ctx, ctr.RepoManager, location, ctr.VcsClient.Username()); err != nil {
			return errors.Wrapf(err, "failed to clone %q", location)
		} else if newLocation != "" {
			locations[index] = newLocation
		}
	}

	return nil
}

type cloner interface {
	Clone(ctx context.Context, cloneUrl, branchName string) (*git.Repo, error)
}

var ErrCannotUseQueryWithFilePath = errors.New("relative and absolute file paths cannot have query parameters")

func maybeCloneGitUrl(ctx context.Context, repoManager cloner, location, vcsUsername string) (string, error) {
	result := strings.SplitN(location, "?", 2)
	if !urlutil.IsGitURL(result[0]) {
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

	go func() {
		tick := time.Tick(time.Minute * 5)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick:
			}

			if err := repo.Update(ctx); err != nil {
				log.Warn().
					Err(err).
					Str("path", repo.Directory).
					Str("url", repo.CloneURL).
					Msg("failed to update repo")
			}
		}
	}()

	path := repo.Directory
	subdir := query.Get("subdir")
	if subdir != "" {
		path = filepath.Join(path, subdir)
	}

	return path, nil
}
