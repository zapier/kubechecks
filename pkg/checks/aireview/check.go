package aireview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	"github.com/zapier/kubechecks/pkg/helmchart"
	client "github.com/zapier/kubechecks/pkg/kubernetes"
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

// WithChartCache enables Helm chart introspection tools with the given cache.
func WithChartCache(cache *helmchart.Cache) NewCheckerOption {
	return func(c *Checker) { c.chartCache = cache }
}

// WithMultiCluster enables multi-cluster Kubernetes tools.
func WithMultiCluster(mcm *client.MultiClusterManager) NewCheckerOption {
	return func(c *Checker) { c.multiCluster = mcm }
}

// Checker holds the AI review agent and its configuration.
type Checker struct {
	provider      aiproviders.Provider
	maxTurns      int
	timeout       time.Duration
	systemPrompt  string
	prometheusURL string
	chartCache    *helmchart.Cache
	multiCluster  *client.MultiClusterManager
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

	// Add local Kubernetes tools if client is available
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

	// Add remote cluster tools if multi-cluster is configured
	if c.multiCluster != nil {
		reviewTools = append(reviewTools,
			tools.ListClustersTool(c.multiCluster),
			tools.RemoteKubernetesQueryTool(c.multiCluster),
			tools.RemoteListNamespacesTool(c.multiCluster),
		)
	}

	// Add Prometheus tools if URL is configured
	if c.prometheusURL != "" {
		reviewTools = append(reviewTools,
			tools.PrometheusRangeQueryTool(c.prometheusURL),
			tools.PrometheusInstantQueryTool(c.prometheusURL),
		)
	}

	// Add Helm chart introspection tools if cache is configured
	if c.chartCache != nil {
		chartTools := c.buildChartTools(request)
		reviewTools = append(reviewTools, chartTools...)
	} else {
		log.Debug().Caller().Msg("chart cache not configured, skipping helm chart introspection tools")
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

	// Extract user-provided Helm values if available
	helmValues := extractHelmValues(request)

	// Bundle diff, manifests, and Helm values inline so the LLM can start reviewing immediately
	renderedManifestsText := strings.Join(request.YamlManifests, "\n---\n")
	userPrompt := aireview.BuildUserPrompt(request.AppName, renderedDiff, renderedManifestsText, helmValues, toolNames)

	// Run the agentic loop — blocking call
	eventID := fmt.Sprintf("mr-%d/%s", request.Note.CheckID, request.AppName)
	agent := c.buildAgent()
	result, err := agent.Run(ctx, eventID, systemPrompt, userPrompt, reviewTools)
	if err != nil {
		telemetry.SetError(span, err, "AI Review")
		log.Error().Caller().Err(err).Str("app", request.AppName).Msg("AI review failed")
		return msg.Result{}, nil // Return empty result — worker skips posting when Details is empty
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

// buildChartTools creates Helm chart introspection tools based on the app's source type.
// Handles both direct chart references (spec.source.chart) and umbrella charts (Chart.yaml with dependencies).
func (c *Checker) buildChartTools(request checks.Request) []aireview.Tool {
	src := request.App.Spec.GetSource()
	log.Debug().Caller().
		Str("RepoURL", src.RepoURL).
		Str("Chart", src.Chart).
		Str("Path", src.Path).
		Msg("checking helm chart source for introspection tools")

	// Case 1: Direct chart reference (e.g., repoURL=https://charts.example.com, chart=podinfo)
	if src.Chart != "" && src.RepoURL != "" {
		chartSources := []tools.ChartSource{
			{Name: src.Chart, Version: src.TargetRevision, Repository: src.RepoURL},
		}
		return []aireview.Tool{
			tools.ListChartFilesTool(c.chartCache, chartSources),
			tools.ReadChartFileTool(c.chartCache, chartSources),
		}
	}

	// Case 2: Git repo with path — check for Chart.yaml with dependencies (umbrella chart)
	if src.Path != "" && request.Repo != nil {
		chartYAMLPath := filepath.Join(request.Repo.Directory, src.Path, "Chart.yaml")
		deps, err := helmchart.ParseDependencies(chartYAMLPath)
		if err != nil {
			log.Debug().Caller().Err(err).Str("path", chartYAMLPath).Msg("no Chart.yaml dependencies found, skipping chart tools")
			return nil
		}

		var chartSources []tools.ChartSource
		for _, dep := range deps {
			if dep.Repository == "" || dep.Name == "" {
				continue
			}
			log.Debug().Caller().
				Str("dep", dep.Name).
				Str("version", dep.Version).
				Str("repo", dep.Repository).
				Msg("found chart dependency")
			chartSources = append(chartSources, tools.ChartSource{
				Name:       dep.Name,
				Version:    dep.Version,
				Repository: dep.Repository,
			})
		}
		if len(chartSources) == 0 {
			return nil
		}
		return []aireview.Tool{
			tools.ListChartFilesTool(c.chartCache, chartSources),
			tools.ReadChartFileTool(c.chartCache, chartSources),
		}
	}

	log.Debug().Caller().Msg("no helm chart source detected, skipping chart introspection tools")
	return nil
}

// extractHelmValues extracts user-provided Helm values from the ArgoCD Application spec.
// Includes inline values and values from valueFiles (read from the cloned repo).
func extractHelmValues(request checks.Request) string {
	src := request.App.Spec.GetSource()
	if src.Helm == nil {
		return ""
	}

	var parts []string

	// Inline values
	if src.Helm.Values != "" {
		parts = append(parts, "# Inline values (spec.source.helm.values)")
		parts = append(parts, src.Helm.Values)
	}

	// Value files — read from cloned repo if available
	if request.Repo != nil && len(src.Helm.ValueFiles) > 0 {
		for _, vf := range src.Helm.ValueFiles {
			filePath := filepath.Join(request.Repo.Directory, src.Path, vf)
			data, err := os.ReadFile(filePath)
			if err != nil {
				log.Debug().Caller().Err(err).Str("file", vf).Msg("could not read values file")
				continue
			}
			parts = append(parts, fmt.Sprintf("# Values file: %s", vf))
			parts = append(parts, string(data))
		}
	}

	return strings.Join(parts, "\n---\n")
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
