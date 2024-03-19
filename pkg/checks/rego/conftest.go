package rego

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/olekukonko/tablewriter"
	"github.com/open-policy-agent/conftest/output"
	"github.com/open-policy-agent/conftest/runner"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

const passedMessage = "\nPassed all policy checks."

var tracer = otel.Tracer("pkg/checks/rego")

type emojiable interface {
	ToEmoji(state pkg.CommitState) string
}

// Conftest runs the conftest validation against an application in a given repository
// path. It generates a summary string with the results, which can later be posted
// as a GitLab comment. The validation checks resources against Zapier policies and
// provides feedback for warnings or errors as informational messages. Failure to
// pass a policy check currently does not block deploy.
func conftest(
	ctx context.Context, app v1alpha1.Application, manifestsPath string, policiesLocations []string, vcs emojiable,
) (msg.Result, error) {
	_, span := tracer.Start(ctx, "Conftest")
	defer span.End()

	log.Debug().Str("dir", manifestsPath).Str("app", app.Name).Msg("running conftest in dir for application")

	r := runner.TestRunner{
		AllNamespaces:      true,
		NoColor:            true,
		Policy:             policiesLocations,
		SuppressExceptions: false,
		Trace:              false,
	}

	results, err := r.Run(ctx, []string{manifestsPath})
	if err != nil {
		telemetry.SetError(span, err, "ConfTest Run")
		return msg.Result{}, err
	}

	var b bytes.Buffer
	formatConftestResults(&b, results, vcs)
	resultsMessage := b.String()
	resultsMessage = strings.ReplaceAll(resultsMessage, fmt.Sprintf("%s/", manifestsPath), "")

	failures := false
	warnings := false
	for _, r := range results {
		for _, f := range r.Warnings {
			warnings = !f.Passed()
		}
		for _, f := range r.Failures {
			failures = !f.Passed()
		}
	}

	if strings.TrimSpace(resultsMessage) != "" {
		resultsMessage = passedMessage
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
		for r := 0; r < checkResult.Successes; r++ {
			// don't include these to be less noisy
			//tableData = append(tableData, []string{"success", checkResult.FileName, "SUCCESS"})
		}

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
