package rfctxt

import (
	"os"
	"sort"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return raw
}

func numberSet(sections []Section) map[string]bool {
	set := make(map[string]bool, len(sections))
	for _, s := range sections {
		set[s.Number] = true
	}
	return set
}

func sectionByNumber(t *testing.T, sections []Section, number string) Section {
	t.Helper()
	for _, s := range sections {
		if s.Number == number {
			return s
		}
	}
	t.Fatalf("no section with number %q found", number)
	return Section{}
}

func assertNoPaginationArtifacts(t *testing.T, sections []Section) {
	t.Helper()
	for _, s := range sections {
		if strings.Contains(s.Content, "\x0c") {
			t.Errorf("section %q content contains a form-feed artifact", s.Number)
		}
		if strings.Contains(s.Content, "[Page") {
			t.Errorf("section %q content contains a page-footer artifact: %q", s.Number, s.Content)
		}
	}
}

func assertNoDuplicateNumbers(t *testing.T, sections []Section) {
	t.Helper()
	seen := make(map[string]bool)
	for _, s := range sections {
		if seen[s.Number] {
			t.Errorf("duplicate section number %q", s.Number)
		}
		seen[s.Number] = true
	}
}

// TestParseRFC9293 exercises the modern, unpaginated case (Tier 1 only).
// The expected numbered-section set below was derived independently, by
// scanning the raw fixture with a standalone regex (see the milestone
// investigation), not by calling this package's own code.
func TestParseRFC9293(t *testing.T) {
	raw := loadFixture(t, "rfc9293.txt")
	sections, err := ParseRFCText(raw, 9293, "Transmission Control Protocol (TCP)")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	wantNumbered := []string{
		"1", "2", "2.1", "2.2", "3", "3.1", "3.2", "3.2.1", "3.2.2", "3.3", "3.3.1", "3.3.2",
		"3.4", "3.4.1", "3.4.2", "3.4.3", "3.5", "3.5.1", "3.5.2", "3.5.3", "3.6", "3.6.1",
		"3.7", "3.7.1", "3.7.2", "3.7.3", "3.7.4", "3.7.5", "3.8", "3.8.1", "3.8.2", "3.8.3",
		"3.8.4", "3.8.5", "3.8.6", "3.8.6.1", "3.8.6.2", "3.8.6.2.1", "3.8.6.2.2", "3.8.6.3",
		"3.9", "3.9.1", "3.9.1.1", "3.9.1.2", "3.9.1.3", "3.9.1.4", "3.9.1.5", "3.9.1.6",
		"3.9.1.7", "3.9.1.8", "3.9.1.9", "3.9.2", "3.9.2.1", "3.9.2.2", "3.9.2.3", "3.10",
		"3.10.1", "3.10.2", "3.10.3", "3.10.4", "3.10.5", "3.10.6", "3.10.7", "3.10.7.1",
		"3.10.7.2", "3.10.7.3", "3.10.7.4", "3.10.8", "4", "5", "6", "7", "8", "8.1", "8.2",
		"A", "A.1", "A.1.1", "A.1.2", "A.2", "A.3", "A.4", "B",
	}

	got := numberSet(sections)
	for _, n := range wantNumbered {
		if !got[n] {
			t.Errorf("expected numbered section %q not found", n)
		}
	}
	knownSlugs := make(map[string]bool, len(knownUnnumbered)+1)
	knownSlugs["header"] = true
	for _, title := range knownUnnumbered {
		knownSlugs[slugify(title)] = true
	}
	var gotNumbered []string
	for n := range got {
		if !knownSlugs[n] {
			gotNumbered = append(gotNumbered, n)
		}
	}
	if len(gotNumbered) != len(wantNumbered) {
		sort.Strings(gotNumbered)
		t.Errorf("got %d numbered sections, want %d\ngot:  %v", len(gotNumbered), len(wantNumbered), gotNumbered)
	}

	for _, slug := range []string{"abstract", "table-of-contents", "header"} {
		if !got[slug] {
			t.Errorf("expected slug section %q not found", slug)
		}
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	a1 := sectionByNumber(t, sections, "A.1")
	if a1.ParentNumber != "A" || a1.Level != 2 {
		t.Errorf("A.1: got parent=%q level=%d, want parent=%q level=2", a1.ParentNumber, a1.Level, "A")
	}
	deep := sectionByNumber(t, sections, "3.8.6.2.1")
	if deep.ParentNumber != "3.8.6.2" || deep.Level != 5 {
		t.Errorf("3.8.6.2.1: got parent=%q level=%d, want parent=%q level=5", deep.ParentNumber, deep.Level, "3.8.6.2")
	}
}

// TestParseRFC4271 exercises the classic, heavily paginated case (104 form
// feeds) still handled entirely by Tier 1.
func TestParseRFC4271(t *testing.T) {
	raw := loadFixture(t, "rfc4271.txt")
	sections, err := ParseRFCText(raw, 4271, "A Border Gateway Protocol 4 (BGP-4)")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	got := numberSet(sections)
	for _, n := range []string{"4.1", "F", "F.1", "F.2", "F.3", "F.4", "F.5", "F.6"} {
		if !got[n] {
			t.Errorf("expected section %q not found", n)
		}
	}
	f1 := sectionByNumber(t, sections, "F.1")
	if f1.ParentNumber != "F" {
		t.Errorf("F.1: got parent=%q, want %q", f1.ParentNumber, "F")
	}

	// The BGP message-header diagram in 4.1 must survive byte-for-byte.
	want := `      0                   1                   2                   3
      0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
      +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
      |                                                               |
      +                                                               +
      |                                                               |
      +                                                               +
      |                           Marker                              |
      +                                                               +
      |                                                               |
      +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
      |          Length               |      Type     |
      +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+`
	s41 := sectionByNumber(t, sections, "4.1")
	if !strings.Contains(s41.Content, want) {
		t.Errorf("section 4.1 does not contain the expected diagram verbatim")
	}
}

// TestParseRFC1035 exercises the flush-left-body case: heading detection
// must not be fooled by column-0 body text ("25 (SMTP)." in section
// 3.3.11), and the 6.4.1/6.4.2 heading-and-body-on-one-line split must
// work.
func TestParseRFC1035(t *testing.T) {
	raw := loadFixture(t, "rfc1035.txt")
	sections, err := ParseRFCText(raw, 1035, "DOMAIN NAMES - IMPLEMENTATION AND SPECIFICATION")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	got := numberSet(sections)
	if got["A"] {
		t.Errorf(`false-positive section "A" detected from body prose ("A host can participate...")`)
	}

	s641 := sectionByNumber(t, sections, "6.4.1")
	if s641.Title != "The contents of inverse queries and responses" {
		t.Errorf("6.4.1 title = %q, want the heading text without the split-off tail", s641.Title)
	}
	if !strings.HasPrefix(s641.Content, "Inverse\nqueries reverse the mappings") {
		t.Errorf("6.4.1 content = %q, want it to start with the split-off tail followed by the next line", s641.Content)
	}
	if s641.ParentNumber != "6.4" {
		t.Errorf("6.4.1 parent = %q, want %q", s641.ParentNumber, "6.4")
	}
}

// TestParseRFC2119 is a small-baseline sanity check: verify the exact
// section count against the real file rather than assuming a number.
func TestParseRFC2119(t *testing.T) {
	raw := loadFixture(t, "rfc2119.txt")
	sections, err := ParseRFCText(raw, 2119, "Key words for use in RFCs to Indicate Requirement Levels")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	got := numberSet(sections)
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "abstract", "status-of-this-memo"} {
		if !got[n] {
			t.Errorf("expected section %q not found", n)
		}
	}
	if len(sections) != 12 {
		t.Errorf("got %d sections, want 12 (header + status + abstract + 9 numbered)", len(sections))
	}
}

// TestParseRFC791 exercises the oldest formatting style with centered
// top-level headings that Tier 1's column-0 regex can't see: Tier 2
// (TOC-anchored) must recover the major sections instead.
func TestParseRFC791(t *testing.T) {
	raw := loadFixture(t, "rfc791.txt")
	sections, err := ParseRFCText(raw, 791, "INTERNET PROTOCOL")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	got := numberSet(sections)
	for _, n := range []string{"1", "1.1", "1.2", "1.3", "1.4", "2", "2.1", "2.2", "2.3", "2.4", "3", "3.1", "3.2", "3.3"} {
		if !got[n] {
			t.Errorf("Tier 2 did not recover major section %q", n)
		}
	}
	one := sectionByNumber(t, sections, "1")
	if one.Title != "INTRODUCTION" {
		t.Errorf(`section "1" title = %q, want "INTRODUCTION"`, one.Title)
	}
	oneOne := sectionByNumber(t, sections, "1.1")
	if oneOne.ParentNumber != "1" {
		t.Errorf(`section "1.1" parent = %q, want "1"`, oneOne.ParentNumber)
	}
}

// TestParseRFC1 exercises the very first RFC: free-form, roman-numeral,
// mostly-unnumbered headings, recovered via Tier 2 anchored on its bare
// "CONTENTS" listing. Tier 1 alone only finds 2 headings here (a
// disambiguation regex requires a trailing period on bare-letter tokens,
// which "I." satisfies but "II."/"III."/"IV." don't shape-match, and most
// of the document's headings carry no number at all), so Tier 2 kicking
// in is exercising the intended fallback cascade, even though it lands on
// a real structural recovery rather than the single-section whole-body
// fallback.
func TestParseRFC1(t *testing.T) {
	raw := loadFixture(t, "rfc1.txt")
	sections, err := ParseRFCText(raw, 1, "Host Software")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}

	assertNoPaginationArtifacts(t, sections)
	assertNoDuplicateNumbers(t, sections)

	got := numberSet(sections)
	for _, n := range []string{"I", "II", "III", "IV"} {
		if !got[n] {
			t.Errorf("expected top-level roman-numeral section %q not found", n)
		}
	}
	for _, title := range []string{"Messages", "Links", "Simple Use", "Experiment One", "Experiment Two"} {
		found := false
		for _, s := range sections {
			if s.Title == title {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected sub-section titled %q not found", title)
		}
	}
}

// TestParseRFCTextTier3Fallback exercises the whole-body fallback
// directly, without depending on a real fixture landing in that tier.
func TestParseRFCTextTier3Fallback(t *testing.T) {
	raw := []byte("Just some free-form text.\nNo headings here at all.\nJust prose.\n")
	sections, err := ParseRFCText(raw, 9999, "Untitled Document")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1 (Tier 3 fallback)", len(sections))
	}
	s := sections[0]
	if s.Number != "body" {
		t.Errorf("Tier 3 section Number = %q, want %q", s.Number, "body")
	}
	if s.Title != "Untitled Document" {
		t.Errorf("Tier 3 section Title = %q, want %q", s.Title, "Untitled Document")
	}
	if !strings.Contains(s.Content, "Just some free-form text.") {
		t.Errorf("Tier 3 section Content does not contain the original body")
	}
}

// TestPromoteDotZeroPromotesWhenBaseMissing exercises the legacy "N.0"
// top-level convention (e.g. RFC 3371's "1.0", "2.0", ... chapters, with
// no separate bare "1"/"2" heading ever appearing): "1.0" must be
// promoted to level 1 with no parent, and "1.1"/"1.2" must nest under
// "1.0" rather than under the phantom "1".
func TestPromoteDotZeroPromotesWhenBaseMissing(t *testing.T) {
	headings := []rawHeading{
		{number: "1.0", title: "Introduction", level: 2, parent: "1"},
		{number: "1.1", title: "Background", level: 2, parent: "1"},
		{number: "1.2", title: "Scope", level: 2, parent: "1"},
		{number: "2.0", title: "Overview", level: 2, parent: "2"},
	}
	got := promoteDotZero(headings)

	if got[0].level != 1 || got[0].parent != "" {
		t.Errorf(`"1.0": got level=%d parent=%q, want level=1 parent=""`, got[0].level, got[0].parent)
	}
	if got[1].parent != "1.0" {
		t.Errorf(`"1.1": got parent=%q, want "1.0"`, got[1].parent)
	}
	if got[2].parent != "1.0" {
		t.Errorf(`"1.2": got parent=%q, want "1.0"`, got[2].parent)
	}
	if got[3].level != 1 || got[3].parent != "" {
		t.Errorf(`"2.0": got level=%d parent=%q, want level=1 parent=""`, got[3].level, got[3].parent)
	}
}

// TestReparentDanglingAncestorsPromotesToRoot exercises RFC 1211's shape:
// its appendix entries "A.1"/"A.2"/... are never preceded by a bare "A"
// heading anywhere in the document. With no existing ancestor at any
// depth, they must become top-level rather than pointing at a parent
// number no section will ever have.
func TestReparentDanglingAncestorsPromotesToRoot(t *testing.T) {
	headings := []rawHeading{
		{number: "4", title: "Summary", level: 1, parent: ""},
		{number: "A.1", title: "Inquiry Message", level: 2, parent: "A"},
		{number: "A.2", title: "Mailbox Was Re-added", level: 2, parent: "A"},
	}
	got := reparentDanglingAncestors(headings)

	byNumber := make(map[string]rawHeading, len(got))
	for _, h := range got {
		byNumber[h.number] = h
	}
	for _, number := range []string{"A.1", "A.2"} {
		h := byNumber[number]
		if h.parent != "" || h.level != 1 {
			t.Errorf("%s: got parent=%q level=%d, want parent=\"\" level=1 (no ancestor exists anywhere)", number, h.parent, h.level)
		}
	}
}

// TestReparentDanglingAncestorsSkipsMissingIntermediate exercises RFC
// 892's shape: "7" is a real, detected heading, but "7.0" itself was
// never detected (its token-to-title gap is wider than headingRE
// tolerates), so "7.0.1"/"7.0.2"'s recorded parent "7.0" doesn't
// correspond to any heading. They must reparent onto "7" instead, one
// level shallower than their number's own segment count would imply.
func TestReparentDanglingAncestorsSkipsMissingIntermediate(t *testing.T) {
	headings := []rawHeading{
		{number: "7", title: "Protocol Classes", level: 1, parent: ""},
		{number: "7.0.1", title: "Characteristics of Class 0", level: 3, parent: "7.0"},
		{number: "7.0.2", title: "Functions of Class 0", level: 3, parent: "7.0"},
	}
	got := reparentDanglingAncestors(headings)

	byNumber := make(map[string]rawHeading, len(got))
	for _, h := range got {
		byNumber[h.number] = h
	}
	for _, number := range []string{"7.0.1", "7.0.2"} {
		h := byNumber[number]
		if h.parent != "7" || h.level != 2 {
			t.Errorf(`%s: got parent=%q level=%d, want parent="7" level=2 (skip the undetected "7.0")`, number, h.parent, h.level)
		}
	}
}

// TestReparentDanglingAncestorsLeavesNormalHierarchyAlone is the
// regression guard: a document where every parent link already points at
// a real heading must come out unchanged.
func TestReparentDanglingAncestorsLeavesNormalHierarchyAlone(t *testing.T) {
	headings := []rawHeading{
		{number: "3", title: "Functional Specification", level: 1, parent: ""},
		{number: "3.1", title: "Header Format", level: 2, parent: "3"},
		{number: "3.1.1", title: "Source Port", level: 3, parent: "3.1"},
	}
	got := reparentDanglingAncestors(headings)
	for i, h := range got {
		if h != headings[i] {
			t.Errorf("heading %d changed: got %+v, want %+v (unchanged)", i, h, headings[i])
		}
	}
}

// TestPromoteDotZeroLeavesRealNestingAlone is the regression guard: when
// a bare "1" heading genuinely exists elsewhere in the document, "1.0"
// is a real, deeper child of it and must be left untouched.
func TestPromoteDotZeroLeavesRealNestingAlone(t *testing.T) {
	headings := []rawHeading{
		{number: "1", title: "Chapter One", level: 1, parent: ""},
		{number: "1.0", title: "Preamble", level: 2, parent: "1"},
		{number: "1.1", title: "Background", level: 2, parent: "1"},
	}
	got := promoteDotZero(headings)

	if got[1].level != 2 || got[1].parent != "1" {
		t.Errorf(`"1.0": got level=%d parent=%q, want level=2 parent="1" (bare "1" exists, so "1.0" is a real child, not the phantom-parent case)`, got[1].level, got[1].parent)
	}
}

// TestRescueDanglingParentsRecoversMissingHeading exercises RFC 1142's
// shape: "12.2 Dynamic Conformance" directly follows the last wrapped
// line of the previous paragraph with no blank line, so detectTier1's
// precededByBlank guard never finds it. Its already-detected child
// "12.2.3" proves the heading must exist, so the rescue pass must find
// it by re-scanning the raw lines without that guard.
func TestRescueDanglingParentsRecoversMissingHeading(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"some wrapped paragraph text ending here.",
		"12.2 Dynamic Conformance",
		"",
		"12.2.3 Decision Process Conformance",
		"body text",
	}
	headings := []rawHeading{
		{lineIdx: 5, number: "12.2.3", title: "Decision Process Conformance", level: 3, parent: "12.2"},
	}
	got := rescueDanglingParents(lines, headings, 0, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 headings after rescue, got %d: %+v", len(got), got)
	}
	if got[0].number != "12.2" || got[0].title != "Dynamic Conformance" || got[0].lineIdx != 3 {
		t.Errorf("expected rescued heading 12.2 at line 3, got %+v", got[0])
	}
}

// TestRescueDanglingParentsChainsThroughMultipleLevels exercises RFC
// 1142's harder shape: both "8.4" and "8.4.1" are missing (neither
// preceded by a blank line -- "8.4.1" directly follows "8.4" itself, two
// headings back to back), and only the deeper "8.4.1.1" was originally
// detected. Rescuing "8.4.1" from that anchor reveals "8.4" is needed
// too, one level further up, which is why the rescue pass loops instead
// of doing a single pass.
func TestRescueDanglingParentsChainsThroughMultipleLevels(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"8.4 Broadcast Subnetworks",
		"8.4.1 Broadcast Subnetwork IIH PDUs",
		"some paragraph text with no blank line before the next heading.",
		"8.4.1.1 IIH PDU Acceptance Tests",
		"body text",
	}
	headings := []rawHeading{
		{lineIdx: 5, number: "8.4.1.1", title: "IIH PDU Acceptance Tests", level: 3, parent: "8.4.1"},
	}
	got := rescueDanglingParents(lines, headings, 0, 0)
	byNumber := make(map[string]rawHeading, len(got))
	for _, h := range got {
		byNumber[h.number] = h
	}
	for _, number := range []string{"8.4", "8.4.1", "8.4.1.1"} {
		if _, ok := byNumber[number]; !ok {
			t.Errorf("expected heading %q to be present after rescue, got %+v", number, got)
		}
	}
	if byNumber["8.4.1"].parent != "8.4" {
		t.Errorf(`"8.4.1": got parent=%q, want "8.4"`, byNumber["8.4.1"].parent)
	}
}

// TestRescueDanglingParentsNoOpWhenNothingDangling is the regression
// guard: a document where every parent link already points at a real
// heading must come out unchanged.
func TestRescueDanglingParentsNoOpWhenNothingDangling(t *testing.T) {
	lines := []string{"Header", "", "3 Functional Specification", "", "3.1 Header Format", "body"}
	headings := []rawHeading{
		{lineIdx: 2, number: "3", title: "Functional Specification", level: 1, parent: ""},
		{lineIdx: 4, number: "3.1", title: "Header Format", level: 2, parent: "3"},
	}
	got := rescueDanglingParents(lines, headings, 0, 0)
	if len(got) != len(headings) {
		t.Fatalf("expected no change, got %d headings, want %d", len(got), len(headings))
	}
}

// TestRescueDanglingParentsLeavesUnrescuableDangling exercises RFC
// 1142's Appendix A/C shape: no heading line for the dangling parent
// exists anywhere, in any format, so the rescue pass must leave it
// alone (reparentDanglingAncestors' existing nearest-ancestor fallback
// handles this case instead).
func TestRescueDanglingParentsLeavesUnrescuableDangling(t *testing.T) {
	lines := []string{"Header", "", "A.1 Introduction", "body"}
	headings := []rawHeading{
		{lineIdx: 2, number: "A.1", title: "Introduction", level: 2, parent: "A"},
	}
	got := rescueDanglingParents(lines, headings, 0, 0)
	if len(got) != 1 {
		t.Fatalf("expected no new heading rescued (no real \"A\" heading line exists), got %d: %+v", len(got), got)
	}
}

// TestRescueDanglingParentsRespectsTOCExclusion guards against rescuing
// a heading from inside the document's own Table of Contents block: a
// TOC line like "7 Protocol Classes 4" (single space before the trailing
// page number "4") doesn't carry enough of a gap to be rejected by
// isTOCTrailer on its own, so only the tocStart/tocEnd range exclusion
// protects against misreading it as the real "7" heading.
func TestRescueDanglingParentsRespectsTOCExclusion(t *testing.T) {
	lines := []string{
		"Header",
		"7 Protocol Classes 4",
		"",
		"7.1 Something",
		"body",
	}
	headings := []rawHeading{
		{lineIdx: 3, number: "7.1", title: "Something", level: 2, parent: "7"},
	}
	got := rescueDanglingParents(lines, headings, 1, 2)
	for _, h := range got {
		if h.number == "7" {
			t.Fatalf(`"7" must not be rescued from inside the excluded TOC range, got %+v`, got)
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected no rescue to happen, got %d headings: %+v", len(got), got)
	}
}

func TestParseRFCTextEmptyTitleUsesRFCNumber(t *testing.T) {
	raw := []byte("No headings at all, just one line.\n")
	sections, err := ParseRFCText(raw, 42, "")
	if err != nil {
		t.Fatalf("ParseRFCText: %v", err)
	}
	if sections[0].Title != "RFC 42" {
		t.Errorf("Title = %q, want %q", sections[0].Title, "RFC 42")
	}
}

// TestParseRFCTextSupplementsTier1FromTOC covers the merged-tier gate:
// Tier 1 is healthy (front matter plus numbered sections), but the TOC
// lists a section whose body heading carries no number, which only the
// TOC title search can locate. The output must keep the Tier-1-only
// headings (front matter is never listed in a TOC) and graft in the
// TOC-only one — neither tier wholesale.
func TestParseRFCTextSupplementsTier1FromTOC(t *testing.T) {
	raw := []byte(`Abstract

This memo tests the supplement path.

Table of Contents

   1.  First
   2.  Second
   Wire Format Details
   3.  Third

1.  First

   first body

2.  Second

   second body

Wire Format Details

   located only via the TOC

3.  Third

   third body
`)
	sections, err := ParseRFCText(raw, 9999, "Test")
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, s := range sections {
		got[s.Number] = s.Title
	}
	for _, want := range []string{"abstract", "1", "2", "3", "wire-format-details"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing section %q in %v", want, sections)
		}
	}
	if title := got["wire-format-details"]; title != "Wire Format Details" {
		t.Errorf("supplement title = %q, want \"Wire Format Details\"", title)
	}
}

func TestSupplementFromTier2SkipsCovered(t *testing.T) {
	tier1 := []rawHeading{
		{lineIdx: 10, number: "1", title: "First"},
		{lineIdx: 20, number: "references", title: "References"},
	}
	tier2 := []rawHeading{
		{lineIdx: 10, number: "1", title: "First"},             // same number and line
		{lineIdx: 20, number: "7", title: "References"},        // TOC spells it 7, Tier 1 owns the line
		{lineIdx: 30, number: "2", title: "Second"},            // genuinely new
		{lineIdx: 40, number: "parameter", title: "parameter"}, // wrapped-fragment slug
	}
	sup := supplementFromTier2(tier2, tier1)
	if len(sup) != 1 || sup[0].number != "2" {
		t.Errorf("supplement = %+v, want just the genuinely new heading 2", sup)
	}
}

func TestPlausibleSupplementEntry(t *testing.T) {
	tests := []struct {
		e    tocEntry
		want bool
	}{
		{tocEntry{number: "3.2", title: "anything"}, true},
		{tocEntry{title: "Appendices"}, true},
		{tocEntry{title: "1.1.1.1: Credential Constructs"}, true},
		{tocEntry{title: "parameter"}, false},
		{tocEntry{title: ""}, false},
	}
	for _, tt := range tests {
		if got := plausibleSupplementEntry(tt.e); got != tt.want {
			t.Errorf("plausibleSupplementEntry(%+v) = %v, want %v", tt.e, got, tt.want)
		}
	}
}
