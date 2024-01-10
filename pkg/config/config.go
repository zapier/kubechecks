package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	giturls "github.com/whilp/git-urls"

	"github.com/zapier/kubechecks/pkg/vcs"
)

type RepoURL struct {
	Host, Path string
}

func (r RepoURL) CloneURL() string {
	return fmt.Sprintf("git@%s:%s", r.Host, r.Path)
}

func buildNormalizedRepoUrl(host, path string) RepoURL {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return RepoURL{host, path}
}

func NormalizeRepoUrl(s string) (RepoURL, error) {
	var parser func(string) (*url.URL, error)

	if strings.HasPrefix(s, "http") {
		parser = url.Parse
	} else {
		parser = giturls.Parse
	}

	r, err := parser(s)
	if err != nil {
		return RepoURL{}, err
	}

	return buildNormalizedRepoUrl(r.Host, r.Path), nil
}

func (v2a *VcsToArgoMap) AddApp(app *v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	appDirectory := v2a.GetAppsInRepo(app.Spec.Source.RepoURL)
	appDirectory.ProcessApp(*app)
}

func (v2a *VcsToArgoMap) UpdateApp(old *v1alpha1.Application, new *v1alpha1.Application) {
	if new.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", new.Namespace, new.Name)
		return
	}

	oldAppDirectory := v2a.GetAppsInRepo(old.Spec.Source.RepoURL)
	oldAppDirectory.RemoveApp(*old)

	newAppDirectory := v2a.GetAppsInRepo(new.Spec.Source.RepoURL)
	newAppDirectory.ProcessApp(*new)
}

func (v2a *VcsToArgoMap) DeleteApp(app *v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	oldAppDirectory := v2a.GetAppsInRepo(app.Spec.Source.RepoURL)
	oldAppDirectory.RemoveApp(*app)
}

type ServerConfig struct {
	UrlPrefix     string
	WebhookSecret string
	VcsToArgoMap  VcsToArgoMap
	VcsClient     vcs.Client
}

func (cfg *ServerConfig) GetVcsRepos() []string {
	var repos []string
	for key := range cfg.VcsToArgoMap.appDirByRepo {
		repos = append(repos, key.CloneURL())
	}
	return repos
}
