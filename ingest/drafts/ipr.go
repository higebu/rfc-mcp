package drafts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// iprDisclosureKinds are the three concrete Datatracker IPR resources --
// live-verified 2026-07-13: there is no discriminator field on the shared
// iprdisclosurebase resource that says which of these a given disclosure
// is, so every doc name is queried against all three rather than against
// the base resource.
//
// Field shapes differ: holderiprdisclosure and thirdpartyiprdisclosure both
// carry has_patent_pending/patent_info, but "licensing" is holder-only;
// genericiprdisclosure has none of those and carries a free-form
// "statement" field instead. All three carry holder_legal_name, state,
// docs, title, time.
var iprDisclosureKinds = []string{"holderiprdisclosure", "thirdpartyiprdisclosure", "genericiprdisclosure"}

// includedIPRState is the only disclosure state get_ipr surfaces by
// default. Live-verified 2026-07-13 against bcp22 (2 disclosures, both
// "removed") and rfc3261/draft-ietf-sip-rfc2543bis (1 "posted" disclosure
// each): "posted" is the state IETF's IPR process treats as an actual,
// standing disclosure -- pending/parked/rejected/removed are administrative
// or discarded states that would misrepresent an active encumbrance if
// included by default.
const includedIPRState = "posted"

// Disclosure is one IPR disclosure as surfaced by get_ipr, normalized
// across the three underlying Datatracker resource kinds (see
// iprDisclosureKinds). Licensing is holder-only; HasPatentPending/
// PatentInfo are also unset for a genericiprdisclosure, which carries
// Statement instead.
type Disclosure struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
	State string `json:"state"`
	// Holder is holder_legal_name, the one substantive field shared by
	// all three disclosure kinds.
	Holder string `json:"holder"`
	// Licensing is the iprlicensetypename slug (e.g. "reasonable",
	// "no-license"), not the full Datatracker URI.
	Licensing string `json:"licensing,omitempty"`
	// HasPatentPending is a pointer so a genericiprdisclosure (which has
	// no such field at all) comes through as unset rather than a
	// misleading "false".
	HasPatentPending *bool    `json:"has_patent_pending,omitempty"`
	PatentInfo       string   `json:"patent_info,omitempty"`
	Statement        string   `json:"statement,omitempty"`
	Time             string   `json:"time"`
	Docs             []string `json:"docs"`
}

// IPRResult is the outcome of a get_ipr fan-out lookup.
type IPRResult struct {
	// SearchedDocs lists every document name IPR disclosures were queried
	// for (see FetchIPR's doc comment for the fan-out rule), so callers
	// can see the coverage behind an empty or partial result.
	SearchedDocs []string     `json:"searched_docs"`
	Disclosures  []Disclosure `json:"disclosures"`
	TotalCount   int          `json:"total_count"`
}

type rawIPRDisclosure struct {
	ID               int      `json:"id"`
	Title            string   `json:"title"`
	State            string   `json:"state"`
	HolderLegalName  string   `json:"holder_legal_name"`
	Licensing        string   `json:"licensing"`
	HasPatentPending *bool    `json:"has_patent_pending"`
	PatentInfo       string   `json:"patent_info"`
	Statement        string   `json:"statement"`
	Time             string   `json:"time"`
	Docs             []string `json:"docs"`
}

type rawIPRListResponse struct {
	Objects []rawIPRDisclosure `json:"objects"`
}

// lastPathSegment extracts the trailing path component of a Datatracker
// resource URI, e.g. "/api/v1/name/iprdisclosurestatename/posted/" ->
// "posted", "/api/v1/doc/document/rfc3261/" -> "rfc3261". A query string
// or fragment on the URI (e.g. "/doc/document/rfc9000/?format=json") is
// stripped rather than swallowed into the segment. Returns "" for "" (a
// field the API omitted for a given disclosure kind).
func lastPathSegment(uri string) string {
	if u, err := url.Parse(uri); err == nil {
		uri = u.Path
	}
	uri = strings.TrimSuffix(uri, "/")
	if uri == "" {
		return ""
	}
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// verifyDocExists confirms name is a real Datatracker document (RFC or
// draft) before FetchIPR spends requests fanning out from it: the IPR list
// endpoints don't 404 for an unknown docs__name filter -- they just report
// zero results -- so an unknown RFC number or draft name would otherwise
// look identical to a real document with no disclosures.
func verifyDocExists(ctx context.Context, client *http.Client, name string) error {
	reqURL := DatatrackerRoot() + "/api/v1/doc/document/" + url.PathEscape(name) + "/?format=json"
	_, err := httpGetWithRetry(ctx, client, reqURL)
	return err
}

type rawRelatedDocSourceResponse struct {
	Objects []struct {
		Source string `json:"source"`
	} `json:"objects"`
}

// draftForRFC looks up the Internet-Draft that became rfcName, via the
// reverse direction of the became_rfc relateddocument (see becameRFC in
// metadata.go for the forward direction). Live-verified 2026-07-13 against
// rfc9000/draft-ietf-quic-transport and rfc3261/draft-ietf-sip-rfc2543bis:
// for this direction the relationship's "source" field (a document URI)
// names the draft; "originaltargetaliasname" instead carries the RFC's own
// alias (the query target), which is useless here. Returns "", nil (not an
// error) when no became_rfc row exists -- true for RFCs published before
// the Datatracker tracked drafts, not a sign of a broken lookup.
func draftForRFC(ctx context.Context, client *http.Client, rfcName string) (string, error) {
	q := url.Values{}
	q.Set("target__name", rfcName)
	q.Set("relationship__slug", "became_rfc")
	q.Set("format", "json")
	// Only the first row is read below, so pin which row that is: order
	// deterministically and let the server send just one.
	q.Set("order_by", "id")
	q.Set("limit", "1")
	reqURL := DatatrackerRoot() + "/api/v1/doc/relateddocument/?" + q.Encode()

	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return "", err
	}
	var raw rawRelatedDocSourceResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parse became_rfc reverse lookup for %s: %w", rfcName, err)
	}
	if len(raw.Objects) == 0 {
		return "", nil
	}
	return lastPathSegment(raw.Objects[0].Source), nil
}

// datatrackerPageLimit is the page size used when walking a Datatracker
// list endpoint's offset pagination.
const datatrackerPageLimit = 100

// datatrackerMaxPages is a safety cap on how many pages one pagination
// walk may fetch (5000 rows -- far beyond any real per-document disclosure
// or replaces count) so a misbehaving endpoint can't turn one lookup into
// an unbounded request loop. Reaching the cap with the last page still
// full is an error, not a silent truncation: an incomplete list must never
// be returned (and cached) as success. A var so tests can lower it.
var datatrackerMaxPages = 50

// replacedDrafts returns the draft name(s) that name directly replaces
// (one hop only, matching FetchIPR's fan-out rule -- a replaced draft's
// own replaces chain is not followed), following offset pagination until
// the endpoint is exhausted. Reuses metadata.go's
// rawRelatedDocumentResponse; see relatedTargetName for why the replaced
// draft's name can live in either originaltargetaliasname or the target
// document URI depending on the row's age.
func replacedDrafts(ctx context.Context, client *http.Client, name string) ([]string, error) {
	var out []string
	for page, offset := 0, 0; ; page++ {
		if page == datatrackerMaxPages {
			// Only reachable when every page so far was full, i.e. more
			// rows likely remain: fail rather than return a truncated list.
			return nil, fmt.Errorf("replaces lookup for %s not exhausted after %d pages (%d rows); refusing to return a truncated list", name, datatrackerMaxPages, len(out))
		}
		q := url.Values{}
		q.Set("source__name", name)
		q.Set("relationship__slug", "replaces")
		q.Set("format", "json")
		q.Set("limit", strconv.Itoa(datatrackerPageLimit))
		q.Set("offset", strconv.Itoa(offset))
		reqURL := DatatrackerRoot() + "/api/v1/doc/relateddocument/?" + q.Encode()

		data, err := httpGetWithRetry(ctx, client, reqURL)
		if err != nil {
			return nil, err
		}
		var raw rawRelatedDocumentResponse
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("parse replaces lookup for %s: %w", name, err)
		}
		for _, o := range raw.Objects {
			out = append(out, relatedTargetName(o.OriginalTargetAliasName, o.Target))
		}
		if len(raw.Objects) < datatrackerPageLimit {
			break
		}
		offset += len(raw.Objects)
	}
	return out, nil
}

// fetchDisclosuresForDoc queries all three IPR disclosure resource kinds
// for docName, following each kind's offset pagination until exhausted,
// and returns the disclosures in includedIPRState.
func fetchDisclosuresForDoc(ctx context.Context, client *http.Client, docName string) ([]Disclosure, error) {
	var out []Disclosure
	for _, kind := range iprDisclosureKinds {
		for page, offset := 0, 0; ; page++ {
			if page == datatrackerMaxPages {
				// Only reachable when every page so far was full, i.e. more
				// rows likely remain: fail rather than return a truncated
				// list.
				return nil, fmt.Errorf("%s disclosures for %s not exhausted after %d pages (%d rows); refusing to return a truncated list", kind, docName, datatrackerMaxPages, len(out))
			}
			q := url.Values{}
			q.Set("docs__name", docName)
			q.Set("format", "json")
			q.Set("limit", strconv.Itoa(datatrackerPageLimit))
			q.Set("offset", strconv.Itoa(offset))
			reqURL := DatatrackerRoot() + "/api/v1/ipr/" + kind + "/?" + q.Encode()

			data, err := httpGetWithRetry(ctx, client, reqURL)
			if err != nil {
				return nil, fmt.Errorf("fetch %s disclosures for %s: %w", kind, docName, err)
			}
			var raw rawIPRListResponse
			if err := json.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("parse %s disclosures for %s: %w", kind, docName, err)
			}

			for _, o := range raw.Objects {
				if lastPathSegment(o.State) != includedIPRState {
					continue
				}
				docs := make([]string, len(o.Docs))
				for i, d := range o.Docs {
					docs[i] = lastPathSegment(d)
				}
				out = append(out, Disclosure{
					ID:               o.ID,
					URL:              DatatrackerRoot() + "/ipr/" + strconv.Itoa(o.ID) + "/",
					Title:            o.Title,
					State:            lastPathSegment(o.State),
					Holder:           o.HolderLegalName,
					Licensing:        lastPathSegment(o.Licensing),
					HasPatentPending: o.HasPatentPending,
					PatentInfo:       o.PatentInfo,
					Statement:        o.Statement,
					Time:             o.Time,
					Docs:             docs,
				})
			}
			if len(raw.Objects) < datatrackerPageLimit {
				break
			}
			offset += len(raw.Objects)
		}
	}
	return out, nil
}

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// FetchIPR returns deduplicated, includedIPRState-only IPR disclosures for
// an RFC (rfc > 0) or a draft (name != "", already the base name with no
// revision suffix -- IPR disclosures attach at the document level, and a
// revision-suffixed name 404s against the same document endpoint FetchText
// and FetchMetadata rely on). Exactly one of rfc/name must be set; callers
// (the get_ipr tool) are responsible for that choice.
//
// Disclosures attach to draft names and are not automatically carried over
// once a draft becomes an RFC, but some do get filed directly against the
// RFC name too (live-verified 2026-07-13: rfc3261 carries one disclosure
// of its own, its originating draft draft-ietf-sip-rfc2543bis carries a
// second). So for an RFC this queries the RFC name, its originating draft
// (via the became_rfc relateddocument, reverse direction -- see
// draftForRFC), and that draft's replaced draft(s) (one hop -- see
// replacedDrafts); for a draft, the draft itself plus its replaced
// draft(s) (one hop). Results are cached on disk per top-level query for
// metadataCacheTTL, the same 1h window FetchMetadata uses -- disclosures
// change rarely but not never.
func FetchIPR(ctx context.Context, client *http.Client, rfc int, name string) (IPRResult, error) {
	primary := name
	isRFC := rfc > 0
	if isRFC {
		primary = fmt.Sprintf("rfc%d", rfc)
	} else if err := validateDraftName(name); err != nil {
		// name flows into a cache path and request URLs below; reject
		// anything that isn't a plausible draft name up front.
		return IPRResult{}, err
	}
	if err := verifyDocExists(ctx, client, primary); err != nil {
		return IPRResult{}, err
	}

	cacheKey := "ipr/" + primary + ".json"
	if data, err := loadCache(cacheKey, metadataCacheTTL); err == nil && data != nil {
		var cached IPRResult
		if err := json.Unmarshal(data, &cached); err == nil {
			return cached, nil
		}
	}

	docs := []string{primary}
	if isRFC {
		// Best-effort: an RFC with no became_rfc row (pre-draft-era, or a
		// transient lookup failure) just narrows the fan-out to the RFC
		// name alone rather than failing the whole call.
		if draft, err := draftForRFC(ctx, client, primary); err == nil && draft != "" {
			docs = append(docs, draft)
		}
	}
	// One-hop "replaces" fan-out from every draft name gathered so far
	// (the RFC's originating draft, or the queried draft itself). A failed
	// lookup fails the whole call: silently dropping the replaced drafts
	// would return -- and cache for an hour -- an incomplete disclosure
	// list that misrepresents a document's IPR encumbrance.
	for _, d := range append([]string(nil), docs...) {
		if !strings.HasPrefix(d, "draft-") {
			continue
		}
		replaced, err := replacedDrafts(ctx, client, d)
		if err != nil {
			return IPRResult{}, fmt.Errorf("look up drafts replaced by %s: %w", d, err)
		}
		docs = append(docs, replaced...)
	}
	docs = dedupeNonEmpty(docs)

	byID := make(map[int]Disclosure)
	for _, d := range docs {
		items, err := fetchDisclosuresForDoc(ctx, client, d)
		if err != nil {
			return IPRResult{}, err
		}
		for _, it := range items {
			byID[it.ID] = it
		}
	}

	// Disclosures starts non-nil so a document with none serializes as
	// an empty JSON array, not null — same contract as get_errata.
	result := IPRResult{SearchedDocs: docs, Disclosures: []Disclosure{}}
	for _, v := range byID {
		result.Disclosures = append(result.Disclosures, v)
	}
	sort.Slice(result.Disclosures, func(i, j int) bool { return result.Disclosures[i].ID < result.Disclosures[j].ID })
	result.TotalCount = len(result.Disclosures)

	if cached, err := json.Marshal(result); err == nil {
		_ = saveCache(cacheKey, cached) // best-effort; a failed write just means the next call re-fetches
	}
	return result, nil
}
