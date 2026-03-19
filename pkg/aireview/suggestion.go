package aireview

import "sync"

// Suggestion represents a code suggestion to post on a PR file.
type Suggestion struct {
	Path       string // file path in the PR
	StartLine  int    // optional, first line for multi-line suggestion (0 = single line)
	EndLine    int    // line number in the PR diff (RIGHT side)
	Body       string // explanation of the issue
	Suggestion string // the corrected code to replace the line(s)
}

// SuggestionCollector accumulates suggestions during a review (thread-safe).
type SuggestionCollector struct {
	mu          sync.Mutex
	suggestions []Suggestion
}

// NewSuggestionCollector creates a new empty collector.
func NewSuggestionCollector() *SuggestionCollector {
	return &SuggestionCollector{}
}

// Add records a suggestion.
func (c *SuggestionCollector) Add(s Suggestion) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.suggestions = append(c.suggestions, s)
}

// Suggestions returns all collected suggestions.
func (c *SuggestionCollector) Suggestions() []Suggestion {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]Suggestion, len(c.suggestions))
	copy(result, c.suggestions)
	return result
}

// Len returns the number of collected suggestions.
func (c *SuggestionCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.suggestions)
}
