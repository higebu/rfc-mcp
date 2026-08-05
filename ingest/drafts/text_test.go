package drafts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestFetchText_RejectsTraversalName is a regression test for cache path
// traversal: a name containing "../" used to be concatenated straight
// into filepath.Join(cacheRoot, name+"-"+rev+".txt"), escaping the cache
// root, and into the archive URL. It must now be rejected before any
// network request or file write.
func TestFetchText_RejectsTraversalName(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("evil body"))
	}))
	defer ts.Close()
	withTestRoots(t, ts.URL)

	base := t.TempDir()
	cacheHome := filepath.Join(base, "cache")
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	name := "../../escape"
	if _, err := FetchText(context.Background(), ts.Client(), name, "00"); err == nil {
		t.Fatal("FetchText accepted a name containing ../, want a validation error")
	}
	if requests.Load() != 0 {
		t.Errorf("requests = %d, want 0 (rejected before any fetch)", requests.Load())
	}
	// The traversal target relative to the cache root
	// (<cacheHome>/rfc-mcp/drafts/../../escape-00.txt = <cacheHome>/escape-00.txt)
	// must not exist, nor anything else outside the cache root.
	if _, err := os.Stat(filepath.Join(cacheHome, "escape-00.txt")); !os.IsNotExist(err) {
		t.Errorf("a file escaped the cache root: %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "cache" {
			t.Errorf("unexpected entry %q created outside the cache root", e.Name())
		}
	}
}

func TestFetchText_NormalizesSingleDigitRev(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/draft-foo-03.txt", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("rev 03"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	data, err := FetchText(context.Background(), ts.Client(), "draft-foo", "3")
	if err != nil {
		t.Fatalf("FetchText: %v", err)
	}
	if string(data) != "rev 03" || gotPath != "/draft-foo-03.txt" {
		t.Errorf("data = %q, path = %q, want the zero-padded revision fetched", data, gotPath)
	}
}

func TestFetchText_RejectsInvalidRev(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be made for an invalid revision")
	}))
	defer ts.Close()
	withTestRoots(t, ts.URL)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, err := FetchText(context.Background(), ts.Client(), "draft-foo", "../03"); err == nil {
		t.Error("FetchText accepted revision \"../03\", want a validation error")
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
