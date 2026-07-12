package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// Section holds a single parsed section of an RFC's body text.
type Section struct {
	RFC          int    `json:"rfc"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	Level        int    `json:"level"`
	ParentNumber string `json:"parent_number,omitempty"`
	Content      string `json:"content,omitempty"`
}

// InsertRFCWithSections inserts an RFC's metadata and all of its sections in
// a single transaction.
func (d *DB) InsertRFCWithSections(rfc RFC, sections []Section) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	_, err = tx.Exec(
		`INSERT OR REPLACE INTO rfcs (
			number, title, status, stream, date, page_count, authors, keywords,
			abstract, draft, wg, area, doi, errata_url, obsoletes, obsoleted_by,
			updates, updated_by, also, not_issued, has_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rfc.Number, rfc.Title, rfc.Status, rfc.Stream, rfc.Date, rfc.PageCount,
		marshalStrings(rfc.Authors), marshalStrings(rfc.Keywords), rfc.Abstract,
		rfc.Draft, rfc.WG, rfc.Area, rfc.DOI, rfc.ErrataURL,
		marshalInts(rfc.Obsoletes), marshalInts(rfc.ObsoletedBy),
		marshalInts(rfc.Updates), marshalInts(rfc.UpdatedBy),
		marshalStrings(rfc.Also), rfc.NotIssued, rfc.HasText,
	)
	if err != nil {
		return fmt.Errorf("upsert rfc: %w", err)
	}

	// Use explicit DELETE + INSERT to ensure FTS5 triggers fire correctly.
	// INSERT OR REPLACE suppresses DELETE triggers unless recursive_triggers is
	// enabled, leaving stale FTS entries that cause "missing row" search errors.
	//
	// The delete covers every existing section for this RFC, not just the
	// numbers present in the new set: a re-parse can drop a section (e.g. a
	// heading the parser no longer recognizes), and a delete scoped to the
	// new set alone would leave that dropped section (and its FTS row)
	// behind indefinitely.
	if _, err = tx.Exec("DELETE FROM sections WHERE rfc = ?", rfc.Number); err != nil {
		return fmt.Errorf("delete existing sections: %w", err)
	}

	insStmt, err := tx.Prepare(
		"INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insStmt.Close()

	for _, s := range sections {
		if _, err = insStmt.Exec(s.RFC, s.Number, s.Title, s.Level, s.ParentNumber, s.Content); err != nil {
			return fmt.Errorf("insert section: %w", err)
		}
	}

	return tx.Commit()
}

// GetTOC returns every section of an RFC, ordered as inserted (i.e. document order).
func (d *DB) GetTOC(rfc int) ([]Section, error) {
	rows, err := d.conn.Query(
		"SELECT rfc, number, title, level, COALESCE(parent_number, '') FROM sections WHERE rfc = ? ORDER BY id",
		rfc,
	)
	if err != nil {
		return nil, fmt.Errorf("get toc: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.RFC, &s.Number, &s.Title, &s.Level, &s.ParentNumber); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get toc: iterate: %w", err)
	}
	return sections, nil
}

// ExistingRFCNumbers returns the sorted, distinct RFC numbers that have at
// least one parsed section stored. Used by the update command to diff a
// freshly fetched rfc-index.xml against what's already in the database, so
// only newly issued RFCs get their body fetched -- existing bodies are
// immutable once published and are never re-fetched.
func (d *DB) ExistingRFCNumbers() ([]int, error) {
	rows, err := d.conn.Query("SELECT DISTINCT rfc FROM sections ORDER BY rfc")
	if err != nil {
		return nil, fmt.Errorf("list existing rfc numbers: %w", err)
	}
	defer rows.Close()

	var numbers []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("scan rfc number: %w", err)
		}
		numbers = append(numbers, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list existing rfc numbers: iterate: %w", err)
	}
	return numbers, nil
}

// descendantsCTE recursively walks the parent_number chain (rather than a
// "number LIKE 'X.%'" prefix match) to find a section plus every transitive
// descendant. A prefix match misses subsections whose own number doesn't
// share the parent's textual prefix -- e.g. RFC 1's section "II" has
// slug-numbered children ("messages", "links", ...) whose ParentNumber is
// "II" but whose Number doesn't start with "II.". Binds: (root number, rfc)
// for the recursive step's scope. UNION (not UNION ALL) also makes the
// recursion safe against a cyclic parent_number chain, should one ever occur.
const descendantsCTE = `WITH RECURSIVE descendants(number) AS (
	SELECT ?
	UNION
	SELECT s.number FROM sections s JOIN descendants d ON s.parent_number = d.number WHERE s.rfc = ?
)
`

// GetSection returns a single section, or that section plus every
// descendant reached via the parent_number chain when includeSubsections is set.
func (d *DB) GetSection(rfc int, number string, includeSubsections bool) ([]Section, error) {
	var rows *sql.Rows
	var err error

	if includeSubsections {
		rows, err = d.conn.Query(
			descendantsCTE+"SELECT rfc, number, title, level, COALESCE(parent_number, ''), content FROM sections WHERE rfc = ? AND number IN (SELECT number FROM descendants) ORDER BY id",
			number, rfc, rfc,
		)
	} else {
		rows, err = d.conn.Query(
			"SELECT rfc, number, title, level, COALESCE(parent_number, ''), content FROM sections WHERE rfc = ? AND number = ?",
			rfc, number,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get section: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.RFC, &s.Number, &s.Title, &s.Level, &s.ParentNumber, &s.Content); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get section: iterate: %w", err)
	}
	return sections, nil
}

// SectionHeading formats the reconstructed "<number>.  <title>" heading
// line used to prefix a section's content wherever it's assembled into a
// larger response (get_document, get_section) -- section content itself
// never includes its own heading (see rfctxt.Section's doc comment).
// Returns "" for the synthetic "header" section and the Tier-3 whole-body
// fallback ("body"), which have no real heading to reconstruct.
func SectionHeading(number, title string) string {
	if number == "header" || number == "body" {
		return ""
	}
	return fmt.Sprintf("%s.  %s\n\n", number, title)
}

// GetDocument returns the full text of an RFC as one continuous document:
// every section's content in document order (sections.id order, same as
// GetTOC), each preceded by its SectionHeading. Returns "" if the RFC has
// no parsed sections.
func (d *DB) GetDocument(rfc int) (string, error) {
	rows, err := d.conn.Query(
		"SELECT number, title, content FROM sections WHERE rfc = ? ORDER BY id",
		rfc,
	)
	if err != nil {
		return "", fmt.Errorf("get document: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var number, title, content string
		if err := rows.Scan(&number, &title, &content); err != nil {
			return "", fmt.Errorf("get document: scan: %w", err)
		}
		sb.WriteString(SectionHeading(number, title))
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("get document: iterate: %w", err)
	}
	return sb.String(), nil
}

// SectionChild is a lightweight (number, title) pair describing one direct
// child of a section, without fetching its content.
type SectionChild struct {
	Number string
	Title  string
}

// GetChildren returns the direct children of section number (rows whose
// parent_number equals it), in document order. Used by get_section to
// offer guidance when a title-only parent section -- empty Content, body
// text living entirely in its children -- is fetched without
// include_subsections.
func (d *DB) GetChildren(rfc int, number string) ([]SectionChild, error) {
	rows, err := d.conn.Query(
		"SELECT number, title FROM sections WHERE rfc = ? AND parent_number = ? ORDER BY id",
		rfc, number,
	)
	if err != nil {
		return nil, fmt.Errorf("get children: %w", err)
	}
	defer rows.Close()

	var children []SectionChild
	for rows.Next() {
		var c SectionChild
		if err := rows.Scan(&c.Number, &c.Title); err != nil {
			return nil, fmt.Errorf("get children: scan: %w", err)
		}
		children = append(children, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get children: iterate: %w", err)
	}
	return children, nil
}

// likeEscaper escapes a LIKE pattern's special characters ('%', '_')
// plus the escape character itself, so GetDescendantsByPrefix can match
// number literally up to the appended wildcard.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// GetDescendantsByPrefix returns sections one level under number by
// number-prefix ("number.X", not "number.X.Y") rather than parent_number,
// in document order. Used by get_section as a fallback when a queried
// section number has no heading of its own AND nothing's parent_number
// points to it either -- ingest/rfctxt's reparentDanglingAncestors walks
// a heading past a missing intermediate ancestor up to the nearest one
// that actually exists, so a genuinely absent number like RFC 892's
// "7.0" no longer has any child pointing at it via parent_number even
// though "7.0.1" etc. still exist. A prefix match is normally unreliable
// for descendant lookups in this codebase (see descendantsCTE's doc
// comment on slug-numbered children), but it's the only signal left once
// the parent_number chain has been intentionally rerouted around a
// missing number.
func (d *DB) GetDescendantsByPrefix(rfc int, number string) ([]SectionChild, error) {
	prefix := likeEscaper.Replace(number) + "."
	rows, err := d.conn.Query(
		`SELECT number, title FROM sections
		 WHERE rfc = ? AND number LIKE ? ESCAPE '\' AND number NOT LIKE ? ESCAPE '\'
		 ORDER BY id`,
		rfc, prefix+"%", prefix+"%.%",
	)
	if err != nil {
		return nil, fmt.Errorf("get descendants by prefix: %w", err)
	}
	defer rows.Close()

	var children []SectionChild
	for rows.Next() {
		var c SectionChild
		if err := rows.Scan(&c.Number, &c.Title); err != nil {
			return nil, fmt.Errorf("get descendants by prefix: scan: %w", err)
		}
		children = append(children, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get descendants by prefix: iterate: %w", err)
	}
	return children, nil
}
