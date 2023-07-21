package app_directory

import (
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
)

type ApplicationStub struct {
	Name, Path string
}

type AppDirectory struct {
	appPaths map[string][]string // directory -> array of app names
	appFiles map[string][]string // file path -> array of app names

	appsMap map[string]ApplicationStub // app name -> app stub
}

func NewAppDirectory() *AppDirectory {
	return &AppDirectory{
		appPaths: make(map[string][]string),
		appFiles: make(map[string][]string),
		appsMap:  make(map[string]ApplicationStub),
	}
}

func (d *AppDirectory) Count() int {
	return len(d.appsMap)
}

func (d *AppDirectory) AddApp(app *v1alpha1.Application) {
	appName := app.Name

	src := app.Spec.Source
	if src == nil {
		return
	}

	// common data
	srcPath := src.Path
	d.appsMap[appName] = ApplicationStub{Name: appName, Path: srcPath}
	d.appPaths[srcPath] = append(d.appPaths[srcPath], appName)

	// handle extra helm paths
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.appFiles[path] = append(d.appFiles[path], appName)
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.appFiles[path] = append(d.appFiles[path], appName)
		}
	}
}

func (d *AppDirectory) FindAppsBasedOnChangeList(changeList []string) []ApplicationStub {
	log.Debug().Msgf("checking %d changes", len(changeList))

	appsMap := make(map[string]string)
	appsSet := make(map[string]struct{})
	for _, changePath := range changeList {
		log.Debug().Msgf("change: %s", changePath)

		for dir, appNames := range d.appPaths {
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
