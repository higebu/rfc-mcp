package rfctxt

import (
	"regexp"
	"sort"
	"strings"
)

var (
	// "Contents" alone covers RFC 1, the oldest RFC in the corpus, which
	// titles its listing just "CONTENTS".
	tocHeadingRE = regexp.MustCompile(`(?i)^(table of contents|contents)$`)
	// As in headingRE, the bare-letter alternative requires a trailing
	// period when there's no digit suffix, so a phrase like "A Summary
	// of Primitives" (a real, unnumbered RFC 1 sub-item) doesn't get
	// mistaken for a lettered entry "A" titled "Summary of Primitives".
	leadingTokenRE = regexp.MustCompile(`^(\d+(?:\.\d+)*\.?|[A-Z](?:\.\d+)+\.?|[A-Z]\.|(?i:[ivxlcdm]+)\.?|(?i:appendix)\s+[A-Z](?:\.\d+)*\.?)\s+`)
	bareRomanRE    = regexp.MustCompile(`(?i)^[ivxlcdm]+$`)
	// cleanNumberSegRE matches a single dot-separated segment of a
	// standard section number ("4", "1", "A"), used by numberDepth to
	// tell a real numbering token apart from a roman numeral or a
	// slugified title.
	cleanNumberSegRE = regexp.MustCompile(`^[A-Za-z0-9]+$`)
)

// numberDepth returns the hierarchical depth implied by a heading
// number's own dot-separated segments (e.g. "4.1" -> 2, "A.2" -> 2), for
// numbers shaped like a standard segment path. It reports ok=false for
// numbers this doesn't apply to (empty, roman numerals, or slugified
// titles), so callers fall back to indentation-derived ranking.
func numberDepth(number string) (depth int, ok bool) {
	if number == "" || bareRomanRE.MatchString(number) {
		return 0, false
	}
	segs := strings.Split(number, ".")
	for _, s := range segs {
		if s == "" || !cleanNumberSegRE.MatchString(s) {
			return 0, false
		}
	}
	return len(segs), true
}

// tocBlankRunThreshold is the number of consecutive blank lines that ends
// a TOC block. It must clear the gap a stripped mid-TOC page break leaves
// behind (RFC 1's TOC spans a page break between section III's and
// section IV's entries, leaving a 6-line blank run after cleanup) while
// still being smaller than a genuine end-of-TOC gap (RFC 791 leaves a
// 22-line blank run — much of a page — between its last entry and the
// real body).
const tocBlankRunThreshold = 10

// findTOCBlock locates an in-document Table of Contents by a tolerant,
// column-agnostic search: RFC 791 centers its "TABLE OF CONTENTS"
// heading, so this can't reuse Tier 1's strict column-0 regex. It returns
// the line range of entries following the heading, stopping at whichever
// comes first: a run of >=tocBlankRunThreshold blank lines, or a column-0
// line that itself looks like a real Tier-1 heading and carries no
// page-number trailer (modern, unpaginated RFCs run the TOC straight
// into section 1 with only a single blank line in between, e.g. RFC
// 9293).
func findTOCBlock(lines []string) (headingIdx, start, end int, found bool) {
	for i, line := range lines {
		if tocHeadingRE.MatchString(strings.TrimSpace(line)) {
			headingIdx, found = i, true
			break
		}
	}
	if !found {
		return 0, 0, 0, false
	}
	start = headingIdx + 1
	end = len(lines)
	blanks := 0
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			blanks++
			if blanks >= tocBlankRunThreshold {
				end = i - blanks + 1
				break
			}
			continue
		}
		blanks = 0
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && headingRE.MatchString(line) && !tocTrailerRE.MatchString(line) {
			end = i
			break
		}
	}
	return headingIdx, start, end, true
}

// tocEntry is one parsed line of the in-document Table of Contents.
type tocEntry struct {
	number string // leading numbering token (decimal, roman, or appendix letter), if any
	title  string // entry text with the token and page-number trailer stripped
	indent int    // leading-space count, used to derive sub-level depth
}

// parseTOCEntries extracts entries from the TOC block, stripping the
// dot-leader/page-number trailer and any leading numbering token.
func parseTOCEntries(lines []string, start, end int) []tocEntry {
	var entries []tocEntry
	for i := start; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(tocTrailerRE.ReplaceAllString(strings.TrimSpace(line), ""))
		if trimmed == "" {
			continue
		}
		number, title := splitLeadingToken(trimmed)
		entries = append(entries, tocEntry{number: number, title: title, indent: indent})
	}
	return entries
}

// splitLeadingToken splits a TOC or body line into its leading numbering
// token (if any) and the remaining title text.
func splitLeadingToken(s string) (number, title string) {
	m := leadingTokenRE.FindStringSubmatchIndex(s)
	if m == nil {
		return "", s
	}
	token := s[m[2]:m[3]]
	title = strings.TrimSpace(s[m[1]:])
	token = strings.TrimRight(token, ".:")
	if idx := strings.IndexByte(token, ' '); idx >= 0 {
		// "Appendix F" / "APPENDIX A" -> keep just the letter.
		token = token[idx+1:]
	}
	return token, title
}

// detectTier2 locates each TOC entry's heading in the body by a
// forward-only, case-folded title search starting right after the TOC
// block, then derives a level/parent tree — preferably from the entry
// number's own dot-depth (so "4.1" nests under "4" even when the TOC
// doesn't visually indent it), falling back to TOC indentation depth for
// numbers with no such standard shape (roman numerals, slugified
// titles). Unlocatable entries are skipped, never fatal.
func detectTier2(lines []string, tocEnd int, entries []tocEntry) []rawHeading {
	if len(entries) == 0 {
		return nil
	}
	indentLevel := rankIndents(entries)

	type located struct {
		entry   tocEntry
		lineIdx int
	}
	var locs []located
	pos := tocEnd
	for _, e := range entries {
		idx := locateHeading(lines, pos, e.title)
		if idx < 0 {
			continue
		}
		locs = append(locs, located{e, idx})
		pos = idx + 1
	}
	if len(locs) < 2 {
		return nil
	}

	type frame struct {
		level  int
		number string
	}
	var stack []frame
	seen := make(map[string]bool)
	var out []rawHeading
	for _, l := range locs {
		level := indentLevel[l.entry.indent]
		if bareRomanRE.MatchString(l.entry.number) {
			// Right-justifying "I."/"II."/"III."/"IV." to align their
			// dots (as RFC 1 does) gives each a different indent even
			// though they're siblings; a bare roman numeral is always a
			// top-level part in classic document structure.
			level = 1
		} else if d, ok := numberDepth(l.entry.number); ok {
			// Prefer the number's own dot-depth over raw indentation
			// rank: some documents (e.g. RFC 1812) don't visually indent
			// "4.1" under "4" in the TOC at all, which would otherwise
			// flatten them to siblings with no parent link.
			level = d
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		parent := ""
		if len(stack) > 0 {
			parent = stack[len(stack)-1].number
		}
		number := l.entry.number
		if number == "" {
			number = slugify(l.entry.title)
		}
		if seen[number] {
			continue
		}
		seen[number] = true
		out = append(out, rawHeading{lineIdx: l.lineIdx, number: number, title: l.entry.title, level: level, parent: parent})
		stack = append(stack, frame{level, number})
	}
	return out
}

// rankIndents maps each distinct indentation width seen among TOC
// entries to a 1-based level rank, adapting to whatever indent step a
// given document actually uses instead of assuming a fixed width.
func rankIndents(entries []tocEntry) map[int]int {
	seen := make(map[int]bool)
	var widths []int
	for _, e := range entries {
		if !seen[e.indent] {
			seen[e.indent] = true
			widths = append(widths, e.indent)
		}
	}
	sort.Ints(widths)
	levels := make(map[int]int, len(widths))
	for i, w := range widths {
		levels[w] = i + 1
	}
	return levels
}

// locateHeading scans forward from line index from for a line whose
// title (after stripping any leading numbering token) case-fold matches
// title. Among matches, it prefers one that sits alone between blank
// lines (or a document boundary) over one that doesn't: a real heading
// is set off from surrounding text this way, while a same-named decoy —
// e.g. a summary table listing "1  Configure-Request" ahead of the real,
// detailed "5.1.  Configure-Request" section in RFC 1661 — sits flush
// against neighboring list entries. The first match found is kept as a
// fallback so a title with only ever one (non-isolated) occurrence is
// still located rather than lost.
func locateHeading(lines []string, from int, title string) int {
	fallback := -1
	for i := from; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		_, candidate := splitLeadingToken(line)
		if strings.EqualFold(candidate, title) {
			if isIsolatedLine(lines, i) {
				return i
			}
			if fallback < 0 {
				fallback = i
			}
		}
	}
	return fallback
}

// isIsolatedLine reports whether both neighbors of lines[i] are blank
// (or the line sits at a document boundary).
func isIsolatedLine(lines []string, i int) bool {
	precededByBlank := i == 0 || strings.TrimSpace(lines[i-1]) == ""
	followedByBlank := i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == ""
	return precededByBlank && followedByBlank
}
