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
			// Skip results with no changes detected, just like BuildComment does
			if result.NoChangesDetected {
				continue
			}
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

const (
	// HTML wrapper fragments used by renderCheck and wrapAppSection.
	appWrapOpen  = "<details>\n<summary>\n\n"
	appWrapClose = "\n</summary>\n\n"
	appWrapEnd   = "</details>"
	checkWrapFmt = "<details>\n<summary>%s</summary>\n\n%s\n</details>"

	// Fragments of checkWrapFmt, used by appSectionOverhead to compute
	// wrapper cost without materialising the rendered string.
	checkWrapOpen = "<details>\n<summary>"
	checkWrapMid  = "</summary>\n\n"
	checkWrapEnd  = "\n</details>"

	// checkSeparator is the horizontal rule placed between checks within a
	// single app section.
	checkSeparator = "\n\n---\n\n"

	// codeFenceClose is the closing marker appended when splitting inside a
	// fenced code block. Its length is reserved when computing available space.
	codeFenceClose = "```\n"

	// maxPartSuffix is the worst-case length of the " (Part N of M)" suffix
	// appended to check summaries when a single check is split across multiple
	// sections. Supports up to 999 parts.
	maxPartSuffix = len(" (Part 999 of 999)")
)

// checkBlock holds a single check result before rendering to HTML.
type checkBlock struct {
	summary string
	details string
}

// renderCheck wraps a checkBlock in a <details> element.
func renderCheck(c checkBlock) string {
	return fmt.Sprintf(checkWrapFmt, c.summary, c.details)
}

// wrapAppSection wraps one or more rendered check blocks under an app-level
// <details> element with a markdown heading as summary.
func wrapAppSection(appHeader string, checks []string) string {
	var sb strings.Builder
	sb.WriteString(appWrapOpen)
	sb.WriteString(appHeader)
	sb.WriteString(appWrapClose)
	sb.WriteString(strings.Join(checks, checkSeparator))
	sb.WriteString(appWrapEnd)
	return sb.String()
}

// appSectionOverhead returns the number of wrapper bytes consumed when a
// single check is placed inside an app section - i.e. everything except the
// check's details content.
func appSectionOverhead(appHeader, checkSummary string) int {
	app := len(appWrapOpen) + len(appHeader) + len(appWrapClose) + len(appWrapEnd)
	check := len(checkWrapOpen) + len(checkSummary) + len(checkWrapMid) + len(checkWrapEnd)
	return app + check
}

// buildAppSections produces one or more self-contained, renderable markdown
// sections for a single app. Splitting follows a three-tier strategy:
//
//  1. Whole app - if all checks fit within maxSectionLen, return one section.
//  2. Per-check - otherwise each check becomes its own section, still wrapped
//     in the app header so the reader knows which app it belongs to.
//  3. Per-line - if a single check's details still exceed the limit, the
//     content is split at line boundaries (see [splitDetailsAtLines]) and each
//     piece is wrapped in its own section with a "(Part N of M)" suffix.
//
// Every returned string is valid, self-contained markdown with balanced HTML
// tags. No post-hoc tag repair is needed.
func (m *Message) buildAppSections(appName string, results *AppResults, maxSectionLen int) []string {
	var checks []checkBlock
	appState := pkg.StateSuccess
	noChangesDetected := false

	for _, check := range results.results {
		if check.NoChangesDetected {
			noChangesDetected = true
			continue
		}
		if check.State == pkg.StateSkip {
			continue
		}

		var summary string
		if check.State == pkg.StateNone {
			summary = check.Summary
		} else {
			summary = fmt.Sprintf("%s %s %s", check.Summary, check.State.BareString(), m.vcs.ToEmoji(check.State))
		}
		checks = append(checks, checkBlock{summary: summary, details: check.Details})
		appState = pkg.WorstState(appState, check.State)
	}

	if noChangesDetected || len(checks) == 0 {
		return nil
	}

	appHeader := fmt.Sprintf("## ArgoCD Application Checks: `%s` %s", appName, m.vcs.ToEmoji(appState))

	// Estimate total size without materialising the full string.
	appWrapLen := len(appWrapOpen) + len(appHeader) + len(appWrapClose) + len(appWrapEnd)
	totalLen := appWrapLen
	renderedChecks := make([]string, 0, len(checks))
	for i, c := range checks {
		r := renderCheck(c)
		renderedChecks = append(renderedChecks, r)
		totalLen += len(r)
		if i > 0 {
			totalLen += len(checkSeparator)
		}
	}

	if totalLen <= maxSectionLen {
		return []string{wrapAppSection(appHeader, renderedChecks)}
	}

	var sections []string
	for i, c := range checks {
		sectionLen := appWrapLen + len(renderedChecks[i])
		if sectionLen <= maxSectionLen {
			sections = append(sections, wrapAppSection(appHeader, []string{renderedChecks[i]}))
			continue
		}

		overhead := appSectionOverhead(appHeader, c.summary) + maxPartSuffix
		available := maxSectionLen - overhead
		if available <= 0 {
			available = 1
		}
		parts := splitDetailsAtLines(c.details, available)
		for pi, part := range parts {
			partSummary := c.summary
			if len(parts) > 1 {
				partSummary = fmt.Sprintf("%s (Part %d of %d)", c.summary, pi+1, len(parts))
			}
			sections = append(sections, wrapAppSection(appHeader, []string{renderCheck(checkBlock{
				summary: partSummary,
				details: part,
			})}))
		}
	}

	return sections
}

// splitDetailsAtLines splits content at line boundaries so each piece fits
// within maxLen bytes.
//
// When the split point falls inside a fenced code block, the block is closed
// with ``` at the end of the current piece and reopened with the original
// language hint (e.g. ```diff) at the start of the next piece. This ensures
// every piece is independently renderable markdown.
func splitDetailsAtLines(content string, maxLen int) []string {
	if maxLen <= 0 || len(content) <= maxLen {
		return []string{content}
	}

	lines := strings.SplitAfter(content, "\n")
	var parts []string
	var buf strings.Builder
	inFence := false
	fenceLang := ""

	for _, line := range lines {
		closeOverhead := 0
		if inFence {
			closeOverhead = len(codeFenceClose)
		}

		if buf.Len()+len(line)+closeOverhead > maxLen && buf.Len() > 0 {
			if inFence {
				buf.WriteString(codeFenceClose)
			}
			parts = append(parts, buf.String())
			buf.Reset()
			if inFence {
				fmt.Fprintf(&buf, "```%s\n", fenceLang)
			}
		}

		// Guard against a single line exceeding the remaining budget
		// (e.g. minified JSON in a CRD diff). buf.Len() is non-zero
		// after a flush when a fence-reopen prefix has been written.
		if buf.Len()+len(line)+closeOverhead > maxLen {
			avail := maxLen - buf.Len() - closeOverhead - len("... (line truncated)\n")
			if avail < 0 {
				avail = 0
			}
			line = line[:avail] + "... (line truncated)\n"
		}

		buf.WriteString(line)

		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence = false
				fenceLang = ""
			} else {
				inFence = true
				fenceLang = strings.TrimPrefix(trimmed, "```")
			}
		}
	}

	if buf.Len() > 0 {
		parts = append(parts, buf.String())
	}

	return parts
}

// BuildComment assembles the final VCS comment from all app check results and
// returns it as one or more chunks, each fitting within maxCommentLength.
//
// Splitting is entirely structural - content is divided at app, check, and
// line boundaries before being wrapped in HTML (see [buildAppSections]). Every
// chunk contains balanced, renderable markdown with no post-hoc tag repair.
func (m *Message) BuildComment(
	ctx context.Context, start time.Time, commitSHA, labelFilter string,
	showDebugInfo bool, identifier string,
	maxCommentLength, maxCommentsPerCheck int,
	appsChecked, totalChecked int,
) []string {
	_, span := tracer.Start(ctx, "buildComment")
	defer span.End()

	names := getSortedKeys(m.apps)

	overhead := maxOverhead(identifier)
	footer := m.buildFooter(start, commitSHA, labelFilter, showDebugInfo, appsChecked, totalChecked)
	maxSectionLen := maxCommentLength - overhead - len(footer)
	if maxSectionLen <= 0 {
		maxSectionLen = 1
	}

	var allSections []string
	for _, appName := range names {
		if m.isDeleted(appName) {
			continue
		}
		sections := m.buildAppSections(appName, m.apps[appName], maxSectionLen)
		allSections = append(allSections, sections...)
	}

	return SplitIntoChunks(allSections, footer, ChunkConfig{
		MaxLength:  maxCommentLength,
		MaxChunks:  maxCommentsPerCheck,
		Identifier: identifier,
	})
}

func getSortedKeys[K constraints.Ordered, V any](m map[K]V) []K {
	var keys []K
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
