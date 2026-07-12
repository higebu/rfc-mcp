package rfctxt

import (
	"fmt"
	"strings"
)

// Section is one logical section of a parsed RFC .txt body. Content is
// preserved verbatim — never dedented, never reflowed — so that ABNF and
// packet-diagram alignment survive intact.
type Section struct {
	Number       string // "4.1", "A.2", or a slug like "abstract"; "body" for the Tier-3 whole-body fallback
	Title        string
	Level        int
	ParentNumber string
	Content      string
}

// ParseRFCText cleans and sections a raw RFC .txt body. rfcNumber and
// title are used only for the Tier-3 whole-body fallback label; if title
// is empty, rfcNumber is used to build one.
//
// Parsing runs in three cascading tiers: Tier 1 detects column-0
// numbered/lettered headings plus well-known unnumbered headings; Tier 2
// falls back to locating headings via the document's own in-text Table
// of Contents when Tier 1's yield looks too small; Tier 3 falls back to
// the whole cleaned body as a single section when both tiers come up
// nearly empty, so callers always get at least one section to index.
func ParseRFCText(raw []byte, rfcNumber int, title string) ([]Section, error) {
	if title == "" {
		title = fmt.Sprintf("RFC %d", rfcNumber)
	}
	lines := cleanLines(raw)

	tier1 := detectTier1(lines)
	headingIdx, tocStart, tocEnd, tocFound := findTOCBlock(lines)
	var tocEntries []tocEntry
	if tocFound {
		tocEntries = parseTOCEntries(lines, tocStart, tocEnd)
	}

	headings := tier1
	excludeStart, excludeEnd := 0, 0
	if len(tier1) < 3 || (tocFound && len(tocEntries) > 0 && len(tier1) < len(tocEntries)) {
		if tier2 := detectTier2(lines, tocEnd, tocEntries); len(tier2) >= 2 {
			headings = tier2
			excludeStart, excludeEnd = headingIdx, tocEnd
		}
	}

	if len(headings) <= 1 {
		return []Section{{Number: "body", Title: title, Level: 1, Content: joinTrimmed(lines)}}, nil
	}

	headings = promoteDotZero(headings)
	headings = reparentDanglingAncestors(headings)
	return assembleSections(lines, headings, excludeStart, excludeEnd), nil
}

// promoteDotZero recognizes the legacy "N.0" top-level-section
// convention (e.g. RFC 3371 numbers its chapters "1.0", "2.0", ... with
// "1.1", "1.2" as real subsections, never a bare "1" heading of its
// own). Left alone, normalizeNumberToken-style depth counting treats
// "N.0" as a depth-2 child of a phantom "N", so "N.1"/"N.2" end up as
// siblings of "N.0" pointing at a parent number that was never actually
// detected as a heading, instead of nesting under "N.0" itself. When no
// independent "N" heading exists, this promotes "N.0" up one level and
// reparents any children that were pointing at the phantom "N" onto
// "N.0" instead. Docs where "N" legitimately exists as its own heading
// (so "N.0" is a genuine, deeper child) are left untouched.
func promoteDotZero(headings []rawHeading) []rawHeading {
	numberSet := make(map[string]bool, len(headings))
	for _, h := range headings {
		numberSet[h.number] = true
	}
	out := make([]rawHeading, len(headings))
	copy(out, headings)
	for i, h := range out {
		if !strings.HasSuffix(h.number, ".0") {
			continue
		}
		base := strings.TrimSuffix(h.number, ".0")
		if numberSet[base] {
			continue
		}
		newParent := ""
		if idx := strings.LastIndex(base, "."); idx >= 0 {
			newParent = base[:idx]
		}
		out[i].level--
		out[i].parent = newParent
		for j := range out {
			if j == i {
				continue
			}
			if out[j].parent == base {
				out[j].parent = h.number
			}
		}
	}
	return out
}

// reparentDanglingAncestors fixes up a heading whose parent number was
// never itself detected as a heading in this document -- e.g. RFC 1211's
// "A.1"/"A.2"/... appendix entries, whose document never writes a bare
// "A" heading at all, or RFC 892's "7.0.1", whose "7.0" heading exists in
// the raw text but goes undetected because the token-to-title gap is
// wider than headingRE tolerates. Left alone, such a section's Parent
// points at a number no Section will ever have, which both silently
// breaks get_toc's implied nesting (the missing parent's indent level is
// skipped over rather than shown) and truncates get_section's
// include_subsections recursion the moment it's asked for an
// existing ancestor further up the chain, since the walk cannot cross a
// parent_number link to a row that was never inserted.
//
// This walks each dangling parent up its own number, stripping one
// trailing dot-segment at a time, until it finds a number that does
// correspond to a detected heading, and reparents onto that instead (or
// to the document root if none exists at all). Every heading's Level is
// then recomputed from its (possibly just-repaired) parent chain, so the
// fix cascades correctly to deeper descendants too instead of leaving a
// level gap of its own.
func reparentDanglingAncestors(headings []rawHeading) []rawHeading {
	out := make([]rawHeading, len(headings))
	copy(out, headings)

	exists := make(map[string]bool, len(out))
	for _, h := range out {
		exists[h.number] = true
	}

	for i, h := range out {
		if h.parent == "" || exists[h.parent] {
			continue
		}
		out[i].parent = nearestExistingAncestor(h.parent, exists)
	}

	recomputeLevels(out)
	return out
}

// nearestExistingAncestor strips trailing ".segment" pieces from
// candidate until it finds one present in exists, or returns "" (the
// document root) once no dot is left to strip.
func nearestExistingAncestor(candidate string, exists map[string]bool) string {
	for candidate != "" {
		if exists[candidate] {
			return candidate
		}
		idx := strings.LastIndex(candidate, ".")
		if idx < 0 {
			return ""
		}
		candidate = candidate[:idx]
	}
	return ""
}

// recomputeLevels assigns each heading's Level as its parent's Level + 1
// (1 for a top-level heading), walking the ParentNumber chain rather than
// trusting each heading's own previously-computed Level -- reparenting
// one heading changes the effective depth of everything nested under it
// too. The hop counter caps the walk at len(headings) so a cyclic
// parent_number chain, should one ever occur, can't loop forever.
func recomputeLevels(headings []rawHeading) {
	index := make(map[string]int, len(headings))
	for i, h := range headings {
		index[h.number] = i
	}
	for i := range headings {
		depth := 1
		cur := i
		for hops := 0; hops < len(headings); hops++ {
			parent := headings[cur].parent
			if parent == "" {
				break
			}
			pi, ok := index[parent]
			if !ok {
				break
			}
			cur = pi
			depth++
		}
		headings[i].level = depth
	}
}

// assembleSections turns raw heading positions into Sections, filling in
// Content from the lines between one heading and the next. Text before
// the first heading is kept as a synthetic "header" section (title-block
// boilerplate, etc.) rather than silently dropped, unless it's empty.
// excludeStart/excludeEnd cut the located in-document Table of Contents
// block out of that header text — Tier 2 must never emit the TOC as a
// section, since it's just a location aid, not real content.
func assembleSections(lines []string, headings []rawHeading, excludeStart, excludeEnd int) []Section {
	var sections []Section

	headerLines := lines[:headings[0].lineIdx]
	if excludeEnd > excludeStart {
		headerLines = append(append([]string{}, lines[:excludeStart]...), lines[excludeEnd:headings[0].lineIdx]...)
	}
	if header := joinTrimmed(headerLines); header != "" {
		sections = append(sections, Section{Number: "header", Title: "Header", Level: 1, Content: header})
	}

	for i, h := range headings {
		endIdx := len(lines)
		if i+1 < len(headings) {
			endIdx = headings[i+1].lineIdx
		}
		var contentLines []string
		if h.tail != "" {
			contentLines = append(contentLines, h.tail)
		}
		contentLines = append(contentLines, lines[h.lineIdx+1:endIdx]...)
		sections = append(sections, Section{
			Number:       h.number,
			Title:        h.title,
			Level:        h.level,
			ParentNumber: h.parent,
			Content:      joinTrimmed(contentLines),
		})
	}
	return sections
}

// joinTrimmed joins lines with "\n" after dropping leading/trailing
// fully-blank lines, without touching anything in between.
func joinTrimmed(lines []string) string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}
