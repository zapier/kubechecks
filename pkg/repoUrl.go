package pkg

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/chainguard-dev/git-urls"
	"github.com/pkg/errors"
)

type RepoURL struct {
	Host, Path string
}

func (r RepoURL) CloneURL(username string) string {
	if username != "" {
		return fmt.Sprintf("https://%s@%s/%s", username, r.Host, r.Path)
	}
	return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
}

func NormalizeRepoUrl(s string) (RepoURL, url.Values, error) {
	var parser func(string) (*url.URL, error)

	if strings.HasPrefix(s, "http") {
		parser = url.Parse
	} else {
		parser = giturls.Parse
	}

	r, err := parser(s)
	if err != nil {
		return RepoURL{}, nil, err
	}

	r.Path = strings.TrimPrefix(r.Path, "/")
	r.Path = strings.TrimSuffix(r.Path, ".git")

	return RepoURL{
		Host: r.Host,
		Path: r.Path,
	}, r.Query(), nil
}

func Canonicalize(cloneURL string) (RepoURL, error) {
	parsed, _, err := NormalizeRepoUrl(cloneURL)
	if err != nil {
		return RepoURL{}, errors.Wrap(err, "failed to parse clone url")
	}

	return parsed, nil
}
