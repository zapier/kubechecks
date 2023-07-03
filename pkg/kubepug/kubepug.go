package kubepug

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/zapier/kubechecks/pkg"
	"go.opentelemetry.io/otel"

	"github.com/masterminds/semver"
	"github.com/olekukonko/tablewriter"
	"github.com/rikatz/kubepug/lib"
	"github.com/rikatz/kubepug/pkg/results"
	"github.com/rs/zerolog/log"
)

const docLinkFmt = "[%s Deprecation Notes](https://kubernetes.io/docs/reference/using-api/deprecation-guide/#%s-v%d%d)"
const kubepugCommentFormat = `
<details><summary><b>Show kubepug report:</b> %s</summary>

 > This provides a list of Kubernetes resources in this application that are either deprecated or deleted from the **next** version (%s) of Kubernetes.

%s
</details>
`

func CheckApp(ctx context.Context, appName, targetKubernetesVersion string, manifests []string) (string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "KubePug")
	defer span.End()

	var outputString []string

	log.Debug().Str("app_name", appName).Msg("KubePug CheckApp")

	// write manifests to temp file because kubepug can only read from file or STDIN
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("/tmp", "kubechecks-kubepug")
	if err != nil {
		log.Error().Err(err).Msg("could not create temp directory to write manifests for kubepug check")
		//return "", err
		return fmt.Sprintf("Error: %v", err), err
	}
	defer os.RemoveAll(tempDir)

	for i, manifest := range manifests {
		tmpFile := fmt.Sprintf("%s/%b.yaml", tempDir, i)
		os.WriteFile(tmpFile, []byte(manifest), 0666)
	}

	nextVersion, err := nextKubernetesVersion(targetKubernetesVersion)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), err
	}
	config := lib.Config{
		K8sVersion:      fmt.Sprintf("v%s", nextVersion.String()),
		ForceDownload:   false,
		APIWalk:         true,
		ShowDescription: true,
		Input:           tempDir,
	}
	kubepug := lib.NewKubepug(config)

	result, err := kubepug.GetDeprecated()
	if err != nil {
		return fmt.Sprintf("Error: %v", err), err
	}

	if len(result.DeprecatedAPIs) > 0 || len(result.DeletedAPIs) > 0 {

		if len(result.DeprecatedAPIs) > 0 {
			outputString = append(outputString, "\n\n**Deprecated APIs**\n")
			buff := &bytes.Buffer{}
			table := tablewriter.NewWriter(buff)

			table.SetHeader([]string{"API Version", "Kind", "Objects", "Docs"})
			table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
			table.SetCenterSeparator("|")
			table.SetAutoWrapText(false)

			for _, dep := range result.DeprecatedAPIs {
				row := []string{
					fmt.Sprintf("%s/%s", dep.Group, dep.Version),
					dep.Kind,
					formatItems(dep.Items),
					fmt.Sprintf(docLinkFmt, dep.Kind, strings.ToLower(dep.Kind), nextVersion.Major(), nextVersion.Minor()),
				}
				table.Append(row)
			}
			table.Render()
			outputString = append(outputString, buff.String())
		}

		if len(result.DeletedAPIs) > 0 {
			outputString = append(outputString, "\n\n**Deleted APIs**\n")
			buff := &bytes.Buffer{}
			table := tablewriter.NewWriter(buff)

			table.SetHeader([]string{"API Version", "Kind", "Objects", "Docs"})
			table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
			table.SetCenterSeparator("|")
			table.SetAutoWrapText(false)

			for _, dep := range result.DeletedAPIs {
				row := []string{
					fmt.Sprintf("%s/%s", dep.Group, dep.Version),
					dep.Kind,
					formatItems(dep.Items),
					fmt.Sprintf(docLinkFmt, dep.Kind, strings.ToLower(dep.Kind), nextVersion.Major(), nextVersion.Minor()),
				}
				table.Append(row)
			}
			table.Render()
			outputString = append(outputString, buff.String())
		}

	} else {
		outputString = append(outputString, "No Deprecated or Deleted APIs found.")
	}

	return fmt.Sprintf(kubepugCommentFormat, checkStatus(result), "`v"+nextVersion.String()+"`", strings.Join(outputString, "\n")), nil
}

func checkStatus(result *results.Result) string {
	switch {
	case len(result.DeletedAPIs) > 0:
		// for now only ever a warning
		return pkg.WarningString()
	case len(result.DeprecatedAPIs) > 0:
		return pkg.WarningString()
	default:
		return pkg.PassString()
	}
}

func nextKubernetesVersion(current string) (*semver.Version, error) {
	sv, err := semver.NewVersion(current)
	if err != nil {
		log.Error().Err(err).Str("input", current).Msg("kubepug: could not parse targetKubernetesVersion")
		return nil, err
	}

	next := sv.IncMinor()
	log.Debug().Str("current", current).Str("next", next.String()).Msg("calculated next Kubernetes version")
	return &next, nil
}

func formatItems(items []results.Item) string {
	itemNames := []string{}
	for _, item := range items {
		itemNames = append(itemNames, item.ObjectName)
	}
	return strings.Join(itemNames, "\n")
}
