package tools

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// rfcRangeHint returns a suffix naming the highest currently issued RFC
// number (e.g. " (valid RFC numbers range from 1 to 9793)"), extended with
// the database's build date when available (e.g. " (valid RFC numbers
// range from 1 to 9793; database built 2026-07-12)"), or "" if the RFC
// lookup itself fails.
func rfcRangeHint(d *db.DB) string {
	result, err := d.ListRFCs("", "", "", "", -1, 0)
	if err != nil || len(result.RFCs) == 0 {
		return ""
	}
	maxNumber := result.RFCs[len(result.RFCs)-1].Number

	var built string
	if builtAt, ok := d.GetMeta("built_at"); ok {
		if t, err := time.Parse(time.RFC3339, builtAt); err == nil {
			built = "; database built " + t.Format("2006-01-02")
		}
	}
	return fmt.Sprintf(" (valid RFC numbers range from 1 to %d%s)", maxNumber, built)
}

// rfcNotFoundResult builds the full handler return value for an RFC number
// that produced no content, distinguishing between: the number was never
// allocated an RFC row at all (unknown), the row exists but marks a number
// that was allocated but never issued, and the row exists and was issued but
// has no parsed sections (a parsing gap rather than a bad request). A real
// database error during the lookup (anything other than sql.ErrNoRows,
// which GetRFCMetadata wraps with %w) is reported generically via
// internalError rather than leaking its detail to the client.
func rfcNotFoundResult(d *db.DB, rfc int) (*mcp.CallToolResult, any, error) {
	meta, err := d.GetRFCMetadata(rfc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errorResult(fmt.Sprintf("RFC %d not found%s", rfc, rfcRangeHint(d))), nil, nil
		}
		return internalError(fmt.Sprintf("failed to look up RFC %d", rfc), err)
	}
	if meta.NotIssued {
		return errorResult(fmt.Sprintf("RFC %d was never issued", rfc)), nil, nil
	}
	return errorResult(fmt.Sprintf("RFC %d exists but has no parsed sections available", rfc)), nil, nil
}
