package db

import "testing"

func TestUpsertRFC_And_GetRFCMetadata(t *testing.T) {
	d := setupTestDB(t)

	rfc := RFC{
		Number:      8259,
		Title:       "The JavaScript Object Notation (JSON) Data Interchange Format",
		Status:      "INTERNET STANDARD",
		Stream:      "IETF",
		Date:        "2017-12",
		PageCount:   16,
		Authors:     []string{"T. Bray"},
		Keywords:    []string{"JSON", "data interchange"},
		Abstract:    "JavaScript Object Notation (JSON) is a text format...",
		WG:          "jsonbis",
		Area:        "art",
		DOI:         "10.17487/RFC8259",
		ErrataURL:   "https://www.rfc-editor.org/errata/rfc8259",
		Obsoletes:   []int{7159},
		ObsoletedBy: nil,
		Updates:     nil,
		UpdatedBy:   nil,
		Also:        []string{"STD 90"},
		NotIssued:   false,
	}
	if err := d.UpsertRFC(rfc); err != nil {
		t.Fatalf("UpsertRFC: %v", err)
	}

	got, err := d.GetRFCMetadata(8259)
	if err != nil {
		t.Fatalf("GetRFCMetadata: %v", err)
	}
	if got.Title != rfc.Title || got.Status != rfc.Status || got.Stream != rfc.Stream {
		t.Errorf("scalar fields mismatch: %+v", got)
	}
	if len(got.Authors) != 1 || got.Authors[0] != "T. Bray" {
		t.Errorf("Authors round-trip failed: %+v", got.Authors)
	}
	if len(got.Obsoletes) != 1 || got.Obsoletes[0] != 7159 {
		t.Errorf("Obsoletes round-trip failed: %+v", got.Obsoletes)
	}
	if len(got.ObsoletedBy) != 0 {
		t.Errorf("expected empty ObsoletedBy, got %+v", got.ObsoletedBy)
	}
	if len(got.Also) != 1 || got.Also[0] != "STD 90" {
		t.Errorf("Also round-trip failed: %+v", got.Also)
	}

	// Upsert replaces existing fields (INSERT OR REPLACE semantics).
	rfc.Title = "Updated Title"
	rfc.Obsoletes = []int{7159, 4627}
	if err := d.UpsertRFC(rfc); err != nil {
		t.Fatalf("UpsertRFC (replace): %v", err)
	}
	got, err = d.GetRFCMetadata(8259)
	if err != nil {
		t.Fatalf("GetRFCMetadata (after replace): %v", err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("expected updated title, got %q", got.Title)
	}
	if len(got.Obsoletes) != 2 {
		t.Errorf("expected 2 obsoletes after replace, got %+v", got.Obsoletes)
	}

	if _, err := d.GetRFCMetadata(99999); err == nil {
		t.Error("expected error for nonexistent RFC")
	}
}

func TestListRFCs(t *testing.T) {
	d := setupTestDB(t)

	t.Run("all excludes not_issued", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Seed has 4 rfcs rows; 9999 is not_issued and must be excluded.
		if len(result.RFCs) != 3 {
			t.Fatalf("expected 3 rfcs, got %d: %+v", len(result.RFCs), result.RFCs)
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
		for _, r := range result.RFCs {
			if r.Number == 9999 {
				t.Error("not_issued RFC 9999 should not appear in results")
			}
		}
	})

	t.Run("filter by title substring", func(t *testing.T) {
		result, err := d.ListRFCs("Border Gateway", "", "", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 || result.RFCs[0].Number != 4271 {
			t.Fatalf("expected only RFC 4271, got %+v", result.RFCs)
		}
	})

	t.Run("filter by stream", func(t *testing.T) {
		result, err := d.ListRFCs("", "Legacy", "", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 || result.RFCs[0].Number != 793 {
			t.Fatalf("expected only RFC 793, got %+v", result.RFCs)
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "DRAFT STANDARD", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 || result.RFCs[0].Number != 4271 {
			t.Fatalf("expected only RFC 4271, got %+v", result.RFCs)
		}
	})

	t.Run("filter by wg", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "", "tcpm", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 || result.RFCs[0].Number != 9293 {
			t.Fatalf("expected only RFC 9293, got %+v", result.RFCs)
		}
	})

	t.Run("no match", func(t *testing.T) {
		result, err := d.ListRFCs("nonexistent topic", "", "", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 0 || result.TotalCount != 0 {
			t.Fatalf("expected 0 rfcs, got %+v", result)
		}
	})

	t.Run("with limit and offset", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "", "", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 {
			t.Fatalf("expected 1 rfc, got %d", len(result.RFCs))
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
		if result.RFCs[0].Number != 793 {
			t.Errorf("expected first rfc 793 (ordered by number), got %d", result.RFCs[0].Number)
		}

		result, err = d.ListRFCs("", "", "", "", 1, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 1 || result.RFCs[0].Number != 4271 {
			t.Fatalf("expected second rfc 4271, got %+v", result.RFCs)
		}
	})

	t.Run("offset beyond end", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "", "", 10, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 0 {
			t.Fatalf("expected 0 rfcs, got %d", len(result.RFCs))
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
	})

	t.Run("no limit", func(t *testing.T) {
		result, err := d.ListRFCs("", "", "", "", -1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 3 {
			t.Fatalf("expected 3 rfcs, got %d", len(result.RFCs))
		}
	})

	t.Run("combined filters narrow to zero", func(t *testing.T) {
		result, err := d.ListRFCs("Border Gateway", "Legacy", "", "", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.RFCs) != 0 {
			t.Fatalf("expected 0 rfcs for mismatched filters, got %+v", result.RFCs)
		}
	})
}
