package vcs

import (
	"github.com/zapier/kubechecks/pkg/config"
)

// PullRequest represents an PR/MR
type PullRequest struct {
	BaseRef       string   // base ref is the branch that the PR is being merged into
	HeadRef       string   // head ref is the branch that the PR is coming from
	DefaultBranch string   // Some repos have default branches we need to capture
	Remote        string   // Remote address
	CloneURL      string   // Where we clone the repo from
	Name          string   // Name of the repo
	Owner         string   // Owner of the repo (in Gitlab this is the namespace)
	CheckID       int      // MR/PR id that generated this Repo
	SHA           string   // SHA of the MR/PRs head
	FullName      string   // Owner/Name combined (ie zapier/kubechecks)
	Username      string   // Username of auth'd client
	Email         string   // Email of auth'd client
	Labels        []string // Labels associated with the MR/PR

	Config config.ServerConfig
}
