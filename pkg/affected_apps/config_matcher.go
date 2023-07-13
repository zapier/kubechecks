package affected_apps

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg/argo_client"
	"github.com/zapier/kubechecks/pkg/repo_config"
)

type ConfigMatcher struct {
	cfg        *repo_config.Config
	argoClient *argo_client.ArgoClient
}

func NewConfigMatcher(cfg *repo_config.Config) *ConfigMatcher {
	argoClient := argo_client.GetArgoClient()
	return &ConfigMatcher{cfg: cfg, argoClient: argoClient}
}

func (b *ConfigMatcher) AffectedApps(ctx context.Context, changeList []string) (AffectedItems, error) {
	appList := make(map[string]string)
	var appSetList []string

	triggeredApps, triggeredAppsets, err := b.triggeredApps(ctx, changeList)
	if err != nil {
		return AffectedItems{}, err
	}

	for _, app := range triggeredApps {
		appList[app.Name] = app.Path
	}

	for _, appset := range triggeredAppsets {
		appSetList = append(appSetList, appset.Name)
	}

	return AffectedItems{appList, appSetList}, nil
}

func (b *ConfigMatcher) triggeredApps(ctx context.Context, modifiedFiles []string) ([]*repo_config.ArgoCdApplicationConfig, []*repo_config.ArgocdApplicationSetConfig, error) {
	triggeredAppsMap := map[string]*repo_config.ArgoCdApplicationConfig{}
	triggeredAppsetsMap := map[string]*repo_config.ArgocdApplicationSetConfig{}

	for _, dir := range modifiedDirs(modifiedFiles) {
		apps := b.applicationsForDir(dir)

		for _, app := range apps {
			triggeredAppsMap[app.Name] = app
		}

		// Check if an appset is modified and fetch it's apps
		if len(apps) == 0 {
			appsets, appsetApps, err := b.appsFromApplicationSetForDir(ctx, dir)
			if err != nil {
				return apps, appsets, fmt.Errorf("failed to get modified apps from modified appsets: %v", err.Error())
			}

			for _, appset := range appsets {
				triggeredAppsetsMap[appset.Name] = appset
			}

			for _, app := range appsetApps {
				triggeredAppsMap[app.Name] = app
			}
		}
	}

	triggeredApps := make([]*repo_config.ArgoCdApplicationConfig, 0, len(triggeredAppsMap))
	for _, v := range triggeredAppsMap {
		triggeredApps = append(triggeredApps, v)
	}

	triggeredAppsets := make([]*repo_config.ArgocdApplicationSetConfig, 0, len(triggeredAppsetsMap))
	for _, v := range triggeredAppsetsMap {
		triggeredAppsets = append(triggeredAppsets, v)
	}

	return triggeredApps, triggeredAppsets, nil
}

func (b *ConfigMatcher) applicationsForDir(dir string) []*repo_config.ArgoCdApplicationConfig {
	apps := []*repo_config.ArgoCdApplicationConfig{}
	for _, app := range b.cfg.Applications {
		if dirMatchForApp(dir, app.Path) {
			apps = append(apps, app)
			continue
		}

		for _, path := range app.AdditionalPaths {
			if dirMatchForApp(dir, path) {
				apps = append(apps, app)
				continue
			}
		}

	}

	return apps
}

// appsFromApplicationSetForDir: Get the list of apps managed by an applicationset from dir
func (b *ConfigMatcher) appsFromApplicationSetForDir(ctx context.Context, dir string) ([]*repo_config.ArgocdApplicationSetConfig, []*repo_config.ArgoCdApplicationConfig, error) {
	appsets := []*repo_config.ArgocdApplicationSetConfig{}
	for _, appset := range b.cfg.ApplicationSets {
		for _, path := range appset.Paths {
			if dirMatchForAppSet(dir, path) {
				appsets = append(appsets, appset)
				break
			}
		}
	}

	apps := []*repo_config.ArgoCdApplicationConfig{}
	for _, appset := range appsets {
		appList, err := b.argoClient.GetApplicationsByAppset(ctx, appset.Name)
		if err != nil {
			return appsets, apps, err
		}

		for _, app := range appList.Items {
			apps = append(apps, &repo_config.ArgoCdApplicationConfig{
				Name:              app.Name,
				Cluster:           app.Spec.Destination.Name,
				Path:              app.Spec.Source.Path,
				EnableConfTest:    appset.EnableConfTest,
				EnableKubeConform: appset.EnableKubeConform,
				EnableKubePug:     appset.EnableKubePug,
			})
		}
	}
	return appsets, apps, nil
}

func dirMatchForApp(changeDir, appDir string) bool {
	// normalize dir for matching
	appDir = path.Clean(appDir)
	changeDir = path.Clean(changeDir)

	if strings.HasSuffix(changeDir, appDir) {
		return true
	} else if changeDir == "." && appDir == "/" {
		return true
	}
	return false
}

// Any files modified under appset subdirectories assumes the appset is modified
func dirMatchForAppSet(changeDir, appSetDir string) bool {
	// normalize dir for matching
	appSetDir = path.Clean(appSetDir)
	changeDir = path.Clean(changeDir)

	log.Debug().Msgf("appSetDir: %s; changeDir: %s", appSetDir, changeDir)

	if strings.HasSuffix(changeDir, appSetDir) {
		return true
	} else if strings.HasPrefix(changeDir, appSetDir) {
		return true
	} else if changeDir == "." && appSetDir == "/" {
		return true
	}
	return false
}
