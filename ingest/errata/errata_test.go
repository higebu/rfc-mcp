package errata

import (
	"os"
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
