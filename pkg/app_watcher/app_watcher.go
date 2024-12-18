package app_watcher

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"github.com/argoproj/argo-cd/v2/util/glob"
	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
)

// ApplicationWatcher is the controller that watches ArgoCD Application resources via the Kubernetes API
type ApplicationWatcher struct {
	applicationClientset appclientset.Interface
	appInformer          cache.SharedIndexInformer
	appLister            applisters.ApplicationLister

	vcsToArgoMap appdir.VcsToArgoMap
}

// NewApplicationWatcher creates a new instance of ApplicationWatcher.
//
//   - kubeCfg is the Kubernetes configuration.
//   - vcsToArgoMap is the mapping between VCS and Argo applications.
//   - cfg is the server configuration.
func NewApplicationWatcher(ctr container.Container, ctx context.Context) (*ApplicationWatcher, error) {
	if ctr.KubeClientSet == nil {
		return nil, fmt.Errorf("kubeCfg cannot be nil")
	}
	ctrl := ApplicationWatcher{
		applicationClientset: appclientset.NewForConfigOrDie(ctr.KubeClientSet.Config()),
		vcsToArgoMap:         ctr.VcsToArgoMap,
	}

	appInformer, appLister := ctrl.newApplicationInformerAndLister(time.Second*30, ctr.Config, ctx)

	ctrl.appInformer = appInformer
	ctrl.appLister = appLister

	return &ctrl, nil
}

// Run starts the Application CRD controller.
func (ctrl *ApplicationWatcher) Run(ctx context.Context, processors int) {
	log.Info().Msg("starting Application Controller")

	defer runtime.HandleCrash()

	go ctrl.appInformer.Run(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), ctrl.appInformer.HasSynced) {
		log.Error().Msg("Timed out waiting for caches to sync")
		return
	}

	<-ctx.Done()
}

// onAdd is the function executed when the informer notifies the
// presence of a new Application in the namespace
func (ctrl *ApplicationWatcher) onApplicationAdded(obj interface{}) {
	app, ok := canProcessApp(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Error().Err(err).Msg("appwatcher: could not get key for added application")
	}
	log.Info().Str("key", key).Msg("appwatcher: onApplicationAdded")
	ctrl.vcsToArgoMap.AddApp(app)
}

func (ctrl *ApplicationWatcher) onApplicationUpdated(old, new interface{}) {
	newApp, newOk := canProcessApp(new)
	oldApp, oldOk := canProcessApp(old)
	if !newOk || !oldOk {
		return
	}

	key, err := cache.MetaNamespaceKeyFunc(new)
	if err != nil {
		log.Warn().Err(err).Msg("appwatcher: could not get key for updated application")
	}

	// We want to update when any of Source or Sources parameters has changed
	if !reflect.DeepEqual(oldApp.Spec.Source, newApp.Spec.Source) || !reflect.DeepEqual(oldApp.Spec.Sources, newApp.Spec.Sources) {
		log.Info().Str("key", key).Msg("appwatcher: onApplicationUpdated")
		ctrl.vcsToArgoMap.UpdateApp(old.(*appv1alpha1.Application), new.(*appv1alpha1.Application))
	}

}

func (ctrl *ApplicationWatcher) onApplicationDeleted(obj interface{}) {
	app, ok := canProcessApp(obj)
	if !ok {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Warn().Err(err).Msg("appwatcher: could not get key for deleted application")
	}

	log.Info().Str("key", key).Msg("appwatcher: onApplicationDeleted")
	ctrl.vcsToArgoMap.DeleteApp(app)
}

// isAppNamespaceAllowed is used by both the ApplicationWatcher and the ApplicationSetWatcher
func isAppNamespaceAllowed(meta *metav1.ObjectMeta, cfg config.ServerConfig) bool {
	return meta.Namespace == cfg.ArgoCDNamespace || glob.MatchStringInList(cfg.AdditionalAppsNamespaces, meta.Namespace, glob.REGEXP)
}

/*
newApplicationInformerAndLister, is part of the ApplicationWatcher struct. It sets up a Kubernetes SharedIndexInformer
and a Lister for Argo CD Applications.

A SharedIndexInformer is used to watch changes to a specific type of Kubernetes resource in an efficient manner.
It significantly reduces the load on the Kubernetes API server by sharing and caching watches between all controllers
that need to observe the object.

newApplicationInformerAndLister use the data from the informer's cache to provide a read-optimized view of the cache which reduces
the load on the API Server and hides some complexity.
*/
func (ctrl *ApplicationWatcher) newApplicationInformerAndLister(refreshTimeout time.Duration, cfg config.ServerConfig, ctx context.Context) (cache.SharedIndexInformer, applisters.ApplicationLister) {

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
				appList, err := ctrl.applicationClientset.ArgoprojV1alpha1().Applications(watchNamespace).List(ctx, options)
				if err != nil {
					return nil, err
				}
				newItems := []appv1alpha1.Application{}
				for _, app := range appList.Items {
					if isAppNamespaceAllowed(&app.ObjectMeta, cfg) {
						newItems = append(newItems, app)
					}
				}
				appList.Items = newItems
				return appList, nil
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return ctrl.applicationClientset.ArgoprojV1alpha1().Applications(watchNamespace).Watch(ctx, options)
			},
		},
		&appv1alpha1.Application{},
		refreshTimeout,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	lister := applisters.NewApplicationLister(informer.GetIndexer())
	if _, err := informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    ctrl.onApplicationAdded,
			UpdateFunc: ctrl.onApplicationUpdated,
			DeleteFunc: ctrl.onApplicationDeleted,
		},
	); err != nil {
		log.Error().Err(err).Msg("failed to add event handler")
	}
	return informer, lister
}

func canProcessApp(obj interface{}) (*appv1alpha1.Application, bool) {
	app, ok := obj.(*appv1alpha1.Application)
	if !ok {
		return nil, false
	}

	if src := app.Spec.Source; src != nil {
		if isGitRepo(src.RepoURL) {
			return app, true
		}
	}

	for _, src := range app.Spec.Sources {
		if isGitRepo(src.RepoURL) {
			return app, true
		}
	}

	return app, false
}

func isGitRepo(url string) bool {
	return strings.Contains(url, "gitlab.com") || strings.Contains(url, "github.com")
}
