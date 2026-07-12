package db

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RFC holds full metadata for a single RFC document.
type RFC struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	Status      string   `json:"status,omitempty"`
	Stream      string   `json:"stream,omitempty"`
	Date        string   `json:"date,omitempty"`
	PageCount   int      `json:"page_count,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Abstract    string   `json:"abstract,omitempty"`
	Draft       string   `json:"draft,omitempty"`
	WG          string   `json:"wg,omitempty"`
	Area        string   `json:"area,omitempty"`
	DOI         string   `json:"doi,omitempty"`
	ErrataURL   string   `json:"errata_url,omitempty"`
	Obsoletes   []int    `json:"obsoletes,omitempty"`
	ObsoletedBy []int    `json:"obsoleted_by,omitempty"`
	Updates     []int    `json:"updates,omitempty"`
	UpdatedBy   []int    `json:"updated_by,omitempty"`
	Also        []string `json:"also,omitempty"`
	NotIssued   bool     `json:"not_issued,omitempty"`
	// HasText reports whether rfc-editor.org publishes a plain-text (.txt)
	// rendition of this RFC. False only for RFC 8, 9, 51, 418, 500, 530, and
	// 598 -- 1969-1973 documents whose rfc-index.xml <format> list omits
	// TXT -- so the pipeline can skip fetch attempts that would only 404.
	HasText bool `json:"has_text"`
}

// RFCSummary is a lightweight listing row returned by ListRFCs.
type RFCSummary struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	Stream string `json:"stream,omitempty"`
	Date   string `json:"date,omitempty"`
	WG     string `json:"wg,omitempty"`
}

// ListRFCsResult holds paginated results from ListRFCs.
type ListRFCsResult struct {
	RFCs       []RFCSummary `json:"rfcs"`
	TotalCount int          `json:"total_count"`
	Limit      int          `json:"limit"`
	Offset     int          `json:"offset"`
}

// marshalStrings and marshalInts serialize the rfcs table's JSON-array
// columns. Empty/nil slices are normalized to "[]" rather than "null" so
// unmarshalStrings/unmarshalInts can treat "" and "[]" interchangeably.
func marshalStrings(a []string) string {
	if len(a) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(a)
	return string(b)
}

func marshalInts(a []int) string {
	if len(a) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(a)
	return string(b)
}

func unmarshalStrings(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var a []string
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return nil
	}
	return a
}

func unmarshalInts(s string) []int {
	if s == "" || s == "[]" {
		return nil
	}
	var a []int
	if err := json.Unmarshal([]byte(s), &a); err != nil {
		return nil
	}
	return a
}

// UpsertRFC inserts or replaces an RFC metadata record.
func (d *DB) UpsertRFC(rfc RFC) error {
	_, err := d.conn.Exec(
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
	return err
}

// GetRFCMetadata retrieves full metadata for a single RFC by number.
func (d *DB) GetRFCMetadata(number int) (*RFC, error) {
	var rfc RFC
	var authors, keywords, obsoletes, obsoletedBy, updates, updatedBy, also string

	err := d.conn.QueryRow(
		`SELECT number, title, COALESCE(status, ''), COALESCE(stream, ''), COALESCE(date, ''),
			COALESCE(page_count, 0), COALESCE(authors, ''), COALESCE(keywords, ''),
			COALESCE(abstract, ''), COALESCE(draft, ''), COALESCE(wg, ''), COALESCE(area, ''),
			COALESCE(doi, ''), COALESCE(errata_url, ''), COALESCE(obsoletes, ''),
			COALESCE(obsoleted_by, ''), COALESCE(updates, ''), COALESCE(updated_by, ''),
			COALESCE(also, ''), not_issued, has_text
		FROM rfcs WHERE number = ?`,
		number,
	).Scan(
		&rfc.Number, &rfc.Title, &rfc.Status, &rfc.Stream, &rfc.Date, &rfc.PageCount,
		&authors, &keywords, &rfc.Abstract, &rfc.Draft, &rfc.WG, &rfc.Area,
		&rfc.DOI, &rfc.ErrataURL, &obsoletes, &obsoletedBy, &updates, &updatedBy,
		&also, &rfc.NotIssued, &rfc.HasText,
	)
	if err != nil {
		return nil, fmt.Errorf("get rfc metadata: %w", err)
	}

	rfc.Authors = unmarshalStrings(authors)
	rfc.Keywords = unmarshalStrings(keywords)
	rfc.Obsoletes = unmarshalInts(obsoletes)
	rfc.ObsoletedBy = unmarshalInts(obsoletedBy)
	rfc.Updates = unmarshalInts(updates)
	rfc.UpdatedBy = unmarshalInts(updatedBy)
	rfc.Also = unmarshalStrings(also)
	return &rfc, nil
}

// ListRFCs lists RFCs, optionally filtered by a title substring, stream,
// status, and working group. Documents marked not_issued (an RFC number that
// was allocated but never published) are always excluded.
func (d *DB) ListRFCs(query, stream, status, wg string, limit, offset int) (*ListRFCsResult, error) {
	if offset < 0 {
		offset = 0
	}
	conditions := []string{"not_issued = 0"}
	var args []any
	if query != "" {
		conditions = append(conditions, "title LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLikePattern(query)+"%")
	}
	if stream != "" {
		conditions = append(conditions, "stream = ?")
		args = append(args, stream)
	}
	if status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, status)
	}
	if wg != "" {
		conditions = append(conditions, "wg = ?")
		args = append(args, wg)
	}
	where := " WHERE " + strings.Join(conditions, " AND ")

	var totalCount int
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM rfcs"+where, args...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("count rfcs: %w", err)
	}

	sqlQuery := "SELECT number, title, COALESCE(status, ''), COALESCE(stream, ''), COALESCE(date, ''), COALESCE(wg, '') FROM rfcs" + where + " ORDER BY number"
	queryArgs := append([]any{}, args...)

	// limit == 0: use default; limit < 0: no limit (return all rows, internal use only).
	if limit == 0 {
		limit = DefaultListRFCsLimit
	}
	if limit > MaxListRFCsLimit {
		limit = MaxListRFCsLimit
	}
	if limit > 0 {
		sqlQuery += " LIMIT ? OFFSET ?"
		queryArgs = append(queryArgs, limit, offset)
	}

	rows, err := d.conn.Query(sqlQuery, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("list rfcs: %w", err)
	}
	defer rows.Close()

	var rfcs []RFCSummary
	for rows.Next() {
		var r RFCSummary
		if err := rows.Scan(&r.Number, &r.Title, &r.Status, &r.Stream, &r.Date, &r.WG); err != nil {
			return nil, fmt.Errorf("scan rfc: %w", err)
		}
		rfcs = append(rfcs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rfcs: iterate: %w", err)
	}
	return &ListRFCsResult{RFCs: rfcs, TotalCount: totalCount, Limit: limit, Offset: offset}, nil
}
