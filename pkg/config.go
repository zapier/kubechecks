package pkg

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	giturls "github.com/whilp/git-urls"
	"github.com/zapier/kubechecks/pkg/app_directory"
)

type repoURL struct {
	Host, Path string
}

func (r repoURL) CloneURL() string {
	return fmt.Sprintf("git@%s:%s", r.Host, r.Path)
}

func buildNormalizedRepoUrl(host, path string) repoURL {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return repoURL{host, path}
}

func normalizeRepoUrl(s string) (repoURL, error) {
	var parser func(string) (*url.URL, error)

	if strings.HasPrefix(s, "http") {
		parser = url.Parse
	} else {
		parser = giturls.Parse
	}

	r, err := parser(s)
	if err != nil {
		return repoURL{}, err
	}

	return buildNormalizedRepoUrl(r.Host, r.Path), nil
}

type VcsToArgoMap struct {
	vcsAppStubsByRepo map[repoURL]*app_directory.AppDirectory
}

func NewVcsToArgoMap() VcsToArgoMap {
	return VcsToArgoMap{
		vcsAppStubsByRepo: make(map[repoURL]*app_directory.AppDirectory),
	}
}

func (v2a *VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) *app_directory.AppDirectory {
	repoUrl, err := normalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
	}

	return v2a.vcsAppStubsByRepo[repoUrl]
}

func (v2a *VcsToArgoMap) AddApp(app v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	rawRepoUrl := app.Spec.Source.RepoURL
	cleanRepoUrl, err := normalizeRepoUrl(rawRepoUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("%s/%s: failed to parse %s", app.Namespace, app.Name, rawRepoUrl)
		return
	}

	log.Debug().Msgf("%s/%s: %s => %s", app.Namespace, app.Name, rawRepoUrl, cleanRepoUrl)

	appDirectory := v2a.vcsAppStubsByRepo[cleanRepoUrl]
	if appDirectory == nil {
		appDirectory = app_directory.NewAppDirectory()
	}
	appDirectory.AddApp(app)
	v2a.vcsAppStubsByRepo[cleanRepoUrl] = appDirectory
}

type ServerConfig struct {
	UrlPrefix     string
	WebhookSecret string
	VcsToArgoMap  VcsToArgoMap
}

func (cfg *ServerConfig) GetVcsRepos() []string {
	var repos []string
	for key := range cfg.VcsToArgoMap.vcsAppStubsByRepo {
		repos = append(repos, key.CloneURL())
	}
	return repos
}
