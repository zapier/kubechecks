package aireview

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"k8s.io/client-go/dynamic"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aiproviders"
	"github.com/zapier/kubechecks/pkg/aireview"
	"github.com/zapier/kubechecks/pkg/aireview/tools"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/checks/diff"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/checks/aireview")

// NewCheckerConfig holds the required configuration for creating a Checker.
type NewCheckerConfig struct {
	// Provider is the LLM provider to use.
	Provider aiproviders.Provider
}

// NewCheckerOption configures optional Checker settings.
type NewCheckerOption func(*Checker)

// WithMaxTurns sets the maximum number of tool use iterations.
func WithMaxTurns(n int) NewCheckerOption {
	return func(c *Checker) { c.maxTurns = n }
}

// WithTimeout sets the timeout per review.
func WithTimeout(d time.Duration) NewCheckerOption {
	return func(c *Checker) { c.timeout = d }
}

// WithSystemPrompt sets a custom system prompt, overriding the default.
func WithSystemPrompt(prompt string) NewCheckerOption {
	return func(c *Checker) { c.systemPrompt = prompt }
}

// WithPrometheusURL enables Prometheus/Thanos tools with the given endpoint.
func WithPrometheusURL(url string) NewCheckerOption {
	return func(c *Checker) { c.prometheusURL = url }
}

// Checker holds the AI review agent and its configuration.
type Checker struct {
	provider      aiproviders.Provider
	maxTurns      int
	timeout       time.Duration
	systemPrompt  string
	prometheusURL string
}

// New creates a Checker with the given config and options.
func New(cfg *NewCheckerConfig, opts ...NewCheckerOption) *Checker {
	// Checker Config values should be resolved from KUBECHECKS_AI_REVIEW_MAX_TURNS and KUBECHECKS_AI_REVIEW_TIMEOUT
	// New() is initializing with a default value.
	c := &Checker{
		provider: cfg.Provider,
		maxTurns: 20,
		timeout:  5 * time.Minute,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Checker) buildAgent() *aireview.Agent {
	return aireview.NewAgent(c.provider,
		aireview.WithMaxTurns(c.maxTurns),
		aireview.WithTimeout(c.timeout),
	)
}

// Check implements the processor function signature for kubechecks.
func (c *Checker) Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	ctx, span := tracer.Start(ctx, "AIReview")
	defer span.End()

	log.Debug().Caller().Str("app", request.AppName).Msg("running AI impact review")

	// Generate diff text using the shared diff logic
	renderedDiff, err := diff.GenerateDiffText(ctx, request)
	if err != nil {
		log.Warn().Caller().Err(err).Str("app", request.AppName).Msg("failed to generate diff for AI review, continuing with manifests only")
		renderedDiff = ""
	}

	// Build app info from the ArgoCD Application spec
	app := request.App
	namespace := app.Spec.Destination.Namespace
	cluster := app.Spec.Destination.Server
	if app.Spec.Destination.Name != "" {
		cluster = app.Spec.Destination.Name
	}
	project := app.Spec.Project
	sourceInfo := formatSourceInfo(app)

	// Build tools as closures over the request data
	reviewTools := []aireview.Tool{
		tools.DiffTool(renderedDiff),
		tools.RenderedManifestsTool(request.YamlManifests),
		tools.AppInfoTool(request.AppName, namespace, cluster, project, sourceInfo),
	}

	// Add Kubernetes tools if client is available
	if request.Container.KubeClientSet != nil {
		clientset := request.Container.KubeClientSet.ClientSet()
		dynamicClient, err := dynamic.NewForConfig(request.Container.KubeClientSet.Config())
		if err != nil {
			log.Warn().Caller().Err(err).Msg("failed to create dynamic k8s client, skipping k8s tools")
		} else {
			reviewTools = append(reviewTools,
				tools.KubernetesQueryTool(dynamicClient, clientset.Discovery()),
				tools.ListNamespacesTool(clientset),
			)
		}
	}

	// Add Prometheus tools if URL is configured
	if c.prometheusURL != "" {
		reviewTools = append(reviewTools,
			tools.PrometheusRangeQueryTool(c.prometheusURL),
			tools.PrometheusInstantQueryTool(c.prometheusURL),
		)
	}

	// Build prompts
	systemPrompt := aireview.BuildSystemPrompt(
		request.AppName,
		namespace,
		cluster,
		request.KubernetesVersion,
		c.systemPrompt,
	)

	toolNames := make([]string, len(reviewTools))
	for i, t := range reviewTools {
		toolNames[i] = t.Def.Name
	}
	userPrompt := aireview.BuildUserPrompt(request.AppName, toolNames)

	// Run the agentic loop — blocking call
	eventID := fmt.Sprintf("mr-%d/%s", request.Note.CheckID, request.AppName)
	agent := c.buildAgent()
	result, err := agent.Run(ctx, eventID, systemPrompt, userPrompt, reviewTools)
	if err != nil {
		telemetry.SetError(span, err, "AI Review")
		return msg.Result{
			State:   pkg.StateNone,
			Summary: "AI review failed",
			Details: fmt.Sprintf("AI review error: %s", err.Error()),
		}, nil // Return nil error so other checks continue
	}

	return msg.Result{
		State:   parseRecommendationState(result),
		Summary: "<b>AI Impact Review</b>",
		Details: result,
	}, nil
}

// recommendationTagRe matches the machine-readable recommendation tag emitted by the LLM.
// Example: <!--RECOMMENDATION:FLAG-->
var recommendationTagRe = regexp.MustCompile(`<!--RECOMMENDATION:(APPROVE|WARN|FLAG)-->`)

// parseRecommendationState extracts the commit state from the machine-readable tag in the AI review output.
func parseRecommendationState(review string) pkg.CommitState {
	match := recommendationTagRe.FindStringSubmatch(review)
	if len(match) < 2 {
		return pkg.StateNone
	}
	switch match[1] {
	case "FLAG":
		return pkg.StateError
	case "WARN":
		return pkg.StateWarning
	case "APPROVE":
		return pkg.StateSuccess
	default:
		return pkg.StateNone
	}
}

// formatSourceInfo extracts source information from the ArgoCD Application.
func formatSourceInfo(app v1alpha1.Application) string {
	sources := app.Spec.GetSources()
	if len(sources) == 0 {
		return "no source configured"
	}

	var parts []string
	for _, src := range sources {
		info := fmt.Sprintf("repo=%s", src.RepoURL)
		if src.Path != "" {
			info += fmt.Sprintf(", path=%s", src.Path)
		}
		if src.Chart != "" {
			info += fmt.Sprintf(", chart=%s", src.Chart)
		}
		if src.TargetRevision != "" {
			info += fmt.Sprintf(", revision=%s", src.TargetRevision)
		}
		if src.Helm != nil && len(src.Helm.ValueFiles) > 0 {
			info += fmt.Sprintf(", valueFiles=%s", strings.Join(src.Helm.ValueFiles, ","))
		}
		parts = append(parts, info)
	}
	return strings.Join(parts, "; ")
}
