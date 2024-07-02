package github_client

import (
	"context"

	"github.com/google/go-github/v62/github"
)

type PullRequestsServices interface {
	List(ctx context.Context, owner string, repo string, opts *github.PullRequestListOptions) ([]*github.PullRequest, *github.Response, error)
	ListFiles(ctx context.Context, owner string, repo string, number int, opts *github.ListOptions) ([]*github.CommitFile, *github.Response, error)
	GetRaw(ctx context.Context, owner string, repo string, number int, opts github.RawOptions) (string, *github.Response, error)
	Get(ctx context.Context, owner string, repo string, number int) (*github.PullRequest, *github.Response, error)
}

type PullRequestsService struct {
	PullRequestsServices
}
