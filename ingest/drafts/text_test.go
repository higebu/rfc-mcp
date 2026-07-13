package drafts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestFetchText_Success(t *testing.T) {
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/draft-ietf-quic-transport-34.txt", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("draft body"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	data, err := FetchText(context.Background(), ts.Client(), "draft-ietf-quic-transport", "34")
	if err != nil {
		t.Fatalf("FetchText: %v", err)
	}
	if string(data) != "draft body" {
		t.Errorf("data = %q", data)
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}

	// A cache hit must skip the network entirely -- a specific revision's
	// body is immutable once submitted, so it never goes stale.
	data2, err := FetchText(context.Background(), ts.Client(), "draft-ietf-quic-transport", "34")
	if err != nil {
		t.Fatalf("FetchText (cached): %v", err)
	}
	if string(data2) != "draft body" {
		t.Errorf("cached data = %q", data2)
	}
	if requests.Load() != 1 {
		t.Errorf("requests after cache hit = %d, want 1 (no new request)", requests.Load())
	}
}

func TestFetchText_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	_, err := FetchText(context.Background(), ts.Client(), "draft-does-not-exist", "00")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchText_DifferentRevisionsCachedSeparately(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/draft-foo-01.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("rev 01"))
	})
	mux.HandleFunc("/draft-foo-02.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("rev 02"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	data1, err := FetchText(context.Background(), ts.Client(), "draft-foo", "01")
	if err != nil {
		t.Fatalf("FetchText(01): %v", err)
	}
	data2, err := FetchText(context.Background(), ts.Client(), "draft-foo", "02")
	if err != nil {
		t.Fatalf("FetchText(02): %v", err)
	}
	if string(data1) != "rev 01" || string(data2) != "rev 02" {
		t.Errorf("data1 = %q, data2 = %q, want distinct per-revision bodies", data1, data2)
	}
}
