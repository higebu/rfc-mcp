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
	// LookupMeta must also treat the missing table as absence, not an error.
	if _, ok, err := d.LookupMeta("built_at"); err != nil || ok {
		t.Errorf("LookupMeta without meta table = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

// TestLookupMeta covers the error-aware variant of GetMeta: present and
// absent keys report a nil error, while a genuine database failure (here, a
// closed handle) is propagated instead of masquerading as absence.
func TestLookupMeta(t *testing.T) {
	d := setupTestDB(t)

	if err := d.SetMeta("built_at", "2026-07-12T00:00:00Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	v, ok, err := d.LookupMeta("built_at")
	if err != nil || !ok || v != "2026-07-12T00:00:00Z" {
		t.Errorf("LookupMeta(built_at) = (%q, %v, %v), want (2026-07-12T00:00:00Z, true, nil)", v, ok, err)
	}

	v, ok, err = d.LookupMeta("nonexistent")
	if err != nil || ok || v != "" {
		t.Errorf("LookupMeta(nonexistent) = (%q, %v, %v), want (\"\", false, nil)", v, ok, err)
	}
}

// TestLookupMeta_OtherMissingTableIsError guards the tightened missing-table
// match: only the meta table itself being absent counts as "key absent". An
// error mentioning a *different* missing table -- here a meta view whose
// backing table was dropped -- must propagate, not be swallowed as absence.
func TestLookupMeta_OtherMissingTableIsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "broken-view.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer d.Close()
	if err := d.ExecScript(`
		CREATE TABLE vanished (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE VIEW meta AS SELECT key, value FROM vanished;
		DROP TABLE vanished;
	`); err != nil {
		t.Fatalf("set up broken meta view: %v", err)
	}

	if _, ok, err := d.LookupMeta("built_at"); err == nil {
		t.Errorf("LookupMeta with a broken meta view: err = nil (ok=%v), want the missing-backing-table error", ok)
	}
}

func TestLookupMeta_PropagatesErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "closed.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	d.Close()

	if _, ok, err := d.LookupMeta("built_at"); err == nil {
		t.Errorf("LookupMeta on a closed handle: err = nil (ok=%v), want an error", ok)
	}
}
