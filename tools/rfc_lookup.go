package tools

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/higebu/rfc-mcp/db"
)

// rfcRangeHint returns a suffix naming the highest currently issued RFC
// number (e.g. " (valid RFC numbers range from 1 to 9793)"), or "" if that
// lookup itself fails.
func rfcRangeHint(d *db.DB) string {
	result, err := d.ListRFCs("", "", "", "", -1, 0)
	if err != nil || len(result.RFCs) == 0 {
		return ""
	}
	return fmt.Sprintf(" (valid RFC numbers range from 1 to %d)", result.RFCs[len(result.RFCs)-1].Number)
}

// rfcNotFoundError builds a helpful error message for an RFC number that
// produced no content, distinguishing between: the number was never
// allocated an RFC row at all (unknown), the row exists but marks a number
// that was allocated but never issued, and the row exists and was issued but
// has no parsed sections (a parsing gap rather than a bad request).
func rfcNotFoundError(d *db.DB, rfc int) string {
	meta, err := d.GetRFCMetadata(rfc)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Sprintf("RFC %d not found%s", rfc, rfcRangeHint(d))
		}
		return fmt.Sprintf("failed to look up RFC %d: %v", rfc, err)
	}
	if meta.NotIssued {
		return fmt.Sprintf("RFC %d was never issued", rfc)
	}
	return fmt.Sprintf("RFC %d exists but has no parsed sections available", rfc)
}
