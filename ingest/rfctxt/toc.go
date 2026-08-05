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
	// The "Section N." alternative covers TOCs that spell every entry
	// out longhand ("Section 1. Conventions", RFC 1574) — without it the
	// entry number never parses, which both starves findTOCBlock's
	// body-restart detection and leaves the entry unmatchable against
	// its body heading ("1.  Conventions"). Only "Section" takes a
	// digit: a digit after "Appendix" (RFC 883's "Appendix 1. Domain
	// Name Syntax Specification") must stay part of the title, or the
	// appendix would collide with the document's real section 1.
	leadingTokenRE = regexp.MustCompile(`^(\d+(?:\.\d+)*\.?|[A-Z](?:\.\d+)+\.?|[A-Z]\.|(?i:[ivxlcdm]+)\.?|(?i:appendix)\s+[A-Z](?:\.\d+)*\.?|(?i:section)\s+\d+(?:\.\d+)*\.?)\s+`)
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

// tocProseRunThreshold is the number of consecutive non-blank lines with
// no TOC-entry shape at all (no leading numbering token, no page-number
// trailer) that ends a TOC block. A real prose paragraph — front matter
// after the listing, or a body whose headings carry no numbers, like RFC
// 1305's — immediately produces such a run, while a wrapped TOC entry
// title only ever contributes one or two shapeless lines before its
// page-numbered continuation resets the count.
const tocProseRunThreshold = 3

// listOfRE matches the "List of Figures" / "List of Tables" caption
// listings some documents (e.g. RFC 1305) append to their Table of
// Contents. Everything from that marker on stays inside the block's
// line range (it's still location-aid material, not body), but
// parseTOCEntries stops collecting entries there: figure captions are
// never sections.
var listOfRE = regexp.MustCompile(`(?i)^list of (figures|tables)$`)

// pureNumberRE matches a line that is nothing but a page number — the
// continuation of a TOC entry whose title wrapped just before it (RFC
// 1305 renders "H.  Appendix H. ..." with its page number "98" alone on
// the next line).
var pureNumberRE = regexp.MustCompile(`^\d{1,4}$`)

// findTOCBlock locates an in-document Table of Contents by a tolerant,
// column-agnostic search: RFC 791 centers its "TABLE OF CONTENTS"
// heading, so this can't reuse Tier 1's strict column-0 regex. It
// returns the line range of entries following the heading, stopping at
// whichever comes first:
//
//   - a run of >=tocBlankRunThreshold blank lines (RFC 791's page-sized
//     gap before the body);
//   - a column-0 heading-shaped line without a page trailer whose number
//     was already listed earlier in the block — the body restarting at a
//     number the TOC itself mentioned (modern RFCs run the TOC straight
//     into "1.  Introduction", e.g. RFC 9293). Requiring the repeat,
//     rather than treating any trailer-less heading shape as the body,
//     is what keeps a trailer-less TOC entry inside the block: RFC 2244
//     lists "C.       Full Copyright Statement" with no page number at
//     all, and RFC 1305's single-space page numbers ("3.2.3.   Peer
//     Variables 12") don't register as trailers either;
//   - a run of >=tocProseRunThreshold shapeless lines (see the constant
//     above), ending the block at the run's first line. When the line
//     just before that run is itself a trailer-less column-0 heading
//     shape, it's pulled back out of the block too: it's the body's
//     first heading (its number simply never appeared in a truncated or
//     partial TOC), not a listing entry.
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
	proseRun, proseStart := 0, 0
	endedAtProse := false
	seen := make(map[string]bool)
	for i := start; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blanks++
			proseRun = 0
			if blanks >= tocBlankRunThreshold {
				end = i - blanks + 1
				break
			}
			continue
		}
		blanks = 0
		number, _ := splitLeadingToken(strings.TrimSpace(stripTOCTrailer(trimmed)))
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 0 && isHeadingLine(line) && !isTOCTrailer(line) && number != "" && seen[number] {
			end = i
			break
		}
		// Well-known unnumbered titles count as entry-like too: a
		// modern pageless TOC ends in a run of numberless entries
		// ("IAB Members at the Time of Approval" / "Acknowledgments" /
		// "Authors' Addresses", RFC 9490) that would otherwise read as
		// three shapeless lines and cut the block short. Crucially,
		// "Introduction" is not in that list, so a body of bare
		// unnumbered headings (RFC 1305) still ends the block.
		_, knownTitle := matchKnownUnnumbered(trimmed)
		entryLike := number != "" ||
			stripTOCTrailer(trimmed) != trimmed ||
			pureNumberRE.MatchString(trimmed) ||
			listOfRE.MatchString(trimmed) ||
			tocHeadingRE.MatchString(trimmed) ||
			knownTitle
		if entryLike {
			proseRun = 0
			if number != "" {
				seen[number] = true
			}
			continue
		}
		if proseRun == 0 {
			proseStart = i
		}
		proseRun++
		if proseRun >= tocProseRunThreshold {
			end = proseStart
			endedAtProse = true
			break
		}
	}
	if endedAtProse {
		// Pull the body's own leading heading(s) back out of the block:
		// at most two lines, covering a top-level heading immediately
		// followed by its first subsection with no prose in between. A
		// line with any strippable page-number tail stays inside — it's
		// a TOC entry whose page number is written with just one space
		// (RFC 1305), not a body heading. So does a line whose title is
		// column-aligned far from its numbering token (gap > 3): that's
		// a trailer-less TOC entry lined up with its siblings' titles
		// (RFC 2244's "C.       Full Copyright Statement"), not the
		// narrow-gap body heading this pull-back exists to recover —
		// without the gap cap, the blank-skipping walk-back would reach
		// across the TOC's own end boundary and pull it out.
		for range 2 {
			j := end - 1
			for j >= start && strings.TrimSpace(lines[j]) == "" {
				j--
			}
			if j < start {
				break
			}
			prev := lines[j]
			trimmedPrev := strings.TrimSpace(prev)
			if len(prev) != len(strings.TrimLeft(prev, " ")) || !isHeadingLine(prev) ||
				stripTOCTrailer(trimmedPrev) != trimmedPrev {
				break
			}
			if m := headingRE.FindStringSubmatch(prev); m != nil && len(m[0])-len(m[1])-len(m[2]) > 3 {
				break
			}
			end = j
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
// dot-leader/page-number trailer and any leading numbering token. It
// stops at a "List of Figures" / "List of Tables" marker (figure
// captions are location aids, not sections) and skips lines that are
// nothing but a page number (a wrapped entry's continuation).
func parseTOCEntries(lines []string, start, end int) []tocEntry {
	var entries []tocEntry
	for i := start; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if listOfRE.MatchString(strings.TrimSpace(line)) {
			break
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(stripTOCTrailer(strings.TrimSpace(line)))
		if trimmed == "" || pureNumberRE.MatchString(trimmed) {
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
// title. The nearest match that looks like a real heading wins: one that
// sits alone between blank lines (or a document boundary), or one at
// near-zero indentation set off by a preceding blank line — RFC 1305's
// body headings ("Introduction" at column 0) run straight into their
// first paragraph with no blank line below, and must still beat the
// identically-titled appendix headings much further down. A same-named
// decoy — e.g. a summary table listing "1  Configure-Request" ahead of
// the real, detailed section in RFC 1661 — passes neither test: it sits
// deeply indented and flush against the next list entry. The first match
// found is kept as a fallback so a title with only ever decoy-shaped
// occurrences is still located rather than lost.
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
			indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
			precededByBlank := i == 0 || strings.TrimSpace(lines[i-1]) == ""
			if precededByBlank && indent <= 3 {
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
