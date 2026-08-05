package drafts

import (
	"context"
	"net/http"
	"net/url"
)

// FetchText returns the plain-text body of a specific draft name+revision
// (rev is one or two digits; it is normalized to the canonical two-digit
// form, e.g. "3" -> "03"). A copy already on disk is returned without a
// network request -- a specific revision's body is immutable once
// submitted, so a cached copy is valid forever and needs no TTL.
func FetchText(ctx context.Context, client *http.Client, name, rev string) ([]byte, error) {
	if err := validateDraftName(name); err != nil {
		return nil, err
	}
	rev, err := normalizeDraftRev(rev)
	if err != nil {
		return nil, err
	}

	cacheKey := name + "-" + rev + ".txt"
	if data, err := loadCache(cacheKey, 0); err == nil && data != nil {
		return data, nil
	}

	reqURL := ArchiveRoot() + "/" + url.PathEscape(name+"-"+rev+".txt")
	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return nil, err
	}

	_ = saveCache(cacheKey, data) // best-effort; a failed write just re-fetches next time
	return data, nil
}
