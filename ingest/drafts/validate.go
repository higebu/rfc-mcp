package drafts

import (
	"fmt"
	"regexp"
)

// draftNameRE matches an IETF Internet-Draft base name (no revision
// suffix): "draft-" followed by lowercase letters, digits, and the
// separator characters that appear in real draft names -- hyphens
// everywhere, dots in older names (e.g. draft-kille-x.400-88), and
// underscores in a handful of historical submissions. This is the
// package's input boundary: names come straight from MCP tool input and
// are concatenated into on-disk cache paths and archive URLs, so anything
// outside this set (path separators, "..", uppercase, whitespace) is
// rejected before either is built.
var draftNameRE = regexp.MustCompile(`^draft-[a-z0-9][a-z0-9._-]*$`)

// draftRevRE matches a draft revision as callers may write it: one or two
// digits. normalizeDraftRev zero-pads to the canonical two-digit form the
// archive URLs and cache keys use.
var draftRevRE = regexp.MustCompile(`^[0-9]{1,2}$`)

// validateDraftName rejects name unless it is a plausible Internet-Draft
// base name (see draftNameRE). It must be called before name is used in a
// cache path or URL.
func validateDraftName(name string) error {
	if !draftNameRE.MatchString(name) {
		return fmt.Errorf("invalid draft name %q: must start with \"draft-\" and contain only lowercase letters, digits, '.', '_', and '-'", name)
	}
	return nil
}

// normalizeDraftRev validates rev (one or two digits) and returns it
// zero-padded to the canonical two-digit form ("3" -> "03").
func normalizeDraftRev(rev string) (string, error) {
	if !draftRevRE.MatchString(rev) {
		return "", fmt.Errorf("invalid draft revision %q: must be one or two digits", rev)
	}
	if len(rev) == 1 {
		rev = "0" + rev
	}
	return rev, nil
}
