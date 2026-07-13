package drafts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// withTestRoots redirects DatatrackerRoot/ArchiveRoot to url for the
// duration of the test, mirroring ingest/pipeline's withTestBaseURL.
func withTestRoots(t *testing.T, url string) {
	t.Helper()
	origDT, origArchive := DatatrackerRoot, ArchiveRoot
	DatatrackerRoot = url
	ArchiveRoot = url
	t.Cleanup(func() {
		DatatrackerRoot = origDT
		ArchiveRoot = origArchive
	})
}

const searchResponseBody = `{
	"meta": {"limit": 20, "next": null, "offset": 0, "previous": null, "total_count": 2},
	"objects": [
		{"name": "draft-ietf-quic-multipath", "rev": "21", "title": "Managing multiple paths for a QUIC connection", "expires": "2026-09-18T09:40:37Z", "pages": 42, "rfc": null, "states": ["/api/v1/doc/state/1/"]},
		{"name": "draft-ietf-quic-transport", "rev": "34", "title": "QUIC: A UDP-Based Multiplexed and Secure Transport", "expires": "2021-07-19T02:14:40Z", "pages": 151, "rfc": null, "states": ["/api/v1/doc/state/3/"]}
	]
}`

const becameRFCResponseBody = `{
	"meta": {"limit": 20, "next": null, "offset": 0, "previous": null, "total_count": 1},
	"objects": [{"originaltargetaliasname": "rfc9000", "relationship": "/api/v1/name/docrelationshipname/became_rfc/"}]
}`

func TestSearchDrafts_Basic(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(searchResponseBody))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(becameRFCResponseBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{Query: "quic"})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", result.TotalCount)
	}
	if len(result.Drafts) != 2 {
		t.Fatalf("len(Drafts) = %d, want 2", len(result.Drafts))
	}
	if result.Drafts[0].Name != "draft-ietf-quic-multipath" || result.Drafts[0].RFC != 0 {
		t.Errorf("Drafts[0] = %+v, want RFC unset (state 1 is not the rfc state)", result.Drafts[0])
	}
	if result.Drafts[1].Name != "draft-ietf-quic-transport" || result.Drafts[1].RFC != 9000 {
		t.Errorf("Drafts[1] = %+v, want RFC 9000 (state 3 triggers became_rfc lookup)", result.Drafts[1])
	}

	if !strings.Contains(gotQuery, "title__icontains=quic") {
		t.Errorf("query = %q, want title__icontains=quic", gotQuery)
	}
	if !strings.Contains(gotQuery, "states__slug=active") {
		t.Errorf("query = %q, want states__slug=active by default", gotQuery)
	}
}

func TestSearchDrafts_IncludeExpired(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	if _, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{IncludeExpired: true}); err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if strings.Contains(gotQuery, "states__slug=active") {
		t.Errorf("query = %q, want states__slug filter dropped when IncludeExpired", gotQuery)
	}
	if !strings.Contains(gotQuery, "states__type=draft") {
		t.Errorf("query = %q, want states__type=draft retained", gotQuery)
	}
}

func TestSearchDrafts_Filters(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	_, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{
		NameContains: "quic-transport",
		Group:        "quic",
		Limit:        5,
		Offset:       10,
	})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	for _, want := range []string{"name__contains=quic-transport", "group__acronym=quic", "limit=5", "offset=10"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, want to contain %q", gotQuery, want)
		}
	}
}

func TestSearchDrafts_DefaultLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if result.Limit != 20 {
		t.Errorf("Limit = %d, want default 20", result.Limit)
	}
}

// TestSearchDrafts_SingleTokenUnchanged covers (e) from the multi-word
// AND-match design: a single-token Query must still take the original
// server-side-only path (default limit=20, no client-side pagination
// walk, Truncated never set), not searchDraftsMultiWord.
func TestSearchDrafts_SingleTokenUnchanged(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"meta": {"total_count": 1}, "objects": [
			{"name": "draft-x", "rev": "01", "title": "Quic thing", "expires": "", "pages": 1, "rfc": null, "states": []}
		]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{Query: "quic"})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if result.Truncated {
		t.Errorf("single-token search should never report Truncated")
	}
	if !strings.Contains(gotQuery, "limit=20") {
		t.Errorf("query = %q, want the single-filter default limit=20 (multi-word path uses page size %d)", gotQuery, multiWordPageSize)
	}
}

// TestSearchDrafts_MultiWordAndMatch covers (a) and (b): a multi-word
// query like "BGP MUP SAFI" must match a title containing all three
// words in any order (draft-ietf-bess-mup-safi's actual title, which
// never contains the literal phrase "BGP MUP SAFI"), filter out a title
// that only contains the server-filtered token, and issue the server-side
// title__icontains on the longest token ("SAFI", 4 chars, beats "BGP"/
// "MUP" at 3).
func TestSearchDrafts_MultiWordAndMatch(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"meta": {"total_count": 2}, "objects": [
			{"name": "draft-ietf-bess-mup-safi", "rev": "05", "title": "BGP Extensions for the Mobile User Plane (MUP) SAFI", "expires": "", "pages": 20, "rfc": null, "states": ["/api/v1/doc/state/1/"]},
			{"name": "draft-other-safi-thing", "rev": "01", "title": "Some other SAFI extension", "expires": "", "pages": 5, "rfc": null, "states": ["/api/v1/doc/state/1/"]}
		]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{Query: "BGP MUP SAFI"})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if !strings.Contains(gotQuery, "title__icontains=SAFI") {
		t.Errorf("query = %q, want the longest token (SAFI) sent as title__icontains", gotQuery)
	}
	if result.TotalCount != 1 || len(result.Drafts) != 1 {
		t.Fatalf("result = %+v, want exactly 1 match (the title lacking BGP/MUP must be filtered out client-side)", result)
	}
	if result.Drafts[0].Name != "draft-ietf-bess-mup-safi" {
		t.Errorf("Drafts[0].Name = %q, want draft-ietf-bess-mup-safi", result.Drafts[0].Name)
	}
	if result.Truncated {
		t.Errorf("Truncated = true, want false (scan finished within the cap)")
	}
}

// TestSearchDrafts_MultiWordOffsetLimit covers (c): offset/limit must be
// applied to the client-side-filtered match list, and TotalCount must
// reflect the filtered count, not the server's raw total_count.
func TestSearchDrafts_MultiWordOffsetLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		var objects []string
		for i := range 5 {
			objects = append(objects, fmt.Sprintf(
				`{"name": "draft-foo-bar-%d", "rev": "01", "title": "Foo Bar item %d", "expires": "", "pages": 1, "rfc": null, "states": []}`, i, i))
		}
		_, _ = fmt.Fprintf(w, `{"meta": {"total_count": 5}, "objects": [%s]}`, strings.Join(objects, ","))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{Query: "foo bar", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if result.TotalCount != 5 {
		t.Errorf("TotalCount = %d, want 5 (all 5 match both tokens)", result.TotalCount)
	}
	if len(result.Drafts) != 2 {
		t.Fatalf("len(Drafts) = %d, want 2 (Limit)", len(result.Drafts))
	}
	if result.Drafts[0].Name != "draft-foo-bar-1" || result.Drafts[1].Name != "draft-foo-bar-2" {
		t.Errorf("Drafts = %+v, want items at index 1,2 (Offset 1, Limit 2)", result.Drafts)
	}
	if result.Truncated {
		t.Errorf("Truncated = true, want false")
	}
}

// TestSearchDrafts_MultiWordTruncated covers (d): when the server-side
// result set exceeds multiWordScanCap, searchDraftsMultiWord must stop
// scanning at the cap (bounding worst-case requests to
// multiWordScanCap/multiWordPageSize) and report Truncated so callers
// know TotalCount is a floor.
func TestSearchDrafts_MultiWordTruncated(t *testing.T) {
	const serverTotal = 650
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		requests++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		n := min(limit, serverTotal-offset)
		var objects []string
		for i := range n {
			objects = append(objects, fmt.Sprintf(
				`{"name": "draft-x-%d", "rev": "01", "title": "Extra keyword draft %d", "expires": "", "pages": 1, "rfc": null, "states": []}`,
				offset+i, offset+i))
		}
		_, _ = fmt.Fprintf(w, `{"meta": {"total_count": %d}, "objects": [%s]}`, serverTotal, strings.Join(objects, ","))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)

	result, err := SearchDrafts(context.Background(), ts.Client(), SearchParams{Query: "keyword extra"})
	if err != nil {
		t.Fatalf("SearchDrafts: %v", err)
	}
	if !result.Truncated {
		t.Errorf("Truncated = false, want true (server total %d exceeds scan cap %d)", serverTotal, multiWordScanCap)
	}
	if result.TotalCount != multiWordScanCap {
		t.Errorf("TotalCount = %d, want %d (every scanned object matched both tokens)", result.TotalCount, multiWordScanCap)
	}
	wantRequests := multiWordScanCap / multiWordPageSize
	if requests != wantRequests {
		t.Errorf("requests = %d, want %d (scan must stop at the cap, not walk all %d server pages)", requests, wantRequests, (serverTotal+multiWordPageSize-1)/multiWordPageSize)
	}
}
