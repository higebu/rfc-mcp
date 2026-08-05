package db

import (
	"fmt"
	"strings"
)

// SearchResult is a single FTS5 match returned by Search.
type SearchResult struct {
	RFC     int    `json:"rfc"`
	Number  string `json:"number"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// fts5Columns is the set of columns in the sections_fts table that support
// column-filter syntax (col:term). rfc is UNINDEXED and is excluded — FTS5
// rejects column filters on UNINDEXED columns.
var fts5Columns = map[string]bool{
	"number": true, "title": true, "content": true,
}

// fts5Operators are FTS5 keywords that must not be quoted.
var fts5Operators = map[string]bool{
	"AND": true, "OR": true, "NOT": true,
}

// consumePhrase returns the index just past the closing quote of the
// double-quoted phrase starting at query[i] (which must be '"'), or len(query)
// if the phrase is unterminated.
func consumePhrase(query string, i int) int {
	j := i + 1
	for j < len(query) && query[j] != '"' {
		j++
	}
	if j < len(query) {
		j++ // include the closing quote
	}
	return j
}

// hasNearPrefix reports whether s begins with a case-insensitive "NEAR("
// group opener.
func hasNearPrefix(s string) bool {
	return len(s) >= 5 && strings.EqualFold(s[:5], "NEAR(")
}

// consumeNear returns the index just past the matching close paren of the
// NEAR(...) group starting at query[i] (which must point at the "NEAR("
// prefix), or len(query) if unbalanced.
func consumeNear(query string, i int) int {
	j := i + 5
	depth := 1
	for j < len(query) && depth > 0 {
		switch query[j] {
		case '(':
			depth++
		case ')':
			depth--
		}
		j++
	}
	return j
}

// columnFilterLen returns the length of the "col:" prefix at the start of s
// when col is a filterable FTS5 column, or 0 when s starts with no such
// prefix.
func columnFilterLen(s string) int {
	for col := range fts5Columns {
		if len(s) > len(col) && s[len(col)] == ':' && s[:len(col)] == col {
			return len(col) + 1
		}
	}
	return 0
}

// sanitizeFTS5Query wraps bare hyphenated tokens in double quotes so FTS5
// does not misinterpret the hyphen as a column-filter separator. Quoted
// phrases, NEAR(...) groups, and column filters followed by either (e.g.
// `title:"foo bar"`, `title:NEAR(a b)`) are consumed as single units and
// passed through verbatim, so whitespace tokenization cannot split them.
func sanitizeFTS5Query(query string) string {
	var result []string
	i := 0
	n := len(query)

	for i < n {
		if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' {
			i++
			continue
		}

		if query[i] == '"' {
			j := consumePhrase(query, i)
			result = append(result, query[i:j])
			i = j
			continue
		}

		if hasNearPrefix(query[i:]) {
			j := consumeNear(query, i)
			result = append(result, query[i:j])
			i = j
			continue
		}

		// A column filter followed by a quoted phrase or a NEAR(...) group
		// is one syntactic unit: consume through the matching close
		// quote/paren before whitespace tokenization can split it.
		if colLen := columnFilterLen(query[i:]); colLen > 0 {
			rest := i + colLen
			if rest < n && query[rest] == '"' {
				j := consumePhrase(query, rest)
				result = append(result, query[i:j])
				i = j
				continue
			}
			if hasNearPrefix(query[rest:]) {
				j := consumeNear(query, rest)
				result = append(result, query[i:j])
				i = j
				continue
			}
			// Plain col:value falls through to bare-token handling below.
		}

		j := i
		for j < n && query[j] != ' ' && query[j] != '\t' && query[j] != '\n' {
			j++
		}
		token := query[i:j]
		i = j

		if fts5Operators[token] {
			result = append(result, token)
			continue
		}

		if colIdx := strings.IndexByte(token, ':'); colIdx > 0 {
			col := token[:colIdx]
			val := token[colIdx+1:]
			if fts5Columns[col] {
				if strings.ContainsRune(val, '-') && !strings.HasPrefix(val, "\"") {
					result = append(result, col+":\""+val+"\"")
				} else {
					result = append(result, token)
				}
				continue
			}
		}

		// A leading hyphen is FTS5 NOT shorthand — leave it alone unless
		// there are additional hyphens in the rest of the token.
		if strings.ContainsRune(token, '-') {
			if token[0] == '-' {
				rest := token[1:]
				if strings.ContainsRune(rest, '-') {
					result = append(result, "\""+token+"\"")
				} else {
					result = append(result, token)
				}
			} else {
				result = append(result, "\""+token+"\"")
			}
			continue
		}

		result = append(result, token)
	}

	return strings.Join(result, " ")
}

// Search performs a full-text search over RFC section content, optionally
// restricted to a set of RFC numbers.
func (d *DB) Search(query string, rfcs []int, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	query = sanitizeFTS5Query(query)

	sqlQuery := "SELECT rfc, number, title, snippet(sections_fts, 3, '<mark>', '</mark>', '...', 32) FROM sections_fts WHERE sections_fts MATCH ?"
	args := []any{query}

	if len(rfcs) > 0 {
		placeholders := make([]string, len(rfcs))
		for i, n := range rfcs {
			placeholders[i] = "?"
			args = append(args, n)
		}
		sqlQuery += " AND rfc IN (" + strings.Join(placeholders, ", ") + ")"
	}
	sqlQuery += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := d.conn.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}
	defer rows.Close()

	results := []SearchResult{} // non-nil so an empty result serializes as [], not null
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.RFC, &r.Number, &r.Title, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: iterate: %w", err)
	}
	return results, nil
}
