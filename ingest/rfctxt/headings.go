package rfctxt

import (
	"regexp"
	"strings"
)

var (
	// The letter alternative normally requires a trailing period when
	// there's no digit suffix ("A." but not bare "A"): RFC 1035 section
	// 2.2 opens a paragraph with "A host can participate ..." right at
	// column 0 after a blank line, which otherwise reads as a heading
	// token "A". Most lettered headings across the fixtures (Appendix
	// A/B/F.1, RFC 1's "A.  Before Link Establishment") carry the dot,
	// but some older RFCs write a bare appendix letter with no dot at
	// all (RFC 2207's "A   Options Considered", RFC 2524's "C
	// RATIONALE FOR KEY DESIGN DECISIONS"). The bare `[A-Z]` alternative
	// below matches that shape too; validHeadingMatch then requires the
	// following word to start with an uppercase letter, since Go's RE2
	// engine has no lookahead to express that as part of the regexp
	// itself -- see validHeadingMatch for how the "A host can
	// participate ..." / "A separate adjacency ..." (RFC 1142) / "A
	// decimal abstract encoding ..." (RFC 1277) false positives stay
	// rejected.
	//
	// The gap between the token and the title is `\s+` (any width, not
	// just 1-3 spaces): typewriter-era column alignment sometimes pads
	// it out much wider, e.g. RFC 892's "7.0     PROTOCOL DESCRIPTION OF
	// CLASS 0...". Widening this can't create new ambiguity with
	// splitHeadingGap's own 5+-space tail split below: `\s+` is greedy,
	// so it always consumes the entire run of spaces immediately after
	// the token before the required `\S` kicks in, leaving any later gap
	// inside the title/tail untouched.
	headingRE = regexp.MustCompile(`^(\d+(?:\.\d+)*\.?|[A-Z](?:\.\d+)+\.?|[A-Z]\.|[A-Z]|Appendix\s+[A-Z](?:\.\d+)*\.?)\s+(\S.*)$`)
	gapRE     = regexp.MustCompile(` {5,}`)

	// tocTrailerGapRE matches a line ending in a run of dots/spaces
	// followed by a roman-numeral or decimal page number -- the shape of
	// a Table of Contents dot-leader entry ("....... 4" or "..... iv").
	// isTOCTrailer/stripTOCTrailer layer extra validation on top that the
	// character class alone can't express: see validRomanNumeralRE and
	// validTOCTrailerTail.
	tocTrailerGapRE = regexp.MustCompile(`([.\s]{2,})([ivxlcdmIVXLCDM]+|\d+)\s*$`)

	// tocLooseTrailerGapRE is stripTOCTrailer's variant of the above,
	// allowing a single-character gap: RFC 1305's TOC writes some page
	// numbers with just one space ("3.2.3.   Peer Variables 12"), which
	// {2,} can never strip. Only the decimal tail may use the short gap;
	// see validTOCTrailerTail.
	tocLooseTrailerGapRE = regexp.MustCompile(`([.\s]+)([ivxlcdmIVXLCDM]+|\d+)\s*$`)

	// validRomanNumeralRE recognizes a syntactically well-formed
	// (case-insensitive) Roman numeral, unlike tocTrailerGapRE's raw
	// character class, which only checks that every letter individually
	// belongs to {I,V,X,L,C,D,M} -- RFC 7069's "A.2.  CDMI" is built
	// entirely from such letters but isn't a valid numeral.
	validRomanNumeralRE = regexp.MustCompile(`(?i)^M{0,4}(CM|CD|D?C{0,3})(XC|XL|L?X{0,3})(IX|IV|V?I{0,3})$`)
)

// maxHeadingLineLen rejects heading candidates that are really just a
// long body sentence starting with something that looks like a numbered
// list item (e.g. "1. First, do X ... " running the width of the page).
// A candidate with no lowercase letters at all gets the wider
// maxAllCapsHeadingLineLen instead: real body prose always contains
// lowercase words, so an all-caps candidate this long is still a real
// heading title, not disguised prose -- RFC 892's "7.3     PROTOCOL
// DESCRIPTION OF CLASS 3: ERROR RECOVERY AND MULTIPLEXING CLASS" runs to
// 78 characters with no internal gap to split on.
const (
	maxHeadingLineLen        = 72
	maxAllCapsHeadingLineLen = 90
)

// hasLower reports whether s contains an ASCII lowercase letter.
func hasLower(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'a' && s[i] <= 'z' {
			return true
		}
	}
	return false
}

// resolveHeadingTitle applies splitHeadingGap to a heading candidate's
// captured text (rest), and enforces the line-length limits above on the
// un-split case. It reports ok=false when the candidate should be
// rejected as a body sentence rather than treated as a heading. A title
// with no letter or digit at all is never a heading: RFC 1305's appendix
// code listings contain the line "7 */" (a numbered brace-comment
// close), which otherwise reads as section "7" titled "*/".
func resolveHeadingTitle(line, rest string) (title, tail string, ok bool) {
	title, tail, split := splitHeadingGap(rest)
	if !split {
		limit := maxHeadingLineLen
		if !hasLower(rest) {
			limit = maxAllCapsHeadingLineLen
		}
		if len(line) > limit {
			return "", "", false
		}
		title, tail = rest, ""
	}
	if !hasAlnum(title) {
		return "", "", false
	}
	return title, tail, true
}

// hasAlnum reports whether s contains an ASCII letter or digit.
func hasAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	return false
}

// knownUnnumbered lists well-known RFC section titles that appear without
// a leading number. Matching is case-insensitive and tolerant of a
// trailing period (see matchKnownUnnumbered).
var knownUnnumbered = []string{
	"Abstract",
	"Status of This Memo",
	"Copyright Notice",
	"Table of Contents",
	"Security Considerations",
	"IANA Considerations",
	"Acknowledgments",
	"Acknowledgements",
	"Acknowledgment",
	"Acknowledgement",
	"Author's Address",
	"Author's Addresses",
	"Authors' Addresses",
	"Full Copyright Statement",
	"Intellectual Property",
	"References",
	"Normative References",
	"Informative References",
	"Contributors",
	"Index",
}

// rawHeading is an intermediate heading record shared by Tier 1 and
// Tier 2 detection; parser.go turns these into Sections.
type rawHeading struct {
	lineIdx int
	number  string
	title   string
	level   int
	parent  string
	tail    string // text split off the heading line; becomes the first content line
}

func isHeadingLine(line string) bool {
	m := headingRE.FindStringSubmatch(line)
	return m != nil && validHeadingMatch(m)
}

// validHeadingMatch applies the disambiguation the widened headingRE
// needs but can't express as part of the regexp itself (Go's RE2 engine
// has no lookahead).
func validHeadingMatch(m []string) bool {
	return validBareLetterMatch(m) && validWideGapDecimalMatch(m)
}

// validBareLetterMatch guards headingRE's bare, dot-less letter
// alternative: the letter must be followed by a real word -- at least
// two characters, starting with an uppercase letter. Real headings in
// this corpus are Title Case or ALL CAPS (RFC 2207's "A   Options
// Considered"), while the false positives this alternative would
// otherwise let through are ordinary sentences that happen to start with
// the English word "A" at column 0 (RFC 1035's "A host can
// participate...", RFC 1142's "A separate adjacency...", RFC 1277's "A
// decimal abstract encoding...") or, more subtly, RFC 1035's own
// zone-file example "A       A       26.3.0.103" -- the DNS record type
// column is itself the single letter "A", so without the two-character
// requirement this reads as heading "A" followed by an
// uppercase-starting "word" that's really just another lone letter.
// Every other alternative in headingRE already disambiguates itself with
// a dot, digits, or the literal word "Appendix", so this check only
// applies to a single bare capital letter.
//
// Two further table-row shapes slip past the two-character word
// requirement and need rejecting on top of it. RFC 1035's header-field
// table ("Z               Reserved for future use.  ...") column-aligns
// an uppercase-starting description far from the field letter: a real
// bare-letter heading never uses a gap that wide (it has no dot to
// justify one — the same principle as validWideGapDecimalMatch), so the
// token-to-title gap is capped at 3. And a DNS zone-file row with a
// class column ("A   IN   A   26.3.0.103") keeps a heading-narrow gap
// but is itself column-formatted: its rest carries internal runs of 2+
// spaces, which real bare-letter heading titles ("Options Considered",
// "RATIONALE FOR KEY DESIGN DECISIONS") never do. That last check only
// applies to the title portion: a 5+-space run may legitimately separate
// the title from a same-line body continuation (the typewriter-era shape
// splitHeadingGap recovers, cf. RFC 1035's section 6.4.1), so the rest
// is split the same way resolveHeadingTitle will split it before the
// internal-double-space rejection is applied.
func validBareLetterMatch(m []string) bool {
	token := m[1]
	if len(token) != 1 || token[0] < 'A' || token[0] > 'Z' {
		return true
	}
	rest := m[2]
	if len(rest) < 2 || rest[0] < 'A' || rest[0] > 'Z' || rest[1] == ' ' || rest[1] == '\t' {
		return false
	}
	if gap := len(m[0]) - len(token) - len(rest); gap > 3 {
		return false
	}
	title := rest
	if short, _, split := splitHeadingGap(rest); split {
		title = short
	}
	return !strings.Contains(strings.TrimRight(title, " \t"), "  ")
}

// validWideGapDecimalMatch guards the widened number-to-title gap
// against a numbered-list table row: RFC 699's "Requests for Comments
// Summary" tables list bare RFC numbers column-aligned exactly like a
// real widened-gap heading, e.g. "600     Berggreen 26 Nov 73
// Interfacing an Illinois Plasma Terminal", and RFC 703's host-survey
// table does the same with site numbers ("102     66      SRI-AI ...").
// Neither carries a single dot anywhere in the number, and both count in
// an order a real section numbering scheme never would (RFC 699 runs
// backwards from 699 to 600). Every real heading recovered by widening
// the gap does carry a dot somewhere in its number -- a trailing period
// on a single-level number ("1.     TCP Based ...", RFC 3091) or a level
// separator on a multi-level one ("5.3     Functions ...", RFC 892) --
// so requiring one, but only once the gap has grown past the original
// \s{1,3} bound, rejects the table rows without disturbing any narrow-gap
// heading (dotted or not) that already matched before the gap was
// widened.
func validWideGapDecimalMatch(m []string) bool {
	token := m[1]
	if token[0] < '0' || token[0] > '9' {
		return true
	}
	gapLen := len(m[0]) - len(token) - len(m[2])
	if gapLen <= 3 {
		return true
	}
	return strings.Contains(token, ".")
}

// isTOCTrailer reports whether line ends in a validated Table of
// Contents dot-leader/page-number trailer. It's used where the line
// itself is still in question -- a body heading candidate that might
// really be a stray TOC line (detectTier1, rescueDanglingParents), or
// the line findTOCBlock is deciding whether ends the TOC block -- so it
// applies validTOCTrailerTail's strict variant: see there for why a
// decimal tail needs more than a single dot's gap.
func isTOCTrailer(line string) bool {
	m := tocTrailerGapRE.FindStringSubmatch(line)
	return m != nil && validTOCTrailerTail(m[1], m[2], true)
}

// stripTOCTrailer removes a validated trailer (see validTOCTrailerTail)
// from the end of s, leaving s unchanged if none is found. Unlike
// isTOCTrailer, this is only ever called on a line already known (or
// being tested for whether) to sit inside the located TOC block, so
// there's no "might this really be body prose" ambiguity to guard
// against, and it uses tocLooseTrailerGapRE plus validTOCTrailerTail's
// loose variant.
func stripTOCTrailer(s string) string {
	loc := tocLooseTrailerGapRE.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	gap, tail := s[loc[2]:loc[3]], s[loc[4]:loc[5]]
	if !validTOCTrailerTail(gap, tail, false) {
		return s
	}
	return s[:loc[0]]
}

// validTOCTrailerTail applies asymmetric validation to tocTrailerGapRE's
// two tail shapes. A roman-numeral tail must always be a syntactically
// real numeral (rejects RFC 7069's "A.2.  CDMI"), in both variants: a
// coincidental run of roman-numeral letters ending a line is exceedingly
// unlikely to be a real page number in either context.
//
// A decimal tail's rule depends on strict: the loose variant (any gap of
// 1+ dot/space characters, down to RFC 1305's single-space "Peer
// Variables 12") is correct inside a confirmed TOC block, e.g. RFC
// 3196's TOC entry "3.1.2.1 Suggested Operation Processing Steps for
// all Operations. 17", whose gap is just the title's own closing period
// plus one space before the page number "17". The strict variant
// additionally rejects a single dot plus a single following character
// -- exactly as long as an abbreviation's period plus one space --
// because that shape is ambiguous outside a confirmed TOC block: RFC
// 5244's real body heading "2.1.  Signalling System No. 5" ends the
// same way, and needs to keep reading as a heading, not a stray TOC
// line. A gap with no dot at all (plain spaces, as in RFC 1237's TOC
// entry "Application for Administrative Authority Identifiers  42") or
// a real dot-leader (2+ dots) is unambiguous either way.
//
// A roman-numeral tail keeps requiring a 2+-character gap even in the
// loose variant: a title's own last word can be a syntactically valid
// numeral ("Part II", "Appendix I"), and unlike decimal page numbers,
// roman folio numbers never appear a single space after the title in
// this corpus.
func validTOCTrailerTail(gap, tail string, strict bool) bool {
	if tail[0] >= '0' && tail[0] <= '9' {
		if !strict {
			return true
		}
		dots := strings.Count(gap, ".")
		return dots != 1 || len(gap) >= 3
	}
	return len(gap) >= 2 && validRomanNumeralRE.MatchString(tail)
}

// detectTier1 finds column-0 numbered/lettered headings plus well-known
// unnumbered headings. tocStart/tocEnd exclude the located in-document
// Table of Contents block (see findTOCBlock) from consideration -- pass
// 0, 0 when none was found, which excludes nothing. This exclusion is
// defense in depth for the widened number-to-title gap above: RFC 1305's
// TOC lists deeper entries like "3.2.3.   Peer Variables 12" with only a
// single space before the page number, which fails isTOCTrailer's own
// check, so without the exclusion the wider gap would let a TOC line
// through as a bogus body heading. It only applies to the numbered/
// lettered path below, not the well-known-unnumbered-title path: a title
// like "Abstract" can fall inside the same line range (front matter such
// as an Abstract routinely sits right after the in-document TOC listing,
// e.g. RFC 2093, and findTOCBlock's own boundary search doesn't stop at
// it since it isn't a numbered/lettered heading shape), and matching it
// there is never ambiguous with a real TOC entry the way a numbered
// heading can be, since matchKnownUnnumbered requires the whole line to
// equal the known title with no dot-leader/page-number trailer attached.
//
// A candidate is rejected unless the previous line is blank (or it's the
// first line of the document): this is what tells a real heading apart
// from a same-column false positive inside flush-left body text, e.g.
// RFC 1035 section 3.3.11's WKS example contains a line "25 (SMTP).  If
// this bit is set, ..." at column 0 that otherwise matches the
// numbered-heading shape. Headings that reuse an already-seen number are
// dropped: real RFC section numbering never repeats, so a repeat is
// always a false positive.
func detectTier1(lines []string, tocStart, tocEnd int) []rawHeading {
	var out []rawHeading
	seen := make(map[string]bool)
	for i, line := range lines {
		if line == "" {
			continue
		}
		inTOCRange := tocStart <= i && i < tocEnd
		if !inTOCRange {
			if m := headingRE.FindStringSubmatch(line); m != nil {
				if !validHeadingMatch(m) {
					continue
				}
				if isTOCTrailer(line) {
					continue
				}
				title, tail, ok := resolveHeadingTitle(line, m[2])
				if !ok {
					continue
				}
				if !precededByBlank(lines, i) {
					continue
				}
				number, level, parent := normalizeNumberToken(m[1])
				if seen[number] {
					continue
				}
				seen[number] = true
				out = append(out, rawHeading{lineIdx: i, number: number, title: title, level: level, parent: parent, tail: tail})
				continue
			}
		}
		if title, ok := matchKnownUnnumbered(line); ok {
			if !precededByBlank(lines, i) {
				continue
			}
			number := slugify(title)
			if seen[number] {
				continue
			}
			seen[number] = true
			out = append(out, rawHeading{lineIdx: i, number: number, title: title, level: 1})
		}
	}
	return out
}

// precededByBlank reports whether the line at index i follows a
// heading-worthy break in the text: the document start, an actual blank
// line, or -- RFC 4543's shape, where the Table of Contents' last entry
// runs directly into "1.  Introduction" with zero blank lines -- a
// previous line that itself ends in a real dot-leader (2+ literal dots)
// before its page number, e.g. "      11.2. Informative References
// ...................................12". That's deliberately narrower
// than isTOCTrailer's general trailer check (which also accepts a bare
// multi-space gap with no dot at all, needed for TOC entries like RFC
// 1237's "...Identifiers  42"): a packet-diagram bit-position ruler like
// RFC 3016's "0                   1                   2                   3"
// also ends in a lone digit after a wide, dot-free gap, and would
// otherwise be misread as TOC-adjacent, wrongly waiving precededByBlank
// for the diagram line right after it. Requiring an actual dot-leader
// keeps this narrowly targeted at real Table of Contents entries.
func precededByBlank(lines []string, i int) bool {
	if i == 0 || strings.TrimSpace(lines[i-1]) == "" {
		return true
	}
	m := tocTrailerGapRE.FindStringSubmatch(lines[i-1])
	return m != nil && strings.Count(m[1], ".") >= 2
}

// splitHeadingGap handles a heading and the start of its body sharing one
// physical line, separated by a run of >=5 spaces — a typewriter-era
// column-alignment artifact verified in RFC 1035 section 6.4.1:
// "6.4.1. The contents of inverse queries and responses          Inverse".
// The returned tail becomes the first line of the section's content.
func splitHeadingGap(title string) (short, tail string, ok bool) {
	loc := gapRE.FindStringIndex(title)
	if loc == nil {
		return "", "", false
	}
	short = strings.TrimRight(title[:loc[0]], " ")
	tail = strings.TrimSpace(title[loc[1]:])
	return short, tail, true
}

// normalizeNumberToken turns a matched heading token ("3.10.", "A.2.",
// "Appendix F.1.") into a canonical section number plus its level and
// parent number. The "Appendix " prefix is stripped so that "Appendix
// F.1" and a bare body-level "F.1" normalize identically.
func normalizeNumberToken(token string) (number string, level int, parent string) {
	token = strings.TrimPrefix(token, "Appendix")
	token = strings.TrimSpace(token)
	token = strings.TrimSuffix(token, ".")
	segments := strings.Split(token, ".")
	level = len(segments)
	if level > 1 {
		parent = strings.Join(segments[:level-1], ".")
	}
	return token, level, parent
}

func matchKnownUnnumbered(line string) (string, bool) {
	trimmed := strings.TrimRight(line, " \t")
	if trimmed == "" || trimmed[0] == ' ' || trimmed[0] == '\t' {
		return "", false
	}
	candidate := strings.TrimSuffix(trimmed, ".")
	for _, known := range knownUnnumbered {
		if strings.EqualFold(candidate, known) {
			return trimmed, true
		}
	}
	return "", false
}

// slugify turns a title into a stable section-number slug: lowercase,
// non-alphanumeric runs collapsed to a single "-".
func slugify(s string) string {
	var b strings.Builder
	dash := true // suppresses a leading "-"
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
