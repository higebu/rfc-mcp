package drafts

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// setMaxFetchSize shrinks the package-level fetch cap for a test and
// restores it on cleanup, so size-cap tests don't need 20 MB bodies.
func setMaxFetchSize(t *testing.T, n int64) {
	t.Helper()
	orig := maxFetchSize
	maxFetchSize = n
	t.Cleanup(func() { maxFetchSize = orig })
}

func TestHTTPGetOnce_ExactCapSizeSucceeds(t *testing.T) {
	setMaxFetchSize(t, 8)
	body := bytes.Repeat([]byte("a"), 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	data, err := httpGetOnce(context.Background(), ts.Client(), ts.URL)
	if err != nil {
		t.Fatalf("httpGetOnce: %v (a body of exactly maxFetchSize bytes must succeed)", err)
	}
	if !bytes.Equal(data, body) {
		t.Errorf("data = %q, want %q", data, body)
	}
}

func TestHTTPGetOnce_OverCapFails(t *testing.T) {
	setMaxFetchSize(t, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), 9))
	}))
	defer ts.Close()

	_, err := httpGetOnce(context.Background(), ts.Client(), ts.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("err = %v, want an exceeds-maximum-size error", err)
	}
}

// TestHTTPGetOnce_ProbeReadErrorSurfaces is a regression test: the
// one-byte probe read after ReadAll(LimitReader(...)) used to discard its
// error, so a body cut off mid-transfer (here: Content-Length promises
// more bytes than the server sends) was silently returned as if complete.
// A non-EOF probe error must surface as a fetch failure.
func TestHTTPGetOnce_ProbeReadErrorSurfaces(t *testing.T) {
	setMaxFetchSize(t, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 8))
		// Handler returns without writing the promised 20 bytes; the
		// client's next read sees an unexpected EOF.
	}))
	defer ts.Close()

	_, err := httpGetOnce(context.Background(), ts.Client(), ts.URL)
	if err == nil {
		t.Fatal("err = nil, want the truncated-body read error surfaced")
	}
	if strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("err = %v, want a read error, not the size-cap error", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("read %s", ts.URL)) {
		t.Errorf("err = %v, want it to name the attempted URL", err)
	}
}
