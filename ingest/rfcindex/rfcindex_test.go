package rfcindex

import (
	"os"
	"strings"
	"testing"

	"github.com/higebu/rfc-mcp/db"
)

func parseFixture(t *testing.T) map[int]db.RFC {
	t.Helper()
	f, err := os.Open("testdata/rfc-index-sample.xml")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	rfcs, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	byNumber := make(map[int]db.RFC, len(rfcs))
	for _, r := range rfcs {
		byNumber[r.Number] = r
	}
	return byNumber
}

func TestParse_EntryCount(t *testing.T) {
	byNumber := parseFixture(t)
	// std-entry (STD3) is discarded; RFC14 (not-issued), RFC8, RFC1149,
	// RFC4271, and RFC9293 are the five entries expected from the fixture.
	if len(byNumber) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(byNumber), byNumber)
	}
}

// TestParse_NoTextFormat covers rfc-index.xml entries whose <format> list
// omits TXT (e.g. RFC 8, distributed only as a scan): HasText must be false
// so the pipeline knows not to attempt fetching a body that will 404.
func TestParse_NoTextFormat(t *testing.T) {
	byNumber := parseFixture(t)
	rfc, ok := byNumber[8]
	if !ok {
		t.Fatal("RFC8 missing")
	}
	if rfc.HasText {
		t.Error("RFC8: HasText = true, want false (PDF-only <format> list)")
	}
}

func TestParse_NotIssued(t *testing.T) {
	byNumber := parseFixture(t)
	rfc, ok := byNumber[14]
	if !ok {
		t.Fatal("RFC14 (not-issued) missing")
	}
	if !rfc.NotIssued {
		t.Error("RFC14: NotIssued = false, want true")
	}
	if rfc.Title != "" {
		t.Errorf("RFC14: Title = %q, want empty", rfc.Title)
	}
}

func TestParse_RFC4271(t *testing.T) {
	byNumber := parseFixture(t)
	rfc, ok := byNumber[4271]
	if !ok {
		t.Fatal("RFC4271 missing")
	}

	if got, want := rfc.Title, "A Border Gateway Protocol 4 (BGP-4)"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := rfc.Status, "DRAFT STANDARD"; got != want {
		t.Errorf("Status = %q, want %q", got, want)
	}
	if got, want := rfc.Stream, "IETF"; got != want {
		t.Errorf("Stream = %q, want %q", got, want)
	}
	if got, want := rfc.Area, "rtg"; got != want {
		t.Errorf("Area = %q, want %q", got, want)
	}
	if got, want := rfc.WG, "idr"; got != want {
		t.Errorf("WG = %q, want %q", got, want)
	}
	if got, want := rfc.Date, "2006-01"; got != want {
		t.Errorf("Date = %q, want %q", got, want)
	}
	if got, want := rfc.PageCount, 104; got != want {
		t.Errorf("PageCount = %d, want %d", got, want)
	}
	if got, want := rfc.Draft, "draft-ietf-idr-bgp4-26"; got != want {
		t.Errorf("Draft = %q, want %q", got, want)
	}
	if got, want := rfc.DOI, "10.17487/RFC4271"; got != want {
		t.Errorf("DOI = %q, want %q", got, want)
	}
	if got, want := rfc.ErrataURL, "https://www.rfc-editor.org/errata/rfc4271"; got != want {
		t.Errorf("ErrataURL = %q, want %q", got, want)
	}
	if got, want := len(rfc.Authors), 3; got != want {
		t.Errorf("len(Authors) = %d, want %d (%v)", got, want, rfc.Authors)
	}
	if !rfc.HasText {
		t.Error("RFC4271: HasText = false, want true (TXT is in the <format> list)")
	}
	if got, want := rfc.Obsoletes, []int{1771}; !intSliceEqual(got, want) {
		t.Errorf("Obsoletes = %v, want %v", got, want)
	}
	wantUpdatedBy := []int{4724, 6286, 6608, 6793, 7606, 7607, 7705, 8212, 8654, 9072, 9687, 9774}
	if got := rfc.UpdatedBy; !intSliceEqual(got, wantUpdatedBy) {
		t.Errorf("UpdatedBy = %v, want %v", got, wantUpdatedBy)
	}
	if got, want := rfc.Keywords, []string{"BGP-4", "routing"}; !strSliceEqual(got, want) {
		t.Errorf("Keywords = %v, want %v", got, want)
	}
	if rfc.Abstract == "" {
		t.Error("Abstract is empty")
	}
}

func TestParse_RFC9293(t *testing.T) {
	byNumber := parseFixture(t)
	rfc, ok := byNumber[9293]
	if !ok {
		t.Fatal("RFC9293 missing")
	}

	if got, want := rfc.Status, "INTERNET STANDARD"; got != want {
		t.Errorf("Status = %q, want %q", got, want)
	}
	if got, want := rfc.Date, "2022-08"; got != want {
		t.Errorf("Date = %q, want %q", got, want)
	}
	if !intSliceContains(rfc.Obsoletes, 793) {
		t.Errorf("Obsoletes = %v, want to contain 793", rfc.Obsoletes)
	}
	if got, want := rfc.Also, []string{"STD7"}; !strSliceEqual(got, want) {
		t.Errorf("Also = %v, want %v", got, want)
	}
	if rfc.Abstract == "" {
		t.Error("Abstract is empty")
	}
	if !rfc.HasText {
		t.Error("RFC9293: HasText = false, want true (TXT is in the <format> list)")
	}
}

func TestParse_DayBearingDateAndKeywords(t *testing.T) {
	byNumber := parseFixture(t)
	rfc, ok := byNumber[1149]
	if !ok {
		t.Fatal("RFC1149 missing")
	}
	if got, want := rfc.Date, "1990-04-01"; got != want {
		t.Errorf("Date = %q, want %q", got, want)
	}
	if got, want := rfc.Keywords, []string{"avian", "carrier", "april", "fools"}; !strSliceEqual(got, want) {
		t.Errorf("Keywords = %v, want %v", got, want)
	}
}

func TestTitleCaseStatus(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"PROPOSED STANDARD", "Proposed Standard"},
		{"DRAFT STANDARD", "Draft Standard"},
		{"INTERNET STANDARD", "Internet Standard"},
		{"BEST CURRENT PRACTICE", "Best Current Practice"},
		{"INFORMATIONAL", "Informational"},
		{"EXPERIMENTAL", "Experimental"},
		{"HISTORIC", "Historic"},
		{"UNKNOWN", "Unknown"},
	}
	for _, tt := range tests {
		if got := TitleCaseStatus(tt.in); got != tt.want {
			t.Errorf("TitleCaseStatus(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestParse_MalformedEntryAmongGood pins the partial-parse contract callers
// rely on (see ingest/pipeline.parseIndex): a malformed entry is skipped and
// reported via a non-nil joined error, but every good entry is still
// returned alongside it.
func TestParse_MalformedEntryAmongGood(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<rfc-index>
  <rfc-entry>
    <doc-id>BOGUS123</doc-id>
    <title>Broken entry</title>
  </rfc-entry>
  <rfc-entry>
    <doc-id>RFC9999</doc-id>
    <title>Good entry</title>
    <date><month>August</month><year>2022</year></date>
    <format><file-format>TXT</file-format></format>
    <current-status>INTERNET STANDARD</current-status>
  </rfc-entry>
</rfc-index>`

	rfcs, err := Parse(strings.NewReader(xml))
	if err == nil {
		t.Error("Parse: err = nil, want a joined skipped-entry error")
	}
	if len(rfcs) != 1 {
		t.Fatalf("Parse returned %d entries, want the 1 good entry: %+v", len(rfcs), rfcs)
	}
	if rfcs[0].Number != 9999 || rfcs[0].Title != "Good entry" {
		t.Errorf("good entry = %+v, want RFC 9999 %q", rfcs[0], "Good entry")
	}
}

// TestParse_BrokenStream covers a truly broken stream (truncated XML that
// yields no entries): callers must be able to detect it as zero entries plus
// a non-nil error.
func TestParse_BrokenStream(t *testing.T) {
	rfcs, err := Parse(strings.NewReader(`<rfc-index><rfc-entry><doc-id>RFC1</doc-id>`))
	if err == nil {
		t.Error("Parse: err = nil, want an error for a truncated stream")
	}
	if len(rfcs) != 0 {
		t.Errorf("Parse returned %d entries, want 0: %+v", len(rfcs), rfcs)
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func intSliceContains(a []int, v int) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
