package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetDraftSectionInput struct {
	Name               string `json:"name" jsonschema:"required,Internet-Draft name, with or without a revision suffix (e.g. 'draft-ietf-quic-transport' or 'draft-ietf-quic-transport-34')"`
	Revision           string `json:"revision,omitempty" jsonschema:"Two-digit revision (e.g. '03'); overrides any revision embedded in name. Defaults to the latest revision"`
	SectionNumber      string `json:"section_number" jsonschema:"required,Section number (e.g. 4.1, A.2) or a slug for an unnumbered section: lowercase the heading and replace spaces/apostrophes with hyphens (e.g. 'abstract', 'security-considerations', 'iana-considerations', 'acknowledgments', 'authors-address')"`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include all subsections (default: false)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines           int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200, 0 = all)"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetDraftSectionTool = &mcp.Tool{
	Name: "get_draft_section",
	Description: "Get the verbatim text of a specific section of an Internet-Draft, fetched on demand from the " +
		"IETF archive and addressed the same way as get_section (section_number by number or slug, paginated " +
		"output, include_subsections). Resolves to the latest revision unless revision is given or embedded in name.",
}

func HandleGetDraftSection(client *http.Client) func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftSectionInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftSectionInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult("name is required"), nil, nil
		}
		if input.SectionNumber == "" {
			return errorResult("section_number is required"), nil, nil
		}

		label, all, err := fetchAndParseDraft(ctx, client, input.Name, input.Revision)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}

		sections := draftSection(all, input.SectionNumber, input.IncludeSubsections)
		if len(sections) == 0 {
			// Mirrors get_section.go's guidance: point at real content
			// instead of a bare not-found error when the queried number
			// has no heading of its own but something underneath it does.
			viaParentLink := true
			children := draftChildren(all, input.SectionNumber)
			if len(children) == 0 {
				viaParentLink = false
				children = draftDescendantsByPrefix(all, input.SectionNumber)
			}
			if len(children) > 0 {
				return textResult(missingSectionGuidance(label, input.SectionNumber, children, viaParentLink)), nil, nil
			}
			return errorResult(fmt.Sprintf("section %q not found in %s", input.SectionNumber, label)), nil, nil
		}

		// A title-only parent (empty Content, body text living entirely in
		// its children) returns whitespace here unless include_subsections
		// is set; point at the children instead, same as get_section.go.
		if !input.IncludeSubsections && strings.TrimSpace(sections[0].Content) == "" {
			children := draftChildren(all, sections[0].Number)
			if len(children) > 0 {
				return textResult(emptyParentGuidance(sections[0], children)), nil, nil
			}
		}

		var full strings.Builder
		for _, s := range sections {
			full.WriteString(db.SectionHeading(s.Number, s.Title))
			full.WriteString(s.Content)
			full.WriteString("\n\n")
		}

		return paginateText(full.String(), input.Offset, input.MaxLines, input.MaxChars), nil, nil
	}
}
