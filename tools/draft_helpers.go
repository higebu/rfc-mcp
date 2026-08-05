package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/ingest/drafts"
	"github.com/higebu/rfc-mcp/ingest/rfctxt"
)

// draftRevisionRE matches a trailing two-digit revision suffix on a draft
// name, e.g. "draft-ietf-quic-transport-34" -> base
// "draft-ietf-quic-transport", rev "34". IETF naming convention never ends
// a draft's meaningful name in a bare two-digit number outside this
// suffix, so the split is unambiguous.
var draftRevisionRE = regexp.MustCompile(`^(.+)-(\d{2})$`)

// splitDraftName splits name into its base draft name and an embedded
// revision, if the caller wrote one directly onto the name (e.g.
// "draft-foo-bar-03"). Returns rev == "" when name carries no revision
// suffix of its own.
func splitDraftName(name string) (base, rev string) {
	if m := draftRevisionRE.FindStringSubmatch(name); m != nil {
		return m[1], m[2]
	}
	return name, ""
}

// normalizeRevision zero-pads a single-digit numeric revision (e.g. "3"
// -> "03") for callers who type it without the leading zero; anything
// else (already two digits, or not numeric at all) passes through
// unchanged and simply 404s downstream if it's not a real revision.
func normalizeRevision(rev string) string {
	if len(rev) == 1 && rev[0] >= '0' && rev[0] <= '9' {
		return "0" + rev
	}
	return rev
}

// resolveDraftRevision determines which two-digit revision to fetch for a
// draft tool call, and returns the base draft name alongside it: an
// explicit revision input wins, then a revision embedded directly in
// name, and only once neither is given does it cost a Datatracker
// metadata call to resolve "latest".
func resolveDraftRevision(ctx context.Context, client *http.Client, name, revision string) (string, string, error) {
	base, embeddedRev := splitDraftName(name)
	switch {
	case revision != "":
		return base, normalizeRevision(revision), nil
	case embeddedRev != "":
		return base, embeddedRev, nil
	default:
		meta, err := drafts.FetchMetadata(ctx, client, base)
		if err != nil {
			return base, "", err
		}
		return base, meta.Rev, nil
	}
}

// draftLabel formats a draft's name+revision the way it's addressed in
// tool output and error messages (e.g. "draft-ietf-quic-transport-34").
func draftLabel(name, rev string) string {
	return name + "-" + rev
}

// wrapDraftFetchError wraps a Datatracker/archive fetch failure with the
// client-facing wording draft tools use, while preserving the error chain
// so errors.Is(err, drafts.ErrNotFound) still works on the result. An
// unknown draft/revision already names the attempted URL via
// drafts.ErrNotFound and is passed through unchanged.
func wrapDraftFetchError(label string, err error) error {
	if errors.Is(err, drafts.ErrNotFound) {
		// The error already reads "draft not found: <attempted URL>".
		return err
	}
	return fmt.Errorf("failed to fetch draft %s: %w", label, err)
}

// draftFetchError formats a Datatracker/archive fetch failure for a draft
// tool's error result, distinguishing an unknown draft/revision (which
// already names the attempted URL, via drafts.ErrNotFound) from any other
// network or server failure.
func draftFetchError(label string, err error) string {
	return wrapDraftFetchError(label, err).Error()
}

// fetchAndParseDraft resolves name/revision, fetches the draft's plain-
// text body, and parses it into sections with the same parser (and thus
// the same section-numbering/slug conventions) used for RFC bodies. label
// is the resolved "name-rev" string tool handlers use in their own output.
func fetchAndParseDraft(ctx context.Context, client *http.Client, name, revision string) (label string, sections []db.Section, err error) {
	base, rev, err := resolveDraftRevision(ctx, client, name, revision)
	if err != nil {
		return "", nil, wrapDraftFetchError(base, err)
	}
	label = draftLabel(base, rev)

	body, err := drafts.FetchText(ctx, client, base, rev)
	if err != nil {
		return "", nil, wrapDraftFetchError(label, err)
	}

	parsed, err := rfctxt.ParseRFCText(body, 0, label)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse %s: %w", label, err)
	}
	return label, toDBSections(parsed), nil
}

// toDBSections adapts parsed draft sections (rfctxt.Section, which carries
// no RFC number) into db.Section so get_draft_toc/get_draft_section can
// reuse get_toc.go/get_section.go's existing rendering and guidance
// helpers (formatTOC, emptyParentGuidance, missingSectionGuidance)
// verbatim.
func toDBSections(sections []rfctxt.Section) []db.Section {
	out := make([]db.Section, len(sections))
	for i, s := range sections {
		out[i] = db.Section{
			Number:       s.Number,
			Title:        s.Title,
			Level:        s.Level,
			ParentNumber: s.ParentNumber,
			Content:      s.Content,
		}
	}
	return out
}

// draftChildren returns the direct children of number within all (rows
// whose ParentNumber equals number), in document order -- the in-memory
// equivalent of db.DB.GetChildren for a draft's freshly parsed sections.
func draftChildren(all []db.Section, number string) []db.SectionChild {
	var children []db.SectionChild
	for _, s := range all {
		if s.ParentNumber == number {
			children = append(children, db.SectionChild{Number: s.Number, Title: s.Title})
		}
	}
	return children
}

// draftDescendantsByPrefix mirrors db.DB.GetDescendantsByPrefix: sections
// one level under number by number-prefix ("number.X", not "number.X.Y")
// rather than ParentNumber. Used as the same fallback get_section.go's
// missingSectionGuidance uses when number has no heading of its own and
// nothing's ParentNumber points to it either (see that function's doc
// comment on db.DB.GetDescendantsByPrefix for why both are needed).
func draftDescendantsByPrefix(all []db.Section, number string) []db.SectionChild {
	prefix := number + "."
	var children []db.SectionChild
	for _, s := range all {
		if !strings.HasPrefix(s.Number, prefix) {
			continue
		}
		if strings.Contains(s.Number[len(prefix):], ".") {
			continue
		}
		children = append(children, db.SectionChild{Number: s.Number, Title: s.Title})
	}
	return children
}

// draftSection mirrors db.DB.GetSection: a single matching section, or
// that section plus every descendant reached via the ParentNumber chain
// when includeSubsections is set.
func draftSection(all []db.Section, number string, includeSubsections bool) []db.Section {
	var match *db.Section
	for i := range all {
		if all[i].Number == number {
			match = &all[i]
			break
		}
	}
	if match == nil {
		return nil
	}
	if !includeSubsections {
		return []db.Section{*match}
	}

	wanted := map[string]bool{number: true}
	for changed := true; changed; {
		changed = false
		for _, s := range all {
			if wanted[s.ParentNumber] && !wanted[s.Number] {
				wanted[s.Number] = true
				changed = true
			}
		}
	}
	var out []db.Section
	for _, s := range all {
		if wanted[s.Number] {
			out = append(out, s)
		}
	}
	return out
}
