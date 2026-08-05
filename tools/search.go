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

		results, err := d.Search(input.Query, rfcs, limit)
		if err != nil {
			// The db layer labels the MATCH query's failure "invalid search
			// query", but that wrapping also covers infrastructure failures
			// (e.g. a closed database), so additionally require SQLite's
			// "SQL logic error" marker (SQLITE_ERROR, which is what a bad
			// FTS5 expression compiles to) before showing the detail: an
			// FTS5 syntax problem is the caller's to fix and they need the
			// message verbatim, while an internal database failure must not
			// leak its detail.
			msg := err.Error()
			if strings.Contains(msg, "invalid search query") && strings.Contains(msg, "SQL logic error") {
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
