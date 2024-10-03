package client

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Interface interface {
	// ClientSet returns the rest clientset to be used.
	ClientSet() kubernetes.Interface
	// ControllerClient returns the controller-runtime client to be used.
	ControllerClient() *controllerClient.Client
	// Config returns the rest.Config to be used.
	Config() *rest.Config
}
