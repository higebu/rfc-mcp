package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/higebu/rfc-mcp/ingest/drafts"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SearchDraftsInput struct {
	Query          string `json:"query,omitempty" jsonschema:"Filter by a case-insensitive substring of the draft title"`
	NameContains   string `json:"name_contains,omitempty" jsonschema:"Filter by a substring of the draft name (e.g. 'quic-transport')"`
	Group          string `json:"group,omitempty" jsonschema:"Filter by working group acronym (e.g. quic, idr)"`
	IncludeExpired bool   `json:"include_expired,omitempty" jsonschema:"Include drafts in any lifecycle state (expired, replaced, published as an RFC, ...), not just Active (default: false)"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default: 20)"`
	Offset         int    `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0)"`
}

var SearchDraftsTool = &mcp.Tool{
	Name: "search_drafts",
	Description: "Search Internet-Drafts by title/name substring and/or working group, fetched on demand from " +
		"the IETF Datatracker. A multi-word query requires every word to appear in the title, in any order " +
		"(e.g. 'BGP MUP SAFI' matches a title containing all three words even without that exact phrase); " +
		"use fewer, rarer/more distinctive words for a broader match. Only Active drafts are returned by " +
		"default; set include_expired to widen the search to every lifecycle state. Results are paginated " +
		"(default 20 per page); use limit and offset to navigate; a multi-word search's response may include " +
		"\"truncated\": true if its internal scan hit a cap, meaning total_count is a floor, not exact. Use " +
		"get_draft_metadata/get_draft_toc/get_draft_section to read a specific draft found here.",
}

func HandleSearchDrafts(client *http.Client) func(ctx context.Context, req *mcp.CallToolRequest, input SearchDraftsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchDraftsInput) (*mcp.CallToolResult, any, error) {
		// Clamp negative paging values at the tool boundary: a negative
		// offset would otherwise reach slice arithmetic downstream (a
		// negative limit just falls back to the default there).
		offset := max(input.Offset, 0)
		limit := max(input.Limit, 0)
		result, err := drafts.SearchDrafts(ctx, client, drafts.SearchParams{
			Query:          input.Query,
			NameContains:   input.NameContains,
			Group:          input.Group,
			IncludeExpired: input.IncludeExpired,
			Limit:          limit,
			Offset:         offset,
		})
		if err != nil {
			return errorResult(fmt.Sprintf("failed to search drafts: %v", err)), nil, nil
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
