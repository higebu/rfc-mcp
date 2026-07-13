package db

import (
	"path/filepath"
	"testing"
)

func TestMeta_SetGetRoundtrip(t *testing.T) {
	d := setupTestDB(t)

	if err := d.SetMeta("built_at", "2026-07-12T00:00:00Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	got, ok := d.GetMeta("built_at")
	if !ok {
		t.Fatal("GetMeta: key not found after SetMeta")
	}
	if got != "2026-07-12T00:00:00Z" {
		t.Errorf("GetMeta = %q, want the value written by SetMeta", got)
	}

	// built_at/rfc_index_fetched_at are rewritten on every build/update, so
	// SetMeta on an existing key must overwrite rather than error.
	if err := d.SetMeta("built_at", "2026-07-13T00:00:00Z"); err != nil {
		t.Fatalf("SetMeta (overwrite): %v", err)
	}
	got, ok = d.GetMeta("built_at")
	if !ok || got != "2026-07-13T00:00:00Z" {
		t.Errorf("GetMeta after overwrite = (%q, %v), want (2026-07-13T00:00:00Z, true)", got, ok)
	}
}

func TestMeta_GetMissingKey(t *testing.T) {
	d := setupTestDB(t)
	if _, ok := d.GetMeta("nonexistent"); ok {
		t.Error("GetMeta on missing key: ok = true, want false")
	}
}

// TestMeta_GetMeta_NoMetaTable covers a database built before the meta
// table was introduced: GetMeta must degrade to "not found" rather than
// erroring, so serve/rfcRangeHint never fail on an old database.
func TestMeta_GetMeta_NoMetaTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "no-meta.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer d.Close()
	if err := d.Exec(`CREATE TABLE rfcs (number INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create rfcs table: %v", err)
	}

	if _, ok := d.GetMeta("built_at"); ok {
		t.Error("GetMeta on a database without a meta table: ok = true, want false")
	}
}
