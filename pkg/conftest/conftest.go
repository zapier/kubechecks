package conftest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/olekukonko/tablewriter"
	"github.com/open-policy-agent/conftest/output"
	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"

	"github.com/open-policy-agent/conftest/runner"
)

var gitLabCommentFormat = `
<details><summary><b>Show Conftest Validation result</b>: %s</summary>

%s

> This check validates resources against conftest policies.
> Currently this is informational only and a warning or error does not block deploy.

</details>
`

const passedMessage = "\nPassed all policy checks."

// Conftest runs the conftest validation against an application in a given repository
// path. It generates a summary string with the results, which can later be posted
// as a GitLab comment. The validation checks resources against Zapier policies and
// provides feedback for warnings or errors as informational messages. Failure to
// pass a policy check currently does not block deploy.
func Conftest(ctx context.Context, app *v1alpha1.Application, repoPath string) (string, error) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "Conftest")
	defer span.End()

	confTestDir := filepath.Join(repoPath, app.Spec.Source.Path)

	log.Debug().Str("dir", confTestDir).Str("app", app.Name).Msg("running conftest in dir for application")

	var r = runner.TestRunner{}

	r.NoColor = true
	r.AllNamespaces = true
	// PATH To Rego Polices
	r.Policy = []string{"./policy"}
	r.SuppressExceptions = false
	r.Trace = false

	//TODO only do the main values app yaml maybe >.>
	innerStrings := []string{}
	failures := false
	warnings := false

	results, err := r.Run(ctx, []string{confTestDir})
	if err != nil {
		telemetry.SetError(span, err, "ConfTest Run")
		return "", err
	}

	var b bytes.Buffer
	formatConftestResults(&b, results)
	innerStrings = append(innerStrings, fmt.Sprintf("\n\n**%s**\n", app.Spec.Source.Path))
	s := b.String()
	s = strings.ReplaceAll(s, fmt.Sprintf("%s/", repoPath), "")

	for _, r := range results {
		for _, f := range r.Warnings {
			warnings = !f.Passed()
		}
		for _, f := range r.Failures {
			failures = !f.Passed()
		}
	}

	if strings.TrimSpace(s) != "" {
		innerStrings = append(innerStrings, s)
	} else {
		innerStrings = append(innerStrings, passedMessage)
	}

	resultString := pkg.PassString()
	if warnings {
		resultString = pkg.WarningString()
	}
	if failures {
		resultString = pkg.FailedString()
	}

	comment := fmt.Sprintf(gitLabCommentFormat, resultString, strings.Join(innerStrings, "\n"))
	return comment, nil
}

// formatConftestResults writes the check results from an array of output.CheckResult objects into a formatted table.
// The table omits success messages to reduce noise and includes file, message, and status (failed, warning, skipped).
// The formatted table is then rendered and written to the given io.Writer 'w'.
func formatConftestResults(w io.Writer, checkResults []output.CheckResult) {
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
			tableData = append(tableData, []string{pkg.FailedEmoji(), code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Warnings {
			tableData = append(tableData, []string{pkg.WarningEmoji(), code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Skipped {
			tableData = append(tableData, []string{" :arrow_right:  ", code(checkResult.FileName), result.Message})
		}

		for _, result := range checkResult.Failures {
			tableData = append(tableData, []string{pkg.FailedEmoji(), code(checkResult.FileName), result.Message})
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
