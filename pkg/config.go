package pkg

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/rs/zerolog/log"
	giturls "github.com/whilp/git-urls"
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
	//                map[string]map[AppPath]AppName
	vcsAppStubsByRepo map[repoURL]map[string]string
}

func NewVcsToArgoMap() VcsToArgoMap {
	return VcsToArgoMap{
		vcsAppStubsByRepo: make(map[repoURL]map[string]string),
	}
}

func (v2a *VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) map[string]string {
	repoUrl, err := normalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
	}

	return v2a.vcsAppStubsByRepo[repoUrl]
}

func (v2a *VcsToArgoMap) AddApp(repoCloneUrl, path, name string) {
	repoUrl, err := normalizeRepoUrl(repoCloneUrl)
	if err != nil {
		log.Warn().Err(err).Msgf("failed to parse %s", repoCloneUrl)
		return
	}

	apps, ok := v2a.vcsAppStubsByRepo[repoUrl]
	if !ok {
		apps = make(map[string]string)
		v2a.vcsAppStubsByRepo[repoUrl] = apps
	}
	apps[path] = name
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
