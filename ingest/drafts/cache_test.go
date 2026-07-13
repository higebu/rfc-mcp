package drafts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", base)

	root, err := cacheRoot()
	if err != nil {
		t.Fatalf("cacheRoot: %v", err)
	}
	want := filepath.Join(base, "rfc-mcp", "drafts")
	if root != want {
		t.Errorf("cacheRoot() = %q, want %q", root, want)
	}
}

func TestSaveAndLoadCache_NeverExpires(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if data, err := loadCache("draft-foo-01.txt", 0); err != nil || data != nil {
		t.Fatalf("loadCache on miss = (%v, %v), want (nil, nil)", data, err)
	}

	want := []byte("draft body")
	if err := saveCache("draft-foo-01.txt", want); err != nil {
		t.Fatalf("saveCache: %v", err)
	}

	got, err := loadCache("draft-foo-01.txt", 0)
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("loadCache = %q, want %q", got, want)
	}
}

func TestLoadCache_TTLExpired(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	cacheDir := filepath.Join(dir, "rfc-mcp", "drafts", "meta")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cacheDir, "draft-foo.json")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	data, err := loadCache("meta/draft-foo.json", time.Hour)
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if data != nil {
		t.Errorf("loadCache on expired entry = %q, want nil (cache miss)", data)
	}
}
