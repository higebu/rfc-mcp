package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/higebu/rfc-mcp/ingest/drafts"
)

// redirectDraftsRoots points ingest/drafts' Datatracker/archive roots at
// url for the duration of the test (drafts.SetRoots is exported
// specifically so callers outside ingest/drafts, like this package's
// tests, can redirect them -- see that package's doc comment), and
// returns a restore func. It also isolates the on-disk draft cache in a
// fresh XDG_CACHE_HOME per call, so cache-hit assertions across
// subtests/helpers never see another test's cached files.
func redirectDraftsRoots(t *testing.T, url string) func() {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return drafts.SetRoots(url, url)
}

// draftTestBody is a small, real-shaped draft plain-text body: an
// xml2rfc-rendered header block followed by a couple of numbered
// sections, enough for ingest/rfctxt.ParseRFCText to detect real
// section structure (not fall back to a single "body" section).
const draftTestBody = `



Network Working Group                                        A. Author
Internet-Draft                                             Some Org
Intended status: Standards Track                        1 January 2026
Expires: 3 July 2026


                     An Example Protocol
                  draft-example-protocol-03

Abstract

   This document describes an example protocol used for testing.

1.  Introduction

   This is the introduction to the example protocol.

2.  Protocol Overview

   This section describes the protocol.

2.1.  Message Format

   This subsection describes the message format.

Security Considerations

   This document raises no new security considerations.

Author's Address

   A. Author
   Some Org
`

// newDraftTestServer serves a Datatracker-shaped API plus an archive .txt
// body, mimicking the real endpoints this package's HTTP calls hit:
//   - /api/v1/doc/document/?... (search)
//   - /api/v1/doc/document/<name>/?format=json (single-doc metadata)
//   - /api/v1/doc/relateddocument/?... (became_rfc lookup)
//   - /<name>-<rev>.txt (archive body)
func newDraftTestServer(t *testing.T, metaRequests, textRequests *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/doc/document/draft-example-protocol/", func(w http.ResponseWriter, r *http.Request) {
		if metaRequests != nil {
			metaRequests.Add(1)
		}
		_, _ = w.Write([]byte(`{
			"name": "draft-example-protocol", "rev": "03",
			"title": "An Example Protocol",
			"abstract": "This document describes an example protocol used for testing.",
			"pages": 5, "time": "2026-01-01T00:00:00Z", "expires": "2026-07-03T00:00:00Z",
			"rfc": null, "rfc_number": null,
			"states": ["/api/v1/doc/state/1/"]
		}`))
	})
	mux.HandleFunc("/api/v1/doc/document/draft-published-thing/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"name": "draft-published-thing", "rev": "12",
			"title": "A Thing That Became An RFC",
			"abstract": "Now an RFC.",
			"pages": 10, "time": "2020-01-01T00:00:00Z", "expires": "2020-06-01T00:00:00Z",
			"rfc": null, "rfc_number": null,
			"states": ["/api/v1/doc/state/3/"]
		}`))
	})
	mux.HandleFunc("/api/v1/doc/document/draft-does-not-exist/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 1}, "objects": [{"originaltargetaliasname": "rfc9999"}]}`))
	})
	mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"meta": {"total_count": 1},
			"objects": [{"name": "draft-example-protocol", "rev": "03", "title": "An Example Protocol", "expires": "2026-07-03T00:00:00Z", "pages": 5, "rfc": null, "states": ["/api/v1/doc/state/1/"]}]
		}`))
	})
	mux.HandleFunc("/draft-example-protocol-03.txt", func(w http.ResponseWriter, r *http.Request) {
		if textRequests != nil {
			textRequests.Add(1)
		}
		_, _ = w.Write([]byte(draftTestBody))
	})
	mux.HandleFunc("/draft-example-protocol-01.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(draftTestBody))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	return httptest.NewServer(mux)
}

func TestHandleSearchDrafts(t *testing.T) {
	ts := newDraftTestServer(t, nil, nil)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleSearchDrafts(ts.Client())

	t.Run("basic search", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchDraftsInput{Query: "example"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		var out drafts.SearchResult
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("failed to unmarshal output: %v\n%s", err, text)
		}
		if out.TotalCount != 1 || len(out.Drafts) != 1 {
			t.Fatalf("out = %+v", out)
		}
		if out.Drafts[0].Name != "draft-example-protocol" {
			t.Errorf("Name = %q", out.Drafts[0].Name)
		}
	})

	t.Run("include_expired flag is wired through", func(t *testing.T) {
		var gotQuery string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
		})
		ts2 := httptest.NewServer(mux)
		defer ts2.Close()
		restore2 := redirectDraftsRoots(t, ts2.URL)
		defer restore2()

		h2 := HandleSearchDrafts(ts2.Client())
		if _, _, err := h2(context.Background(), nil, SearchDraftsInput{IncludeExpired: true}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(gotQuery, "states__slug=active") {
			t.Errorf("query = %q, want states__slug dropped when include_expired", gotQuery)
		}
	})

	t.Run("negative offset and limit are clamped", func(t *testing.T) {
		var gotQuery string
		mux := http.NewServeMux()
		mux.HandleFunc("/api/v1/doc/document/", func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
		})
		ts2 := httptest.NewServer(mux)
		defer ts2.Close()
		restore2 := redirectDraftsRoots(t, ts2.URL)
		defer restore2()

		h2 := HandleSearchDrafts(ts2.Client())
		result, _, err := h2(context.Background(), nil, SearchDraftsInput{Query: "quic", Offset: -3, Limit: -1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if text := getTextContent(result); result.IsError {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if !strings.Contains(gotQuery, "offset=0") {
			t.Errorf("query = %q, want offset=0 (negative offset clamped)", gotQuery)
		}
		if !strings.Contains(gotQuery, "limit=20") {
			t.Errorf("query = %q, want limit=20 (negative limit falls back to the default)", gotQuery)
		}
	})
}

func TestHandleGetDraftMetadata(t *testing.T) {
	ts := newDraftTestServer(t, nil, nil)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleGetDraftMetadata(ts.Client())

	t.Run("not yet published", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{Name: "draft-example-protocol"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		var out getDraftMetadataOutput
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", err, getTextContent(result))
		}
		if out.Rev != "03" || out.RFC != 0 || out.Hint != "" {
			t.Errorf("out = %+v, want rev 03, no RFC, no hint", out)
		}
	})

	t.Run("name with embedded revision is stripped and reported", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{Name: "draft-example-protocol-01"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var out getDraftMetadataOutput
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", err, getTextContent(result))
		}
		if out.RequestedRevision != "01" {
			t.Errorf("RequestedRevision = %q, want 01", out.RequestedRevision)
		}
		if out.Rev != "03" {
			t.Errorf("Rev = %q, want latest (03) regardless of the requested revision", out.Rev)
		}
	})

	t.Run("published as RFC surfaces rfc and hint", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{Name: "draft-published-thing"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var out getDraftMetadataOutput
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", err, getTextContent(result))
		}
		if out.RFC != 9999 {
			t.Errorf("RFC = %d, want 9999", out.RFC)
		}
		if !strings.Contains(out.Hint, "RFC 9999") {
			t.Errorf("Hint = %q, want it to mention RFC 9999", out.Hint)
		}
	})

	t.Run("expired draft surfaces expires in the past plainly", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{Name: "draft-published-thing"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var out getDraftMetadataOutput
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", err, getTextContent(result))
		}
		if out.Expires != "2020-06-01T00:00:00Z" {
			t.Errorf("Expires = %q, want the past expiry surfaced as-is", out.Expires)
		}
	})

	t.Run("unknown draft", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{Name: "draft-does-not-exist"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for unknown draft")
		}
		if !strings.Contains(getTextContent(result), "draft-does-not-exist") {
			t.Errorf("expected error to name the draft, got: %s", getTextContent(result))
		}
	})

	t.Run("name required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftMetadataInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing name")
		}
	})
}

func TestHandleGetDraftTOC(t *testing.T) {
	ts := newDraftTestServer(t, nil, nil)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleGetDraftTOC(ts.Client())

	t.Run("latest revision resolution via metadata", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftTOCInput{Name: "draft-example-protocol"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "draft-example-protocol-03 - Table of Contents") {
			t.Errorf("expected header naming the resolved latest revision, got: %s", text)
		}
		for _, want := range []string{"1 Introduction", "2 Protocol Overview", "2.1 Message Format"} {
			if !strings.Contains(text, want) {
				t.Errorf("expected %q in TOC, got: %s", want, text)
			}
		}
	})

	t.Run("explicit revision skips metadata lookup", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftTOCInput{Name: "draft-example-protocol", Revision: "01"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "draft-example-protocol-01 - Table of Contents") {
			t.Errorf("expected header naming revision 01, got: %s", text)
		}
	})

	t.Run("name required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftTOCInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing name")
		}
	})
}

func TestHandleGetDraftSection(t *testing.T) {
	ts := newDraftTestServer(t, nil, nil)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleGetDraftSection(ts.Client())

	t.Run("basic section retrieval", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftSectionInput{Name: "draft-example-protocol", SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "introduction to the example protocol") {
			t.Errorf("expected section 1 content, got: %s", text)
		}
	})

	t.Run("include_subsections", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftSectionInput{
			Name: "draft-example-protocol", SectionNumber: "2", IncludeSubsections: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Protocol Overview") || !strings.Contains(text, "Message Format") {
			t.Errorf("expected section 2 and its subsection 2.1, got: %s", text)
		}
	})

	t.Run("slug-addressed unnumbered section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftSectionInput{
			Name: "draft-example-protocol", SectionNumber: "security-considerations",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "no new security considerations") {
			t.Errorf("expected Security Considerations content, got: %s", text)
		}
	})

	t.Run("unknown section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftSectionInput{
			Name: "draft-example-protocol", SectionNumber: "99",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for a section that doesn't exist")
		}
		if !strings.Contains(getTextContent(result), "not found") {
			t.Errorf("expected 'not found' message, got: %s", getTextContent(result))
		}
	})

	t.Run("name and section_number required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDraftSectionInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing name")
		}

		result, _, err = handler(context.Background(), nil, GetDraftSectionInput{Name: "draft-example-protocol"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing section_number")
		}
	})
}

// newGetIPRTestServer serves the Datatracker endpoints HandleGetIPR's
// underlying drafts.FetchIPR hits: document existence checks, an empty
// became_rfc/replaces relateddocument response (the fan-out itself is
// covered by ingest/drafts's own tests; this only needs to prove the tool
// layer wires rfc/name through correctly), and one posted disclosure each
// for rfc9000 and draft-ipr-example.
func newGetIPRTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/doc/document/rfc9000/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "rfc9000"}`))
	})
	mux.HandleFunc("/api/v1/doc/document/rfc7000/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "rfc7000"}`))
	})
	mux.HandleFunc("/api/v1/doc/document/draft-ipr-example/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"name": "draft-ipr-example"}`))
	})
	mux.HandleFunc("/api/v1/doc/document/rfc404/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/v1/doc/relateddocument/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"meta": {"total_count": 0}, "objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/holderiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("docs__name") {
		case "rfc9000":
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 900, "title": "Disclosure against RFC 9000", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Acme Corp", "licensing": "/api/v1/name/iprlicensetypename/reasonable/",
				 "has_patent_pending": true, "patent_info": "US0000000", "time": "2022-01-01T00:00:00Z",
				 "docs": ["/api/v1/doc/document/rfc9000/"]}
			]}`))
		case "draft-ipr-example":
			_, _ = w.Write([]byte(`{"objects": [
				{"id": 901, "title": "Disclosure against the draft", "state": "/api/v1/name/iprdisclosurestatename/posted/",
				 "holder_legal_name": "Beta LLC", "has_patent_pending": false, "time": "2023-01-01T00:00:00Z",
				 "docs": ["/api/v1/doc/document/draft-ipr-example/"]}
			]}`))
		default:
			_, _ = w.Write([]byte(`{"objects": []}`))
		}
	})
	mux.HandleFunc("/api/v1/ipr/thirdpartyiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})
	mux.HandleFunc("/api/v1/ipr/genericiprdisclosure/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"objects": []}`))
	})

	return httptest.NewServer(mux)
}

func TestHandleGetIPR(t *testing.T) {
	ts := newGetIPRTestServer(t)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleGetIPR(ts.Client())

	t.Run("by rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{RFC: 9000})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		var out drafts.IPRResult
		if unmarshalErr := json.Unmarshal([]byte(getTextContent(result)), &out); unmarshalErr != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", unmarshalErr, getTextContent(result))
		}
		if out.TotalCount != 1 || len(out.Disclosures) != 1 || out.Disclosures[0].ID != 900 {
			t.Fatalf("out = %+v", out)
		}
		if out.Disclosures[0].Holder != "Acme Corp" || out.Disclosures[0].Licensing != "reasonable" {
			t.Errorf("Disclosures[0] = %+v", out.Disclosures[0])
		}
	})

	t.Run("by draft name", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{Name: "draft-ipr-example"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var out drafts.IPRResult
		if unmarshalErr := json.Unmarshal([]byte(getTextContent(result)), &out); unmarshalErr != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", unmarshalErr, getTextContent(result))
		}
		if out.TotalCount != 1 || out.Disclosures[0].ID != 901 {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("draft name with embedded revision is stripped before lookup", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{Name: "draft-ipr-example-03"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		var out drafts.IPRResult
		if unmarshalErr := json.Unmarshal([]byte(getTextContent(result)), &out); unmarshalErr != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", unmarshalErr, getTextContent(result))
		}
		if len(out.SearchedDocs) != 1 || out.SearchedDocs[0] != "draft-ipr-example" {
			t.Errorf("SearchedDocs = %v, want the revision suffix stripped", out.SearchedDocs)
		}
	})

	t.Run("empty result for a document with no disclosures", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{RFC: 7000})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		var out drafts.IPRResult
		if unmarshalErr := json.Unmarshal([]byte(getTextContent(result)), &out); unmarshalErr != nil {
			t.Fatalf("failed to unmarshal: %v\n%s", unmarshalErr, getTextContent(result))
		}
		if out.TotalCount != 0 || len(out.Disclosures) != 0 {
			t.Errorf("out = %+v, want zero disclosures, not an error", out)
		}
	})

	t.Run("unknown rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{RFC: 404})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for an unknown RFC number")
		}
		if !strings.Contains(getTextContent(result), "rfc404") {
			t.Errorf("expected error to name the attempted document, got: %s", getTextContent(result))
		}
	})

	t.Run("both rfc and name given", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{RFC: 9000, Name: "draft-ipr-example"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result when both rfc and name are given")
		}
	})

	t.Run("neither rfc nor name given", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetIPRInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result when neither rfc nor name is given")
		}
	})
}

func TestHandleGetDraftSection_CacheHit(t *testing.T) {
	var metaRequests, textRequests atomic.Int32
	ts := newDraftTestServer(t, &metaRequests, &textRequests)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	handler := HandleGetDraftSection(ts.Client())
	for range 2 {
		if _, _, err := handler(context.Background(), nil, GetDraftSectionInput{
			Name: "draft-example-protocol", SectionNumber: "1",
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if metaRequests.Load() != 1 {
		t.Errorf("metadata requests = %d, want 1 (2nd call's latest-rev resolution should hit drafts.FetchMetadata's 1h cache)", metaRequests.Load())
	}
	if textRequests.Load() != 1 {
		t.Errorf("text requests = %d, want 1 (2nd call should hit the on-disk body cache)", textRequests.Load())
	}
}
