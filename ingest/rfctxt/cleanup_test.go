package rfctxt

import (
	"strings"
	"testing"
)

func TestCleanLinesStripsBOM(t *testing.T) {
	raw := append([]byte("\xef\xbb\xbf"), []byte("Hello\nWorld\n")...)
	lines := cleanLines(raw)
	if strings.HasPrefix(lines[0], "\xef\xbb\xbf") {
		t.Fatalf("BOM not stripped: %q", lines[0])
	}
	if lines[0] != "Hello" {
		t.Errorf("got %q, want %q", lines[0], "Hello")
	}
}

func TestCleanLinesNormalizesLineEndings(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("Hello\r\nWorld\r\n"),
		[]byte("Hello\rWorld\r"),
	} {
		lines := cleanLines(raw)
		if len(lines) < 2 || lines[0] != "Hello" || lines[1] != "World" {
			t.Errorf("cleanLines(%q) = %#v, want [Hello World ...]", raw, lines)
		}
	}
}

func TestRemovePagination(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "footer and running header stripped",
			in: []string{
				"some content",
				"",
				"",
				"Rekhter, et al.             Standards Track                     [Page 1]",
				"\f",
				"RFC 4271                         BGP-4                      January 2006",
				"",
				"",
				"4.2.  OPEN Message Format",
			},
			want: []string{
				"some content",
				"",
				"",
				"",
				"",
				"4.2.  OPEN Message Format",
			},
		},
		{
			name: "heading right after form feed is kept",
			in: []string{
				"some content",
				"Author                                                        [Page 1]",
				"\f",
				"4.  Next Section",
				"body text",
			},
			want: []string{
				"some content",
				"4.  Next Section",
				"body text",
			},
		},
		{
			name: "footer with no author-name prefix (RFC 791 style)",
			in: []string{
				"some content",
				"",
				"                                                                [Page i]",
				"\f",
				"",
				"September 1981",
				"Internet Protocol",
				"",
				"more content",
			},
			want: []string{
				"some content",
				"",
				"",
				"Internet Protocol",
				"",
				"more content",
			},
		},
		{
			// RFC 849 page 2 carries no running header at all: the page
			// starts directly with indented body prose, which must not be
			// eaten as if it were a header line.
			name: "page starting with body prose keeps the prose (RFC 849)",
			in: []string{
				"formats.  I have three suggestions.",
				"",
				"Crispin                                                         [Page 1]",
				"\f",
				"     A more short-term solution is to make possible faster and more",
				"thorough updating of the various local copies of the name tables.",
			},
			want: []string{
				"formats.  I have three suggestions.",
				"",
				"     A more short-term solution is to make possible faster and more",
				"thorough updating of the various local copies of the name tables.",
			},
		},
		{
			// RFC 2244 page iv->v: the form feed is glued to the front of
			// the running header ("\fRFC 2244  ACAP  November 1997") rather
			// than sitting alone on its line. The break must still strip
			// the preceding footer, the FF itself, and the header.
			name: "form feed glued to the running header (RFC 2244)",
			in: []string{
				"some content",
				"",
				"Newman & Myers              Standards Track                    [Page iv]",
				"\fRFC 2244                          ACAP                     November 1997",
				"",
				"ACAP Protocol Specification",
			},
			want: []string{
				"some content",
				"",
				"",
				"ACAP Protocol Specification",
			},
		},
		{
			// A page-break line with trailing whitespace after the form
			// feed must still be recognized as a page break.
			name: "form feed with trailing whitespace",
			in: []string{
				"some content",
				"Author                                                        [Page 1]",
				"\f ",
				"RFC 9999                     Some Title                       June 2001",
				"body text",
			},
			want: []string{
				"some content",
				"body text",
			},
		},
		{
			// A packet-diagram bit ruler landing as the first non-blank
			// line after a form feed is digits-only: it must not be
			// mistaken for a column-justified running header and eaten.
			name: "digits-only bit ruler after form feed survives",
			in: []string{
				"The diagram below shows the header layout:",
				"",
				"Author                                                        [Page 3]",
				"\f",
				"    0                   1                   2                   3",
				"    0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1",
			},
			want: []string{
				"The diagram below shows the header layout:",
				"",
				"    0                   1                   2                   3",
				"    0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1",
			},
		},
		{
			// RFC 1142 scatters form feeds mid-sentence (even mid-word:
			// "manual\fArea\fAddresses"), with no footers and no running
			// headers anywhere; the continuation line after each break is
			// real content and must survive.
			name: "mid-text page break keeps the continuation line (RFC 1142)",
			in: []string{
				"ter manual",
				"\f",
				"Area",
				"\f",
				"Addresses. This parameter is set locally",
			},
			want: []string{
				"ter manual",
				"Area",
				"Addresses. This parameter is set locally",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removePagination(tt.in)
			if !equalLines(got, tt.want) {
				t.Errorf("removePagination() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestIsRunningHeader(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		// Column-justified running headers (2+ segments, 3+ space gaps).
		{"RFC 4271                         BGP-4                      January 2006", true},
		{"RFC 17& 17a         Re: HOST-IMP Protocol & Response         August 1969", true},
		{"August 1982                                                      RFC 821", true},
		{"Network Working Group                                          J. Postel", true},
		// Date-only headers (RFC 791 right- or left-justified, RFC 768).
		{"                                                          September 1981", true},
		{"September 1981                                                          ", true},
		{"28 Aug 1980", true},
		// A packet-diagram bit ruler is multi-segment but digits-only;
		// running headers always carry at least one letter.
		{"0                   1                   2                   3", false},
		{" 0                   1                   2                   3", false},
		// Body prose after a header-less page break must not be mistaken
		// for a running header (RFC 849, RFC 1142).
		{"     A more short-term solution is to make possible faster and more", false},
		{"mission facility, as does a broadcast sub", false},
		{"Area", false},
		{"Addresses parameter.", false},
	}
	for _, tt := range tests {
		if got := isRunningHeader(tt.line); got != tt.want {
			t.Errorf("isRunningHeader(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
