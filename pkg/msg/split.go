package msg

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/zapier/kubechecks/pkg"
)

// A report becomes comments in three steps. buildAppSections renders every app
// as sections that fit sectionBudget, cutting inside a check only when it has
// to. packSections fills chunks with them in order. frameChunks gives each chunk
// a header and a tail; the part count in the header is why nothing can be framed
// before everything is packed. cutShort is the backstop for a section that could
// not be made to fit.
//
// A report that takes a single comment has no continuation notes to leave room
// for, so BuildComment sizes it against oneCommentBudget first and packs it into
// sectionBudget chunks only when it does not fit.

type chunkConfig struct {
	MaxLength  int // bytes
	MaxChunks  int
	Identifier string
	Footer     string
}

const (
	footerSeparator = "\n\n"

	continuedFrom = "\n*Continued from previous comment.*\n\n"
	continuedIn   = "\n\n**Continued in next comment.**"
	truncatedNote = "\n\n**Warning**: Report exceeded the maximum number of comments. Some output was truncated."
	cutShortNote  = "\n\n**Warning**: This comment was too long and was cut short."
)

func chunkHeader(identifier string, part, total int) string {
	if total == 1 {
		return fmt.Sprintf("# Kubechecks %s Report\n", identifier)
	}
	return fmt.Sprintf("# Kubechecks %s Report (Part %d of %d)\n", identifier, part, total)
}

// sectionBudget is the space left for app sections in a chunk. We do not know
// up front which chunk ends up last, so every chunk is budgeted for the worse
// of the two tails.
func (cfg chunkConfig) sectionBudget() int {
	head := len(chunkHeader(cfg.Identifier, pkg.MaxCommentsPerCheck, pkg.MaxCommentsPerCheck)) + len(continuedFrom)
	tail := max(len(continuedIn), len(footerSeparator)+len(cfg.Footer)+len(truncatedNote))

	return cfg.MaxLength - head - tail
}

// oneCommentBudget is the space left for app sections in a report that takes a
// single comment: it has no continuation notes to leave room for, and its header
// is the plain one, so it is sized with that comment's overhead only.
func (cfg chunkConfig) oneCommentBudget() int {
	return cfg.MaxLength - len(chunkHeader(cfg.Identifier, 1, 1)) - len(footerSeparator) - len(cfg.Footer)
}

func (cfg chunkConfig) fitsOneComment(sections []string) bool {
	total := 0
	for _, section := range sections {
		total += len(section)
	}

	return total <= cfg.oneCommentBudget()
}

func splitIntoChunks(appSections []string, cfg chunkConfig) []string {
	rawChunks := packSections(appSections, cfg.sectionBudget())

	truncated := len(rawChunks) > cfg.MaxChunks
	if truncated {
		rawChunks = rawChunks[:cfg.MaxChunks]
	}

	return frameChunks(rawChunks, truncated, cfg)
}

func frameChunks(chunks [][]string, truncated bool, cfg chunkConfig) []string {
	if len(chunks) == 0 || (len(chunks) == 1 && len(chunks[0]) == 0) {
		chunks = [][]string{{"No changes"}}
	}

	total := len(chunks)
	result := make([]string, 0, total)

	for i, sections := range chunks {
		body := chunkHeader(cfg.Identifier, i+1, total)
		if i > 0 {
			body += continuedFrom
		}
		body += strings.Join(sections, "")

		tail := continuedIn
		if i == total-1 {
			tail = footerSeparator + cfg.Footer
			if truncated {
				tail += truncatedNote
			}
		}

		if len(body)+len(tail) > cfg.MaxLength {
			// only a section that could not be split gets here, e.g. a check summary the size of a comment
			log.Warn().Int("length", len(body)+len(tail)).Int("limit", cfg.MaxLength).Msg("comment does not fit, cutting it short")
			body = cutShort(body, cfg.MaxLength-len(tail))
		}

		result = append(result, body+tail)
	}

	return result
}

// cutShort cuts markdown down to maxLen and closes what the cut left open, so
// that the tail of the comment and the comments after it still render.
func cutShort(markdown string, maxLen int) string {
	budget := maxLen - len(cutShortNote)
	for room := budget; room > 0; {
		cut := cutAtRune(markdown, room)

		var state mdState
		for _, line := range strings.SplitAfter(cut, "\n") {
			state = state.next(line)
		}
		// the cut is most likely in the middle of a line
		closer := "\n" + state.closer()

		if over := len(cut) + len(closer) - budget; over > 0 {
			room -= over
			continue
		}
		return cut + closer + cutShortNote
	}
	return cutAtRune(markdown, maxLen)
}

func packSections(sections []string, budget int) [][]string {
	var chunks [][]string
	var current []string
	currentLen := 0

	for _, section := range sections {
		if len(current) > 0 && currentLen+len(section) > budget {
			chunks = append(chunks, current)
			current, currentLen = nil, 0
		}
		current = append(current, section)
		currentLen += len(section)
	}

	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	return chunks
}
