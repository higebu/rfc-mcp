package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LookupMeta looks up a value from the meta key/value table, used to record
// build provenance (built_at, rfc_index_fetched_at -- see
// ingest/pipeline.Pipeline.recordBuildMeta). ok is false with a nil error
// only when the key is genuinely absent: no row for the key, or the meta
// table itself doesn't exist yet (a database built before the table was
// introduced degrades gracefully instead of erroring). Any other failure --
// I/O error, corrupt database -- is returned as a non-nil error rather than
// being conflated with absence.
func (d *DB) LookupMeta(key string) (value string, ok bool, err error) {
	err = d.conn.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	switch {
	case err == nil:
		return value, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case strings.Contains(err.Error(), "no such table: meta"):
		// Pre-meta-table database: treat as absent, not an error. The match
		// names the meta table explicitly (modernc.org/sqlite reports
		// "SQL logic error: no such table: meta (1)") so an unrelated
		// missing-table error -- e.g. a view whose backing table was dropped
		// -- still propagates as a failure below.
		return "", false, nil
	default:
		return "", false, fmt.Errorf("get meta %s: %w", key, err)
	}
}

// GetMeta is a convenience wrapper around LookupMeta for callers that treat
// any failure as "absent" (e.g. best-effort provenance hints). It discards
// the error; use LookupMeta where errors must be distinguished from absence.
func (d *DB) GetMeta(key string) (value string, ok bool) {
	value, ok, _ = d.LookupMeta(key)
	return value, ok
}

// SetMeta inserts or updates a key/value pair in the meta table.
func (d *DB) SetMeta(key, value string) error {
	_, err := d.conn.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set meta %s: %w", key, err)
	}
	return nil
}
