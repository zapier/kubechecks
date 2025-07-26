package msg

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"golang.org/x/exp/constraints"
	"golang.org/x/exp/slices"

	"github.com/rs/zerolog/log"
	"github.com/zapier/kubechecks/pkg"
)

var tracer = otel.Tracer("pkg/msg")

type Result struct {
	State             pkg.CommitState
	Summary, Details  string
	NoChangesDetected bool
}

type AppResults struct {
	results []Result
}

func (ar *AppResults) AddCheckResult(result Result) {
	ar.results = append(ar.results, result)
}

func NewMessage(name string, prId, commentId int, vcs toEmoji) *Message {
	return &Message{
		Name:    name,
		CheckID: prId,
		NoteID:  commentId,
		vcs:     vcs,

		apps:           make(map[string]*AppResults),
		deletedAppsSet: make(map[string]struct{}),
	}
}

type toEmoji interface {
	ToEmoji(state pkg.CommitState) string
}

// Message type that allows concurrent updates
// Has a reference to the owner/repo (ie zapier/kubechecks),
// the PR/MR id, and the actual messsage
type Message struct {
	Name    string
	Owner   string
	CheckID int
	NoteID  int

	// Key = Appname, value = Results
	apps map[string]*AppResults
	lock sync.Mutex
	vcs  toEmoji

	deletedAppsSet map[string]struct{}
}

func (m *Message) WorstState() pkg.CommitState {
	state := pkg.StateNone

	for app, r := range m.apps {
		if m.isDeleted(app) {
			continue
		}

		for _, result := range r.results {
			state = pkg.WorstState(state, result.State)
		}
	}

	return state
}

func (m *Message) RemoveApp(app string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.deletedAppsSet[app] = struct{}{}
}

func (m *Message) isDeleted(app string) bool {
	if _, ok := m.deletedAppsSet[app]; ok {
		return true
	}

	return false
}

func (m *Message) AddNewApp(ctx context.Context, app string) {
	if m.isDeleted(app) {
		return
	}

	_, span := tracer.Start(ctx, "AddNewApp")
	defer span.End()
	m.lock.Lock()
	defer m.lock.Unlock()

	m.apps[app] = new(AppResults)
}

func (m *Message) AddToAppMessage(ctx context.Context, app string, result Result) {
	if m.isDeleted(app) {
		return
	}

	_, span := tracer.Start(ctx, "AddToAppMessage")
	defer span.End()
	m.lock.Lock()
	defer m.lock.Unlock()

	m.apps[app].AddCheckResult(result)
}

var hostname = ""

func init() {
	hostname, _ = os.Hostname()
}

func (m *Message) buildFooter(
	start time.Time, commitSHA, labelFilter string, showDebugInfo bool,
	appsChecked, totalChecked int,
) string {
	if !showDebugInfo {
		return fmt.Sprintf("<small> _Done. CommitSHA: %s_ <small>\n", commitSHA)
	}

	envStr := ""
	if labelFilter != "" {
		envStr = fmt.Sprintf(", Env: %s", labelFilter)
	}
	duration := time.Since(start)

	return fmt.Sprintf("<small> _Done: Pod: %s, Dur: %v, SHA: %s%s_ <small>, Apps Checked: %d, Total Checks: %d\n",
		hostname, duration.Round(time.Second), pkg.GitCommit, envStr, appsChecked, totalChecked)
}

// BuildComment iterates the map of all apps in this message, building a final comment from their current state
//
// This function is responsible for generating the VCS comment(s) for a PR/MR, handling:
//   - Character limits (splitting into multiple comments if needed)
//   - Appending headers, footers, and split warnings
//   - Skipping apps with only NoChangesDetected or StateSkip
//   - Formatting summaries and details for each app's results
//   - Ensuring correct emoji/state display for each app/result
//
// COMMENT SPLITTING LOGIC:
// The function implements a sophisticated comment splitting strategy to handle VCS character limits:
//
//  1. PRE-SPLIT DETECTION: Before writing any content, check if adding the next piece (app header, summary, etc.)
//     would exceed the chunk limit. If so, finalize current chunk and start a new one with continuedHeader.
//
// 2. MID-CONTENT SPLITTING: For large details blocks that exceed available space:
//   - Calculate availableSpace = maxContentLength - currentLength - splitCommentFooter length
//   - If content fits entirely, write it all
//   - If content exceeds available space, write as much as possible, add split warning, then continue in next chunk
//   - This ensures no content is lost and users are warned about the split
//
// 3. CHUNK MANAGEMENT: Each chunk includes:
//
//   - First chunk: header from pkg.GetMessageHeader()
//
//   - Subsequent chunks: continuedHeader with link to previous comment
//
//   - All chunks: footer with timing/debug info
//
//     4. SPLIT WARNINGS: When content is split mid-details, a warning message is added to inform users
//     that the output continues in the next comment, with a link to the previous comment.
//
//     5. FINAL VALIDATION: After building the complete comment, if it still exceeds maxCommentLength,
//     truncate it to the limit and return as a single chunk.
//
// The output is a slice of strings, each representing a comment chunk to be posted.
func (m *Message) BuildComment(
	ctx context.Context, start time.Time, commitSHA, labelFilter string, showDebugInfo bool, identifier string,
	appsChecked, totalChecked int, maxCommentLength int, prLinkTemplate string,
) []string {
	_, span := tracer.Start(ctx, "buildComment")
	defer span.End()

	// Get sorted app names for deterministic output
	names := getSortedKeys(m.apps)

	// Header for the first comment
	header := pkg.GetMessageHeader(identifier)
	// Footer warning for split comments
	splitCommentFooter := "\n\n> **Warning**: Output length greater than maximum allowed comment size. Continued in next comment"
	// Header for continued comments
	// this is written from the VCS PostMessage/UpdateMessage function. So, we need to account for it here
	continuedHeader := fmt.Sprintf("%s> Continued from previous [comment](%s)\n\n", header, prLinkTemplate)
	// Max content length for each chunk (accounting for continuedHeader)
	maxContentLength := maxCommentLength - len(continuedHeader) - 10 // 10 is a safety margin
	contentLength := 0

	comments := []string{}

	var sb strings.Builder
	sb.WriteString(header)
	contentLength = len(header)

	// Helper to finalize and append a chunk, always ending with a splitCommentFooter
	// and starting new chunks with continuedHeader after the first
	appendChunk := func(appHeader string) {
		if sb.Len() > 0 {
			// Only add splitCommentFooter if there's enough space
			if (contentLength)+len(splitCommentFooter) <= maxContentLength {
				sb.WriteString(splitCommentFooter)
			} else {
				// Create a truncated copy of splitCommentFooter to make room for it
				availableSpace := maxContentLength - contentLength
				if availableSpace > 0 {
					truncatedFooter := splitCommentFooter[:availableSpace]
					sb.WriteString(truncatedFooter)
				}
			}
			comments = append(comments, sb.String())
			sb.Reset()
			sb.WriteString(header)
			// continuedHeader contains both the header and the comment about the continued fromprevious comment
			// header is written here but the rest of the content is written from the vcs PostMessage/UpdateMessage function
			// that's why we're setting the contentLength to the length of the continuedHeader
			contentLength = len(continuedHeader)
			sb.WriteString(appHeader)
			contentLength += len(appHeader)

		}
	}

	updateWritten := false
	for appIndex, appName := range names {
		if m.isDeleted(appName) {
			continue
		}

		results := m.apps[appName]

		// Skip app if all results are StateSkip or NoChangesDetected
		skipApp := true
		// Determine worst state for the app (for emoji in header)
		appState := pkg.StateNone
		for _, check := range results.results {
			if !check.NoChangesDetected && check.State != pkg.StateSkip {
				skipApp = false
				appState = pkg.WorstState(appState, check.State)
			}
		}
		if skipApp {
			continue
		}

		// App header: show emoji only if state is not None
		appHeader := "\n\n<details>\n<summary>\n\n"
		if appState == pkg.StateNone {
			appHeader += fmt.Sprintf("## ArgoCD Application Checks: `%s`\n", appName)
		} else {
			appHeader += fmt.Sprintf("## ArgoCD Application Checks: `%s` %s\n", appName, m.vcs.ToEmoji(appState))
		}
		appHeader += "</summary>\n\n"

		// Only split if adding the app header would exceed the chunk limit
		if contentLength+len(appHeader) > maxContentLength {
			appendChunk(appHeader)
		} else {
			sb.WriteString(appHeader)
			contentLength += len(appHeader)
		}

		// Write each result for the app
		for _, check := range results.results {
			if check.NoChangesDetected || check.State == pkg.StateSkip {
				continue
			}

			// Summary formatting:
			// - For StateNone: just summary text or 'Success' (no emoji, no state string)
			// - For others: if summary/details are empty, 'Success <emoji>' (no state string)
			var summary string
			if check.State == pkg.StateNone {
				if check.Summary == "" && check.Details == "" {
					summary = "Success"
				} else {
					summary = check.Summary
				}
			} else {
				if check.Summary == "" && check.Details == "" {
					summary = "Success " + m.vcs.ToEmoji(check.State)
				} else {
					summary = fmt.Sprintf("%s %s %s", check.Summary, check.State.BareString(), m.vcs.ToEmoji(check.State))
				}
			}
			summaryHeader := fmt.Sprintf("<details>\n<summary>%s</summary>\n", summary)

			// Only split if adding the summary would exceed the chunk limit (not if it equals)
			if contentLength+len(summaryHeader) > maxContentLength {
				appendChunk(appHeader)
			}
			sb.WriteString(summaryHeader)
			contentLength += len(summaryHeader)

			// Details block (may need to split across chunks)
			msg := fmt.Sprintf("%s\n</details>", check.Details)
			for len(msg) > 0 {
				availableSpace := maxContentLength - contentLength - len(splitCommentFooter)
				if availableSpace <= 0 {
					appendChunk(appHeader)
					availableSpace = maxContentLength - contentLength - len(splitCommentFooter)
				}
				if availableSpace > len(msg) {
					availableSpace = len(msg)
				}
				if availableSpace > 0 && availableSpace < len(msg) {
					// Split content while preserving code blocks
					// Create space for writing the the closing </details> tag
					availableSpace -= len("</details>")
					firstPart, secondPart := splitContentPreservingCodeBlocks(msg, availableSpace)
					sb.WriteString(firstPart)
					// close the summary tag. This ensures it's not split across chunks.
					sb.WriteString("</details>")
					appendChunk(appHeader)
					msg = secondPart
				} else {
					sb.WriteString(msg)
					contentLength += len(msg)
					msg = ""
				}
			}
		}

		// Don't split if there's no more apps to write. An unclosed details tag wouldn't cause an issue
		// unless there's more contents to write
		closingDetailsTag := "\n</details>\n"
		if appIndex < len(names)-1 {
			if contentLength+len(closingDetailsTag) > maxContentLength {
				appendChunk(appHeader)
			} // Close the app details block
		}
		// if there's space to write the closingTag, write it
		if contentLength+len(closingDetailsTag) < maxContentLength {
			sb.WriteString(closingDetailsTag)
			contentLength += len(closingDetailsTag)
		}

		updateWritten = true
	}

	// If no apps were written, output 'No changes'
	// we don't need to split this because it's a small output
	if !updateWritten {
		sb.WriteString("No changes")
		contentLength += len("No changes")
	}

	// Add the footer (with debug info if requested)
	footer := m.buildFooter(start, commitSHA, labelFilter, showDebugInfo, appsChecked, totalChecked)
	sb.WriteString(fmt.Sprintf("\n\n%s", footer))

	// Only split if content exceeds the max length
	if len(sb.String()) > maxCommentLength {
		comments = append(comments, sb.String()[:maxCommentLength])
	} else {
		comments = append(comments, sb.String())
	}

	for _, comment := range comments {
		log.Debug().Msgf("Comment length: %d", len(comment))
	}
	return comments
}

// splitContentPreservingCodeBlocks splits content at a given position while preserving code blocks and their types.
// If the split position is inside a code block, it will close the code block in the first part
// and open a new one in the second part, preserving the code block type (e.g., ```diff).
func splitContentPreservingCodeBlocks(content string, splitPos int) (string, string) {
	_, span := tracer.Start(context.Background(), "splitContentPreservingCodeBlocks")
	defer span.End()

	if splitPos >= len(content) {
		return content, ""
	}
	if splitPos <= 0 {
		return "", content
	}

	firstPart := content[:splitPos]
	secondPart := content[splitPos:]

	// Count the number of code block markers (```) in the first part
	codeBlockMarkers := strings.Count(firstPart, "```")

	// If the number of markers is odd, we're inside a code block
	if codeBlockMarkers%2 == 1 {
		// Find the last opening code block and extract the type (if any)
		lastIdx := strings.LastIndex(firstPart, "```")
		codeBlockType := ""
		if lastIdx != -1 {
			// Look for the type after the opening backticks, up to the next newline or end
			typeStart := lastIdx + 3
			typeEnd := typeStart
			for typeEnd < len(firstPart) && firstPart[typeEnd] != '\n' && firstPart[typeEnd] != '\r' && firstPart[typeEnd] != '`' {
				typeEnd++
			}
			codeBlockType = strings.TrimSpace(firstPart[typeStart:typeEnd])
		}
		// Close the code block in the first part
		if codeBlockType != "" {
			firstPart += "\n```"
			secondPart = "```" + codeBlockType + "\n" + secondPart
		} else {
			firstPart += "\n```"
			secondPart = "```\n" + secondPart
		}
	}

	return firstPart, secondPart
}

func getSortedKeys[K constraints.Ordered, V any](m map[K]V) []K {
	var keys []K
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
