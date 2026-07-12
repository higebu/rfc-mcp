package tools

import (
	"context"
	"fmt"
	"strings"

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
			return errorResult(fmt.Sprintf("failed to get TOC: %v", err)), nil, nil
		}

		if len(sections) == 0 {
			return errorResult(rfcNotFoundError(d, input.RFC)), nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "# RFC %d - Table of Contents\n\n", input.RFC)
		for _, s := range sections {
			indent := strings.Repeat("  ", s.Level-1)
			fmt.Fprintf(&sb, "%s- %s %s\n", indent, s.Number, s.Title)
		}

		return textResult(sb.String()), nil, nil
	}
}
