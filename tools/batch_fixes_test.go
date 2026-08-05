package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestHandleListRFCs_StreamFilterCaseInsensitive(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListRFCs(d)

	result, _, err := handler(context.Background(), nil, ListRFCsInput{Stream: "legacy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, `"number": 793`) {
		t.Errorf("expected RFC 793 for stream=legacy, got: %s", text)
	}
	if strings.Contains(text, `"number": 4271`) {
		t.Errorf("expected only RFC 793, got: %s", text)
	}
}

func TestHandleListRFCs_WGFilterCaseInsensitive(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListRFCs(d)

	result, _, err := handler(context.Background(), nil, ListRFCsInput{WG: "IDR"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, `"number": 4271`) {
		t.Errorf("expected RFC 4271 for wg=IDR, got: %s", text)
	}
	if strings.Contains(text, `"number": 9293`) {
		t.Errorf("expected only RFC 4271, got: %s", text)
	}
}

func TestHandleListRFCs_NegativeLimitUsesDefault(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListRFCs(d)

	// A negative limit means "no limit" inside the db layer (internal use
	// only); the tool boundary must clamp it to the default instead.
	result, _, err := handler(context.Background(), nil, ListRFCsInput{Limit: -1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, `"limit": 20`) {
		t.Errorf("expected default limit 20 in result, got: %s", text)
	}
}

func TestHandleGetMetadata_ErrataAlwaysPresent(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetMetadata(d)

	// RFC 9293 has no seeded errata; the key must still serialize as [].
	result, _, err := handler(context.Background(), nil, GetMetadataInput{RFC: 9293})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, `"errata": []`) {
		t.Errorf("expected empty errata array to be present, got: %s", text)
	}
}

func TestPaginateText_MaxCharsIsHardCap(t *testing.T) {
	// Previously the smart cut (extend to the next paragraph boundary) ran
	// after the max_chars truncation, so the empty line after "bbbb" pulled
	// the result past the byte cap.
	content := "aaaa\nbbbb\n\ncccc\ndddd"
	result := paginateText(content, 0, 0, 6)
	text := getTextContent(result)
	if !strings.Contains(text, "[Lines 1-1 of 5]") {
		t.Errorf("expected the cut to stop at line 1, got: %q", text)
	}
	if strings.Contains(text, "bbbb") || strings.Contains(text, "cccc") {
		t.Errorf("max_chars cap exceeded: %q", text)
	}
}

func TestPaginateText_MaxCharsSingleLongLineStillProgresses(t *testing.T) {
	// A single line longer than max_chars is returned whole (the one
	// documented exception), so pagination cannot get stuck.
	result := paginateText("aaaaaaaaaa\nbbbb", 0, 0, 4)
	text := getTextContent(result)
	if !strings.Contains(text, "aaaaaaaaaa") {
		t.Errorf("expected the oversized first line to be returned, got: %q", text)
	}
	if strings.Contains(text, "bbbb") {
		t.Errorf("expected only the first line, got: %q", text)
	}
}

func TestMaxRFCNumberIsCached(t *testing.T) {
	d := setupTestDB(t)

	n, ok := maxRFCNumber(d)
	if !ok || n != 9293 {
		t.Fatalf("maxRFCNumber = %d, %v; want 9293, true", n, ok)
	}
	// Second lookup must come from the cache: close the database first so
	// a fresh query would fail.
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	n, ok = maxRFCNumber(d)
	if !ok || n != 9293 {
		t.Errorf("cached maxRFCNumber = %d, %v; want 9293, true", n, ok)
	}
}

func TestNormalizeErrataSection(t *testing.T) {
	cases := map[string]string{
		"3.1":    "3.1",
		"3.1.":   "3.1",
		"3.1 .":  "3.1",
		" 3.1. ": "3.1",
		"3.1 . ": "3.1",
	}
	for in, want := range cases {
		if got := normalizeErrataSection(in); got != want {
			t.Errorf("normalizeErrataSection(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleGetErrata_SectionFilterMixedSuffix(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetErrata(d)

	result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271, Section: "5.1 ."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, `"id": 1`) {
		t.Errorf("expected erratum 1 to match section filter \"5.1 .\", got: %s", text)
	}
}

func TestHandleGetIPR_NegativeRFCRejected(t *testing.T) {
	handler := HandleGetIPR(&http.Client{})

	// A negative rfc used to be silently ignored when name was also given.
	result, _, err := handler(context.Background(), nil, GetIPRInput{RFC: -3, Name: "draft-example-protocol"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError result")
	}
	if text := getTextContent(result); !strings.Contains(text, "positive") {
		t.Errorf("expected explicit rejection of negative rfc, got: %q", text)
	}
}

func TestHandleSearch_NonPositiveRFCFilterRejected(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleSearch(d)

	for _, input := range []SearchInput{
		{Query: "tcp", RFCs: []int{9293, -1}},
		{Query: "tcp", RFC: -5},
	} {
		result, _, err := handler(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected IsError result for input %+v", input)
		}
		if text := getTextContent(result); !strings.Contains(text, "must be positive") {
			t.Errorf("expected validation message, got: %q", text)
		}
	}
}

func TestSplitDraftName(t *testing.T) {
	cases := []struct {
		in, base, rev string
	}{
		{"draft-ietf-quic-transport-34", "draft-ietf-quic-transport", "34"},
		{"draft-foo-bar-03", "draft-foo-bar", "03"},
		{"draft-foo-3", "draft-foo", "03"},
		{"draft-foo", "draft-foo", ""},
		// A longer digit run is part of the name, not a revision.
		{"draft-foo-2029", "draft-foo-2029", ""},
	}
	for _, c := range cases {
		base, rev := splitDraftName(c.in)
		if base != c.base || rev != c.rev {
			t.Errorf("splitDraftName(%q) = (%q, %q), want (%q, %q)", c.in, base, rev, c.base, c.rev)
		}
	}
}
