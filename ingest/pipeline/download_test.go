package pipeline

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTestBaseURL redirects FetchRFCText's default base URL to url for the
// duration of the test.
func withTestBaseURL(t *testing.T, url string) {
	t.Helper()
	orig := rfcTxtBaseURL
	rfcTxtBaseURL = url
	t.Cleanup(func() { rfcTxtBaseURL = orig })
}

func TestFetchRFCText_Success(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/rfc9293.txt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("RFC 9293 body"))
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	rawDir := t.TempDir()
	data, err := FetchRFCText(context.Background(), ts.Client(), 9293, rawDir)
	if err != nil {
		t.Fatalf("FetchRFCText: %v", err)
	}
	if string(data) != "RFC 9293 body" {
		t.Errorf("data = %q", data)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}

	// A rawDir hit must skip the network entirely -- RFC bodies are
	// immutable once published, so the cached copy never goes stale.
	data2, err := FetchRFCText(context.Background(), ts.Client(), 9293, rawDir)
	if err != nil {
		t.Fatalf("FetchRFCText (cached): %v", err)
	}
	if string(data2) != "RFC 9293 body" {
		t.Errorf("cached data = %q", data2)
	}
	if requests != 1 {
		t.Errorf("requests after cache hit = %d, want 1 (no new request)", requests)
	}
}

// TestFetchRFCText_EmptyCachedFileRefetched: a zero-byte rfcN.txt in rawDir
// (a botched write) must be treated as absent -- re-fetched from the network
// and replaced -- rather than returned as the RFC's body forever (issue #10).
func TestFetchRFCText_EmptyCachedFileRefetched(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte("RFC 42 body"))
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	rawDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rawDir, "rfc42.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := FetchRFCText(context.Background(), ts.Client(), 42, rawDir)
	if err != nil {
		t.Fatalf("FetchRFCText: %v", err)
	}
	if string(data) != "RFC 42 body" {
		t.Errorf("data = %q, want the re-fetched body", data)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (empty cache file must not count as a hit)", requests)
	}

	onDisk, err := os.ReadFile(filepath.Join(rawDir, "rfc42.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(onDisk) != "RFC 42 body" {
		t.Errorf("cached file = %q, want the re-fetched body to replace the empty file", onDisk)
	}
}

// TestFetchRFCText_RawWriteLeavesNoTempFiles pins the atomic raw-cache
// write (issue #9): after a fetch, rawDir must contain exactly the final
// rfcN.txt -- the temp file used for the atomic rename must be gone, and
// the content complete.
func TestFetchRFCText_RawWriteLeavesNoTempFiles(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("RFC 1 body"))
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	rawDir := t.TempDir()
	if _, err := FetchRFCText(context.Background(), ts.Client(), 1, rawDir); err != nil {
		t.Fatalf("FetchRFCText: %v", err)
	}

	entries, err := os.ReadDir(rawDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "rfc1.txt" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("rawDir contents = %v, want exactly [rfc1.txt]", names)
	}
	data, err := os.ReadFile(filepath.Join(rawDir, "rfc1.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "RFC 1 body" {
		t.Errorf("cached body = %q, want %q", data, "RFC 1 body")
	}
}

func TestFetchRFCText_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	_, err := FetchRFCText(context.Background(), ts.Client(), 999999, t.TempDir())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFetchRFCText_RetryThenSucceed(t *testing.T) {
	orig := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = orig })

	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("ok after retries"))
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	data, err := FetchRFCText(context.Background(), ts.Client(), 1, t.TempDir())
	if err != nil {
		t.Fatalf("FetchRFCText: %v", err)
	}
	if string(data) != "ok after retries" {
		t.Errorf("data = %q", data)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestFetchRFCText_RetryExhausted(t *testing.T) {
	orig := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = orig })

	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	_, err := FetchRFCText(context.Background(), ts.Client(), 1, t.TempDir())
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want a non-ErrNotFound error", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

// TestFetchRFCText_TruncatedBodyDetected verifies the pipeline half of
// issue #13: when the transfer breaks exactly at the size cap (here: a
// Content-Length promising more bytes than the server delivers), the
// one-byte probe read's error must surface as a fetch failure instead of
// being discarded and the truncated data returned as the complete document.
func TestFetchRFCText_TruncatedBodyDetected(t *testing.T) {
	origSize := maxFetchSize
	maxFetchSize = 10
	t.Cleanup(func() { maxFetchSize = origSize })

	origDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = origDelay })

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Promise 20 bytes but deliver only maxFetchSize (10): ReadAll of the
		// LimitReader succeeds, and the probe read then hits the broken body.
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write([]byte(strings.Repeat("x", 10)))
	}))
	// Silence the server's "wrote less than declared Content-Length" log.
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.Start()
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	_, err := FetchRFCText(context.Background(), ts.Client(), 1, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a body truncated mid-transfer")
	}
	if strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("err = %v, want a read failure, not the size-cap error", err)
	}
	if !strings.Contains(err.Error(), "unexpected EOF") {
		t.Errorf("err = %v, want the probe read's unexpected EOF surfaced", err)
	}
}

func TestFetchRFCText_SizeCap(t *testing.T) {
	origSize := maxFetchSize
	maxFetchSize = 10
	t.Cleanup(func() { maxFetchSize = origSize })

	origDelay := retryBaseDelay
	retryBaseDelay = time.Millisecond
	t.Cleanup(func() { retryBaseDelay = origDelay })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer ts.Close()
	withTestBaseURL(t, ts.URL)

	_, err := FetchRFCText(context.Background(), ts.Client(), 1, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a response exceeding maxFetchSize")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("err = %v, want a size-exceeded error", err)
	}
}
