package gitea_client

import "code.gitea.io/sdk/gitea"

// PullRequestsServices defines the interface for pull request operations.
type PullRequestsServices interface {
	GetPullRequest(owner, repo string, index int64) (*gitea.PullRequest, *gitea.Response, error)
	ListPullRequestFiles(owner, repo string, index int64, opt gitea.ListPullRequestFilesOptions) ([]*gitea.ChangedFile, *gitea.Response, error)
}

// RepositoriesServices defines the interface for repository operations.
type RepositoriesServices interface {
	GetRepo(owner, reponame string) (*gitea.Repository, *gitea.Response, error)
	CreateStatus(owner, repo, sha string, opts gitea.CreateStatusOption) (*gitea.Status, *gitea.Response, error)
	ListRepoHooks(user, repo string, opt gitea.ListHooksOptions) ([]*gitea.Hook, *gitea.Response, error)
	CreateRepoHook(user, repo string, opt gitea.CreateHookOption) (*gitea.Hook, *gitea.Response, error)
}

// IssueCommentsServices defines the interface for issue comment operations.
type IssueCommentsServices interface {
	CreateIssueComment(owner, repo string, index int64, opt gitea.CreateIssueCommentOption) (*gitea.Comment, *gitea.Response, error)
	EditIssueComment(owner, repo string, commentID int64, opt gitea.EditIssueCommentOption) (*gitea.Comment, *gitea.Response, error)
	ListIssueComments(owner, repo string, index int64, opt gitea.ListIssueCommentOptions) ([]*gitea.Comment, *gitea.Response, error)
	DeleteIssueComment(owner, repo string, commentID int64) (*gitea.Response, error)
}

// GClient wraps the Gitea SDK client into service groups for testability.
type GClient struct {
	PullRequests PullRequestsServices
	Repositories RepositoriesServices
	Issues       IssueCommentsServices
}
