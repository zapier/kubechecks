package appdir

import (
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
)

type AppDirectory struct {
	appDirs  map[string][]string // directory -> array of app names
	appFiles map[string][]string // file path -> array of app names

	appsMap map[string]v1alpha1.Application // app name -> app stub
}

func NewAppDirectory() *AppDirectory {
	return &AppDirectory{
		appDirs:  make(map[string][]string),
		appFiles: make(map[string][]string),
		appsMap:  make(map[string]v1alpha1.Application),
	}
}

func (d *AppDirectory) AppsCount() int {
	return len(d.appsMap)
}

func (d *AppDirectory) AppFilesCount() int {
	return len(d.appFiles)
}

func (d *AppDirectory) AppDirsCount() int {
	return len(d.appDirs)
}

func (d *AppDirectory) Union(other *AppDirectory) *AppDirectory {
	var join AppDirectory
	join.appsMap = mergeMaps(d.appsMap, other.appsMap, takeFirst[v1alpha1.Application])
	join.appDirs = mergeMaps(d.appDirs, other.appDirs, mergeLists[string])
	join.appFiles = mergeMaps(d.appFiles, other.appFiles, mergeLists[string])
	return &join
}

// FindAppsBasedOnChangeList receives a list of modified file paths and
// returns the list of applications that are affected by the changes.
//
// changeList: a slice of strings representing the paths of modified files.
// targetBranch: the branch name to compare against the target revision of the applications.
// e.g. changeList = ["path/to/file1", "path/to/file2"]
func (d *AppDirectory) FindAppsBasedOnChangeList(changeList []string, targetBranch string) []v1alpha1.Application {
	log.Debug().Msgf("checking %d changes", len(changeList))

	appsSet := make(map[string]struct{})
	for _, changePath := range changeList {
		log.Debug().Msgf("change: %s", changePath)
		for dir, appNames := range d.appDirs {
			if strings.HasPrefix(changePath, dir) {
				log.Debug().Msg("dir match!")
				for _, appName := range appNames {
					appsSet[appName] = struct{}{}
				}
			}
		}

		appNames, ok := d.appFiles[changePath]
		if ok {
			log.Debug().Msg("file match!")
			for _, appName := range appNames {
				appsSet[appName] = struct{}{}
			}
		}
	}

	var appsSlice []v1alpha1.Application
	for appName := range appsSet {
		app, ok := d.appsMap[appName]
		if !ok {
			log.Warn().Msgf("failed to find matched app named '%s'", appName)
			continue
		}

		if !shouldInclude(app, targetBranch) {
			log.Debug().Msgf("target revision of %s is %s and does not match '%s'", appName, getTargetRevision(app), targetBranch)
			continue
		}

		appsSlice = append(appsSlice, app)
	}

	log.Debug().Msgf("matched %d files into %d apps", len(changeList), len(appsSet))
	return appsSlice
}

func getTargetRevision(app v1alpha1.Application) string {
	return app.Spec.GetSource().TargetRevision
}

func getSourcePath(app v1alpha1.Application) string {
	return app.Spec.GetSource().Path
}

func shouldInclude(app v1alpha1.Application, targetBranch string) bool {
	targetRevision := getTargetRevision(app)
	if targetRevision == "" {
		return true
	}

	if targetRevision == targetBranch {
		return true
	}

	if targetRevision == "HEAD" {
		if targetBranch == "main" {
			return true
		}

		if targetBranch == "master" {
			return true
		}
	}

	return false
}

func (d *AppDirectory) GetApps(filter func(stub v1alpha1.Application) bool) []v1alpha1.Application {
	var result []v1alpha1.Application
	for _, value := range d.appsMap {
		if filter != nil && !filter(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (d *AppDirectory) AddApp(app v1alpha1.Application) {
	if _, exists := d.appsMap[app.Name]; exists {
		log.Debug().Msgf("app %s already exists", app.Name)
		return
	}

	appName := app.Name

	for _, src := range getSources(app) {
		sourcePath := getSourcePath(app)
		log.Debug().
			Str("appName", app.Name).
			Str("cluster-name", app.Spec.Destination.Name).
			Str("cluster-server", app.Spec.Destination.Server).
			Str("source", sourcePath).
			Msg("add app")

		d.appsMap[app.Name] = app
		d.AddDir(app.Name, sourcePath)

		// handle extra helm paths
		if helm := src.Helm; helm != nil {
			for _, param := range helm.FileParameters {
				path := filepath.Join(sourcePath, param.Path)
				d.AddFile(appName, path)
			}

			for _, valueFilePath := range helm.ValueFiles {
				path := filepath.Join(sourcePath, valueFilePath)
				d.AddFile(appName, path)
			}
		}
	}
}

func getSources(app v1alpha1.Application) []v1alpha1.ApplicationSource {
	if !app.Spec.HasMultipleSources() {
		return []v1alpha1.ApplicationSource{*app.Spec.Source}
	}

	return app.Spec.Sources
}

func (d *AppDirectory) AddDir(appName, path string) {
	d.appDirs[path] = append(d.appDirs[path], appName)
}

func (d *AppDirectory) AddFile(appName, path string) {
	d.appFiles[path] = append(d.appFiles[path], appName)
}

func (d *AppDirectory) RemoveApp(app v1alpha1.Application) {
	log.Debug().
		Str("appName", app.Name).
		Str("cluster-name", app.Spec.Destination.Name).
		Str("cluster-server", app.Spec.Destination.Server).
		Msg("delete app")

	// remove app from appsMap
	delete(d.appsMap, app.Name)

	// Clean up app from appDirs
	sourcePath := getSourcePath(app)
	d.appDirs[sourcePath] = removeFromSlice[string](d.appDirs[sourcePath], app.Name, func(a, b string) bool { return a == b })

	// Clean up app from appFiles
	src := app.Spec.GetSource()
	srcPath := src.Path
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.appFiles[path] = removeFromSlice[string](d.appFiles[path], app.Name, func(a, b string) bool { return a == b })
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.appFiles[path] = removeFromSlice[string](d.appFiles[path], app.Name, func(a, b string) bool { return a == b })
		}
	}
}

func mergeMaps[T any](first map[string]T, second map[string]T, combine func(T, T) T) map[string]T {
	result := make(map[string]T)
	for key, value := range first {
		result[key] = value
	}
	for key, value := range second {
		exist, ok := result[key]
		if ok {
			result[key] = combine(exist, value)
		} else {
			result[key] = value
		}
	}
	return result
}

func mergeLists[T any](a []T, b []T) []T {
	return append(a, b...)
}

func takeFirst[T any](a, _ T) T {
	return a
}

func removeFromSlice[T any](slice []T, element T, equal func(T, T) bool) []T {
	for i, j := range slice {
		if equal(j, element) {
			return append(slice[:i], slice[i+1:]...)
		}
	}
	return slice
}
