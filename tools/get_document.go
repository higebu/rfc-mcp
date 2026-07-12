package tools

import (
	"context"
	"fmt"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetDocumentInput struct {
	RFC      int `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
	Offset   int `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines int `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200, 0 = all)"`
	MaxChars int `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetDocumentTool = &mcp.Tool{
	Name: "get_document",
	Description: "Get the full text of an RFC as one document, paginated. Prefer get_section for targeted " +
		"reading of long RFCs; use this for short RFCs or documents without section structure.",
}

func HandleGetDocument(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetDocumentInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetDocumentInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}

		doc, err := d.GetDocument(input.RFC)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get document: %v", err)), nil, nil
		}
		if doc == "" {
			return errorResult(rfcNotFoundError(d, input.RFC)), nil, nil
		}

		return paginateText(doc, input.Offset, input.MaxLines, input.MaxChars), nil, nil
	}
}
