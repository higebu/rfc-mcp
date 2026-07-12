package rfctxt

import (
	"fmt"
	"sort"
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
// locates headings via the document's own in-text Table of Contents —
// wholesale when Tier 1's yield is hopeless, otherwise only to
// supplement Tier 1 with the TOC entries it provably missed; Tier 3
// falls back to the whole cleaned body as a single section when both
// tiers come up nearly empty, so callers always get at least one section
// to index.
func ParseRFCText(raw []byte, rfcNumber int, title string) ([]Section, error) {
	if title == "" {
		title = fmt.Sprintf("RFC %d", rfcNumber)
	}
	lines := cleanLines(raw)

	headingIdx, tocStart, tocEnd, tocFound := findTOCBlock(lines)
	tier1 := detectTier1(lines, tocStart, tocEnd)
	var tocEntries []tocEntry
	if tocFound {
		tocEntries = parseTOCEntries(lines, tocStart, tocEnd)
	}

	headings := tier1
	excludeStart, excludeEnd := 0, 0
	if tocFound && len(tocEntries) > 0 {
		if len(tier1) < 3 {
			// Tier 1 is hopeless — replace it with the TOC-anchored
			// headings outright, and excise the TOC block from the
			// header section (it's a location aid, not content).
			if tier2 := detectTier2(lines, tocEnd, tocEntries); len(tier2) >= 2 {
				headings = tier2
				excludeStart, excludeEnd = headingIdx, tocEnd
			}
		} else if hasUncoveredTOCEntries(tocEntries, tier1) {
			// Tier 1 is healthy but the TOC lists sections it never
			// found. Rather than choosing one tier wholesale — Tier 2
			// alone drops real Tier-1-only headings (front matter such
			// as an Abstract is never listed in its own TOC), Tier 1
			// alone drops headings only the TOC's title search can see
			// (RFC 1305's body headings carry no numbers at all) — keep
			// Tier 1 and graft in just the Tier-2 headings that cover
			// the gap. The TOC block itself stays in place, exactly as
			// it always has for Tier-1-parsed documents.
			if sup := supplementFromTier2(detectTier2(lines, tocEnd, tocEntries), tier1); len(sup) > 0 {
				headings = append(append([]rawHeading{}, tier1...), sup...)
				sort.Slice(headings, func(a, b int) bool { return headings[a].lineIdx < headings[b].lineIdx })
			}
		}
	}

	if len(headings) <= 1 {
		return []Section{{Number: "body", Title: title, Level: 1, Content: joinTrimmed(lines)}}, nil
	}

	headings = promoteDotZero(headings)
	headings = rescueDanglingParents(lines, headings, tocStart, tocEnd)
	headings = reparentDanglingAncestors(headings)
	return assembleSections(lines, headings, excludeStart, excludeEnd), nil
}

// tocEntryNumber returns the section number a TOC entry will produce if
// Tier 2 locates it — its own numbering token, or the slug of its title
// for numberless entries — matching the assignment detectTier2 makes.
func tocEntryNumber(e tocEntry) string {
	if e.number != "" {
		return e.number
	}
	return slugify(e.title)
}

// plausibleSupplementEntry filters what a numberless TOC line must look
// like before its absence from Tier 1 is worth acting on: its title
// must not start with a lowercase letter. A lowercase start means the
// line is almost always the wrapped continuation of the previous
// entry's title (RFC 1922's "parameter", the tail of "Registration of
// New "charset"s and New MIME parameter"), which locateHeading would
// happily match against the body heading's own wrapped continuation
// line. A digit start stays plausible: RFC 2743's TOC writes entries
// like "1.1.1.1: Credential Constructs and Concepts", whose colon keeps
// splitLeadingToken from reading the number, leaving it in the title.
func plausibleSupplementEntry(e tocEntry) bool {
	if e.number != "" {
		return true
	}
	return e.title != "" && (e.title[0] < 'a' || e.title[0] > 'z')
}

// hasUncoveredTOCEntries reports whether the TOC lists a plausible entry
// whose number no Tier-1 heading produced — the trigger for running the
// Tier-2 title search at all on a document Tier 1 otherwise handled.
func hasUncoveredTOCEntries(entries []tocEntry, tier1 []rawHeading) bool {
	covered := make(map[string]bool, len(tier1))
	for _, h := range tier1 {
		covered[h.number] = true
	}
	for _, e := range entries {
		if plausibleSupplementEntry(e) && !covered[tocEntryNumber(e)] {
			return true
		}
	}
	return false
}

// supplementFromTier2 keeps only the Tier-2 headings that add coverage
// Tier 1 lacks: both the number and the located line must be new. The
// line check catches the same physical heading found under two spellings
// (Tier 1 reads the number token off the body line, Tier 2 carries the
// TOC's; when they disagree, Tier 1's reading of its own line wins).
func supplementFromTier2(tier2, tier1 []rawHeading) []rawHeading {
	numbers := make(map[string]bool, len(tier1))
	taken := make(map[int]bool, len(tier1))
	for _, h := range tier1 {
		numbers[h.number] = true
		taken[h.lineIdx] = true
	}
	var sup []rawHeading
	for _, h := range tier2 {
		if numbers[h.number] || taken[h.lineIdx] {
			continue
		}
		if h.number == slugify(h.title) && !plausibleSupplementEntry(tocEntry{title: h.title}) {
			continue
		}
		sup = append(sup, h)
	}
	return sup
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

// rescueDanglingParents recovers a heading whose number is referenced as
// a parent by an already-detected child but wasn't itself detected --
// not because the line doesn't exist, but because detectTier1's
// precededByBlank guard rejected it. RFC 1142's body never inserts a
// blank line before a heading anywhere (document-wide blank-line density
// of 9.9%, versus 20-36% elsewhere in the fixtures), so e.g. "12.2
// Dynamic Conformance" directly follows the last wrapped line of the
// previous paragraph, and "8.4.1 Broadcast Subnetwork IIH PDUs" directly
// follows the "8.4" heading itself with no blank line between two
// headings.
//
// Rather than relaxing precededByBlank globally -- it exists
// specifically to reject RFC 1035-style column-0 body prose, e.g. "25
// (SMTP).  If this bit is set, ..." -- this re-scans only for numbers a
// real child heading already proves must exist, applying every other
// guard detectTier1 does (the TOC-block exclusion, the isTOCTrailer
// reject, the length/gap-split handling) except precededByBlank. It
// loops because rescuing one heading can itself dangle a level further
// up (RFC 1142's "8.4.1" is only provably needed once "8.4.1.5" is
// found; rescuing "8.4.1" then reveals "8.4" is needed too), stopping
// once a pass rescues nothing more.
func rescueDanglingParents(lines []string, headings []rawHeading, tocStart, tocEnd int) []rawHeading {
	out := make([]rawHeading, len(headings))
	copy(out, headings)

	for {
		exists := make(map[string]bool, len(out))
		for _, h := range out {
			exists[h.number] = true
		}
		needed := make(map[string]bool)
		for _, h := range out {
			if h.parent != "" && !exists[h.parent] {
				needed[h.parent] = true
			}
		}
		if len(needed) == 0 {
			break
		}

		rescued := false
		for i, line := range lines {
			if line == "" || (tocStart <= i && i < tocEnd) {
				continue
			}
			m := headingRE.FindStringSubmatch(line)
			if m == nil || !validHeadingMatch(m) || isTOCTrailer(line) {
				continue
			}
			number, level, parent := normalizeNumberToken(m[1])
			if !needed[number] || exists[number] {
				continue
			}
			title, tail, ok := resolveHeadingTitle(line, m[2])
			if !ok {
				continue
			}
			exists[number] = true
			out = append(out, rawHeading{lineIdx: i, number: number, title: title, level: level, parent: parent, tail: tail})
			rescued = true
		}
		if !rescued {
			break
		}
	}

	sort.Slice(out, func(a, b int) bool { return out[a].lineIdx < out[b].lineIdx })
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
