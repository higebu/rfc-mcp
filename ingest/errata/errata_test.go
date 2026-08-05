package errata

import (
	"os"
	"strings"
	"testing"

	"github.com/higebu/rfc-mcp/db"
)

func parseFixture(t *testing.T) map[int]db.Errata {
	t.Helper()
	f, err := os.Open("testdata/errata-sample.json")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	items, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	byID := make(map[int]db.Errata, len(items))
	for _, e := range items {
		byID[e.ID] = e
	}
	return byID
}

func TestParse_EntryCount(t *testing.T) {
	byID := parseFixture(t)
	if len(byID) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(byID), byID)
	}
}

func TestParse_FieldsExact(t *testing.T) {
	byID := parseFixture(t)

	want := db.Errata{
		ID: 1, RFC: 4954, Status: "Verified", Type: "Editorial", Section: "4.1",
		OrigText:      "   S: 220-smtp.example.com ESMTP Server",
		CorrectText:   "   S: 220 smtp.example.com ESMTP Server",
		Notes:         "There are 3 instances of this (one on p. 7 and two on p. 8). \n",
		SubmittedDate: "2007-07-19",
		SubmitterName: "Rob Siemborski",
		VerifierName:  "", // null in the live JSON
		UpdatedDate:   "2019-09-10 16:09:03",
	}
	got, ok := byID[1]
	if !ok {
		t.Fatal("errata id 1 missing")
	}
	if got != want {
		t.Errorf("errata id 1 = %+v, want %+v", got, want)
	}
}

// TestParse_MalformedEntryAmongGood pins the partial-parse contract callers
// rely on (see ingest/pipeline.parseErrata): a malformed entry is skipped
// and reported via a non-nil joined error, but every good entry is still
// returned alongside it.
func TestParse_MalformedEntryAmongGood(t *testing.T) {
	const jsonData = `[
  {"errata_id": "not-a-number", "doc-id": "RFC1", "errata_status_code": "Verified"},
  {"errata_id": "42", "doc-id": "RFC9293", "errata_status_code": "Verified",
   "errata_type_code": "Technical", "section": "3.1", "orig_text": "a",
   "correct_text": "b", "notes": "", "submit_date": "2023-01-01",
   "submitter_name": "X", "verifier_name": null, "update_date": null}
]`

	items, err := Parse(strings.NewReader(jsonData))
	if err == nil {
		t.Error("Parse: err = nil, want a joined skipped-entry error")
	}
	if len(items) != 1 {
		t.Fatalf("Parse returned %d entries, want the 1 good entry: %+v", len(items), items)
	}
	if items[0].ID != 42 || items[0].RFC != 9293 {
		t.Errorf("good entry = %+v, want errata 42 for RFC 9293", items[0])
	}
}

// TestParse_BrokenStream covers a truly broken stream (not a JSON array):
// callers must be able to detect it as zero entries plus a non-nil error.
func TestParse_BrokenStream(t *testing.T) {
	items, err := Parse(strings.NewReader(`{"not": "an array"}`))
	if err == nil {
		t.Error("Parse: err = nil, want an error for a non-array stream")
	}
	if len(items) != 0 {
		t.Errorf("Parse returned %d entries, want 0: %+v", len(items), items)
	}
}

func TestParse_NullVerifierName(t *testing.T) {
	byID := parseFixture(t)
	got, ok := byID[1]
	if !ok {
		t.Fatal("errata id 1 missing")
	}
	if got.VerifierName != "" {
		t.Errorf("VerifierName = %q, want empty (JSON null)", got.VerifierName)
	}
}

func TestParse_Rejected(t *testing.T) {
	byID := parseFixture(t)
	got, ok := byID[2570]
	if !ok {
		t.Fatal("errata id 2570 missing")
	}
	if got.Status != "Rejected" {
		t.Errorf("Status = %q, want %q", got.Status, "Rejected")
	}
	if got.RFC != 5810 {
		t.Errorf("RFC = %d, want 5810", got.RFC)
	}
	if got.Notes == "" {
		t.Error("Notes is empty, want non-empty")
	}
}
