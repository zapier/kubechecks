package cmd

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/docker/docker/builder/remotecontext/urlutil"
	"github.com/pkg/errors"
	giturls "github.com/whilp/git-urls"

	"github.com/zapier/kubechecks/pkg/git"
)

func processLocations(ctx context.Context, repoManager *git.RepoManager, locations []string) error {
	for index, policyLocation := range locations {
		if newLocation, err := maybeCloneGitUrl(ctx, repoManager, policyLocation); err != nil {
			return errors.Wrapf(err, "failed to clone %q", policyLocation)
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

func maybeCloneGitUrl(ctx context.Context, repoManager cloner, location string) (string, error) {
	result := strings.SplitN(location, "?", 2)
	if !urlutil.IsGitURL(result[0]) {
		if len(result) > 1 {
			return "", ErrCannotUseQueryWithFilePath
		}
		return result[0], nil
	}

	parsed, err := giturls.Parse(location)
	if err != nil {
		return "", errors.Wrapf(err, "invalid git url: %q", location)
	}
	query := parsed.Query()

	repo, err := repoManager.Clone(ctx, parsed.String(), query.Get("branch"))
	if err != nil {
		return "", errors.Wrap(err, "failed to clone")
	}

	path := repo.Directory
	subdir := query.Get("subdir")
	if subdir != "" {
		path = filepath.Join(path, subdir)
	}

	return path, nil
}
