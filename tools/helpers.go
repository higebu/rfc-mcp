package tools

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/higebu/rfc-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultMaxLines = 200

// formatTOC renders a Table-of-Contents listing for sections as an
// indented bullet list, prefixed by header (e.g. "# RFC 4271 - Table of
// Contents\n\n"). Shared by get_toc and get_draft_toc.
func formatTOC(header string, sections []db.Section) string {
	var sb strings.Builder
	sb.WriteString(header)
	for _, s := range sections {
		indent := strings.Repeat("  ", s.Level-1)
		fmt.Fprintf(&sb, "%s- %s %s\n", indent, s.Number, s.Title)
	}
	return sb.String()
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// internalError is the full handler return value for an unexpected
// internal failure (database error, marshalling error): the underlying
// error is logged server-side, while the client sees only the generic
// clientMsg. The Go error handed back to the framework is deliberately
// built from clientMsg alone -- the SDK's typed-handler wrapper copies a
// non-nil error's text into client-visible content (discarding the
// handler's own result), so wrapping err itself would leak internal
// detail to the client. Returning a non-nil error still marks the call
// failed for the framework and exposes it to server middleware via
// CallToolResult.GetError.
func internalError(clientMsg string, err error) (*mcp.CallToolResult, any, error) {
	log.Printf("%s: %v", clientMsg, err)
	return errorResult(clientMsg), nil, errors.New(clientMsg)
}

func paginateText(content string, offset, maxLines, maxChars int) *mcp.CallToolResult {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	if offset < 0 {
		offset = 0
	}
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	// Clamp before computing end: offset and maxLines come from MCP tool
	// input, and offset+maxLines with maxLines near math.MaxInt would wrap
	// negative and panic on the slice index below.
	if maxLines > totalLines {
		maxLines = totalLines
	}

	if offset >= totalLines {
		return textResult(fmt.Sprintf("[No content at offset %d. Total lines: %d]", offset, totalLines))
	}

	end := offset + maxLines
	if end > totalLines {
		end = totalLines
	}

	if maxChars > 0 {
		charCount := 0
		charEnd := end
		for i := offset; i < end; i++ {
			charCount += len(lines[i]) + 1
			if charCount > maxChars {
				if i > offset {
					charEnd = i
				} else {
					charEnd = i + 1
				}
				break
			}
		}
		if charEnd < end {
			end = charEnd
		}
	}

	// Smart cut: extend to the next paragraph boundary (empty line).
	// maxLines * 1.2 caps how far we look ahead.
	if end < totalLines {
		linesUsed := end - offset
		hardLimit := end + linesUsed/5
		if hardLimit <= end {
			hardLimit = end + 1
		}
		if hardLimit > totalLines {
			hardLimit = totalLines
		}
		for i := end; i < hardLimit; i++ {
			if lines[i] == "" {
				end = i + 1
				break
			}
		}
	}

	truncated := end < totalLines

	var sb strings.Builder
	fmt.Fprintf(&sb, "[Lines %d-%d of %d]\n\n", offset+1, end, totalLines)
	for i := offset; i < end; i++ {
		if i > offset {
			sb.WriteByte('\n')
		}
		sb.WriteString(lines[i])
	}
	if truncated {
		fmt.Fprintf(&sb, "\n\n[Truncated. Use offset=%d to continue]", end)
	}

	return textResult(sb.String())
}
