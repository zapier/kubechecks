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

func (v2a *VcsToArgoMap) AddApp(app v1alpha1.Application) {
	if app.Spec.Source == nil {
		log.Warn().Msgf("%s/%s: no source, skipping", app.Namespace, app.Name)
		return
	}

	appDirectory := v2a.GetAppsInRepo(app.Spec.Source.RepoURL)
	appDirectory.ProcessApp(app)
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
