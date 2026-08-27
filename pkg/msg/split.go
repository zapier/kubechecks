package msg

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"
)

// ChunkConfig controls how a report is assembled into VCS comment chunks.
type ChunkConfig struct {
	MaxLength  int    // per-VCS comment size limit (bytes)
	MaxChunks  int    // hard cap on number of comments; 0 = unlimited
	Identifier string // kubechecks instance identifier shown in headers
}

const (
	// sectionSeparator is placed between app sections within a single chunk.
	sectionSeparator = "\n\n"

	// Continuation notes inserted into multi-chunk reports so the reader
	// knows the report spans several comments.
	continuedFrom = "\n*Continued from previous comment.*\n\n"
	continuedIn   = "\n\n**Continued in next comment.**"

	// truncatedNote is appended to the final chunk when the MaxChunks cap
	// caused sections to be dropped.
	truncatedNote = "\n\n**Warning**: Report exceeded the maximum number of comments. Some output was truncated."
)

// chunkHeader returns the markdown heading for a chunk.
// Single-chunk reports omit the part number for a cleaner look.
func chunkHeader(identifier string, part, total int) string {
	if total == 1 {
		return fmt.Sprintf("# Kubechecks %s Report\n", identifier)
	}
	return fmt.Sprintf("# Kubechecks %s Report (Part %d of %d)\n", identifier, part, total)
}

// maxHeaderLen returns a worst-case header length for capacity planning.
// Assumes up to 3-digit part numbers (999 chunks).
func maxHeaderLen(identifier string) int {
	return len(fmt.Sprintf("# Kubechecks %s Report (Part 999 of 999)\n", identifier))
}

// maxOverhead returns the worst-case per-chunk overhead (header + continuation
// notes). Used to compute the available space for section content.
func maxOverhead(identifier string) int {
	return maxHeaderLen(identifier) + len(continuedFrom) + len(continuedIn)
}

// SplitIntoChunks packs pre-split sections into decorated VCS comment chunks
// that each fit within cfg.MaxLength. The footer is appended only to the final
// chunk.
//
// Sections are expected to arrive pre-split from [Message.buildAppSections] so
// that each individual section already fits within the available space. This
// function handles:
//
//  1. Greedy bin-packing of sections into chunks.
//  2. Decorating each chunk with a header, continuation notes, and the footer.
//  3. Enforcing MaxChunks - if the cap is hit, the last chunk carries a
//     truncation warning.
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

		sb.WriteString(strings.Join(sections, sectionSeparator))

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
//
// Sections are expected to be pre-split by buildAppSections so that each
// individual section fits within the limit. If a section still exceeds the
// limit (defensive), it is placed alone in its own chunk with a warning log.
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
			separatorLen = len(sectionSeparator)
		}

		if sectionLen > availablePerChunk {
			log.Warn().Int("section_len", sectionLen).Int("limit", availablePerChunk).
				Msg("section exceeds chunk limit; placing in its own chunk")
			if len(current) > 0 {
				chunks = append(chunks, current)
				current = nil
				currentLen = 0
			}
			chunks = append(chunks, []string{section})
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

// countSections returns the total number of sections across all chunks.
// Used to detect whether the MaxChunks cap caused sections to be dropped.
func countSections(chunks [][]string) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}
