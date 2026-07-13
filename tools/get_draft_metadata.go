package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/higebu/rfc-mcp/ingest/drafts"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetDraftMetadataInput struct {
	Name string `json:"name" jsonschema:"required,Internet-Draft name, with or without a revision suffix (e.g. 'draft-ietf-quic-transport' or 'draft-ietf-quic-transport-34')"`
}

var GetDraftMetadataTool = &mcp.Tool{
	Name: "get_draft_metadata",
	Description: "Get metadata for an Internet-Draft, fetched on demand from the IETF Datatracker: latest " +
		"revision, title, abstract, page count, submission time, and expiration date. Always reports the " +
		"latest revision's metadata, even if name carried an older revision suffix (see requested_revision). " +
		"If the draft has since been published as an RFC, rfc carries its number -- use the RFC tools " +
		"(get_metadata/get_toc/get_section/get_document) with that number instead of this draft's own tools.",
}

// getDraftMetadataOutput mirrors drafts.Metadata's fields plus two things
// specific to this tool's request: the caller's own embedded revision (if
// any), and a hint pointing at the RFC tools once RFC is set.
type getDraftMetadataOutput struct {
	Name              string `json:"name"`
	Rev               string `json:"rev"`
	RequestedRevision string `json:"requested_revision,omitempty"`
	Title             string `json:"title"`
	Abstract          string `json:"abstract,omitempty"`
	Pages             int    `json:"pages,omitempty"`
	Time              string `json:"time,omitempty"`
	Expires           string `json:"expires,omitempty"`
	RFC               int    `json:"rfc,omitempty"`
	Hint              string `json:"hint,omitempty"`
}

func HandleGetDraftMetadata(client *http.Client) func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftMetadataInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetDraftMetadataInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult("name is required"), nil, nil
		}

		base, embeddedRev := splitDraftName(input.Name)
		meta, err := drafts.FetchMetadata(ctx, client, base)
		if err != nil {
			return errorResult(draftFetchError(base, err)), nil, nil
		}

		out := getDraftMetadataOutput{
			Name:              meta.Name,
			Rev:               meta.Rev,
			RequestedRevision: embeddedRev,
			Title:             meta.Title,
			Abstract:          meta.Abstract,
			Pages:             meta.Pages,
			Time:              meta.Time,
			Expires:           meta.Expires,
			RFC:               meta.RFC,
		}
		if meta.RFC > 0 {
			out.Hint = fmt.Sprintf(
				"This draft was published as RFC %d; use get_metadata/get_toc/get_section/get_document with rfc=%d instead.",
				meta.RFC, meta.RFC,
			)
		}

		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
