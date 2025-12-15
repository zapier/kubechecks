package argo_client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/project"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/settings"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v3/util/argo"
	"github.com/argoproj/argo-cd/v3/util/db"
	argosettings "github.com/argoproj/argo-cd/v3/util/settings"
	"github.com/argoproj/argo-cd/v3/util/tgzstream"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/kustomize"
	"github.com/zapier/kubechecks/pkg/vcs"
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

type getRepo func(ctx context.Context, cloneURL string, branchName string) (*git.Repo, error)

func (a *ArgoClient) GetManifests(ctx context.Context, name string, app v1alpha1.Application, pullRequest vcs.PullRequest, getRepo getRepo) ([]string, error) {
	ctx, span := tracer.Start(ctx, "GetManifests")
	defer span.End()

	log.Debug().Caller().Str("name", name).Msg("GetManifests")

	start := time.Now()
	defer func() {
		duration := time.Since(start)
		getManifestsDuration.WithLabelValues(name).Observe(duration.Seconds())
	}()

	contentRefs, refs := preprocessSources(&app, pullRequest)

	var manifests []string
	for _, source := range contentRefs {
		moreManifests, err := a.generateManifests(ctx, app, source, refs, pullRequest, getRepo)
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate manifests")
		}
		manifests = append(manifests, moreManifests...)
	}

	getManifestsSuccess.WithLabelValues(name).Inc()
	return manifests, nil
}

// preprocessSources splits the content sources from the ref sources, and transforms source refs that point at the pull
// request's base into refs that point at the pull request's head. This is necessary to generate manifests based on what
// the world will look like _after_ the branch gets merged in.
func preprocessSources(app *v1alpha1.Application, pullRequest vcs.PullRequest) ([]v1alpha1.ApplicationSource, []v1alpha1.ApplicationSource) {
	if !app.Spec.HasMultipleSources() {
		return []v1alpha1.ApplicationSource{app.Spec.GetSource()}, nil
	}

	// collect all ref sources, map by name
	var contentSources []v1alpha1.ApplicationSource
	var refSources []v1alpha1.ApplicationSource

	for _, source := range app.Spec.Sources {
		if source.Ref == "" {
			contentSources = append(contentSources, source)
			continue
		}

		/*
			This is to make sure that the respository server understands where to pull the values.yaml file from.

			Or put differently:

			| PR Repo   | PR Base     | PR Target | Ref Repo  | Ref Target |                                                                                                 |
			| --------- | ----------- | --------- | --------- | ---------- | ----------------------------------------------------------------------------------------------- |
			| repo1.git | new-feature | main      | repo1.git | main       | need to change main to new-feature for preview, as the base will become the target after merge. |
			| repo1.git | new-feature | main      | repo2.git | main       | no change, ref source refers to a different repository unaffected by the pull request           |
			| repo1.git | new-feature | main      | repo1.git | staging    | no change, ref source refers to a different branch than the pull request                        |
		*/
		if pkg.AreSameRepos(source.RepoURL, pullRequest.CloneURL) {
			if source.TargetRevision == pullRequest.BaseRef {
				source.TargetRevision = pullRequest.HeadRef
			}
		}

		refSources = append(refSources, source)
	}

	return contentSources, refSources
}

// generateManifests generates an Application along with all of its files, and sends it to the ArgoCD
// Repository service to be transformed into raw kubernetes manifests. This allows us to take advantage of server
// configuration and credentials.
func (a *ArgoClient) generateManifests(ctx context.Context, app v1alpha1.Application, source v1alpha1.ApplicationSource, refs []v1alpha1.ApplicationSource, pullRequest vcs.PullRequest, getRepo func(ctx context.Context, cloneURL string, branchName string) (*git.Repo, error)) ([]string, error) {
	// The GenerateManifestWithFiles has some non-obvious rules due to assumptions that it makes:
	// 1. first source must be a non-ref source
	// 2. there must be one and only one non-ref source
	// 3. ref sources that match the pull requests' repo and target branch need to have their target branch swapped to the head branch of the pull request
	log.Info().Str("app", app.Name).Msg("generating manifests")
	clusterCloser, clusterClient := a.GetClusterClient()
	defer pkg.WithErrorLogging(clusterCloser.Close, "failed to close connection")

	clusterData, err := clusterClient.Get(ctx, &cluster.ClusterQuery{Name: app.Spec.Destination.Name, Server: app.Spec.Destination.Server})
	if err != nil {
		getManifestsFailed.WithLabelValues(app.Name).Inc()
		return nil, errors.Wrap(err, "failed to get cluster")
	}

	settingsCloser, settingsClient := a.GetSettingsClient()
	defer pkg.WithErrorLogging(settingsCloser.Close, "failed to close connection")

	log.Debug().Caller().Str("app", app.Name).Msg("get settings")
	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		getManifestsFailed.WithLabelValues(app.Name).Inc()
		return nil, errors.Wrap(err, "failed to get settings")
	}

	settingsMgr := argosettings.NewSettingsManager(ctx, a.k8s, a.cfg.ArgoCDNamespace)
	argoDB := db.NewDB(a.cfg.ArgoCDNamespace, settingsMgr, a.k8s)

	repoTarget := source.TargetRevision
	if pkg.AreSameRepos(source.RepoURL, pullRequest.CloneURL) && areSameTargetRef(source.TargetRevision, pullRequest.BaseRef) {
		repoTarget = pullRequest.HeadRef
	}

	log.Debug().Caller().Str("app", app.Name).Msg("get repo")
	repo, err := getRepo(ctx, source.RepoURL, repoTarget)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get repo")
	}

	var packageDir string
	if a.cfg.ArgoCDSendFullRepository {
		log.Debug().Caller().Str("app", app.Name).Msg("sending full repository")
		packageDir = repo.Directory
	} else {
		log.Debug().Caller().Str("app", app.Name).Msg("packaging app")
		packageDir, err = packageApp(ctx, source, refs, repo, getRepo)
		if err != nil {
			return nil, errors.Wrap(err, "failed to package application")
		}
	}

	log.Debug().Caller().Str("app", app.Name).Msg("compressing files")

	exclude := []string{}
	if !a.cfg.ArgoCDIncludeDotGit {
		exclude = append(exclude, ".git")
	}

	f, filesWritten, checksum, err := tgzstream.CompressFiles(packageDir, []string{"*"}, exclude)
	if err != nil {
		return nil, fmt.Errorf("failed to compress files: %w", err)
	}
	log.Debug().Caller().Str("app", app.Name).Msgf("%d files compressed", filesWritten)
	//if filesWritten == 0 {
	//	return nil, fmt.Errorf("no files to send")
	//}

	closer, projectClient, err := a.client.NewProjectClient()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get project client")
	}
	defer pkg.WithErrorLogging(closer.Close, "failed to close connection")

	proj, err := projectClient.Get(ctx, &project.ProjectQuery{Name: app.Spec.Project})
	if err != nil {
		return nil, fmt.Errorf("error getting app project: %w", err)
	}

	helmRepos, err := argoDB.ListHelmRepositories(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing helm repositories: %w", err)
	}
	permittedHelmRepos, err := argo.GetPermittedRepos(proj, helmRepos)
	if err != nil {
		return nil, fmt.Errorf("error retrieving permitted repos: %w", err)
	}
	helmRepositoryCredentials, err := argoDB.GetAllHelmRepositoryCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting helm repository credentials: %w", err)
	}
	helmOptions, err := settingsMgr.GetHelmSettings()
	if err != nil {
		return nil, fmt.Errorf("error getting helm settings: %w", err)
	}
	permittedHelmCredentials, err := argo.GetPermittedReposCredentials(proj, helmRepositoryCredentials)
	if err != nil {
		return nil, fmt.Errorf("error getting permitted repos credentials: %w", err)
	}
	enabledSourceTypes, err := settingsMgr.GetEnabledSourceTypes()
	if err != nil {
		return nil, fmt.Errorf("error getting settings enabled source types: %w", err)
	}

	refSources, err := argo.GetRefSources(context.Background(), app.Spec.Sources, app.Spec.Project, argoDB.GetRepository, []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ref sources: %w", err)
	}

	app.Spec.Sources = append([]v1alpha1.ApplicationSource{source}, refs...)

	q := repoapiclient.ManifestRequest{
		Repo:               &v1alpha1.Repository{Repo: source.RepoURL},
		Revision:           source.TargetRevision,
		AppLabelKey:        argoSettings.AppLabelKey,
		AppName:            app.Name,
		Namespace:          app.Spec.Destination.Namespace,
		ApplicationSource:  &source,
		Repos:              permittedHelmRepos,
		KustomizeOptions:   argoSettings.KustomizeOptions,
		KubeVersion:        clusterData.Info.ServerVersion,
		ApiVersions:        clusterData.Info.APIVersions,
		HelmRepoCreds:      permittedHelmCredentials,
		HelmOptions:        helmOptions,
		TrackingMethod:     argoSettings.TrackingMethod,
		EnabledSourceTypes: enabledSourceTypes,
		ProjectName:        proj.Name,
		ProjectSourceRepos: proj.Spec.SourceRepos,
		HasMultipleSources: app.Spec.HasMultipleSources(),
		RefSources:         refSources,
	}

	// creating a new client forces grpc to create a new connection, which causes
	//the k8s load balancer to select a new pod, balancing requests among all repo-server pods.
	repoClient, conn, err := a.createRepoServerClient()
	if err != nil {
		return nil, errors.Wrap(err, "error creating repo client")
	}
	defer pkg.WithErrorLogging(conn.Close, "failed to close connection")

	log.Debug().Caller().Str("app", app.Name).Msg("generating manifest with files")
	stream, err := repoClient.GenerateManifestWithFiles(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get manifests with files")
	}
	defer pkg.WithErrorLogging(stream.CloseSend, "failed to close stream")

	log.Debug().Caller().Str("app", app.Name).Msg("sending request to repo server")
	if err := stream.Send(&repoapiclient.ManifestRequestWithFiles{
		Part: &repoapiclient.ManifestRequestWithFiles_Request{
			Request: &q,
		},
	}); err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}

	log.Debug().Caller().Str("app", app.Name).Msg("sending metadata to repo server")
	if err := stream.Send(&repoapiclient.ManifestRequestWithFiles{
		Part: &repoapiclient.ManifestRequestWithFiles_Metadata{
			Metadata: &repoapiclient.ManifestFileMetadata{
				Checksum: checksum,
			},
		},
	}); err != nil {
		return nil, errors.Wrap(err, "failed to send metadata")
	}

	err = sendFile(ctx, stream, f)
	if err != nil {
		return nil, fmt.Errorf("failed to send manifest stream file: %w", err)
	}

	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get response")
	}

	log.Debug().Caller().Str("app", app.Name).Msg("finished generating manifests")
	return response.Manifests, nil
}

func copyDir(fs filesys.FileSystem, src, dst string) error {

	if !fs.Exists(dst) {
		// First create the destination root directory
		if err := os.MkdirAll(dst, 0o777); err != nil {
			return errors.Wrapf(err, "failed to create directory %s", dst)
		}
	}

	return filepath.Walk(src, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip root directory creation (already handled above)
		if srcPath == src {
			return nil
		}

		// Get relative path from source root
		relPath, err := filepath.Rel(src, srcPath)
		if err != nil {
			return errors.Wrapf(err, "failed to get relative path for %s", srcPath)
		}

		dstPath := filepath.Join(dst, relPath)

		// Handle directories
		if info.IsDir() {
			if err := os.MkdirAll(dstPath, 0o777); err != nil {
				return errors.Wrapf(err, "failed to create directory %s", dstPath)
			}
			return nil
		}

		// Handle regular files
		return copyFile(srcPath, dstPath)
	})
}

func copyFile(srcpath, dstpath string) error {
	dstdir := filepath.Dir(dstpath)
	if err := os.MkdirAll(dstdir, 0o777); err != nil {
		return errors.Wrap(err, "failed to make directories")
	}

	r, err := os.Open(srcpath)
	if err != nil {
		return err
	}
	defer pkg.WithErrorLogging(r.Close, "failed to close file")

	w, err := os.Create(dstpath)
	if err != nil {
		return err
	}

	defer func() {
		// Report the error, if any, from Close, but do so
		// only if there isn't already an outgoing error.
		if c := w.Close(); err == nil {
			err = c
		}
	}()

	_, err = io.Copy(w, r)
	return err
}

// processLocalHelmDependency handles copying local helm dependencies to the temp directory
func processLocalHelmDependency(
	srcAppPath string,
	destAppDir string,
	dependencyPath string,
) error {
	// Remove the file:// prefix if present
	cleanPath := strings.TrimPrefix(dependencyPath, "file://")

	// Resolve the absolute path of the dependency
	absDepPath := filepath.Join(srcAppPath, cleanPath)

	// Create the destination path in the temp directory
	destDepPath := filepath.Join(destAppDir, cleanPath)

	// Create the charts directory if it doesn't exist
	if err := os.MkdirAll(destDepPath, os.ModePerm); err != nil {
		return errors.Wrapf(err, "failed to create charts directory %s", destDepPath)
	}

	// Copy the entire dependency directory
	log.Debug().Caller().Msgf("copying helm dependency from %s to %s", absDepPath, destDepPath)
	if err := copyDir(filesys.MakeFsOnDisk(), absDepPath, destDepPath); err != nil {
		return errors.Wrapf(err, "failed to copy helm dependency from %s to %s", absDepPath, destDepPath)
	}

	return nil
}

// parseChartYAML reads and parses a Chart.yaml file to extract dependencies
func parseChartYAML(chartPath string) ([]struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Repository string `yaml:"repository"`
}, error) {
	content, err := os.ReadFile(chartPath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read Chart.yaml")
	}

	var chart struct {
		Dependencies []struct {
			Name       string `yaml:"name"`
			Version    string `yaml:"version"`
			Repository string `yaml:"repository"`
		} `yaml:"dependencies"`
	}

	if err := yaml.Unmarshal(content, &chart); err != nil {
		return nil, errors.Wrap(err, "failed to parse Chart.yaml")
	}

	return chart.Dependencies, nil
}

// packageApp packages an Argo CD application source and its dependencies into a temporary directory.
// It copies the source files and processes both Kustomize and Helm dependencies.
func packageApp(
	ctx context.Context,
	source v1alpha1.ApplicationSource,
	refs []v1alpha1.ApplicationSource,
	repo *git.Repo,
	getRepo getRepo,
) (string, error) {
	destDir, err := os.MkdirTemp("", "package-*")
	if err != nil {
		return "", errors.Wrap(err, "failed to make temp dir")
	}

	fsIface := filesys.MakeFsOnDisk()

	destAppDir := filepath.Join(destDir, source.Path)
	srcAppPath := filepath.Join(repo.Directory, source.Path)
	sourceFS := os.DirFS(repo.Directory)

	// First copy the entire source directory
	if err := copyDir(fsIface, srcAppPath, destAppDir); err != nil {
		return "", errors.Wrap(err, "failed to copy base directory")
	}

	// Process kustomization dependencies
	relKustPath := filepath.Join(source.Path, "kustomization.yaml")
	absKustPath := filepath.Join(destDir, relKustPath)
	if fsIface.Exists(absKustPath) {
		files, _, err := kustomize.ProcessKustomizationFile(sourceFS, relKustPath)
		if err != nil {
			return "", errors.Wrap(err, "failed to process kustomization dependencies")
		}
		for _, file := range files {
			err := addFile(repo.Directory, destDir, file)
			if err != nil {
				return "", errors.Wrap(err, "failed to add file")
			}
		}
	}

	// Process helm dependencies
	if source.Helm != nil {
		// Handle local helm dependencies from Chart.yaml
		chartPath := filepath.Join(srcAppPath, "Chart.yaml")
		if _, err := os.Stat(chartPath); err == nil {
			log.Debug().Caller().Str("chartPath", chartPath).Msg("processing helm dependencies")
			deps, err := parseChartYAML(chartPath)
			if err != nil {
				return "", errors.Wrap(err, "failed to parse Chart.yaml")
			}

			for _, dep := range deps {
				if strings.HasPrefix(dep.Repository, "file://") {
					log.Debug().Caller().Str("chartPath", chartPath).Msgf("processing local helm dependency %s", dep.Repository)
					if err := processLocalHelmDependency(srcAppPath, destAppDir, dep.Repository); err != nil {
						return "", errors.Wrapf(err, "failed to process local helm dependency %s", dep.Name)
					}
				}
			}
		}

		refsByName := make(map[string]v1alpha1.ApplicationSource)
		for _, ref := range refs {
			refsByName[ref.Ref] = ref
		}

		for index, valueFile := range source.Helm.ValueFiles {
			if strings.HasPrefix(valueFile, "$") {
				relPath, err := processValueReference(ctx, source, valueFile, refsByName, repo, getRepo, destDir, destAppDir)
				if err != nil {
					return "", err
				}

				source.Helm.ValueFiles[index] = relPath
				continue
			}

			if strings.Contains(valueFile, "://") {
				continue
			}

			relPath, err := filepath.Rel(source.Path, valueFile)
			if err != nil {
				return "", errors.Wrap(err, "failed to calculate relative path")
			}

			if !strings.HasPrefix(relPath, "../") {
				continue // this values file is already copied
			}

			src := filepath.Join(srcAppPath, valueFile)
			dst := filepath.Join(destAppDir, valueFile)
			if err = copyFile(src, dst); err != nil {
				if !ignoreValuesFileCopyError(source, valueFile, err) {
					return "", errors.Wrapf(err, "failed to copy file: %q", valueFile)
				}
			}
		}
	}

	return destDir, nil
}

// processValueReference processes a Helm value file reference that starts with '$' and points to another source.
// It copies the referenced value file from the source repository to a temporary location and returns the relative path.
func processValueReference(
	ctx context.Context,
	source v1alpha1.ApplicationSource,
	valueFile string,
	refsByName map[string]v1alpha1.ApplicationSource,
	repo *git.Repo,
	getRepo getRepo,
	tempDir, tempAppDir string,
) (string, error) {
	refName, refPath, err := splitRefFromPath(valueFile)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse value file")
	}

	ref, ok := refsByName[refName]
	if !ok {
		return "", errors.Wrap(err, "value file points at missing ref")
	}

	refRepo := repo
	if !pkg.AreSameRepos(ref.RepoURL, repo.CloneURL) {
		refRepo, err = getRepo(ctx, ref.RepoURL, ref.TargetRevision)
		if err != nil {
			return "", errors.Wrapf(err, "failed to clone repo: %q", ref.RepoURL)
		}
	}

	src := filepath.Join(refRepo.Directory, refPath)
	dst := filepath.Join(tempDir, ".refs", refName, refPath)
	if err = copyFile(src, dst); err != nil {
		if !ignoreValuesFileCopyError(source, valueFile, err) {
			return "", errors.Wrapf(err, "failed to copy referenced value file: %q", valueFile)
		}
	}

	relPath, err := filepath.Rel(tempAppDir, dst)
	if err != nil {
		return "", errors.Wrap(err, "failed to find a relative path")
	}
	return relPath, nil
}

func ignoreValuesFileCopyError(source v1alpha1.ApplicationSource, valueFile string, err error) bool {
	if errors.Is(err, os.ErrNotExist) && source.Helm.IgnoreMissingValueFiles {
		log.Debug().Caller().
			Str("valueFile", valueFile).
			Msg("ignore missing values file, because source.Helm.IgnoreMissingValueFiles is true")
		return true
	}

	return false
}

var valueRef = regexp.MustCompile(`^\$([^/]+)/(.*)$`)
var ErrInvalidSourceRef = errors.New("invalid value ref")

func splitRefFromPath(file string) (string, string, error) {
	match := valueRef.FindStringSubmatch(file)
	if match == nil {
		return "", "", ErrInvalidSourceRef
	}

	return match[1], match[2], nil
}

type sender interface {
	Send(*repoapiclient.ManifestRequestWithFiles) error
}

func sendFile(ctx context.Context, sender sender, file *os.File) error {
	reader := bufio.NewReader(file)
	chunk := make([]byte, 1024)
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("client stream context error: %w", err)
			}
		}
		n, err := reader.Read(chunk)
		if n > 0 {
			fr := &repoapiclient.ManifestRequestWithFiles{
				Part: &repoapiclient.ManifestRequestWithFiles_Chunk{
					Chunk: &repoapiclient.ManifestFileChunk{
						Chunk: chunk[:n],
					},
				},
			}
			if e := sender.Send(fr); e != nil {
				return fmt.Errorf("error sending stream: %w", e)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("buffer reader error: %w", err)
		}
	}
	return nil
}

func areSameTargetRef(ref1, ref2 string) bool {
	return ref1 == ref2
}
