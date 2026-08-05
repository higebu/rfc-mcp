package db

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Reference represents a cross-reference from one RFC section to another RFC.
type Reference struct {
	SourceRFC     int    `json:"source_rfc"`
	SourceSection string `json:"source_section"`
	TargetRFC     int    `json:"target_rfc"`
	TargetSection string `json:"target_section,omitempty"`
	TargetTitle   string `json:"target_title,omitempty"`
	Context       string `json:"context"`
}

// rfcSecNum matches an RFC section or appendix number: digit-led (4.2,
// 3.8.6.2.1) or letter-led for lettered appendices (A, A.2), mirroring
// 3gpp-mcp's secNum handling of TS/TR annexes.
const rfcSecNum = `(\d+(?:\.\d+)*|[A-Z](?:\.\d+)*)`

// Compiled regex patterns for extracting cross-references to other RFCs from
// section content. In ExtractReferences they are applied most-specific
// first, so a match with a section number claims its text span before a more
// general pattern can re-derive a less specific reference from the same span.
var (
	// rfcRefRE matches "RFC 793", "RFC793", "RFC-793", "IETF RFC 3327", with
	// an optional trailing "Section N.N" / "clause N.N" (e.g. "RFC 3261
	// section 10.2"). Ported from 3gpp-mcp db/db.go's rfcRefRE.
	rfcRefRE = regexp.MustCompile(`(?:IETF\s+)?RFC[\s-]?(\d+)(?:\s*[,;]?\s*(?:[Ss]ection|clause)\s+` + rfcSecNum + `)?`)

	// rfcPrefixRefRE matches "Section 4.2 of RFC 793" (section precedes the
	// RFC number).
	rfcPrefixRefRE = regexp.MustCompile(`(?:[Ss]ection|clause)\s+` + rfcSecNum + `\s+of\s+(?:IETF\s+)?RFC[\s-]?(\d+)`)

	// rfcBracketDirectRE matches the older bracketed-citation style used by
	// e.g. RFC 1035, "[RFC-793]" or "[RFC793]" -- the RFC number is inline,
	// not indirected through a numeric bracket map.
	rfcBracketDirectRE = regexp.MustCompile(`\[RFC[\s-]?(\d+)\]`)

	// bracketMapRE extracts "[16] ... RFC 793 ..." -> {16: 793} mappings from
	// a References section. The bounded, non-greedy gap keeps the match
	// inside a single reference-list entry.
	bracketMapRE = regexp.MustCompile(`\[(\d+)\][^[]{0,200}?RFC\s+(\d+)`)

	// bracketRefRE matches inline numeric-bracket citations like "[16]" in
	// body text, with an optional trailing section suffix as in "[16],
	// Section 4.2". Unlike 3gpp-mcp's equivalent, the suffix is optional:
	// RFCs are commonly cited as a bare bracket with no following
	// section/clause keyword.
	bracketRefRE = regexp.MustCompile(`\[(\d+)\](?:\s*,?\s*(?:[Ss]ection|clause)\s+` + rfcSecNum + `)?`)
)

// ParseBracketedRefMap builds a numeric-bracket -> RFC number map (e.g. "16"
// -> 793) from a References section, so inline "[16]"-style citations
// elsewhere in the document can be resolved to a target RFC. Returns nil if
// no mappings are found.
func ParseBracketedRefMap(referencesContent string) map[string]int {
	matches := bracketMapRE.FindAllStringSubmatch(referencesContent, -1)
	if len(matches) == 0 {
		return nil
	}
	m := make(map[string]int, len(matches))
	for _, match := range matches {
		targetRFC, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		m[match[1]] = targetRFC
	}
	return m
}

// ExtractReferences scans section content for cross-references to other RFCs
// and returns one Reference per distinct (target RFC, target section) pair.
// Self-references (targetRFC == sourceRFC) are excluded. bracketMap resolves
// inline "[16]"-style numeric citations -- build it with ParseBracketedRefMap
// from the source RFC's References section content; pass nil to skip that
// pattern.
func ExtractReferences(sourceRFC int, sectionNumber, content string, bracketMap map[string]int) []Reference {
	seen := make(map[string]bool)
	var claimed [][2]int
	var refs []Reference

	// add records a candidate match unless its span overlaps one already
	// claimed by an earlier, more specific pattern -- this stops e.g.
	// "Section 4.2 of RFC 793" also being counted as a bare, sectionless
	// "RFC 793" citation when rfcRefRE independently re-scans the same text.
	add := func(start, end, targetRFC int, targetSection string) {
		for _, c := range claimed {
			if start < c[1] && c[0] < end {
				return
			}
		}
		claimed = append(claimed, [2]int{start, end})

		if targetRFC == sourceRFC {
			return
		}
		key := fmt.Sprintf("%d#%s", targetRFC, targetSection)
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, Reference{
			SourceRFC:     sourceRFC,
			SourceSection: sectionNumber,
			TargetRFC:     targetRFC,
			TargetSection: targetSection,
			Context:       extractContext(content, start, end),
		})
	}

	for _, m := range rfcPrefixRefRE.FindAllStringSubmatchIndex(content, -1) {
		targetRFC, err := strconv.Atoi(content[m[4]:m[5]])
		if err != nil {
			continue
		}
		add(m[0], m[1], targetRFC, content[m[2]:m[3]])
	}

	for _, m := range rfcRefRE.FindAllStringSubmatchIndex(content, -1) {
		targetRFC, err := strconv.Atoi(content[m[2]:m[3]])
		if err != nil {
			continue
		}
		var section string
		if m[4] >= 0 {
			section = content[m[4]:m[5]]
		}
		add(m[0], m[1], targetRFC, section)
	}

	for _, m := range rfcBracketDirectRE.FindAllStringSubmatchIndex(content, -1) {
		targetRFC, err := strconv.Atoi(content[m[2]:m[3]])
		if err != nil {
			continue
		}
		add(m[0], m[1], targetRFC, "")
	}

	if bracketMap != nil {
		for _, m := range bracketRefRE.FindAllStringSubmatchIndex(content, -1) {
			targetRFC, ok := bracketMap[content[m[2]:m[3]]]
			if !ok {
				continue
			}
			var section string
			if m[4] >= 0 {
				section = content[m[4]:m[5]]
			}
			add(m[0], m[1], targetRFC, section)
		}
	}

	return refs
}

// InsertReferences bulk-inserts reference rows. Before inserting, every
// existing row belonging to a source RFC present in refs is deleted within
// the same transaction (mirroring InsertRFCWithSections' delete-then-insert
// strategy for sections), so a re-import that yields fewer references leaves
// no stale rows behind.
//
// An empty refs set is a no-op: with no rows there is no source RFC to scope
// a delete to. A re-parse that finds no references at all must instead clear
// the old rows explicitly via DeleteReferencesForRFC.
func (d *DB) InsertReferences(refs []Reference) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	// Delete existing rows for each distinct source RFC being re-inserted,
	// in first-seen order for determinism.
	seen := make(map[int]bool)
	for _, r := range refs {
		if seen[r.SourceRFC] {
			continue
		}
		seen[r.SourceRFC] = true
		if _, err := tx.Exec("DELETE FROM rfc_references WHERE source_rfc = ?", r.SourceRFC); err != nil {
			return fmt.Errorf("delete existing references for rfc %d: %w", r.SourceRFC, err)
		}
	}

	stmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO rfc_references (source_rfc, source_section, target_rfc, target_section, context) VALUES (?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare reference insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range refs {
		if _, err := stmt.Exec(r.SourceRFC, r.SourceSection, r.TargetRFC, r.TargetSection, r.Context); err != nil {
			return fmt.Errorf("insert reference: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteReferencesForRFC removes every stored reference originating from
// sourceRFC. Intended for re-imports whose re-parse yields no references at
// all: InsertReferences with an empty set is a no-op (it has no source RFC
// to scope a delete to), so old rows must be cleared explicitly.
func (d *DB) DeleteReferencesForRFC(sourceRFC int) error {
	if _, err := d.conn.Exec("DELETE FROM rfc_references WHERE source_rfc = ?", sourceRFC); err != nil {
		return fmt.Errorf("delete references for rfc %d: %w", sourceRFC, err)
	}
	return nil
}

// extractContext returns a snippet of content around the match [start, end),
// snapping to word boundaries to avoid splitting words or multi-byte characters.
func extractContext(content string, start, end int) string {
	const window = 50
	ctxStart := max(start-window, 0)
	if ctxStart > 0 {
		if idx := strings.IndexByte(content[ctxStart:start], ' '); idx >= 0 {
			ctxStart += idx + 1
		}
	}

	ctxEnd := min(end+window, len(content))
	if ctxEnd < len(content) {
		if idx := strings.LastIndexByte(content[end:ctxEnd], ' '); idx >= 0 {
			ctxEnd = end + idx
		}
	}

	var b strings.Builder
	if ctxStart > 0 {
		b.WriteString("...")
	}
	b.WriteString(content[ctxStart:ctxEnd])
	if ctxEnd < len(content) {
		b.WriteString("...")
	}
	return b.String()
}

// Direction constants for GetReferences.
const (
	DirectionOutgoing = "outgoing"
	DirectionIncoming = "incoming"
)

const refBaseQuery = `SELECT r.source_rfc, r.source_section, r.target_rfc, r.target_section, r.context,
	COALESCE(s.title, '')
	FROM rfc_references r
	LEFT JOIN sections s ON r.target_rfc = s.rfc AND r.target_section = s.number`

// GetReferences retrieves cross-references in the given direction.
//
// Subsection expansion (includeSubsections for outgoing; always-on for
// incoming when sectionNumber is given) walks the parent_number chain via
// descendantsCTE (db/sections.go) rather than a "section LIKE 'X.%'" prefix
// match, so it also picks up slug-numbered children whose own number
// doesn't share the parent's textual prefix.
func (d *DB) GetReferences(rfc int, sectionNumber, direction string, includeSubsections bool) ([]Reference, error) {
	if direction == "" {
		direction = DirectionOutgoing
	}

	var query string
	var args []any
	switch direction {
	case DirectionOutgoing:
		if includeSubsections {
			query = descendantsCTE + refBaseQuery + `
				WHERE r.source_rfc = ? AND r.source_section IN (SELECT number FROM descendants)
				ORDER BY r.source_section, r.target_rfc, r.target_section`
			args = []any{sectionNumber, rfc, rfc}
		} else {
			query = refBaseQuery + `
				WHERE r.source_rfc = ? AND r.source_section = ?
				ORDER BY r.target_rfc, r.target_section`
			args = []any{rfc, sectionNumber}
		}
	case DirectionIncoming:
		if sectionNumber != "" {
			query = descendantsCTE + refBaseQuery + `
				WHERE r.target_rfc = ? AND (r.target_section IN (SELECT number FROM descendants) OR r.target_section = '')
				ORDER BY r.source_rfc, r.source_section`
			args = []any{sectionNumber, rfc, rfc}
		} else {
			query = refBaseQuery + `
				WHERE r.target_rfc = ?
				ORDER BY r.source_rfc, r.source_section`
			args = []any{rfc}
		}
	default:
		return nil, fmt.Errorf("invalid direction %q: must be %s or %s", direction, DirectionOutgoing, DirectionIncoming)
	}

	return d.queryReferences(query, args)
}

func (d *DB) queryReferences(query string, args []any) ([]Reference, error) {
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query references: %w", err)
	}
	defer rows.Close()

	var refs []Reference
	for rows.Next() {
		var r Reference
		if err := rows.Scan(&r.SourceRFC, &r.SourceSection, &r.TargetRFC, &r.TargetSection, &r.Context, &r.TargetTitle); err != nil {
			return nil, fmt.Errorf("scan reference: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get references: iterate: %w", err)
	}
	return refs, nil
}
