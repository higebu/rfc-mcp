// Package rfctxt parses the plain-text RFC bodies served from
// https://www.rfc-editor.org/rfc/rfcN.txt into a flat list of sections.
package rfctxt

import (
	"bytes"
	"regexp"
	"strings"
)

// The second alternative covers RFC 791-era footers, which carry no
// author-name prefix before the page marker (some pages even flush it to
// column 0: "[Page ii]"), unlike the "Author, et al.  Track  [Page N]"
// style of later RFCs the first alternative targets.
var pageFooterRE = regexp.MustCompile(`(?i)^(\S.*\[Page\s+[ivxlcdm\d]+\]|\s*\[Page\s+[ivxlcdm\d]+\])\s*$`)

// runningHeaderDateRE matches a line that is nothing but a date — the
// single-segment running-header style of RFC 791 ("September 1981",
// right- or left-justified) and RFC 768 ("28 Aug 1980").
var runningHeaderDateRE = regexp.MustCompile(`(?i)^(?:\d{1,2}\s+)?(?:january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sept?|oct|nov|dec)\.?\s+\d{4}$`)

// isRunningHeader reports whether the first non-blank line after a page
// break looks like a running page header rather than body content. Every
// running header in the corpus is either column-justified — two or more
// segments separated by runs of 3+ spaces ("RFC 4271     BGP-4
// January 2006", RFC 821's "August 1982 ... RFC 821") — or a lone date
// (RFC 791/768). Body text resuming directly after a header-less page
// break (RFC 849's indented prose, RFC 1142's mid-sentence breaks) fits
// neither shape and must be kept: dropping it silently loses content.
func isRunningHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "   ") {
		return true
	}
	return runningHeaderDateRE.MatchString(trimmed)
}

// cleanLines strips a UTF-8 BOM (present on e.g. RFC 9293), normalizes
// CRLF/CR line endings to LF, and removes pagination artifacts, returning
// the result split into lines.
func cleanLines(raw []byte) []string {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return removePagination(strings.Split(text, "\n"))
}

// removePagination drops form-feed page breaks together with the footer
// line that precedes them (and any blank lines right before the footer),
// and the running header line that follows the form feed — but only when
// that line actually looks like a running header (see isRunningHeader)
// and isn't a real Tier-1 heading. Pages that start directly with body
// text — RFC 849 has a header-less page, RFC 1142 scatters form feeds
// mid-sentence with no headers at all — keep their first line intact.
func removePagination(lines []string) []string {
	out := make([]string, 0, len(lines))
	afterFF := false
	for _, line := range lines {
		if line == "\f" {
			for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
				out = out[:len(out)-1]
			}
			if len(out) > 0 && pageFooterRE.MatchString(out[len(out)-1]) {
				out = out[:len(out)-1]
			}
			afterFF = true
			continue
		}
		if afterFF {
			if strings.TrimSpace(line) == "" {
				out = append(out, line)
				continue
			}
			afterFF = false
			if isHeadingLine(line) || !isRunningHeader(line) {
				out = append(out, line)
			}
			continue
		}
		out = append(out, line)
	}
	return out
}
