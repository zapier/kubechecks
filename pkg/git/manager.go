package git

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg/config"
)

var tracer = otel.Tracer("pkg/git")

type RepoManager struct {
	lock  sync.Mutex
	repos []*Repo
	cfg   config.ServerConfig
}

func NewRepoManager(cfg config.ServerConfig) *RepoManager {
	return &RepoManager{cfg: cfg}
}

func (rm *RepoManager) Clone(ctx context.Context, cloneUrl, branchName string) (*Repo, error) {
	repo := New(rm.cfg, cloneUrl, branchName)

	if err := repo.Clone(ctx); err != nil {
		return nil, errors.Wrap(err, "failed to clone repository")
	}

	rm.lock.Lock()
	//defer rm.lock.Unlock() // just for safety's sake
	rm.repos = append(rm.repos, repo)

	return repo, nil
}

func (rm *RepoManager) Cleanup() {
	rm.lock.Lock()
	//defer rm.lock.Unlock()

	for _, repo := range rm.repos {
		repo.Wipe()
	}
}
