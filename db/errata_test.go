package db

import "testing"

func TestGetErrataByRFC(t *testing.T) {
	d := setupTestDB(t)

	t.Run("existing rfc", func(t *testing.T) {
		items, err := d.GetErrataByRFC(4271)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 errata, got %d", len(items))
		}
		if items[0].ID != 1 || items[1].ID != 2 {
			t.Errorf("expected errata ordered by id, got ids %d, %d", items[0].ID, items[1].ID)
		}
		if items[0].Status != "Verified" || items[0].Type != "Technical" {
			t.Errorf("unexpected fields for errata 1: %+v", items[0])
		}
	})

	t.Run("rfc without errata", func(t *testing.T) {
		items, err := d.GetErrataByRFC(9293)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 errata, got %d", len(items))
		}
	})
}

func TestReplaceAllErrata(t *testing.T) {
	d := setupTestDB(t)

	// Seed data already has 2 errata for RFC 4271. Replace wholesale with a
	// different set for a different RFC and confirm the old rows are gone.
	newItems := []Errata{
		{ID: 100, RFC: 9293, Status: "Verified", Type: "Technical", Section: "3.1", OrigText: "old", CorrectText: "new"},
	}
	if err := d.ReplaceAllErrata(newItems); err != nil {
		t.Fatalf("ReplaceAllErrata: %v", err)
	}

	// Old errata for RFC 4271 must be gone.
	items, err := d.GetErrataByRFC(4271)
	if err != nil {
		t.Fatalf("GetErrataByRFC(4271): %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 errata for RFC 4271 after wholesale replace, got %d: %+v", len(items), items)
	}

	// New errata for RFC 9293 must be present.
	items, err = d.GetErrataByRFC(9293)
	if err != nil {
		t.Fatalf("GetErrataByRFC(9293): %v", err)
	}
	if len(items) != 1 || items[0].ID != 100 {
		t.Fatalf("expected 1 new errata with id 100, got %+v", items)
	}

}

// TestReplaceAllErrata_EmptySetRefused covers the safety net: a valid but
// empty errata.json must not wipe previously stored errata.
func TestReplaceAllErrata_EmptySetRefused(t *testing.T) {
	d := setupTestDB(t)

	// Seed data holds 2 errata for RFC 4271: an empty replace is refused.
	if err := d.ReplaceAllErrata(nil); err == nil {
		t.Fatal("ReplaceAllErrata(nil) over a non-empty table: expected error, got nil")
	}

	// The stored rows must be untouched by the refused replace.
	items, err := d.GetErrataByRFC(4271)
	if err != nil {
		t.Fatalf("GetErrataByRFC(4271): %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 errata to survive the refused replace, got %d", len(items))
	}
}

// TestReplaceAllErrata_EmptyOverEmpty covers empty items over an empty
// table: a no-op success, not an error.
func TestReplaceAllErrata_EmptyOverEmpty(t *testing.T) {
	d := setupTestDB(t)
	if err := d.Exec("DELETE FROM errata"); err != nil {
		t.Fatalf("clear errata: %v", err)
	}

	if err := d.ReplaceAllErrata(nil); err != nil {
		t.Fatalf("ReplaceAllErrata(nil) over an empty table: %v", err)
	}
	if err := d.ReplaceAllErrata([]Errata{}); err != nil {
		t.Fatalf("ReplaceAllErrata(empty) over an empty table: %v", err)
	}
}
