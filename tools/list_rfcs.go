package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/ingest/rfcindex"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListRFCsInput struct {
	Query  string `json:"query,omitempty" jsonschema:"Filter by a case-insensitive substring of the RFC title"`
	Stream string `json:"stream,omitempty" jsonschema:"Filter by publishing stream (e.g. IETF, IRTF, IAB, Independent, Legacy); case-insensitive"`
	Status string `json:"status,omitempty" jsonschema:"Filter by status (e.g. 'Draft Standard', 'Internet Standard', 'Informational'); case-insensitive"`
	WG     string `json:"wg,omitempty" jsonschema:"Filter by working group acronym (e.g. idr, tcpm); case-insensitive"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default: 20)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0)"`
}

var ListRFCsTool = &mcp.Tool{
	Name:        "list_rfcs",
	Description: "List IETF RFCs. Optionally filter by a title substring (query), publishing stream, status, and working group. Documents that were allocated an RFC number but never issued are always excluded. Results are paginated (default 20 per page); use limit and offset to navigate.",
}

// canonicalStreams maps a lowercased stream filter onto the exact form
// rfc-index.xml stores (mixed case: "IETF" but "Legacy", "INDEPENDENT"),
// so the stream filter is as case-insensitive as the status filter. An
// unknown value passes through unchanged and simply matches nothing.
var canonicalStreams = map[string]string{
	"ietf":        "IETF",
	"irtf":        "IRTF",
	"iab":         "IAB",
	"independent": "INDEPENDENT",
	"legacy":      "Legacy",
	"editorial":   "Editorial",
}

func HandleListRFCs(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input ListRFCsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListRFCsInput) (*mcp.CallToolResult, any, error) {
		// Statuses are stored verbatim from rfc-index.xml, which uses
		// all-caps (e.g. "DRAFT STANDARD"); accept either case from callers.
		status := strings.ToUpper(input.Status)
		// Streams are stored verbatim too, but in mixed case; normalize
		// case-insensitively onto the known stored forms.
		stream := input.Stream
		if canonical, ok := canonicalStreams[strings.ToLower(stream)]; ok {
			stream = canonical
		}
		// Working group acronyms are stored lowercase.
		wg := strings.ToLower(input.WG)

		// The db layer treats a negative limit as "no limit" (internal
		// use only); clamp it to 0 so client input gets the default.
		limit := input.Limit
		if limit < 0 {
			limit = 0
		}

		result, err := d.ListRFCs(input.Query, stream, status, wg, limit, input.Offset)
		if err != nil {
			return internalError("failed to list RFCs", err)
		}
		// The db layer stores/returns status verbatim (all-caps); title-case
		// it for display here, same as get_metadata.
		for i := range result.RFCs {
			result.RFCs[i].Status = rfcindex.TitleCaseStatus(result.RFCs[i].Status)
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}
