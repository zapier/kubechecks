package events

import (
	"context"
	"fmt"
	"strings"

	"github.com/zapier/kubechecks/pkg"
)

// panicDetails renders a recovered panic as markdown, with the stack trace folded
// into a collapsible section so it doesn't drown out the rest of the report.
func panicDetails(what string, r any, stack []byte) string {
	return fmt.Sprintf(`:warning: **kubechecks encountered an error while %s** :warning:

`+"```"+`
%v
`+"```"+`

<details>
<summary>Stack trace</summary>

`+"```"+`
%s
`+"```"+`
</details>

Check kubechecks application logs for more information.
`, what, r, strings.TrimSpace(string(stack)))
}

// reportPanic tells the PR/MR that the check blew up, so it isn't left with a
// "kubechecks running..." placeholder (or nothing at all) forever.
func (ce *CheckEvent) reportPanic(ctx context.Context, r any, stack []byte) {
	body := fmt.Sprintf("## Kubechecks %s Report\n%s", ce.ctr.Config.Identifier, panicDetails("checking this change", r, stack))
	if maxLen := ce.ctr.VcsClient.MaxCommentLength(); len(body) > maxLen {
		body = body[:maxLen]
	}

	if ce.vcsNote != nil {
		if err := ce.ctr.VcsClient.UpdateMessage(ctx, ce.pullRequest, ce.vcsNote.NoteID, []string{body}); err != nil {
			ce.logger.Error().Caller().Err(err).Msg("failed to update comment with panic")
		}
	} else if _, err := ce.ctr.VcsClient.PostMessage(ctx, ce.pullRequest, body); err != nil {
		ce.logger.Error().Caller().Err(err).Msg("failed to post comment with panic")
	}

	ce.CommitStatus(ctx, pkg.StatePanic)
}
