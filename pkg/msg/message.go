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

// BuildComment iterates the map of all apps in this message, building a final comment from their current state
func (m *Message) BuildComment(
	ctx context.Context, start time.Time, commitSHA, labelFilter string, showDebugInfo bool, identifier string,
	appsChecked, totalChecked int,
) string {
	_, span := tracer.Start(ctx, "buildComment")
	defer span.End()

	names := getSortedKeys(m.apps)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Kubechecks %s Report\n", identifier))

	updateWritten := false
	for _, appName := range names {
		if m.isDeleted(appName) {
			continue
		}

		var checkStrings []string
		results := m.apps[appName]

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

			msg := fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n</details>", summary, check.Details)
			checkStrings = append(checkStrings, msg)
			appState = pkg.WorstState(appState, check.State)
		}

		if noChangesDetected {
			continue
		}

		sb.WriteString("<details>\n")
		sb.WriteString("<summary>\n\n")
		sb.WriteString(fmt.Sprintf("## ArgoCD Application Checks: `%s` %s\n", appName, m.vcs.ToEmoji(appState)))
		sb.WriteString("</summary>\n\n")
		sb.WriteString(strings.Join(checkStrings, "\n\n---\n\n"))
		sb.WriteString("</details>")

		updateWritten = true
	}

	if !updateWritten {
		sb.WriteString("No changes")
	}

	footer := m.buildFooter(start, commitSHA, labelFilter, showDebugInfo, appsChecked, totalChecked)
	sb.WriteString(fmt.Sprintf("\n\n%s", footer))

	return sb.String()
}

func getSortedKeys[K constraints.Ordered, V any](m map[K]V) []K {
	var keys []K
	for key := range m {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}
