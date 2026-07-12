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

// sanitizeFTS5Query wraps bare hyphenated tokens in double quotes so FTS5
// does not misinterpret the hyphen as a column-filter separator.
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
			j := i + 1
			for j < n && query[j] != '"' {
				j++
			}
			if j < n {
				j++
			}
			result = append(result, query[i:j])
			i = j
			continue
		}

		if i+5 <= n && query[i:i+5] == "NEAR(" {
			j := i + 5
			depth := 1
			for j < n && depth > 0 {
				switch query[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			result = append(result, query[i:j])
			i = j
			continue
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

	var results []SearchResult
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
