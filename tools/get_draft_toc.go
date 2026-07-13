package tools

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetDraftTOCInput struct {
	Name     string `json:"name" jsonschema:"required,Internet-Draft name, with or without a revision suffix (e.g. 'draft-ietf-quic-transport' or 'draft-ietf-quic-transport-34')"`
	Revision string `json:"revision,omitempty" jsonschema:"Two-digit revision (e.g. '03'); overrides any revision embedded in name. Defaults to the latest revision"`
}

var GetDraftTOCTool = &mcp.Tool{
	Name: "get_draft_toc",
	Description: "Get the table of contents (section structure) of an Internet-Draft, fetched on demand from " +
		"the IETF archive. Resolves to the latest revision unless revision is given or embedded in name.",
}

func HandleGetDraftTOC(client *http.Client) func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftTOCInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftTOCInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult("name is required"), nil, nil
		}

		label, sections, err := fetchAndParseDraft(ctx, client, input.Name, input.Revision)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}

		header := fmt.Sprintf("# %s - Table of Contents\n\n", label)
		return textResult(formatTOC(header, sections)), nil, nil
	}
}
