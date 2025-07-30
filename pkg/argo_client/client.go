package argo_client

import (
	"crypto/tls"
	"io"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/applicationset"
	"github.com/argoproj/argo-cd/v3/pkg/apiclient/settings"
	repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/argoproj/argo-cd/v3/pkg/apiclient/cluster"

	"github.com/zapier/kubechecks/pkg/config"
)

type ArgoClient struct {
	client    apiclient.Client
	k8s       kubernetes.Interface
	k8sConfig *rest.Config
	cfg       config.ServerConfig
}

func NewArgoClient(
	cfg config.ServerConfig,
	k8s client.Interface,
) (*ArgoClient, error) {
	opts := &apiclient.ClientOptions{
		ServerAddr:      cfg.ArgoCDServerAddr,
		AuthToken:       cfg.ArgoCDToken,
		GRPCWebRootPath: cfg.ArgoCDPathPrefix,
		Insecure:        cfg.ArgoCDInsecure,
		PlainText:       cfg.ArgoCDPlainText,
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
		cfg:       cfg,
		client:    argo,
		k8s:       k8s.ClientSet(),
		k8sConfig: k8s.Config(),
	}, nil
}

func (a *ArgoClient) createRepoServerClient() (repoapiclient.RepoServerServiceClient, *grpc.ClientConn, error) {
	log.Info().Msg("creating client")
	tlsConfig := tls.Config{InsecureSkipVerify: a.cfg.ArgoCDRepositoryInsecure}
	conn, err := grpc.NewClient(a.cfg.ArgoCDRepositoryEndpoint,
		grpc.WithTransportCredentials(
			credentials.NewTLS(&tlsConfig),
		),
	)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create client")
	}

	return repoapiclient.NewRepoServerServiceClient(conn), conn, nil
}

// GetApplicationClient has related argocd diff code https://github.com/argoproj/argo-cd/blob/d3ff9757c460ae1a6a11e1231251b5d27aadcdd1/cmd/argocd/commands/app.go#L899
func (a *ArgoClient) GetApplicationClient() (io.Closer, application.ApplicationServiceClient) {
	closer, appClient, err := a.client.NewApplicationClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Application Client")
	}
	return closer, appClient
}

func (a *ArgoClient) GetApplicationSetClient() (io.Closer, applicationset.ApplicationSetServiceClient) {
	closer, appClient, err := a.client.NewApplicationSetClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Application Set Client")
	}
	return closer, appClient
}

func (a *ArgoClient) GetSettingsClient() (io.Closer, settings.SettingsServiceClient) {
	closer, appClient, err := a.client.NewSettingsClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Settings Client")
	}
	return closer, appClient
}

func (a *ArgoClient) GetClusterClient() (io.Closer, cluster.ClusterServiceClient) {
	closer, clusterClient, err := a.client.NewClusterClient()
	if err != nil {
		log.Fatal().Err(err).Msg("could not create ArgoCD Cluster Client")
	}
	return closer, clusterClient
}
