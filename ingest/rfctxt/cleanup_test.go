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
