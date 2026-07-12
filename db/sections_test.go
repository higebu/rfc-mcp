package db

import (
	"strings"
	"testing"
)

func TestGetTOC(t *testing.T) {
	d := setupTestDB(t)

	t.Run("existing rfc", func(t *testing.T) {
		sections, err := d.GetTOC(9293)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 4 {
			t.Fatalf("expected 4 sections, got %d", len(sections))
		}
		if sections[0].Number != "1" || sections[0].Title != "Introduction" {
			t.Errorf("unexpected first section: %+v", sections[0])
		}
	})

	t.Run("nonexistent rfc", func(t *testing.T) {
		sections, err := d.GetTOC(99999)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 0 {
			t.Fatalf("expected 0 sections, got %d", len(sections))
		}
	})
}

func TestGetSection(t *testing.T) {
	d := setupTestDB(t)

	t.Run("single section", func(t *testing.T) {
		sections, err := d.GetSection(9293, "1", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d", len(sections))
		}
		if sections[0].Content == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("with subsections", func(t *testing.T) {
		sections, err := d.GetSection(9293, "3", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 3 {
			t.Fatalf("expected 3 sections (3, 3.1, 3.1.1), got %d", len(sections))
		}
	})

	t.Run("without subsections", func(t *testing.T) {
		sections, err := d.GetSection(9293, "3", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d", len(sections))
		}
	})

	t.Run("nonexistent section", func(t *testing.T) {
		sections, err := d.GetSection(9293, "99", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 0 {
			t.Fatalf("expected 0 sections, got %d", len(sections))
		}
	})

	// GetSection matches descendants by exact parent_number equality, not a
	// LIKE prefix, so a section number containing a SQL wildcard character
	// ("_" matches any single character in LIKE) can't over-match an
	// unrelated sibling: "3_x" must not sweep in "3yx.1" (an unparented
	// decoy) when queried with includeSubsections.
	t.Run("section number with underscore does not wildcard-match", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8000, '3_x', 'Underscore Section', 1, NULL, 'content a'),
			(8000, '3_x.1', 'Underscore Child', 2, '3_x', 'content b'),
			(8000, '3yx.1', 'Decoy Section', 2, NULL, 'content c')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		sections, err := d.GetSection(8000, "3_x", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 2 {
			t.Fatalf("expected 2 sections (3_x, 3_x.1), got %d: %+v", len(sections), sections)
		}
		for _, s := range sections {
			if s.Number == "3yx.1" {
				t.Errorf("decoy section %q matched despite having no parent_number relation", s.Number)
			}
		}
	})

	// Regression test for RFC 1's Tier-2 parse shape: section "II" is a
	// nearly-empty parent whose real children ("Messages", "Links", ...)
	// are numbered by slug, not by a dotted "II.N" child number, so a
	// "number LIKE 'II.%'" prefix match (the old implementation) can never
	// find them; the parent_number chain must.
	t.Run("slug-numbered children found via parent_number, not a dotted prefix", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8200, 'II', 'Part II', 1, NULL, ''),
			(8200, 'messages', 'Messages', 2, 'II', 'Messages section content.'),
			(8200, 'links', 'Links', 2, 'II', 'Links section content.')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		sections, err := d.GetSection(8200, "II", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 3 {
			t.Fatalf("expected 3 sections (II, messages, links), got %d: %+v", len(sections), sections)
		}
		var gotMessages, gotLinks bool
		for _, s := range sections {
			if s.Number == "messages" && strings.Contains(s.Content, "Messages section content") {
				gotMessages = true
			}
			if s.Number == "links" && strings.Contains(s.Content, "Links section content") {
				gotLinks = true
			}
		}
		if !gotMessages || !gotLinks {
			t.Errorf("expected slug-numbered children's content, got: %+v", sections)
		}
	})
}

func TestSectionHeading(t *testing.T) {
	if got, want := SectionHeading("3.1", "Header Format"), "3.1.  Header Format\n\n"; got != want {
		t.Errorf("SectionHeading(%q, %q) = %q, want %q", "3.1", "Header Format", got, want)
	}
	for _, number := range []string{"header", "body"} {
		if got := SectionHeading(number, "Untitled"); got != "" {
			t.Errorf("SectionHeading(%q, ...) = %q, want empty (pseudo-section)", number, got)
		}
	}
}

func TestGetChildren(t *testing.T) {
	d := setupTestDB(t)

	t.Run("returns direct children in document order", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8300, '8', 'FSM Events', 1, NULL, ''),
			(8300, '8.1', 'Optional Events', 2, '8', ''),
			(8300, '8.1.1', 'Optional Events A', 3, '8.1', 'content a'),
			(8300, '8.1.2', 'Optional Events B', 3, '8.1', 'content b')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		children, err := d.GetChildren(8300, "8.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 2 {
			t.Fatalf("expected 2 children, got %d: %+v", len(children), children)
		}
		if children[0].Number != "8.1.1" || children[1].Number != "8.1.2" {
			t.Errorf("expected document order 8.1.1, 8.1.2, got %+v", children)
		}
	})

	t.Run("no children", func(t *testing.T) {
		children, err := d.GetChildren(9293, "1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 0 {
			t.Errorf("expected 0 children, got %d", len(children))
		}
	})
}

func TestGetDescendantsByPrefix(t *testing.T) {
	d := setupTestDB(t)

	// RFC 892's shape after reparentDanglingAncestors: "7.0" never got its
	// own heading, so "7.0.1"/"7.0.2" point at the real ancestor "7"
	// instead of "7.0" -- GetChildren(rfc, "7.0") finds nothing, but their
	// Number still starts with "7.0.".
	t.Run("finds descendants whose ParentNumber was rerouted past the missing number", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8400, '7', 'Protocol Classes', 1, NULL, ''),
			(8400, '7.0.1', 'Characteristics of Class 0', 2, '7', 'content a'),
			(8400, '7.0.2', 'Functions of Class 0', 2, '7', 'content b'),
			(8400, '7.0.3.1', 'Connection Establishment Phase', 3, '7.0.3', 'content c')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		children, err := d.GetDescendantsByPrefix(8400, "7.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 2 {
			t.Fatalf("expected 2 direct descendants (7.0.1, 7.0.2), got %d: %+v", len(children), children)
		}
		if children[0].Number != "7.0.1" || children[1].Number != "7.0.2" {
			t.Errorf("expected document order 7.0.1, 7.0.2, got %+v", children)
		}
	})

	t.Run("does not include descendants more than one level deeper", func(t *testing.T) {
		children, err := d.GetDescendantsByPrefix(8400, "7.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, c := range children {
			if c.Number == "7.0.3.1" {
				t.Errorf("expected only direct descendants, got a deeper one: %+v", children)
			}
		}
	})

	t.Run("no descendants", func(t *testing.T) {
		children, err := d.GetDescendantsByPrefix(9293, "99")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 0 {
			t.Errorf("expected 0 descendants, got %d", len(children))
		}
	})

	// Mirrors GetSection's own underscore-escaping regression test: "_"
	// and "%" are LIKE wildcards and must be matched literally.
	t.Run("section number with underscore does not wildcard-match", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8401, '3_x.1', 'Underscore Child', 2, NULL, 'content a'),
			(8401, '3yx.1', 'Decoy Section', 2, NULL, 'content b')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		children, err := d.GetDescendantsByPrefix(8401, "3_x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(children) != 1 || children[0].Number != "3_x.1" {
			t.Errorf("expected only 3_x.1, got %+v", children)
		}
	})
}

func TestGetDocument(t *testing.T) {
	d := setupTestDB(t)

	t.Run("existing rfc concatenates all sections in document order", func(t *testing.T) {
		doc, err := d.GetDocument(9293)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		idxIntro := strings.Index(doc, "TCP is a reliable, connection-oriented transport layer protocol.")
		idxHeading := strings.Index(doc, "3.1.  Header Format")
		idxHeader := strings.Index(doc, "The TCP header contains Source Port")
		idxSourcePort := strings.Index(doc, "as discussed in Section 3.1 of this document")
		if idxIntro < 0 || idxHeading < 0 || idxHeader < 0 || idxSourcePort < 0 {
			t.Fatalf("expected all section contents present, got: %q", doc)
		}
		if !(idxIntro < idxHeading && idxHeading < idxHeader && idxHeader < idxSourcePort) {
			t.Errorf("expected document order 1 < 3.1 heading < 3.1 content < 3.1.1, got positions %d, %d, %d, %d",
				idxIntro, idxHeading, idxHeader, idxSourcePort)
		}
	})

	t.Run("nonexistent rfc returns empty document", func(t *testing.T) {
		doc, err := d.GetDocument(99999)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if doc != "" {
			t.Errorf("expected empty document for nonexistent rfc, got: %q", doc)
		}
	})
}

func TestInsertRFCWithSections(t *testing.T) {
	d := setupTestDB(t)

	rfc := RFC{Number: 2119, Title: "Key words for use in RFCs to Indicate Requirement Levels", Stream: "IETF"}
	sections := []Section{
		{RFC: 2119, Number: "1", Title: "MUST", Level: 1, Content: "MUST This word means that the definition is an absolute requirement."},
		{RFC: 2119, Number: "2", Title: "SHOULD", Level: 1, Content: "SHOULD This word means valid reasons may exist to ignore a particular item."},
	}
	if err := d.InsertRFCWithSections(rfc, sections); err != nil {
		t.Fatalf("InsertRFCWithSections: %v", err)
	}

	got, err := d.GetRFCMetadata(2119)
	if err != nil {
		t.Fatalf("GetRFCMetadata: %v", err)
	}
	if got.Title != rfc.Title {
		t.Errorf("expected title %q, got %q", rfc.Title, got.Title)
	}

	toc, err := d.GetTOC(2119)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if len(toc) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(toc))
	}

	results, err := d.Search("absolute requirement", []int{2119}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 search hit, got %d", len(results))
	}
}

// TestInsertRFCWithSections_ReupsertKeepsFTSConsistent verifies the
// delete-then-insert upsert pattern: re-inserting the same RFC must not
// leave stale or duplicate rows in the FTS index.
func TestInsertRFCWithSections_ReupsertKeepsFTSConsistent(t *testing.T) {
	d := setupTestDB(t)

	rfc := RFC{Number: 5321, Title: "Simple Mail Transfer Protocol", Stream: "IETF"}
	sections := []Section{
		{RFC: 5321, Number: "1", Title: "Introduction", Level: 1, Content: "SMTP transports electronic mail reliably and efficiently."},
	}
	if err := d.InsertRFCWithSections(rfc, sections); err != nil {
		t.Fatalf("InsertRFCWithSections (first): %v", err)
	}

	// Re-insert the same RFC and section content unchanged.
	if err := d.InsertRFCWithSections(rfc, sections); err != nil {
		t.Fatalf("InsertRFCWithSections (second): %v", err)
	}

	results, err := d.Search("electronic mail", []int{5321}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 hit after re-upsert, got %d: %+v", len(results), results)
	}

	toc, err := d.GetTOC(5321)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if len(toc) != 1 {
		t.Fatalf("expected exactly 1 section after re-upsert, got %d", len(toc))
	}

	// Now change the content and re-insert; the old content must not still
	// be searchable and the new content must be.
	sections[0].Content = "SMTP now discusses extended SMTP (ESMTP) service extensions."
	if err := d.InsertRFCWithSections(rfc, sections); err != nil {
		t.Fatalf("InsertRFCWithSections (updated content): %v", err)
	}

	results, err = d.Search("electronic mail", []int{5321}, 10)
	if err != nil {
		t.Fatalf("Search (stale content): %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected stale content to no longer match, got %+v", results)
	}

	results, err = d.Search("ESMTP", []int{5321}, 10)
	if err != nil {
		t.Fatalf("Search (new content): %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected new content to match, got %d hits", len(results))
	}
}

// TestInsertRFCWithSections_ReparseDropsOrphanSections covers a re-parse that
// shrinks the section set (e.g. a parser fix that no longer recognizes a
// heading it used to): the old delete-then-insert scoped its DELETE to only
// the (rfc, number) pairs present in the NEW set, so a section absent from a
// re-parse lingered in both the sections table and its FTS index forever.
func TestInsertRFCWithSections_ReparseDropsOrphanSections(t *testing.T) {
	d := setupTestDB(t)

	rfc := RFC{Number: 6120, Title: "Extensible Messaging and Presence Protocol (XMPP)", Stream: "IETF"}
	sections := []Section{
		{RFC: 6120, Number: "1", Title: "Introduction", Level: 1, Content: "XMPP is a protocol for streaming XML elements."},
		{RFC: 6120, Number: "2", Title: "Common Concepts", Level: 1, Content: "This section describes concepts common to XMPP."},
		{RFC: 6120, Number: "3", Title: "Orphaned Section", Level: 1, Content: "This section discusses widget frobnication uniquely."},
	}
	if err := d.InsertRFCWithSections(rfc, sections); err != nil {
		t.Fatalf("InsertRFCWithSections (first): %v", err)
	}

	// Re-parse drops section 3 (e.g. the parser no longer recognizes it).
	if err := d.InsertRFCWithSections(rfc, sections[:2]); err != nil {
		t.Fatalf("InsertRFCWithSections (re-parse): %v", err)
	}

	toc, err := d.GetTOC(6120)
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if len(toc) != 2 {
		t.Fatalf("expected exactly 2 sections after re-parse, got %d: %+v", len(toc), toc)
	}

	results, err := d.Search("widget frobnication", []int{6120}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected dropped section's content to no longer be searchable, got %+v", results)
	}
}
