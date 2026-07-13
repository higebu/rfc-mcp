package drafts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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
func SearchDrafts(ctx context.Context, client *http.Client, params SearchParams) (SearchResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 20
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
