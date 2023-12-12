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
