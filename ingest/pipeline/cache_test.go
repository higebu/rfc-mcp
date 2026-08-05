package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempCacheDir points CacheDir/loadCache/saveCache at a fresh temp
// directory for the duration of the test by overriding XDG_CACHE_HOME.
func withTempCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

func TestCacheDir(t *testing.T) {
	base := withTempCacheDir(t)

	dir, err := CacheDir()
	if err != nil {
		t.Fatalf("CacheDir: %v", err)
	}
	want := filepath.Join(base, "rfc-mcp")
	if dir != want {
		t.Errorf("CacheDir() = %q, want %q", dir, want)
	}
}

// TestLoadCache_EmptyFile: a zero-byte cache file (e.g. a botched write) is
// a miss, not a hit -- otherwise the pipeline would parse empty data instead
// of falling through to a live fetch (issue #10).
func TestLoadCache_EmptyFile(t *testing.T) {
	dir := withTempCacheDir(t)

	cacheSubdir := filepath.Join(dir, "rfc-mcp")
	if err := os.MkdirAll(cacheSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheSubdir, "test.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	data, mtime, err := loadCache("test.txt", time.Hour)
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if data != nil {
		t.Errorf("loadCache on empty file = %q, want nil (cache miss)", data)
	}
	if !mtime.IsZero() {
		t.Errorf("loadCache mtime on empty file = %v, want zero", mtime)
	}
}

func TestSaveAndLoadCache(t *testing.T) {
	withTempCacheDir(t)

	if data, mtime, err := loadCache("test.txt", time.Hour); err != nil || data != nil || !mtime.IsZero() {
		t.Fatalf("loadCache on miss = (%v, %v, %v), want (nil, zero, nil)", data, mtime, err)
	}

	before := time.Now()
	want := []byte("hello world")
	if err := saveCache("test.txt", want); err != nil {
		t.Fatalf("saveCache: %v", err)
	}

	got, mtime, err := loadCache("test.txt", time.Hour)
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("loadCache = %q, want %q", got, want)
	}
	if mtime.Before(before.Add(-time.Second)) {
		t.Errorf("loadCache mtime = %v, want at or after %v", mtime, before)
	}
}

// TestWriteFileAtomic_Mode: the temp file os.CreateTemp creates is 0600
// and os.Rename preserves that, so writeFileAtomic must chmod to 0644
// before the rename -- otherwise cached files silently become owner-only
// readable, breaking shared cache/raw-dir setups.
func TestWriteFileAtomic_Mode(t *testing.T) {
	dir := t.TempDir()

	if err := writeFileAtomic(dir, "test.txt", []byte("hello")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "test.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm() & 0o777; got != 0o644 {
		t.Errorf("file mode = %o, want 644", got)
	}
}

func TestLoadCache_Expired(t *testing.T) {
	dir := withTempCacheDir(t)

	cacheSubdir := filepath.Join(dir, "rfc-mcp")
	if err := os.MkdirAll(cacheSubdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cacheSubdir, "test.txt")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	data, mtime, err := loadCache("test.txt", time.Hour)
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if data != nil {
		t.Errorf("loadCache on expired file = %q, want nil (cache miss)", data)
	}
	if !mtime.IsZero() {
		t.Errorf("loadCache mtime on expired file = %v, want zero", mtime)
	}
}
