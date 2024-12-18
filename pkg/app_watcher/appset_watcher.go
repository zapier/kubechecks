package app_watcher

import (
	"context"
	"fmt"
	"reflect"
	"time"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
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
func NewApplicationSetWatcher(ctr container.Container, ctx context.Context) (*ApplicationSetWatcher, error) {
	if ctr.KubeClientSet == nil {
		return nil, fmt.Errorf("kubeCfg cannot be nil")
	}
	ctrl := ApplicationSetWatcher{
		applicationClientset: appclientset.NewForConfigOrDie(ctr.KubeClientSet.Config()),
		vcsToArgoMap:         ctr.VcsToArgoMap,
	}

	appInformer, appLister := ctrl.newApplicationSetInformerAndLister(time.Second*30, ctr.Config, ctx)

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

func (ctrl *ApplicationSetWatcher) newApplicationSetInformerAndLister(refreshTimeout time.Duration, cfg config.ServerConfig, ctx context.Context) (cache.SharedIndexInformer, applisters.ApplicationSetLister) {
	watchNamespace := cfg.ArgoCDNamespace
	// If we have at least one additional namespace configured, we need to
	// watch on them all.
	if len(cfg.AdditionalAppsNamespaces) > 0 {
		watchNamespace = ""
	}

	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (apiruntime.Object, error) {
				// We are only interested in apps that exist in namespaces the
				// user wants to be enabled.
				appList, err := ctrl.applicationClientset.ArgoprojV1alpha1().ApplicationSets(watchNamespace).List(ctx, options)
				if err != nil {
					return nil, err
				}
				newItems := []appv1alpha1.ApplicationSet{}
				for _, appSet := range appList.Items {
					if isAppNamespaceAllowed(&appSet.ObjectMeta, cfg) {
						newItems = append(newItems, appSet)
					}
				}
				appList.Items = newItems
				return appList, nil
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return ctrl.applicationClientset.ArgoprojV1alpha1().ApplicationSets(watchNamespace).Watch(ctx, options)
			},
		},
		&appv1alpha1.ApplicationSet{},
		refreshTimeout,
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
