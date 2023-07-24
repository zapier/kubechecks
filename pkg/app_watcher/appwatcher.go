package app_watcher

import (
	"context"

	appclientset "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg/config"
	"k8s.io/client-go/tools/clientcmd"

	"strings"
	"time"

	appv1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	applisters "github.com/argoproj/argo-cd/v2/pkg/client/listers/application/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

// ApplicationWatcher is the controller that watches ArgoCD Application resources via the Kubernetes API
type ApplicationWatcher struct {
	cfg                  *config.ServerConfig
	applicationClientset appclientset.Interface
	appInformer          cache.SharedIndexInformer
	appCache             cache.InformerSynced
	appLister            applisters.ApplicationLister
}

// NewApplicationWatcher creates new instance of ApplicationWatcher.
func NewApplicationWatcher(cfg *config.ServerConfig) (*ApplicationWatcher, error) {
	// this assumes kubechecks is running inside the cluster
	kubeCfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		log.Fatal().Msgf("Error building kubeconfig: %s", err.Error())
	}

	appClient := appclientset.NewForConfigOrDie(kubeCfg)

	ctrl := ApplicationWatcher{
		cfg:                  cfg,
		applicationClientset: appClient,
	}

	appInformer, appLister := ctrl.newApplicationInformerAndLister()

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
	if !canProcessApp(obj) {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Error().Err(err).Msg("appwatcher: could not get key for added application")
	}
	log.Trace().Str("key", key).Msg("appwatcher: onApplicationAdded")
	ctrl.cfg.VcsToArgoMap.AddApp(obj.(*appv1alpha1.Application))
}

func (ctrl *ApplicationWatcher) onApplicationUpdated(old, new interface{}) {
	key, err := cache.MetaNamespaceKeyFunc(new)
	if err != nil {
		log.Warn().Err(err).Msg("appwatcher: could not get key for updated application")
	}
	// TODO
	// have any of the Source repoURLs changed?
	log.Trace().Str("key", key).Msg("appwatcher: onApplicationUpdated")
}

func (ctrl *ApplicationWatcher) onApplicationDeleted(obj interface{}) {
	if !canProcessApp(obj) {
		return
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Warn().Err(err).Msg("appwatcher: could not get key for deleted application")
	}

	log.Trace().Str("key", key).Msg("appwatcher: onApplicationDeleted")
}

/*
This Go function, named newApplicationInformerAndLister, is part of the ApplicationWatcher struct. It sets up a Kubernetes SharedIndexInformer and a Lister for Argo CD Applications.
A SharedIndexInformer is used to watch changes to a specific type of Kubernetes resource in an efficient manner. It significantly reduces the load on the Kubernetes API server by sharing and caching watches between all controllers that need to observe the object.
Listers use the data from the informer's cache to provide a read-optimized view of the cache which reduces the load on the API Server and hides some complexity.
*/
func (ctrl *ApplicationWatcher) newApplicationInformerAndLister() (cache.SharedIndexInformer, applisters.ApplicationLister) {
	refreshTimeout := time.Second * 30
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (apiruntime.Object, error) {
				return ctrl.applicationClientset.ArgoprojV1alpha1().Applications(ctrl.cfg.ArgoCdNamespace).List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				return ctrl.applicationClientset.ArgoprojV1alpha1().Applications(ctrl.cfg.ArgoCdNamespace).Watch(context.TODO(), options)
			},
		},
		&appv1alpha1.Application{},
		refreshTimeout,
		cache.Indexers{
			cache.NamespaceIndex: func(obj interface{}) ([]string, error) {
				return cache.MetaNamespaceIndexFunc(obj)
			},
		},
	)
	lister := applisters.NewApplicationLister(informer.GetIndexer())
	informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    ctrl.onApplicationAdded,
			UpdateFunc: ctrl.onApplicationUpdated,
			DeleteFunc: ctrl.onApplicationDeleted,
		},
	)
	return informer, lister
}

func canProcessApp(obj interface{}) bool {
	app, ok := obj.(*appv1alpha1.Application)
	if !ok {
		return false
	}

	for _, src := range app.Spec.Sources {
		if isGitRepo(src.RepoURL) {
			return true
		}
	}

	if !isGitRepo(app.Spec.Source.RepoURL) {
		return false
	}

	return true
}

func isGitRepo(url string) bool {
	return strings.Contains(url, "gitlab.com") || strings.Contains(url, "github.com")
}
