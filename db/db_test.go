package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedData mirrors internal/testutil.SeedData. It is duplicated here (rather
// than imported) to avoid an import cycle: testutil imports db.
const seedData = `
INSERT INTO rfcs (number, title, status, stream, date, page_count, authors, keywords, abstract, wg, area, doi, errata_url, obsoletes, obsoleted_by, updates, updated_by, also, not_issued) VALUES
    (9293, 'Transmission Control Protocol (TCP) Specification', 'INTERNET STANDARD', 'IETF', '2022-08', 160,
     '["W. Eddy"]', '["TCP","transport protocol"]',
     'This document specifies the Transmission Control Protocol (TCP).',
     'tcpm', 'tsv', '10.17487/RFC9293', 'https://www.rfc-editor.org/errata/rfc9293',
     '[793,879,2873,6093,6429,6528,6691]', '[]', '[1122]', '[]', '["STD 7"]', 0),
    (793, 'TRANSMISSION CONTROL PROTOCOL', 'INTERNET STANDARD', 'Legacy', '1981-09', 85,
     '["J. Postel"]', '[]',
     'This document describes the DoD Standard Transmission Control Protocol.',
     '', '', '', '', '[]', '[9293]', '[]', '[]', '[]', 0),
    (4271, 'A Border Gateway Protocol 4 (BGP-4)', 'DRAFT STANDARD', 'IETF', '2006-01', 104,
     '["Y. Rekhter","T. Li","S. Hares"]', '["BGP","routing"]',
     'This document defines the Border Gateway Protocol version 4 (BGP-4).',
     'idr', 'rtg', '10.17487/RFC4271', '', '[1771]', '[]', '[]', '[]', '[]', 0),
    (9999, '', '', '', '', 0, '[]', '[]', '', '', '', '', '', '[]', '[]', '[]', '[]', '[]', 1);

-- Fixture parent rows for tests that insert ad-hoc sections rows directly
-- (PRAGMA foreign_keys=ON makes sections.rfc REFERENCES rfcs(number)
-- enforced, so every fixture section needs a parent rfcs row). not_issued=1
-- keeps them out of ListRFCs results and totals.
INSERT INTO rfcs (number, title, not_issued) VALUES
    (8000, 'Test Fixture 8000', 1),
    (8100, 'Test Fixture 8100', 1),
    (8200, 'Test Fixture 8200', 1),
    (8300, 'Test Fixture 8300', 1),
    (8301, 'Test Fixture 8301', 1),
    (8400, 'Test Fixture 8400', 1),
    (8401, 'Test Fixture 8401', 1),
    (8500, 'Test Fixture 8500', 1),
    (8501, 'Test Fixture 8501', 1);

INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
    (9293, '1', 'Introduction', 1, NULL,
     '# 1 Introduction
This document specifies the Transmission Control Protocol (TCP). TCP is a reliable, connection-oriented transport layer protocol.'),
    (9293, '3', 'Functional Specification', 1, NULL,
     '# 3 Functional Specification
TCP uses a three-way handshake to establish a connection between two endpoints.'),
    (9293, '3.1', 'Header Format', 2, '3',
     '## 3.1 Header Format
The TCP header contains Source Port, Destination Port, Sequence Number, and Acknowledgment Number fields. See RFC 793 for the original header format.'),
    (9293, '3.1.1', 'Source Port', 3, '3.1',
     '### 3.1.1 Source Port
The source port number, as discussed in Section 3.1 of this document.'),
    (793, '1', 'Introduction', 1, NULL,
     '# 1 Introduction
This document describes the DoD Standard Transmission Control Protocol (TCP).'),
    (4271, '5', 'Path Attributes', 1, NULL,
     '# 5 Path Attributes
Each path attribute is a triple <attribute type, attribute length, attribute value>.'),
    (4271, '5.1', 'Path Attribute Usage', 2, '5',
     '## 5.1 Path Attribute Usage
This section is based on RFC 9293 Section 3.1 and the original specification in RFC 793.');

INSERT INTO rfc_references (source_rfc, source_section, target_rfc, target_section, context) VALUES
    (4271, '5.1', 9293, '3.1', '...based on RFC 9293 Section 3.1 and the original...'),
    (4271, '5.1', 793, '', '...original specification in RFC 793...');

INSERT INTO errata (id, rfc, status, type, section, orig_text, correct_text, notes, submitted_date, submitter_name, verifier_name, updated_date) VALUES
    (1, 4271, 'Verified', 'Technical', '5.1', 'triple <attribute type, attribute length>', 'triple <attribute type, attribute length, attribute value>', 'Missing field in original text.', '2020-01-01', 'Jane Doe', 'John Smith', '2020-02-01'),
    (2, 4271, 'Reported', 'Editorial', '5', 'Path Attribute', 'Path Attributes', '', '2021-03-15', 'Alex Kim', '', '');
`

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := d.ExecScript(Schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	if err := d.ExecScript(seedData); err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestInitSchema_FreshDB covers the public InitSchema entrypoint that
// ingestion tooling uses, separate from the ExecScript(Schema) shortcut used
// in the rest of this package's tests.
func TestInitSchema_FreshDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "init.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	// Calling InitSchema twice should be idempotent.
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema (second call): %v", err)
	}

	for _, table := range []string{"rfcs", "sections", "sections_fts", "rfc_references", "errata"} {
		if err := d.Exec("SELECT 1 FROM " + table + " WHERE 0 = 1"); err != nil {
			t.Errorf("table %q missing or query failed: %v", table, err)
		}
	}
}

// TestOpen_ReadOnly verifies the public Open constructor returns a working
// handle that can query previously persisted data.
func TestOpen_ReadOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "readonly.db")

	rw, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := rw.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := rw.UpsertRFC(RFC{Number: 9293, Title: "TCP Specification"}); err != nil {
		t.Fatalf("UpsertRFC: %v", err)
	}
	rw.Close()

	ro, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ro.Close()

	result, err := ro.ListRFCs("", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("ListRFCs: %v", err)
	}
	if len(result.RFCs) != 1 || result.RFCs[0].Number != 9293 {
		t.Errorf("expected RFC 9293, got %+v", result.RFCs)
	}

	// Open is read-only: writes must fail.
	if err := ro.Exec("DELETE FROM rfcs"); err == nil {
		t.Error("expected write to fail on a read-only handle")
	}
}

// TestOpen_PathWithSpecialChars verifies Open's file: URI construction
// percent-encodes characters that would otherwise mis-parse as the URI query
// string ('?'), fragment ('#'), or an escape sequence ('%') -- and that
// mode=ro still survives the encoding.
func TestOpen_PathWithSpecialChars(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "we?ird %40 #dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dir, "odd?na%me#.db")

	rw, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := rw.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := rw.UpsertRFC(RFC{Number: 793, Title: "TCP"}); err != nil {
		t.Fatalf("UpsertRFC: %v", err)
	}
	rw.Close()

	ro, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with special chars in path: %v", err)
	}
	defer ro.Close()

	result, err := ro.ListRFCs("", "", "", "", 0, 0)
	if err != nil {
		t.Fatalf("ListRFCs: %v", err)
	}
	if len(result.RFCs) != 1 || result.RFCs[0].Number != 793 {
		t.Errorf("expected RFC 793, got %+v", result.RFCs)
	}

	// mode=ro must survive the path encoding: writes fail.
	if err := ro.Exec("DELETE FROM rfcs"); err == nil {
		t.Error("expected write to fail on a read-only handle")
	}
}

// TestOpenReadWrite_ForeignKeysEnforced verifies foreign-key enforcement is
// active on read-write handles: a sections row referencing a nonexistent RFC
// must be rejected. The pragma travels in the DSN (_pragma=foreign_keys(1)),
// so it applies to every connection the pool opens, not just the one that
// happened to serve a one-shot Exec.
func TestOpenReadWrite_ForeignKeysEnforced(t *testing.T) {
	d := setupTestDB(t)

	var fk int
	if err := d.conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", fk)
	}

	err := d.Exec(
		"INSERT INTO sections (rfc, number, title, level, content) VALUES (?, ?, ?, ?, ?)",
		424242, "1", "Ghost", 1, "dangling parent",
	)
	if err == nil {
		t.Error("expected FK violation inserting a section for a nonexistent RFC")
	}
}

// TestExec_DirectSQL covers the exported Exec helper used for ad-hoc writes.
func TestExec_DirectSQL(t *testing.T) {
	d := setupTestDB(t)

	if err := d.Exec(
		"INSERT INTO rfcs (number, title, stream) VALUES (?, ?, ?)",
		1234, "Exec-inserted", "IETF",
	); err != nil {
		t.Fatalf("Exec insert: %v", err)
	}

	result, err := d.ListRFCs("", "IETF", "", "", 0, 0)
	if err != nil {
		t.Fatalf("ListRFCs: %v", err)
	}
	found := false
	for _, r := range result.RFCs {
		if r.Number == 1234 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected RFC 1234 in stream IETF, got %+v", result.RFCs)
	}

	if err := d.Exec("NOT VALID SQL"); err == nil {
		t.Error("expected error for invalid SQL")
	}
}

// TestEmptyResultsAreNonNilSlices verifies every list-returning query helper
// yields an empty non-nil slice when nothing matches, so MCP tool responses
// serialize as [] rather than null.
func TestEmptyResultsAreNonNilSlices(t *testing.T) {
	d := setupTestDB(t)

	if items, err := d.GetErrataByRFC(77777); err != nil || items == nil {
		t.Errorf("GetErrataByRFC empty = (%v, %v), want non-nil slice", items, err)
	}
	if result, err := d.ListRFCs("zzz-no-such-title", "", "", "", 0, 0); err != nil || result.RFCs == nil {
		t.Errorf("ListRFCs empty: err=%v, RFCs nil=%v, want non-nil slice", err, result == nil || result.RFCs == nil)
	}
	if refs, err := d.GetReferences(77777, "", DirectionIncoming, false); err != nil || refs == nil {
		t.Errorf("GetReferences empty = (%v, %v), want non-nil slice", refs, err)
	}
	if results, err := d.Search("xyznonexistent", nil, 10); err != nil || results == nil {
		t.Errorf("Search empty = (%v, %v), want non-nil slice", results, err)
	}
	if toc, err := d.GetTOC(77777); err != nil || toc == nil {
		t.Errorf("GetTOC empty = (%v, %v), want non-nil slice", toc, err)
	}
	if secs, err := d.GetSection(9293, "no-such-section", false); err != nil || secs == nil {
		t.Errorf("GetSection empty = (%v, %v), want non-nil slice", secs, err)
	}
	if secs, err := d.GetSection(9293, "no-such-section", true); err != nil || secs == nil {
		t.Errorf("GetSection empty (subsections) = (%v, %v), want non-nil slice", secs, err)
	}
	if children, err := d.GetChildren(9293, "no-such-section"); err != nil || children == nil {
		t.Errorf("GetChildren empty = (%v, %v), want non-nil slice", children, err)
	}
	if children, err := d.GetDescendantsByPrefix(9293, "no-such-section"); err != nil || children == nil {
		t.Errorf("GetDescendantsByPrefix empty = (%v, %v), want non-nil slice", children, err)
	}
}

// TestVacuumInto verifies VacuumInto produces a compact, independently
// readable copy of the database.
func TestVacuumInto(t *testing.T) {
	d := setupTestDB(t)

	vacPath := filepath.Join(t.TempDir(), "vacuumed.db")
	if err := d.VacuumInto(vacPath); err != nil {
		t.Fatalf("VacuumInto: %v", err)
	}

	copy, err := Open(vacPath)
	if err != nil {
		t.Fatalf("Open vacuumed copy: %v", err)
	}
	defer copy.Close()

	result, err := copy.ListRFCs("", "", "", "", -1, 0)
	if err != nil {
		t.Fatalf("ListRFCs on vacuumed copy: %v", err)
	}
	// Seed data has 4 rfcs rows, one of which is not_issued and excluded.
	if len(result.RFCs) != 3 {
		t.Fatalf("expected 3 rfcs in vacuumed copy, got %d", len(result.RFCs))
	}

	results, err := copy.Search("TCP", nil, 10)
	if err != nil {
		t.Fatalf("Search on vacuumed copy: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected FTS index to survive VACUUM INTO")
	}
}

// TestWALCheckpointTruncate verifies the checkpoint helper cmdUpdate relies
// on before renaming the working copy over the live database: after a
// successful TRUNCATE checkpoint the WAL sidecar holds no unwritten frames
// (it is truncated to zero length), so the main file is self-contained. It
// also verifies errors are surfaced rather than swallowed (closed handle).
func TestWALCheckpointTruncate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := d.Exec("INSERT INTO rfcs (number, title) VALUES (1, 'seed')"); err != nil {
		t.Fatalf("Exec insert: %v", err)
	}

	if err := d.WALCheckpointTruncate(); err != nil {
		t.Fatalf("WALCheckpointTruncate: %v", err)
	}
	if info, statErr := os.Stat(dbPath + "-wal"); statErr == nil && info.Size() != 0 {
		t.Errorf("WAL sidecar not truncated after checkpoint: %d bytes", info.Size())
	}

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := d.WALCheckpointTruncate(); err == nil {
		t.Error("expected error from WALCheckpointTruncate on a closed handle")
	}
}

// TestWALCheckpointTruncate_Busy covers the busy branch: a concurrent reader
// holding an open read transaction pins the WAL, so a TRUNCATE checkpoint
// cannot complete and the pragma reports busy=1. WALCheckpointTruncate must
// surface that as an error (cmdUpdate relies on it to abort the atomic swap
// rather than rename a copy whose WAL still holds unwritten frames), and must
// succeed once the reader is gone.
func TestWALCheckpointTruncate_Busy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal.db")
	w, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer w.Close()
	if err := w.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := w.Exec("INSERT INTO rfcs (number, title) VALUES (1, 'seed')"); err != nil {
		t.Fatalf("Exec insert: %v", err)
	}

	r, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open reader: %v", err)
	}
	defer r.Close()
	tx, err := r.conn.Begin()
	if err != nil {
		t.Fatalf("Begin reader tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	// The read lock is only taken on the first read, so actually read.
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM rfcs").Scan(&n); err != nil {
		t.Fatalf("reader select: %v", err)
	}

	// Frames appended after the reader's mark can never be checkpointed while
	// its transaction stays open, guaranteeing busy=1 rather than depending on
	// TRUNCATE's stricter no-readers requirement alone.
	if err := w.Exec("INSERT INTO rfcs (number, title) VALUES (2, 'post-reader')"); err != nil {
		t.Fatalf("Exec insert past reader mark: %v", err)
	}
	// Shrink the writer's 5s busy_timeout so the blocked checkpoint gives up
	// quickly instead of stalling the test.
	if err := w.Exec("PRAGMA busy_timeout=100"); err != nil {
		t.Fatalf("Exec busy_timeout: %v", err)
	}

	if err := w.WALCheckpointTruncate(); err == nil {
		t.Fatal("expected busy error from WALCheckpointTruncate with an open reader")
	} else if !strings.Contains(err.Error(), "checkpoint blocked") {
		t.Errorf("error = %v, want mention of \"checkpoint blocked\"", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback reader tx: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close reader: %v", err)
	}
	if err := w.WALCheckpointTruncate(); err != nil {
		t.Errorf("WALCheckpointTruncate after reader closed: %v", err)
	}
}
