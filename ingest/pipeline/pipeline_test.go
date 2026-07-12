package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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
