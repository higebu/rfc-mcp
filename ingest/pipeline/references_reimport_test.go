package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// A re-import whose re-parse yields no references must clear the rows left
// by the previous import (InsertReferences cannot: an empty set gives it no
// source RFC to scope its delete to).
func TestPipeline_ImportFile_ReimportClearsReferences(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	withRefs := "Request for Comments: 9999\n\n" +
		"                          A Test Document\n\n" +
		"1.  Introduction\n\n" +
		"   This document updates RFC 793 for testing purposes.\n"
	withoutRefs := "Request for Comments: 9999\n\n" +
		"                          A Test Document\n\n" +
		"1.  Introduction\n\n" +
		"   This document no longer cites anything.\n"

	dir1 := t.TempDir()
	path1 := filepath.Join(dir1, "rfc9999.txt")
	if err := os.WriteFile(path1, []byte(withRefs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.ImportFile(path1); err != nil {
		t.Fatalf("ImportFile (with refs): %v", err)
	}
	refs, err := d.GetReferences(793, "", "incoming", false)
	if err != nil {
		t.Fatalf("GetReferences after first import: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("expected incoming references to RFC 793 after first import")
	}

	dir2 := t.TempDir()
	path2 := filepath.Join(dir2, "rfc9999.txt")
	if err := os.WriteFile(path2, []byte(withoutRefs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.ImportFile(path2); err != nil {
		t.Fatalf("ImportFile (no refs): %v", err)
	}
	refs, err = d.GetReferences(793, "", "incoming", false)
	if err != nil {
		t.Fatalf("GetReferences after re-import: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected stale references cleared on re-import, got %d", len(refs))
	}
}
