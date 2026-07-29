package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/higebu/rfc-mcp/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServerInMemorySession exercises the full MCP handshake and a tool call
// over an in-memory transport -- the protocol layer that the direct handler
// tests in tools/ bypass.
func TestServerInMemorySession(t *testing.T) {
	tests := []struct {
		name      string
		drafts    bool
		wantTools int
	}{
		{"rfc tools only", false, 8},
		{"with draft tools", true, 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testutil.SetupTestDB(t)
			s := newServer(d, tt.drafts)

			ctx := context.Background()
			ct, st := mcp.NewInMemoryTransports()
			ss, err := s.Connect(ctx, st, nil)
			if err != nil {
				t.Fatalf("server connect: %v", err)
			}
			defer ss.Close()

			client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
			cs, err := client.Connect(ctx, ct, nil)
			if err != nil {
				t.Fatalf("client connect: %v", err)
			}
			defer cs.Close()

			tl, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("list tools: %v", err)
			}
			if len(tl.Tools) != tt.wantTools {
				t.Errorf("got %d tools, want %d", len(tl.Tools), tt.wantTools)
			}

			res, err := cs.CallTool(ctx, &mcp.CallToolParams{
				Name:      "list_rfcs",
				Arguments: map[string]any{},
			})
			if err != nil {
				t.Fatalf("call list_rfcs: %v", err)
			}
			if res.IsError {
				t.Fatalf("list_rfcs returned error: %+v", res.Content)
			}
			tc, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("content is %T, want *mcp.TextContent", res.Content[0])
			}
			if !strings.Contains(tc.Text, "4271") {
				t.Errorf("list_rfcs result missing seeded RFC 4271: %s", tc.Text)
			}
		})
	}
}

// TestStatelessHTTPNegotiatesLatestProtocol asserts that the streamable HTTP
// handler in stateless mode -- the configuration cmdServe uses -- negotiates
// protocol version 2026-07-28 and round-trips a tool call.
func TestStatelessHTTPNegotiatesLatestProtocol(t *testing.T) {
	d := testutil.SetupTestDB(t)
	s := newServer(d, false)

	h := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cs.Close()

	if got := cs.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Errorf("negotiated protocol version = %q, want %q", got, "2026-07-28")
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"query": "BGP"},
	})
	if err != nil {
		t.Fatalf("call search over HTTP: %v", err)
	}
	if res.IsError {
		t.Errorf("search returned error: %+v", res.Content)
	}
}
