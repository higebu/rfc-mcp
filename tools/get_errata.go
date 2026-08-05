package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetErrataInput struct {
	RFC     int    `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
	Status  string `json:"status,omitempty" jsonschema:"Filter by errata status (e.g. Verified, Reported, Held for Document Update, Rejected); case-insensitive exact match"`
	Type    string `json:"type,omitempty" jsonschema:"Filter by errata type (Technical or Editorial); case-insensitive exact match"`
	Section string `json:"section,omitempty" jsonschema:"Filter by section (e.g. 3.1); matched case-insensitively, ignoring a trailing '.' on either side"`
}

var GetErrataTool = &mcp.Tool{
	Name: "get_errata",
	Description: "Get the full detail of errata reported against an RFC: status, type, section, original and " +
		"corrected text, notes, submitter/verifier names, and dates. Optionally filter by status, type, and/or " +
		"section. get_metadata embeds a compact summary of the same errata (id/status/type/section only); use " +
		"this tool for the complete original/corrected text and notes. An RFC with no matching errata returns " +
		"an empty list, not an error.",
}

func HandleGetErrata(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetErrataInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetErrataInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}

		rfc, err := d.GetRFCMetadata(input.RFC)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errorResult(fmt.Sprintf("RFC %d not found%s", input.RFC, rfcRangeHint(d))), nil, nil
			}
			return internalError(fmt.Sprintf("failed to look up RFC %d", input.RFC), err)
		}
		if rfc.NotIssued {
			return errorResult(fmt.Sprintf("RFC %d was never issued", input.RFC)), nil, nil
		}

		items, err := d.GetErrataByRFC(input.RFC)
		if err != nil {
			return internalError(fmt.Sprintf("failed to get errata for RFC %d", input.RFC), err)
		}

		var filtered []db.Errata
		for _, e := range items {
			if input.Status != "" && !strings.EqualFold(e.Status, input.Status) {
				continue
			}
			if input.Type != "" && !strings.EqualFold(e.Type, input.Type) {
				continue
			}
			if input.Section != "" && !strings.EqualFold(normalizeErrataSection(e.Section), normalizeErrataSection(input.Section)) {
				continue
			}
			filtered = append(filtered, e)
		}

		if len(filtered) == 0 {
			return textResult("[]"), nil, nil
		}

		data, err := json.MarshalIndent(filtered, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}

// normalizeErrataSection loosens section-value comparisons for the section
// filter: errata.json is inconsistent about a trailing "." on section
// numbers for the same document (e.g. "3.3.2." vs "3.3.1"), so an exact
// string match would silently miss entries depending on which form the
// caller passes. Trailing dots and spaces are trimmed together so mixed
// suffixes like "3.1 ." normalize to "3.1" too.
func normalizeErrataSection(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), ". \t")
}
