package argo_client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/project"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	repoapiclient "github.com/argoproj/argo-cd/v2/reposerver/apiclient"
	"github.com/argoproj/argo-cd/v2/util/argo"
	"github.com/argoproj/argo-cd/v2/util/db"
	argosettings "github.com/argoproj/argo-cd/v2/util/settings"
	"github.com/argoproj/argo-cd/v2/util/tgzstream"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/vcs"
)

type getRepo func(ctx context.Context, cloneURL string, branchName string) (*git.Repo, error)

func (a *ArgoClient) GetManifests(ctx context.Context, name string, app v1alpha1.Application, pullRequest vcs.PullRequest, getRepo getRepo) ([]string, error) {
	ctx, span := tracer.Start(ctx, "GetManifests")
	defer span.End()

	log.Debug().Str("name", name).Msg("GetManifests")

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

	clusterCloser, clusterClient := a.GetClusterClient()
	defer clusterCloser.Close()

	cluster, err := clusterClient.Get(ctx, &cluster.ClusterQuery{Name: app.Spec.Destination.Name, Server: app.Spec.Destination.Server})
	if err != nil {
		getManifestsFailed.WithLabelValues(app.Name).Inc()
		return nil, errors.Wrap(err, "failed to get cluster")
	}

	settingsCloser, settingsClient := a.GetSettingsClient()
	defer settingsCloser.Close()

	log.Debug().Msg("get settings")
	argoSettings, err := settingsClient.Get(ctx, &settings.SettingsQuery{})
	if err != nil {
		getManifestsFailed.WithLabelValues(app.Name).Inc()
		return nil, errors.Wrap(err, "failed to get settings")
	}

	settingsMgr := argosettings.NewSettingsManager(ctx, a.k8s, a.namespace)
	argoDB := db.NewDB(a.namespace, settingsMgr, a.k8s)

	repoTarget := source.TargetRevision
	if pkg.AreSameRepos(source.RepoURL, pullRequest.CloneURL) && areSameTargetRef(source.TargetRevision, pullRequest.BaseRef) {
		repoTarget = pullRequest.HeadRef
	}

	log.Debug().Msg("get repo")
	repo, err := getRepo(ctx, source.RepoURL, repoTarget)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get repo")
	}

	var packageDir string
	if a.sendFullRepository {
		log.Info().Msg("sending full repository")
		packageDir = repo.Directory
	} else {
		log.Info().Msg("packaging app")
		packageDir, err = packageApp(ctx, source, refs, repo, getRepo)
		if err != nil {
			return nil, errors.Wrap(err, "failed to package application")
		}
	}

	log.Debug().Msg("compressing files")
	f, filesWritten, checksum, err := tgzstream.CompressFiles(packageDir, []string{"*"}, []string{".git"})
	if err != nil {
		return nil, fmt.Errorf("failed to compress files: %w", err)
	}
	log.Debug().Msgf("%d files compressed", filesWritten)
	//if filesWritten == 0 {
	//	return nil, fmt.Errorf("no files to send")
	//}

	closer, projectClient, err := a.client.NewProjectClient()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get project client")
	}
	defer closer.Close()

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

	refSources, err := argo.GetRefSources(context.Background(), app.Spec.Sources, app.Spec.Project, argoDB.GetRepository, []string{}, false)
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
		KubeVersion:        cluster.Info.ServerVersion,
		ApiVersions:        cluster.Info.APIVersions,
		HelmRepoCreds:      permittedHelmCredentials,
		HelmOptions:        helmOptions,
		TrackingMethod:     argoSettings.TrackingMethod,
		EnabledSourceTypes: enabledSourceTypes,
		ProjectName:        proj.Name,
		ProjectSourceRepos: proj.Spec.SourceRepos,
		HasMultipleSources: app.Spec.HasMultipleSources(),
		RefSources:         refSources,
	}

	log.Debug().Msg("generating manifest with files")
	stream, err := a.repoClient.GenerateManifestWithFiles(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get manifests with files")
	}

	log.Debug().Msg("sending request")
	if err := stream.Send(&repoapiclient.ManifestRequestWithFiles{
		Part: &repoapiclient.ManifestRequestWithFiles_Request{
			Request: &q,
		},
	}); err != nil {
		return nil, errors.Wrap(err, "failed to send request")
	}

	log.Debug().Msg("sending metadata")
	if err := stream.Send(&repoapiclient.ManifestRequestWithFiles{
		Part: &repoapiclient.ManifestRequestWithFiles_Metadata{
			Metadata: &repoapiclient.ManifestFileMetadata{
				Checksum: checksum,
			},
		},
	}); err != nil {
		return nil, errors.Wrap(err, "failed to send metadata")
	}

	log.Debug().Msg("sending file")
	err = sendFile(ctx, stream, f)
	if err != nil {
		return nil, fmt.Errorf("failed to send manifest stream file: %w", err)
	}

	log.Debug().Msg("receiving repsonse")
	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get response")
	}

	log.Debug().Msg("done!")
	return response.Manifests, nil
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
	defer r.Close() // ignore error: file was opened read-only.

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

func packageApp(ctx context.Context, source v1alpha1.ApplicationSource, refs []v1alpha1.ApplicationSource, repo *git.Repo, getRepo getRepo) (string, error) {
	tempDir, err := os.MkdirTemp("", "package-*")
	if err != nil {
		return "", errors.Wrap(err, "failed to make temp dir")
	}

	tempAppDir := filepath.Join(tempDir, source.Path)
	appPath := filepath.Join(repo.Directory, source.Path)

	// copy app files to the temp dir
	if err = filepath.Walk(appPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(appPath, path)
		if err != nil {
			return errors.Wrapf(err, "failed to calculate rel between %q and %q", appPath, path)
		}
		src := path
		dst := filepath.Join(tempAppDir, relPath)
		if err := copyFile(src, dst); err != nil {
			return errors.Wrapf(err, "failed to %s => %s", src, dst)
		}
		return nil
	}); err != nil {
		return "", errors.Wrap(err, "failed to copy files")
	}

	if source.Helm != nil {
		refsByName := make(map[string]v1alpha1.ApplicationSource)
		for _, ref := range refs {
			refsByName[ref.Ref] = ref
		}

		for index, valueFile := range source.Helm.ValueFiles {
			if strings.HasPrefix(valueFile, "$") {
				relPath, err := processValueReference(ctx, source, valueFile, refsByName, repo, getRepo, tempDir, tempAppDir)
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

			src := filepath.Join(appPath, valueFile)
			dst := filepath.Join(tempAppDir, valueFile)
			if err = copyFile(src, dst); err != nil {
				if !ignoreValuesFileCopyError(source, valueFile, err) {
					return "", errors.Wrapf(err, "failed to copy file: %q", valueFile)
				}
			}
		}
	}

	return tempDir, nil
}

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
		log.Debug().Str("valueFile", valueFile).Msg("ignore missing values file, because source.Helm.IgnoreMissingValueFiles is true")
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
