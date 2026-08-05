package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/higebu/rfc-mcp/ingest/drafts"
)

// TestDBErrorsAreGenericAndReturned covers the internalError contract for
// every DB-backed tool: on a real database failure (forced here by closing
// the database) the handler must return a non-nil Go error, mark the
// result IsError, and keep the underlying detail ("database is closed")
// out of the client-visible text.
func TestDBErrorsAreGenericAndReturned(t *testing.T) {
	d := setupTestDB(t)
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ctx := context.Background()

	check := func(t *testing.T, text string, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected a non-nil Go error for a real database failure")
		}
		for _, s := range []string{text, err.Error()} {
			if strings.Contains(s, "database is closed") || strings.Contains(s, "sql:") {
				t.Errorf("internal detail leaked: %q", s)
			}
		}
	}

	t.Run("get_toc", func(t *testing.T) {
		result, _, err := HandleGetTOC(d)(ctx, nil, GetTOCInput{RFC: 9293})
		check(t, getTextContent(result), err)
		if !result.IsError {
			t.Error("expected IsError result")
		}
		if got := getTextContent(result); got != "failed to get TOC for RFC 9293" {
			t.Errorf("client text = %q", got)
		}
	})

	t.Run("get_section", func(t *testing.T) {
		result, _, err := HandleGetSection(d)(ctx, nil, GetSectionInput{RFC: 9293, SectionNumber: "1"})
		check(t, getTextContent(result), err)
	})

	t.Run("get_document", func(t *testing.T) {
		result, _, err := HandleGetDocument(d)(ctx, nil, GetDocumentInput{RFC: 9293})
		check(t, getTextContent(result), err)
	})

	t.Run("get_metadata", func(t *testing.T) {
		result, _, err := HandleGetMetadata(d)(ctx, nil, GetMetadataInput{RFC: 9293})
		check(t, getTextContent(result), err)
	})

	t.Run("get_errata", func(t *testing.T) {
		result, _, err := HandleGetErrata(d)(ctx, nil, GetErrataInput{RFC: 4271})
		check(t, getTextContent(result), err)
	})

	t.Run("list_rfcs", func(t *testing.T) {
		result, _, err := HandleListRFCs(d)(ctx, nil, ListRFCsInput{})
		check(t, getTextContent(result), err)
	})

	t.Run("get_references", func(t *testing.T) {
		result, _, err := HandleGetReferences(d)(ctx, nil, GetReferencesInput{RFC: 4271, SectionNumber: "5.1"})
		check(t, getTextContent(result), err)
	})

	t.Run("search", func(t *testing.T) {
		result, _, err := HandleSearch(d)(ctx, nil, SearchInput{Query: "tcp"})
		check(t, getTextContent(result), err)
	})
}

// TestSearchSyntaxErrorIsShownVerbatim: a bad FTS5 expression is the
// caller's problem and its detail must stay client-visible (unlike
// internal database failures, covered above).
func TestSearchSyntaxErrorIsShownVerbatim(t *testing.T) {
	d := setupTestDB(t)
	result, _, err := HandleSearch(d)(context.Background(), nil, SearchInput{Query: "AND AND"})
	if err != nil {
		t.Fatalf("unexpected Go error for a query syntax problem: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError result")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "syntax error") {
		t.Errorf("expected FTS5 syntax detail in client text, got: %q", text)
	}
}

// TestFetchAndParseDraftPreservesNotFound: the error returned for an
// unknown draft must keep drafts.ErrNotFound in its chain (it used to be
// flattened through errors.New, losing the identity).
func TestFetchAndParseDraftPreservesNotFound(t *testing.T) {
	ts := newDraftTestServer(t, nil, nil)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	_, _, err := fetchAndParseDraft(context.Background(), ts.Client(), "draft-does-not-exist", "")
	if err == nil {
		t.Fatal("expected an error for an unknown draft")
	}
	if !errors.Is(err, drafts.ErrNotFound) {
		t.Errorf("errors.Is(err, drafts.ErrNotFound) = false, err = %v", err)
	}
}

// TestWrapDraftFetchErrorGenericFailure: a non-404 failure keeps the
// helpful label wording and wraps (rather than flattens) the cause.
func TestWrapDraftFetchErrorGenericFailure(t *testing.T) {
	cause := errors.New("connection refused")
	err := wrapDraftFetchError("draft-x-00", cause)
	if !errors.Is(err, cause) {
		t.Error("expected the cause to remain in the error chain")
	}
	if got := err.Error(); !strings.Contains(got, "failed to fetch draft draft-x-00") {
		t.Errorf("err = %q", got)
	}
}

// TestGetDraftSectionNotFoundMessage keeps the client-facing wording for
// an unknown draft intact through the errors.New -> %w change.
func TestGetDraftSectionNotFoundMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	ts := httptest.NewServer(mux)
	defer ts.Close()
	restore := redirectDraftsRoots(t, ts.URL)
	defer restore()

	result, _, err := HandleGetDraftSection(ts.Client())(context.Background(), nil, GetDraftSectionInput{
		Name: "draft-nope", SectionNumber: "1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError result")
	}
	if text := getTextContent(result); !strings.Contains(text, "draft not found") {
		t.Errorf("expected 'draft not found' wording, got: %q", text)
	}
}
