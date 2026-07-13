package db

import "fmt"

// GetMeta looks up a value from the meta key/value table, used to record
// build provenance (built_at, rfc_index_fetched_at -- see
// ingest/pipeline.Pipeline.recordBuildMeta). ok is false if the key is
// absent, including when the meta table itself doesn't exist yet, so a
// database built before this table was introduced degrades gracefully
// instead of erroring.
func (d *DB) GetMeta(key string) (value string, ok bool) {
	err := d.conn.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	return value, err == nil
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
