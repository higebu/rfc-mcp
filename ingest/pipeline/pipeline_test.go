package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/rfc-mcp/db"
)

// newTestServer serves the rfc-index/errata sample fixtures (shared with
// ingest/rfcindex and ingest/errata) plus the real RFC 9293 plain-text
// fixture, mimicking the RFC Editor's URL layout: rfc-index.xml and
// errata.json at the root, RFC bodies under /rfc/.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfcindex/testdata/rfc-index-sample.xml")
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../errata/testdata/errata-sample.json")
	})
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfctxt/testdata/rfc9293.txt")
	})
	mux.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenReadWrite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestPipeline_Run exercises the full fetch-parse-store flow against the
// rfc-index-sample.xml / errata-sample.json fixtures (also used by
// ingest/rfcindex and ingest/errata) plus the real RFC 9293 .txt fixture,
// restricted to RFC 9293 so the run stays fast.
func TestPipeline_Run(t *testing.T) {
	// Isolate the on-disk rfc-index.xml/errata.json cache from the real
	// ~/.cache/rfc-mcp: without this, a saved fixture (a few KB, 4 index
	// entries) would sit there within defaultCacheTTL and get picked up by
	// an unrelated later `rfc-mcp build` run instead of the real ~14 MB index.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	ts := newTestServer(t)
	defer ts.Close()
	d := newTestDB(t)

	p := &Pipeline{
		DB:      d,
		Client:  ts.Client(),
		Workers: 2,
		RawDir:  t.TempDir(),
		BaseURL: ts.URL,
	}
	if err := p.Run(context.Background(), 9293, 9293); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Metadata is upserted for every rfc-index.xml entry regardless of
	// range: RFC 9293 (in range, fully parsed), RFC 1149/4271 (out of
	// range, metadata only), and RFC 14 (not-issued).
	rfc, err := d.GetRFCMetadata(9293)
	if err != nil {
		t.Fatalf("GetRFCMetadata(9293): %v", err)
	}
	if rfc.Title != "Transmission Control Protocol (TCP)" {
		t.Errorf("rfc 9293 title = %q", rfc.Title)
	}
	if len(rfc.Obsoletes) == 0 {
		t.Error("rfc 9293 obsoletes should be non-empty")
	}

	if _, err := d.GetRFCMetadata(4271); err != nil {
		t.Errorf("GetRFCMetadata(4271) [out of range, metadata-only]: %v", err)
	}
	notIssued, err := d.GetRFCMetadata(14)
	if err != nil {
		t.Fatalf("GetRFCMetadata(14) [not-issued]: %v", err)
	}
	if !notIssued.NotIssued {
		t.Error("rfc 14 should be marked not_issued")
	}

	// Sections: RFC 9293 was fetched and parsed (Tier 1 numbered headings).
	toc, err := d.GetTOC(9293)
	if err != nil {
		t.Fatalf("GetTOC(9293): %v", err)
	}
	if len(toc) < 10 {
		t.Errorf("rfc 9293 section count = %d, want a real multi-section parse", len(toc))
	}
	// RFC 4271 is out of range: its body was never fetched, so it has no sections.
	if outOfRangeTOC, err := d.GetTOC(4271); err != nil {
		t.Fatalf("GetTOC(4271): %v", err)
	} else if len(outOfRangeTOC) != 0 {
		t.Errorf("rfc 4271 (out of range) should have no sections, got %d", len(outOfRangeTOC))
	}

	// References: section 8.2 (Informative References) cites RFC 793 via
	// the numeric-bracket citation "[16]", resolved through the RFC's own
	// bracket map built from sections 8.1+8.2.
	refs, err := d.GetReferences(9293, "8.2", db.DirectionOutgoing, false)
	if err != nil {
		t.Fatalf("GetReferences(9293, 8.2): %v", err)
	}
	found793 := false
	for _, r := range refs {
		if r.TargetRFC == 793 {
			found793 = true
		}
	}
	if !found793 {
		t.Errorf("expected a reference to RFC 793 from section 8.2, got %+v", refs)
	}

	// Errata: errata-sample.json's first entry is against RFC 4954.
	items, err := d.GetErrataByRFC(4954)
	if err != nil {
		t.Fatalf("GetErrataByRFC(4954): %v", err)
	}
	if len(items) == 0 {
		t.Error("expected errata for rfc 4954 from errata-sample.json")
	}

	// A completed run must stamp built_at and rfc_index_fetched_at (see
	// recordBuildMeta) so serve/rfcRangeHint can report data freshness.
	builtAt, ok := d.GetMeta("built_at")
	if !ok {
		t.Error("expected built_at to be recorded in meta after Run")
	} else if _, err := time.Parse(time.RFC3339, builtAt); err != nil {
		t.Errorf("built_at = %q is not RFC 3339: %v", builtAt, err)
	}
	fetchedAt, ok := d.GetMeta("rfc_index_fetched_at")
	if !ok {
		t.Error("expected rfc_index_fetched_at to be recorded in meta after Run")
	} else if _, err := time.Parse(time.RFC3339, fetchedAt); err != nil {
		t.Errorf("rfc_index_fetched_at = %q is not RFC 3339: %v", fetchedAt, err)
	}
}

// TestPipeline_Run_SkipsTextlessRFCs verifies that RFCs whose rfc-index.xml
// <format> list omits TXT (e.g. RFC 8, see rfc-index-sample.xml) are never
// fetched: attempting them would just 404 on every retry (see
// db.RFC.HasText and issuedNumbersInRange).
func TestPipeline_Run_SkipsTextlessRFCs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var rfc8Requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfcindex/testdata/rfc-index-sample.xml")
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../errata/testdata/errata-sample.json")
	})
	mux.HandleFunc("/rfc/rfc8.txt", func(w http.ResponseWriter, r *http.Request) {
		rfc8Requests.Add(1)
		http.NotFound(w, r)
	})
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfctxt/testdata/rfc9293.txt")
	})
	mux.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := newTestDB(t)
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 2, RawDir: t.TempDir(), BaseURL: ts.URL}
	if err := p.Run(context.Background(), 0, 0); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := rfc8Requests.Load(); n != 0 {
		t.Errorf("rfc8.txt was fetched %d times, want 0 (RFC 8 has no plain-text rendition)", n)
	}

	rfc, err := d.GetRFCMetadata(8)
	if err != nil {
		t.Fatalf("GetRFCMetadata(8): %v", err)
	}
	if rfc.HasText {
		t.Error("rfc 8 HasText = true, want false")
	}
	toc, err := d.GetTOC(8)
	if err != nil {
		t.Fatalf("GetTOC(8): %v", err)
	}
	if len(toc) != 0 {
		t.Errorf("expected no sections for textless rfc 8, got %d", len(toc))
	}
}

// TestPipeline_Run_ToleratesMalformedEntries verifies the parse contract of
// issue #6: one malformed rfc-index.xml or errata.json entry among good ones
// must not abort the build -- the parsers return the good rows alongside a
// joined error, and the pipeline logs it as a warning and proceeds.
func TestPipeline_Run_ToleratesMalformedEntries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	const indexXML = `<?xml version="1.0" encoding="UTF-8"?>
<rfc-index>
  <rfc-entry>
    <doc-id>BOGUS123</doc-id>
    <title>Broken entry</title>
  </rfc-entry>
  <rfc-entry>
    <doc-id>RFC9293</doc-id>
    <title>Transmission Control Protocol (TCP)</title>
    <date><month>August</month><year>2022</year></date>
    <format><file-format>TXT</file-format></format>
    <current-status>INTERNET STANDARD</current-status>
  </rfc-entry>
</rfc-index>`
	const errataJSON = `[
  {"errata_id": "not-a-number", "doc-id": "RFC9293", "errata_status_code": "Verified"},
  {"errata_id": "42", "doc-id": "RFC9293", "errata_status_code": "Verified",
   "errata_type_code": "Technical", "section": "3.1", "orig_text": "a",
   "correct_text": "b", "notes": "", "submit_date": "2023-01-01",
   "submitter_name": "X", "verifier_name": null, "update_date": null}
]`

	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(indexXML))
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(errataJSON))
	})
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfctxt/testdata/rfc9293.txt")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := newTestDB(t)
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 2, RawDir: t.TempDir(), BaseURL: ts.URL}
	if err := p.Run(context.Background(), 9293, 9293); err != nil {
		t.Fatalf("Run with one malformed index/errata entry: %v, want nil (proceed with good rows)", err)
	}

	rfc, err := d.GetRFCMetadata(9293)
	if err != nil {
		t.Fatalf("GetRFCMetadata(9293): %v", err)
	}
	if rfc.Title != "Transmission Control Protocol (TCP)" {
		t.Errorf("rfc 9293 title = %q", rfc.Title)
	}
	items, err := d.GetErrataByRFC(9293)
	if err != nil {
		t.Fatalf("GetErrataByRFC(9293): %v", err)
	}
	if len(items) != 1 {
		t.Errorf("errata count = %d, want 1 (good entry kept, malformed one skipped)", len(items))
	}
}

// TestPipeline_Run_DBWriteFailureFatal verifies issue #7: a database write
// failure during per-RFC processing must not be demoted to a per-RFC
// FETCH_FAILED stat -- it means the database is globally broken, so Run must
// return a non-nil error. The database is closed from the rfc9293.txt body
// handler, i.e. after the metadata/errata phase succeeded but before
// processOne's InsertRFCWithSections write.
func TestPipeline_Run_DBWriteFailureFatal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	d := newTestDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfcindex/testdata/rfc-index-sample.xml")
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../errata/testdata/errata-sample.json")
	})
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		_ = d.Close() // break the DB before the worker's insert
		http.ServeFile(w, r, "../rfctxt/testdata/rfc9293.txt")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 2, RawDir: t.TempDir(), BaseURL: ts.URL}
	err := p.Run(context.Background(), 9293, 9293)
	if err == nil {
		t.Fatal("Run with a failing DB write: err = nil, want a database write failure")
	}
	if !strings.Contains(err.Error(), "database write failure") {
		t.Errorf("err = %v, want a wrapped database write failure", err)
	}
}

// TestPipeline_Run_BrokenIndexFatal: a truly broken rfc-index.xml stream
// (zero parseable entries) must still abort the build.
func TestPipeline_Run_BrokenIndexFatal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<rfc-index><rfc-entry><doc-id>RFC1</doc-id>`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := newTestDB(t)
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 2, RawDir: t.TempDir(), BaseURL: ts.URL}
	if err := p.Run(context.Background(), 0, 0); err == nil {
		t.Fatal("Run with a truncated rfc-index.xml: err = nil, want fatal")
	}
}

// TestPipeline_Run_BrokenErrataFatal: same for a broken errata.json stream.
func TestPipeline_Run_BrokenErrataFatal(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../rfcindex/testdata/rfc-index-sample.xml")
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not": "an array"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	d := newTestDB(t)
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 2, RawDir: t.TempDir(), BaseURL: ts.URL}
	if err := p.Run(context.Background(), 0, 0); err == nil {
		t.Fatal("Run with a non-array errata.json: err = nil, want fatal")
	}
}

func TestPipeline_ImportFile(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	if err := p.ImportFile("../rfctxt/testdata/rfc791.txt"); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}

	// RFC 791's header uses "RFC:  791", not "Request for Comments:", so
	// the number must come from the filename fallback.
	rfc, err := d.GetRFCMetadata(791)
	if err != nil {
		t.Fatalf("GetRFCMetadata(791): %v", err)
	}
	if rfc.Title != "INTERNET PROTOCOL" {
		t.Errorf("rfc 791 title = %q, want best-effort header title", rfc.Title)
	}

	toc, err := d.GetTOC(791)
	if err != nil {
		t.Fatalf("GetTOC(791): %v", err)
	}
	if len(toc) == 0 {
		t.Error("expected at least one section for rfc 791")
	}
}

func TestPipeline_ImportFile_ModernHeader(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	if err := p.ImportFile("../rfctxt/testdata/rfc9293.txt"); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}

	rfc, err := d.GetRFCMetadata(9293)
	if err != nil {
		t.Fatalf("GetRFCMetadata(9293): %v", err)
	}
	if rfc.Title != "Transmission Control Protocol (TCP)" {
		t.Errorf("rfc 9293 title = %q", rfc.Title)
	}
}

// TestPipeline_ImportFile_ExistingMetadataKept: when the RFC already has an
// rfcs row (rfc-index.xml previously loaded), importRaw's sql.ErrNoRows
// branch must NOT trigger -- the authoritative DB title wins over the
// header-scraped one.
func TestPipeline_ImportFile_ExistingMetadataKept(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	if err := d.UpsertRFC(db.RFC{Number: 791, Title: "Internet Protocol (from index)", HasText: true}); err != nil {
		t.Fatalf("UpsertRFC: %v", err)
	}
	if err := p.ImportFile("../rfctxt/testdata/rfc791.txt"); err != nil {
		t.Fatalf("ImportFile: %v", err)
	}

	rfc, err := d.GetRFCMetadata(791)
	if err != nil {
		t.Fatalf("GetRFCMetadata(791): %v", err)
	}
	if rfc.Title != "Internet Protocol (from index)" {
		t.Errorf("rfc 791 title = %q, want the pre-existing index title", rfc.Title)
	}
}

// TestPipeline_ImportFile_DBErrorPropagated verifies issue #11: a real
// database failure during the metadata lookup must propagate as an error,
// not be mistaken for "RFC not in index" and papered over with fabricated
// metadata.
func TestPipeline_ImportFile_DBErrorPropagated(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}
	_ = d.Close() // break the DB: GetRFCMetadata now fails with a non-ErrNoRows error

	err := p.ImportFile("../rfctxt/testdata/rfc791.txt")
	if err == nil {
		t.Fatal("ImportFile with a broken DB: err = nil, want the lookup error propagated")
	}
	if !strings.Contains(err.Error(), "look up rfc 791 metadata") {
		t.Errorf("err = %v, want the metadata lookup failure, not a fabricated-metadata insert error", err)
	}
}

func TestPipeline_ImportDir(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	dir := t.TempDir()
	for _, name := range []string{"rfc791.txt", "rfc9293.txt"} {
		data, err := os.ReadFile(filepath.Join("../rfctxt/testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := p.ImportDir(dir, 2); err != nil {
		t.Fatalf("ImportDir: %v", err)
	}

	for _, number := range []int{791, 9293} {
		if _, err := d.GetRFCMetadata(number); err != nil {
			t.Errorf("GetRFCMetadata(%d): %v", number, err)
		}
	}
}

func TestPipeline_ImportDir_NoFiles(t *testing.T) {
	d := newTestDB(t)
	p := &Pipeline{DB: d}

	if err := p.ImportDir(t.TempDir(), 1); err == nil {
		t.Fatal("expected an error for a directory with no .txt files")
	}
}
