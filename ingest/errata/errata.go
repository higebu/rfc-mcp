// Package errata parses the RFC Editor's errata.json into db.Errata records.
package errata

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strconv"

	"github.com/higebu/rfc-mcp/db"
)

// docIDRE extracts the numeric part of an errata doc-id (e.g. "RFC4954" -> "4954").
var docIDRE = regexp.MustCompile(`^RFC(\d+)$`)

// entry mirrors one element of the errata.json array. verifier_name and
// update_date are nullable in the live data, hence the pointer fields.
type entry struct {
	ErrataID      string  `json:"errata_id"`
	DocID         string  `json:"doc-id"`
	StatusCode    string  `json:"errata_status_code"`
	TypeCode      string  `json:"errata_type_code"`
	Section       string  `json:"section"`
	OrigText      string  `json:"orig_text"`
	CorrectText   string  `json:"correct_text"`
	Notes         string  `json:"notes"`
	SubmitDate    string  `json:"submit_date"`
	SubmitterName string  `json:"submitter_name"`
	VerifierName  *string `json:"verifier_name"`
	UpdateDate    *string `json:"update_date"`
}

// Parse reads errata.json (a top-level JSON array) and returns one
// db.Errata per entry.
//
// It streams the array with a json.Decoder rather than unmarshaling the
// whole 11+ MB file into a single slice-of-structs. An entry whose doc-id
// isn't an RFC (BCP/STD errata, none of which exist today but the schema
// allows it) or whose errata_id doesn't parse as an integer is logged and
// skipped rather than aborting the parse; the returned error, built with
// errors.Join, is nil unless at least one entry was skipped or the JSON
// stream itself was malformed.
func Parse(r io.Reader) ([]db.Errata, error) {
	dec := json.NewDecoder(r)

	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("errata: read opening token: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, fmt.Errorf("errata: expected a JSON array, got %v", tok)
	}

	var items []db.Errata
	var errs []error
	for dec.More() {
		var e entry
		if err := dec.Decode(&e); err != nil {
			errs = append(errs, fmt.Errorf("errata: decode entry: %w", err))
			break // the decoder's position is unreliable after a decode error
		}
		item, err := convertEntry(e)
		if err != nil {
			log.Printf("errata: skipping entry %q: %v", e.ErrataID, err)
			errs = append(errs, err)
			continue
		}
		items = append(items, item)
	}

	return items, errors.Join(errs...)
}

// convertEntry converts a parsed JSON entry into a db.Errata.
func convertEntry(e entry) (db.Errata, error) {
	rfcNum, ok := parseRFCNumber(e.DocID)
	if !ok {
		return db.Errata{}, fmt.Errorf("errata: non-RFC doc-id %q", e.DocID)
	}
	id, err := strconv.Atoi(e.ErrataID)
	if err != nil {
		return db.Errata{}, fmt.Errorf("errata: invalid errata_id %q: %w", e.ErrataID, err)
	}
	return db.Errata{
		ID:            id,
		RFC:           rfcNum,
		Status:        e.StatusCode,
		Type:          e.TypeCode,
		Section:       e.Section,
		OrigText:      e.OrigText,
		CorrectText:   e.CorrectText,
		Notes:         e.Notes,
		SubmittedDate: e.SubmitDate,
		SubmitterName: e.SubmitterName,
		VerifierName:  derefString(e.VerifierName),
		UpdatedDate:   derefString(e.UpdateDate),
	}, nil
}

// parseRFCNumber extracts the numeric RFC number from a doc-id like "RFC4954".
func parseRFCNumber(docID string) (int, bool) {
	m := docIDRE.FindStringSubmatch(docID)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
