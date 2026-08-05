package tools

import (
	"context"
	"fmt"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetTOCInput struct {
	RFC int `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
}

var GetTOCTool = &mcp.Tool{
	Name:        "get_toc",
	Description: "Get the table of contents (section structure) of an RFC.",
}

func HandleGetTOC(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}

		sections, err := d.GetTOC(input.RFC)
		if err != nil {
			return internalError(fmt.Sprintf("failed to get TOC for RFC %d", input.RFC), err)
		}

		if len(sections) == 0 {
			return rfcNotFoundResult(d, input.RFC)
		}

		header := fmt.Sprintf("# RFC %d - Table of Contents\n\n", input.RFC)
		return textResult(formatTOC(header, sections)), nil, nil
	}
}
