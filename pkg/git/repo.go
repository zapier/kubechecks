package git

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
	"github.com/zapier/kubechecks/telemetry"
)

type Repo struct {
	// informational
	BranchName string
	Config     config.ServerConfig
	CloneURL   string
	Shallow    bool

	// exposed state
	Directory string
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

func (r *Repo) Clone(ctx context.Context) error {
	if r.Shallow {
		return r.shallowClone(ctx)
	}

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
		Msg("cloning git repo")

	//  Attempt to locally clone the repo based on the provided information stored within
	_, span := tracer.Start(ctx, "CloneRepo")
	defer span.End()

	args := []string{"clone", r.CloneURL, r.Directory}
	if r.BranchName != "HEAD" {
		args = append(args, "--branch", r.BranchName)
	}

	cmd := r.execGitCommand(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to clone repository, %s", out)
		return err
	}

	if log.Trace().Enabled() {
		if err = filepath.WalkDir(r.Directory, printFile); err != nil {
			log.Warn().Err(err).Msg("failed to walk directory")
		}
	}

	log.Info().Msg("repo has been cloned")
	return nil
}

func (r *Repo) shallowClone(ctx context.Context) error {
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
		Msg("cloning git repo")

	//  Attempt to locally clone the repo based on the provided information stored within
	_, span := tracer.Start(ctx, "ShallowCloneRepo")
	defer span.End()

	args := []string{"clone", r.CloneURL, r.Directory, "--depth", "1"}
	cmd := r.execGitCommand(args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to clone repository, %s", out)
		return err
	}

	// Set all branches to be fetched to allow checking out any branch
	// https://github.com/zapier/kubechecks/issues/407#issuecomment-2850431802
	remoteSetBranchesArgs := []string{"remote", "set-branches", "origin", "*"}
	cmd = r.execGitCommand(remoteSetBranchesArgs...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to set remote branches, %s", out)
		return err
	}

	if r.BranchName != "HEAD" {
		// Fetch SHA
		args = []string{"fetch", "origin", r.BranchName, "--depth", "1"}
		cmd = r.execGitCommand(args...)
		out, err = cmd.CombinedOutput()
		if err != nil {
			log.Error().Err(err).Msgf("unable to fetch %s repository, %s", r.BranchName, out)
			return err
		}
		// Checkout SHA
		args = []string{"checkout", r.BranchName}
		cmd = r.execGitCommand(args...)
		out, err = cmd.CombinedOutput()
		if err != nil {
			log.Error().Err(err).Msgf("unable to checkout branch %s repository, %s", r.BranchName, out)
			return err
		}
	}

	if log.Trace().Enabled() {
		if err = filepath.WalkDir(r.Directory, printFile); err != nil {
			log.Warn().Err(err).Msg("failed to walk directory")
		}
	}

	log.Info().Msg("repo has been cloned")
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
	cmd := r.execGitCommand("symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrap(err, "failed to determine which branch HEAD points to")
	}

	branchName := strings.TrimSpace(string(out))
	branchName = strings.TrimPrefix(branchName, "origin/")

	return branchName, nil
}

func (r *Repo) GetCurrentBranch() (string, error) {
	cmd := r.execGitCommand("rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrap(err, "failed to determine which branch HEAD points to")
	}

	branchName := strings.TrimSpace(string(out))

	return branchName, nil
}

func (r *Repo) MergeIntoTarget(ctx context.Context, ref string) error {
	// Merge the last commit into a tmp branch off of the target branch
	_, span := tracer.Start(ctx, "Repo - RepoMergeIntoTarget",
		trace.WithAttributes(
			attribute.String("branch_name", r.BranchName),
			attribute.String("clone_url", r.CloneURL),
			attribute.String("directory", r.Directory),
			attribute.String("sha", ref),
		))
	defer span.End()
	merge_command := []string{"merge", ref}
	// For shallow clones, we need to pull the ref into the repo
	if r.Shallow {
		ref = strings.TrimPrefix(ref, "origin/")
		cmd := r.execGitCommand("fetch", "origin", fmt.Sprintf("%s:%s", ref, ref), "--depth", "1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			telemetry.SetError(span, err, "fetch origin ref")
			log.Error().Err(err).Msgf("unable to fetch ref %s, %s", ref, out)
			return err
		}
		// When merging shallow clones, we need to allow unrelated histories
		// and use the "theirs" strategy to avoid conflicts
		// cons of this is that it may not be entirely accurate and may overwrite changes in the target branch
		merge_command = []string{"merge", ref, "--allow-unrelated-histories", "-X", "theirs"}
	}

	cmd := r.execGitCommand(merge_command...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge commit into branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", ref, out)
		return err
	}

	return nil
}

func (r *Repo) Update(ctx context.Context) error {
	// Since we're shallow cloning, to update we need to wipe the directory and re-clone
	if r.Shallow {
		r.Wipe()
		err := os.Mkdir(r.Directory, 0700)
		if err != nil {
			return errors.Wrap(err, "failed to create repo directory")
		}
		return r.Clone(ctx)
	}
	cmd := r.execGitCommand("pull")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	return cmd.Run()
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
func SetCredentials(cfg config.ServerConfig, vcsClient vcs.Client) error {
	email := vcsClient.Email()
	username := vcsClient.Username()

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

	cloneUrl, err := getCloneUrl(username, cfg)
	if err != nil {
		return errors.Wrap(err, "failed to get clone url")
	}

	homedir, err := os.UserHomeDir()
	if err != nil {
		if err != nil {
			return errors.Wrap(err, "unable to get home directory")
		}
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

func getCloneUrl(user string, cfg config.ServerConfig) (string, error) {
	vcsBaseUrl := cfg.VcsBaseUrl
	vcsType := cfg.VcsType
	vcsToken := cfg.VcsToken

	var hostname, scheme string

	if vcsBaseUrl == "" {
		// hack: but it does happen to work for now
		hostname = fmt.Sprintf("%s.com", vcsType)
		scheme = "https"
	} else {
		parts, err := url.Parse(vcsBaseUrl)
		if err != nil {
			return "", errors.Wrapf(err, "failed to parse %q", vcsBaseUrl)
		}
		hostname = parts.Host
		scheme = parts.Scheme
	}

	if cfg.GithubAppID != 0 && cfg.GithubInstallationID != 0 && cfg.GithubPrivateKey != "" {
		client := &http.Client{
			Timeout: 5 * time.Minute,
		}
		stringAppId := fmt.Sprintf("%d", cfg.GithubAppID)
		jwt, err := pkg.CreateJWT(cfg.GithubPrivateKey, stringAppId)
		if err != nil {
			return "", errors.Wrapf(err, "failed to create jwt")
		}
		url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", cfg.GithubInstallationID)

		req, err := http.NewRequest("POST", url, nil)
		if err != nil {
			return "", errors.Wrapf(err, "failed to create request")
		}
		req.Header.Add("Accept", "application/vnd.github.v3+json")
		req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", jwt))

		resp, err := client.Do(req)
		if err != nil {
			return "", errors.Wrapf(err, "failed to get response")
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", errors.Wrapf(err, "failed to read response")
		}

		var result interface{}
		err = json.Unmarshal(body, &result)
		if err != nil {
			return "", errors.Wrapf(err, "failed to unmarshal response")
		}

		data, ok := result.(map[string]interface{})
		if !ok {
			return "", errors.New("failed to convert response to map")
		}

		if token, exists := data["token"]; exists {
			user = fmt.Sprintf("x-access-token:%s", token.(string))
		}
	}

	return fmt.Sprintf("%s://%s:%s@%s", scheme, user, vcsToken, hostname), nil
}

// GetListOfChangedFiles returns a list of files that have changed between the current branch and the target branch
func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	var fileList []string

	cmd := r.execGitCommand("diff", "--name-only", fmt.Sprintf("%s/%s", "origin", r.BranchName))
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
