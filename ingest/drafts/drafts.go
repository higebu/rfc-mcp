// Package drafts fetches Internet-Draft metadata and plain-text bodies on
// demand from the IETF Datatracker and archive. Unlike ingest/pipeline,
// nothing here is stored in SQLite: drafts are pre-publication documents
// that change too often (new revisions, expiry, replacement) to be worth
// importing wholesale, so every lookup goes over the network, with a
// small on-disk cache for the parts that are safe to reuse briefly or
// forever (see cache.go).
package drafts

import "sync"

// defaultDatatrackerRoot and defaultArchiveRoot are live-verified
// 2026-07-13:
//   - The Datatracker document API takes the base draft name (no revision
//     suffix) for both the search list and single-doc endpoints; a name
//     with "-NN" appended 404s.
//   - "states__type=draft&states__slug=active" restricts a search to
//     drafts in the "Active" lifecycle state; dropping "states__slug"
//     broadens it to every state (expired, replaced, withdrawn, and
//     "rfc" once a draft has been published).
//   - A draft document's own "rfc"/"rfc_number" fields are always null,
//     even once it has been published -- the association is recorded as
//     a separate "became_rfc" relateddocument row instead (see
//     becameRFC in metadata.go).
//   - Archive .txt fetches always take an explicit two-digit revision and
//     404 for both an unknown draft name and an unknown revision of a
//     known draft; a past revision's body is served regardless of the
//     draft's current lifecycle state (expired, replaced, or published).
const (
	defaultDatatrackerRoot = "https://datatracker.ietf.org"
	defaultArchiveRoot     = "https://www.ietf.org/archive/id"
)

// The Datatracker/archive base URLs live in package-level state (rather
// than function parameters, which would need threading through every
// exported function here and every tools/get_draft_*.go caller) so tests
// -- including tools package tests, which only see this package's
// exported surface -- can redirect them to an httptest.Server via
// SetRoots, mirroring ingest/pipeline's Pipeline.BaseURL override. Access
// goes through rootsMu so a test rewriting the roots can never race a
// concurrent fetch reading them.
var (
	rootsMu         sync.RWMutex
	datatrackerRoot = defaultDatatrackerRoot
	archiveRoot     = defaultArchiveRoot
)

// DatatrackerRoot returns the base URL Datatracker API requests are built
// against.
func DatatrackerRoot() string {
	rootsMu.RLock()
	defer rootsMu.RUnlock()
	return datatrackerRoot
}

// ArchiveRoot returns the base URL draft plain-text bodies are fetched
// from.
func ArchiveRoot() string {
	rootsMu.RLock()
	defer rootsMu.RUnlock()
	return archiveRoot
}

// SetRoots replaces both base URLs and returns a func that restores the
// previous values. It exists for tests; production code never changes the
// defaults.
func SetRoots(datatracker, archive string) (restore func()) {
	rootsMu.Lock()
	prevDT, prevArchive := datatrackerRoot, archiveRoot
	datatrackerRoot = datatracker
	archiveRoot = archive
	rootsMu.Unlock()
	return func() { SetRoots(prevDT, prevArchive) }
}

// rawDocument mirrors the fields this package uses from the Datatracker's
// document resource, shared by both the search list endpoint's "objects"
// entries and the single-doc metadata endpoint.
type rawDocument struct {
	Name     string   `json:"name"`
	Rev      string   `json:"rev"`
	Title    string   `json:"title"`
	Abstract string   `json:"abstract"`
	Pages    int      `json:"pages"`
	Time     string   `json:"time"`
	Expires  string   `json:"expires"`
	States   []string `json:"states"`
}

// draftRFCStateID is the Datatracker's numeric ID for the draft-lifecycle
// "rfc" state (type=draft, slug=rfc) -- live-verified 2026-07-13 via GET
// /api/v1/doc/state/?type=draft&slug=rfc. Only a document carrying this
// state can possibly have become an RFC, so it gates the extra
// became_rfc lookup (an unconditional per-result lookup would be
// wasteful: the vast majority of search/metadata calls are for drafts
// that never reach this state).
//
// This ID is an external dependency on the Datatracker's database: it is
// not defined by any IETF spec, and if the Datatracker ever renumbered
// its state table, published drafts would silently stop resolving to
// their RFC numbers. TestDraftRFCStateContract pins the assumed ID and
// the state-URI shape it is matched against, so any deliberate change
// here must update that contract in one visible place.
const draftRFCStateID = "3"

// hasState reports whether states (a list of Datatracker resource URIs
// like "/api/v1/doc/state/3/") includes the state with the given numeric
// id.
func hasState(states []string, id string) bool {
	suffix := "/state/" + id + "/"
	for _, s := range states {
		if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}
