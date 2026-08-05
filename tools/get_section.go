package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetSectionInput struct {
	RFC                int    `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
	SectionNumber      string `json:"section_number" jsonschema:"required,Section number (e.g. 4.1, A.2) or a slug for an unnumbered section: lowercase the heading and replace spaces/apostrophes with hyphens (e.g. 'abstract', 'security-considerations', 'iana-considerations', 'acknowledgments', 'authors-address')"`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include all subsections (default: false)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0; negative values are treated as 0)"`
	MaxLines           int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200; 0 or omitted uses the default; use offset to page through longer content)"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetSectionTool = &mcp.Tool{
	Name: "get_section",
	Description: "Get the verbatim text of a specific section of an RFC, identified by rfc and section_number. " +
		"Unnumbered sections (Abstract, Security Considerations, IANA Considerations, Acknowledgments, " +
		"Author's Address, ...) are addressed by a slug: lowercase the heading and replace spaces/apostrophes " +
		"with hyphens (e.g. \"Security Considerations\" -> \"security-considerations\"). " +
		"Output is prefixed with a heading line per section. If a section has no body text of its own " +
		"(a title-only parent whose text lives entirely in its subsections), this returns a summary of its " +
		"subsections instead of empty text; pass include_subsections=true to read them all. " +
		"Large sections are paginated (default 200 lines); use offset and max_lines to navigate.",
}

func HandleGetSection(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}
		if input.SectionNumber == "" {
			return errorResult("section_number is required"), nil, nil
		}

		sections, err := d.GetSection(input.RFC, input.SectionNumber, input.IncludeSubsections)
		if err != nil {
			return internalError(fmt.Sprintf("failed to get section %q of RFC %d", input.SectionNumber, input.RFC), err)
		}

		if len(sections) == 0 {
			// Distinguish "this RFC has other sections, just not this one"
			// from "this RFC has no content at all" (unknown/not-issued/unparsed).
			// GetTOC/GetChildren/GetDescendantsByPrefix signal not-found as
			// an empty slice with a nil error, so any non-nil error from
			// them is a real database failure, not a missing RFC/section.
			toc, tocErr := d.GetTOC(input.RFC)
			if tocErr != nil {
				return internalError(fmt.Sprintf("failed to look up RFC %d", input.RFC), tocErr)
			}
			if len(toc) > 0 {
				// The queried number has no heading of its own, but something
				// underneath it does -- most commonly a caller guessing at an
				// intermediate number that the source document itself never
				// wrote as its own heading (see ingest/rfctxt's
				// reparentDanglingAncestors). GetChildren covers a heading
				// whose ParentNumber still points at the missing number;
				// GetDescendantsByPrefix is the fallback for the number
				// reparentDanglingAncestors has since rerouted real children
				// away from -- include_subsections=true on the missing
				// number itself won't retrieve those (there's no
				// ParentNumber chain leading to them any more), so the
				// guidance only offers it when GetChildren is what found them.
				viaParentLink := true
				children, childErr := d.GetChildren(input.RFC, input.SectionNumber)
				if childErr == nil && len(children) == 0 {
					viaParentLink = false
					children, childErr = d.GetDescendantsByPrefix(input.RFC, input.SectionNumber)
				}
				if childErr != nil {
					return internalError(fmt.Sprintf("failed to get section %q of RFC %d", input.SectionNumber, input.RFC), childErr)
				}
				if len(children) > 0 {
					return textResult(missingSectionGuidance(fmt.Sprintf("RFC %d", input.RFC), input.SectionNumber, children, viaParentLink)), nil, nil
				}
				return errorResult(fmt.Sprintf("section %q not found in RFC %d", input.SectionNumber, input.RFC)), nil, nil
			}
			return rfcNotFoundResult(d, input.RFC)
		}

		// A title-only parent (empty Content, body text living entirely in
		// its children) returns whitespace here unless include_subsections
		// is set; that's indistinguishable from a bug to the caller, so
		// point at the children instead of returning it verbatim. Leaf
		// sections with no children (parser artifacts) fall through and are
		// returned as-is -- the heading line below still identifies them.
		if !input.IncludeSubsections && strings.TrimSpace(sections[0].Content) == "" {
			children, err := d.GetChildren(input.RFC, sections[0].Number)
			if err != nil {
				return internalError(fmt.Sprintf("failed to get section %q of RFC %d", input.SectionNumber, input.RFC), err)
			}
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

// emptyParentGuidance describes a title-only parent section's direct
// children, so the caller can decide whether to fetch them all via
// include_subsections or request one directly, rather than receiving
// indistinguishable-from-a-bug whitespace.
func emptyParentGuidance(s db.Section, children []db.SectionChild) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Section %s (%s) has no body text of its own. It has %d subsection", s.Number, s.Title, len(children))
	if len(children) != 1 {
		sb.WriteString("s")
	}
	sb.WriteString(": ")
	for i, c := range children {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s (%s)", c.Number, c.Title)
	}
	sb.WriteString(". Use include_subsections=true to read them all, or request a subsection directly.")
	return sb.String()
}

// missingSectionGuidance describes a queried section number that has no
// heading of its own anywhere in the document (an RFC identified by
// label "RFC %d", or a draft identified by its name+revision -- see
// get_draft_section.go), found via GetChildren (a heading still pointing
// at it as ParentNumber) or GetDescendantsByPrefix (nothing does, but
// numbered descendants exist), so the caller gets a pointer to real
// content instead of a bare not-found error. include_subsections=true is
// only offered when viaParentLink is true -- otherwise the ParentNumber
// chain that flag walks doesn't lead to these children any more (see the
// call site).
func missingSectionGuidance(label, number string, children []db.SectionChild, viaParentLink bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Section %s has no heading of its own in %s. It has %d subsection", number, label, len(children))
	if len(children) != 1 {
		sb.WriteString("s")
	}
	sb.WriteString(": ")
	for i, c := range children {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s (%s)", c.Number, c.Title)
	}
	if viaParentLink {
		sb.WriteString(". Use include_subsections=true to read them all, or request a subsection directly.")
	} else {
		sb.WriteString(". Request one of them directly.")
	}
	return sb.String()
}
