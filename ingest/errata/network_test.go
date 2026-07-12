package errata

import (
	"net/http"
	"testing"
	"time"
)

// TestParse_LiveErrata downloads the live errata.json and parses it. It is
// skipped in -short mode and whenever the live fetch fails, mirroring
// 3gpp-mcp's testutil network-test idiom (GET only; rfc-editor.org rejects
// HEAD, and errata.json 302-redirects to /api/v1/errata.json, which
// http.Client follows by default).
func TestParse_LiveErrata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in -short mode")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get("https://www.rfc-editor.org/errata.json")
	if err != nil {
		t.Skipf("skipping: cannot download errata.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("skipping: HTTP %d for errata.json", resp.StatusCode)
	}

	items, err := Parse(resp.Body)
	if err != nil {
		t.Errorf("Parse: %v", err)
	}
	if len(items) < 7961 {
		t.Errorf("entries = %d, want >= 7961", len(items))
	}
	t.Logf("parsed %d errata entries", len(items))
}
