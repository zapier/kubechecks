package vcs_clients

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
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
	Client  Client
}

func (m *Message) AddToMessage(ctx context.Context, msg string) {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "AddToMessage")
	defer span.End()
	m.Lock.Lock()
	defer m.Lock.Unlock()

	m.Msg = fmt.Sprintf("%s \n\n---\n\n%s", m.Msg, msg)
	m.Client.UpdateMessage(ctx, m, m.Msg)

}
