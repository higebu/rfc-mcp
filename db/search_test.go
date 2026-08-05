package db

import "testing"

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare hyphenated term", "three-way", `"three-way"`},
		{"bare hyphenated term lowercase", "sec-agree", `"sec-agree"`},
		{"multiple hyphens", "one-two-three", `"one-two-three"`},
		{"no hyphen", "TCP", "TCP"},
		{"operators preserved", "TCP AND handshake", "TCP AND handshake"},
		{"OR operator", "TCP OR UDP", "TCP OR UDP"},
		{"NOT operator", "TCP NOT UDP", "TCP NOT UDP"},
		{"quoted phrase preserved", `"three way handshake"`, `"three way handshake"`},
		{"prefix wildcard", "handsh*", "handsh*"},
		{"valid column filter", "content:handshake", "content:handshake"},
		{"valid column filter title", "title:introduction", "title:introduction"},
		{"column filter with hyphen value", "title:three-way", `title:"three-way"`},
		{"NEAR preserved", "NEAR(TCP UDP, 5)", "NEAR(TCP UDP, 5)"},
		{"lowercase near preserved", "near(TCP UDP, 5)", "near(TCP UDP, 5)"},
		{"mixed-case Near preserved", "Near(TCP UDP, 5)", "Near(TCP UDP, 5)"},
		{"NEAR with hyphenated term", "NEAR(foo-bar baz)", "NEAR(foo-bar baz)"},
		{"column filter with quoted phrase", `title:"foo bar"`, `title:"foo bar"`},
		{"column filter with hyphen in quoted phrase", `title:"foo bar-baz"`, `title:"foo bar-baz"`},
		{"column filter with NEAR", "title:NEAR(foo-bar baz)", "title:NEAR(foo-bar baz)"},
		{"column filter with lowercase near", "content:near(a b, 3)", "content:near(a b, 3)"},
		{"column filter phrase then bare token", `title:"a b" three-way`, `title:"a b" "three-way"`},
		{"column filter with unterminated phrase", `title:"foo bar`, `title:"foo bar`},
		{"non-column prefix with quote splits as before", `bogus:"foo bar"`, `bogus:"foo bar"`},
		{"leading hyphen NOT shorthand", "-excluded", "-excluded"},
		{"leading hyphen with more hyphens", "-one-two", `"-one-two"`},
		{"mixed query", `three-way AND "core network"`, `"three-way" AND "core network"`},
		{"hyphen with operator", "sec-agree OR authentication", `"sec-agree" OR authentication`},
		{"empty query", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	d := setupTestDB(t)

	t.Run("basic search", func(t *testing.T) {
		results, err := d.Search("Transmission Control Protocol", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}
	})

	t.Run("search with single rfc filter", func(t *testing.T) {
		results, err := d.Search("Introduction", []int{793}, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].RFC != 793 {
			t.Errorf("expected rfc 793, got %d", results[0].RFC)
		}
	})

	t.Run("search with multiple rfc filter", func(t *testing.T) {
		results, err := d.Search("Introduction", []int{9293, 793}, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("no results", func(t *testing.T) {
		results, err := d.Search("xyznonexistent", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("hyphenated term does not error", func(t *testing.T) {
		results, err := d.Search("three-way", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error for hyphenated search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for three-way")
		}
	})

	t.Run("unrelated rfc filter excludes results", func(t *testing.T) {
		results, err := d.Search("Introduction", []int{4271}, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results for rfc 4271 (no Introduction section), got %d", len(results))
		}
	})

	t.Run("limit is applied", func(t *testing.T) {
		results, err := d.Search("Section OR Protocol OR Attribute", nil, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected exactly 1 result due to limit, got %d", len(results))
		}
	})
}
