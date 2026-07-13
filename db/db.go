// Package db implements the SQLite storage layer for rfc-mcp: RFC metadata,
// section text, full-text search, cross-references, and errata.
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

// Open opens a database in read-only mode. Intended for the MCP server,
// which never writes to the database it serves.
func Open(path string) (*DB, error) {
	// The "file:" URI scheme prefix is required for modernc.org/sqlite to
	// honor the mode=ro query parameter; without it, mode=ro is silently
	// ignored and the connection remains writable.
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Limit open connections to avoid resource exhaustion under concurrent reads.
	conn.SetMaxOpenConns(4)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{conn: conn}, nil
}

// OpenReadWrite opens a database in read-write mode. Intended for ingestion
// tooling and tests.
func OpenReadWrite(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Limit to a single connection to serialize writes and avoid SQLITE_BUSY.
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("warning: failed to set WAL mode: %v", err)
	}
	if _, err := conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		log.Printf("warning: failed to set busy_timeout: %v", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{conn: conn}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// Exec executes a SQL statement on the database. Intended for testing.
func (d *DB) Exec(query string, args ...any) error {
	_, err := d.conn.Exec(query, args...)
	return err
}

// ExecScript executes multiple SQL statements. Intended for testing.
func (d *DB) ExecScript(script string) error {
	_, err := d.conn.Exec(script)
	return err
}

// VacuumInto creates a compact, consistent copy of the database at path.
func (d *DB) VacuumInto(path string) error {
	_, err := d.conn.Exec("VACUUM INTO ?", path)
	return err
}

// DefaultSearchLimit is the default number of results returned by Search.
const DefaultSearchLimit = 10

// MaxSearchLimit is the upper bound for search results.
const MaxSearchLimit = 200

// DefaultListRFCsLimit is the default number of RFCs returned by ListRFCs.
const DefaultListRFCsLimit = 20

// MaxListRFCsLimit is the upper bound for ListRFCs results.
const MaxListRFCsLimit = 1000

// Schema is the SQL schema for the RFC database.
const Schema = `
CREATE TABLE IF NOT EXISTS rfcs (
    number INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    status TEXT,
    stream TEXT,
    date TEXT,
    page_count INTEGER,
    authors TEXT,
    keywords TEXT,
    abstract TEXT,
    draft TEXT,
    wg TEXT,
    area TEXT,
    doi TEXT,
    errata_url TEXT,
    obsoletes TEXT,
    obsoleted_by TEXT,
    updates TEXT,
    updated_by TEXT,
    also TEXT,
    not_issued BOOLEAN NOT NULL DEFAULT 0,
    has_text BOOLEAN NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rfc INTEGER NOT NULL REFERENCES rfcs(number),
    number TEXT NOT NULL,
    title TEXT NOT NULL,
    level INTEGER NOT NULL,
    parent_number TEXT,
    content TEXT NOT NULL,
    UNIQUE(rfc, number)
);

CREATE INDEX IF NOT EXISTS idx_sections_rfc ON sections(rfc);
CREATE INDEX IF NOT EXISTS idx_sections_number ON sections(rfc, number);

CREATE VIRTUAL TABLE IF NOT EXISTS sections_fts USING fts5(
    rfc UNINDEXED, number, title, content,
    content=sections,
    content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS sections_ai AFTER INSERT ON sections BEGIN
    INSERT INTO sections_fts(rowid, rfc, number, title, content)
    VALUES (new.id, new.rfc, new.number, new.title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS sections_ad AFTER DELETE ON sections BEGIN
    INSERT INTO sections_fts(sections_fts, rowid, rfc, number, title, content)
    VALUES ('delete', old.id, old.rfc, old.number, old.title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS sections_au AFTER UPDATE ON sections BEGIN
    INSERT INTO sections_fts(sections_fts, rowid, rfc, number, title, content)
    VALUES ('delete', old.id, old.rfc, old.number, old.title, old.content);
    INSERT INTO sections_fts(rowid, rfc, number, title, content)
    VALUES (new.id, new.rfc, new.number, new.title, new.content);
END;

CREATE TABLE IF NOT EXISTS rfc_references (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_rfc INTEGER NOT NULL,
    source_section TEXT NOT NULL,
    target_rfc INTEGER NOT NULL,
    target_section TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL,
    UNIQUE(source_rfc, source_section, target_rfc, target_section)
);

CREATE INDEX IF NOT EXISTS idx_ref_source ON rfc_references(source_rfc, source_section);
CREATE INDEX IF NOT EXISTS idx_ref_target ON rfc_references(target_rfc);

CREATE TABLE IF NOT EXISTS errata (
    id INTEGER PRIMARY KEY,
    rfc INTEGER NOT NULL,
    status TEXT,
    type TEXT,
    section TEXT,
    orig_text TEXT,
    correct_text TEXT,
    notes TEXT,
    submitted_date TEXT,
    submitter_name TEXT,
    verifier_name TEXT,
    updated_date TEXT
);

CREATE INDEX IF NOT EXISTS idx_errata_rfc ON errata(rfc);

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// InitSchema creates the database tables, indexes, and triggers.
func (d *DB) InitSchema() error {
	_, err := d.conn.Exec(Schema)
	return err
}

// escapeLikePattern escapes SQLite LIKE wildcards (% and _) in a
// user-supplied string so it can be used as a literal substring with an
// ESCAPE '\' clause.
func escapeLikePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}
