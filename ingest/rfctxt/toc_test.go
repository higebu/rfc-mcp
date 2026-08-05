package rfctxt

import "testing"

func TestSplitLeadingToken(t *testing.T) {
	tests := []struct {
		in         string
		wantNumber string
		wantTitle  string
	}{
		{"1.  Purpose and Scope", "1", "Purpose and Scope"},
		{"2.1  Requirements Language", "2.1", "Requirements Language"},
		{"II. Some Requirements", "II", "Some Requirements"},
		{"III. The Host Software", "III", "The Host Software"},
		{"Appendix F.1  Multiple Networks", "F.1", "Multiple Networks"},
		// The colon after "A" doesn't fit any token shape, so this is left
		// unstripped. That's fine for Tier 2's purposes: the same TOC line
		// and its body counterpart both fail to strip identically, so the
		// title-equality check used to locate headings still lines up (see
		// RFC 791's "APPENDIX A:  Examples & Scenarios").
		{"APPENDIX A:  Examples & Scenarios", "", "APPENDIX A:  Examples & Scenarios"},
		{"PREFACE", "", "PREFACE"},
		{"A Summary of Primitives", "", "A Summary of Primitives"},
		{"Messages", "", "Messages"},
		// RFC 1574 spells TOC entries longhand; the number must parse
		// so the body's "1.  Conventions" can be matched against it.
		{"Section 1. Conventions", "1", "Conventions"},
		{"Section 3.1 Availability", "3.1", "Availability"},
		// A digit after "Appendix" stays in the title (RFC 883):
		// reading it as number "1" would collide with real section 1.
		{"Appendix 1. Domain Name Syntax Specification", "", "Appendix 1. Domain Name Syntax Specification"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			number, title := splitLeadingToken(tt.in)
			if number != tt.wantNumber || title != tt.wantTitle {
				t.Errorf("splitLeadingToken(%q) = (%q, %q), want (%q, %q)", tt.in, number, title, tt.wantNumber, tt.wantTitle)
			}
		})
	}
}

func TestFindTOCBlock(t *testing.T) {
	lines := []string{
		"Title block",
		"",
		"Table of Contents",
		"",
		"   1.  Introduction",
		"   2.  Body",
		"",
		"1.  Introduction",
		"body text",
	}
	headingIdx, start, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if headingIdx != 2 {
		t.Errorf("headingIdx = %d, want 2", headingIdx)
	}
	if start != 3 {
		t.Errorf("start = %d, want 3", start)
	}
	if end != 7 {
		t.Errorf("end = %d, want 7 (stops at the real column-0 heading)", end)
	}
}

func TestFindTOCBlockNotFound(t *testing.T) {
	_, _, _, found := findTOCBlock([]string{"no toc here", "just body text"})
	if found {
		t.Fatal("expected not to find a TOC heading")
	}
}

func TestParseTOCEntriesStripsTrailerAndToken(t *testing.T) {
	lines := []string{
		"   1.  Purpose and Scope",
		"   2.  Introduction",
		"     2.1.  Requirements Language",
	}
	entries := parseTOCEntries(lines, 0, len(lines))
	want := []tocEntry{
		{number: "1", title: "Purpose and Scope", indent: 3},
		{number: "2", title: "Introduction", indent: 3},
		{number: "2.1", title: "Requirements Language", indent: 5},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, e, want[i])
		}
	}
}

func TestRankIndents(t *testing.T) {
	entries := []tocEntry{{indent: 4}, {indent: 0}, {indent: 2}, {indent: 0}}
	got := rankIndents(entries)
	want := map[int]int{0: 1, 2: 2, 4: 3}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("rankIndents()[%d] = %d, want %d", k, got[k], v)
		}
	}
}

func TestLocateHeadingForwardOnly(t *testing.T) {
	lines := []string{
		"Interfaces", // decoy occurrence before the search window
		"",
		"real content",
		"",
		"Interfaces",
		"more content",
	}
	idx := locateHeading(lines, 2, "Interfaces")
	if idx != 4 {
		t.Errorf("locateHeading found index %d, want 4 (should skip the earlier decoy)", idx)
	}
}

// TestLocateHeadingPrefersIsolatedMatch guards against RFC 1661's
// content-loss bug: a summary table lists "1  Configure-Request" ahead
// of the real, detailed "Configure-Request" section, and a naive
// first-match search anchors the TOC entry there instead, silently
// merging the real section's content into a neighboring one. The decoy
// sits flush against the next list entry (no blank line below), while
// the real heading is set off by blank lines on both sides.
func TestLocateHeadingPrefersIsolatedMatch(t *testing.T) {
	lines := []string{
		"      following values:",
		"",
		"         1       Configure-Request", // decoy: not isolated (list continues below)
		"         2       Configure-Ack",
		"",
		"real content before the real heading",
		"",
		"Configure-Request", // real heading: isolated
		"",
		"the real body text",
	}
	idx := locateHeading(lines, 0, "Configure-Request")
	if idx != 7 {
		t.Errorf("locateHeading found index %d, want 7 (should skip the non-isolated decoy)", idx)
	}
}

// TestLocateHeadingFallsBackToOnlyOccurrence ensures a title with no
// isolated occurrence at all is still located rather than lost — the
// isolation preference is a tiebreaker, never a hard filter.
func TestLocateHeadingFallsBackToOnlyOccurrence(t *testing.T) {
	lines := []string{
		"intro text",
		"         1       Configure-Request",
		"         2       Configure-Ack",
	}
	idx := locateHeading(lines, 0, "Configure-Request")
	if idx != 1 {
		t.Errorf("locateHeading found index %d, want 1 (only occurrence, even though not isolated)", idx)
	}
}

// TestDetectTier2LevelFromNumberDepth guards against RFC 1812's bug: a
// TOC that doesn't visually indent "4.1" under "4" must still nest them,
// using the entry number's own dot-depth rather than raw indent rank.
func TestDetectTier2LevelFromNumberDepth(t *testing.T) {
	lines := []string{
		"real content before body",
		"",
		"Introduction",
		"intro body",
		"",
		"Overview",
		"overview body",
	}
	entries := []tocEntry{
		{number: "4", title: "Introduction", indent: 0},
		{number: "4.1", title: "Overview", indent: 0},
	}
	headings := detectTier2(lines, 0, entries)
	if len(headings) != 2 {
		t.Fatalf("expected 2 located headings, got %d: %+v", len(headings), headings)
	}
	four, fourOne := headings[0], headings[1]
	if four.level != 1 || four.parent != "" {
		t.Errorf(`section "4": got level=%d parent=%q, want level=1 parent=""`, four.level, four.parent)
	}
	if fourOne.level != 2 || fourOne.parent != "4" {
		t.Errorf(`section "4.1": got level=%d parent=%q, want level=2 parent="4" (same TOC indent as "4" must not flatten it to a sibling)`, fourOne.level, fourOne.parent)
	}
}

func TestDetectTier2SkipsUnlocatableEntries(t *testing.T) {
	lines := []string{
		"real content before body",
		"",
		"Introduction",
		"intro body",
		"",
		"Conclusion",
		"conclusion body",
	}
	entries := []tocEntry{
		{title: "Introduction", indent: 0},
		{title: "Nonexistent Section", indent: 0},
		{title: "Conclusion", indent: 0},
	}
	headings := detectTier2(lines, 0, entries)
	if len(headings) != 2 {
		t.Fatalf("expected 2 located headings (unlocatable entry skipped), got %d: %+v", len(headings), headings)
	}
	if headings[0].title != "Introduction" || headings[1].title != "Conclusion" {
		t.Errorf("unexpected headings: %+v", headings)
	}
}

// TestFindTOCBlockKeepsTrailerlessEntry guards against RFC 2244's bug: a
// TOC entry with no page number at all ("C.       Full Copyright
// Statement") looks exactly like a body heading, and the old boundary
// rule ("any trailer-less heading shape ends the block") cut the TOC
// short there. The block must only end at a number the TOC already
// listed — the body restarting at "1." — so the C entry stays inside.
func TestFindTOCBlockKeepsTrailerlessEntry(t *testing.T) {
	lines := []string{
		"Table of Contents",
		"",
		"1.       Protocol Overview ....................................    4",
		"B.       ACAP Keyword Index ...................................   66",
		"C.       Full Copyright Statement",
		"",
		"1.  Protocol Overview",
		"",
		"   body text",
	}
	_, _, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 6 {
		t.Errorf("end = %d, want 6 (trailer-less C entry stays inside; block ends at the body's repeat of 1.)", end)
	}
	entries := parseTOCEntries(lines, 1, end)
	var last tocEntry
	if len(entries) > 0 {
		last = entries[len(entries)-1]
	}
	if last.number != "C" {
		t.Errorf("last entry = %+v, want the trailer-less C entry", last)
	}
}

// TestFindTOCBlockEndsAtUnnumberedBody covers RFC 1305's shape: TOC
// entries with single-space page numbers ("3.2.3.   Peer Variables 12")
// that no trailer check recognizes, followed by a body whose headings
// carry no numbers at all. The single-space entries must not end the
// block (their numbers are first occurrences), and the block must end at
// the bare-title body start via the shapeless-prose run instead.
func TestFindTOCBlockEndsAtUnnumberedBody(t *testing.T) {
	lines := []string{
		"Table of Contents",
		"",
		"1.       Introduction   1",
		"",
		"3.2.     State Variables and Parameters 9",
		"",
		"3.2.3.   Peer Variables 12",
		"",
		"Introduction",
		"This document constitutes a formal specification of the protocol",
		"and everything that follows is ordinary running body prose text.",
	}
	_, start, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 8 {
		t.Errorf("end = %d, want 8 (block ends at the bare-title body start)", end)
	}
	entries := parseTOCEntries(lines, start, end)
	if len(entries) != 3 || entries[2].number != "3.2.3" || entries[2].title != "Peer Variables" {
		t.Errorf("entries = %+v, want the single-space page numbers stripped", entries)
	}
}

// TestFindTOCBlockKnownUnnumberedTail covers RFC 9490's shape: a modern
// pageless TOC ending in a run of numberless entries ("IAB Members at
// the Time of Approval" / "Acknowledgments" / "Authors' Addresses").
// The well-known titles among them count as entry-like, so the run
// never reaches the shapeless-prose threshold and the block extends to
// the body's numbered restart.
func TestFindTOCBlockKnownUnnumberedTail(t *testing.T) {
	lines := []string{
		"Table of Contents",
		"",
		"   1.  Introduction",
		"   IAB Members at the Time of Approval",
		"   Acknowledgments",
		"   Authors' Addresses",
		"",
		"1.  Introduction",
		"",
		"   body text",
	}
	_, start, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 7 {
		t.Errorf("end = %d, want 7 (numberless tail entries stay inside the block)", end)
	}
	entries := parseTOCEntries(lines, start, end)
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4 (including the numberless tail)", len(entries))
	}
	if entries[1].title != "IAB Members at the Time of Approval" {
		t.Errorf("entries[1] = %+v, want the IAB Members entry", entries[1])
	}
}

// TestFindTOCBlockBacktracksBodyHeading: when the block ends at a
// shapeless-prose run, a trailer-less heading line immediately before
// the run is the body's first heading (its number just never appeared
// in the truncated TOC), and must be pulled back out of the block.
func TestFindTOCBlockBacktracksBodyHeading(t *testing.T) {
	lines := []string{
		"Table of Contents",
		"",
		"PART ONE",
		"",
		"1.  Overview",
		"prose line one runs here",
		"prose line two runs here",
		"prose line three runs here",
	}
	_, _, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 4 {
		t.Errorf("end = %d, want 4 (the body heading before the prose run is not TOC)", end)
	}
}

// TestFindTOCBlockPullBackKeepsColumnAlignedEntry: the prose-run
// pull-back must not reach across the TOC's own blank-line boundary and
// pull a genuine trailer-less TOC entry (RFC 2244's "C.       Full
// Copyright Statement" shape) out of the block. Such an entry is
// column-aligned — its title sits far from the numbering token to line
// up with its sibling entries' titles — while the body headings the
// pull-back exists to recover ("1.  Overview") keep a narrow gap.
func TestFindTOCBlockPullBackKeepsColumnAlignedEntry(t *testing.T) {
	lines := []string{
		"Table of Contents", // 0
		"",                  // 1
		"1.       Protocol Overview ....................................    4", // 2
		"B.       ACAP Keyword Index ...................................   66", // 3
		"C.       Full Copyright Statement",     // 4: trailer-less entry
		"",                                      // 5
		"untitled front-matter prose line one",  // 6
		"untitled front-matter prose line two",  // 7
		"untitled front-matter prose line tres", // 8
	}
	_, start, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 6 {
		t.Errorf("end = %d, want 6 (the column-aligned C entry stays inside the block)", end)
	}
	entries := parseTOCEntries(lines, start, end)
	if len(entries) != 3 || entries[2].number != "C" {
		t.Errorf("entries = %+v, want the trailer-less C entry kept as the last entry", entries)
	}
}

// TestFindTOCBlockPullBackStillRecoversNarrowGapHeading is the
// regression guard alongside the above: a narrow-gap body heading
// separated from the following prose paragraph by a blank line — the
// common modern body shape — must still be pulled back out.
func TestFindTOCBlockPullBackStillRecoversNarrowGapHeading(t *testing.T) {
	lines := []string{
		"Table of Contents", // 0
		"",                  // 1
		"PART ONE",          // 2
		"",                  // 3
		"1.  Overview",      // 4: body's first heading, narrow gap
		"",                  // 5
		"prose line one runs here",   // 6
		"prose line two runs here",   // 7
		"prose line three runs here", // 8
	}
	_, _, end, found := findTOCBlock(lines)
	if !found {
		t.Fatal("expected to find TOC heading")
	}
	if end != 4 {
		t.Errorf("end = %d, want 4 (the narrow-gap body heading is pulled back out)", end)
	}
}

func TestParseTOCEntriesStopsAtListOfFigures(t *testing.T) {
	lines := []string{
		"1.       Introduction   1",
		"List of Figures",
		"Figure 1. Implementation Model  6",
	}
	entries := parseTOCEntries(lines, 0, len(lines))
	if len(entries) != 1 || entries[0].number != "1" {
		t.Errorf("entries = %+v, want just the numbered entry before List of Figures", entries)
	}
}

func TestParseTOCEntriesSkipsPageNumberContinuation(t *testing.T) {
	lines := []string{
		"H.       Appendix H. Analysis of Errors",
		"98",
		"H.1.     Introduction   98",
	}
	entries := parseTOCEntries(lines, 0, len(lines))
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (the lone page number line is skipped)", len(entries))
	}
}

// TestLocateHeadingPrefersNearestColumnZeroMatch covers RFC 1305: the
// body heading "Introduction" sits at column 0 with a blank line above
// but body text directly below (not isolated), while an appendix much
// further down repeats the same title fully isolated. The nearest
// column-0, blank-preceded match must win, or every TOC entry after it
// anchors into the appendices.
func TestLocateHeadingPrefersNearestColumnZeroMatch(t *testing.T) {
	lines := []string{
		"",
		"Introduction",
		"This document constitutes a formal specification.",
		"",
		"more content",
		"",
		"Introduction", // appendix twin: fully isolated
		"",
	}
	idx := locateHeading(lines, 0, "Introduction")
	if idx != 1 {
		t.Errorf("locateHeading found index %d, want 1 (nearest column-0 match preceded by a blank)", idx)
	}
}
