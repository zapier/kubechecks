package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	giturls "github.com/whilp/git-urls"
)

const defaultBranchName = "HEAD"

type ReposDirectory struct {
	username      string
	rootPath      string
	repoDirsByUrl map[repoKey]string
	mutex         sync.Mutex
}

func NewReposDirectory(username string) (*ReposDirectory, error) {
	tempFolder, err := os.MkdirTemp("", "repos-cache")
	if err != nil {
		return nil, errors.Wrap(err, "failed to make repos cache root")
	}

	return &ReposDirectory{
		username:      username,
		rootPath:      tempFolder,
		repoDirsByUrl: make(map[repoKey]string),
	}, nil
}

type parsedUrl struct {
	cloneUrl string
	subdir   string
}

type repoKey string

func parseCloneUrl(username, url string) (parsedUrl, error) {
	parts, err := giturls.Parse(url)
	if err != nil {
		return parsedUrl{}, errors.Wrap(err, "failed to parse git url")
	}

	query := parts.Query()
	query.Get("subdir")

	parts.Path = strings.TrimPrefix(parts.Path, "/")

	cloneUrl := fmt.Sprintf("https://%s@%s/%s", username, parts.Host, parts.Path)

	subdir := query.Get("subdir")
	subdir = strings.TrimLeft(subdir, "/")

	return parsedUrl{
		cloneUrl: cloneUrl,
		subdir:   subdir,
	}, nil
}

func (rd *ReposDirectory) Clone(ctx context.Context, cloneUrl string) (string, error) {
	return rd.CloneWithBranch(ctx, cloneUrl, defaultBranchName)
}

func makeRepoKey(cloneUrl parsedUrl, ref string) repoKey {
	return repoKey(fmt.Sprintf("%s||%s", cloneUrl.cloneUrl, ref))
}

func (rd *ReposDirectory) CloneWithBranch(ctx context.Context, cloneUrl, ref string) (string, error) {
	var (
		ok      bool
		repoDir string
		err     error

		logger = log.With().
			Str("clone-url", cloneUrl).
			Logger()
	)

	rd.mutex.Lock()
	defer rd.mutex.Unlock()

	parsed, err := parseCloneUrl(cloneUrl, rd.username)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse clone url")
	}

	repoKey := makeRepoKey(parsed, ref)

	repoDir, ok = rd.repoDirsByUrl[repoKey]
	if ok {
		if err = rd.pull(repoDir); err != nil {
			logger.Warn().Err(err).Msg("failed to fetch latest")
		}
	} else {
		if repoDir, err = clone(cloneUrl, ref); err != nil {
			return "", errors.Wrap(err, "failed to clone repo")
		}
		rd.repoDirsByUrl[repoKey] = repoDir
	}

	if parsed.subdir != "" {
		repoDir = filepath.Join(repoDir, parsed.subdir)
	}

	return repoDir, nil
}

func (rd *ReposDirectory) pull(repoDir string) error {
	cmd := exec.Command("git", "pull")
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	return cmd.Run()
}

func clone(cloneUrl, branchName string) (string, error) {
	repoDir, err := os.MkdirTemp("/tmp", "schemas")
	if err != nil {
		return "", errors.Wrap(err, "failed to make temp dir")
	}

	log.Info().
		Str("temp-dir", repoDir).
		Str("clone-url", cloneUrl).
		Str("branch", branchName).
		Msg("cloning git repo")

	args := []string{"clone", cloneUrl, repoDir}
	if branchName != defaultBranchName {
		args = append(args, "-b", branchName)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	if err = cmd.Run(); err != nil {
		return "", errors.Wrap(err, "failed to clone repository")
	}

	return repoDir, nil
}
