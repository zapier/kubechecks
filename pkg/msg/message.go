package msg

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"

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
	appWrapOpen    = "<details>\n<summary>\n\n"
	appWrapClose   = "\n</summary>\n\n"
	appWrapEnd     = "</details>"
	checkWrapFmt   = "<details>\n<summary>%s</summary>\n\n%s\n</details>"
	checkSeparator = "\n\n---\n\n"

	lineTruncated = "... (line truncated)\n"
)

func partSuffix(part, total int) string {
	return fmt.Sprintf(" (Part %d of %d)", part, total)
}

type checkBlock struct {
	summary string
	details string
}

func renderCheck(c checkBlock) string {
	return fmt.Sprintf(checkWrapFmt, c.summary, c.details)
}

func wrapAppSection(appHeader string, checks []string) string {
	return appWrapOpen + appHeader + appWrapClose + strings.Join(checks, checkSeparator) + appWrapEnd
}

func (m *Message) buildAppSections(appName string, results *AppResults, maxSectionLen int) []string {
	var checks []checkBlock
	appState := pkg.StateSuccess

	for _, check := range results.results {
		if check.NoChangesDetected {
			return nil
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

	appHeader := fmt.Sprintf("## ArgoCD Application Checks: `%s` %s", appName, m.vcs.ToEmoji(appState))
	wrapLen := len(wrapAppSection(appHeader, nil))

	var sections, group []string
	groupLen := wrapLen
	flush := func() {
		sections = append(sections, wrapAppSection(appHeader, group))
		group, groupLen = nil, wrapLen
	}

	for _, c := range checks {
		rendered := renderCheck(c)
		added := len(rendered)
		if len(group) > 0 {
			added += len(checkSeparator)
		}

		if groupLen+added <= maxSectionLen {
			group = append(group, rendered)
			groupLen += added
			continue
		}
		if len(group) > 0 {
			flush()
		}
		if wrapLen+len(rendered) <= maxSectionLen {
			group, groupLen = []string{rendered}, wrapLen+len(rendered)
			continue
		}

		for _, part := range splitCheck(c, maxSectionLen-wrapLen) {
			sections = append(sections, wrapAppSection(appHeader, []string{part}))
		}
	}

	if len(group) > 0 || len(sections) == 0 {
		flush()
	}

	return sections
}

func splitCheck(c checkBlock, maxLen int) []string {
	wrapLen := len(renderCheck(checkBlock{summary: c.summary}))

	// the suffix eats into the room for details, and its width depends on how
	// many parts come out, so go again if the guess was too narrow
	var parts []string
	for reserve := len(partSuffix(99, 99)); ; {
		parts = splitDetailsAtLines(c.details, maxLen-wrapLen-reserve)
		needed := len(partSuffix(len(parts), len(parts)))
		if needed <= reserve {
			break
		}
		reserve = needed
	}

	rendered := make([]string, len(parts))
	for i, part := range parts {
		summary := c.summary
		if len(parts) > 1 {
			summary += partSuffix(i+1, len(parts))
		}
		rendered[i] = renderCheck(checkBlock{summary, part})
	}
	return rendered
}

type openDetails struct {
	summary    string
	hasSummary bool
}

// mdState is what is still open at some line of a check's details. Cutting
// there means closing all of it, and opening it again in the next part.
//
// It knows what the checks produce: backtick fences and <details> blocks with
// a one line <summary>. It is not a markdown parser. Tilde fences, indented
// code blocks and tags quoted in inline code are taken for plain text. A bare
// fence line in the context of a diff ends the diff block, as it does on screen.
type mdState struct {
	fence, fenceInfo string
	details          []openDetails
}

// fenceOf returns the backticks of a code fence line and what follows them.
// A fence may be indented by up to three spaces. Diff context lines are
// indented by one, so they count, same as they do for the renderer.
func fenceOf(line string) (marker, info string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return "", ""
	}
	rest := strings.TrimLeft(trimmed, "`")
	if len(trimmed)-len(rest) < 3 {
		return "", ""
	}
	return trimmed[:len(trimmed)-len(rest)], strings.TrimSpace(rest)
}

func (s mdState) next(line string) mdState {
	marker, info := fenceOf(line)

	if s.fence != "" {
		if info == "" && len(marker) >= len(s.fence) {
			s.fence, s.fenceInfo = "", ""
		}
		return s
	}
	if marker != "" {
		s.fence, s.fenceInfo = marker, info
		return s
	}

	s.details = slices.Clone(s.details)
	for i := 0; i < len(line); i++ {
		switch rest := line[i:]; {
		case strings.HasPrefix(rest, "<details>"), strings.HasPrefix(rest, "<details "):
			s.details = append(s.details, openDetails{})
		case strings.HasPrefix(rest, "</details>") && len(s.details) > 0:
			s.details = s.details[:len(s.details)-1]
		}
	}
	if n := len(s.details); n > 0 && !s.details[n-1].hasSummary {
		if _, rest, ok := strings.Cut(line, "<summary>"); ok {
			if text, _, ok := strings.Cut(rest, "</summary>"); ok {
				s.details[n-1] = openDetails{summary: cutAtRune(text, maxCarriedSummary), hasSummary: true}
			}
		}
	}
	return s
}

const (
	detailsClose = "\n</details>\n"

	// a summary is repeated in every part its block continues into
	maxCarriedSummary = 80
)

// closerLen is len(closer()) without building it, this runs for every line
func (s mdState) closerLen() int {
	n := len(s.details) * len(detailsClose)
	if s.fence != "" {
		n += len(s.fence) + 1
	}
	return n
}

func (s mdState) closer() string {
	var sb strings.Builder
	if s.fence != "" {
		sb.WriteString(s.fence + "\n")
	}
	for range s.details {
		sb.WriteString(detailsClose)
	}
	return sb.String()
}

func (s mdState) opener() string {
	var sb strings.Builder
	for _, d := range s.details {
		sb.WriteString("<details>\n")
		if d.hasSummary {
			fmt.Fprintf(&sb, "<summary>%s (continued)</summary>\n\n", d.summary)
		}
	}
	if s.fence != "" {
		sb.WriteString(s.fence + s.fenceInfo + "\n")
	}
	return sb.String()
}

func cutAtRune(s string, n int) string {
	if n >= len(s) {
		return s
	}
	n = max(n, 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// splitDetailsAtLines cuts content between lines into parts of at most maxLen
// bytes. Code fences and <details> blocks open at a cut are closed and then
// reopened in the next part, so each part renders on its own.
func splitDetailsAtLines(content string, maxLen int) []string {
	if maxLen <= 0 || len(content) <= maxLen {
		return []string{content}
	}

	var parts []string
	var buf strings.Builder
	var state mdState
	hasContent := false

	for _, line := range strings.SplitAfter(content, "\n") {
		// SplitAfter leaves an empty string after the last newline
		if line == "" {
			continue
		}

		after := state.next(line)

		if hasContent && buf.Len()+len(line)+after.closerLen() > maxLen {
			buf.WriteString(state.closer())
			parts = append(parts, buf.String())
			buf.Reset()
			buf.WriteString(state.opener())
		}

		// a single line can be bigger than a whole part, e.g. minified JSON.
		// What is left of it may open or close less than the full line did.
		for room := maxLen - buf.Len() - after.closerLen(); len(line) > room; room = maxLen - buf.Len() - after.closerLen() {
			if room < len(lineTruncated) {
				line = lineTruncated
				after = state
				break
			}
			line = cutAtRune(line, room-len(lineTruncated)) + lineTruncated
			after = state.next(line)
		}

		buf.WriteString(line)
		state = after
		hasContent = true
	}

	if hasContent {
		buf.WriteString(state.closer())
		parts = append(parts, buf.String())
	}

	return parts
}

type CommentOptions struct {
	Start         time.Time
	CommitSHA     string
	LabelFilter   string
	ShowDebugInfo bool
	Identifier    string

	AppsChecked, TotalChecked int

	// MaxLength is the comment size limit of the VCS, in bytes
	MaxLength int
	// MaxComments is how many comments the report may take, pkg.MaxCommentsPerCheck at most
	MaxComments int
}

// BuildComment iterates the map of all apps in this message, building the final comment from their current state.
// A report that does not fit in opts.MaxLength comes back as several comments.
func (m *Message) BuildComment(ctx context.Context, opts CommentOptions) []string {
	_, span := tracer.Start(ctx, "buildComment")
	defer span.End()

	if opts.MaxComments <= 0 || opts.MaxComments > pkg.MaxCommentsPerCheck {
		opts.MaxComments = pkg.MaxCommentsPerCheck
	}

	names := getSortedKeys(m.apps)

	cfg := chunkConfig{
		MaxLength:  opts.MaxLength,
		MaxChunks:  opts.MaxComments,
		Identifier: opts.Identifier,
		Footer:     m.buildFooter(opts.Start, opts.CommitSHA, opts.LabelFilter, opts.ShowDebugInfo, opts.AppsChecked, opts.TotalChecked),
	}

	// sized with a single comment's overhead first, the report may well fit one
	sections := m.buildSections(names, cfg.oneCommentBudget(), 1)
	if cfg.fitsOneComment(sections) {
		return frameChunks([][]string{sections}, false, cfg)
	}

	sections = m.buildSections(names, cfg.sectionBudget(), cfg.MaxChunks)

	return splitIntoChunks(sections, cfg)
}

// what is past the cap never gets posted, so buildSections stops rendering once
// the sections it has pack into more chunks than the cap allows.
func (m *Message) buildSections(names []string, budget, maxChunks int) []string {
	var sections []string
	for _, appName := range names {
		if len(packSections(sections, budget)) > maxChunks {
			break
		}
		if m.isDeleted(appName) {
			continue
		}

		sections = append(sections, m.buildAppSections(appName, m.apps[appName], budget)...)
	}

	return sections
}

func getSortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	var keys []K
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
