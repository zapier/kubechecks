package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ClusterInfo holds connection details for a cluster discovered from ArgoCD secrets.
type ClusterInfo struct {
	Name   string
	Server string
}

// ClusterClients holds the K8s clients for a single cluster.
type ClusterClients struct {
	Clientset     kubernetes.Interface
	DynamicClient dynamic.Interface
}

// MultiClusterManager provides K8s clients for clusters registered in ArgoCD.
// It reads cluster secrets from the ArgoCD namespace and caches clients.
type MultiClusterManager struct {
	localClientset kubernetes.Interface
	argoNamespace  string

	mu      sync.Mutex
	clients map[string]*ClusterClients // keyed by cluster name
}

// NewMultiClusterManager creates a new multi-cluster manager.
// localClientset is used to read ArgoCD cluster secrets from the local cluster.
func NewMultiClusterManager(localClientset kubernetes.Interface, argoNamespace string) *MultiClusterManager {
	return &MultiClusterManager{
		localClientset: localClientset,
		argoNamespace:  argoNamespace,
		clients:        make(map[string]*ClusterClients),
	}
}

// ListClusters returns the names and servers of all clusters registered in ArgoCD.
func (m *MultiClusterManager) ListClusters(ctx context.Context) ([]ClusterInfo, error) {
	secrets, err := m.localClientset.CoreV1().Secrets(m.argoNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "argocd.argoproj.io/secret-type=cluster",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list ArgoCD cluster secrets: %w", err)
	}

	var clusters []ClusterInfo
	for _, secret := range secrets.Items {
		name := string(secret.Data["name"])
		server := string(secret.Data["server"])
		if name == "" {
			name = secret.Name
		}
		clusters = append(clusters, ClusterInfo{
			Name:   name,
			Server: server,
		})
	}
	return clusters, nil
}

// GetClusterClients returns K8s clients for the named cluster.
// Clients are cached after first creation.
func (m *MultiClusterManager) GetClusterClients(ctx context.Context, clusterName string) (*ClusterClients, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if clients, ok := m.clients[clusterName]; ok {
		return clients, nil
	}

	config, err := m.getClusterConfig(ctx, clusterName)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset for cluster %q: %w", clusterName, err)
	}

	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client for cluster %q: %w", clusterName, err)
	}

	clients := &ClusterClients{
		Clientset:     clientset,
		DynamicClient: dynClient,
	}
	m.clients[clusterName] = clients

	log.Debug().Caller().Str("cluster", clusterName).Msg("created K8s clients for cluster")
	return clients, nil
}

// getClusterConfig reads the ArgoCD cluster secret and builds a rest.Config.
func (m *MultiClusterManager) getClusterConfig(ctx context.Context, clusterName string) (*rest.Config, error) {
	secrets, err := m.localClientset.CoreV1().Secrets(m.argoNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "argocd.argoproj.io/secret-type=cluster",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list ArgoCD cluster secrets: %w", err)
	}

	for _, secret := range secrets.Items {
		name := string(secret.Data["name"])
		if name == "" {
			name = secret.Name
		}
		if name != clusterName {
			continue
		}

		server := string(secret.Data["server"])
		configJSON := secret.Data["config"]

		return buildRestConfig(server, configJSON)
	}

	return nil, fmt.Errorf("cluster %q not found in ArgoCD secrets", clusterName)
}

// argoClusterConfig is the JSON structure stored in ArgoCD cluster secret's "config" field.
type argoClusterConfig struct {
	BearerToken     string              `json:"bearerToken,omitempty"`
	Username        string              `json:"username,omitempty"`
	Password        string              `json:"password,omitempty"`
	TLSClientConfig argoTLSClientConfig `json:"tlsClientConfig,omitempty"`
	AWSAuthConfig   *argoAWSAuthConfig  `json:"awsAuthConfig,omitempty"`
}

type argoTLSClientConfig struct {
	Insecure bool   `json:"insecure,omitempty"`
	CAData   string `json:"caData,omitempty"`
	CertData string `json:"certData,omitempty"`
	KeyData  string `json:"keyData,omitempty"`
}

type argoAWSAuthConfig struct {
	ClusterName string `json:"clusterName,omitempty"`
	RoleARN     string `json:"roleARN,omitempty"`
}

func buildRestConfig(server string, configJSON []byte) (*rest.Config, error) {
	if len(configJSON) == 0 {
		// No config — likely the in-cluster server
		return &rest.Config{Host: server}, nil
	}

	var cfg argoClusterConfig
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse cluster config: %w", err)
	}

	restConfig := &rest.Config{
		Host:        server,
		BearerToken: cfg.BearerToken,
		Username:    cfg.Username,
		Password:    cfg.Password,
	}

	if cfg.TLSClientConfig.Insecure {
		restConfig.Insecure = true
	}
	if cfg.TLSClientConfig.CAData != "" {
		restConfig.CAData = []byte(cfg.TLSClientConfig.CAData)
	}
	if cfg.TLSClientConfig.CertData != "" {
		restConfig.CertData = []byte(cfg.TLSClientConfig.CertData)
	}
	if cfg.TLSClientConfig.KeyData != "" {
		restConfig.KeyData = []byte(cfg.TLSClientConfig.KeyData)
	}

	// AWS EKS auth is handled separately — if awsAuthConfig is set,
	// the caller needs to use the EKS token provider.
	// For now, bearerToken-based auth covers most cases.
	if cfg.AWSAuthConfig != nil && cfg.BearerToken == "" {
		log.Warn().Str("cluster", cfg.AWSAuthConfig.ClusterName).
			Msg("AWS EKS cluster detected but no bearerToken — EKS token refresh not yet implemented in multi-cluster manager")
	}

	return restConfig, nil
}
