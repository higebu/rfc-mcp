package db

import (
	"strings"
	"testing"
)

func TestExtractContext(t *testing.T) {
	content := "This section is based on RFC 9293 Section 3.1 and the original specification in RFC 793."

	match := "RFC 9293"
	start := strings.Index(content, match)
	end := start + len(match)

	got := extractContext(content, start, end)
	if got == "" {
		t.Fatal("expected non-empty context")
	}
	if !strings.Contains(got, match) {
		t.Errorf("expected context to contain the match, got %q", got)
	}

	// A match at the very start of the content should not be prefixed with "...".
	got = extractContext(content, 0, 4)
	if strings.HasPrefix(got, "...") {
		t.Errorf("expected no leading ellipsis for a match at content start, got %q", got)
	}

	// A match at the very end of the content should not be suffixed with "...".
	got = extractContext(content, len(content)-1, len(content))
	if strings.HasSuffix(got, "...") {
		t.Errorf("expected no trailing ellipsis for a match at content end, got %q", got)
	}

	// A match with enough surrounding text on both sides (beyond the 50-byte
	// window) gets ellipses on both sides.
	padded := strings.Repeat("x ", 60) + match + " " + strings.Repeat("y ", 60)
	start = strings.Index(padded, match)
	end = start + len(match)
	got = extractContext(padded, start, end)
	if !strings.HasPrefix(got, "...") || !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipses on both sides for a padded match, got %q", got)
	}
	if !strings.Contains(got, match) {
		t.Errorf("expected padded context to contain the match, got %q", got)
	}
}

func TestInsertReferences(t *testing.T) {
	d := setupTestDB(t)

	refs := []Reference{
		{SourceRFC: 2119, SourceSection: "1", TargetRFC: 8174, TargetSection: "", Context: "...updates RFC 2119..."},
	}
	if err := d.InsertReferences(refs); err != nil {
		t.Fatalf("InsertReferences: %v", err)
	}

	got, err := d.GetReferences(2119, "1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}
	if len(got) != 1 || got[0].TargetRFC != 8174 {
		t.Fatalf("expected 1 reference to RFC 8174, got %+v", got)
	}

	// Re-inserting the same key replaces rather than duplicates.
	refs[0].Context = "updated context"
	if err := d.InsertReferences(refs); err != nil {
		t.Fatalf("InsertReferences (replace): %v", err)
	}
	got, err = d.GetReferences(2119, "1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences (after replace): %v", err)
	}
	if len(got) != 1 || got[0].Context != "updated context" {
		t.Fatalf("expected replaced context, got %+v", got)
	}

	// Empty input is a no-op, not an error -- and must not delete anything.
	if err := d.InsertReferences(nil); err != nil {
		t.Errorf("InsertReferences(nil) should be a no-op, got error: %v", err)
	}
	got, err = d.GetReferences(2119, "1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences (after empty insert): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the existing reference to survive an empty insert, got %+v", got)
	}
}

// TestInsertReferences_ReimportDropsStaleRows covers issue #12: a re-import
// whose re-parse yields fewer (or different) references must remove the rows
// the previous import stored, not leave them behind alongside the new set.
func TestInsertReferences_ReimportDropsStaleRows(t *testing.T) {
	d := setupTestDB(t)

	// Seed data has 2 references from RFC 4271 section 5.1 (to 9293 and 793).
	// Re-import with a single, different reference from a different section.
	refs := []Reference{
		{SourceRFC: 4271, SourceSection: "5", TargetRFC: 1771, TargetSection: "", Context: "...obsoletes RFC 1771..."},
	}
	if err := d.InsertReferences(refs); err != nil {
		t.Fatalf("InsertReferences: %v", err)
	}

	all, err := d.queryReferences(
		refBaseQuery+" WHERE r.source_rfc = ? ORDER BY r.source_section, r.target_rfc", []any{4271},
	)
	if err != nil {
		t.Fatalf("query all references for 4271: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 reference after re-import, got %d: %+v", len(all), all)
	}
	if all[0].TargetRFC != 1771 || all[0].SourceSection != "5" {
		t.Fatalf("expected only the re-imported reference to remain, got %+v", all[0])
	}

	// References from other source RFCs must be untouched: seed a row for
	// another RFC and confirm a 4271 re-import leaves it alone.
	if err := d.InsertReferences([]Reference{
		{SourceRFC: 9293, SourceSection: "3.1", TargetRFC: 793, Context: "...RFC 793..."},
	}); err != nil {
		t.Fatalf("InsertReferences (9293): %v", err)
	}
	if err := d.InsertReferences(refs); err != nil {
		t.Fatalf("InsertReferences (4271 again): %v", err)
	}
	other, err := d.GetReferences(9293, "3.1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences (9293): %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("expected the 9293 reference to survive a 4271 re-import, got %+v", other)
	}
}

// TestDeleteReferencesForRFC covers the explicit clear used when a re-parse
// finds no references at all (InsertReferences with an empty set is a no-op).
func TestDeleteReferencesForRFC(t *testing.T) {
	d := setupTestDB(t)

	if err := d.DeleteReferencesForRFC(4271); err != nil {
		t.Fatalf("DeleteReferencesForRFC: %v", err)
	}
	got, err := d.GetReferences(4271, "5.1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 references after delete, got %+v", got)
	}

	// Deleting for an RFC with no rows is a no-op success.
	if err := d.DeleteReferencesForRFC(424242); err != nil {
		t.Errorf("DeleteReferencesForRFC on absent rfc: %v", err)
	}
}

func TestExtractReferences_BareMentions(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantTargetRFC int
		wantSection   string
	}{
		{"RFC N", "See RFC 793 for the original spec.", 793, ""},
		{"RFCN no separator", "See RFC793 for the original spec.", 793, ""},
		{"RFC-N dash", "See RFC-793 for the original spec.", 793, ""},
		{"IETF RFC N", "As per IETF RFC 3327, the identity is asserted.", 3327, ""},
		{"RFC N section X", "See RFC 3261 section 10.2 for SIP registration.", 3261, "10.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := ExtractReferences(9999, "1", tt.content, nil)
			var found bool
			for _, r := range refs {
				if r.TargetRFC == tt.wantTargetRFC && r.TargetSection == tt.wantSection {
					found = true
				}
			}
			if !found {
				t.Errorf("expected reference to RFC %d section %q, got: %+v", tt.wantTargetRFC, tt.wantSection, refs)
			}
		})
	}
}

func TestExtractReferences_PrefixForm(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantTargetRFC int
		wantSection   string
	}{
		{"short section", "See Section 4.2 of RFC 793 for the state machine.", 793, "4.2"},
		{"long section", "See section 3.8.6.2.1 of RFC 9293 for retransmission timing.", 9293, "3.8.6.2.1"},
		{"lettered appendix", "The state diagram is shown in Section A.2 of RFC 793.", 793, "A.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := ExtractReferences(9999, "1", tt.content, nil)
			var found bool
			for _, r := range refs {
				if r.TargetRFC == tt.wantTargetRFC && r.TargetSection == tt.wantSection {
					found = true
				}
			}
			if !found {
				t.Errorf("expected reference to RFC %d section %q, got: %+v", tt.wantTargetRFC, tt.wantSection, refs)
			}
		})
	}
}

// TestExtractReferences_PrefixNoDoubleCount guards the overlap-claim
// mechanism: rfcRefRE must not also add a bare, sectionless "RFC 793" entry
// for text already captured by the more specific rfcPrefixRefRE.
func TestExtractReferences_PrefixNoDoubleCount(t *testing.T) {
	content := "See Section 4.2 of RFC 793 for the details."
	refs := ExtractReferences(9999, "1", content, nil)
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 reference (no duplicate bare-RFC match), got %d: %+v", len(refs), refs)
	}
	if refs[0].TargetRFC != 793 || refs[0].TargetSection != "4.2" {
		t.Errorf("unexpected reference: %+v", refs[0])
	}
}

func TestExtractReferences_BracketDirect(t *testing.T) {
	tests := []struct {
		name          string
		content       string
		wantTargetRFC int
	}{
		{"dash form", "This document obsoletes [RFC-1034].", 1034},
		{"no separator", "This document obsoletes [RFC793].", 793},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := ExtractReferences(9999, "1", tt.content, nil)
			var found bool
			for _, r := range refs {
				if r.TargetRFC == tt.wantTargetRFC && r.TargetSection == "" {
					found = true
				}
			}
			if !found {
				t.Errorf("expected reference to RFC %d, got: %+v", tt.wantTargetRFC, refs)
			}
		})
	}
}

func TestExtractReferences_SelfReferenceExcluded(t *testing.T) {
	content := "As defined in RFC 793 section 3.1, this document updates RFC 793."
	refs := ExtractReferences(793, "1", content, nil)
	for _, r := range refs {
		if r.TargetRFC == 793 {
			t.Errorf("self-reference should be excluded: %+v", r)
		}
	}
}

func TestExtractReferences_Dedup(t *testing.T) {
	content := "See RFC 793 for the header format. Later, RFC 793 is referenced again."
	refs := ExtractReferences(9999, "1", content, nil)
	count := 0
	for _, r := range refs {
		if r.TargetRFC == 793 && r.TargetSection == "" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 deduped reference to RFC 793, got %d: %+v", count, refs)
	}
}

func TestParseBracketedRefMap(t *testing.T) {
	// Excerpt from RFC 9293's Normative References
	// (https://www.rfc-editor.org/rfc/rfc9293.txt).
	references := `
   [15]       Allman, M., "Requirements for Time-Based Loss Detection",
              BCP 233, RFC 8961, DOI 10.17487/RFC8961, November 2020,
              <https://www.rfc-editor.org/info/rfc8961>.

   [16]       Postel, J., "Transmission Control Protocol", STD 7,
              RFC 793, DOI 10.17487/RFC0793, September 1981,
              <https://www.rfc-editor.org/info/rfc793>.

   [17]       Nagle, J., "Congestion Control in IP/TCP Internetworks",
              RFC 896, DOI 10.17487/RFC0896, January 1984,
              <https://www.rfc-editor.org/info/rfc896>.
`
	m := ParseBracketedRefMap(references)
	want := map[string]int{"15": 8961, "16": 793, "17": 896}
	for num, rfc := range want {
		if m[num] != rfc {
			t.Errorf("expected [%s] to resolve to RFC %d, got %d", num, rfc, m[num])
		}
	}
}

func TestExtractReferences_NumericBracket(t *testing.T) {
	bracketMap := map[string]int{"16": 793}

	t.Run("bare bracket", func(t *testing.T) {
		content := "The original specification is in [16], which defines the header format."
		refs := ExtractReferences(9293, "3.1", content, bracketMap)
		var found bool
		for _, r := range refs {
			if r.TargetRFC == 793 && r.TargetSection == "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected [16] to resolve to RFC 793, got: %+v", refs)
		}
	})

	t.Run("bracket with section suffix", func(t *testing.T) {
		content := "See [16], Section 3.1 for the header format."
		refs := ExtractReferences(9293, "3.1", content, bracketMap)
		var found bool
		for _, r := range refs {
			if r.TargetRFC == 793 && r.TargetSection == "3.1" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected [16], Section 3.1 to resolve with section, got: %+v", refs)
		}
	})

	t.Run("unresolved bracket is skipped", func(t *testing.T) {
		content := "See [42] for unrelated information."
		refs := ExtractReferences(9293, "3.1", content, bracketMap)
		if len(refs) != 0 {
			t.Errorf("expected unresolved bracket [42] to be skipped, got: %+v", refs)
		}
	})

	t.Run("nil bracket map skips numeric bracket pattern entirely", func(t *testing.T) {
		content := "The original specification is in [16]."
		refs := ExtractReferences(9293, "3.1", content, nil)
		if len(refs) != 0 {
			t.Errorf("expected no references without a bracket map, got: %+v", refs)
		}
	})
}

// TestExtractReferences_InsertAndGetRoundTrip covers the full path from
// extraction through storage and back, using the package-local setupTestDB
// helper (see db_test.go) rather than internal/testutil to avoid an import
// cycle from this being package db, not db_test.
func TestExtractReferences_InsertAndGetRoundTrip(t *testing.T) {
	d := setupTestDB(t)

	content := "This section revises the state machine from Section 3.4 of RFC 793 and RFC 1122."
	refs := ExtractReferences(9293, "3.1.1", content, nil)

	if err := d.InsertReferences(refs); err != nil {
		t.Fatalf("InsertReferences: %v", err)
	}

	got, err := d.GetReferences(9293, "3.1.1", DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}

	want := map[int]string{793: "3.4", 1122: ""}
	for _, r := range got {
		if sec, ok := want[r.TargetRFC]; ok && r.TargetSection == sec {
			delete(want, r.TargetRFC)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing extracted references after round-trip: %v, got: %+v", want, got)
	}
}

func TestGetReferences(t *testing.T) {
	d := setupTestDB(t)

	t.Run("outgoing", func(t *testing.T) {
		refs, err := d.GetReferences(4271, "5.1", DirectionOutgoing, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("expected 2 outgoing refs, got %d", len(refs))
		}
	})

	t.Run("outgoing with subsections", func(t *testing.T) {
		// Section 5 itself has no refs, but its subsection 5.1 has 2.
		refs, err := d.GetReferences(4271, "5", DirectionOutgoing, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("expected 2 outgoing refs (from subsections), got %d", len(refs))
		}
	})

	t.Run("outgoing without subsections excludes them", func(t *testing.T) {
		refs, err := d.GetReferences(4271, "5", DirectionOutgoing, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("expected 0 refs for section 5 itself, got %d", len(refs))
		}
	})

	t.Run("incoming without section", func(t *testing.T) {
		refs, err := d.GetReferences(793, "", DirectionIncoming, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 incoming ref, got %d", len(refs))
		}
		if refs[0].SourceRFC != 4271 || refs[0].SourceSection != "5.1" {
			t.Errorf("unexpected source: %+v", refs[0])
		}
	})

	t.Run("incoming with section resolves target title", func(t *testing.T) {
		refs, err := d.GetReferences(9293, "3.1", DirectionIncoming, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("expected 1 incoming ref, got %d", len(refs))
		}
		if refs[0].TargetTitle != "Header Format" {
			t.Errorf("expected resolved target title 'Header Format', got %q", refs[0].TargetTitle)
		}
	})

	t.Run("no results", func(t *testing.T) {
		refs, err := d.GetReferences(99999, "", DirectionIncoming, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("default direction is outgoing", func(t *testing.T) {
		refs, err := d.GetReferences(4271, "5.1", "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 2 {
			t.Fatalf("expected 2 outgoing refs with default direction, got %d", len(refs))
		}
	})

	t.Run("invalid direction", func(t *testing.T) {
		_, err := d.GetReferences(4271, "5.1", "sideways", false)
		if err == nil {
			t.Fatal("expected error for invalid direction")
		}
	})

	// GetReferences expands subsections by exact parent_number equality
	// (see descendantsCTE in db/sections.go), not a LIKE prefix, so a
	// section number containing a SQL wildcard character ("_" matches any
	// single character in LIKE) can't over-match an unrelated sibling:
	// "3_x" must not sweep in a "3yx.1"-rooted reference (an unparented
	// decoy) under includeSubsections.
	t.Run("section number with underscore does not wildcard-match", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8000, '3_x', 'Underscore Section', 1, NULL, 'content a'),
			(8000, '3_x.1', 'Underscore Child', 2, '3_x', 'content b'),
			(8000, '3yx.1', 'Decoy Section', 2, NULL, 'content c')`); err != nil {
			t.Fatalf("seed sections: %v", err)
		}
		if err := d.Exec(`INSERT INTO rfc_references (source_rfc, source_section, target_rfc, target_section, context) VALUES
			(8000, '3_x.1', 9293, '', 'safe match'),
			(8000, '3yx.1', 9293, '', 'decoy match')`); err != nil {
			t.Fatalf("seed references: %v", err)
		}

		refs, err := d.GetReferences(8000, "3_x", DirectionOutgoing, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 || refs[0].SourceSection != "3_x.1" {
			t.Fatalf("expected exactly 1 ref from 3_x.1, got %+v", refs)
		}
	})

	// Regression test mirroring TestGetSection's slug-numbered-children
	// case: an outgoing reference stored under a slug-numbered child
	// section (parented via parent_number, not a dotted "II.N" number)
	// must be picked up when collecting from the parent with
	// includeSubsections.
	t.Run("outgoing with subsections finds refs under a slug-numbered child", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8200, 'II', 'Part II', 1, NULL, ''),
			(8200, 'messages', 'Messages', 2, 'II', 'See RFC 793 for details.')`); err != nil {
			t.Fatalf("seed sections: %v", err)
		}
		if err := d.Exec(`INSERT INTO rfc_references (source_rfc, source_section, target_rfc, target_section, context) VALUES
			(8200, 'messages', 793, '', 'See RFC 793 for details.')`); err != nil {
			t.Fatalf("seed references: %v", err)
		}

		refs, err := d.GetReferences(8200, "II", DirectionOutgoing, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 1 || refs[0].TargetRFC != 793 || refs[0].SourceSection != "messages" {
			t.Fatalf("expected 1 reference from child section 'messages', got %+v", refs)
		}
	})
}
