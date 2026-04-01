package msg

import (
	"fmt"
	"strings"
)

// ChunkConfig controls how the report is assembled into VCS comment chunks.
type ChunkConfig struct {
	MaxLength  int // per-VCS comment size limit
	MaxChunks  int // 0 = unlimited
	Identifier string
}

const (
	continuedFrom = "\n*Continued from previous comment.*\n\n"
	continuedIn   = "\n\n**Continued in next comment.**"
	truncatedNote = "\n\n**Warning**: Report exceeded the maximum number of comments. Some output was truncated."
)

// chunkHeader builds the header for chunk part (1-indexed) of total.
func chunkHeader(identifier string, part, total int) string {
	if total == 1 {
		return fmt.Sprintf("# Kubechecks %s Report\n", identifier)
	}
	return fmt.Sprintf("# Kubechecks %s Report (Part %d of %d)\n", identifier, part, total)
}

// maxHeaderLen returns a worst-case header length estimate for capacity planning.
// Assumes up to 3-digit part numbers, which covers up to 999 chunks.
func maxHeaderLen(identifier string) int {
	return len(fmt.Sprintf("# Kubechecks %s Report (Part 999 of 999)\n", identifier))
}

// maxOverhead returns the worst-case per-chunk overhead (header + continuation notes).
func maxOverhead(identifier string) int {
	return maxHeaderLen(identifier) + len(continuedFrom) + len(continuedIn)
}

// SplitIntoChunks packs appSections into decorated chunks that each fit within
// cfg.MaxLength. Footer is appended to the final chunk only.
//
// Splitting strategy:
//  1. Structural: app sections are kept intact wherever possible.
//  2. Byte-level fallback: if a single section exceeds the available space,
//     it is split at byte boundaries with open markdown constructs repaired.
//  3. Cap: if MaxChunks > 0 and chunks exceed the cap, the last chunk is
//     truncated with a warning.
func SplitIntoChunks(appSections []string, footer string, cfg ChunkConfig) []string {
	overhead := maxOverhead(cfg.Identifier)
	available := cfg.MaxLength - overhead - len(footer)
	if available <= 0 {
		available = 1
	}

	rawChunks := packSections(appSections, available)

	if cfg.MaxChunks > 0 && len(rawChunks) > cfg.MaxChunks {
		rawChunks = rawChunks[:cfg.MaxChunks]
	}

	if len(rawChunks) == 0 {
		rawChunks = [][]string{{"No changes"}}
	}

	total := len(rawChunks)
	result := make([]string, 0, total)

	for i, sections := range rawChunks {
		var sb strings.Builder
		sb.WriteString(chunkHeader(cfg.Identifier, i+1, total))

		if i > 0 {
			sb.WriteString(continuedFrom)
		}

		sb.WriteString(strings.Join(sections, "\n\n"))

		isLast := i == total-1
		if isLast {
			fmt.Fprintf(&sb, "\n\n%s", footer)
			if cfg.MaxChunks > 0 && total == cfg.MaxChunks && len(appSections) > countSections(rawChunks) {
				sb.WriteString(truncatedNote)
			}
		} else {
			sb.WriteString(continuedIn)
		}

		result = append(result, sb.String())
	}

	return result
}

// packSections greedily packs sections into chunks where each chunk's
// content does not exceed availablePerChunk bytes.
func packSections(sections []string, availablePerChunk int) [][]string {
	if len(sections) == 0 {
		return nil
	}

	var chunks [][]string
	var current []string
	currentLen := 0

	for _, section := range sections {
		sectionLen := len(section)
		separatorLen := 0
		if len(current) > 0 {
			separatorLen = 2 // "\n\n"
		}

		if sectionLen > availablePerChunk {
			if len(current) > 0 {
				chunks = append(chunks, current)
				current = nil
				currentLen = 0
			}
			parts := byteSplitMarkdown(section, availablePerChunk)
			for _, part := range parts {
				chunks = append(chunks, []string{part})
			}
			continue
		}

		if currentLen+separatorLen+sectionLen > availablePerChunk {
			chunks = append(chunks, current)
			current = []string{section}
			currentLen = sectionLen
		} else {
			current = append(current, section)
			currentLen += separatorLen + sectionLen
		}
	}

	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	return chunks
}

// byteSplitMarkdown splits content into pieces of at most maxLen bytes.
// At each split point it closes any open markdown code blocks (```) and
// <details> tags, then reopens them at the start of the next piece.
func byteSplitMarkdown(content string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{content}
	}
	if len(content) <= maxLen {
		return []string{content}
	}

	var parts []string
	remaining := content

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			parts = append(parts, remaining)
			break
		}

		splitAt := maxLen
		suffix := closingTags(remaining[:splitAt])
		prefix := reopeningTags(remaining[:splitAt])

		for len(remaining[:splitAt])+len(suffix) > maxLen && splitAt > 0 {
			splitAt--
			suffix = closingTags(remaining[:splitAt])
		}
		if splitAt == 0 {
			splitAt = maxLen
			suffix = ""
			prefix = ""
		}

		parts = append(parts, remaining[:splitAt]+suffix)
		remaining = prefix + remaining[splitAt:]
	}

	return parts
}

// closingTags returns markdown closing tags needed for any constructs left
// open in the given text (code fences and <details> tags).
func closingTags(text string) string {
	var sb strings.Builder
	if hasOpenCodeFence(text) {
		sb.WriteString("\n```\n")
	}
	openDetails := countOpen(text, "<details>", "</details>")
	for range openDetails {
		sb.WriteString("</details>")
	}
	return sb.String()
}

// reopeningTags returns the markdown opening tags that need to be prepended
// to the next chunk to continue any constructs that were left open.
func reopeningTags(text string) string {
	var sb strings.Builder
	openDetails := countOpen(text, "<details>", "</details>")
	for range openDetails {
		sb.WriteString("<details>\n")
	}
	if hasOpenCodeFence(text) {
		sb.WriteString("```\n")
	}
	return sb.String()
}

// hasOpenCodeFence returns true if the text has an odd number of ``` fences,
// meaning a code block is left open.
func hasOpenCodeFence(text string) bool {
	count := 0
	idx := 0
	for {
		pos := strings.Index(text[idx:], "```")
		if pos == -1 {
			break
		}
		count++
		idx += pos + 3
	}
	return count%2 != 0
}

// countOpen returns how many opening tags are unmatched by closing tags.
func countOpen(text, open, close string) int {
	opens := strings.Count(text, open)
	closes := strings.Count(text, close)
	n := opens - closes
	if n < 0 {
		return 0
	}
	return n
}

func countSections(chunks [][]string) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}
