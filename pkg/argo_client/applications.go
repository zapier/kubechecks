package argo_client

import (
	"context"
	"fmt"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/applicationset"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apiclient/cluster"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/pkg/errors"

	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/argo_client")

var ErrNoVersionFound = errors.New("no kubernetes version found")

// GetApplicationByName takes a context and a name, then queries the Argo Application client to retrieve the Application with the specified name.
// It returns the found Application and any error encountered during the process.
// If successful, the Application client connection is closed before returning.
func (argo *ArgoClient) GetApplicationByName(ctx context.Context, name string) (*v1alpha1.Application, error) {
	ctx, span := tracer.Start(ctx, "GetApplicationByName")
	defer span.End()

	closer, appClient := argo.GetApplicationClient()
	defer closer.Close()

	resp, err := appClient.Get(ctx, &application.ApplicationQuery{Name: &name})
	if err != nil {
		telemetry.SetError(span, err, "Argo Get Application error")
		return nil, fmt.Errorf("failed to retrieve the application: %v", err)
	}

	return resp, nil
}

// GetKubernetesVersionByApplication is a method on the ArgoClient struct that takes a context and an application name as parameters,
// and returns the Kubernetes version of the destination cluster where the specified application is running.
// It returns an error if the application or cluster information cannot be retrieved.
func (argo *ArgoClient) GetKubernetesVersionByApplication(ctx context.Context, app v1alpha1.Application) (string, error) {
	ctx, span := tracer.Start(ctx, "GetKubernetesVersionByApplicationName")
	defer span.End()

	// Get destination cluster
	// Some app specs have a Name defined, some have a Server defined, some have both, take a valid one and use it
	log.Debug().Msgf("for appname %s, server dest says: %s and name dest says: %s", app.Name, app.Spec.Destination.Server, app.Spec.Destination.Name)
	var clusterRequest *cluster.ClusterQuery
	if app.Spec.Destination.Server != "" {
		clusterRequest = &cluster.ClusterQuery{Server: app.Spec.Destination.Server}
	} else {
		clusterRequest = &cluster.ClusterQuery{Name: app.Spec.Destination.Name}
	}

	// Get cluster client
	clusterCloser, clusterClient := argo.GetClusterClient()
	defer clusterCloser.Close()

	// Get cluster
	clusterResponse, err := clusterClient.Get(ctx, clusterRequest)
	if err != nil {
		telemetry.SetError(span, err, "Argo Get Cluster error")
		return "", fmt.Errorf("failed to retrieve the destination Kubernetes cluster: %v", err)
	}

	// Get Kubernetes version
	version := clusterResponse.Info.GetKubeVersion()

	// cleanup trailing "+"
	version = strings.TrimSuffix(version, "+")

	version = strings.TrimSpace(version)
	if version == "" {
		return "", ErrNoVersionFound
	}

	return version, nil
}

// GetApplicationsByLabels takes a context and a labelselector, then queries the Argo Application client to retrieve the Applications with the specified label.
// It returns the found ApplicationList and any error encountered during the process.
// If successful, the Application client connection is closed before returning.
func (argo *ArgoClient) GetApplicationsByLabels(ctx context.Context, labels string) (*v1alpha1.ApplicationList, error) {
	ctx, span := tracer.Start(ctx, "GetApplicationsByLabels")
	defer span.End()

	closer, appClient := argo.GetApplicationClient()
	defer closer.Close()

	resp, err := appClient.List(ctx, &application.ApplicationQuery{Selector: &labels})
	if err != nil {
		telemetry.SetError(span, err, "Argo List Application error")
		return nil, fmt.Errorf("failed to retrieve applications from labels: %v", err)
	}

	return resp, nil
}

// GetApplicationsByAppset takes a context and an appset, then queries the Argo Application client to retrieve the Applications managed by the appset
// It returns the found ApplicationList and any error encountered during the process.
func (argo *ArgoClient) GetApplicationsByAppset(ctx context.Context, name string) (*v1alpha1.ApplicationList, error) {
	appsetLabelSelector := "argocd.argoproj.io/application-set-name=" + name
	return argo.GetApplicationsByLabels(ctx, appsetLabelSelector)
}

func (argo *ArgoClient) GetApplications(ctx context.Context) (*v1alpha1.ApplicationList, error) {
	ctx, span := tracer.Start(ctx, "GetApplications")
	defer span.End()

	closer, appClient := argo.GetApplicationClient()
	defer closer.Close()

	resp, err := appClient.List(ctx, new(application.ApplicationQuery))
	if err != nil {
		telemetry.SetError(span, err, "Argo List All Applications error")
		return nil, errors.Wrap(err, "failed to applications")
	}
	return resp, nil
}

func (argo *ArgoClient) GetApplicationSets(ctx context.Context) (*v1alpha1.ApplicationSetList, error) {
	ctx, span := tracer.Start(ctx, "GetApplications")
	defer span.End()

	closer, appClient := argo.GetApplicationSetClient()
	defer closer.Close()

	resp, err := appClient.List(ctx, new(applicationset.ApplicationSetListQuery))
	if err != nil {
		telemetry.SetError(span, err, "Argo List All Application Sets error")
		return nil, errors.Wrap(err, "failed to application sets")
	}
	return resp, nil
}
