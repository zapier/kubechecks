package app_watcher

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	informers "github.com/argoproj/argo-cd/v2/pkg/client/informers/externalversions/application/v1alpha1"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
)

// ApplicationSetWatcher is the controller that watches ArgoCD Application resources via the Kubernetes API
type ApplicationSetWatcher struct {
	applicationClientset appclientset.Interface
	appInformer          []cache.SharedIndexInformer
	appLister            []applisters.ApplicationSetLister

	vcsToArgoMap appdir.VcsToArgoMap
}

// NewApplicationSetWatcher creates new instance of ApplicationWatcher.
func NewApplicationSetWatcher(ctr container.Container) (*ApplicationSetWatcher, error) {
	if ctr.KubeClientSet == nil {
		return nil, fmt.Errorf("kubeCfg cannot be nil")
	}
	ctrl := ApplicationSetWatcher{
		applicationClientset: appclientset.NewForConfigOrDie(ctr.KubeClientSet.Config()),
		vcsToArgoMap:         ctr.VcsToArgoMap,
	}

	appInformer, appLister := ctrl.newApplicationSetInformerAndLister(time.Second*30, ctr.Config)

	ctrl.appInformer = appInformer
	ctrl.appLister = appLister

	return &ctrl, nil
}

// Run starts the Application CRD controller.
func (ctrl *ApplicationSetWatcher) Run(ctx context.Context) {
	log.Info().Msg("starting ApplicationSet Controller")

	defer runtime.HandleCrash()

	var wg sync.WaitGroup
	wg.Add(len(ctrl.appInformer))

	for _, informer := range ctrl.appInformer {
		go func(inf cache.SharedIndexInformer) {
			defer wg.Done()
			inf.Run(ctx.Done())
			if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
				log.Warn().Msg("Timed out waiting for caches to sync")
				return
			}
		}(informer)
	}
	wg.Wait()
}

func (ctrl *ApplicationSetWatcher) newApplicationSetInformerAndLister(refreshTimeout time.Duration, cfg config.ServerConfig) (map[string]cache.SharedIndexInformer, map[string]applisters.ApplicationSetLister) {
	totalNamespaces := append(cfg.MonitorAppsNamespaces, cfg.ArgoCDNamespace)
	totalInformers := make(map[string]cache.SharedIndexInformer)
	totalAppSetListers := make(map[string]applisters.ApplicationSetLister)
	for _, ns := range totalNamespaces {
		log.Debug().Msgf("Creating ApplicationSet informer with namespace: %s", ns)
		informer := informers.NewApplicationSetInformer(ctrl.applicationClientset, ns, refreshTimeout,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		)

		AppSetLister := applisters.NewApplicationSetLister(informer.GetIndexer())
		if _, err := informer.AddEventHandler(
			cache.ResourceEventHandlerFuncs{
				AddFunc:    ctrl.onApplicationSetAdded,
				UpdateFunc: ctrl.onApplicationSetUpdated,
				DeleteFunc: ctrl.onApplicationSetDeleted,
			},
		); err != nil {
			log.Error().Err(err).Msg("failed to add event handler for Application Set")
		}
		totalInformers[ns] = informer
		totalAppSetListers[ns] = AppSetLister
	}
	return totalInformers, totalAppSetListers
}

// onAdd is the function executed when the informer notifies the
// presence of a new Application in the namespace
func (ctrl *ApplicationSetWatcher) onApplicationSetAdded(obj interface{}) {
	appSet, ok := canProcessAppSet(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Error().Err(err).Msg("appsetwatcher: could not get key for added application")
	}
	log.Info().Str("key", key).Msg("appsetwatcher: onApplicationAdded")
	ctrl.vcsToArgoMap.AddAppSet(appSet)
}

func (ctrl *ApplicationSetWatcher) onApplicationSetUpdated(old, new interface{}) {
	newApp, newOk := canProcessAppSet(new)
	oldApp, oldOk := canProcessAppSet(old)
	if !newOk || !oldOk {
		return
	}

	key, err := cache.MetaNamespaceKeyFunc(new)
	if err != nil {
		log.Warn().Err(err).Msg("appsetwatcher: could not get key for updated applicationset")
	}

	// We want to update when any of Source or Sources parameters has changed
	if !reflect.DeepEqual(oldApp.Spec.Template.Spec.GetSource(), newApp.Spec.Template.Spec.GetSource()) || !reflect.DeepEqual(oldApp.Spec.Template.Spec.GetSources(), newApp.Spec.Template.Spec.GetSources()) {
		log.Info().Str("key", key).Msg("appsetwatcher: onApplicationSetUpdated")
		ctrl.vcsToArgoMap.UpdateAppSet(old.(*appv1alpha1.ApplicationSet), new.(*appv1alpha1.ApplicationSet))
	}

}

func (ctrl *ApplicationSetWatcher) onApplicationSetDeleted(obj interface{}) {
	app, ok := canProcessAppSet(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Warn().Err(err).Msg("appsetwatcher: could not get key for deleted applicationset")
	}

	log.Info().Str("key", key).Msg("appsetwatcher: onApplicationSetDeleted")
	ctrl.vcsToArgoMap.DeleteAppSet(app)
}
func canProcessAppSet(obj interface{}) (*appv1alpha1.ApplicationSet, bool) {
	app, ok := obj.(*appv1alpha1.ApplicationSet)
	if !ok {
		return nil, false
	}

	for _, src := range app.Spec.Template.Spec.GetSources() {
		if isGitRepo(src.RepoURL) {
			return app, true
		}
	}

	if isGitRepo(app.Spec.Template.Spec.GetSource().RepoURL) {
		return app, true
	}

	return app, false
}
