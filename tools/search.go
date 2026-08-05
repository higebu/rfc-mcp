package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SearchInput struct {
	Query string `json:"query" jsonschema:"required,FTS5 query string. Hyphenated terms (e.g. three-way-handshake) are auto-quoted. Use AND/OR/NOT operators and double-quoted phrases for exact matches (e.g. '\"three way handshake\" AND SYN')."`
	RFC   int    `json:"rfc,omitempty" jsonschema:"Limit search to a single RFC number. Ignored when rfcs is provided."`
	RFCs  []int  `json:"rfcs,omitempty" jsonschema:"Limit search to one or more RFC numbers (e.g. [9293, 793]). Takes precedence over rfc."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default: 10)"`
}

var SearchTool = &mcp.Tool{
	Name: "search",
	Description: `Full-text search across RFC section content using SQLite FTS5 syntax.

Query syntax:
- AND/OR/NOT:    handshake AND retransmission
- Phrase:        "congestion control"
- Prefix:        retransmi*
- Column filter: title:security  or  content:handshake
- Proximity:     NEAR(SYN ACK, 5)
- Hyphenated terms are auto-quoted to avoid FTS5 syntax errors.

Tips:
- Use rfc or rfcs to restrict the search to one or more specific RFCs.
- Phrase search improves precision for multi-word concepts.
- title:term restricts matches to section headings only.`,
}

// fts5QueryErrorSignatures are the messages SQLite's FTS5 query parser
// produces for a malformed MATCH expression (observed with the
// modernc.org/sqlite driver, e.g. `fts5: syntax error near "AND"`,
// `unknown special query: `, `no such column: nosuchcol`,
// `unterminated string`). Only these are safe to show the client
// verbatim -- any other error under the same "invalid search query"
// wrapping (e.g. "no such table: sections_fts") is an internal failure.
var fts5QueryErrorSignatures = []string{
	"fts5: syntax error",
	"unknown special query",
	"no such column:",
	"unterminated string",
}

func isFTS5QueryError(msg string) bool {
	for _, sig := range fts5QueryErrorSignatures {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	return false
}

func HandleSearch(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
			return errorResult("query is required"), nil, nil
		}

		limit := input.Limit
		if limit <= 0 {
			limit = db.DefaultSearchLimit
		}

		rfcs := input.RFCs
		if len(rfcs) == 0 && input.RFC != 0 {
			rfcs = []int{input.RFC}
		}
		// Reject non-positive RFC numbers instead of silently binding
		// them into the IN(...) filter, where they can never match.
		for _, n := range rfcs {
			if n <= 0 {
				return errorResult(fmt.Sprintf("invalid RFC number %d in rfc/rfcs filter; RFC numbers must be positive", n)), nil, nil
			}
		}

		results, err := d.Search(input.Query, rfcs, limit)
		if err != nil {
			// The db layer labels the MATCH query's failure "invalid search
			// query", but that wrapping also covers infrastructure failures
			// (e.g. a closed database), so additionally require a known
			// FTS5 query-syntax signature before showing the detail: an
			// FTS5 syntax problem is the caller's to fix and they need the
			// message verbatim, while an internal database failure must not
			// leak its detail. SQLite's generic "SQL logic error" marker is
			// not enough -- modernc.org/sqlite prefixes every SQLITE_ERROR
			// with it, including internal failures such as "no such table".
			msg := err.Error()
			if strings.Contains(msg, "invalid search query") && isFTS5QueryError(msg) {
				return errorResult(fmt.Sprintf("search failed: %v", err)), nil, nil
			}
			return internalError("search failed", err)
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}
