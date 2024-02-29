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
	"github.com/open-policy-agent/conftest/runner"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

const passedMessage = "\nPassed all policy checks."

var tracer = otel.Tracer("pkg/conftest")

type emojiable interface {
	ToEmoji(state pkg.CommitState) string
}

// Conftest runs the conftest validation against an application in a given repository
// path. It generates a summary string with the results, which can later be posted
// as a GitLab comment. The validation checks resources against Zapier policies and
// provides feedback for warnings or errors as informational messages. Failure to
// pass a policy check currently does not block deploy.
func Conftest(
	ctx context.Context, ctr container.Container, app *v1alpha1.Application, repoPath string, policiesLocations []string, vcs emojiable,
	gitManager *git.RepoManager,
) (msg.CheckResult, error) {
	_, span := tracer.Start(ctx, "Conftest")
	defer span.End()

	confTestDir := filepath.Join(repoPath, app.Spec.Source.Path)

	log.Debug().Str("dir", confTestDir).Str("app", app.Name).Msg("running conftest in dir for application")

	var locations []string
	for _, policiesLocation := range policiesLocations {
		logger := log.With().Str("policies-location", policiesLocation).Logger()
		if repo, err := gitManager.Clone(ctx, policiesLocation, ""); err != nil {
			logger.Warn().Err(err).Msg("failed to clone location")
		} else if repoPath != "" {
			logger.Info().Str("path", repo.Directory).Msg("cloned policies repo")
			locations = append(locations, repo.Directory)
		} else {
			logger.Warn().Msg("failed to clone schema policies location")
		}
	}

	if len(locations) == 0 {
		return msg.CheckResult{
			State:   pkg.StateWarning,
			Summary: "no policies locations configured",
		}, nil
	}

	var r runner.TestRunner

	r.NoColor = true
	r.AllNamespaces = true
	// PATH To Rego Polices
	r.Policy = locations
	r.SuppressExceptions = false
	r.Trace = false

	//TODO only do the main values app yaml maybe >.>
	innerStrings := []string{}
	failures := false
	warnings := false

	results, err := r.Run(ctx, []string{confTestDir})
	if err != nil {
		telemetry.SetError(span, err, "ConfTest Run")
		return msg.CheckResult{}, err
	}

	var b bytes.Buffer
	formatConftestResults(&b, results, vcs)
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

	var cr msg.CheckResult
	if failures {
		cr.State = pkg.StateFailure
	} else if warnings {
		cr.State = pkg.StateWarning
	} else {
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show Conftest Validation result</b>"
	cr.Details = strings.Join(innerStrings, "\n")

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
