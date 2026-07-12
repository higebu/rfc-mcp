package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
