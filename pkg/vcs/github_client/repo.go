package github_client

import (
	"context"

	"github.com/google/go-github/v74/github"
)

type RepositoriesServices interface {
	GetContents(ctx context.Context, owner, repo, path string, opts *github.RepositoryContentGetOptions) (fileContent *github.RepositoryContent, directoryContent []*github.RepositoryContent, resp *github.Response, err error)
	Get(ctx context.Context, owner, repo string) (*github.Repository, *github.Response, error)
	CreateStatus(ctx context.Context, owner, repo, ref string, status *github.RepoStatus) (*github.RepoStatus, *github.Response, error)
	CreateHook(ctx context.Context, owner, repo string, hook *github.Hook) (*github.Hook, *github.Response, error)
	ListHooks(ctx context.Context, owner, repo string, opts *github.ListOptions) ([]*github.Hook, *github.Response, error)
}

type RepositoriesService struct {
	RepositoriesServices
}
