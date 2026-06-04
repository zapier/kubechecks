// Package a2askills extends the kubechecks AI review with dynamically discovered
// skills from any A2A-compatible agent. The AI reviewer already has access to
// diffs, rendered manifests, and live Kubernetes cluster state via ArgoCD —
// a2askills adds the team's accumulated conventions, patterns, and incident
// learnings by connecting to an external agent at startup and registering its
// skills as review tools. Skills are discovered via the A2A AgentCard protocol
// so kubechecks remains decoupled from any specific agent's schema.
package a2askills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2agrpc/v0"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is the interface for interacting with an A2A-compatible skill agent.
// Skills are discovered via Discover; each skill is called via Call with
// free-form params — no hardcoded schema on the caller's side.
//
//go:generate mockery --name Client
type Client interface {
	// Discover fetches the agent's published skills from its AgentCard.
	// Called once at startup; results are cached by the caller.
	Discover(ctx context.Context) ([]a2a.AgentSkill, error)

	// Call invokes a named skill with arbitrary params.
	// Returns the skill's JSON result string.
	Call(ctx context.Context, skill string, params map[string]any) (string, error)
}

// AgentClient is a read-only A2A gRPC client.
// Sends no auth token — the engine serves it as an anonymous read-only principal.
type AgentClient struct {
	transport a2aclient.Transport
}

var _ Client = (*AgentClient)(nil)

// NewAgentClient dials the A2A agent at serverAddr.
func NewAgentClient(serverAddr string) (*AgentClient, error) {
	conn, err := grpc.NewClient(serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("a2a dial %s: %w", serverAddr, err)
	}
	return &AgentClient{transport: a2agrpc.NewGRPCTransport(conn)}, nil
}

// Discover fetches the extended AgentCard and returns the declared skills.
// Returns an empty slice (not an error) when the agent exposes no card.
func (c *AgentClient) Discover(ctx context.Context) ([]a2a.AgentSkill, error) {
	card, err := c.transport.GetExtendedAgentCard(ctx, nil, &a2a.GetExtendedAgentCardRequest{})
	if err != nil {
		return nil, fmt.Errorf("get agent card: %w", err)
	}
	if card == nil {
		return nil, nil
	}
	return card.Skills, nil
}

// Call invokes skill on the agent with the given params.
// params is marshaled to JSON and sent as the skill's DataPart payload.
func (c *AgentClient) Call(ctx context.Context, skill string, params map[string]any) (string, error) {
	req := &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser,
			a2a.NewDataPart(map[string]any{
				"skill":  skill,
				"params": params,
			}),
		),
	}
	req.Message.ID = uuid.New().String()

	result, err := c.transport.SendMessage(ctx, nil, req) // nil = no auth header
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	return extractArtifact(result)
}

func extractArtifact(result a2a.SendMessageResult) (string, error) {
	task, ok := result.(*a2a.Task)
	if !ok {
		return "", fmt.Errorf("unexpected result type %T", result)
	}
	if task.Status.State == a2a.TaskStateFailed {
		if task.Status.Message != nil {
			for _, p := range task.Status.Message.Parts {
				if t := p.Text(); t != "" {
					return "", fmt.Errorf("task failed: %s", t)
				}
			}
		}
		return "", fmt.Errorf("task failed (no error detail)")
	}
	if len(task.Artifacts) == 0 || len(task.Artifacts[0].Parts) == 0 {
		return "", fmt.Errorf("task completed with no artifact")
	}
	data := task.Artifacts[0].Parts[0].Data()
	if data == nil {
		return "", fmt.Errorf("artifact part has no data")
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal artifact: %w", err)
	}
	return string(out), nil
}
