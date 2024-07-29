package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"k8s.io/client-go/rest"

	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	clusterIDHeader        = "x-k8s-aws-id"
	presignedURLExpiration = 10 * time.Minute
	v1Prefix               = "k8s-aws-v1."
)

// getEKSToken returns a pre-signed token for accessing the EKS cluster.
func getEKSToken(ctx context.Context, cfg *aws.Config, clusterName string) (*string, error) {
	stsClient := sts.NewFromConfig(*cfg)
	// Create the pre-signed request
	presignClient := sts.NewPresignClient(stsClient)
	presignedURLRequest, err := presignClient.PresignGetCallerIdentity(ctx, &sts.GetCallerIdentityInput{},
		func(presignOptions *sts.PresignOptions) {
			presignOptions.ClientOptions = append(presignOptions.ClientOptions, appendPresignHeaderValuesFunc(clusterName))
		})
	if err != nil {
		return nil, fmt.Errorf("failed to presign caller identity: %w", err)
	}

	// Encode the pre-signed URL
	token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(presignedURLRequest.URL))
	return &token, nil
}

func appendPresignHeaderValuesFunc(clusterID string) func(stsOptions *sts.Options) {
	return func(stsOptions *sts.Options) {
		stsOptions.APIOptions = append(stsOptions.APIOptions,
			// Add clusterId Header.
			smithyhttp.SetHeaderValue(clusterIDHeader, clusterID),
			// Add X-Amz-Expires query param.
			smithyhttp.SetHeaderValue("X-Amz-Expires", "60"))
	}
}

// EKSClientOption sets up a Kubernetes client for accessing an AWS EKS cluster.
//
// This function assumes that the AWS SDK configuration can obtain the necessary credentials
// from the environment. This is the default behavior of the AWS SDK. It is recommended to
// use IAM Roles for Service Accounts (IRSA) for EKS clusters to grant IAM roles to the pods.
// Using IRSA provides fine-grained permissions and security best practices. If IRSA is not
// used, the pod will attempt to use the instance credentials and generate an STS token for
// accessing the EKS cluster.
//
// Parameters:
//
//   - ctx: the context for the AWS SDK client.
//
//   - clusterID: the name of the EKS cluster.
func EKSClientOption(ctx context.Context, clusterID string) NewClientOption {
	return func(c *NewClientInput) {
		if c.ClusterType != ClusterTypeEKS {
			slog.Error("incorrect cluster type from the ClientInput, type must be ClusterTypeEKS", "ClusterType", c.ClusterType)
			return
		}
		var (
			cfg aws.Config
			err error
		)

		cfg, err = config.LoadDefaultConfig(ctx)
		if err != nil {
			slog.Error("unable to load AWS SDK config", "err", err)
			return
		}
		eksClient := eks.NewFromConfig(cfg)
		// perform DescribeCluster to get endpoint address and CA data.
		describeClusterOutput, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: &clusterID,
		})
		if err != nil {
			slog.Error("unable to describe EKS cluster", "err", err)
			return
		}

		token, err := getEKSToken(ctx, &cfg, *describeClusterOutput.Cluster.Name)
		if err != nil {
			slog.Error("unable to get EKS token", "err", err)
			return
		}

		caData, err := base64.StdEncoding.DecodeString(*describeClusterOutput.Cluster.CertificateAuthority.Data)
		if err != nil {
			slog.Error("failed to decode CA data", "err", err)
			return
		}
		k8sConfig := &rest.Config{
			Host:        *describeClusterOutput.Cluster.Endpoint,
			BearerToken: *token,
			TLSClientConfig: rest.TLSClientConfig{
				CAData: caData,
			},
		}
		// Create a token refresher and wrap the config with it
		tokenRefresher := &TokenRefresher{
			awsConfig:       &cfg,
			clusterID:       *describeClusterOutput.Cluster.Name,
			restConfig:      k8sConfig,
			token:           *token,
			tokenExpiration: time.Now().Add(presignedURLExpiration),
		}

		// override the WrapTransport method to refresh the token before each request
		k8sConfig.WrapTransport = tokenRefresher.WrapTransport

		c.restConfig = k8sConfig
	}
}

type TokenRefresher struct {
	sync.Mutex
	awsConfig       *aws.Config
	clusterID       string
	restConfig      *rest.Config
	token           string
	tokenExpiration time.Time
}

// refreshToken checks if the token is expired and refreshes it if necessary.
func (t *TokenRefresher) refreshToken(ctx context.Context) error {
	t.Lock()
	defer t.Unlock()

	if time.Now().After(t.tokenExpiration) {
		token, err := getEKSToken(ctx, t.awsConfig, t.clusterID)
		if err != nil {
			return fmt.Errorf("unable to refresh EKS token, %v", err)
		}

		t.token = *token
		t.tokenExpiration = time.Now().Add(presignedURLExpiration)
		t.restConfig.BearerToken = t.token
	}
	return nil
}

func (t *TokenRefresher) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	return &transportWrapper{
		transport: rt,
		refresher: t,
	}
}

type transportWrapper struct {
	transport http.RoundTripper
	refresher *TokenRefresher
}

// RoundTrip will perform the refrsh token before each request
func (t *transportWrapper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.refresher.refreshToken(req.Context()); err != nil {
		// log the error and continue with the request
		slog.Warn("failed to refresh token", "err", err)
	} else {
		// if only the token is refreshed, set the Authorization header
		req.Header.Set("Authorization", "Bearer "+t.refresher.token)
	}
	return t.transport.RoundTrip(req)
}
