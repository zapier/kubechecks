# pkg/a2askills

Generic A2A skill client for kubechecks. Connects to any [A2A](https://github.com/a2aproject/A2A)-compatible
agent, discovers its skills at startup, and calls them with free-form params.

---

## Why this exists

kubechecks' aireview perform checks based on the provided diffs and existing files, as well as kubernetes cluster
status via ArgoCD. However, there are several dozens of ideas and knowledge that it should be provided to help support
review conclusions. a2askills will help add the skills dynamically, by utilising a2a protocol and connect to the compatible
agent that can provide the search result.

---

## Interface

```go
type Client interface {
    // Discover fetches skills from the agent's AgentCard (GetExtendedAgentCard).
    // Called once at startup; results are cached by the caller.
    Discover(ctx context.Context) ([]a2a.AgentSkill, error)

    // Call invokes a named skill with arbitrary params.
    // Returns the skill's JSON result string.
    Call(ctx context.Context, skill string, params map[string]any) (string, error)
}
```

`AgentClient` implements `Client`. The mockery-generated `MockClient`
(`mocks/keclient/mocks/`) satisfies it in tests.

---

## Usage

### Startup — discover skills

```go
kc, err := keclient.NewAgentClient("knowledge-engine-server.knowledge-engine:50051")
if err != nil { ... }

skills, err := kc.Discover(ctx)
// skills == []a2a.AgentSkill{
//   {ID: "knowledge_search", Name: "Knowledge Search", Description: "..."},
// }
```

### Register as aireview tools

```go
for _, skill := range skills {
    reviewTools = append(reviewTools, tools.A2ASkillTool(kc, skill, timeout))
}
```

`A2ASkillTool` builds an `aireview.Tool` directly from the `AgentSkill` —
tool name, description, and tag hints all come from the agent card.

### Call a skill directly

```go
result, err := kc.Call(ctx, "knowledge_search", map[string]any{
    "query": "pod identity migration helm chart",
    "limit": 5,
})
```

`result` is a JSON string with shape defined by the agent:
```json
{
  "entries": [
    {
      "Domain":    "convention",
      "Key":       "ack-not-terraform",
      "Content":   "Create EKS Pod Identity associations via ACK...",
      "Tags":      ["pod-identity", "iam"],
      "SourceRef": "shared-brain/abhi/pod-identity-migration/SKILL.md"
    }
  ],
  "count": 1,
  "mode": "semantic"
}
```

---

## Activation

Set one or more agent addresses. Presence of any address enables the feature —
no separate enabled flag needed. Multiple agents can be registered simultaneously.

```
KUBECHECKS_SKILL_AGENT_ADDRS=knowledge-engine-server.knowledge-engine:50051
KUBECHECKS_SKILL_AGENT_TIMEOUT=3s   # optional, default 3s
```

For multiple agents (comma-separated or repeated flag):
```
KUBECHECKS_SKILL_AGENT_ADDRS=agent1:50051,agent2:50051
```

At startup kubechecks logs each connected agent and its discovered skills:
```
skill agent connected  skills=["knowledge_search"]  addr=...
```

If discovery fails or returns no skills for an agent, that agent is skipped
and kubechecks continues with any remaining agents or without skills.

---

## Auth

None. The engine admits no-token requests as a read-only anonymous principal
when `allow-anonymous-read: true`. If auth is needed in future (e.g. the
knowledge base grows to contain sensitive entries), add a `TokenProvider` to
`AgentClient` — the gRPC transport already supports `ServiceParams` for bearer
tokens.

---

## Why map[string]any instead of typed structs?

Reads are inherently loose: the LLM decides what params to pass, the agent does
fuzzy vector matching, the LLM judges relevance from the results. `map[string]any`
fits naturally and keeps kubechecks decoupled from any specific agent's schema.

Contrast with the ingestor (`knowledge-ingestor`), which is a closed-source
companion tool where coupling to the engine's write schema is intentional — it
keeps typed `WriteParams` for compile-time enforcement of required fields.
