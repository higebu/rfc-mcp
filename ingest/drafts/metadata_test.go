package drafts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const notPublishedMetaBody = `{
	"name": "draft-ietf-quic-multipath", "rev": "21",
	"title": "Managing multiple paths for a QUIC connection",
	"abstract": "  This document specifies a multipath extension.  \n",
	"pages": 42, "time": "2026-05-20T21:19:07Z", "expires": "2026-09-18T09:40:37Z",
	"rfc": null, "rfc_number": null,
	"states": ["/api/v1/doc/state/1/"]
}`

const publishedMetaBody = `{
	"name": "draft-ietf-quic-transport", "rev": "34",
	"title": "QUIC: A UDP-Based Multiplexed and Secure Transport",
	"abstract": "This document defines the core of the QUIC transport protocol.",
	"pages": 151, "time": "2022-02-19T08:46:51Z", "expires": "2021-07-19T02:14:40Z",
	"rfc": null, "rfc_number": null,
	"states": ["/api/v1/doc/state/3/"]
}`

func TestFetchMetadata_NotPublished(t *testing.T) {
	var metaRequests, relatedRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-ietf-quic-multipath/", func(w http.ResponseWriter, r *http.Request) {
		metaRequests.Add(1)
		_, _ = w.Write([]byte(notPublishedMetaBody))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		relatedRequests.Add(1)
		_, _ = w.Write([]byte(becameRFCResponseBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	meta, err := FetchMetadata(context.Background(), ts.Client(), "draft-ietf-quic-multipath")
	if err != nil {
		t.Fatalf("FetchMetadata: %v", err)
	}
	if meta.Rev != "21" || meta.Pages != 42 {
		t.Errorf("meta = %+v", meta)
	}
	if meta.Abstract != "This document specifies a multipath extension." {
		t.Errorf("Abstract = %q, want trimmed", meta.Abstract)
	}
	if meta.RFC != 0 {
		t.Errorf("RFC = %d, want 0 (state 1 is not the rfc state, so no became_rfc lookup)", meta.RFC)
	}
	if relatedRequests.Load() != 0 {
		t.Errorf("became_rfc requests = %d, want 0", relatedRequests.Load())
	}
}

func TestFetchMetadata_Published(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-ietf-quic-transport/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(publishedMetaBody))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("source__name"); got != "draft-ietf-quic-transport" {
			t.Errorf("source__name = %q", got)
		}
		_, _ = w.Write([]byte(becameRFCResponseBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	meta, err := FetchMetadata(context.Background(), ts.Client(), "draft-ietf-quic-transport")
	if err != nil {
		t.Fatalf("FetchMetadata: %v", err)
	}
	if meta.RFC != 9000 {
		t.Errorf("RFC = %d, want 9000", meta.RFC)
	}
	if meta.Expires != "2021-07-19T02:14:40Z" {
		t.Errorf("Expires = %q, want the (already past) draft expiry surfaced plainly", meta.Expires)
	}
}

// TestFetchMetadata_PublishedNullAlias covers the modern Datatracker row
// shape: became_rfc rows for every RFC published since the docalias
// removal (live-verified: rfc9793, rfc10014) carry
// originaltargetaliasname null, naming the RFC only via the target URI.
func TestFetchMetadata_PublishedNullAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-ietf-quic-transport/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(publishedMetaBody))
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 1}, "objects": [
			{"originaltargetaliasname": null, "target": "/api/v1/doc/document/rfc9000/"}
		]}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	meta, err := FetchMetadata(context.Background(), ts.Client(), "draft-ietf-quic-transport")
	if err != nil {
		t.Fatalf("FetchMetadata: %v", err)
	}
	if meta.RFC != 9000 {
		t.Errorf("RFC = %d, want 9000 (derived from the target URI when the alias is null)", meta.RFC)
	}
}

func TestFetchMetadata_CacheHit(t *testing.T) {
	var metaRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-ietf-quic-multipath/", func(w http.ResponseWriter, r *http.Request) {
		metaRequests.Add(1)
		_, _ = w.Write([]byte(notPublishedMetaBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := FetchMetadata(context.Background(), ts.Client(), "draft-ietf-quic-multipath"); err != nil {
		t.Fatalf("FetchMetadata (1st): %v", err)
	}
	if _, err := FetchMetadata(context.Background(), ts.Client(), "draft-ietf-quic-multipath"); err != nil {
		t.Fatalf("FetchMetadata (2nd): %v", err)
	}
	if n := metaRequests.Load(); n != 1 {
		t.Errorf("metadata requests = %d, want 1 (2nd call should hit the 1h cache)", n)
	}
}

func TestFetchMetadata_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/doc/document/draft-does-not-exist/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	_, err := FetchMetadata(context.Background(), ts.Client(), "draft-does-not-exist")
	if err == nil {
		t.Fatal("expected an error for an unknown draft")
	}
	if !strings.Contains(err.Error(), "draft-does-not-exist") {
		t.Errorf("err = %v, want it to name the attempted draft/URL", err)
	}
}
