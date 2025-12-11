package git

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/telemetry"
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
	_, err = gogit.PlainCloneContext(ctx, r.Directory, false, cloneOpts)
	if err != nil {
		log.Error().Err(err).Msg("unable to clone repository with go-git")
		return errors.Wrap(err, "failed to clone repository")
	}

	if log.Trace().Enabled() {
		if err = filepath.WalkDir(r.Directory, printFile); err != nil {
			log.Warn().Err(err).Msg("failed to walk directory")
		}
	}

	log.Info().Msg("repo has been cloned with go-git")
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

func (r *Repo) MergeIntoTarget(ctx context.Context, ref string) error {
	// Merge the last commit into a tmp branch off of the target branch
	// NOTE: Still using git binary for merge as go-git's merge support is limited
	// Archive mode doesn't use this method (VCS provides pre-merged state)
	// This is only used by persistent mode for PR checks
	_, span := tracer.Start(ctx, "Repo - RepoMergeIntoTarget",
		trace.WithAttributes(
			attribute.String("branch_name", r.BranchName),
			attribute.String("clone_url", r.CloneURL),
			attribute.String("directory", r.Directory),
			attribute.String("sha", ref),
			attribute.String("temp_branch", r.TempBranch),
		))
	defer span.End()

	// If we have a temp branch, ensure we're on it before merging
	// This is critical for concurrent PR checks using the same persistent repo
	if r.TempBranch != "" {
		log.Debug().
			Str("temp_branch", r.TempBranch).
			Str("ref", ref).
			Msg("checking out temp branch before merge")

		cmd := r.execGitCommand("checkout", r.TempBranch)
		out, err := cmd.CombinedOutput()
		if err != nil {
			telemetry.SetError(span, err, "checkout temp branch")
			log.Error().Err(err).Msgf("unable to checkout temp branch %s: %s", r.TempBranch, out)
			return errors.Wrapf(err, "failed to checkout temp branch %s", r.TempBranch)
		}
	}

	mergeCommand := []string{"merge", ref}
	cmd := r.execGitCommand(mergeCommand...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge commit into branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", ref, out)
		return err
	}

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

func (r *Repo) execGitCommand(args ...string) *exec.Cmd {
	argsToLog := r.censorVcsToken(args)

	log.Debug().Strs("args", argsToLog).Msg("building command")
	cmd := exec.Command("git", args...)
	if r.Directory != "" {
		cmd.Dir = r.Directory
	}
	return cmd
}

func (r *Repo) Wipe() {
	pkg.WipeDir(r.Directory)
}

func (r *Repo) censorVcsToken(args []string) []string {
	return censorVcsToken(r.Config, args)
}

func execCommand(cfg config.ServerConfig, name string, args ...string) *exec.Cmd {
	argsToLog := censorVcsToken(cfg, args)

	log.Debug().Strs("args", argsToLog).Msg("building command")
	cmd := exec.Command(name, args...)
	return cmd
}

func censorVcsToken(cfg config.ServerConfig, args []string) []string {
	vcsToken := cfg.VcsToken
	if len(vcsToken) == 0 {
		return args
	}

	var argsToLog []string
	for _, arg := range args {
		argsToLog = append(argsToLog, strings.Replace(arg, vcsToken, "********", 10))
	}
	return argsToLog
}

// SetCredentials ensures Git auth is set up for cloning
func SetCredentials(ctx context.Context, cfg config.ServerConfig, email, username, cloneUrl string) error {
	_, span := tracer.Start(ctx, "SetCredentials")
	defer span.End()

	cmd := execCommand(cfg, "git", "config", "--global", "user.email", email)
	err := cmd.Run()
	if err != nil {
		return errors.Wrap(err, "failed to set git email address")
	}

	cmd = execCommand(cfg, "git", "config", "--global", "user.name", username)
	err = cmd.Run()
	if err != nil {
		return errors.Wrap(err, "failed to set git user name")
	}

	homedir, err := os.UserHomeDir()
	if err != nil {
		return errors.Wrap(err, "unable to get home directory")
	}
	outfile, err := os.Create(fmt.Sprintf("%s/.git-credentials", homedir))
	if err != nil {
		return errors.Wrap(err, "unable to create credentials file")
	}
	defer pkg.WithErrorLogging(outfile.Close, "failed to close output file")

	cmd = execCommand(cfg, "echo", cloneUrl)
	cmd.Stdout = outfile
	err = cmd.Run()
	if err != nil {
		return errors.Wrap(err, "unable to set git credentials")
	}

	cmd = execCommand(cfg, "git", "config", "--global", "credential.helper", "store")
	err = cmd.Run()
	if err != nil {
		return errors.Wrap(err, "unable to set git credential usage")
	}
	log.Debug().Msg("git credentials set")

	return nil
}

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

	var fileList []string

	cmd := r.execGitCommand("diff", "--name-only", fmt.Sprintf("origin/%s...HEAD", r.BranchName))
	pipe, _ := cmd.StdoutPipe()
	var wg sync.WaitGroup
	scanner := bufio.NewScanner(pipe)
	wg.Add(1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			fileList = append(fileList, line)
		}
		wg.Done()
	}()
	err := cmd.Start()
	if err != nil {
		log.Error().Err(err).Msg("unable to start diff command")
		return nil, err
	}
	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		log.Error().Err(err).Msg("unable to diff branches")
		return nil, err
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

	// Create and checkout temp branch
	cmd := r.execGitCommand("checkout", "-b", tempBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("failed to create temp branch: %s", out)
		return "", errors.Wrapf(err, "failed to create temp branch %s", tempBranch)
	}

	log.Debug().
		Str("temp_branch", tempBranch).
		Msg("temporary branch created successfully")

	return tempBranch, nil
}

// FetchAndMergePR fetches a PR branch and merges it into the current temp branch
func (r *Repo) FetchAndMergePR(ctx context.Context, prBranch string) error {
	_, span := tracer.Start(ctx, "FetchAndMergePR")
	defer span.End()

	log.Debug().
		Str("pr_branch", prBranch).
		Str("current_branch", r.TempBranch).
		Msg("fetching and merging PR branch")

	// Fetch the PR branch
	cmd := r.execGitCommand("fetch", "origin", prBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("failed to fetch PR branch: %s", out)
		return errors.Wrapf(err, "failed to fetch PR branch %s", prBranch)
	}

	// Merge it into current temp branch
	mergeRef := fmt.Sprintf("origin/%s", prBranch)
	cmd = r.execGitCommand("merge", mergeRef)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("failed to merge PR branch: %s", out)
		return errors.Wrapf(err, "failed to merge %s", mergeRef)
	}

	log.Debug().
		Str("pr_branch", prBranch).
		Msg("PR branch merged successfully")

	return nil
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

	// Checkout base branch first
	cmd := r.execGitCommand("checkout", baseBranch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Warn().Err(err).Msgf("failed to checkout base branch during cleanup: %s", out)
		// Continue with cleanup anyway
	}

	// Delete the temp branch
	cmd = r.execGitCommand("branch", "-D", tempBranch)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Warn().Err(err).Msgf("failed to delete temp branch: %s", out)
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
