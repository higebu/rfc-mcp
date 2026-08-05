package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetReferencesInput struct {
	RFC                int    `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
	SectionNumber      string `json:"section_number,omitempty" jsonschema:"Section number (e.g. 5.1). Required for outgoing direction."`
	Direction          string `json:"direction,omitempty" jsonschema:"outgoing (default): references FROM this section to other RFCs. incoming: references TO this RFC from other RFCs' sections."`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include subsections when collecting outgoing references (default: false)"`
}

var GetReferencesTool = &mcp.Tool{
	Name: "get_references",
	Description: `Get cross-references between RFCs.

Directions:
- outgoing (default): Find all RFCs referenced by a given section.
  Requires rfc and section_number. Use include_subsections to also gather refs from child sections.
- incoming: Find all sections (in any RFC) that reference a given RFC (and optionally a specific section of it).
  Requires rfc; section_number is optional.

Returns structured reference data including target RFC, section, title (if known), and a context snippet.`,
}

func HandleGetReferences(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetReferencesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetReferencesInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}

		direction := input.Direction
		if direction == "" {
			direction = db.DirectionOutgoing
		}

		if direction == db.DirectionOutgoing && input.SectionNumber == "" {
			return errorResult("section_number is required for outgoing direction"), nil, nil
		}

		refs, err := d.GetReferences(input.RFC, input.SectionNumber, direction, input.IncludeSubsections)
		if err != nil {
			return internalError(fmt.Sprintf("failed to get references for RFC %d", input.RFC), err)
		}

		if len(refs) == 0 {
			return textResult("[]"), nil, nil
		}

		data, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}
