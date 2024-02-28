package argo_client

import (
	"io"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/applicationset"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/settings"
	"github.com/rs/zerolog/log"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"

	"github.com/zapier/kubechecks/pkg/config"
)

type ArgoClient struct {
	client apiclient.Client
}

func NewArgoClient(cfg config.ServerConfig) (*ArgoClient, error) {
	opts := &apiclient.ClientOptions{
		ServerAddr:      cfg.ArgoCDServerAddr,
		AuthToken:       cfg.ArgoCDToken,
		GRPCWebRootPath: cfg.ArgoCDPathPrefix,
		Insecure:        cfg.ArgoCDInsecure,
	}

	log.Info().
		Str("server-addr", opts.ServerAddr).
		Int("auth-token", len(opts.AuthToken)).
		Str("grpc-web-root-path", opts.GRPCWebRootPath).
		Bool("insecure", cfg.ArgoCDInsecure).
		Msg("ArgoCD client configuration")

	argo, err := apiclient.NewClient(opts)
	if err != nil {
		return nil, err
	}

	return &ArgoClient{
		client: argo,
	}, nil
}

// GetApplicationClient has related argocd diff code https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L899
func (argo *ArgoClient) GetApplicationClient() (io.Closer, application.ApplicationServiceClient) {
	closer, appClient, err := argo.client.NewApplicationClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Application Client")
	}
	return closer, appClient
}

func (argo *ArgoClient) GetApplicationSetClient() (io.Closer, applicationset.ApplicationSetServiceClient) {
	closer, appClient, err := argo.client.NewApplicationSetClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Application Set Client")
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
