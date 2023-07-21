package config

import (
	"fmt"
	"net/url"
	"strings"

	giturls "github.com/whilp/git-urls"
)

type ServerConfig struct {
	ArgoCdNamespace string
	UrlPrefix       string
	WebhookSecret   string
	VcsToArgoMap    VcsToArgoMap
}

func (cfg *ServerConfig) GetVcsRepos() []string {
	var repos []string
	for key := range cfg.VcsToArgoMap.vcsAppStubsByRepo {
		repos = append(repos, key.CloneURL())
	}
	return repos
}

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
