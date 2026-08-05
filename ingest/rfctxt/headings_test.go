package rfctxt

import "testing"

func TestHeadingRE(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{"decimal top level", "1.  Purpose and Scope", true},
		{"decimal two levels", "2.1.  Requirements Language", true},
		{"decimal deep", "3.8.6.2.1.  Sender's Algorithm -- When to Send Data", true},
		{"appendix letter", "Appendix A.  Other Implementation Notes", true},
		{"bare letter sub-level", "A.1.  IP Security Compartment and Precedence", true},
		{"bare letter top level", "A.  Before Link Establishment", true},
		{"appendix sub-numbered", "Appendix F.1.  Multiple Networks Per Message", true},
		// RFC 892's typewriter-era column alignment pads the gap between
		// the number and the title out to 5 spaces, well past the old
		// \s{1,3} bound.
		{"wide typewriter-era gap", "5.3     Functions of the Transport Layer", true},
		// Bare letter, no period at all (RFC 2207's style): headingRE
		// matches the shape at this level; the extra "is the next word
		// really a title word" disambiguation lives in validHeadingMatch
		// / isHeadingLine (see TestIsHeadingLineBareLetterDisambiguation),
		// since Go's RE2 engine has no lookahead to fold it into the
		// regexp itself.
		{"bare letter with no period at all", "A   Options Considered", true},

		{"indented enumeration in body text", "   1. First, do X, then do Y in this indented body paragraph.", false},
		{"all-caps appendix with colon does not match (relies on Tier 2)", "APPENDIX A:  Examples & Scenarios", false},
		{"plain prose line", "This is just a regular sentence.", false},
		{"multi-letter roman numeral is not a letter token", "II. Some Requirements Upon the Host-to-Host Software", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headingRE.MatchString(tt.line); got != tt.match {
				t.Errorf("headingRE.MatchString(%q) = %v, want %v", tt.line, got, tt.match)
			}
		})
	}
}

// TestIsHeadingLineBareLetterDisambiguation exercises the extra check
// validHeadingMatch applies to headingRE's bare, dot-less letter
// alternative, which headingRE's shape match alone can't express. Real
// headings like RFC 2207's "A   Options Considered" must pass, while
// ordinary sentences starting with the English word "A" at column 0
// (RFC 1035, RFC 1142, RFC 1277) and RFC 1035's own zone-file example
// row "A       A       26.3.0.103" -- where the "next word" is just
// another lone letter, the DNS record type "A" -- must stay rejected.
func TestIsHeadingLineBareLetterDisambiguation(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		match bool
	}{
		{"RFC 2207 bare appendix letter, real heading", "A   Options Considered", true},
		{"RFC 2524 bare appendix letter, ALL CAPS", "C  RATIONALE FOR KEY DESIGN DECISIONS", true},
		{"RFC 2769 bare appendix letter, short title", "A  Examples", true},
		{"RFC 1035 body prose starting with the word \"A\"", "A host can participate in the domain name system in a number of ways,", false},
		{"RFC 1142 body prose starting with the word \"A\"", "A separate adjacency is created for each neighbour", false},
		{"RFC 1142 body prose starting with the word \"C\"", "C is a non-broadcast circuit, Clear SRMflag for C", false},
		{"RFC 1277 body prose starting with the word \"A\"", "A decimal abstract encoding is defined for the DSP. The ECMA 117", false},
		{"RFC 1035 zone-file example row, next \"word\" is a lone letter", "A       A       26.3.0.103", false},
		// RFC 1035's header-field table in section 4.1.1 column-aligns a
		// description at a wide gap after the field letter; a real bare-
		// letter heading never separates its title that far (bare letters
		// have no dot to justify a wide gap, cf. validWideGapDecimalMatch).
		{"RFC 1035 header-field table row with a wide gap", "Z               Reserved for future use.  Must be zero in all queries", false},
		// A zone-file row with a class column: the gap is heading-narrow
		// but the rest is itself column-aligned (internal 2+-space runs),
		// which real bare-letter heading titles never are.
		{"zone-file row with class column", "A   IN   A   26.3.0.103", false},
		// The internal-double-space rejection applies to the title
		// portion only: a single 5+-space run separating the title from
		// a same-line body continuation is splitHeadingGap's shape and
		// must not disqualify the heading.
		{"bare letter heading with wide-gap body continuation", "A   Options Considered          Inverse queries take the form", true},
		{"dotted letter heading with wide-gap body continuation", "A. Some Title          continuation text", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isHeadingLine(tt.line); got != tt.match {
				t.Errorf("isHeadingLine(%q) = %v, want %v", tt.line, got, tt.match)
			}
		})
	}
}

func TestSplitHeadingGap(t *testing.T) {
	tests := []struct {
		name      string
		title     string
		wantShort string
		wantTail  string
		wantOK    bool
	}{
		{
			name:      "RFC 1035 6.4.1 gap",
			title:     "The contents of inverse queries and responses          Inverse",
			wantShort: "The contents of inverse queries and responses",
			wantTail:  "Inverse",
			wantOK:    true,
		},
		{
			name:   "small gap below threshold is not split",
			title:  "MUST   This word, or the terms \"REQUIRED\" or \"SHALL\", mean that the",
			wantOK: false,
		},
		{
			name:   "no gap at all",
			title:  "Purpose and Scope",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			short, tail, ok := splitHeadingGap(tt.title)
			if ok != tt.wantOK {
				t.Fatalf("splitHeadingGap(%q) ok = %v, want %v", tt.title, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if short != tt.wantShort || tail != tt.wantTail {
				t.Errorf("splitHeadingGap(%q) = (%q, %q), want (%q, %q)", tt.title, short, tail, tt.wantShort, tt.wantTail)
			}
		})
	}
}

func TestNormalizeNumberToken(t *testing.T) {
	tests := []struct {
		token      string
		wantNumber string
		wantLevel  int
		wantParent string
	}{
		{"1.", "1", 1, ""},
		{"2.1.", "2.1", 2, "2"},
		{"3.8.6.2.1.", "3.8.6.2.1", 5, "3.8.6.2"},
		{"A.", "A", 1, ""},
		{"A.1.", "A.1", 2, "A"},
		{"A.1.1.", "A.1.1", 3, "A.1"},
		{"Appendix A.", "A", 1, ""},
		{"Appendix F.1.", "F.1", 2, "F"},
	}
	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			number, level, parent := normalizeNumberToken(tt.token)
			if number != tt.wantNumber || level != tt.wantLevel || parent != tt.wantParent {
				t.Errorf("normalizeNumberToken(%q) = (%q, %d, %q), want (%q, %d, %q)",
					tt.token, number, level, parent, tt.wantNumber, tt.wantLevel, tt.wantParent)
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Abstract", "abstract"},
		{"Security Considerations", "security-considerations"},
		{"Author's Address", "author-s-address"},
		{"IANA Considerations", "iana-considerations"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMatchKnownUnnumbered(t *testing.T) {
	tests := []struct {
		line  string
		want  bool
		title string
	}{
		{"Abstract", true, "Abstract"},
		{"Abstract.", true, "Abstract."},
		{"ACKNOWLEDGEMENT", true, "ACKNOWLEDGEMENT"},
		{"Acknowledgements", true, "Acknowledgements"},
		{"Status of This Memo", true, "Status of This Memo"},
		{"  Abstract", false, ""},
		{"Abstractive", false, ""},
		{"Not a known heading", false, ""},
	}
	for _, tt := range tests {
		title, ok := matchKnownUnnumbered(tt.line)
		if ok != tt.want || title != tt.title {
			t.Errorf("matchKnownUnnumbered(%q) = (%q, %v), want (%q, %v)", tt.line, title, ok, tt.title, tt.want)
		}
	}
}

func TestDetectTier1RejectsTOCLookalikes(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"1.  Introduction ..................................................... 4",
		"",
		"APPENDIX B ......................................................... iii",
		"",
		"1.  Introduction",
		"real body content",
	}
	headings := detectTier1(lines, 0, 0)
	if len(headings) != 1 {
		t.Fatalf("expected only the real heading to be detected, got %d: %+v", len(headings), headings)
	}
	if headings[0].lineIdx != 6 {
		t.Errorf("expected the real (non-TOC) heading at line 6, got line %d", headings[0].lineIdx)
	}
}

func TestDetectTier1DedupDuplicateNumbers(t *testing.T) {
	lines := []string{
		"Header line",
		"",
		"A.  First Use of Letter A",
		"body",
		"",
		"A.  Second, Bogus Use of Letter A",
		"more body",
	}
	headings := detectTier1(lines, 0, 0)
	count := 0
	for _, h := range headings {
		if h.number == "A" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one heading numbered %q, got %d", "A", count)
	}
	if headings[0].title != "First Use of Letter A" {
		t.Errorf("expected the first occurrence to win, got title %q", headings[0].title)
	}
}

func TestPrecededByBlank(t *testing.T) {
	lines := []string{"1.  Heading", "", "2.  Another", "body", "3.  Not preceded by blank"}
	if !precededByBlank(lines, 0) {
		t.Errorf("first line should count as preceded by blank")
	}
	if !precededByBlank(lines, 2) {
		t.Errorf("line 2 follows a blank line")
	}
	if precededByBlank(lines, 4) {
		t.Errorf("line 4 follows non-blank body text")
	}
}

// TestPrecededByBlankTOCBoundary guards RFC 4543's shape: the Table of
// Contents' last entry runs directly into "1.  Introduction" with zero
// blank lines, and (as in the real document) that entry is indented
// under its own section heading, so it can never satisfy headingRE's
// column-0 anchor. A previous line ending in a validated TOC trailer
// must count the same as a blank predecessor, but an ordinary line that
// merely happens to end in some digits must not.
func TestPrecededByBlankTOCBoundary(t *testing.T) {
	lines := []string{
		"      11.2. Informative References ...................................12",
		"1.  Introduction",
	}
	if !precededByBlank(lines, 1) {
		t.Errorf("a TOC entry line as predecessor should count as preceded by blank")
	}

	lines2 := []string{"ordinary body prose, not a TOC entry", "1.  Introduction"}
	if precededByBlank(lines2, 1) {
		t.Errorf("an ordinary non-trailer predecessor should not count as preceded by blank")
	}
}

// TestPrecededByBlankRejectsPacketDiagramRuler guards against RFC
// 3016's shape: a packet-diagram bit-position ruler line like "0
// 1                   2                   3" ends in a lone digit after
// a wide, dot-free gap, superficially resembling isTOCTrailer's general
// trailer check. precededByBlank must not treat it as TOC-adjacent --
// only a real dot-leader counts -- or the diagram body line right after
// it would wrongly bypass the precededByBlank guard.
func TestPrecededByBlankRejectsPacketDiagramRuler(t *testing.T) {
	lines := []string{
		"0                   1                   2                   3",
		"0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1",
	}
	if precededByBlank(lines, 1) {
		t.Errorf("a packet-diagram bit-position ruler must not count as preceded by blank")
	}
}

// TestDetectTier1RejectsWideGapTableRow guards against the widened
// number-to-title gap misreading a numbered-list table row as a heading:
// RFC 699's "Requests for Comments Summary" lists bare RFC numbers
// column-aligned exactly like a real wide-gap heading, with no dot
// anywhere in the number.
func TestDetectTier1RejectsWideGapTableRow(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"600     Berggreen 26 Nov 73      Interfacing an Illinois Plasma Terminal",
		"",
		"body text",
	}
	headings := detectTier1(lines, 0, 0)
	if len(headings) != 0 {
		t.Fatalf("expected the table row to be rejected, got %d: %+v", len(headings), headings)
	}
}

// TestDetectTier1WideGapDottedNumberStillMatches is the regression guard
// alongside the above: a real wide-gap heading with a dot in its number
// (a trailing period on a single-level number, or a level separator on a
// multi-level one) must still be detected.
func TestDetectTier1WideGapDottedNumberStillMatches(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"1.     TCP Based Digit Generator Service",
		"",
		"body text",
	}
	headings := detectTier1(lines, 0, 0)
	if len(headings) != 1 || headings[0].number != "1" {
		t.Fatalf("expected the dotted wide-gap heading to be detected, got %d: %+v", len(headings), headings)
	}
}

// TestResolveHeadingTitleAllCapsGetsMoreRoom guards RFC 892's "7.3"/"7.4"
// class-description headings: both exceed maxHeadingLineLen (78 and 75
// characters) with no internal gap to split on, but neither is real
// body prose -- prose sentences always contain lowercase words, while
// these are entirely uppercase title text.
func TestResolveHeadingTitleAllCapsGetsMoreRoom(t *testing.T) {
	line := "7.3     PROTOCOL DESCRIPTION OF CLASS 3: ERROR RECOVERY AND MULTIPLEXING CLASS"
	rest := "PROTOCOL DESCRIPTION OF CLASS 3: ERROR RECOVERY AND MULTIPLEXING CLASS"
	title, _, ok := resolveHeadingTitle(line, rest)
	if !ok {
		t.Fatalf("resolveHeadingTitle(%q) rejected an all-caps title, want accepted", line)
	}
	if title != rest {
		t.Errorf("resolveHeadingTitle title = %q, want %q", title, rest)
	}
}

// TestResolveHeadingTitleRejectsLongLowercaseProse is the regression
// guard for maxHeadingLineLen's original purpose: an unsplit, page-width
// candidate that contains lowercase words (real prose, not a title) must
// still be rejected regardless of the all-caps relaxation above.
func TestResolveHeadingTitleRejectsLongLowercaseProse(t *testing.T) {
	line := "1. First, do X, then do Y, then do Z, and repeat this long list item all the way to the end of the line."
	rest := "First, do X, then do Y, then do Z, and repeat this long list item all the way to the end of the line."
	if _, _, ok := resolveHeadingTitle(line, rest); ok {
		t.Errorf("resolveHeadingTitle(%q) accepted long lowercase prose, want rejected", line)
	}
}

func TestDetectTier1WideGap(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"5.3     Functions of the Transport Layer",
		"body text",
	}
	headings := detectTier1(lines, 0, 0)
	if len(headings) != 1 {
		t.Fatalf("expected 1 heading detected through the widened gap, got %d: %+v", len(headings), headings)
	}
	if headings[0].number != "5.3" || headings[0].title != "Functions of the Transport Layer" {
		t.Errorf("got number=%q title=%q, want number=%q title=%q", headings[0].number, headings[0].title, "5.3", "Functions of the Transport Layer")
	}
}

// TestDetectTier1ExcludesTOCRangeEvenWithWideGap is the defense-in-depth
// guard for the widened gap: RFC 1305's TOC lists deeper entries like
// "3.2.3.   Peer Variables 12" with only a single space before the page
// number, which isTOCTrailer's own check doesn't catch. Without
// structurally excluding the TOC's line range from Tier 1's scan, the
// widened gap would let this line through as a bogus body heading.
func TestDetectTier1ExcludesTOCRangeEvenWithWideGap(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"3.2.     State Variables and Parameters 9", // inside the TOC block
		"",
		"3.2.     State Variables and Parameters", // the real body heading
		"body text",
	}
	headings := detectTier1(lines, 2, 3)
	if len(headings) != 1 {
		t.Fatalf("expected only the real body heading, got %d: %+v", len(headings), headings)
	}
	if headings[0].lineIdx != 4 {
		t.Errorf("expected the real heading at line 4, got line %d", headings[0].lineIdx)
	}
}

// TestDetectTier1TOCExclusionDoesNotHideKnownUnnumbered guards RFC
// 2093's shape: "Abstract" sits right after the in-document TOC listing,
// inside the same line range findTOCBlock reports as the TOC block
// (its boundary search only stops at a numbered/lettered heading shape,
// which "Abstract" isn't). The TOC-range exclusion must only suppress
// the numbered/lettered heading path, not matchKnownUnnumbered.
func TestDetectTier1TOCExclusionDoesNotHideKnownUnnumbered(t *testing.T) {
	lines := []string{
		"Header",
		"",
		"   7. Security Considerations........................................ 23",
		"   8. Author's Address............................................... 23",
		"",
		"Abstract",
		"",
		"body text",
	}
	headings := detectTier1(lines, 2, 6)
	if len(headings) != 1 {
		t.Fatalf("expected only the Abstract heading, got %d: %+v", len(headings), headings)
	}
	if headings[0].title != "Abstract" {
		t.Errorf("got title %q, want %q", headings[0].title, "Abstract")
	}
}

func TestIsTOCTrailer(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"dot-leader with decimal page number", "1.  Introduction ..................................................... 4", true},
		{"short gap decimal page number", "Overview of the Protocol      12", true},
		{"dot-leader with roman-numeral page number", "Preface ......................................................... iii", true},
		{"real roman numeral, short gap", "Appendix A ......... iv", true},
		// RFC 1237's TOC entry "A.1.1  Application for Administrative
		// Authority Identifiers  42" trails its page number with a plain
		// two-space gap and no dot at all -- this must still count as a
		// real trailer, unlike the single-dot RFC 5244 case just below,
		// or Tier 2 can no longer match this entry's title against the
		// body heading (which has no page number suffix).
		{"plain two-space gap with no dot at all", "Application for Administrative Authority Identifiers  42", true},
		// RFC 5244: "No. 5" is an abbreviation plus a single digit, not a
		// page number -- the gap is just one dot and one space, exactly
		// as long as the abbreviation's own period.
		{"RFC 5244 abbreviation, not a page number", "2.1.  Signalling System No. 5", false},
		// RFC 7069: "CDMI" is built entirely from roman-numeral letters
		// (C, D, M, I) but isn't a syntactically valid roman numeral.
		{"RFC 7069 CDMI is not a real roman numeral", "A.2.  CDMI", false},
		{"no trailer at all", "A.  Before Link Establishment", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTOCTrailer(tt.line); got != tt.want {
				t.Errorf("isTOCTrailer(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestStripTOCTrailer(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips a real dot-leader trailer", "1.  Introduction ..... 4", "1.  Introduction"},
		// Unlike isTOCTrailer, stripTOCTrailer only ever runs on a line
		// already confirmed to sit inside the located TOC block (see
		// parseTOCEntries), so a single-dot gap before the page number is
		// unambiguous here -- RFC 3196's TOC entry title happens to end in
		// its own closing period right before the page number, and must
		// still be stripped.
		{"strips a single-dot gap inside a confirmed TOC block", "3.1.2.1  Suggested Operation Processing Steps for all Operations. 17", "3.1.2.1  Suggested Operation Processing Steps for all Operations"},
		{"leaves RFC 7069's CDMI untouched", "A.2.  CDMI", "A.2.  CDMI"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripTOCTrailer(tt.in); got != tt.want {
				t.Errorf("stripTOCTrailer(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestDetectTier1RejectsPunctuationOnlyTitle guards against RFC 1305's
// appendix code listings: "7 */" (a numbered brace-comment close,
// preceded by a blank line) otherwise parses as section "7" titled "*/".
func TestDetectTier1RejectsPunctuationOnlyTitle(t *testing.T) {
	lines := []string{
		"some code above",
		"",
		"7 */",
		"",
		"more code",
	}
	if got := detectTier1(lines, 0, 0); len(got) != 0 {
		t.Errorf("detectTier1 = %+v, want no headings for a punctuation-only title", got)
	}
}
