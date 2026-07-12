package db

import "fmt"

// Errata holds a single reported erratum for an RFC.
type Errata struct {
	ID            int    `json:"id"`
	RFC           int    `json:"rfc"`
	Status        string `json:"status,omitempty"`
	Type          string `json:"type,omitempty"`
	Section       string `json:"section,omitempty"`
	OrigText      string `json:"orig_text,omitempty"`
	CorrectText   string `json:"correct_text,omitempty"`
	Notes         string `json:"notes,omitempty"`
	SubmittedDate string `json:"submitted_date,omitempty"`
	SubmitterName string `json:"submitter_name,omitempty"`
	VerifierName  string `json:"verifier_name,omitempty"`
	UpdatedDate   string `json:"updated_date,omitempty"`
}

// ReplaceAllErrata wholesale-replaces the errata table: it deletes every
// existing row and bulk-inserts items in a single transaction. errata.json is
// republished in full on every IETF update, so an incremental upsert would
// leave stale rows behind once an erratum is withdrawn.
func (d *DB) ReplaceAllErrata(items []Errata) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	if _, err := tx.Exec("DELETE FROM errata"); err != nil {
		return fmt.Errorf("delete errata: %w", err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO errata (
			id, rfc, status, type, section, orig_text, correct_text, notes,
			submitted_date, submitter_name, verifier_name, updated_date
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare errata insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range items {
		_, err := stmt.Exec(
			e.ID, e.RFC, e.Status, e.Type, e.Section, e.OrigText, e.CorrectText,
			e.Notes, e.SubmittedDate, e.SubmitterName, e.VerifierName, e.UpdatedDate,
		)
		if err != nil {
			return fmt.Errorf("insert errata: %w", err)
		}
	}
	return tx.Commit()
}

// GetErrataByRFC retrieves all errata reported against an RFC, ordered by id.
func (d *DB) GetErrataByRFC(rfc int) ([]Errata, error) {
	rows, err := d.conn.Query(
		`SELECT id, rfc, COALESCE(status, ''), COALESCE(type, ''), COALESCE(section, ''),
			COALESCE(orig_text, ''), COALESCE(correct_text, ''), COALESCE(notes, ''),
			COALESCE(submitted_date, ''), COALESCE(submitter_name, ''),
			COALESCE(verifier_name, ''), COALESCE(updated_date, '')
		FROM errata WHERE rfc = ? ORDER BY id`,
		rfc,
	)
	if err != nil {
		return nil, fmt.Errorf("get errata: %w", err)
	}
	defer rows.Close()

	var items []Errata
	for rows.Next() {
		var e Errata
		if err := rows.Scan(
			&e.ID, &e.RFC, &e.Status, &e.Type, &e.Section, &e.OrigText, &e.CorrectText,
			&e.Notes, &e.SubmittedDate, &e.SubmitterName, &e.VerifierName, &e.UpdatedDate,
		); err != nil {
			return nil, fmt.Errorf("scan errata: %w", err)
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get errata: iterate: %w", err)
	}
	return items, nil
}
