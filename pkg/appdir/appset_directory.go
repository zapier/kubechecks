package appdir

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/git"
	"sigs.k8s.io/yaml"
)

// AppSetDirectory manages the mapping between ApplicationSets and their associated directories and files.
// It provides functionality to track which ApplicationSets are affected by changes in specific directories or files,
// and maintains a collection of Argo CD ApplicationSets.
type AppSetDirectory struct {
	// appSetDirs maps directory paths to the names of ApplicationSets that use those directories.
	// This is used to quickly identify which ApplicationSets are affected when changes occur in a directory.
	appSetDirs map[string][]string

	// appSetFiles maps file paths to the names of ApplicationSets that use those files.
	// This is used to quickly identify which ApplicationSets are affected when specific files change.
	appSetFiles map[string][]string

	// appSetsMap stores the full Argo CD ApplicationSet definitions, indexed by ApplicationSet name.
	// This serves as the source of truth for ApplicationSet configurations.
	appSetsMap map[string]v1alpha1.ApplicationSet
}

func NewAppSetDirectory() *AppSetDirectory {
	return &AppSetDirectory{
		appSetDirs:  make(map[string][]string),
		appSetFiles: make(map[string][]string),
		appSetsMap:  make(map[string]v1alpha1.ApplicationSet),
	}
}

func (d *AppSetDirectory) Count() int {
	return len(d.appSetsMap)
}

func (d *AppSetDirectory) Union(other *AppSetDirectory) *AppSetDirectory {
	var join AppSetDirectory
	join.appSetsMap = mergeMaps(d.appSetsMap, other.appSetsMap, takeFirst[v1alpha1.ApplicationSet])
	join.appSetDirs = mergeMaps(d.appSetDirs, other.appSetDirs, mergeLists[string])
	join.appSetFiles = mergeMaps(d.appSetFiles, other.appSetFiles, mergeLists[string])
	return &join
}

func (d *AppSetDirectory) ProcessAppSet(app v1alpha1.ApplicationSet) {
	appName := app.GetName()

	src := app.Spec.Template.Spec.GetSource()

	// common data
	srcPath := src.Path
	d.AddAppSet(&app)

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

// FindAppSetsBasedOnChangeList receives the modified file path and
// returns the list of applications that are affected by the changes.
//
//	e.g. changeList = ["/appset/httpdump/httpdump.yaml", "/app/testapp/values.yaml"]
//  if the changed file is application set file, return it.

func (d *AppSetDirectory) FindAppSetsBasedOnChangeList(changeList []string, repo *git.Repo) []v1alpha1.ApplicationSet {
	log.Debug().Caller().
		Str("type", "applicationsets").
		Msgf("checking %d changes", len(changeList))

	appsSet := make(map[string]struct{})
	var appSets []v1alpha1.ApplicationSet

	for _, changePath := range changeList {
		log.Debug().Caller().
			Msgf("changePath: %s", changePath)
		absPath := filepath.Join(repo.Directory, changePath)

		// Check if file contains `kind: ApplicationSet`
		if !containsKindApplicationSet(absPath) {
			continue
		}

		// Open the yaml file and parse it as v1alpha1.ApplicationSet
		fileContent, err := os.ReadFile(absPath)
		if err != nil {
			log.Error().Msgf("failed to open file %s: %v", absPath, err)
			continue
		}

		appSet := &v1alpha1.ApplicationSet{}
		err = yaml.Unmarshal(fileContent, appSet)
		if err != nil {
			log.Error().Msgf("failed to parse file %s as ApplicationSet: %v", absPath, err)
			continue
		}

		// Store the unique ApplicationSet
		if _, exists := appsSet[appSet.Name]; !exists {
			appsSet[appSet.Name] = struct{}{}
			appSets = append(appSets, *appSet)
		}
	}

	log.Debug().Caller().Str("source", "appset_directory").Msgf("matched %d files into %d appset", len(changeList), len(appSets))
	return appSets
}

func appSetGetSourcePath(app *v1alpha1.ApplicationSet) string {
	return app.Spec.Template.Spec.GetSource().Path
}

func (d *AppSetDirectory) GetAppSets(filter func(stub v1alpha1.ApplicationSet) bool) []v1alpha1.ApplicationSet {
	var result []v1alpha1.ApplicationSet
	for _, value := range d.appSetsMap {
		if filter != nil && !filter(value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func (d *AppSetDirectory) AddAppSet(appSet *v1alpha1.ApplicationSet) {
	if _, exists := d.appSetsMap[appSet.GetName()]; exists {
		log.Info().Msgf("appset %s already exists", appSet.Name)
		return
	}
	log.Debug().
		Caller().
		Str("appName", appSet.GetName()).
		Str("source", appSetGetSourcePath(appSet)).
		Msg("add appset")
	d.appSetsMap[appSet.GetName()] = *appSet
	d.AddDir(appSet.GetName(), appSetGetSourcePath(appSet))
}

func (d *AppSetDirectory) AddDir(appName, path string) {
	d.appSetDirs[path] = append(d.appSetDirs[path], appName)
}

func (d *AppSetDirectory) AddFile(appName, path string) {
	d.appSetFiles[path] = append(d.appSetFiles[path], appName)
}

func (d *AppSetDirectory) RemoveAppSet(app v1alpha1.ApplicationSet) {
	log.Debug().
		Caller().
		Str("appName", app.Name).
		Msg("delete app")

	// remove app from appSetsMap
	delete(d.appSetsMap, app.Name)

	// Clean up app from appSetDirs
	sourcePath := appSetGetSourcePath(&app)
	d.appSetDirs[sourcePath] = removeFromSlice[string](d.appSetDirs[sourcePath], app.Name, func(a, b string) bool { return a == b })

	// Clean up app from appSetFiles
	src := app.Spec.Template.Spec.GetSource()
	srcPath := src.Path
	if helm := src.Helm; helm != nil {
		for _, param := range helm.FileParameters {
			path := filepath.Join(srcPath, param.Path)
			d.appSetFiles[path] = removeFromSlice[string](d.appSetFiles[path], app.Name, func(a, b string) bool { return a == b })
		}

		for _, valueFilePath := range helm.ValueFiles {
			path := filepath.Join(srcPath, valueFilePath)
			d.appSetFiles[path] = removeFromSlice[string](d.appSetFiles[path], app.Name, func(a, b string) bool { return a == b })
		}
	}
}

// containsKindApplicationSet checks if the file contains kind: ApplicationSet
func containsKindApplicationSet(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		log.Error().Err(err).Stack().Msgf("failed to open file %s: %v", path, err)
		return false
	}
	defer pkg.WithErrorLogging(file.Close, "failed to close file")

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "kind: ApplicationSet") {
			log.Debug().Caller().Msgf("found kind: ApplicationSet in %s", path)
			return true
		}
	}

	if err := scanner.Err(); err != nil {
		log.Error().Err(err).Stack().Msgf("error reading file %s: %v", path, err)
	}

	return false
}
