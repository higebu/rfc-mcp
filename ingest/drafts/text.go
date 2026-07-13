package drafts

import (
	"context"
	"net/http"
)

// FetchText returns the plain-text body of a specific draft name+revision
// (rev is the two-digit form, e.g. "03"). A copy already on disk is
// returned without a network request -- a specific revision's body is
// immutable once submitted, so a cached copy is valid forever and needs
// no TTL.
func FetchText(ctx context.Context, client *http.Client, name, rev string) ([]byte, error) {
	cacheKey := name + "-" + rev + ".txt"
	if data, err := loadCache(cacheKey, 0); err == nil && data != nil {
		return data, nil
	}

	reqURL := ArchiveRoot + "/" + name + "-" + rev + ".txt"
	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return nil, err
	}

	_ = saveCache(cacheKey, data) // best-effort; a failed write just re-fetches next time
	return data, nil
}
