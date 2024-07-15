// Package client provides utilities for creating and managing Kubernetes clients.
//
// This package includes functions and types for configuring and creating
// Kubernetes clients tailored to specific requirements, such as accessing
// AWS EKS clusters with appropriate authentication.
//
// Example usage:
//
// # specify the opts with Kubernetes Cluster type, such as EKSClientOption, otherwise the default local client will be created.
//
// * for EKS setup:
//
//	client, err := client.New(&NewClientInput{ClusterType: ClusterTypeEKS}, EKSClientOption(ctx, "test-cluster-01", "us-east-1"))
//	if err != nil {
//	    fmt.Printf("failed to create client: %v", err)
//	    return
//	}
//
// * for localhost setup:
//
//	client, err := client.New(&NewClientInput{ClusterType: ClusterTypeLOCAL, KuberKubernetesConfigPath: *configFilePath})
package client
