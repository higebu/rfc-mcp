package drafts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearchParams holds filters for SearchDrafts.
type SearchParams struct {
	Query          string // matched against title (title__icontains)
	NameContains   string // matched against draft name (name__contains)
	Group          string // working group acronym (group__acronym)
	IncludeExpired bool   // false: only "active" drafts; true: any lifecycle state
	Limit          int    // <= 0 defaults to 20
	Offset         int
}

// SearchResultItem is one row of a SearchDrafts result.
type SearchResultItem struct {
	Name    string `json:"name"`
	Rev     string `json:"rev"`
	Title   string `json:"title"`
	Expires string `json:"expires,omitempty"`
	Pages   int    `json:"pages,omitempty"`
	// RFC is the published RFC number, set only when this result's
	// lifecycle state shows it was published (see draftRFCStateID) and
	// the became_rfc lookup for it succeeds.
	RFC int `json:"rfc,omitempty"`
}

// SearchResult is the paginated result of SearchDrafts.
type SearchResult struct {
	Drafts     []SearchResultItem `json:"drafts"`
	TotalCount int                `json:"total_count"`
	Limit      int                `json:"limit"`
	Offset     int                `json:"offset"`
	// Truncated is set when a multi-word Query hit multiWordScanCap
	// before exhausting the server-side results (see
	// searchDraftsMultiWord): TotalCount is then a floor, not exact.
	Truncated bool `json:"truncated,omitempty"`
}

type rawSearchResponse struct {
	Meta struct {
		TotalCount int `json:"total_count"`
	} `json:"meta"`
	Objects []rawDocument `json:"objects"`
}

// SearchDrafts queries the Datatracker document list for Internet-Drafts
// matching params. Results are never cached: listings change too often to
// be worth it, and the search itself is cheap.
//
// A multi-word Query is handled specially: the Datatracker's
// title__icontains matches one substring, so e.g. "BGP MUP SAFI" would
// find nothing server-side even though draft-ietf-bess-mup-safi's title
// contains all three words but not that exact phrase (live-verified
// 2026-07-13: repeated title__icontains params do not combine as AND).
// See searchDraftsMultiWord for the client-side fallback.
func SearchDrafts(ctx context.Context, client *http.Client, params SearchParams) (SearchResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
	}

	if tokens := strings.Fields(params.Query); len(tokens) > 1 {
		return searchDraftsMultiWord(ctx, client, params, tokens, limit)
	}

	q := url.Values{}
	q.Set("type", "draft")
	q.Set("format", "json")
	q.Set("states__type", "draft")
	if !params.IncludeExpired {
		q.Set("states__slug", "active")
	}
	if params.Query != "" {
		q.Set("title__icontains", params.Query)
	}
	if params.NameContains != "" {
		q.Set("name__contains", params.NameContains)
	}
	if params.Group != "" {
		q.Set("group__acronym", params.Group)
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(params.Offset))

	reqURL := DatatrackerRoot + "/api/v1/doc/document/?" + q.Encode()
	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return SearchResult{}, err
	}

	var raw rawSearchResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return SearchResult{}, fmt.Errorf("parse draft search response: %w", err)
	}

	result := SearchResult{TotalCount: raw.Meta.TotalCount, Limit: limit, Offset: params.Offset}
	for _, o := range raw.Objects {
		item := SearchResultItem{Name: o.Name, Rev: o.Rev, Title: o.Title, Expires: o.Expires, Pages: o.Pages}
		// Best-effort: a became_rfc lookup failure (network hiccup, an
		// unexpected alias shape) just leaves RFC unset rather than
		// failing the whole search.
		if hasState(o.States, draftRFCStateID) {
			if n, err := becameRFC(ctx, client, o.Name); err == nil {
				item.RFC = n
			}
		}
		result.Drafts = append(result.Drafts, item)
	}
	return result, nil
}

// multiWordPageSize is the page size used when walking Datatracker
// pagination for searchDraftsMultiWord's client-side AND-match fallback.
const multiWordPageSize = 100

// multiWordScanCap bounds how many server-side objects
// searchDraftsMultiWord will scan (multiWordScanCap/multiWordPageSize
// requests, worst case) before giving up and reporting Truncated, so a
// token that matches thousands of drafts can't turn one search_drafts
// call into an unbounded number of requests.
const multiWordScanCap = 500

// longestToken picks the token to use as the server-side title__icontains
// filter for a multi-word query: a longer substring is rarer and so
// narrows the server-side result set the most before the remaining
// tokens are checked client-side. Ties (equal length) prefer the later
// token in the query, an arbitrary but deterministic tiebreak.
func longestToken(tokens []string) string {
	best := tokens[0]
	for _, t := range tokens[1:] {
		if len(t) >= len(best) {
			best = t
		}
	}
	return best
}

// titleContainsAll reports whether title contains every token, matched
// case-insensitively, mirroring the Datatracker's title__icontains
// semantics for the tokens that aren't server-filtered.
func titleContainsAll(title string, tokens []string) bool {
	lower := strings.ToLower(title)
	for _, t := range tokens {
		if !strings.Contains(lower, strings.ToLower(t)) {
			return false
		}
	}
	return true
}

// searchDraftsMultiWord implements AND matching across every token of a
// multi-word Query (see SearchDrafts's doc comment for why): it filters
// server-side on the longest token, then walks pagination checking the
// remaining tokens against each title client-side, up to multiWordScanCap
// scanned objects. params.Offset/limit are then applied to the filtered
// list, and TotalCount reflects the filtered match count -- a floor, not
// an exact count, when the scan cap is hit (see SearchResult.Truncated).
func searchDraftsMultiWord(ctx context.Context, client *http.Client, params SearchParams, tokens []string, limit int) (SearchResult, error) {
	filterToken := longestToken(tokens)
	others := make([]string, 0, len(tokens)-1)
	removed := false
	for _, t := range tokens {
		if !removed && t == filterToken {
			removed = true
			continue
		}
		others = append(others, t)
	}

	var matches []rawDocument
	scanned := 0
	serverOffset := 0
	serverTotal := 0
	for scanned < multiWordScanCap {
		q := url.Values{}
		q.Set("type", "draft")
		q.Set("format", "json")
		q.Set("states__type", "draft")
		if !params.IncludeExpired {
			q.Set("states__slug", "active")
		}
		q.Set("title__icontains", filterToken)
		if params.NameContains != "" {
			q.Set("name__contains", params.NameContains)
		}
		if params.Group != "" {
			q.Set("group__acronym", params.Group)
		}
		q.Set("limit", strconv.Itoa(multiWordPageSize))
		q.Set("offset", strconv.Itoa(serverOffset))

		reqURL := DatatrackerRoot + "/api/v1/doc/document/?" + q.Encode()
		data, err := httpGetWithRetry(ctx, client, reqURL)
		if err != nil {
			return SearchResult{}, err
		}
		var raw rawSearchResponse
		if err := json.Unmarshal(data, &raw); err != nil {
			return SearchResult{}, fmt.Errorf("parse draft search response: %w", err)
		}
		serverTotal = raw.Meta.TotalCount

		for _, o := range raw.Objects {
			if scanned >= multiWordScanCap {
				break
			}
			scanned++
			if titleContainsAll(o.Title, others) {
				matches = append(matches, o)
			}
		}

		serverOffset += len(raw.Objects)
		if len(raw.Objects) < multiWordPageSize || serverOffset >= serverTotal {
			break
		}
	}

	result := SearchResult{TotalCount: len(matches), Limit: limit, Offset: params.Offset, Truncated: serverTotal > scanned}

	start := min(params.Offset, len(matches))
	end := min(start+limit, len(matches))
	for _, o := range matches[start:end] {
		item := SearchResultItem{Name: o.Name, Rev: o.Rev, Title: o.Title, Expires: o.Expires, Pages: o.Pages}
		// Best-effort, same as SearchDrafts: a became_rfc lookup failure
		// just leaves RFC unset rather than failing the whole search.
		if hasState(o.States, draftRFCStateID) {
			if n, err := becameRFC(ctx, client, o.Name); err == nil {
				item.RFC = n
			}
		}
		result.Drafts = append(result.Drafts, item)
	}
	return result, nil
}
