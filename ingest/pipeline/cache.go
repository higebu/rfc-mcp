// Package pipeline orchestrates fetching rfc-index.xml, errata.json, and
// per-RFC plain-text bodies from the RFC Editor, and storing them via db.DB.
package pipeline

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// defaultCacheTTL is the default cache time-to-live for rfc-index.xml and
// errata.json. Override via RFC_MCP_CACHE_TTL_HOURS.
var defaultCacheTTL = func() time.Duration {
	if v := os.Getenv("RFC_MCP_CACHE_TTL_HOURS"); v != "" {
		if hours, err := strconv.Atoi(v); err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour
		}
	}
	return 24 * time.Hour
}()

// CacheDir returns the cache directory path: $XDG_CACHE_HOME/rfc-mcp if
// set, otherwise ~/.cache/rfc-mcp. Exported so cmd/rfc-mcp can derive its
// default --raw-dir from the same root.
func CacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "rfc-mcp"), nil
}

// loadCache reads a cached file's contents and modification time if present
// and within ttl. Returns nil data (and a zero time) on a cache miss
// (missing file or expired) rather than an error, so callers can treat
// "miss" and "fetch fresh" uniformly. The modification time lets callers
// record when the cached data was actually obtained (see
// Pipeline.fetchCached) rather than the time of this read.
func loadCache(key string, ttl time.Duration) ([]byte, time.Time, error) {
	dir, err := CacheDir()
	if err != nil {
		return nil, time.Time{}, nil //nolint:nilerr // cache is best-effort; fall through to a live fetch
	}

	path := filepath.Join(dir, key)
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, nil // file doesn't exist
	}
	if time.Since(info.ModTime()) > ttl {
		return nil, time.Time{}, nil // expired
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, nil
	}

	log.Printf("Cache hit: %s (%d bytes)", key, len(data))
	return data, info.ModTime(), nil
}

// writeFileAtomic writes data to dir/name via a temp file in the same
// directory plus os.Rename, so a crash or full disk mid-write can never
// leave a truncated file behind at the final path. Shared by saveCache
// (rfc-index.xml/errata.json) and fetchRFCText's raw-body cache, both of
// which serve any existing file forever and so must never persist a
// partial write.
func writeFileAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
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

	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

// saveCache writes data to a cache file atomically via rename.
func saveCache(key string, data []byte) error {
	dir, err := CacheDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	if err := writeFileAtomic(dir, key, data); err != nil {
		return fmt.Errorf("write cache file %s: %w", key, err)
	}

	log.Printf("Cache saved: %s (%d bytes)", key, len(data))
	return nil
}
