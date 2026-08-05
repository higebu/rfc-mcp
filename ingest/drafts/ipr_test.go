package drafts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newIPRTestServer builds an httptest.Server mimicking the Datatracker
// endpoints FetchIPR hits for an RFC (rfc9999, whose originating draft is
// draft-example-ipr, which itself replaced draft-example-ipr-old) and for
// a bare draft (draft-standalone-ipr, which replaces nothing). A
// disclosure with id 100 is deliberately returned by both the rfc9999 and
// draft-example-ipr holderiprdisclosure queries, to exercise cross-name
// dedup; id 101 is "removed" (must be excluded); id 102 is a "posted"
// thirdpartyiprdisclosure (no licensing field) on draft-example-ipr-old.
func newIPRTestServer(t *testing.T, iprRequests *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Document existence checks.
	mux.HandleFunc("/api/v1/doc/document/rfc9999/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "rfc9999", "title": "An Example RFC"}`))
	})
	mux.HandleFunc("/api/v1/doc/document/draft-standalone-ipr/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "draft-standalone-ipr", "title": "A Standalone Draft"}`))
	})
	mux.HandleFunc("/api/v1/doc/document/rfc404/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// became_rfc reverse + replaces, both served from the same path,
	// routed by query params the way the real Datatracker filter API works.
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("relationship__slug") == "became_rfc" && q.Get("target__name") == "rfc9999":
			_, _ = w.Write([]byte(`{"meta": {"total_count": 1}, "objects": [
				{"source": "/api/v1/doc/document/draft-example-ipr/", "target": "/api/v1/doc/document/rfc9999/", "originaltargetaliasname": "rfc9999"}
			]}`))
		case q.Get("relationship__slug") == "replaces" && q.Get("source__name") == "draft-example-ipr":
			// Modern Datatracker rows leave originaltargetaliasname null
			// and only name the replaced draft via the target URI
			// (regression: draft-ietf-bess-mup-safi's predecessor was
			// silently skipped when only the alias field was read).
			_, _ = w.Write([]byte(`{"meta": {"total_count": 1}, "objects": [
				{"originaltargetaliasname": null, "target": "/api/v1/doc/document/draft-example-ipr-old/"}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
		}
	})

	mux.HandleFunc("/api/v1/ipr/holderiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		if iprRequests != nil {
			iprRequests.Add(1)
		}
		switch r.URL.Query().Get("docs__name") {
		case "rfc9999":
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 100, "title": "Holder disclosure on the RFC itself", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Acme Corp", "licensing": "/api/v1/name/iprlicensetypename/reasonable/",
				 "has_patent_pending": true, "patent_info": "US1234567", "time": "2020-01-01T00:00:00Z",
				 "docs": ["/api/v1/doc/document/rfc9999/"]}
			]}`))
		case "draft-example-ipr":
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 100, "title": "Holder disclosure on the RFC itself", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Acme Corp", "licensing": "/api/v1/name/iprlicensetypename/reasonable/",
				 "has_patent_pending": true, "patent_info": "US1234567", "time": "2020-01-01T00:00:00Z",
				 "docs": ["/api/v1/doc/document/rfc9999/"]},
				{"id": 101, "title": "A removed disclosure", "state": "/api/v1/name/iprdisclosurestatename/removed/",
				 "holder_legal_name": "Should Not Appear", "docs": ["/api/v1/doc/document/draft-example-ipr/"]}
			]}`))
		case "draft-standalone-ipr":
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 200, "title": "Standalone draft disclosure", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Standalone Inc", "licensing": "/api/v1/name/iprlicensetypename/no-license/",
				 "has_patent_pending": false, "patent_info": "", "time": "2021-01-01T00:00:00Z",
				 "docs": ["/api/v1/doc/document/draft-standalone-ipr/"]}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"objects": []}`))
		}
	})
	mux.HandleFunc("/api/v1/ipr/thirdpartyiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		if iprRequests != nil {
			iprRequests.Add(1)
		}
		if r.URL.Query().Get("docs__name") == "draft-example-ipr-old" {
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 102, "title": "Third-party disclosure on the replaced draft", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Third Party LLC", "has_patent_pending": true, "patent_info": "US7654321",
				 "time": "2019-01-01T00:00:00Z", "docs": ["/api/v1/doc/document/draft-example-ipr-old/"]}
			]}`))
			return
		}
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/genericiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		if iprRequests != nil {
			iprRequests.Add(1)
		}
		_, _ = w.Write([]byte(`{"objects": []}`))
	})

	return httptest.NewServer(mux)
}

func TestFetchIPR_RFCFanOutAndDedup(t *testing.T) {
	ts := newIPRTestServer(t, nil)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	result, err := FetchIPR(context.Background(), ts.Client(), 9999, "")
	if err != nil {
		t.Fatalf("FetchIPR: %v", err)
	}

	wantDocs := map[string]bool{"rfc9999": true, "draft-example-ipr": true, "draft-example-ipr-old": true}
	if len(result.SearchedDocs) != len(wantDocs) {
		t.Fatalf("SearchedDocs = %v, want %d entries matching %v", result.SearchedDocs, len(wantDocs), wantDocs)
	}
	for _, d := range result.SearchedDocs {
		if !wantDocs[d] {
			t.Errorf("unexpected searched doc %q", d)
		}
	}

	if result.TotalCount != 2 {
		t.Fatalf("TotalCount = %d, want 2 (id 100 deduped across rfc9999+draft-example-ipr, plus id 102; id 101 excluded as removed)", result.TotalCount)
	}
	ids := map[int]Disclosure{}
	for _, d := range result.Disclosures {
		ids[d.ID] = d
	}
	if _, ok := ids[101]; ok {
		t.Error("removed disclosure (id 101) should have been excluded")
	}
	d100, ok := ids[100]
	if !ok {
		t.Fatal("expected deduped disclosure id 100")
	}
	if d100.Holder != "Acme Corp" || d100.Licensing != "reasonable" || d100.State != "posted" {
		t.Errorf("disclosure 100 = %+v", d100)
	}
	if d100.HasPatentPending == nil || !*d100.HasPatentPending {
		t.Errorf("disclosure 100 HasPatentPending = %v, want true", d100.HasPatentPending)
	}
	if d100.URL != ts.URL+"/ipr/100/" {
		t.Errorf("URL = %q", d100.URL)
	}

	d102, ok := ids[102]
	if !ok {
		t.Fatal("expected disclosure id 102 found via the one-hop replaces fan-out")
	}
	if d102.Licensing != "" {
		t.Errorf("thirdparty disclosure Licensing = %q, want empty (thirdpartyiprdisclosure has no licensing field)", d102.Licensing)
	}
}

func TestFetchIPR_DraftName(t *testing.T) {
	ts := newIPRTestServer(t, nil)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	result, err := FetchIPR(context.Background(), ts.Client(), 0, "draft-standalone-ipr")
	if err != nil {
		t.Fatalf("FetchIPR: %v", err)
	}
	if len(result.SearchedDocs) != 1 || result.SearchedDocs[0] != "draft-standalone-ipr" {
		t.Errorf("SearchedDocs = %v, want just [draft-standalone-ipr] (it replaces nothing)", result.SearchedDocs)
	}
	if result.TotalCount != 1 || result.Disclosures[0].ID != 200 {
		t.Fatalf("result = %+v", result)
	}
	if result.Disclosures[0].HasPatentPending == nil || *result.Disclosures[0].HasPatentPending {
		t.Errorf("HasPatentPending = %v, want false (explicit false, not nil)", result.Disclosures[0].HasPatentPending)
	}
}

func TestFetchIPR_EmptyResult(t *testing.T) {
	ts := newIPRTestServer(t, nil)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/rfc1/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "rfc1"}`))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/holderiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/thirdpartyiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/genericiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	ts2 := httptest.NewServer(mux)
	defer ts2.Close()
	withTestRoots(t, ts2.URL)

	result, err := FetchIPR(context.Background(), ts2.Client(), 1, "")
	if err != nil {
		t.Fatalf("FetchIPR: %v", err)
	}
	if result.TotalCount != 0 || result.Disclosures == nil || len(result.Disclosures) != 0 {
		t.Errorf("result = %+v, want an empty (non-nil) Disclosures slice so JSON shows [], not null", result)
	}
	// rfc1 has no became_rfc row: an RFC published before the Datatracker
	// tracked drafts (or, here, the mock's default empty response).
	if len(result.SearchedDocs) != 1 || result.SearchedDocs[0] != "rfc1" {
		t.Errorf("SearchedDocs = %v, want just [rfc1]", result.SearchedDocs)
	}
}

func TestFetchIPR_UnknownDoc(t *testing.T) {
	ts := newIPRTestServer(t, nil)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	_, err := FetchIPR(context.Background(), ts.Client(), 404, "")
	if err == nil {
		t.Fatal("expected an error for an unknown RFC number")
	}
	if !strings.Contains(err.Error(), "rfc404") {
		t.Errorf("err = %v, want it to name the attempted document", err)
	}
}

// TestFetchIPR_ReplacedDraftsErrorPropagates is a regression test: a
// failed replaces lookup used to be silently ignored, returning -- and
// caching for an hour -- an incomplete disclosure list. It must now fail
// the FetchIPR call, and nothing may be cached on the error path.
func TestFetchIPR_ReplacedDraftsErrorPropagates(t *testing.T) {
	origDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = origDelay }()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-err-ipr/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "draft-err-ipr", "title": "A Draft"}`))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	_, err := FetchIPR(context.Background(), ts.Client(), 0, "draft-err-ipr")
	if err == nil {
		t.Fatal("FetchIPR = nil error, want the replaces lookup failure propagated")
	}
	if !strings.Contains(err.Error(), "draft-err-ipr") {
		t.Errorf("err = %v, want it to name the draft whose replaces lookup failed", err)
	}
	if _, statErr := os.Stat(filepath.Join(cacheHome, "rfc-mcp", "drafts", "ipr", "draft-err-ipr.json")); !os.IsNotExist(statErr) {
		t.Errorf("cache stat = %v, want no IPR result cached on the error path", statErr)
	}
}

// TestFetchIPR_PagedDisclosures: disclosure lists used to be truncated at
// the Datatracker's limit=100 page size. FetchIPR must follow offset
// pagination until the endpoint is exhausted.
func TestFetchIPR_PagedDisclosures(t *testing.T) {
	const total = 130 // 100 on the first page, 30 on the second

	pagedObjects := func(offset, limit int) string {
		n := min(limit, total-offset)
		var objects []string
		for i := range n {
			id := offset + i
			objects = append(objects, fmt.Sprintf(
				`{"id": %d, "title": "Disclosure %d", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				  "holder_legal_name": "Holder %d", "docs": ["/api/v1/doc/document/draft-paged-ipr/"]}`, id, id, id))
		}
		return "[" + strings.Join(objects, ",") + "]"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-paged-ipr/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "draft-paged-ipr", "title": "A Draft"}`))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/holderiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		_, _ = fmt.Fprintf(w, `{"objects": %s}`, pagedObjects(offset, limit))
	})
	mux.HandleFunc("/api/v1/ipr/thirdpartyiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/genericiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	result, err := FetchIPR(context.Background(), ts.Client(), 0, "draft-paged-ipr")
	if err != nil {
		t.Fatalf("FetchIPR: %v", err)
	}
	if result.TotalCount != total {
		t.Fatalf("TotalCount = %d, want %d (pagination must be followed past the first 100 rows)", result.TotalCount, total)
	}
	for i, d := range result.Disclosures {
		if d.ID != i {
			t.Fatalf("Disclosures[%d].ID = %d, want %d (all pages collected, sorted by ID)", i, d.ID, i)
		}
	}
}

// TestReplacedDrafts_Paged: the one-hop replaces lookup must follow offset
// pagination too, not stop at the first 100 rows.
func TestReplacedDrafts_Paged(t *testing.T) {
	const total = 102
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		n := min(limit, total-offset)
		var objects []string
		for i := range n {
			objects = append(objects, fmt.Sprintf(
				`{"originaltargetaliasname": "draft-old-%d", "target": ""}`, offset+i))
		}
		_, _ = fmt.Fprintf(w, `{"meta": {"total_count": %d}, "objects": [%s]}`, total, strings.Join(objects, ","))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	replaced, err := replacedDrafts(context.Background(), ts.Client(), "draft-new")
	if err != nil {
		t.Fatalf("replacedDrafts: %v", err)
	}
	if len(replaced) != total {
		t.Fatalf("len(replaced) = %d, want %d", len(replaced), total)
	}
	if replaced[0] != "draft-old-0" || replaced[total-1] != fmt.Sprintf("draft-old-%d", total-1) {
		t.Errorf("replaced[0], replaced[last] = %q, %q", replaced[0], replaced[total-1])
	}
}

func TestFetchIPR_RejectsInvalidDraftName(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for an invalid draft name")
	}))
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := FetchIPR(context.Background(), ts.Client(), 0, "../../escape"); err == nil {
		t.Error("FetchIPR accepted a name containing ../, want a validation error")
	}
}

func TestFetchIPR_CacheHit(t *testing.T) {
	var iprRequests atomic.Int32
	ts := newIPRTestServer(t, &iprRequests)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := FetchIPR(context.Background(), ts.Client(), 0, "draft-standalone-ipr"); err != nil {
		t.Fatalf("FetchIPR (1st): %v", err)
	}
	if _, err := FetchIPR(context.Background(), ts.Client(), 0, "draft-standalone-ipr"); err != nil {
		t.Fatalf("FetchIPR (2nd): %v", err)
	}
	// 3 disclosure-kind requests for the 1st call only; the 2nd call
	// should be served entirely from the 1h on-disk cache.
	if n := iprRequests.Load(); n != 3 {
		t.Errorf("IPR disclosure requests = %d, want 3 (2nd FetchIPR call should hit the cache)", n)
	}
}
