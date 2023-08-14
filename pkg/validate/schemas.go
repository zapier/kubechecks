package validate

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/repo"
)

type ReposDirectory struct {
	paths map[string]string

	mutex sync.Mutex
}

func newReposDirectory() *ReposDirectory {
	rd := &ReposDirectory{
		paths: make(map[string]string),
	}

	return rd
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
		log.Err(err).Msg("failed to clone schemas repository")
		return ""
	}

	rd.paths[cloneUrl] = repoDir
	return repoDir
}
