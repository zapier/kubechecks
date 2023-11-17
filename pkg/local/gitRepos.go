package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/repo"
)

type ReposDirectory struct {
	paths map[string]string

	mutex sync.Mutex
}

func NewReposDirectory() *ReposDirectory {
	rd := &ReposDirectory{
		paths: make(map[string]string),
	}

	return rd
}

func (rd *ReposDirectory) EnsurePath(ctx context.Context, tempRepoPath, location string) string {
	if location == "" {
		return ""
	}

	if strings.HasPrefix(location, "https://") || strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "git@") {
		log.Debug().Str("location", location).Msg("registering remote repository")
		localPath := rd.Register(ctx, location)
		return localPath
	}

	schemaPath := filepath.Join(tempRepoPath, location)
	if stat, err := os.Stat(schemaPath); err == nil && stat.IsDir() {
		log.Debug().Str("location", location).Msg("registering in-repo path")
		return schemaPath
	} else {
		log.Warn().Str("location", location).Err(err).Msg("failed to find in-repo path")
	}

	return ""
}

func (rd *ReposDirectory) Register(ctx context.Context, cloneUrl string) string {
	var (
		ok      bool
		repoDir string
	)

	rd.mutex.Lock()
	defer rd.mutex.Unlock()

	repoDir, ok = rd.paths[cloneUrl]
	if ok {
		rd.fetchLatest()
		return repoDir
	}

	return rd.clone(ctx, cloneUrl)
}

func (rd *ReposDirectory) fetchLatest() {
	cmd := exec.Command("git", "pull")
	err := cmd.Run()
	if err != nil {
		log.Err(err).Msg("failed to pull latest")
	}
}

func (rd *ReposDirectory) clone(ctx context.Context, cloneUrl string) string {
	repoDir, err := os.MkdirTemp("/tmp", "schemas")
	if err != nil {
		log.Err(err).Msg("failed to make temp dir")
		return ""
	}

	r := repo.Repo{CloneURL: cloneUrl}
	err = r.CloneRepoLocal(ctx, repoDir)
	if err != nil {
		log.Err(err).Str("clone-url", cloneUrl).Msg("failed to clone repository")
		return ""
	}

	rd.paths[cloneUrl] = repoDir
	return repoDir
}
