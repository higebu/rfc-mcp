// Package rfcindex parses the RFC Editor's rfc-index.xml into db.RFC records.
package rfcindex

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/higebu/rfc-mcp/db"
)

// docIDRE extracts the numeric part of an RFC doc-id (e.g. "RFC4271" -> "4271").
// is-also doc-ids can also be STD/BCP/FYI numbers, which are kept as-is in
// db.RFC.Also rather than parsed here.
var docIDRE = regexp.MustCompile(`^RFC(\d+)$`)

// monthNumbers maps the full English month names used in rfc-index.xml dates
// to their two-digit numeric form.
var monthNumbers = map[string]string{
	"January": "01", "February": "02", "March": "03", "April": "04",
	"May": "05", "June": "06", "July": "07", "August": "08",
	"September": "09", "October": "10", "November": "11", "December": "12",
}

// rfcEntry mirrors the <rfc-entry> element of rfc-index.xml. Only the
// fields db.RFC needs are decoded; std-entry/bcp-entry/fyi-entry siblings
// are unmatched by Parse's token loop and fall through untouched.
type rfcEntry struct {
	DocID         string    `xml:"doc-id"`
	Title         string    `xml:"title"`
	Authors       []author  `xml:"author"`
	Date          entryDate `xml:"date"`
	PageCount     int       `xml:"page-count"`
	Keywords      []string  `xml:"keywords>kw"`
	Abstract      []string  `xml:"abstract>p"`
	Draft         string    `xml:"draft"`
	IsAlso        []string  `xml:"is-also>doc-id"`
	Obsoletes     []string  `xml:"obsoletes>doc-id"`
	ObsoletedBy   []string  `xml:"obsoleted-by>doc-id"`
	Updates       []string  `xml:"updates>doc-id"`
	UpdatedBy     []string  `xml:"updated-by>doc-id"`
	Formats       []string  `xml:"format>file-format"`
	CurrentStatus string    `xml:"current-status"`
	Stream        string    `xml:"stream"`
	Area          string    `xml:"area"`
	WGAcronym     string    `xml:"wg_acronym"`
	ErrataURL     string    `xml:"errata-url"`
	DOI           string    `xml:"doi"`
}

type author struct {
	Name string `xml:"name"`
}

type entryDate struct {
	Month string `xml:"month"`
	Day   string `xml:"day"`
	Year  string `xml:"year"`
}

type notIssuedEntry struct {
	DocID string `xml:"doc-id"`
}

// Parse reads rfc-index.xml and returns one db.RFC per rfc-entry and
// rfc-not-issued-entry. std-entry/bcp-entry/fyi-entry are discarded.
//
// It streams the document with an xml.Decoder token loop rather than
// unmarshaling the whole 13+ MB file into memory at once. A malformed
// individual entry is logged and skipped rather than aborting the parse;
// the returned error, built with errors.Join, is nil unless at least one
// entry was skipped or the underlying XML stream itself was truncated.
func Parse(r io.Reader) ([]db.RFC, error) {
	dec := xml.NewDecoder(r)
	var rfcs []db.RFC
	var errs []error

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("rfcindex: decode token: %w", err))
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}

		switch se.Name.Local {
		case "rfc-entry":
			var e rfcEntry
			if err := dec.DecodeElement(&e, &se); err != nil {
				errs = append(errs, fmt.Errorf("rfcindex: decode rfc-entry: %w", err))
				continue
			}
			rfc, err := convertEntry(e)
			if err != nil {
				log.Printf("rfcindex: skipping entry %q: %v", e.DocID, err)
				errs = append(errs, err)
				continue
			}
			rfcs = append(rfcs, rfc)
		case "rfc-not-issued-entry":
			var e notIssuedEntry
			if err := dec.DecodeElement(&e, &se); err != nil {
				errs = append(errs, fmt.Errorf("rfcindex: decode rfc-not-issued-entry: %w", err))
				continue
			}
			num, ok := parseRFCNumber(e.DocID)
			if !ok {
				log.Printf("rfcindex: skipping not-issued entry %q: not an RFC doc-id", e.DocID)
				errs = append(errs, fmt.Errorf("rfcindex: invalid not-issued doc-id %q", e.DocID))
				continue
			}
			rfcs = append(rfcs, db.RFC{Number: num, NotIssued: true})
		}
	}

	return rfcs, errors.Join(errs...)
}

// convertEntry converts a parsed rfc-entry into a db.RFC.
func convertEntry(e rfcEntry) (db.RFC, error) {
	num, ok := parseRFCNumber(e.DocID)
	if !ok {
		return db.RFC{}, fmt.Errorf("rfcindex: invalid doc-id %q", e.DocID)
	}

	var authors []string
	for _, a := range e.Authors {
		if a.Name != "" {
			authors = append(authors, a.Name)
		}
	}

	date, err := formatDate(e.Date)
	if err != nil {
		log.Printf("rfcindex: RFC%d: %v", num, err)
	}

	return db.RFC{
		Number:      num,
		Title:       e.Title,
		Status:      e.CurrentStatus,
		Stream:      e.Stream,
		Date:        date,
		PageCount:   e.PageCount,
		Authors:     authors,
		Keywords:    e.Keywords,
		Abstract:    strings.Join(e.Abstract, "\n\n"),
		Draft:       e.Draft,
		WG:          e.WGAcronym,
		Area:        e.Area,
		DOI:         e.DOI,
		ErrataURL:   e.ErrataURL,
		Obsoletes:   parseRFCNumbers(e.Obsoletes),
		ObsoletedBy: parseRFCNumbers(e.ObsoletedBy),
		Updates:     parseRFCNumbers(e.Updates),
		UpdatedBy:   parseRFCNumbers(e.UpdatedBy),
		Also:        e.IsAlso,
		// A handful of 1969-1973 RFCs (8, 9, 51, 418, 500, 530, 598) were
		// only ever distributed as scanned images or on paper: their
		// <format> list has no TXT, and rfc-editor.org has no .txt for
		// them to fetch.
		HasText: slices.Contains(e.Formats, "TXT"),
	}, nil
}

// parseRFCNumber extracts the numeric RFC number from a doc-id like "RFC4271".
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

// parseRFCNumbers converts a list of RFC doc-ids to their numbers, silently
// dropping any that aren't RFC-prefixed (rfc-index.xml has none in practice
// for obsoletes/updates/obsoleted-by/updated-by, but is-also can carry
// STD/BCP/FYI doc-ids, which callers keep as raw strings instead).
func parseRFCNumbers(docIDs []string) []int {
	var nums []int
	for _, id := range docIDs {
		if n, ok := parseRFCNumber(id); ok {
			nums = append(nums, n)
		}
	}
	return nums
}

// formatDate renders an entryDate as "YYYY-MM", or "YYYY-MM-DD" when the
// entry carries a day (71 entries in the live index do, mostly April 1st
// jokes such as RFC 1149).
func formatDate(d entryDate) (string, error) {
	mm, ok := monthNumbers[d.Month]
	if !ok {
		return "", fmt.Errorf("unknown month %q", d.Month)
	}
	if d.Day == "" {
		return fmt.Sprintf("%s-%s", d.Year, mm), nil
	}
	day, err := strconv.Atoi(d.Day)
	if err != nil {
		return fmt.Sprintf("%s-%s", d.Year, mm), fmt.Errorf("invalid day %q", d.Day)
	}
	return fmt.Sprintf("%s-%s-%02d", d.Year, mm, day), nil
}

// TitleCaseStatus renders an rfc-index.xml current-status value such as
// "DRAFT STANDARD" in title case ("Draft Standard"), for display by the
// get_metadata MCP tool. The database stores current-status verbatim;
// this is purely a presentation helper.
func TitleCaseStatus(s string) string {
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
