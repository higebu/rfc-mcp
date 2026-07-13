package tools

import "testing"

// TestRfcRangeHint_WithoutBuiltAt covers the wording used by every
// pre-existing "not found" assertion across this package's tests
// (testutil.SeedData never sets built_at): it must stay byte-for-byte
// unchanged from before the built_at extension.
func TestRfcRangeHint_WithoutBuiltAt(t *testing.T) {
	d := setupTestDB(t)

	got := rfcRangeHint(d)
	want := " (valid RFC numbers range from 1 to 9293)"
	if got != want {
		t.Errorf("rfcRangeHint() = %q, want %q", got, want)
	}
}

func TestRfcRangeHint_WithBuiltAt(t *testing.T) {
	d := setupTestDB(t)
	if err := d.SetMeta("built_at", "2026-07-12T03:04:05Z"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	got := rfcRangeHint(d)
	want := " (valid RFC numbers range from 1 to 9293; database built 2026-07-12)"
	if got != want {
		t.Errorf("rfcRangeHint() = %q, want %q", got, want)
	}
}

// TestRfcRangeHint_MalformedBuiltAt covers a corrupted meta value: the hint
// must degrade to the unchanged range-only wording rather than surfacing a
// parse error.
func TestRfcRangeHint_MalformedBuiltAt(t *testing.T) {
	d := setupTestDB(t)
	if err := d.SetMeta("built_at", "not-a-timestamp"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}

	got := rfcRangeHint(d)
	want := " (valid RFC numbers range from 1 to 9293)"
	if got != want {
		t.Errorf("rfcRangeHint() = %q, want %q", got, want)
	}
}
