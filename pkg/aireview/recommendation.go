package aireview

import (
	"fmt"
	"strings"
	"sync"

	"github.com/zapier/kubechecks/pkg"
)

// RecommendationEntry represents a single recommendation from the LLM.
type RecommendationEntry struct {
	Recommendation string // APPROVE, WARN, FLAG
	Reason         string // brief explanation
	Source         string // what check produced this (e.g. "values validation")
}

// RecommendationCollector accumulates recommendations during a review (thread-safe).
// Multiple recommendations can be submitted; the final state is worst-wins.
type RecommendationCollector struct {
	mu      sync.Mutex
	entries []RecommendationEntry
}

// NewRecommendationCollector creates a new empty collector.
func NewRecommendationCollector() *RecommendationCollector {
	return &RecommendationCollector{}
}

// Add records a recommendation.
func (c *RecommendationCollector) Add(entry RecommendationEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, entry)
}

// Entries returns all collected recommendations.
func (c *RecommendationCollector) Entries() []RecommendationEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]RecommendationEntry, len(c.entries))
	copy(result, c.entries)
	return result
}

// Len returns the number of collected recommendations.
func (c *RecommendationCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// State returns the worst commit state across all recommendations.
func (c *RecommendationCollector) State() pkg.CommitState {
	c.mu.Lock()
	defer c.mu.Unlock()

	worst := pkg.StateNone
	for _, e := range c.entries {
		worst = pkg.WorstState(worst, MapRecommendation(e.Recommendation))
	}
	return worst
}

// Summary returns a markdown summary of all recommendations showing the full chain.
func (c *RecommendationCollector) Summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) == 0 {
		return ""
	}

	worst := pkg.StateNone
	for _, e := range c.entries {
		worst = pkg.WorstState(worst, MapRecommendation(e.Recommendation))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "<details>\n<summary><b>Recommendation Chain</b> — Final: %s (worst-wins)</summary>\n\n", worst.BareString())
	sb.WriteString("| # | Recommendation | Source | Reason |\n")
	sb.WriteString("|---|----------------|--------|--------|\n")

	for i, e := range c.entries {
		emoji := recommendationEmoji(e.Recommendation)
		fmt.Fprintf(&sb, "| %d | %s %s | %s | %s |\n", i+1, emoji, e.Recommendation, e.Source, e.Reason)
	}

	sb.WriteString("\n</details>")

	return sb.String()
}

// MapRecommendation maps a recommendation string to a CommitState.
func MapRecommendation(rec string) pkg.CommitState {
	switch strings.ToUpper(rec) {
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

func recommendationEmoji(rec string) string {
	switch strings.ToUpper(rec) {
	case "FLAG":
		return ":red_circle:"
	case "WARN":
		return ":warning:"
	case "APPROVE":
		return ":white_check_mark:"
	default:
		return ":question:"
	}
}
