package tools

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxRFCNumbers caches maxRFCNumber's answer per database handle. The
// content of an open database never changes while it's being served, so
// the lookup is worth doing once per handle, not on every not-found error.
var maxRFCNumbers sync.Map // *db.DB -> int

// maxRFCNumber returns the highest currently issued RFC number. The db
// layer exposes no MAX(number) query, so this pages ListRFCs (ordered by
// number ascending) to its last row with two LIMIT-1 queries instead of
// materializing the whole corpus; the answer is then cached for the
// handle's lifetime. Lookup failures are not cached, so a transient error
// doesn't pin a missing hint.
func maxRFCNumber(d *db.DB) (int, bool) {
	if v, ok := maxRFCNumbers.Load(d); ok {
		return v.(int), true
	}
	first, err := d.ListRFCs("", "", "", "", 1, 0)
	if err != nil || first.TotalCount == 0 {
		return 0, false
	}
	last, err := d.ListRFCs("", "", "", "", 1, first.TotalCount-1)
	if err != nil || len(last.RFCs) == 0 {
		return 0, false
	}
	n := last.RFCs[len(last.RFCs)-1].Number
	maxRFCNumbers.Store(d, n)
	return n, true
}

// rfcRangeHint returns a suffix naming the highest currently issued RFC
// number (e.g. " (valid RFC numbers range from 1 to 9793)"), extended with
// the database's build date when available (e.g. " (valid RFC numbers
// range from 1 to 9793; database built 2026-07-12)"), or "" if the RFC
// lookup itself fails.
func rfcRangeHint(d *db.DB) string {
	maxNumber, ok := maxRFCNumber(d)
	if !ok {
		return ""
	}

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
