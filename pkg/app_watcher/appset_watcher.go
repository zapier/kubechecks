package app_watcher

import (
	"context"
	"fmt"
	"reflect"
	"time"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	informers "github.com/argoproj/argo-cd/v2/pkg/client/informers/externalversions/application/v1alpha1"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// ApplicationSetWatcher is the controller that watches ArgoCD Application resources via the Kubernetes API
type ApplicationSetWatcher struct {
	applicationClientset appclientset.Interface
	appInformer          cache.SharedIndexInformer
	appLister            applisters.ApplicationSetLister

	vcsToArgoMap appdir.VcsToArgoMap
}

// NewApplicationSetWatcher creates new instance of ApplicationWatcher.
func NewApplicationSetWatcher(kubeCfg *rest.Config, vcsToArgoMap appdir.VcsToArgoMap, cfg config.ServerConfig) (*ApplicationSetWatcher, error) {
	if kubeCfg == nil {
		return nil, fmt.Errorf("kubeCfg cannot be nil")
	}
	ctrl := ApplicationSetWatcher{
		applicationClientset: appclientset.NewForConfigOrDie(kubeCfg),
		vcsToArgoMap:         vcsToArgoMap,
	}

	appInformer, appLister := ctrl.newApplicationSetInformerAndLister(time.Second*30, cfg)

	ctrl.appInformer = appInformer
	ctrl.appLister = appLister

	return &ctrl, nil
}

// Run starts the Application CRD controller.
func (ctrl *ApplicationSetWatcher) Run(ctx context.Context) {
	log.Info().Msg("starting ApplicationSet Controller")

	defer runtime.HandleCrash()

	go ctrl.appInformer.Run(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), ctrl.appInformer.HasSynced) {
		log.Error().Msg("Timed out waiting for caches to sync")
		return
	}

	<-ctx.Done()
}

func (ctrl *ApplicationSetWatcher) newApplicationSetInformerAndLister(refreshTimeout time.Duration, cfg config.ServerConfig) (cache.SharedIndexInformer, applisters.ApplicationSetLister) {
	log.Debug().Msgf("Creating ApplicationSet informer with namespace: %s", cfg.ArgoCDNamespace)
	informer := informers.NewApplicationSetInformer(ctrl.applicationClientset, cfg.ArgoCDNamespace, refreshTimeout,
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
	return informer, AppSetLister
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
