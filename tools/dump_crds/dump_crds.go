package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver"
)

type ServerVersion struct {
	GitVersion string `json:"gitVersion"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("usage: %s\n", os.Args[0])
		os.Exit(1)
	}

	here, _ := os.Getwd()
	basepath := filepath.Join(here, os.Args[1])
	dump(basepath)
}

func dump(basepath string) {
	content, _ := exec.Command("kubectl", "version", "-o", "json").Output()
	var versionInfo struct {
		ServerVersion ServerVersion `json:"serverVersion"`
	}
	json.Unmarshal(content, &versionInfo)

	version, _ := semver.NewVersion(strings.Split(versionInfo.ServerVersion.GitVersion, "-")[0])
	versionStr := fmt.Sprintf("v%d.%d.0", version.Major(), version.Minor())
	basepath = filepath.Join(basepath, versionStr)

	content, _ = exec.Command("kubectl", "get", "crds", "-o", "json").Output()
	var crdsInfo struct {
		Items []json.RawMessage `json:"items"`
	}
	json.Unmarshal(content, &crdsInfo)
	fmt.Printf("got %d items\n", len(crdsInfo.Items))

	os.MkdirAll(basepath, os.ModePerm)

	for _, item := range crdsInfo.Items {
		var crdMetadata struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
				Versions []struct {
					Name   string                 `json:"name"`
					Schema map[string]interface{} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		json.Unmarshal(item, &crdMetadata)

		namespace := strings.Split(crdMetadata.Metadata.Name, ".")[1]
		kind := strings.ToLower(crdMetadata.Spec.Names.Kind)

		for _, version := range crdMetadata.Spec.Versions {
			versionName := version.Name
			schema, found := version.Schema["openAPIV3Schema"]
			if !found {
				continue
			}

			filename := fmt.Sprintf("%s-%s-%s.json", kind, namespace, versionName)
			filename = filepath.Join(basepath, filename)

			fmt.Printf("writing %s\n", filename)

			if _, err := os.Stat(filename); err == nil {
				os.Remove(filename)
			}

			schemaJSON, _ := json.MarshalIndent(schema, "", "  ")

			os.WriteFile(filename, schemaJSON, 0644)
		}
	}
}
