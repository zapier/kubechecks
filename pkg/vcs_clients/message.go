package vcs_clients

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
)

const (
	appFormat = `<details><summary>

## ArgoCD Application Checks:` + "`%s`" +
		`
</summary>
%s 
</details>
`
)

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

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Kubechecks Report \n")
	// m.Msg = fmt.Sprintf("%s \n\n---\n\n%s", m.Msg, msg)
	for app, msg := range m.Apps {
		fmt.Fprintf(&sb, appFormat, app, msg)
	}
	return sb.String()
}
