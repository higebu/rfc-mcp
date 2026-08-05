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

func TestPaginateText_NegativeInputsUseDefaults(t *testing.T) {
	result := paginateText("line1\nline2\nline3", -5, -5, -5)
	text := getTextContent(result)
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q in output, got: %q", want, text)
		}
	}
}
