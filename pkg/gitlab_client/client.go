package gitlab_client

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/xanzy/go-gitlab"
	"github.com/zapier/kubechecks/pkg/repo"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var gitlabClient *Client
var gitlabTokenUser string
var gitlabTokenEmail string
var once sync.Once

const GitlabTokenHeader = "X-Gitlab-Token"

type Client struct {
	*gitlab.Client
}

type Repo struct {
	mr      *gitlab.MergeEvent
	repoDir string
	remote  string
}

func GetGitlabClient() (*Client, string) {
	once.Do(func() {
		gitlabClient = createGitlabClient()
		gitlabTokenUser, gitlabTokenEmail = gitlabClient.getTokenUser()
		initializeGitSettings()
	})

	return gitlabClient, gitlabTokenUser
}

func createGitlabClient() *Client {
	// Initialize the GitLab client with access token
	t := viper.GetString("vcs-token")
	if t == "" {
		log.Fatal().Msg("gitlab token needs to be set")
	}
	log.Debug().Msgf("Token Length - %d", len(t))
	c, err := gitlab.NewClient(t)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Gitlab client")
	}

	return &Client{c}
}

func (c *Client) getTokenUser() (string, string) {
	user, _, err := c.Users.CurrentUser()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create Gitlab token user")
	}

	return user.Username, user.Email
}

func (c *Client) NewRepo(ctx context.Context, mr *gitlab.MergeEvent, tempRepoDir string) (*Repo, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GetRepo")
	defer span.End()

	r := &Repo{
		mr:      mr,
		repoDir: tempRepoDir,
	}

	cmd := exec.Command("git", "clone", mr.Project.GitHTTPURL, r.repoDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error().Err(err).Msgf("unable to clone repository, %s", out)
		return nil, err
	}

	cmd = exec.Command("git", "remote")
	cmd.Dir = r.repoDir
	pipe, _ := cmd.StdoutPipe()
	var wg sync.WaitGroup
	scanner := bufio.NewScanner(pipe)
	wg.Add(1)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			r.remote = line
			// Just grab the first remote as it should be the default
			break
		}
		wg.Done()
	}()
	err = cmd.Start()
	if err != nil {
		telemetry.SetError(span, err, "unable to get remote")
		log.Error().Err(err).Msg("unable to get git remote")
		return nil, err
	}
	wg.Wait()
	err = cmd.Wait()
	if err != nil {
		telemetry.SetError(span, err, "unable to get remote")
		log.Error().Err(err).Msg("unable to get git remote")
		return nil, err
	}

	project, _, err := c.Projects.GetProject(mr.Project.PathWithNamespace, &gitlab.GetProjectOptions{})
	if err != nil {
		telemetry.SetError(span, err, "unable get project details")
		log.Error().Err(err).Msg("could not retrieve project details")
		return nil, err
	}

	if log.Trace().Enabled() {
		// print contents of repo
		//nolint
		filepath.WalkDir(tempRepoDir, walk)
	}

	// Print the path to the cloned repository
	log.Info().Str("project", project.PathWithNamespace).Str("ref", mr.ObjectAttributes.SourceBranch).Msgf("Repository cloned to: %s", r.repoDir)

	return r, nil
}

func (r *Repo) GetListOfRepoFiles() ([]string, error) {
	files := []string{}
	walkFn := func(s string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			// make path relative to repo root
			rel, _ := filepath.Rel(r.repoDir, s)
			if strings.HasPrefix(rel, ".git") {
				// ignore files in .git subdir
				return nil
			}

			files = append(files, rel)
		}
		return nil
	}

	err := filepath.WalkDir(r.repoDir, walkFn)

	return files, err
}

func (r *Repo) GetListOfChangedFiles(ctx context.Context) ([]string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "RepoGetListOfChangedFiles")
	defer span.End()

	var fileList = []string{}

	cmd := exec.Command("git", "diff", "--name-only", r.mr.ObjectAttributes.TargetBranch)
	cmd.Dir = r.repoDir
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

func initializeGitSettings() error {
	token := viper.GetString("gitlab-token")
	log.Debug().
		Str("gitUser", gitlabTokenUser).
		Str("gitEmail", gitlabTokenEmail).
		Int("tokenLength", len(token)).
		Msg("configuring git settings & auth")
	cmd := exec.Command("git", "config", "--global", "user.email", gitlabTokenEmail)
	err := cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git email")
		return err
	}

	cmd = exec.Command("git", "config", "--global", "user.name", gitlabTokenUser)
	err = cmd.Run()
	if err != nil {
		log.Error().Err(err).Msg("unable to set git username")
		return err
	}

	cmd = exec.Command("echo", fmt.Sprintf("https://%s:%s@gitlab.com", gitlabTokenUser, token))
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
	return nil
}

func (r *Repo) MergeIntoTarget(ctx context.Context) error {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "Repo - RepoMergeIntoTarget",
		trace.WithAttributes(
			attribute.String("mr_id", strconv.Itoa(r.mr.ObjectAttributes.IID)),
			attribute.String("project", r.mr.Project.PathWithNamespace),
			attribute.String("source_branch", r.mr.ObjectAttributes.SourceBranch),
			attribute.String("target_branch", r.mr.ObjectAttributes.TargetBranch),
			attribute.String("default_branch", r.mr.Project.DefaultBranch),
			attribute.String("last_commit_id", r.mr.ObjectAttributes.LastCommit.ID),
		))
	defer span.End()

	if r.mr.Project.DefaultBranch != "" {
		if r.mr.ObjectAttributes.TargetBranch != r.mr.Project.DefaultBranch {
			err := fmt.Errorf("target branch (%s) is not default branch (%s)\nfor kubechecks to run target branch must be default branch", r.mr.ObjectAttributes.TargetBranch, r.mr.Repository.DefaultBranch)
			telemetry.SetError(span, err, "MergeIntoTarget")
			return err
		}
	}

	log.Debug().Msgf("Merging MR commit %s into a tmp branch off of %s for manifest generation...", r.mr.ObjectAttributes.LastCommit.ID, r.mr.ObjectAttributes.TargetBranch)
	cmd := exec.Command("git", "fetch", r.remote, r.mr.ObjectAttributes.TargetBranch)
	cmd.Dir = r.repoDir
	err := cmd.Run()
	if err != nil {
		telemetry.SetError(span, err, "git fetch remote into target branch")
		log.Error().Err(err).Msgf("unable to fetch %s", r.mr.ObjectAttributes.TargetBranch)
		return err
	}

	cmd = exec.Command("git", "checkout", "-b", "tmp", fmt.Sprintf("%s/%s", r.remote, r.mr.ObjectAttributes.TargetBranch))
	cmd.Dir = r.repoDir
	_, err = cmd.Output()
	if err != nil {
		telemetry.SetError(span, err, "git checkout tmp branch")
		log.Error().Err(err).Msgf("unable to checkout %s %s", r.remote, r.mr.ObjectAttributes.TargetBranch)
		return err
	}

	cmd = exec.Command("git", "merge", r.mr.ObjectAttributes.LastCommit.ID)
	cmd.Dir = r.repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		telemetry.SetError(span, err, "merge last commit id into tmp branch")
		log.Error().Err(err).Msgf("unable to merge %s, %s", r.mr.ObjectAttributes.LastCommit.ID, out)
		return err
	}

	return nil
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

// Each client has a different way of verifying their payloads; return an err if this isnt valid
func (c *Client) VerifyHook(secret string, p echo.Context) error {
	if secret != p.Request().Header.Get(GitlabTokenHeader) {
		return fmt.Errorf("unable to verify payload")
	}

	return nil

}

// Each client has a different way of discerning their webhook events; return an err if this isnt valid
func (c *Client) ParseHook(r *http.Request, payload []byte) (interface{}, error) {
	return gitlab.ParseHook(gitlab.HookEventType(r), payload)
}

// Takes a valid gitlab webhook event request, and determines if we should process it
// Returns a generic Repo with all info kubechecks needs on success, err if not
func (c *Client) CreateRepo(ctx context.Context, eventRequest interface{}) (*repo.Repo, error) {
	switch event := eventRequest.(type) {
	case *gitlab.MergeEvent:
		switch event.ObjectAttributes.Action {
		case "update":
			if event.ObjectAttributes.OldRev != "" && event.ObjectAttributes.OldRev != event.ObjectAttributes.LastCommit.ID {
				return buildRepoFromEvent(event), nil
			}
			log.Trace().Msgf("Skipping update event sha didn't change")
		case "open", "reopen":
			return buildRepoFromEvent(event), nil
		default:
			log.Trace().Msgf("Unhandled Action %s", event.ObjectAttributes.Action)
			return nil, fmt.Errorf("unhandled action %s", event.ObjectAttributes.Action)
		}
	default:
		log.Trace().Msgf("Unhandled Event: %T", event)
		return nil, fmt.Errorf("unhandled Event %T", event)
	}
	return nil, fmt.Errorf("unhandled Event %T", eventRequest)
}

func buildRepoFromEvent(event *gitlab.MergeEvent) *repo.Repo {
	fmt.Printf("%+v\n", event)
	return &repo.Repo{
		BaseRef:       event.ObjectAttributes.TargetBranch,
		HeadRef:       event.ObjectAttributes.SourceBranch,
		DefaultBranch: event.Project.DefaultBranch,
		OwnerName:     event.Project.PathWithNamespace,
		CloneURL:      event.Project.GitHTTPURL,
		Name:          event.Project.Name,
		CheckID:       event.ObjectAttributes.IID,
		SHA:           event.ObjectAttributes.LastCommit.ID,
		Username:      gitlabTokenUser,
		Email:         gitlabTokenEmail,
	}
}
