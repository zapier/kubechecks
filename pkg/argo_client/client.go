package argo_client

import (
	"io"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
)

type ArgoClient struct {
	client apiclient.Client
}

var argoClient *ArgoClient
var once sync.Once

func GetArgoClient() *ArgoClient {
	once.Do(func() {
		argoClient = createArgoClient()
	})
	return argoClient
}

func createArgoClient() *ArgoClient {
	clientOptions := &apiclient.ClientOptions{
		ServerAddr:      viper.GetString("argocd-api-server-addr"),
		AuthToken:       viper.GetString("argocd-api-token"),
		GRPCWebRootPath: viper.GetString("argocd-api-path-prefix"),
		Insecure:        viper.GetBool("argocd-api-insecure"),
	}
	argo, err := apiclient.NewClient(clientOptions)
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD API client")
	}

	return &ArgoClient{
		client: argo,
	}
}

func NewArgoClient(client apiclient.Client) *ArgoClient {
	return &ArgoClient{
		client: client,
	}
}

// related argocd diff code https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L899
func (argo *ArgoClient) GetApplicationClient() (io.Closer, application.ApplicationServiceClient) {
	closer, appClient, err := argo.client.NewApplicationClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Application Client")
	}
	return closer, appClient
}

func (argo *ArgoClient) GetSettingsClient() (io.Closer, settings.SettingsServiceClient) {
	closer, appClient, err := argo.client.NewSettingsClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Settings Client")
	}
	return closer, appClient
}

func (argo *ArgoClient) GetClusterClient() (io.Closer, cluster.ClusterServiceClient) {
	closer, clusterClient, err := argo.client.NewClusterClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Cluster Client")
	}
	return closer, clusterClient
}
