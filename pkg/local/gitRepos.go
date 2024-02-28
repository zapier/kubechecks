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

	"github.com/zapier/kubechecks/pkg/vcs"
)

type ReposDirectory struct {
	rootPath      string
	repoDirsByUrl map[string]string
	mutex         sync.Mutex
}

func NewReposDirectory() (*ReposDirectory, error) {
	tempFolder, err := os.MkdirTemp("", "repos-cache")
	if err != nil {
		return nil, errors.Wrap(err, "failed to make repos cache root")
	}

	return &ReposDirectory{
		rootPath:      tempFolder,
		repoDirsByUrl: make(map[string]string),
	}, nil
}

type parsedUrl struct {
	cloneUrl string
	subdir   string
}

func parseCloneUrl(url string) (parsedUrl, error) {
	parts, err := giturls.Parse(url)
	if err != nil {
		return parsedUrl{}, errors.Wrap(err, "failed to parse git url")
	}

	query := parts.Query()
	query.Get("subdir")

	var cloneUrl string
	if parts.Scheme == "ssh" {
		cloneUrl = fmt.Sprintf("%s@%s:%s", parts.User.Username(), parts.Host, parts.Path)
	} else {
		cloneUrl = fmt.Sprintf("%s://%s%s", parts.Scheme, parts.Host, parts.Path)
	}

	subdir := query.Get("subdir")
	subdir = strings.TrimLeft(subdir, "/")

	return parsedUrl{
		cloneUrl: cloneUrl,
		subdir:   subdir,
	}, nil
}

func (rd *ReposDirectory) Clone(ctx context.Context, cloneUrl string) (string, error) {
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

	parsed, err := parseCloneUrl(cloneUrl)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse clone url")
	}

	repoDir, ok = rd.repoDirsByUrl[parsed.cloneUrl]
	if ok {
		if err = rd.fetchLatest(repoDir); err != nil {
			logger.Warn().Err(err).Msg("failed to fetch latest")
		}
	} else {
		if repoDir, err = rd.clone(ctx, cloneUrl); err != nil {
			return "", errors.Wrap(err, "failed to clone repo")
		}
	}

	if parsed.subdir != "" {
		repoDir = filepath.Join(repoDir, parsed.subdir)
	}

	return repoDir, nil

}

func (rd *ReposDirectory) fetchLatest(repoDir string) error {
	cmd := exec.Command("git", "pull")
	cmd.Dir = repoDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	return cmd.Run()
}

func (rd *ReposDirectory) clone(ctx context.Context, cloneUrl string) (string, error) {
	repoDir, err := os.MkdirTemp("/tmp", "schemas")
	if err != nil {
		return "", errors.Wrap(err, "failed to make temp dir")
	}

	r := vcs.Repo{CloneURL: cloneUrl}
	err = r.CloneRepoLocal(ctx, repoDir)
	if err != nil {
		return "", errors.Wrap(err, "failed to clone repository")
	}

	rd.repoDirsByUrl[cloneUrl] = repoDir
	return repoDir, nil
}
