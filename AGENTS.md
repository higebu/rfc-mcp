# AGENTS.md

## Project Overview

rfc-mcp is an MCP (Model Context Protocol) server that downloads IETF RFC
plain-text documents from rfc-editor.org, parses them by section, stores
them in a SQLite database with full-text search (FTS5), and serves them via
MCP tools.

## Tech Stack

- **Language**: Go 1.26+
- **MCP SDK**: `github.com/modelcontextprotocol/go-sdk`
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **Linter**: golangci-lint v2 (`.golangci.yml`)
- **CI**: GitHub Actions

## Project Structure

```
cmd/rfc-mcp/         # CLI entry point (serve, build, download, import, import-dir, update)
ingest/
  rfcindex/           # rfc-index.xml parser (streaming encoding/xml)
  errata/             # errata.json parser
  rfctxt/             # Plain-text RFC section parser (column-0 headings, TOC-anchor, whole-body fallback)
  pipeline/           # Download + cache + build/update orchestration
  drafts/             # On-demand Internet-Draft fetch: Datatracker search/metadata + archive body, own on-disk cache
db/                   # SQLite schema, queries, FTS5 full-text search, reference extraction, build metadata (meta table: built_at, rfc_index_fetched_at)
tools/                # MCP tool handlers (list_rfcs, get_metadata, get_errata, get_toc, get_section, get_document, search, get_references, search_drafts, get_draft_metadata, get_draft_toc, get_draft_section)
internal/testutil/    # Shared test helpers (SeedData for a small real-RFC fixture DB)
data/                 # Database files (gitignored except .gitkeep)
examples/systemd/     # Deployment examples (service + timer)
```

## Development Commands

```bash
# Build
make build                          # Build to bin/rfc-mcp
make install                        # Install to $GOPATH/bin

# Test
go test ./...                       # Run all tests
go test -race -coverprofile=coverage.out ./...  # With race detection + coverage

# Lint & Format
gofmt -l .                          # Check formatting
go vet ./...                        # Static analysis
golangci-lint run                   # Full lint (config: .golangci.yml)

# Build database (download + parse + import)
make build-db                       # Full RFC corpus (~8 min, ~865 MB)
make build-db FROM=9290 TO=9295      # ...or restrict to a numeric range
make import FILE=path/to/rfcNNNN.txt # Import a single file
make import-dir DIR=raw              # Import a directory of .txt files

# Download only (no import)
make download                       # Full corpus
make download FROM=9290 TO=9295      # ...or restrict to a numeric range

# Refresh an existing database (new RFCs + metadata/errata refresh)
make update

# Utilities
make db-info                        # Show database statistics
make clean                          # Remove bin/ and data/rfc.db
```

## Architecture

### Pipeline

The core feature is a pipeline (`ingest/pipeline/`) that:

1. Fetches `rfc-index.xml` (all RFC metadata) and `errata.json`, both cached
   under `$XDG_CACHE_HOME/rfc-mcp` (or `~/.cache/rfc-mcp`) with a TTL
   (default 24h, `RFC_MCP_CACHE_TTL_HOURS`).
2. Fetches each RFC's plain-text body individually (rfc-editor.org has no
   bulk tarball and rejects HEAD requests, so every fetch is a GET). Bodies
   are immutable once published, so the on-disk raw cache never expires.
3. Parses each body into sections (`ingest/rfctxt`) using a three-tier
   strategy: column-0 heading detection, then TOC-anchor matching for texts
   where that yields too few sections, then a whole-body fallback so every
   RFC produces at least one retrievable section.
4. Extracts cross-references (`RFC NNNN`, `Section X of RFC NNNN`, numeric
   bracket citations resolved via the References section) into
   `rfc_references`.
5. Inserts everything into SQLite with FTS5 indexing.

Uses a worker pool (`runtime.NumCPU()` workers by default) for parallel
per-RFC fetch+parse.

### Internet-Drafts

`ingest/drafts/` fetches Internet-Draft search results, metadata, and
plain-text bodies from the IETF Datatracker and archive on demand, at MCP
call time -- unlike the pipeline above, nothing is imported into SQLite
(drafts move too fast to be worth it). Draft bodies are cached on disk
forever per revision (immutable once submitted); Datatracker metadata is
cached for 1 hour (vs. the pipeline's 24h `rfc-index.xml` TTL). Set
`RFC_MCP_DISABLE_DRAFTS=1` to skip registering the draft tools for
offline/no-egress deployments.

### MCP Tools

Twelve tools are exposed via MCP:

| Tool | Description |
|------|-------------|
| `list_rfcs` | List RFCs, filterable by title substring, stream, status, working group |
| `get_metadata` | Title, status, dates, obsoletes/updates relationships, compact errata summary |
| `get_errata` | Full errata detail for an RFC (original/corrected text, notes), filterable by status, type, section |
| `get_toc` | Table of contents (section structure) for an RFC |
| `get_section` | Section content (paginated), addressed by number or slug |
| `get_document` | Full text of an RFC as one paginated document |
| `search` | Full-text search across all RFCs (FTS5 syntax) |
| `get_references` | Cross-references between RFCs (outgoing/incoming) |
| `search_drafts` | Search Internet-Drafts by title/name substring and/or working group (fetched on demand) |
| `get_draft_metadata` | Title, abstract, page count, submission/expiry dates, and the RFC number if published |
| `get_draft_toc` | Table of contents (section structure) for an Internet-Draft |
| `get_draft_section` | Section content (paginated), addressed the same way as `get_section` |

### Transport

- **stdio** (default): For Claude Code / IDE integration
- **HTTP**: With optional Bearer token auth (`RFC_MCP_BEARER_TOKEN`); `PORT`
  (Cloud Run / Heroku convention) forces HTTP transport when set

## Coding Standards

- Follow standard Go conventions (`gofmt`, `go vet`)
- Error handling: return errors with context, use `fmt.Errorf("...: %w", err)` for wrapping
- `errcheck` exceptions are configured in `.golangci.yml` for `io.WriteString`, `fmt.Fprint*`, `defer`, and test files
- No CGO — the project uses pure Go SQLite (`modernc.org/sqlite`)
- Section content is stored verbatim (no dedent/reflow) to preserve ABNF and packet-diagram alignment

## Testing

- Tests are co-located with source files (`*_test.go`)
- Use `internal/testutil.SeedData` for database tests — a small fixture DB
  seeded with a handful of real RFCs (9293, 793, 4271) covering filtering,
  full-text search, and cross-reference lookups
- Parser tests use real RFC `.txt` fixtures under `ingest/rfctxt/testdata/`
  spanning eras (RFC 9293 modern, RFC 4271/2119/1035 paginated, RFC 791/1
  free-form ancient documents)
- HTTP mocks use `net/http/httptest`
- Always run with `-race` flag in CI

## CI Pipeline

GitHub Actions (`.github/workflows/ci.yml`) runs on push/PR to `main`:

1. `go build ./...`
2. `go vet ./...`
3. `gofmt -l .` (must produce no output)
4. `golangci-lint run`
5. `go test -race -coverprofile=coverage.out ./...`
6. Codecov upload

## Environment Variables

| Variable | Description |
|----------|-------------|
| `RFC_MCP_TRANSPORT` | Transport type for `serve` (`stdio` or `http`); overridden by `--transport` |
| `RFC_MCP_ADDR` | HTTP listen address for `serve` (e.g. `:8080`); overridden by `--addr` |
| `RFC_MCP_BEARER_TOKEN` | Bearer token for HTTP transport auth |
| `PORT` | PaaS convention (Cloud Run / Heroku). When set, `serve` defaults to HTTP transport and binds to `:$PORT`. `RFC_MCP_*` and CLI flags take precedence |
| `RFC_MCP_CACHE_TTL_HOURS` | Cache TTL in hours for `rfc-index.xml` / `errata.json` (default: 24). Per-RFC text bodies are cached indefinitely, independent of this setting |
| `RFC_MCP_MAX_TXT_SIZE_MB` | Max response size for any single HTTP GET (default: 20 MB); also caps `ingest/drafts` fetches |
| `RFC_MCP_DISABLE_DRAFTS` | Set to `1` to skip registering the Internet-Draft tools (`search_drafts`, `get_draft_metadata`, `get_draft_toc`, `get_draft_section`) for offline/no-egress deployments |
| `XDG_CACHE_HOME` | Override cache directory (follows XDG Base Directory spec) |
