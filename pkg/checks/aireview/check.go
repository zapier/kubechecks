package aireview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/aiproviders"
	"github.com/zapier/kubechecks/pkg/aireview"
	"github.com/zapier/kubechecks/pkg/aireview/tools"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/checks/diff"
	"github.com/zapier/kubechecks/pkg/helmchart"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
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

// WithModel sets the model to use for AI review. Overrides the provider's default.
func WithModel(model string) NewCheckerOption {
	return func(c *Checker) { c.model = model }
}

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

// WithChartCache enables Helm chart introspection tools with the given cache.
func WithChartCache(cache *helmchart.Cache) NewCheckerOption {
	return func(c *Checker) { c.chartCache = cache }
}

// WithExtraInstructions appends additional instructions to the system prompt.
func WithExtraInstructions(instructions string) NewCheckerOption {
	return func(c *Checker) { c.extraInstructions = instructions }
}

// Checker holds the AI review agent and its configuration.
type Checker struct {
	provider          aiproviders.Provider
	model             string
	maxTurns          int
	timeout           time.Duration
	systemPrompt      string
	extraInstructions string
	chartCache        *helmchart.Cache
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
		aireview.WithModel(c.model),
		aireview.WithMaxTurns(c.maxTurns),
		aireview.WithTimeout(c.timeout),
		aireview.WithMaxOutputTokens(8192),
		aireview.WithTemperature(0.2),
	)
}

// AggregateReviews consolidates multiple per-app reviews into a single concise review.
func (c *Checker) AggregateReviews(ctx context.Context, appReviews map[string]string) (string, error) {
	return aireview.AggregateReviews(ctx, c.provider, c.model, appReviews)
}

// Check runs the AI review and returns the result with any code suggestions.
func (c *Checker) Check(ctx context.Context, request checks.Request) (vcs.AIReviewResult, error) {
	ctx, span := tracer.Start(ctx, "AIReview")
	defer span.End()

	log.Debug().Caller().Str("app", request.AppName).Msg("running AI impact review")

	// Use pre-computed diff if available, otherwise generate it
	renderedDiff := request.RenderedDiff
	if renderedDiff == "" {
		var err error
		renderedDiff, err = diff.GenerateDiffText(ctx, request)
		if err != nil {
			log.Warn().Caller().Err(err).Str("app", request.AppName).Msg("failed to generate diff for AI review, continuing with manifests only")
			renderedDiff = ""
		}
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

	// Add ArgoCD-backed resource tools — queries live state via ArgoCD API (works across all clusters)
	if request.Container.ArgoClient != nil {
		reviewTools = append(reviewTools,
			tools.QueryAppResourcesTool(request.Container.ArgoClient),
			tools.GetAppResourceTool(request.Container.ArgoClient),
		)
	}

	// Add Helm chart introspection tools if cache is configured
	if c.chartCache != nil {
		chartTools := c.buildChartTools(request)
		reviewTools = append(reviewTools, chartTools...)
	} else {
		log.Debug().Caller().Msg("chart cache not configured, skipping helm chart introspection tools")
	}

	// Add suggestion tool — collects suggestions to post as review comments after the review
	suggestionCollector := aireview.NewSuggestionCollector()
	if len(request.ChangedFiles) > 0 {
		reviewTools = append(reviewTools, tools.PostSuggestionTool(suggestionCollector, request.ChangedFiles))
	}

	// Add recommendation tool — collects structured recommendations (worst-wins)
	recommendationCollector := aireview.NewRecommendationCollector()
	reviewTools = append(reviewTools, tools.SubmitRecommendationTool(recommendationCollector))

	// Build prompts
	systemPrompt := aireview.BuildSystemPrompt(
		request.AppName,
		namespace,
		cluster,
		request.KubernetesVersion,
		c.systemPrompt,
		c.extraInstructions,
	)

	toolNames := make([]string, len(reviewTools))
	for i, t := range reviewTools {
		toolNames[i] = t.Def.Name
	}

	// Extract user-provided Helm values if available
	helmValues := extractHelmValues(request)

	// Build changed files content with line numbers for accurate suggestions
	changedFilesContent := buildChangedFilesContent(request)

	// Bundle diff, manifests, and Helm values inline so the LLM can start reviewing immediately
	renderedManifestsText := strings.Join(request.YamlManifests, "\n---\n")
	userPrompt := aireview.BuildUserPrompt(request.AppName, renderedDiff, renderedManifestsText, helmValues, changedFilesContent, toolNames)

	// Run the agentic loop — blocking call
	eventID := fmt.Sprintf("mr-%d/%s", request.Note.CheckID, request.AppName)
	agent := c.buildAgent()
	result, err := agent.Run(ctx, eventID, systemPrompt, userPrompt, reviewTools)
	if err != nil {
		telemetry.SetError(span, err, "AI Review")
		log.Error().Caller().Err(err).Str("app", request.AppName).Msg("AI review failed")
		return vcs.AIReviewResult{}, fmt.Errorf("ai review agent failed: %w", err)
	}

	// Convert collected suggestions to vcs.ReviewSuggestion
	var suggestions []vcs.ReviewSuggestion
	for _, s := range suggestionCollector.Suggestions() {
		suggestions = append(suggestions, vcs.ReviewSuggestion{
			Path:       s.Path,
			StartLine:  s.StartLine,
			EndLine:    s.EndLine,
			Body:       s.Body,
			Suggestion: s.Suggestion,
		})
	}

	if len(suggestions) > 0 {
		log.Info().Caller().Int("count", len(suggestions)).Str("app", request.AppName).Msg("AI review collected code suggestions")
	}

	// Build final details: LLM review text + recommendation chain
	details := result
	if recSummary := recommendationCollector.Summary(); recSummary != "" {
		details += "\n\n" + recSummary
	}

	// Determine commit state from recommendations (worst-wins)
	// Default to success if the LLM completed without submitting any recommendations
	state := recommendationCollector.State()
	if recommendationCollector.Len() == 0 {
		state = pkg.StateSuccess
	}
	log.Info().Caller().
		Str("app", request.AppName).
		Str("state", state.BareString()).
		Int("recommendations", recommendationCollector.Len()).
		Int("suggestions", len(suggestions)).
		Msg("AI review completed")

	return vcs.AIReviewResult{
		Result: msg.Result{
			State:   state,
			Summary: "<b>AI Impact Review</b>",
			Details: details,
		},
		Suggestions: suggestions,
	}, nil
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
// Values are shown with line numbers so the LLM can reference correct lines for suggestions.
func extractHelmValues(request checks.Request) string {
	src := request.App.Spec.GetSource()
	if src.Helm == nil {
		return ""
	}

	var parts []string

	// Inline values
	if src.Helm.Values != "" {
		parts = append(parts, "# Inline values (spec.source.helm.values)")
		parts = append(parts, addLineNumbers(src.Helm.Values))
	}

	// Value files — read from cloned repo if available, with line numbers
	if request.Repo != nil && len(src.Helm.ValueFiles) > 0 {
		repoDir := request.Repo.Directory
		absRepoDir, absErr := filepath.Abs(repoDir)
		for _, vf := range src.Helm.ValueFiles {
			filePath := filepath.Join(repoDir, src.Path, vf)
			// Protect against path traversal
			if absErr == nil {
				absPath, err := filepath.Abs(filePath)
				if err != nil || !strings.HasPrefix(absPath, absRepoDir+string(filepath.Separator)) {
					log.Warn().Str("file", vf).Msg("skipping value file with path outside repo directory")
					continue
				}
			}
			data, err := os.ReadFile(filePath)
			if err != nil {
				// some app doesn't have values-<clusterName>.yaml and will error here, dont bother print them.
				continue
			}
			// Include the file path that matches the PR's changed files list
			relPath := filepath.Join(src.Path, vf)
			parts = append(parts, fmt.Sprintf("# Values file: %s (use this path and line numbers for post_suggestion)", relPath))
			parts = append(parts, addLineNumbers(string(data)))
		}
	}

	return strings.Join(parts, "\n---\n")
}

// addLineNumbers prepends line numbers to each line of content.
func addLineNumbers(content string) string {
	lines := strings.Split(content, "\n")
	numbered := make([]string, len(lines))
	for i, line := range lines {
		numbered[i] = fmt.Sprintf("%4d | %s", i+1, line)
	}
	return strings.Join(numbered, "\n")
}

// buildChangedFilesContent reads changed files from the repo with line numbers.
// This gives the LLM exact line references for post_suggestion.
func buildChangedFilesContent(request checks.Request) string {
	if request.Repo == nil || len(request.ChangedFiles) == 0 {
		return ""
	}

	var parts []string
	repoDir := request.Repo.Directory
	for _, f := range request.ChangedFiles {
		filePath := filepath.Join(repoDir, f)
		// Protect against path traversal — ensure the resolved path stays within the repo
		absPath, err := filepath.Abs(filePath)
		if err != nil {
			continue
		}
		absRepoDir, err := filepath.Abs(repoDir)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(absPath, absRepoDir+string(filepath.Separator)) {
			log.Warn().Str("file", f).Msg("skipping file with path outside repo directory")
			continue
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("### File: `%s`\n```\n%s\n```", f, addLineNumbers(string(data))))
	}

	if len(parts) == 0 {
		return ""
	}

	return "## Changed Files (with line numbers)\nUse these line numbers when calling post_suggestion.\n\n" + strings.Join(parts, "\n\n")
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
