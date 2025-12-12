package git

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
)

// HTTPClient interface for HTTP operations to enable testing
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Repo struct {
	// informational
	BranchName string
	Config     config.ServerConfig
	CloneURL   string

	// exposed state
	Directory string

	TempBranch     string // Temporary branch name for isolated PR checks
	BaseBranchName string // Original base branch (e.g., main/master)
}

func New(cfg config.ServerConfig, cloneUrl, branchName string) *Repo {
	if branchName == "" {
		branchName = "HEAD"
	}

	return &Repo{
		CloneURL:   cloneUrl,
		BranchName: branchName,
		Config:     cfg,
	}
}

// getAuth returns authentication options for go-git operations
// Returns nil for anonymous/public access when no token is configured
func (r *Repo) getAuth() *gogithttp.BasicAuth {
	// If no token configured, use anonymous access (for public repos)
	if r.Config.VcsToken == "" {
		return nil
	}

	// Extract username from clone URL if present, otherwise use default
	username := "git"
	if parsed, err := url.Parse(r.CloneURL); err == nil && parsed.User != nil {
		username = parsed.User.Username()
	}

	return &gogithttp.BasicAuth{
		Username: username,
		Password: r.Config.VcsToken,
	}
}

func (r *Repo) Clone(ctx context.Context) error {
	var err error

	if r.Directory == "" {
		r.Directory, err = os.MkdirTemp("/tmp", "kubechecks-repo-")
		if err != nil {
			return errors.Wrap(err, "failed to make temp dir")
		}
	}

	log.Info().
		Str("temp-dir", r.Directory).
		Str("clone-url", r.CloneURL).
		Str("branch", r.BranchName).
		Msg("cloning git repo with go-git")

	_, span := tracer.Start(ctx, "CloneRepo")
	defer span.End()

	// Prepare clone options
	cloneOpts := &gogit.CloneOptions{
		URL:  r.CloneURL,
		Auth: r.getAuth(),
	}

	// If branch is specified and not HEAD, checkout that branch after clone
	// Note: We don't use SingleBranch=true because it doesn't set up refs/remotes/origin/HEAD
	// which is needed by GetRemoteHead(). For policy repos, having all branches is acceptable.
	if r.BranchName != "HEAD" && r.BranchName != "" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(r.BranchName)
	}

	// Clone the repository
	repo, err := gogit.PlainCloneContext(ctx, r.Directory, false, cloneOpts)
	if err != nil {
		log.Error().Err(err).Msg("unable to clone repository with go-git")
		return errors.Wrap(err, "failed to clone repository")
	}

	// Set up refs/remotes/origin/HEAD symbolic reference
	// This is needed by GetRemoteHead() and mirrors what git binary does automatically
	if err := r.setupRemoteHead(repo); err != nil {
		log.Warn().Err(err).Msg("failed to set up refs/remotes/origin/HEAD, continuing anyway")
		// Don't fail the clone operation, GetRemoteHead() has fallback logic
	}

	if log.Trace().Enabled() {
		if err = filepath.WalkDir(r.Directory, printFile); err != nil {
			log.Warn().Err(err).Msg("failed to walk directory")
		}
	}

	log.Info().Msg("repo has been cloned with go-git")
	return nil
}

// setupRemoteHead creates the refs/remotes/origin/HEAD symbolic reference
// that points to the default branch. This mirrors what git binary does automatically.
func (r *Repo) setupRemoteHead(repo *gogit.Repository) error {
	// Query the remote to find the default branch
	remote, err := repo.Remote("origin")
	if err != nil {
		return errors.Wrap(err, "failed to get origin remote")
	}

	refs, err := remote.List(&gogit.ListOptions{
		Auth: r.getAuth(),
	})
	if err != nil {
		return errors.Wrap(err, "failed to list remote refs")
	}

	// Find the HEAD reference
	var defaultBranch string
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD {
			defaultBranch = ref.Target().Short()
			break
		}
	}

	if defaultBranch == "" {
		return errors.New("remote HEAD not found")
	}

	// Create the symbolic reference refs/remotes/origin/HEAD -> refs/remotes/origin/{defaultBranch}
	originHeadRef := plumbing.NewSymbolicReference(
		plumbing.NewRemoteReferenceName("origin", "HEAD"),
		plumbing.NewRemoteReferenceName("origin", defaultBranch),
	)

	if err := repo.Storer.SetReference(originHeadRef); err != nil {
		return errors.Wrap(err, "failed to set refs/remotes/origin/HEAD")
	}

	log.Debug().
		Str("default-branch", defaultBranch).
		Msg("set up refs/remotes/origin/HEAD")

	return nil
}

func printFile(s string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if !d.IsDir() {
		println(s)
	}
	return nil
}

func (r *Repo) GetRemoteHead() (string, error) {
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		return "", errors.Wrap(err, "failed to open repository")
	}

	// Try to get the symbolic reference for origin/HEAD first
	ref, err := repo.Reference(plumbing.ReferenceName("refs/remotes/origin/HEAD"), true)
	if err == nil {
		// Extract branch name from reference (e.g., refs/remotes/origin/main -> main)
		branchName := ref.Name().String()
		branchName = strings.TrimPrefix(branchName, "refs/remotes/origin/")
		return branchName, nil
	}

	// If refs/remotes/origin/HEAD doesn't exist (go-git doesn't create it),
	// query the remote to find the default branch
	remote, err := repo.Remote("origin")
	if err != nil {
		return "", errors.Wrap(err, "failed to get remote 'origin'")
	}

	// List remote refs to find HEAD
	refs, err := remote.List(&gogit.ListOptions{
		Auth: r.getAuth(),
	})
	if err != nil {
		return "", errors.Wrap(err, "failed to list remote refs")
	}

	// Find the HEAD ref
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD {
			// HEAD points to the default branch
			targetBranch := ref.Target().Short()
			return targetBranch, nil
		}
	}

	return "", errors.New("failed to determine remote HEAD")
}

func (r *Repo) GetCurrentBranch() (string, error) {
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		return "", errors.Wrap(err, "failed to open repository")
	}

	// Get the HEAD reference
	head, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to determine which branch HEAD points to")
	}

	// Extract branch name from reference (e.g., refs/heads/main -> main)
	branchName := head.Name().Short()

	return branchName, nil
}

// Checkout checks out a branch using go-git
func (r *Repo) Checkout(branchName string) error {
	log.Debug().
		Str("branch", branchName).
		Str("path", r.Directory).
		Msg("checking out branch with go-git")

	// Open the repository
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		return errors.Wrap(err, "failed to open repository")
	}

	// Get the worktree
	worktree, err := repo.Worktree()
	if err != nil {
		return errors.Wrap(err, "failed to get worktree")
	}

	// Checkout the branch
	checkoutOpts := &gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branchName),
		Force:  false, // Don't force, preserve local changes if any
	}

	if err := worktree.Checkout(checkoutOpts); err != nil {
		return errors.Wrapf(err, "failed to checkout branch %s", branchName)
	}

	log.Debug().
		Str("branch", branchName).
		Msg("successfully checked out branch")

	return nil
}

func (r *Repo) Update(ctx context.Context) error {
	// Record fetch metrics
	repoFetchTotal.Inc()
	timer := prometheus.NewTimer(repoFetchDuration)
	defer timer.ObserveDuration()

	// Open the repository
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		repoFetchFailed.Inc()
		return errors.Wrap(err, "failed to open repository")
	}

	// Fetch latest changes from remote
	fetchOpts := &gogit.FetchOptions{
		RemoteName: "origin",
		Auth:       r.getAuth(),
	}

	// If branch is specified, fetch only that branch
	if r.BranchName != "HEAD" && r.BranchName != "" {
		fetchOpts.RefSpecs = []gogitconfig.RefSpec{
			gogitconfig.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", r.BranchName, r.BranchName)),
		}
	}

	if err := repo.FetchContext(ctx, fetchOpts); err != nil && err != gogit.NoErrAlreadyUpToDate {
		repoFetchFailed.Inc()
		log.Error().Err(err).Msgf("failed to fetch branch %s", r.BranchName)
		return errors.Wrapf(err, "failed to fetch branch %s", r.BranchName)
	}

	// Get the worktree
	worktree, err := repo.Worktree()
	if err != nil {
		repoFetchFailed.Inc()
		return errors.Wrap(err, "failed to get worktree")
	}

	// Get the reference to origin/branch
	remoteBranch := fmt.Sprintf("refs/remotes/origin/%s", r.BranchName)
	ref, err := repo.Reference(plumbing.ReferenceName(remoteBranch), true)
	if err != nil {
		repoFetchFailed.Inc()
		log.Error().Err(err).Msgf("failed to get reference to %s", remoteBranch)
		return errors.Wrapf(err, "failed to get reference to %s", remoteBranch)
	}

	// Reset to match remote branch (hard reset)
	resetOpts := &gogit.ResetOptions{
		Commit: ref.Hash(),
		Mode:   gogit.HardReset,
	}

	if err := worktree.Reset(resetOpts); err != nil {
		repoFetchFailed.Inc()
		log.Error().Err(err).Msgf("failed to reset to origin/%s", r.BranchName)
		return errors.Wrapf(err, "failed to reset to origin/%s", r.BranchName)
	}

	repoFetchSuccess.Inc()
	log.Debug().
		Caller().
		Str("url", r.CloneURL).
		Str("branch", r.BranchName).
		Msg("updated branch to latest with go-git")

	return nil
}

func (r *Repo) Wipe() {
	pkg.WipeDir(r.Directory)
}

// BuildCloneURL constructs a clone URL with embedded credentials
func BuildCloneURL(baseURL, user, password string) (string, error) {
	var hostname, scheme string

	parts, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse %q", baseURL)
	}
	hostname = parts.Host
	scheme = parts.Scheme

	return fmt.Sprintf("%s://%s:%s@%s", scheme, user, password, hostname), nil
}

// GetListOfChangedFiles returns a list of files that have changed between the current branch and the target branch
func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	// Open the repository
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		log.Error().Err(err).Msg("failed to open repository")
		return nil, errors.Wrap(err, "failed to open repository")
	}

	// Get HEAD commit
	headRef, err := repo.Head()
	if err != nil {
		log.Error().Err(err).Msg("failed to get HEAD")
		return nil, errors.Wrap(err, "failed to get HEAD")
	}

	// Get origin/<branch> commit
	remoteBranchRef := fmt.Sprintf("refs/remotes/origin/%s", r.BranchName)
	baseRef, err := repo.Reference(plumbing.ReferenceName(remoteBranchRef), true)
	if err != nil {
		log.Error().Err(err).Str("ref", remoteBranchRef).Msg("failed to get base branch reference")
		return nil, errors.Wrapf(err, "failed to get reference %s", remoteBranchRef)
	}

	// Get commit objects
	headCommit, err := repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, errors.Wrap(err, "failed to get HEAD commit")
	}

	baseCommit, err := repo.CommitObject(baseRef.Hash())
	if err != nil {
		return nil, errors.Wrap(err, "failed to get base commit")
	}

	// Get trees
	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get HEAD tree")
	}

	baseTree, err := baseCommit.Tree()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get base tree")
	}

	// Calculate diff
	changes, err := baseTree.Diff(headTree)
	if err != nil {
		log.Error().Err(err).Msg("unable to diff branches")
		return nil, errors.Wrap(err, "failed to diff trees")
	}

	// Extract file paths
	var fileList []string
	for _, change := range changes {
		// Include both From and To names to handle renames
		if change.From.Name != "" {
			fileList = append(fileList, change.From.Name)
		}
		if change.To.Name != "" && change.To.Name != change.From.Name {
			fileList = append(fileList, change.To.Name)
		}
	}

	return fileList, nil
}

// GetCurrentCommitSHA returns the current commit SHA
func (r *Repo) GetCurrentCommitSHA() (string, error) {
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		return "", errors.Wrap(err, "failed to open repository")
	}

	// Get the HEAD reference
	head, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get current commit SHA")
	}

	sha := head.Hash().String()
	// Return first 8 characters for short SHA
	if len(sha) >= 8 {
		return sha[:8], nil
	}
	return sha, nil
}

// CreateTempBranch creates a temporary branch for isolated PR checks
// prIdentifier should be unique (e.g., timestamp)
// commitSHA is used in the branch name for traceability
func (r *Repo) CreateTempBranch(ctx context.Context, prIdentifier, commitSHA string) (string, error) {
	_, span := tracer.Start(ctx, "CreateTempBranch")
	defer span.End()

	// Sanitize inputs
	safePRID := sanitizeBranchName(prIdentifier)
	safeSHA := sanitizeBranchName(commitSHA)

	// Create unique temp branch name
	tempBranch := fmt.Sprintf("temp-pr-%s-%s", safePRID, safeSHA)

	log.Debug().
		Str("temp_branch", tempBranch).
		Str("from_branch", r.BranchName).
		Msg("creating temporary branch")

	// Open the repository
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		return "", errors.Wrap(err, "failed to open repository")
	}

	// Get current HEAD
	headRef, err := repo.Head()
	if err != nil {
		return "", errors.Wrap(err, "failed to get HEAD")
	}

	// Create new branch reference
	branchRef := plumbing.NewBranchReferenceName(tempBranch)
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())
	if err := repo.Storer.SetReference(ref); err != nil {
		log.Error().Err(err).Msgf("failed to create temp branch reference")
		return "", errors.Wrapf(err, "failed to create branch %s", tempBranch)
	}

	// Checkout the new branch
	worktree, err := repo.Worktree()
	if err != nil {
		return "", errors.Wrap(err, "failed to get worktree")
	}

	checkoutOpts := &gogit.CheckoutOptions{
		Branch: branchRef,
	}
	if err := worktree.Checkout(checkoutOpts); err != nil {
		log.Error().Err(err).Msg("failed to checkout temp branch")
		return "", errors.Wrapf(err, "failed to checkout branch %s", tempBranch)
	}

	log.Debug().
		Str("temp_branch", tempBranch).
		Msg("temporary branch created successfully")

	return tempBranch, nil
}

// CleanupTempBranch removes a temporary branch and returns to the base branch
func (r *Repo) CleanupTempBranch(ctx context.Context, tempBranch, baseBranch string) error {
	_, span := tracer.Start(ctx, "CleanupTempBranch")
	defer span.End()

	if tempBranch == "" {
		return nil
	}

	log.Debug().
		Str("temp_branch", tempBranch).
		Str("base_branch", baseBranch).
		Msg("cleaning up temporary branch")

	// Checkout base branch first using go-git
	if err := r.Checkout(baseBranch); err != nil {
		log.Warn().Err(err).Msg("failed to checkout base branch during cleanup")
		// Continue with cleanup anyway
	}

	// Delete the temp branch using go-git
	repo, err := gogit.PlainOpen(r.Directory)
	if err != nil {
		log.Warn().Err(err).Msg("failed to open repository for branch cleanup")
		return errors.Wrap(err, "failed to open repository")
	}

	// Delete the branch reference
	branchRef := plumbing.NewBranchReferenceName(tempBranch)
	if err := repo.Storer.RemoveReference(branchRef); err != nil {
		log.Warn().Err(err).Msg("failed to delete temp branch")
		return errors.Wrapf(err, "failed to delete temp branch %s", tempBranch)
	}

	log.Debug().
		Str("temp_branch", tempBranch).
		Msg("temporary branch cleaned up successfully")

	return nil
}

// sanitizeBranchName removes characters that are invalid in git branch names
func sanitizeBranchName(name string) string {
	// Replace invalid characters with hyphens
	replacer := strings.NewReplacer(
		" ", "-",
		"/", "-",
		"\\", "-",
		":", "-",
		"~", "-",
		"^", "-",
		"?", "-",
		"*", "-",
		"[", "-",
		"]", "-",
	)
	sanitized := replacer.Replace(name)

	// Remove any consecutive hyphens
	for strings.Contains(sanitized, "--") {
		sanitized = strings.ReplaceAll(sanitized, "--", "-")
	}

	// Trim hyphens from start and end
	sanitized = strings.Trim(sanitized, "-")

	return sanitized
}
