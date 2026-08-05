package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/ingest/rfcindex"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetMetadataInput struct {
	RFC int `json:"rfc" jsonschema:"required,RFC number (e.g. 4271)"`
}

var GetMetadataTool = &mcp.Tool{
	Name: "get_metadata",
	Description: "Get metadata for an RFC: title, status, stream, publication date, working group/area, " +
		"authors, abstract, obsoletes/obsoleted-by and updates/updated-by relationships, and errata. " +
		"Errata are returned as a compact summary (id, status, type, section only) rather than full " +
		"original/corrected text — follow errata_url for the complete report.",
}

// errataSummary is the compact errata shape embedded in get_metadata output;
// full erratum text (orig_text/correct_text/notes) is deliberately out of
// scope for this tool.
type errataSummary struct {
	ID      int    `json:"id"`
	Status  string `json:"status,omitempty"`
	Type    string `json:"type,omitempty"`
	Section string `json:"section,omitempty"`
}

type getMetadataOutput struct {
	RFC         int             `json:"rfc"`
	Title       string          `json:"title"`
	Status      string          `json:"status,omitempty"`
	Stream      string          `json:"stream,omitempty"`
	Date        string          `json:"date,omitempty"`
	PageCount   int             `json:"page_count,omitempty"`
	WG          string          `json:"wg,omitempty"`
	Area        string          `json:"area,omitempty"`
	Authors     []string        `json:"authors,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	Abstract    string          `json:"abstract,omitempty"`
	Draft       string          `json:"draft,omitempty"`
	DOI         string          `json:"doi,omitempty"`
	ErrataURL   string          `json:"errata_url,omitempty"`
	Obsoletes   []int           `json:"obsoletes,omitempty"`
	ObsoletedBy []int           `json:"obsoleted_by,omitempty"`
	Updates     []int           `json:"updates,omitempty"`
	UpdatedBy   []int           `json:"updated_by,omitempty"`
	Also        []string        `json:"also,omitempty"`
	Errata      []errataSummary `json:"errata,omitempty"`
}

func HandleGetMetadata(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetMetadataInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetMetadataInput) (*mcp.CallToolResult, any, error) {
		if input.RFC <= 0 {
			return errorResult("rfc is required"), nil, nil
		}

		rfc, err := d.GetRFCMetadata(input.RFC)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errorResult(fmt.Sprintf("RFC %d not found%s", input.RFC, rfcRangeHint(d))), nil, nil
			}
			return internalError(fmt.Sprintf("failed to get metadata for RFC %d", input.RFC), err)
		}
		if rfc.NotIssued {
			return errorResult(fmt.Sprintf("RFC %d was never issued", input.RFC)), nil, nil
		}

		errataItems, err := d.GetErrataByRFC(input.RFC)
		if err != nil {
			return internalError(fmt.Sprintf("failed to get errata for RFC %d", input.RFC), err)
		}
		var errata []errataSummary
		for _, e := range errataItems {
			errata = append(errata, errataSummary{ID: e.ID, Status: e.Status, Type: e.Type, Section: e.Section})
		}

		out := getMetadataOutput{
			RFC:         rfc.Number,
			Title:       rfc.Title,
			Status:      rfcindex.TitleCaseStatus(rfc.Status),
			Stream:      rfc.Stream,
			Date:        rfc.Date,
			PageCount:   rfc.PageCount,
			WG:          rfc.WG,
			Area:        rfc.Area,
			Authors:     rfc.Authors,
			Keywords:    rfc.Keywords,
			Abstract:    rfc.Abstract,
			Draft:       rfc.Draft,
			DOI:         rfc.DOI,
			ErrataURL:   rfc.ErrataURL,
			Obsoletes:   rfc.Obsoletes,
			ObsoletedBy: rfc.ObsoletedBy,
			Updates:     rfc.Updates,
			UpdatedBy:   rfc.UpdatedBy,
			Also:        rfc.Also,
			Errata:      errata,
		}

		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return internalError("failed to marshal result", err)
		}

		return textResult(string(data)), nil, nil
	}
}
