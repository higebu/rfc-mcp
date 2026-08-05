package drafts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/higebu/rfc-mcp/ingest/pipeline"
)

// cacheRoot returns pipeline's shared XDG cache directory joined with
// "drafts", the root both permanent per-revision text bodies (text.go)
// and short-TTL per-name metadata (metadata.go) are cached under.
func cacheRoot() (string, error) {
	base, err := pipeline.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "drafts"), nil
}

// loadCache reads a cached file's contents if present and within ttl.
// ttl <= 0 means the entry never expires (used for immutable per-revision
// text bodies). Returns nil, nil on a cache miss (missing file or
// expired) rather than an error, so callers can treat "miss" and "fetch
// fresh" uniformly.
func loadCache(relPath string, ttl time.Duration) ([]byte, error) {
	root, err := cacheRoot()
	if err != nil {
		return nil, nil //nolint:nilerr // cache is best-effort; fall through to a live fetch
	}

	path := filepath.Join(root, relPath)
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil // file doesn't exist
	}
	if ttl > 0 && time.Since(info.ModTime()) > ttl {
		return nil, nil // expired
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// The file vanished between the Stat above and this read (e.g. a
		// concurrent cache cleanup): that's a miss per this function's
		// contract, not an error.
		return nil, nil
	}
	return data, err
}

// saveCache writes data to a cache file atomically via rename.
func saveCache(relPath string, data []byte) error {
	root, err := cacheRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, relPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename cache file: %w", err)
	}
	return nil
}
