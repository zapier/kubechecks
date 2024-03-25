package app_watcher

import (
	"context"
	"reflect"
	"strings"
	"time"

	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	"github.com/rs/zerolog/log"
	"k8s.io/client-go/tools/clientcmd"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	informers "github.com/argoproj/argo-cd/v2/pkg/client/informers/externalversions/application/v1alpha1"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/config"
)

// ApplicationWatcher is the controller that watches ArgoCD Application resources via the Kubernetes API
type ApplicationWatcher struct {
	applicationClientset appclientset.Interface
	appInformer          cache.SharedIndexInformer
	appLister            applisters.ApplicationLister

	vcsToArgoMap appdir.VcsToArgoMap
}

// NewApplicationWatcher creates new instance of ApplicationWatcher.
func NewApplicationWatcher(vcsToArgoMap appdir.VcsToArgoMap, cfg config.ServerConfig) (*ApplicationWatcher, error) {
	// this assumes kubechecks is running inside the cluster
	kubeCfg, err := clientcmd.BuildConfigFromFlags("", cfg.KubernetesConfig)
	if err != nil {
		log.Fatal().Msgf("Error building kubeconfig: %s", err.Error())
	}

	appClient := appclientset.NewForConfigOrDie(kubeCfg)

	ctrl := ApplicationWatcher{
		applicationClientset: appClient,
		vcsToArgoMap:         vcsToArgoMap,
	}

	appInformer, appLister := ctrl.newApplicationInformerAndLister(time.Second * 30)

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

/*
This Go function, named newApplicationInformerAndLister, is part of the ApplicationWatcher struct. It sets up a Kubernetes SharedIndexInformer and a Lister for Argo CD Applications.
A SharedIndexInformer is used to watch changes to a specific type of Kubernetes resource in an efficient manner. It significantly reduces the load on the Kubernetes API server by sharing and caching watches between all controllers that need to observe the object.
Listers use the data from the informer's cache to provide a read-optimized view of the cache which reduces the load on the API Server and hides some complexity.
*/
func (ctrl *ApplicationWatcher) newApplicationInformerAndLister(refreshTimeout time.Duration) (cache.SharedIndexInformer, applisters.ApplicationLister) {
	informer := informers.NewApplicationInformer(ctrl.applicationClientset, "", refreshTimeout,
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

	for _, src := range app.Spec.Sources {
		if isGitRepo(src.RepoURL) {
			return app, true
		}
	}

	if app.Spec.Source != nil {
		if isGitRepo(app.Spec.Source.RepoURL) {
			return app, true
		}
	}

	return app, false
}

func isGitRepo(url string) bool {
	return strings.Contains(url, "gitlab.com") || strings.Contains(url, "github.com")
}
