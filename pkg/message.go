package pkg

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"golang.org/x/exp/constraints"
	"golang.org/x/exp/slices"
)

type CheckResult struct {
	State            CommitState
	Summary, Details string
}

type AppResults struct {
	lock    sync.Locker
	results []CheckResult
}

func (ar *AppResults) AddCheckResult(result CheckResult) {
	ar.results = append(ar.results, result)
}

func NewMessage(name string, prId, commentId int) *Message {
	return &Message{
		Name:    name,
		CheckID: prId,
		NoteID:  commentId,
		apps:    make(map[string]*AppResults),
	}
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
	apps   map[string]*AppResults
	footer string
	lock   sync.Mutex
}

func (m *Message) IsSuccess() bool {
	isSuccess := true

	for _, r := range m.apps {
		for _, result := range r.results {
			switch result.State {
			case StateSuccess, StateWarning, StateNone:
				isSuccess = false
			}
		}
	}

	return isSuccess
}

func (m *Message) AddNewApp(ctx context.Context, app string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddNewApp")
	defer span.End()
	m.lock.Lock()
	defer m.lock.Unlock()

	m.apps[app] = new(AppResults)
}

func (m *Message) AddToAppMessage(ctx context.Context, app string, result CheckResult) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddToAppMessage")
	defer span.End()
	m.lock.Lock()
	defer m.lock.Unlock()

	m.apps[app].AddCheckResult(result)
}

var hostname = ""

func init() {
	hostname, _ = os.Hostname()
}

func (m *Message) SetFooter(start time.Time, commitSha string) {
	m.footer = buildFooter(start, commitSha)
}

func (m *Message) PushComment(ctx context.Context, client Client) error {
	return client.UpdateMessage(ctx, m, buildComment(ctx, m.apps))
}

func buildFooter(start time.Time, commitSHA string) string {
	showDebug := viper.GetBool("show-debug-info")
	if !showDebug {
		return fmt.Sprintf("<small>_Done. CommitSHA: %s_<small>\n", commitSHA)
	}

	label := viper.GetString("label-filter")
	envStr := ""
	if label != "" {
		envStr = fmt.Sprintf(", Env: %s", label)
	}
	duration := time.Since(start)

	return fmt.Sprintf("<small>_Done: Pod: %s, Dur: %v, SHA: %s%s_<small>\n", hostname, duration, GitCommit, envStr)
}

// Iterate the map of all apps in this message, building a final comment from their current state
func buildComment(ctx context.Context, apps map[string]*AppResults) string {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "buildComment")
	defer span.End()

	names := getSortedKeys(apps)

	var sb strings.Builder
	sb.WriteString("# Kubechecks Report\n")

	for _, appName := range names {
		var checkStrings []string
		results := apps[appName]

		appState := StateSuccess
		for _, check := range results.results {
			var summary string
			if check.State == StateNone {
				summary = check.Summary
			} else {
				summary = fmt.Sprintf("%s %s", check.Summary, check.State.String())
			}

			msg := fmt.Sprintf("<details><summary>%s</summary>\n%s\n</details>", summary, check.Details)
			checkStrings = append(checkStrings, msg)
			appState = WorstState(appState, check.State)
		}

		sb.WriteString("<details>\n")
		sb.WriteString("<summary>\n")
		sb.WriteString(fmt.Sprintf("## ArgoCD Application Checks:`%s` %s\n", appName, appState.Emoji()))
		sb.WriteString("</summary>\n")
		sb.WriteString(strings.Join(checkStrings, "---\n"))
		sb.WriteString("</details>")
	}

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
