package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/higebu/rfc-mcp/ingest/drafts"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetIPRInput struct {
	RFC  int    `json:"rfc,omitempty" jsonschema:"RFC number (e.g. 3261). Exactly one of rfc or name is required"`
	Name string `json:"name,omitempty" jsonschema:"Internet-Draft name, with or without a revision suffix (e.g. 'draft-ietf-quic-transport'). Exactly one of rfc or name is required"`
}

var GetIPRTool = &mcp.Tool{
	Name: "get_ipr",
	Description: "Get IETF IPR (patent) disclosures filed against an RFC or Internet-Draft, fetched live from the " +
		"IETF Datatracker. Only 'Posted' disclosures are returned (pending/parked/rejected/removed disclosures " +
		"are excluded). Disclosures are usually filed against a pre-publication draft name rather than the RFC " +
		"itself, so this also searches, for an RFC, its originating Internet-Draft and any draft(s) it replaced " +
		"(one hop), and for a draft, any draft(s) it replaced (one hop); see searched_docs in the result for " +
		"exactly which document names were queried. No matching disclosures returns an empty list, not an error.",
}

// iprFetchError formats a drafts.FetchIPR failure for get_ipr's error
// result. Mirrors draftFetchError's ErrNotFound branch (same underlying
// document-existence check, same "not found" wording other draft tools
// use) but names IPR disclosures rather than a draft fetch in the generic
// failure case, since the failing operation here isn't fetching a draft
// body.
func iprFetchError(label string, err error) string {
	if errors.Is(err, drafts.ErrNotFound) {
		return err.Error()
	}
	return fmt.Sprintf("failed to fetch IPR disclosures for %s: %v", label, err)
}

func HandleGetIPR(client *http.Client) func(ctx context.Context, req *mcp.CallToolRequest, input GetIPRInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetIPRInput) (*mcp.CallToolResult, any, error) {
		switch {
		case input.RFC <= 0 && input.Name == "":
			return errorResult("rfc or name is required"), nil, nil
		case input.RFC > 0 && input.Name != "":
			return errorResult("specify only one of rfc or name"), nil, nil
		}

		label := input.Name
		base := input.Name
		if base != "" {
			base, _ = splitDraftName(base)
			label = base
		}
		if input.RFC > 0 {
			label = fmt.Sprintf("rfc%d", input.RFC)
		}

		result, err := drafts.FetchIPR(ctx, client, input.RFC, base)
		if err != nil {
			return errorResult(iprFetchError(label, err)), nil, nil
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}
