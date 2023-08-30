package vcs_clients

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/zapier/kubechecks/pkg"
	"go.opentelemetry.io/otel"
)

const (
	appFormat = `<details><summary>

## ArgoCD Application Checks:` + "`%s` %s" +
			`
</summary>
%s 
</details>
`
)

// Used to test messages quickly if we have to update internal emoji
var summaryEmojiRegex = regexp.MustCompile(pkg.FailedEmoji() + "|" + pkg.WarningEmoji())

// Message type that allows concurrent updates
// Has a reference to the owner/repo (ie zapier/kubechecks),
// the PR/MR id, and the actual messsage
type Message struct {
	Lock    sync.Mutex
	Name    string
	Owner   string
	CheckID int
	NoteID  int
	Msg     string
	// Key = Appname, value = Msg
	Apps   map[string]string
	Client Client
}

func (m *Message) AddToMessage(ctx context.Context, msg string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddToMessage")
	defer span.End()
	m.Lock.Lock()
	defer m.Lock.Unlock()

	m.Msg = fmt.Sprintf("%s \n\n---\n\n%s", m.Msg, msg)
	m.Client.UpdateMessage(ctx, m, m.Msg)

}

func (m *Message) AddNewApp(ctx context.Context, app string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddNewApp")
	defer span.End()
	m.Lock.Lock()
	defer m.Lock.Unlock()

	m.Apps[app] = ""

	m.Client.UpdateMessage(ctx, m, m.buildComment(ctx))
}

func (m *Message) AddToAppMessage(ctx context.Context, app string, msg string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddToAppMessage")
	defer span.End()
	m.Lock.Lock()
	defer m.Lock.Unlock()

	m.Apps[app] = fmt.Sprintf("%s \n\n---\n\n%s", m.Apps[app], msg)
	m.Client.UpdateMessage(ctx, m, m.buildComment(ctx))
}

// Iterate the map of all apps in this message, building a final comment from their current state
func (m *Message) buildComment(ctx context.Context) string {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "buildComment")
	defer span.End()

	var names []string
	for _, name := range m.Apps {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Kubechecks Report\n")
	// m.Msg = fmt.Sprintf("%s \n\n---\n\n%s", m.Msg, msg)
	for _, name := range names {
		msg := m.Apps[name]
		appEmoji := pkg.PassEmoji()

		// Test the message for failures, since we'll be showing this at the top
		if summaryEmojiRegex.MatchString(msg) {
			appEmoji = pkg.FailedEmoji()
		}

		fmt.Fprintf(&sb, appFormat, name, appEmoji, msg)
	}
	return sb.String()
}
