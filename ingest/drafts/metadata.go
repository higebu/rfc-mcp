package drafts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// metadataCacheTTL is deliberately shorter than ingest/pipeline's 24h
// rfc-index.xml TTL: drafts move faster (new revisions land during active
// WG work, expiry is date-driven), so a fixed 1-hour TTL keeps rev/
// expires from going stale for long without re-fetching on every call.
const metadataCacheTTL = time.Hour

// Metadata is a draft's Datatracker metadata, always resolved to its
// latest revision -- the endpoint this is fetched from doesn't accept a
// revision (see FetchText for a specific past revision's body).
type Metadata struct {
	Name     string `json:"name"`
	Rev      string `json:"rev"`
	Title    string `json:"title"`
	Abstract string `json:"abstract,omitempty"`
	Pages    int    `json:"pages,omitempty"`
	Time     string `json:"time,omitempty"`
	Expires  string `json:"expires,omitempty"`
	// RFC is the published RFC number, set only once this draft's
	// lifecycle state shows it was published and the became_rfc lookup
	// for it succeeds; see becameRFC.
	RFC int `json:"rfc,omitempty"`
}

// FetchMetadata returns name's latest-revision metadata. name must be the
// base draft name (no revision suffix) -- see the package doc comment.
// Cached on disk per name for metadataCacheTTL.
func FetchMetadata(ctx context.Context, client *http.Client, name string) (Metadata, error) {
	cacheKey := "meta/" + name + ".json"
	if data, err := loadCache(cacheKey, metadataCacheTTL); err == nil && data != nil {
		var cached Metadata
		if err := json.Unmarshal(data, &cached); err == nil {
			return cached, nil
		}
	}

	reqURL := DatatrackerRoot + "/api/v1/doc/document/" + url.PathEscape(name) + "/?format=json"
	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return Metadata{}, err
	}

	var raw rawDocument
	if err := json.Unmarshal(data, &raw); err != nil {
		return Metadata{}, fmt.Errorf("parse metadata for %s: %w", name, err)
	}

	meta := Metadata{
		Name:     raw.Name,
		Rev:      raw.Rev,
		Title:    raw.Title,
		Abstract: strings.TrimSpace(raw.Abstract),
		Pages:    raw.Pages,
		Time:     raw.Time,
		Expires:  raw.Expires,
	}
	// Best-effort: a became_rfc lookup failure just leaves RFC unset
	// rather than failing the metadata call the caller actually asked for.
	if hasState(raw.States, draftRFCStateID) {
		if n, err := becameRFC(ctx, client, name); err == nil {
			meta.RFC = n
		}
	}

	if cached, err := json.Marshal(meta); err == nil {
		_ = saveCache(cacheKey, cached) // best-effort; a failed write just means the next call re-fetches
	}
	return meta, nil
}

type rawRelatedDocumentResponse struct {
	Objects []struct {
		OriginalTargetAliasName string `json:"originaltargetaliasname"`
		Target                  string `json:"target"`
	} `json:"objects"`
}

// relatedTargetName returns the plain document name of a relateddocument
// row's target. Rows written before the Datatracker dropped its docalias
// model carry it in originaltargetaliasname; newer rows (live-verified
// 2026-07-14: draft-ietf-bess-mup-safi's replaces row, and the became_rfc
// rows of every 2025+ RFC checked, e.g. rfc9793/rfc10014) leave that
// field null and only name the target via its document URI.
func relatedTargetName(aliasName, targetURI string) string {
	if aliasName != "" {
		return aliasName
	}
	return lastPathSegment(targetURI)
}

// rfcNumberFromAlias parses a Datatracker alias like "rfc9000" into 9000.
func rfcNumberFromAlias(alias string) (int, bool) {
	if !strings.HasPrefix(alias, "rfc") {
		return 0, false
	}
	n, err := strconv.Atoi(alias[len("rfc"):])
	if err != nil {
		return 0, false
	}
	return n, true
}

// becameRFC looks up whether name was ever published as an RFC, via the
// Datatracker's "became_rfc" relateddocument. A draft document's own
// "rfc"/"rfc_number" fields are always null, even once published -- live-
// verified 2026-07-13 against draft-ietf-quic-transport (became RFC 9000):
// the publication is recorded as a separate relateddocument row instead,
// whose target's alias name (e.g. "rfc9000") carries the number.
func becameRFC(ctx context.Context, client *http.Client, name string) (int, error) {
	q := url.Values{}
	q.Set("source__name", name)
	q.Set("relationship__slug", "became_rfc")
	q.Set("format", "json")
	reqURL := DatatrackerRoot + "/api/v1/doc/relateddocument/?" + q.Encode()

	data, err := httpGetWithRetry(ctx, client, reqURL)
	if err != nil {
		return 0, err
	}
	var raw rawRelatedDocumentResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, fmt.Errorf("parse became_rfc lookup for %s: %w", name, err)
	}
	if len(raw.Objects) == 0 {
		return 0, fmt.Errorf("no became_rfc relationship for %s", name)
	}
	targetName := relatedTargetName(raw.Objects[0].OriginalTargetAliasName, raw.Objects[0].Target)
	n, ok := rfcNumberFromAlias(targetName)
	if !ok {
		return 0, fmt.Errorf("unexpected became_rfc target %q for %s", targetName, name)
	}
	return n, nil
}
