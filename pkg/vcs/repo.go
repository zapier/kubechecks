package vcs

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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/telemetry"
)

// Repo represents a local Repostiory on disk, based off of a PR/MR
type Repo struct {
	BaseRef       string   // base ref is the branch that the PR is being merged into
	HeadRef       string   // head ref is the branch that the PR is coming from
	DefaultBranch string   // Some repos have default branches we need to capture
	RepoDir       string   // The directory where the repo is cloned
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

func (r *Repo) CloneRepoLocal(ctx context.Context, repoDir string) error {
	//  Attempt to locally clone the repo based on the provided information stored within
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CloneRepo")
	defer span.End()

	// TODO: Look if this is still needed
	r.RepoDir = repoDir

	cmd := r.execCommand("git", "clone", r.CloneURL, repoDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to clone repository, %s", out)
		return err
	}

	cmd = r.execCommand("git", "remote")
	pipe, _ := cmd.StdoutPipe()
	var wg sync.WaitGroup
	scanner := bufio.NewScanner(pipe)
	wg.Add(1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			r.Remote = line
			// Just grab the first remote as it should be the default
			break
		}
		wg.Done()
	}()
	err = cmd.Start()
	if err != nil {
		telemetry.SetError(span, err, "unable to get remote")
		log.Error().Err(err).Msg("unable to get git remote")
		return err
	}
	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		telemetry.SetError(span, err, "unable to get remote")
		log.Error().Err(err).Msg("unable to get git remote")
		return err
	}

	if log.Trace().Enabled() {
		// print contents of repo
		//nolint
		filepath.WalkDir(repoDir, printFile)
	}

	// Print the path to the cloned repository
	log.Info().Str("project", r.Name).Str("ref", r.HeadRef).Msgf("Repository cloned to: %s", r.RepoDir)

	return nil
}

func (r *Repo) MergeIntoTarget(ctx context.Context) error {
	// Merge the last commit into a tmp branch off of the target branch
	_, span := otel.Tracer("Kubechecks").Start(ctx, "Repo - RepoMergeIntoTarget",
		trace.WithAttributes(
			attribute.String("check_id", fmt.Sprintf("%d", r.CheckID)),
			attribute.String("project", r.Name),
			attribute.String("source_branch", r.HeadRef),
			attribute.String("target_branch", r.BaseRef),
			attribute.String("default_branch", r.DefaultBranch),
			attribute.String("last_commit_id", r.SHA),
		))
	defer span.End()

	log.Debug().Msgf("Merging MR commit %s into a tmp branch off of %s for manifest generation...", r.SHA, r.BaseRef)
	cmd := r.execCommand("git", "fetch", r.Remote, r.BaseRef)
	err := cmd.Run()
	if err != nil {
		telemetry.SetError(span, err, "git fetch remote into target branch")
		log.Error().Err(err).Msgf("unable to fetch %s", r.BaseRef)
		return err
	}

	cmd = r.execCommand("git", "checkout", "-b", "tmp", fmt.Sprintf("%s/%s", r.Remote, r.BaseRef))
	_, err = cmd.Output()
	if err != nil {
		telemetry.SetError(span, err, "git checkout tmp branch")
		log.Error().Err(err).Msgf("unable to checkout %s %s", r.Remote, r.BaseRef)
		return err
	}

	cmd = r.execCommand("git", "merge", r.SHA)
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge last commit id into tmp branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", r.SHA, out)
		return err
	}

	return nil
}

// GetListOfChangedFiles returns a list of files that have changed between the current branch and the target branch
func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	var fileList = []string{}

	cmd := r.execCommand("git", "diff", "--name-only", fmt.Sprintf("%s/%s", r.Remote, r.BaseRef))
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

func printFile(s string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if !d.IsDir() {
		println(s)
	}
	return nil
}

func (r *Repo) execCommand(name string, args ...string) *exec.Cmd {
	cmd := execCommand(r.Config, name, args...)
	cmd.Dir = r.RepoDir
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

func execCommand(cfg config.ServerConfig, name string, args ...string) *exec.Cmd {
	argsToLog := censorVcsToken(cfg, args)

	log.Debug().Strs("args", argsToLog).Msg("building command")
	cmd := exec.Command(name, args...)
	return cmd
}

// InitializeGitSettings ensures Git auth is set up for cloning
func InitializeGitSettings(cfg config.ServerConfig, vcsClient VcsClient) error {
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
