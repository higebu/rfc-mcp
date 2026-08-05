package tools

import (
	"math"
	"strings"
	"testing"
)

func TestPaginateText_MaxIntDoesNotPanic(t *testing.T) {
	// Regression test: offset >= 1 with maxLines near MaxInt used to wrap
	// end := offset + maxLines negative and panic on the slice index.
	result := paginateText("line1\nline2\nline3\nline4", 1, math.MaxInt, 0)
	text := getTextContent(result)
	if !strings.Contains(text, "line2") || !strings.Contains(text, "line4") {
		t.Errorf("expected lines 2-4 in output, got: %q", text)
	}
	if strings.Contains(text, "line1\n") {
		t.Errorf("expected line1 to be skipped by offset, got: %q", text)
	}
}

func TestPaginateText_MaxIntOffsetAndMaxLines(t *testing.T) {
	result := paginateText("line1\nline2", math.MaxInt, math.MaxInt, 0)
	text := getTextContent(result)
	if !strings.Contains(text, "No content at offset") {
		t.Errorf("expected out-of-range offset message, got: %q", text)
	}
}

// TestPaginateText_ZeroMaxLinesUsesDefault pins the behavior the max_lines
// schema text documents: 0 (indistinguishable from omitted with int +
// omitempty) means the default page size, not "all lines".
func TestPaginateText_ZeroMaxLinesUsesDefault(t *testing.T) {
	content := strings.TrimSuffix(strings.Repeat("x\n", 500), "\n")
	result := paginateText(content, 0, 0, 0)
	text := getTextContent(result)
	if !strings.Contains(text, "[Lines 1-200 of 500]") {
		t.Errorf("expected default 200-line page, got header: %q", strings.SplitN(text, "\n", 2)[0])
	}
	if !strings.Contains(text, "[Truncated. Use offset=200 to continue]") {
		t.Errorf("expected truncation notice at line 200, got: %q", text[len(text)-80:])
	}
}

// TestPaginateText_SmartCutRespectsMaxChars: maxChars is a hard byte cap
// even when the initial [offset,end) window already fits under it -- the
// smart-cut paragraph extension must not push the total past maxChars.
func TestPaginateText_SmartCutRespectsMaxChars(t *testing.T) {
	// 10 short lines (counted as 5 bytes each = 50), then a 20-byte line
	// and an empty line, both inside the lookahead window (10/5 = 2
	// lines). Without the cap check the extension would include the long
	// line and blow past maxChars=60.
	lines := make([]string, 0, 13)
	for i := 0; i < 10; i++ {
		lines = append(lines, "aaaa")
	}
	lines = append(lines, strings.Repeat("b", 20), "", "tail")
	content := strings.Join(lines, "\n")

	result := paginateText(content, 0, 10, 60)
	text := getTextContent(result)
	if strings.Contains(text, "bbbb") {
		t.Fatalf("smart-cut extension exceeded maxChars: long line included in %q", text)
	}
	if !strings.Contains(text, "[Lines 1-10 of 13]") {
		t.Errorf("expected window to stay at 10 lines, got header: %q", strings.SplitN(text, "\n", 2)[0])
	}
	body := text
	if i := strings.Index(body, "\n\n"); i >= 0 {
		body = body[i+2:]
	}
	if i := strings.Index(body, "\n\n[Truncated"); i >= 0 {
		body = body[:i]
	}
	if len(body) > 60 {
		t.Errorf("body is %d bytes, exceeds maxChars=60: %q", len(body), body)
	}
}

func TestPaginateText_NegativeInputsUseDefaults(t *testing.T) {
	result := paginateText("line1\nline2\nline3", -5, -5, -5)
	text := getTextContent(result)
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got: %q", want, text)
		}
	}
}
