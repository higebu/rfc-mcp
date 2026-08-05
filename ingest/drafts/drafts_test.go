package drafts

import "testing"

// TestDraftRFCStateContract is a guard test pinning the external contract
// with the IETF Datatracker that gates every became_rfc lookup: the
// draft-lifecycle "rfc" state has numeric ID 3, and a document's states
// list carries it as a resource URI ending in "/state/3/". If the
// Datatracker ever changes either half, published drafts silently stop
// resolving to RFC numbers -- this test makes any change to the assumed
// contract deliberate and visible.
func TestDraftRFCStateContract(t *testing.T) {
	if draftRFCStateID != "3" {
		t.Errorf("draftRFCStateID = %q, want \"3\" (type=draft, slug=rfc per /api/v1/doc/state/)", draftRFCStateID)
	}

	// The exact URI shape the Datatracker serves in a document's "states".
	if !hasState([]string{"/api/v1/doc/state/3/"}, draftRFCStateID) {
		t.Error(`hasState(["/api/v1/doc/state/3/"], "3") = false, want true`)
	}
	// Other states alongside it must not hide it.
	if !hasState([]string{"/api/v1/doc/state/1/", "/api/v1/doc/state/3/"}, draftRFCStateID) {
		t.Error("hasState must find the rfc state among other states")
	}

	// IDs that merely end in the same digit must not match.
	for _, states := range [][]string{
		{"/api/v1/doc/state/13/"},
		{"/api/v1/doc/state/103/"},
		{"/api/v1/doc/state/1/"},
		{"/api/v1/doc/state/3/extra/"},
		{},
		nil,
	} {
		if hasState(states, draftRFCStateID) {
			t.Errorf("hasState(%v, %q) = true, want false", states, draftRFCStateID)
		}
	}
}

// TestSetRoots covers the mutex-guarded root accessors: SetRoots swaps
// both roots, and its restore func puts the previous values back.
func TestSetRoots(t *testing.T) {
	origDT, origArchive := DatatrackerRoot(), ArchiveRoot()

	restore := SetRoots("http://dt.test", "http://archive.test")
	if got := DatatrackerRoot(); got != "http://dt.test" {
		t.Errorf("DatatrackerRoot() = %q, want the override", got)
	}
	if got := ArchiveRoot(); got != "http://archive.test" {
		t.Errorf("ArchiveRoot() = %q, want the override", got)
	}

	restore()
	if got := DatatrackerRoot(); got != origDT {
		t.Errorf("DatatrackerRoot() after restore = %q, want %q", got, origDT)
	}
	if got := ArchiveRoot(); got != origArchive {
		t.Errorf("ArchiveRoot() after restore = %q, want %q", got, origArchive)
	}
}
