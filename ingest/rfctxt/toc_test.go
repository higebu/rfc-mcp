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
