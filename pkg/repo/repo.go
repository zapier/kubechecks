package repo

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Represents a local Repostiory on disk, based off of a PR/MR
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
	OwnerName     string   // Owner/Name combined (ie zapier/kubechecks)
	Username      string   // Username of auth'd client
	Email         string   // Email of auth'd client
	Labels        []string // Labels associated with the MR/PR
}

func (r *Repo) CloneRepoLocal(ctx context.Context, repoDir string) error {
	//  Attempt to locally clone the repo based on the provided information stored within
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CloneRepo")
	defer span.End()

	// TODO: Look if this is still needed
	r.RepoDir = repoDir

	cmd := exec.Command("git", "clone", r.CloneURL, repoDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to clone repository, %s", out)
		return err
	}

	cmd = exec.Command("git", "remote")
	cmd.Dir = repoDir
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
		filepath.WalkDir(repoDir, walk)
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

	if r.DefaultBranch != "" {
		if r.BaseRef != r.DefaultBranch {
			err := fmt.Errorf("target branch (%s) is not default branch (%s)\nfor kubechecks to run target branch must be default branch", r.HeadRef, r.DefaultBranch)
			telemetry.SetError(span, err, "MergeIntoTarget")
			return err
		}
	}

	log.Debug().Msgf("Merging MR commit %s into a tmp branch off of %s for manifest generation...", r.SHA, r.BaseRef)
	cmd := exec.Command("git", "fetch", r.Remote, r.BaseRef)
	cmd.Dir = r.RepoDir
	err := cmd.Run()
	if err != nil {
		telemetry.SetError(span, err, "git fetch remote into target branch")
		log.Error().Err(err).Msgf("unable to fetch %s", r.BaseRef)
		return err
	}

	cmd = exec.Command("git", "checkout", "-b", "tmp", fmt.Sprintf("%s/%s", r.Remote, r.BaseRef))
	cmd.Dir = r.RepoDir
	_, err = cmd.Output()
	if err != nil {
		telemetry.SetError(span, err, "git checkout tmp branch")
		log.Error().Err(err).Msgf("unable to checkout %s %s", r.Remote, r.BaseRef)
		return err
	}

	cmd = exec.Command("git", "merge", r.SHA)
	cmd.Dir = r.RepoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge last commit id into tmp branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", r.SHA, out)
		return err
	}

	return nil
}

// GetListOfRepoFiles returns a list of all files in the local repository
func (r *Repo) GetListOfRepoFiles() ([]string, error) {
	files := []string{}
	walkFn := func(s string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			// make path relative to repo root
			rel, _ := filepath.Rel(r.RepoDir, s)
			if strings.HasPrefix(rel, ".git") {
				// ignore files in .git subdir
				return nil
			}

			files = append(files, rel)
		}
		return nil
	}

	err := filepath.WalkDir(r.RepoDir, walkFn)

	return files, err
}

// GetListOfChangedFiles returns a list of files that have changed between the current branch and the target branch
func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	var fileList = []string{}

	cmd := exec.Command("git", "diff", "--name-only", r.BaseRef)
	cmd.Dir = r.RepoDir
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

func walk(s string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if !d.IsDir() {
		println(s)
	}
	return nil
}

// InitializeGitSettings ensures Git auth is set up for cloning
func InitializeGitSettings(user string, email string) error {
	cmd := exec.Command("git", "config", "--global", "user.email", email)
	err := cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git username")
		return err
	}

	cmd = exec.Command("git", "config", "--global", "user.name", user)
	err = cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git username")
		return err
	}

	cmd = exec.Command("echo", fmt.Sprintf("https://%s:%s@%s.com", user, viper.GetString("vcs-token"), viper.GetString("vcs-type")))
	homedir, err := os.UserHomeDir()
	if err != nil {
		if err != nil {
			log.Error().Err(err).Msg("unable to get home directory")
			return err
		}
	}
	outfile, err := os.Create(fmt.Sprintf("%s/.git-credentials", homedir))
	if err != nil {
		log.Error().Err(err).Msg("unable to create credentials file")
		return err
	}
	defer outfile.Close()
	cmd.Stdout = outfile
	err = cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git credentials")
		return err
	}

	cmd = exec.Command("git", "config", "--global", "credential.helper", "store")
	err = cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git credential usage")
		return err
	}
	log.Debug().Msg("git credentials set")

	return nil
}
