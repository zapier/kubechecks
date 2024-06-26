package dumpcrd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver"
	"github.com/spf13/cobra"
	"github.com/zapier/kubechecks/tools/dump_crds/internal/logger"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// dumpCmd represents the dump command
var dumpCmd = &cobra.Command{
	Use:   "dump",
	Short: "Custom Resource Definition (CRD) schema extraction tool",
	Long: `Custom Resource Definition (CRD) schema extraction tool.
Used to generate openAPIV3Schema for each CRD in the cluster. To be consumed by kubechecks.`,
	Run: func(cmd *cobra.Command, args []string) {
		dump()
	},
}

func init() {
	rootCmd.AddCommand(dumpCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// dumpCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// dumpCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func dump() {
	logHandler := logger.InitLogger(false, true)
	slog.SetDefault(logHandler)
	ctx := context.Background()
	config, err := getKubeConfig()
	if err != nil {
		slog.Error("failed to get kubeconfig\n\t", "err", err)
		cobra.CheckErr("please setup ~/.kube/config or specify kubeconfig flag")
	}
	serverVersion, err := getServerVersion(config)
	if err != nil {
		slog.Error("Error getting server version, check the network and vpn\n\t", "err", err)
		cobra.CheckErr("failed to connect to the EKS cluster")
	}
	currentVersion := strings.Split(serverVersion.GitVersion, "-")[0]

	semVer, err := semver.NewVersion(currentVersion)
	if err != nil {
		slog.Error("Error parsing server version\n\t", "err", err)
		cobra.CheckErr("invalid server version")
	}
	here, _ := os.Getwd()
	versionStr := fmt.Sprintf("v%d.%d.0", semVer.Major(), semVer.Minor())
	basepath := filepath.Join(here, versionStr)
	err = os.MkdirAll(basepath, os.ModePerm)
	if err != nil {
		slog.Error("Error creating basepath\n\t", "basepath", basepath, "err", err)
		cobra.CheckErr("failed to create a new directory for the schema files")
	}
	slog.Debug("Basepath: "+basepath, "version", versionStr)
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		slog.Error("Error creating dynamic client\n\t", "err", err)
		cobra.CheckErr("failed to create kubernetes client")
	}
	// List all CRDs in the cluster
	crds, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}).List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Error("Error listing CRDs\n\t", "err", err)
		cobra.CheckErr("failed to list CRDs, try again")
	}
	slog.Info(fmt.Sprintf("got %d items", len(crds.Items)))
	// Iterate over each CRD and list the versions.schema.openAPIV3Schema
	for _, crd := range crds.Items {
		crd.GetResourceVersion()
		slog.Info("CRD Name: " + crd.GetName())
		spec, found, err := unstructured.NestedFieldNoCopy(crd.Object, "spec")
		if !found || err != nil {
			slog.Error("Error finding spec for CRD\n\t", "crd", crd.GetName(), "err", err)
			continue
		}
		versions, found, err := unstructured.NestedSlice(spec.(map[string]interface{}), "versions")
		if !found || err != nil {
			slog.Error("Error finding versions for CRD\n\t", "crd", crd.GetName(), "err", err)
			continue
		}
		specNames, found, err := unstructured.NestedFieldNoCopy(spec.(map[string]interface{}), "names")
		if !found || err != nil {
			slog.Error("Error finding names for CRD\n\t", "crd", crd.GetName(), "err", err)
			continue
		}
		specNamesMap := specNames.(map[string]interface{})

		// Iterate over each version and write the schema to a file only if it contains openAPIV3Schema
		for _, ver := range versions {
			versionMap := ver.(map[string]interface{})
			versionSchema, foundSchema, err := unstructured.NestedFieldNoCopy(versionMap, "schema")
			if foundSchema && err == nil {
				openAPIV3Schema, found, err := unstructured.NestedFieldNoCopy(versionSchema.(map[string]interface{}), "openAPIV3Schema")
				if found && err == nil {
					jsonData, err := json.MarshalIndent(openAPIV3Schema, "", "  ")
					if err != nil {
						slog.Error("Error marshaling schema to JSON\n\t", "err", err)
						continue
					}
					namespace := strings.Split(crd.GetName(), ".")[1] // (e.g. acraccesstokens.generators.external-secrets.io -> generators)
					filename := fmt.Sprintf("%s-%s-%s.json", strings.ToLower(specNamesMap["kind"].(string)), namespace, versionMap["name"])
					slog.Info("saving file", "file", filename)
					filename = filepath.Join(basepath, filename)
					err = os.WriteFile(filename, jsonData, 0644)
					if err != nil {
						slog.Error("Error writing schema to file\n\t", "err", err)
						continue
					}
				}
			}
		}

	}
}

func getServerVersion(config *rest.Config) (*version.Info, error) {
	// Create a discovery client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	discoveryClient := clientset.Discovery()

	// Get the Kubernetes server version
	return discoveryClient.ServerVersion()
}

// getKubeConfig gets the Kubernetes configuration
func getKubeConfig() (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &configOverrides)
	return kubeConfig.ClientConfig()
}
