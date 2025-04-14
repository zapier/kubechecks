package preupgrade

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kubepug/kubepug/lib"
	"github.com/kubepug/kubepug/pkg/results"
	"github.com/masterminds/semver"
	"github.com/olekukonko/tablewriter"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
)

const docLinkFmt = "[%s Deprecation Notes](https://kubernetes.io/docs/reference/using-api/deprecation-guide/#%s-v%d%d)"

var tracer = otel.Tracer("pkg/checks/preupgrade")

func checkApp(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion string, manifests []string) (msg.Result, error) {
	_, span := tracer.Start(ctx, "KubePug")
	defer span.End()

	logger := log.With().
		Ctx(ctx).
		Str("app_name", appName).
		Logger()

	var outputString []string

	logger.Debug().Msg("KubePug CheckApp")

	// write manifests to temp file because kubepug can only read from file or STDIN
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "kubechecks-kubepug-*")
	if err != nil {
		log.Error().Err(err).Stack().Msgf("failed to create temporary directory: %v", err)
		return msg.Result{}, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer pkg.WithErrorLogging(func() error { return os.RemoveAll(tempDir) }, "failed to remove directory")

	for i, manifest := range manifests {
		tmpFile := fmt.Sprintf("%s/%b.yaml", tempDir, i)
		if err = os.WriteFile(tmpFile, []byte(manifest), 0666); err != nil {
			logger.Error().Err(err).Str("path", tmpFile).Msg("failed to write file")
		}
	}

	nextVersion, err := nextKubernetesVersion(targetKubernetesVersion)
	if err != nil {
		return msg.Result{}, err
	}

	config := lib.Config{
		K8sVersion:     fmt.Sprintf("v%s", nextVersion.String()),
		Input:          tempDir,
		GeneratedStore: ctr.Config.KubepugGeneratedStore,
	}

	kubepug, err := lib.NewKubepug(&config)
	if err != nil {
		return msg.Result{}, err
	}

	result, err := kubepug.GetDeprecated()
	if err != nil {
		return msg.Result{}, err
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

	return msg.Result{
		State:   checkStatus(result),
		Summary: "<b>Show kubepug report:</b>",
		Details: fmt.Sprintf(
			"> This provides a list of Kubernetes resources in this application that are either deprecated or deleted from the **next** version (v%s) of Kubernetes.\n\n%s",
			nextVersion.String(),
			strings.Join(outputString, "\n"),
		),
	}, nil
}

func checkStatus(result *results.Result) pkg.CommitState {
	switch {
	case len(result.DeletedAPIs) > 0:
		// for now only ever a warning
		return pkg.StateWarning
	case len(result.DeprecatedAPIs) > 0:
		return pkg.StateWarning
	default:
		return pkg.StateSuccess
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
