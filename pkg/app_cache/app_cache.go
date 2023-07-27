package app_cache

import (
	"path/filepath"

	"github.com/zapier/kubechecks/pkg/config"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
)

// AppSourceCache keeps a cache of ArgoCD applications and their sources in memory,
// so it can be searched quickly by various dimensions:
// * find all apps for a git repo
// * fina all apps that reference a certain file path in the repo
// The type is an alias for a map keyed by ApplicationName
type AppSourceCache map[string][]*AppSource

type AppSource struct {
	// RepoUrl is the Git Repo URL
	RepoUrl config.RepoURL
	// SourceDir is a path in the git repo that is the main directory for an app.
	SourceDir string
	// AdditionalDirs are additional directories in the git repo
	// that the app references and can impact app rendering
	AdditionalDirs []string
	// ReferencedFiles are distinct files in the git repo that the app references
	ReferencedFiles []string
	// ParentApplicationSet
	ParentApplicationSet string
}

func NewApplicationCache() AppSourceCache {
	return AppSourceCache{}
}

func (ac AppSourceCache) clone() AppSourceCache {
	// clone the map
	clone := make(AppSourceCache)
	for key, apps := range ac {
		appClone := make([]*AppSource, len(apps))
		copy(appClone, apps)
		clone[key] = appClone
	}
	return clone
}

type FilterFunc func(*AppSource) bool

func (ac AppSourceCache) filter(f FilterFunc) AppSourceCache {
	clone := ac.clone()

	// filter the cloned map
	for key, apps := range clone {
		filteredApps := []*AppSource{}
		for _, app := range apps {
			if f(app) {
				filteredApps = append(filteredApps, app)
			}
		}
		if len(filteredApps) > 0 {
			clone[key] = filteredApps
		} else {
			delete(clone, key)
		}
	}

	return clone
}

func (ac AppSourceCache) AddApp(app *v1alpha1.Application) {
	repoUrl, err := config.NormalizeRepoUrl(app.Spec.Source.RepoURL)
	if err != nil {
		log.Warn().Err(err).Str("app_name", app.Name).Msg("could not add App to cache, unable to normalize repo URL")
		return
	}

	processAppSource := func(argoSource v1alpha1.ApplicationSource) *AppSource {
		src := &AppSource{
			RepoUrl:              repoUrl,
			SourceDir:            app.Spec.Source.Path,
			AdditionalDirs:       nil,
			ReferencedFiles:      nil,
			ParentApplicationSet: "",
		}

		// handle extra helm paths
		if argoSource.IsHelm() {
			helm := argoSource.Helm
			for _, param := range helm.FileParameters {
				path := filepath.Join(app.Spec.Source.Path, param.Path)
				src.ReferencedFiles = append(src.ReferencedFiles, path)
			}

			for _, valueFilePath := range helm.ValueFiles {
				path := filepath.Join(app.Spec.Source.Path, valueFilePath)
				src.ReferencedFiles = append(src.ReferencedFiles, path)
			}
		}
		return src
	}

	sources := make([]*AppSource, len(app.Spec.Sources))
	for i, argoSrc := range app.Spec.GetSources() {
		sources[i] = processAppSource(argoSrc)
	}
	ac[app.Name] = sources

}

func (ac AppSourceCache) ByName(name string) AppSourceCache {
	//if stub, ok := ac[name]; ok {
	//	return stub
	//}
	return nil
}

// ByRepoUrl filters the app cache to those that match the source repo URL
func (ac AppSourceCache) ByRepoUrl(repoUrl string) AppSourceCache {
	return ac.filter(func(src *AppSource) bool {
		want, _ := config.NormalizeRepoUrl(repoUrl)
		return src.RepoUrl == want
	})
}

// ByRepoDirectory filters based on directory within the git repo
// Note - this does no validation of the repo itself, and it's assumed the
func (ac AppSourceCache) ByRepoDirectory(repoDir string) AppSourceCache {
	return ac.filter(func(stub *AppSource) bool {
		// TODO: apply some normalization to repoDir?
		// TODO: should we support wildcard glob matching (e.g. parent dir/*)

		if stub.SourceDir == repoDir {
			return true
		} else {
			for _, dir := range stub.AdditionalDirs {
				if dir == repoDir {
					return true
				}
			}
		}

		return false
	})
}

func (ac AppSourceCache) ByRepoFile(repoFile string) AppSourceCache {
	return ac.filter(func(stub *AppSource) bool {
		// TODO: apply some normalization to repoFile?
		for _, file := range stub.ReferencedFiles {
			if file == repoFile {
				return true
			}
		}

		return false
	})
}
