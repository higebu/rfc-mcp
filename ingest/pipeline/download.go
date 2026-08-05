package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// defaultRFCEditorRoot is the root of the RFC Editor's site. rfc-index.xml
// and errata.json live directly under it; RFC txt bodies live under /rfc/.
// Live-verified 2026-07-11: bulk tarballs (RFC-all.tar.gz) 404, so each RFC
// is fetched individually; HEAD requests are rejected (JSON 404 body), so
// every request here is a GET.
const defaultRFCEditorRoot = "https://www.rfc-editor.org"

// rfcTxtBaseURL is the root FetchRFCText downloads rfcN.txt from. It's a
// package-level var (rather than a FetchRFCText parameter, whose signature
// is fixed) so download_test.go can redirect it to an httptest.Server;
// Pipeline.BaseURL provides the equivalent override for Pipeline's own
// fetches (see pipeline.go), which take the base URL as an explicit
// argument instead of going through this var.
var rfcTxtBaseURL = defaultRFCEditorRoot + "/rfc"

// defaultMaxTxtSizeMB is also reused as the cap for rfc-index.xml (13.6 MB
// live) and errata.json (11.5 MB live) fetches, rather than adding a
// separate env var for what is, in both cases, a decompression-bomb safety
// net rather than a real expected limit.
const defaultMaxTxtSizeMB = 20

// maxFetchSize caps any single HTTP GET response body read by this
// package. Configurable via RFC_MCP_MAX_TXT_SIZE_MB.
var maxFetchSize = int64(defaultMaxTxtSizeMB) << 20

func init() {
	if v := os.Getenv("RFC_MCP_MAX_TXT_SIZE_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			maxFetchSize = mb << 20
		}
	}
}

// Version is embedded in the User-Agent header sent with every request.
// cmd/rfc-mcp sets it from the build-time version at startup.
var Version = "dev"

// ErrNotFound indicates the RFC Editor returned 404 for a requested RFC
// text (a not-issued number, or one too new to be published yet). Callers
// fetching a specific RFC's body should skip it gracefully rather than
// treat this as fatal; rfc-index.xml and errata.json are expected to
// always exist, so a 404 fetching those is still a hard error.
var ErrNotFound = errors.New("rfc text not found")

// FetchRFCText returns the plain-text body of the given RFC number. A copy
// already on disk under rawDir is returned without a network request --
// RFC bodies are immutable once published, so a cached file is valid
// forever and needs no TTL or conditional GET.
func FetchRFCText(ctx context.Context, client *http.Client, number int, rawDir string) ([]byte, error) {
	return fetchRFCText(ctx, client, number, rawDir, rfcTxtBaseURL)
}

func fetchRFCText(ctx context.Context, client *http.Client, number int, rawDir, baseURL string) ([]byte, error) {
	name := fmt.Sprintf("rfc%d.txt", number)

	if rawDir != "" {
		// A zero-byte file is a botched write, not a cached body (no RFC's
		// plain text is empty): treat it as absent and re-fetch.
		if data, err := os.ReadFile(filepath.Join(rawDir, name)); err == nil && len(data) > 0 {
			return data, nil
		}
	}

	data, err := httpGetWithRetry(ctx, client, baseURL+"/"+name)
	if err != nil {
		return nil, err
	}

	if rawDir != "" {
		if err := os.MkdirAll(rawDir, 0o755); err != nil {
			return nil, fmt.Errorf("create raw dir: %w", err)
		}
		// Atomic write (temp file + rename): the read path above trusts any
		// existing rfcN.txt forever, so a truncated partial write must never
		// become visible at the final path.
		if err := writeFileAtomic(rawDir, name, data); err != nil {
			return nil, fmt.Errorf("write raw file: %w", err)
		}
	}
	return data, nil
}

// retryBaseDelay scales the exponential backoff between retry attempts
// (2x, 4x this value). It's a package-level var, rather than a constant,
// so download_test.go can shrink it and keep the retry tests fast.
var retryBaseDelay = time.Second

// httpGetWithRetry performs an HTTP GET with up to 3 attempts and
// exponential backoff (2x, 4x retryBaseDelay) between them, mirroring
// 3gpp-mcp's download retry shape. A 404 response returns ErrNotFound
// immediately without retrying, since retrying cannot change a
// "does not exist" response.
func httpGetWithRetry(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}

	var lastErr error
	for attempt := range 3 {
		data, err := httpGetOnce(ctx, client, url)
		if err == nil {
			return data, nil
		}
		if errors.Is(err, ErrNotFound) {
			return nil, err
		}
		lastErr = err
		if attempt < 2 {
			select {
			case <-time.After(time.Duration(1<<uint(attempt+1)) * retryBaseDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, fmt.Errorf("GET %s failed after 3 attempts: %w", url, lastErr)
}

func httpGetOnce(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("rfc-mcp/%s (+https://github.com/higebu/rfc-mcp)", Version))

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchSize))
	if err != nil {
		return nil, err
	}

	// Detect truncation: if we can read one more byte, the response exceeded the cap.
	var extra [1]byte
	if n, _ := resp.Body.Read(extra[:]); n > 0 {
		return nil, fmt.Errorf("%s exceeds maximum size of %d MB", url, maxFetchSize>>20)
	}
	return data, nil
}
