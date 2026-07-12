package rfcindex

import (
	"net/http"
	"testing"
	"time"
)

// TestParse_LiveIndex downloads the live rfc-index.xml and parses it. It is
// skipped in -short mode and whenever the live fetch fails, mirroring
// 3gpp-mcp's testutil network-test idiom (GET only; rfc-editor.org rejects HEAD).
func TestParse_LiveIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in -short mode")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get("https://www.rfc-editor.org/rfc-index.xml")
	if err != nil {
		t.Skipf("skipping: cannot download rfc-index.xml: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("skipping: HTTP %d for rfc-index.xml", resp.StatusCode)
	}

	rfcs, err := Parse(resp.Body)
	if err != nil {
		t.Errorf("Parse: %v", err)
	}

	var issued, notIssued int
	for _, r := range rfcs {
		if r.NotIssued {
			notIssued++
		} else {
			issued++
		}
	}
	if issued < 9794 {
		t.Errorf("issued entries = %d, want >= 9794", issued)
	}
	if notIssued < 188 {
		t.Errorf("not-issued entries = %d, want >= 188", notIssued)
	}
	t.Logf("parsed %d issued, %d not-issued entries", issued, notIssued)
}
