package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	return testutil.SetupTestDB(t)
}

func getTextContent(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestHandleListRFCs(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListRFCs(d)

	t.Run("default listing excludes not_issued", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListRFCsInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		for _, want := range []string{`"number": 793`, `"number": 4271`, `"number": 9293`} {
			if !strings.Contains(text, want) {
				t.Errorf("expected %s in output, got: %s", want, text)
			}
		}
		if strings.Contains(text, "9999") {
			t.Errorf("expected not_issued RFC 9999 to be excluded, got: %s", text)
		}
		if !strings.Contains(text, `"total_count": 3`) {
			t.Errorf("expected total_count 3, got: %s", text)
		}
		// Status is stored verbatim (all-caps) in the db layer but must be
		// title-cased for display here, same as get_metadata.
		if !strings.Contains(text, `"status": "Draft Standard"`) {
			t.Errorf("expected title-cased status 'Draft Standard', got: %s", text)
		}
		if strings.Contains(text, `"status": "DRAFT STANDARD"`) {
			t.Errorf("expected no raw all-caps status in output, got: %s", text)
		}
	})

	t.Run("query filter", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListRFCsInput{Query: "Border"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"number": 4271`) {
			t.Errorf("expected RFC 4271 in output, got: %s", text)
		}
		if strings.Contains(text, `"number": 9293`) {
			t.Errorf("expected only RFC 4271, got: %s", text)
		}
	})

	t.Run("status filter is case-insensitive", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListRFCsInput{Status: "draft standard"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"number": 4271`) {
			t.Errorf("expected RFC 4271 in output, got: %s", text)
		}
		if strings.Contains(text, `"number": 9293`) {
			t.Errorf("expected only RFC 4271, got: %s", text)
		}
	})

	t.Run("stream filter", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListRFCsInput{Stream: "Legacy"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"number": 793`) {
			t.Errorf("expected RFC 793 in output, got: %s", text)
		}
		if strings.Contains(text, `"number": 4271`) {
			t.Errorf("expected only RFC 793, got: %s", text)
		}
	})

	t.Run("pagination limit", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListRFCsInput{Limit: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"number": 793`) {
			t.Errorf("expected first RFC (793) in output, got: %s", text)
		}
		if strings.Contains(text, `"number": 4271`) || strings.Contains(text, `"number": 9293`) {
			t.Errorf("expected only one RFC in output, got: %s", text)
		}
	})
}

func TestHandleGetMetadata(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetMetadata(d)

	t.Run("happy path JSON shape", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetMetadataInput{RFC: 4271})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}

		var out getMetadataOutput
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal output: %v\n%s", err, getTextContent(result))
		}

		if out.RFC != 4271 {
			t.Errorf("RFC = %d, want 4271", out.RFC)
		}
		if out.Title != "A Border Gateway Protocol 4 (BGP-4)" {
			t.Errorf("Title = %q", out.Title)
		}
		if out.Status != "Draft Standard" {
			t.Errorf("Status = %q, want title-cased 'Draft Standard'", out.Status)
		}
		if out.Stream != "IETF" {
			t.Errorf("Stream = %q, want IETF", out.Stream)
		}
		if out.WG != "idr" || out.Area != "rtg" {
			t.Errorf("WG/Area = %q/%q, want idr/rtg", out.WG, out.Area)
		}
		if len(out.Obsoletes) != 1 || out.Obsoletes[0] != 1771 {
			t.Errorf("Obsoletes = %v, want [1771]", out.Obsoletes)
		}
		if len(out.Errata) != 2 {
			t.Fatalf("Errata length = %d, want 2", len(out.Errata))
		}
		if out.Errata[0].ID != 1 || out.Errata[0].Status != "Verified" || out.Errata[0].Type != "Technical" || out.Errata[0].Section != "5.1" {
			t.Errorf("Errata[0] = %+v, want {1 Verified Technical 5.1}", out.Errata[0])
		}
	})

	t.Run("not issued", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetMetadataInput{RFC: 9999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for not-issued RFC")
		}
		if !strings.Contains(getTextContent(result), "was never issued") {
			t.Errorf("expected 'was never issued' message, got: %s", getTextContent(result))
		}
	})

	t.Run("unknown RFC", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetMetadataInput{RFC: 999999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for unknown RFC")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "range from 1 to 9293") {
			t.Errorf("expected not-found message with range hint, got: %s", text)
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetMetadataInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})
}

func TestHandleGetErrata(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetErrata(d)

	t.Run("basic retrieval returns full detail", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}

		var out []db.Errata
		if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
			t.Fatalf("failed to unmarshal output: %v\n%s", err, getTextContent(result))
		}
		if len(out) != 2 {
			t.Fatalf("expected 2 errata, got %d: %+v", len(out), out)
		}
		if out[0].ID != 1 || out[0].OrigText == "" || out[0].CorrectText == "" {
			t.Errorf("expected full orig_text/correct_text for errata 1, got: %+v", out[0])
		}
	})

	t.Run("status filter is case-insensitive", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271, Status: "verified"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"id": 1`) {
			t.Errorf("expected errata 1 in output, got: %s", text)
		}
		if strings.Contains(text, `"id": 2`) {
			t.Errorf("expected errata 2 filtered out, got: %s", text)
		}
	})

	t.Run("type filter is case-insensitive", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271, Type: "editorial"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"id": 2`) {
			t.Errorf("expected errata 2 in output, got: %s", text)
		}
		if strings.Contains(text, `"id": 1`) {
			t.Errorf("expected errata 1 filtered out, got: %s", text)
		}
	})

	t.Run("section filter", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271, Section: "5.1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"id": 1`) {
			t.Errorf("expected errata 1 in output, got: %s", text)
		}
		if strings.Contains(text, `"id": 2`) {
			t.Errorf("expected errata 2 (section 5) filtered out, got: %s", text)
		}
	})

	// errata.json is inconsistent about a trailing "." on section numbers
	// for the same document (e.g. "3.3.2." vs "3.3.1" on real RFC 9293
	// data); the section filter must ignore it on either side.
	t.Run("section filter ignores trailing dot", func(t *testing.T) {
		d := setupTestDB(t)
		if err := d.Exec(`INSERT INTO errata (id, rfc, status, type, section, orig_text, correct_text) VALUES
			(200, 9293, 'Verified', 'Editorial', '3.3.2.', 'old', 'new')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		handler := HandleGetErrata(d)

		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 9293, Section: "3.3.2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), `"id": 200`) {
			t.Errorf("expected errata 200 to match despite trailing dot, got: %s", getTextContent(result))
		}
	})

	t.Run("filter matching nothing returns empty array, not an error", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 4271, Status: "Rejected"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		if getTextContent(result) != "[]" {
			t.Errorf("expected empty array, got: %s", getTextContent(result))
		}
	})

	t.Run("rfc with no errata returns empty array, not an error", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 9293})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		if getTextContent(result) != "[]" {
			t.Errorf("expected empty array, got: %s", getTextContent(result))
		}
	})

	t.Run("not issued", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 9999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for not-issued RFC")
		}
		if !strings.Contains(getTextContent(result), "was never issued") {
			t.Errorf("expected 'was never issued' message, got: %s", getTextContent(result))
		}
	})

	t.Run("unknown RFC", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{RFC: 999999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for unknown RFC")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "range from 1 to 9293") {
			t.Errorf("expected not-found message with range hint, got: %s", text)
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetErrataInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})
}

func TestHandleGetTOC(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetTOC(d)

	t.Run("valid rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{RFC: 9293})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Table of Contents") {
			t.Errorf("expected TOC header, got: %s", text)
		}
		if !strings.Contains(text, "3.1 Header Format") {
			t.Errorf("expected section 3.1, got: %s", text)
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})

	t.Run("not issued", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{RFC: 9999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), "was never issued") {
			t.Errorf("expected 'was never issued' message, got: %s", getTextContent(result))
		}
	})

	t.Run("unknown rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{RFC: 555555})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "range from 1 to 9293") {
			t.Errorf("expected not-found message with range hint, got: %s", text)
		}
	})
}

func TestHandleGetSection(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetSection(d)

	t.Run("valid section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9293, SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Transmission Control Protocol (TCP)") {
			t.Errorf("expected section content, got: %s", text)
		}
		// A single-section fetch is prefixed with its own heading, so the
		// caller can confirm which section it got.
		if !strings.Contains(text, "1.  Introduction") {
			t.Errorf("expected section's own heading line, got: %s", text)
		}
	})

	t.Run("pagination with subsections", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			RFC: 9293, SectionNumber: "3", IncludeSubsections: true, MaxLines: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Truncated") {
			t.Errorf("expected truncation notice, got: %s", text)
		}
	})

	t.Run("include_subsections interleaves child heading lines in document order", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9293, SectionNumber: "3", IncludeSubsections: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		idx3 := strings.Index(text, "3.  Functional Specification")
		idx31 := strings.Index(text, "3.1.  Header Format")
		idx311 := strings.Index(text, "3.1.1.  Source Port")
		if idx3 < 0 || idx31 < 0 || idx311 < 0 {
			t.Fatalf("expected headings for 3, 3.1, 3.1.1, got: %s", text)
		}
		if idx3 >= idx31 || idx31 >= idx311 {
			t.Errorf("expected heading order 3 < 3.1 < 3.1.1, got positions %d, %d, %d", idx3, idx31, idx311)
		}
	})

	// A title-only parent (empty Content, body text living entirely in its
	// children) must not silently return whitespace when fetched without
	// include_subsections -- that's indistinguishable from a bug. It should
	// list its children instead, both when it has grandchildren-bearing
	// children and when a child is itself a plain leaf.
	t.Run("empty title-only parent without include_subsections returns guidance", func(t *testing.T) {
		d := setupTestDB(t)
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8300, '8', 'FSM Events', 1, NULL, ''),
			(8300, '8.1', 'Optional Events', 2, '8', ''),
			(8300, '8.1.1', 'Optional Events A', 3, '8.1', 'content a'),
			(8300, '8.1.2', 'Optional Events B', 3, '8.1', 'content b')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		handler := HandleGetSection(d)

		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 8300, SectionNumber: "8.1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		for _, want := range []string{"Section 8.1", "Optional Events", "has no body text of its own", "8.1.1", "8.1.2", "include_subsections=true"} {
			if !strings.Contains(text, want) {
				t.Errorf("expected guidance to mention %q, got: %s", want, text)
			}
		}
	})

	t.Run("empty leaf with no children still returns plain heading-only output", func(t *testing.T) {
		d := setupTestDB(t)
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8301, '9', 'Reserved', 1, NULL, '')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		handler := HandleGetSection(d)

		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 8301, SectionNumber: "9"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "9.  Reserved") {
			t.Errorf("expected heading line, got: %s", text)
		}
		if strings.Contains(text, "has no body text of its own") {
			t.Errorf("expected no guidance message for a childless leaf, got: %s", text)
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})

	t.Run("section_number required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9293})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing section_number")
		}
	})

	t.Run("nonexistent section in existing rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9293, SectionNumber: "99"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for nonexistent section")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found in RFC 9293") {
			t.Errorf("expected section-not-found message, got: %s", text)
		}
	})

	// A queried number with no heading of its own, but with a heading
	// still pointing at it via ParentNumber (the rare case
	// reparentDanglingAncestors wouldn't have produced, but still handled
	// defensively): guidance names the subsections and include_subsections
	// still works, since the ParentNumber chain to them is intact.
	t.Run("missing intermediate section found via parent_number offers include_subsections", func(t *testing.T) {
		d := setupTestDB(t)
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8500, '7', 'Protocol Classes', 1, NULL, 'content a'),
			(8500, '7.0.1', 'Characteristics of Class 0', 3, '7.0', 'content b'),
			(8500, '7.0.2', 'Functions of Class 0', 3, '7.0', 'content c')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		handler := HandleGetSection(d)

		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 8500, SectionNumber: "7.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		for _, want := range []string{"Section 7.0", "has no heading of its own", "7.0.1", "7.0.2", "include_subsections=true"} {
			if !strings.Contains(text, want) {
				t.Errorf("expected guidance to mention %q, got: %s", want, text)
			}
		}
	})

	// The normal post-reparentDanglingAncestors shape: nothing's
	// parent_number points at the missing number any more (its real
	// children were rerouted past it to the nearest existing ancestor),
	// so guidance falls back to a number-prefix search and must not claim
	// include_subsections=true would work on the missing number itself.
	t.Run("missing intermediate section found via prefix fallback does not offer include_subsections", func(t *testing.T) {
		d := setupTestDB(t)
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8501, '7', 'Protocol Classes', 1, NULL, 'content a'),
			(8501, '7.0.1', 'Characteristics of Class 0', 2, '7', 'content b'),
			(8501, '7.0.2', 'Functions of Class 0', 2, '7', 'content c')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		handler := HandleGetSection(d)

		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 8501, SectionNumber: "7.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		for _, want := range []string{"Section 7.0", "has no heading of its own", "7.0.1", "7.0.2", "Request one of them directly"} {
			if !strings.Contains(text, want) {
				t.Errorf("expected guidance to mention %q, got: %s", want, text)
			}
		}
		if strings.Contains(text, "include_subsections=true") {
			t.Errorf("expected no include_subsections claim (parent_number chain doesn't lead here), got: %s", text)
		}
	})

	t.Run("unknown rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 555555, SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "range from 1 to 9293") {
			t.Errorf("expected not-found message with range hint, got: %s", text)
		}
	})

	t.Run("not issued rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9999, SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), "was never issued") {
			t.Errorf("expected 'was never issued' message, got: %s", getTextContent(result))
		}
	})

	t.Run("offset beyond content", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 9293, SectionNumber: "1", Offset: 10000})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), "No content at offset") {
			t.Errorf("expected overflow message, got: %s", getTextContent(result))
		}
	})

	// Regression test for the Tier-3 whole-body fallback's section number:
	// it used to be "" (unretrievable via get_section, which requires a
	// section_number), now it's "body" (see
	// ingest/rfctxt.ParseRFCText's Tier-3 fallback doc comment).
	t.Run("tier-3 body slug is retrievable and shown in the TOC", func(t *testing.T) {
		if err := d.Exec(`INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
			(8100, 'body', 'Untitled Document', 1, NULL, 'Whole free-form document text with no section structure.')`); err != nil {
			t.Fatalf("seed: %v", err)
		}

		tocResult, _, err := HandleGetTOC(d)(context.Background(), nil, GetTOCInput{RFC: 8100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(tocResult), "- body Untitled Document") {
			t.Errorf("expected TOC to show the 'body' section, got: %s", getTextContent(tocResult))
		}

		result, _, err := handler(context.Background(), nil, GetSectionInput{RFC: 8100, SectionNumber: "body"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		if !strings.Contains(getTextContent(result), "Whole free-form document text") {
			t.Errorf("expected section content, got: %s", getTextContent(result))
		}
	})
}

func TestHandleGetDocument(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetDocument(d)

	t.Run("valid rfc returns full document in order", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 9293})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		idxIntro := strings.Index(text, "connection-oriented transport layer protocol")
		idxHeader := strings.Index(text, "3.1.  Header Format")
		if idxIntro < 0 || idxHeader < 0 {
			t.Fatalf("expected content from multiple sections, got: %s", text)
		}
		if idxIntro >= idxHeader {
			t.Errorf("expected section 1 before section 3.1, got positions %d, %d", idxIntro, idxHeader)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 9293, MaxLines: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), "Truncated") {
			t.Errorf("expected truncation notice, got: %s", getTextContent(result))
		}
	})

	t.Run("second page via offset", func(t *testing.T) {
		first, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 9293, MaxLines: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		second, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 9293, Offset: 1, MaxLines: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if getTextContent(first) == getTextContent(second) {
			t.Errorf("expected different content for different offsets")
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDocumentInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})

	t.Run("not issued", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 9999})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(getTextContent(result), "was never issued") {
			t.Errorf("expected 'was never issued' message, got: %s", getTextContent(result))
		}
	})

	t.Run("unknown rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetDocumentInput{RFC: 555555})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "range from 1 to 9293") {
			t.Errorf("expected not-found message with range hint, got: %s", text)
		}
	})
}

func TestHandleSearch(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleSearch(d)

	t.Run("empty query", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{Query: ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty query")
		}
	})

	t.Run("valid query", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{Query: "handshake"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"rfc": 9293`) {
			t.Errorf("expected search result for RFC 9293, got: %s", text)
		}
	})

	t.Run("rfc filter restricts results", func(t *testing.T) {
		unfiltered, _, err := handler(context.Background(), nil, SearchInput{Query: "Transmission"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		unfilteredText := getTextContent(unfiltered)
		if !strings.Contains(unfilteredText, `"rfc": 793`) || !strings.Contains(unfilteredText, `"rfc": 9293`) {
			t.Fatalf("expected both RFCs unfiltered, got: %s", unfilteredText)
		}

		result, _, err := handler(context.Background(), nil, SearchInput{Query: "Transmission", RFC: 793})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"rfc": 793`) {
			t.Errorf("expected RFC 793 in output, got: %s", text)
		}
		if strings.Contains(text, `"rfc": 9293`) {
			t.Errorf("expected RFC 9293 to be filtered out, got: %s", text)
		}
	})

	t.Run("rfcs takes precedence over rfc", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{Query: "Transmission", RFC: 9293, RFCs: []int{793}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"rfc": 793`) {
			t.Errorf("expected RFC 793 in output, got: %s", text)
		}
		if strings.Contains(text, `"rfc": 9293`) {
			t.Errorf("expected RFC 9293 to be excluded (rfcs takes precedence), got: %s", text)
		}
	})
}

func TestHandleGetReferences(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetReferences(d)

	t.Run("outgoing", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{RFC: 4271, SectionNumber: "5.1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"target_rfc": 9293`) {
			t.Errorf("expected reference to RFC 9293, got: %s", text)
		}
		if !strings.Contains(text, `"target_rfc": 793`) {
			t.Errorf("expected reference to RFC 793, got: %s", text)
		}
	})

	t.Run("outgoing without section_number", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{RFC: 4271})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error for outgoing without section_number")
		}
	})

	t.Run("incoming", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{RFC: 9293, Direction: "incoming"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"source_rfc": 4271`) {
			t.Errorf("expected RFC 4271 as source, got: %s", text)
		}
	})

	t.Run("incoming with section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{RFC: 9293, SectionNumber: "3.1", Direction: "incoming"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, `"source_rfc": 4271`) {
			t.Errorf("expected RFC 4271 as source, got: %s", text)
		}
	})

	t.Run("rfc required", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing rfc")
		}
	})

	t.Run("no results", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{RFC: 9293, SectionNumber: "1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if getTextContent(result) != "[]" {
			t.Errorf("expected empty array, got: %s", getTextContent(result))
		}
	})
}
