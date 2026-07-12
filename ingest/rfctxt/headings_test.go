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

		{"indented enumeration in body text", "   1. First, do X, then do Y in this indented body paragraph.", false},
		{"bare letter with no period is body prose", "A host can participate in the domain name system in a number of ways,", false},
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
	headings := detectTier1(lines)
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
	headings := detectTier1(lines)
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
