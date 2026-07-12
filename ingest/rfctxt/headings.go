package rfctxt

import (
	"regexp"
	"strings"
)

var (
	// The letter alternative requires a trailing period when there's no
	// digit suffix ("A." but not bare "A"): RFC 1035 section 2.2 opens a
	// paragraph with "A host can participate ..." right at column 0
	// after a blank line, which otherwise reads as a heading token "A".
	// Every real lettered heading across the fixtures (Appendix A/B/F.1,
	// RFC 1's "A.  Before Link Establishment") already carries the dot.
	headingRE    = regexp.MustCompile(`^(\d+(?:\.\d+)*\.?|[A-Z](?:\.\d+)+\.?|[A-Z]\.|Appendix\s+[A-Z](?:\.\d+)*\.?)\s{1,3}(\S.*)$`)
	tocTrailerRE = regexp.MustCompile(`[.\s]{2,}[ivxlcdmIVXLCDM\d]+\s*$`)
	gapRE        = regexp.MustCompile(` {5,}`)
)

// maxHeadingLineLen rejects heading candidates that are really just a
// long body sentence starting with something that looks like a numbered
// list item (e.g. "1. First, do X ... " running the width of the page).
const maxHeadingLineLen = 72

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
	return headingRE.MatchString(line)
}

// detectTier1 finds column-0 numbered/lettered headings plus well-known
// unnumbered headings. A candidate is rejected unless the previous line
// is blank (or it's the first line of the document): this is what tells
// a real heading apart from a same-column false positive inside
// flush-left body text, e.g. RFC 1035 section 3.3.11's WKS example
// contains a line "25 (SMTP).  If this bit is set, ..." at column 0 that
// otherwise matches the numbered-heading shape. Headings that reuse an
// already-seen number are dropped: real RFC section numbering never
// repeats, so a repeat is always a false positive.
func detectTier1(lines []string) []rawHeading {
	var out []rawHeading
	seen := make(map[string]bool)
	for i, line := range lines {
		if line == "" {
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			if tocTrailerRE.MatchString(line) {
				continue
			}
			title, tail, split := splitHeadingGap(m[2])
			if !split {
				if len(line) > maxHeadingLineLen {
					continue
				}
				title = m[2]
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

func precededByBlank(lines []string, i int) bool {
	return i == 0 || strings.TrimSpace(lines[i-1]) == ""
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
