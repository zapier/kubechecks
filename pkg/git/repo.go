package git

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

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
	var err error

	r.Directory, err = os.MkdirTemp("/tmp", "kubechecks-repo-")
	if err != nil {
		return errors.Wrap(err, "failed to make temp dir")
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

	cmd := r.execCommand("git", args...)
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
	cmd := r.execCommand("git", "symbolic-ref", "refs/remotes/origin/HEAD", "--short")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Wrap(err, "failed to determine which branch HEAD points to")
	}

	branchName := strings.TrimSpace(string(out))
	branchName = strings.TrimPrefix(branchName, "origin/")

	return branchName, nil
}

func (r *Repo) MergeIntoTarget(ctx context.Context, sha string) error {
	// Merge the last commit into a tmp branch off of the target branch
	_, span := tracer.Start(ctx, "Repo - RepoMergeIntoTarget",
		trace.WithAttributes(
			attribute.String("branch_name", r.BranchName),
			attribute.String("clone_url", r.CloneURL),
			attribute.String("directory", r.Directory),
			attribute.String("sha", sha),
		))
	defer span.End()

	cmd := r.execCommand("git", "merge", sha)
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge commit into branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", sha, out)
		return err
	}

	return nil
}

func (r *Repo) Update(ctx context.Context) error {
	cmd := r.execCommand("git", "pull")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	return cmd.Run()
}

func (r *Repo) execCommand(name string, args ...string) *exec.Cmd {
	argsToLog := r.censorVcsToken(args)

	log.Debug().Strs("args", argsToLog).Msg("building command")
	cmd := exec.Command(name, args...)
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
	defer outfile.Close()

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

	return fmt.Sprintf("%s://%s:%s@%s", scheme, user, vcsToken, hostname), nil
}

// GetListOfChangedFiles returns a list of files that have changed between the current branch and the target branch
func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := tracer.Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	var fileList []string

	cmd := r.execCommand("git", "diff", "--name-only", fmt.Sprintf("%s/%s", "origin", r.BranchName))
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
