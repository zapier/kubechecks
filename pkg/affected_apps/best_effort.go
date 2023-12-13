package affected_apps

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/config"
)

var KustomizeSubPaths = []string{"base/", "bases/", "components/", "overlays/", "resources/"}

type BestEffort struct {
	repoName     string
	repoFileList []string
}

func NewBestEffortMatcher(repoName string, repoFileList []string) *BestEffort {
	return &BestEffort{
		repoName:     repoName,
		repoFileList: repoFileList,
	}
}

func (b *BestEffort) AffectedApps(_ context.Context, changeList []string) (AffectedItems, error) {
	appsMap := make(map[string]string)

	for _, file := range changeList {
		fileParts := strings.Split(file, "/")
		// Expected structure is /apps/<app_name>/<cluster_names> or /manifests/<cluster_names>
		// and thus anything shorter than 3 elements isn't an Argo manifest
		if len(fileParts) < 3 {
			continue
		}
		// If using the /apps/ pattern, the application name is cluster-app from the fileparts
		if fileParts[0] == "apps" {
			if isKustomizeApp(file) {
				if isKustomizeBaseComponentsChange(file) {
					// return all apps in overlays dir adjacent to the change dir
					oversDir := overlaysDir(file)
					for _, repoFile := range b.repoFileList {
						if strings.Contains(repoFile, oversDir) {
							repoFileParts := strings.Split(repoFile, "/")
							appName := fmt.Sprintf("%s-%s", repoFileParts[3], fileParts[1])
							appPath := fmt.Sprintf("%s%s/", oversDir, repoFileParts[3])
							log.Debug().Str("app", appName).Str("path", appPath).Msg("adding application to map")
							appsMap[appName] = appPath
						}
					}
				} else {
					appsMap[fmt.Sprintf("%s-%s", fileParts[3], fileParts[1])] = fmt.Sprintf("%s/%s/%s/%s/", fileParts[0], fileParts[1], fileParts[2], fileParts[3])
				}
			} else {
				// helm
				if isHelmClusterAppFile(file) {
					appsMap[fmt.Sprintf("%s-%s", fileParts[2], fileParts[1])] = fmt.Sprintf("%s/%s/%s/", fileParts[0], fileParts[1], fileParts[2])
				} else {
					// touching a file that is at the helm root, return list of all cluster apps below this dir
					appDir := filepath.Dir(file)
					for _, repoFile := range b.repoFileList {
						dir := filepath.Dir(repoFile)
						if dir != appDir && strings.Contains(dir, appDir) {
							repoFileParts := strings.Split(dir, "/")
							if len(repoFileParts) > 2 && len(fileParts) > 1 {
								appName := fmt.Sprintf("%s-%s", repoFileParts[2], fileParts[1])
								appPath := fmt.Sprintf("%s/%s/", appDir, repoFileParts[2])
								log.Debug().Str("app", appName).Str("path", appPath).Msg("adding application to map")
								appsMap[appName] = appPath
							} else {
								log.Warn().Str("dir", dir).Msg("ignoring dir")
							}
						}
					}
				}
			}
		}
		// If using the /manifests/ pattern, we need the repo name to use as the app
		if fileParts[0] == "manifests" || fileParts[0] == "charts" {
			appsMap[fmt.Sprintf("%s-%s", fileParts[1], b.repoName)] = fmt.Sprintf("%s/%s/", fileParts[0], fileParts[1])
		}
	}

	var appsSlice []config.ApplicationStub
	for name, path := range appsMap {
		appsSlice = append(appsSlice, config.ApplicationStub{Name: name, Path: path})
	}

	return AffectedItems{Applications: appsSlice}, nil
}

func isHelmClusterAppFile(file string) bool {
	dir := filepath.Dir(file)
	return len(strings.Split(dir, "/")) > 2
}

func isKustomizeApp(file string) bool {
	if file == "kustomization.yaml" {
		return true
	} else {
		for _, sub := range KustomizeSubPaths {
			if strings.Contains(file, sub) {
				return true
			}
		}
	}
	return false
}

func isKustomizeBaseComponentsChange(file string) bool {
	return strings.Contains(file, "base/") ||
			strings.Contains(file, "bases/") ||
			strings.Contains(file, "components/") ||
			strings.Contains(file, "resources/")
}

func overlaysDir(file string) string {
	appBaseDir := filepath.Dir(filepath.Dir(file))
	overlays := filepath.Join(appBaseDir, "overlays/")

	return overlays + "/"
}
