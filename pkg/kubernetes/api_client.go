package client

import (
	"fmt"
	"log/slog"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ClusterTypes must match with the cmd/root.go kubernetes-type flag
const (
	ClusterTypeEKS   = "eks"
	ClusterTypeLOCAL = "localhost"
)

type NewClientOption func(*NewClientInput)

type NewClientInput struct {
	// ClusterType is a type of the Kubernetes cluster (required)
	ClusterType string
	// KubernetesConfigPath is a path to the kubeconfig file (optional)
	KubernetesConfigPath string
	// restConfig is the configuration for the Kubernetes client, used as a placeholder for the client creation
	restConfig *rest.Config
}

// New creates new Kubernetes clients with the specified options.
func New(input *NewClientInput, opts ...NewClientOption) (Interface, error) {

	var (
		k8sConfig *rest.Config
		err       error
	)
	if input.ClusterType == ClusterTypeLOCAL {
		if input.KubernetesConfigPath != "" {
			k8sConfig, err = clientcmd.BuildConfigFromFlags("", input.KubernetesConfigPath)
			if err != nil {
				// running the service outside kubernetes without specifying a kubeconfig file or ENVVAR will error.
				// ignore it and continue with the opts specific configuration.
				slog.Warn(err.Error(), "msg", "Error building kubeconfig using local config")
			}
		} else {
			// kubeConfigPath not provided, try rest.InClusterConfig, going to assume this service is running in a k8s cluster
			k8sConfig, err = rest.InClusterConfig()
			if err != nil {
				slog.Error("unable to load in-cluster config", "err", err)
				return nil, err
			}
		}

	}

	input.restConfig = k8sConfig

	// iterate the optional clients configuration and apply, if successful config.RestConfig will be updated
	for _, opt := range opts {
		opt(input)
	}
	if input.restConfig == nil {
		return nil, fmt.Errorf("failed to init kubernetes configuration")
	}

	clientSet, err := kubernetes.NewForConfig(input.restConfig)
	if err != nil {
		return nil, err
	}
	contClient, err := controllerClient.New(input.restConfig, controllerClient.Options{})
	if err != nil {
		return nil, err
	}
	return &kubernetesClientSet{
		clientSet:        clientSet,
		config:           input.restConfig,
		controllerClient: &contClient,
	}, nil
}

type kubernetesClientSet struct {
	clientSet        kubernetes.Interface
	config           *rest.Config
	controllerClient *controllerClient.Client
}

func (c *kubernetesClientSet) ClientSet() kubernetes.Interface {
	return c.clientSet
}

func (c *kubernetesClientSet) Config() *rest.Config {
	return c.config
}

func (c *kubernetesClientSet) ControllerClient() *controllerClient.Client {
	return c.controllerClient
}
