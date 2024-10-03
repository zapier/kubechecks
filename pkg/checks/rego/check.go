package rego

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/olekukonko/tablewriter"
	"github.com/open-policy-agent/conftest/output"
	"github.com/open-policy-agent/conftest/parser"
	"github.com/open-policy-agent/conftest/runner"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/checks/rego")

type emojiable interface {
	ToEmoji(state pkg.CommitState) string
}

type Checker struct {
	locations []string
}

var ErrNoLocationsConfigured = errors.New("no policy locations configured")

func NewChecker(cfg config.ServerConfig) (*Checker, error) {
	var c Checker

	c.locations = cfg.PoliciesLocation
	if len(c.locations) == 0 {
		return nil, ErrNoLocationsConfigured
	}

	return &c, nil
}

var ErrResourceMustHaveKind = errors.New("resource does not have kind")
var ErrResourceMustHaveMetadata = errors.New("resource does not have metadata")
var ErrResourceMustHaveName = errors.New("resource does not have name")

func getFilenameFromRawManifest(manifest string) (string, error) {
	resource := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(manifest), &resource); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal resource")
	}

	kind, okKind := resource["kind"].(string)
	if !okKind {
		return "", ErrResourceMustHaveKind
	}

	metadata, okMetadata := resource["metadata"].(map[string]interface{})
	if !okMetadata {
		return "", ErrResourceMustHaveMetadata
	}

	name, okName := metadata["name"].(string)
	if !okName {
		return "", ErrResourceMustHaveName
	}

	namespace, okNamespace := metadata["namespace"].(string)
	if !okNamespace {
		return fmt.Sprintf("kind=%s,name=%s.yaml", kind, name), nil
	}

	return fmt.Sprintf("namespace=%s,kind=%s,name=%s.yaml", namespace, kind, name), nil
}

func dumpFiles(manifests []string) (string, error) {
	result, err := os.MkdirTemp("", "kubechecks-manifests-")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp dir")
	}

	log.Debug().
		Int("manifest_count", len(manifests)).
		Msg("dumping manifests")

	for index, manifest := range manifests {
		filename, err := getFilenameFromRawManifest(manifest)
		if err != nil {
			return result, errors.Wrap(err, "failed to get filename from manifest")
		}

		fullPath := filepath.Join(result, filename)
		manifestBytes := []byte(manifest)
		log.Debug().
			Str("path", fullPath).
			Int("index", index).
			Int("size", len(manifestBytes)).
			Msg("dumping manifest")

		if err = os.WriteFile(fullPath, manifestBytes, 0o666); err != nil {
			return result, errors.Wrapf(err, "failed to write %s", filename)
		}
	}

	return result, nil
}

// Check runs the conftest validation against an application in a given repository
// path. It generates a summary string with the results, which can later be posted
// as a GitLab comment. The validation checks resources against Zapier policies and
// provides feedback for warnings or errors as informational messages. Failure to
// pass a policy check currently does not block deploy.
func (c *Checker) Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	_, span := tracer.Start(ctx, "Conftest")
	defer span.End()

	manifestsPath, err := dumpFiles(request.YamlManifests)
	if manifestsPath != "" {
		defer pkg.WipeDir(manifestsPath)
	}
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "failed to write manifests to disk")
	}

	log.Debug().
		Strs("policiesPaths", c.locations).
		Str("manifestsPath", manifestsPath).
		Str("app", request.App.Name).
		Msg("running conftest in dir for application")

	r := runner.TestRunner{
		AllNamespaces:      true,
		NoColor:            true,
		Policy:             c.locations,
		Parser:             parser.YAML,
		ShowBuiltinErrors:  request.Container.Config.ShowDebugInfo,
		SuppressExceptions: false,
		Trace:              request.Container.Config.ShowDebugInfo,
	}

	results, err := r.Run(ctx, []string{manifestsPath})
	if err != nil {
		telemetry.SetError(span, err, "ConfTest Run")
		return msg.Result{}, err
	}

	var b bytes.Buffer
	formatConftestResults(&b, results, request.Container.VcsClient)
	resultsMessage := b.String()
	resultsMessage = strings.ReplaceAll(resultsMessage, fmt.Sprintf("%s/", manifestsPath), "")

	failures := false
	warnings := false
	for _, r := range results {
		for _, f := range r.Warnings {
			if !f.Passed() {
				warnings = true
			}
		}
		for _, f := range r.Failures {
			if !f.Passed() {
				failures = true
			}
		}
	}

	if strings.TrimSpace(resultsMessage) == "" {
		resultsMessage = "Passed all policy checks."
	}

	var cr msg.Result
	if failures {
		cr.State = pkg.StateFailure
	} else if warnings {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show Conftest Validation result</b>"
	cr.Details = resultsMessage

	return cr, nil
}

// formatConftestResults writes the check results from an array of output.CheckResult objects into a formatted table.
// The table omits success messages to reduce noise and includes file, message, and status (failed, warning, skipped).
// The formatted table is then rendered and written to the given io.Writer 'w'.
func formatConftestResults(w io.Writer, checkResults []output.CheckResult, vcs emojiable) {
	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{" ", "file", "message"})
	table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
	table.SetCenterSeparator("|")
	table.SetAutoWrapText(false)

	var tableData [][]string
	for _, checkResult := range checkResults {
		for _, result := range checkResult.Exceptions {
			tableData = append(tableData, []string{vcs.ToEmoji(pkg.StateError), code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Warnings {
			tableData = append(tableData, []string{vcs.ToEmoji(pkg.StateWarning), code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Skipped {
			tableData = append(tableData, []string{" :arrow_right:  ", code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Failures {
			tableData = append(tableData, []string{vcs.ToEmoji(pkg.StateFailure), code(checkResult.FileName), result.Message})
		}
	}

	if len(tableData) > 0 {
		table.AppendBulk(tableData)
		table.Render()
	}
}

func code(s string) string {
	return "`" + s + "`"
}
