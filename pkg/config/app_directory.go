package config

import (
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
)

type ApplicationStub struct {
	Name, Path string

	IsHelm, IsKustomize bool
}

type AppDirectory struct {
	appDirs  map[string][]string // directory -> array of app names
	appFiles map[string][]string // file path -> array of app names

	appsMap map[string]ApplicationStub // app name -> app stub
}

func NewAppDirectory() *AppDirectory {
	return &AppDirectory{
		appDirs:  make(map[string][]string),
		appFiles: make(map[string][]string),
		appsMap:  make(map[string]ApplicationStub),
	}
}

func (d *AppDirectory) Count() int {
	return len(d.appsMap)
}

func (d *AppDirectory) Union(other *AppDirectory) *AppDirectory {
	var join AppDirectory
	join.appsMap = mergeMaps(d.appsMap, other.appsMap, takeFirst[ApplicationStub])
	join.appDirs = mergeMaps(d.appDirs, other.appDirs, mergeLists[string])
	join.appFiles = mergeMaps(d.appFiles, other.appFiles, mergeLists[string])
	return &join
}

func (d *AppDirectory) ProcessApp(app v1alpha1.Application) {
	appName := app.Name

	src := app.Spec.Source
	if src == nil {
		return
	}

	// common data
	srcPath := src.Path
	d.AddAppStub(appName, srcPath, src.IsHelm(), !src.Kustomize.IsZero())

	// handle extra helm paths
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.AddFile(appName, path)
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.AddFile(appName, path)
		}
	}
}

func (d *AppDirectory) FindAppsBasedOnChangeList(changeList []string) []ApplicationStub {
	log.Debug().Msgf("checking %d changes", len(changeList))

	appsMap := make(map[string]string)
	appsSet := make(map[string]struct{})
	for _, changePath := range changeList {
		log.Debug().Msgf("change: %s", changePath)

		for dir, appNames := range d.appDirs {
			log.Debug().Msgf("- app path: %s", dir)
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

	var appsSlice []ApplicationStub
	for appName := range appsSet {
		app, ok := d.appsMap[appName]
		if !ok {
			log.Warn().Msgf("failed to find matched app named '%s'", appName)
			continue
		}
		appsSlice = append(appsSlice, app)
	}

	log.Debug().Msgf("matched %d files into %d apps", len(appsMap), len(appsSet))
	return appsSlice
}

func (d *AppDirectory) GetApps(filter func(stub ApplicationStub) bool) []ApplicationStub {
	var result []ApplicationStub
	for _, value := range d.appsMap {
		if filter != nil && !filter(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (d *AppDirectory) AddAppStub(appName, srcPath string, isHelm, isKustomize bool) {
	d.appsMap[appName] = ApplicationStub{
		Name:        appName,
		Path:        srcPath,
		IsHelm:      isHelm,
		IsKustomize: isKustomize,
	}
	d.AddDir(appName, srcPath)
}

func (d *AppDirectory) AddDir(appName, path string) {
	d.appDirs[path] = append(d.appDirs[path], appName)
}

func (d *AppDirectory) AddFile(appName, path string) {
	d.appFiles[path] = append(d.appFiles[path], appName)
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
