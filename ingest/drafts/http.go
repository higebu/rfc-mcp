package drafts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"
)

// Version is embedded in the User-Agent header sent with every request.
// cmd/rfc-mcp sets it from the build-time version at startup, mirroring
// ingest/pipeline.Version.
var Version = "dev"

// ErrNotFound indicates the Datatracker or archive returned 404 for a
// requested draft name or revision -- an unknown draft name, or a
// revision that was never submitted. Wrapped with the attempted URL so
// callers get a self-explanatory error message for free.
var ErrNotFound = errors.New("draft not found")

// defaultMaxTxtSizeMB mirrors ingest/pipeline's default and env var, so a
// single RFC_MCP_MAX_TXT_SIZE_MB setting caps both RFC and draft fetches.
const defaultMaxTxtSizeMB = 20

var maxFetchSize = int64(defaultMaxTxtSizeMB) << 20

func init() {
	if v := os.Getenv("RFC_MCP_MAX_TXT_SIZE_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
			maxFetchSize = mb << 20
		}
	}
}

// retryBaseDelay scales the exponential backoff between retry attempts
// (2x, 4x this value). It's a package-level var, rather than a constant,
// so tests can shrink it and keep retry tests fast.
var retryBaseDelay = time.Second

// httpGetWithRetry performs an HTTP GET with up to 3 attempts and
// exponential backoff (2x, 4x retryBaseDelay) between them, mirroring
// ingest/pipeline's retry shape. A 404 response returns an error wrapping
// ErrNotFound immediately, since retrying cannot change a "does not
// exist" response.
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
		return nil, fmt.Errorf("%w: %s", ErrNotFound, url)
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
