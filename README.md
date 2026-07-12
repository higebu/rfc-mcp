# rfc-mcp

[![Go Reference](https://pkg.go.dev/badge/github.com/higebu/rfc-mcp.svg)](https://pkg.go.dev/github.com/higebu/rfc-mcp)
[![CI](https://github.com/higebu/rfc-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/higebu/rfc-mcp/actions/workflows/ci.yml)
[![codecov](https://codecov.io/github/higebu/rfc-mcp/graph/badge.svg)](https://codecov.io/github/higebu/rfc-mcp)
![GitHub Release](https://img.shields.io/github/v/release/higebu/rfc-mcp)

An MCP (Model Context Protocol) server that makes IETF RFCs accessible to LLMs.

## Background

RFCs are the primary reference for Internet protocols, but they are difficult for LLMs to work with effectively:

- **Too many documents** - Nearly 9,800 RFCs have been published since 1969, spanning every era of Internet protocol design.
- **Individual documents can be huge** - Foundational specs like TCP (RFC 9293) or BGP-4 (RFC 4271) run to a hundred pages or more.
- **Inconsistent formatting across five decades** - Plain-text RFCs range from modern unpaginated documents to 1980s page-and-form-feed layouts to free-form 1970s documents with no section numbering at all.
- **Heavy cross-referencing** - RFCs constantly reference, obsolete, and update each other (e.g. RFC 9293 obsoletes RFC 793 and six others, and updates three more); reading one document in isolation gives an incomplete picture.
- **Status and errata complexity** - The same protocol can be described across an original RFC, several updates, and a list of errata reports, and knowing which parts are still current matters.

This tool addresses these challenges by parsing the plain-text RFC bodies published at [rfc-editor.org](https://www.rfc-editor.org/), structuring the content by section, and storing everything in a SQLite database with full-text search (FTS5). An MCP server then exposes tools for browsing, searching, and following cross-references — letting an LLM navigate RFCs the way a protocol engineer would.

### Why not RAG?

A RAG (Retrieval-Augmented Generation) approach — chunking documents, generating embeddings, and performing vector similarity search — is a common solution for document Q&A. However, RFCs are structured technical documents where that approach has significant drawbacks:

- **Loss of structure** - RAG splits documents into flat chunks, discarding the section hierarchy needed to navigate a spec (e.g. jumping straight to "Section 3.10.7.4" of RFC 9293).
- **No relationship traversal** - Vector search cannot follow "obsoletes", "updates", or cross-reference relationships between RFCs.
- **Noisy retrieval** - Similarity search may return loosely related chunks instead of the exact section needed.
- **Additional cost** - Embedding generation and vector database hosting add infrastructure and API costs.

This tool takes a structure-aware approach instead: it preserves each RFC's section hierarchy (including legacy pagination quirks), enables precise section-level retrieval by number or by slug, supports full-text search with FTS5 syntax, and resolves cross-references between RFCs. All data is stored in a single SQLite file with no external dependencies.

## Getting Started

### Build a self-contained Docker image

The `Dockerfile` is multi-stage and builds the database directly, producing a
self-contained image with the SQLite database baked in. No LibreOffice or
other heavy dependency is needed — RFCs are parsed straight from plain text.

```bash
# Build an image with the full RFC corpus baked in (default, ~8 min, ~865 MB)
docker build -t rfc-mcp:latest .

# ...or restrict the database to a numeric RFC range (fast smoke-test image)
docker build --build-arg FROM_RFC=9290 --build-arg TO_RFC=9295 -t rfc-mcp:smoke .

# stdio transport (Claude Code / IDE integration)
docker run --rm -i rfc-mcp:latest

# HTTP transport
docker run --rm -p 8080:8080 rfc-mcp:latest serve --db /rfc.db --transport http --addr :8080
```

`FROM_RFC`/`TO_RFC` default to empty, which bakes in the full corpus. Set
either (or both) to build a database restricted to a numeric RFC range.

### Deploy to Cloud Run

To run on Cloud Run, see `cloudbuild.yaml` (build + push + deploy) and
`service.yaml` (Cloud Run service spec).

---

### 1. Install

```bash
go install github.com/higebu/rfc-mcp/cmd/rfc-mcp@latest
```

Requires Go 1.26+. No CGO, no external runtime dependencies — RFC bodies are parsed from plain text only.

### 2. Build the database

Download and import RFCs into the database. Downloaded `.txt` bodies are
cached (see [Data sources & update cadence](#data-sources--update-cadence)
below), so re-running `build` after an interruption resumes cheaply.

```bash
# Download and import the full RFC corpus (~8 min with 16 workers, ~865 MB)
rfc-mcp build --db data/rfc.db

# ...or restrict to a numeric range, e.g. for a quick local test
rfc-mcp build --db data/rfc.db --from 9290 --to 9295
```

This fetches `rfc-index.xml` and `errata.json`, then each RFC's plain-text
body individually (rfc-editor.org has no bulk tarball), parses it into
sections, and inserts everything into the SQLite database.

### 3. Register with your MCP client

#### Claude Code

```bash
claude mcp add --scope user rfc -- rfc-mcp serve --db /path/to/data/rfc.db
```

#### VS Code / GitHub Copilot

```bash
code --add-mcp '{"name":"rfc","command":"rfc-mcp","args":["serve","--db","/path/to/data/rfc.db"]}'
```

#### Claude Desktop

Add to your configuration file (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, `%APPDATA%\Claude\claude_desktop_config.json` on Windows):

```json
{
  "mcpServers": {
    "rfc": {
      "command": "rfc-mcp",
      "args": ["serve", "--db", "/path/to/data/rfc.db"]
    }
  }
}
```

#### Streamable HTTP (remote deployment)

Start the server with HTTP transport:

```bash
rfc-mcp serve --db data/rfc.db --transport http --addr :8080
```

Optionally enable Bearer token authentication:

```bash
export RFC_MCP_BEARER_TOKEN=$(openssl rand -hex 32)
rfc-mcp serve --db data/rfc.db --transport http --addr :8080
```

Then configure your client to connect via HTTP:

```json
{
  "mcpServers": {
    "rfc": {
      "url": "http://your-server:8080",
      "headers": {
        "Authorization": "Bearer YOUR_SECRET_TOKEN"
      }
    }
  }
}
```

`GET /health` returns `200 OK` without authentication, for platform health
checks (Cloud Run, Kubernetes liveness/readiness probes, etc.).

For container platforms like Cloud Run or Heroku that inject a `PORT`
environment variable, the server automatically switches to HTTP transport
and binds to `:$PORT`. Explicit flags or `RFC_MCP_TRANSPORT` /
`RFC_MCP_ADDR` always take precedence.

See [`examples/systemd/`](examples/systemd/) for production deployment with systemd.

## MCP Tools

### Browsing RFCs

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_rfcs` | List RFCs, optionally filtered | `query` (title substring), `stream`, `status`, `wg`, `limit`, `offset` |
| `get_metadata` | Title, status, dates, obsoletes/updates, errata | `rfc` (required) |
| `get_toc` | Table of contents of an RFC | `rfc` (required) |
| `get_section` | Section content, each section prefixed with its heading line (paginated). A title-only section fetched without `include_subsections` returns a summary of its subsections instead of empty text | `rfc`, `section_number` (required), `include_subsections`, `offset`, `max_lines`, `max_chars` |
| `get_document` | Full text of an RFC as one document (paginated) | `rfc` (required), `offset`, `max_lines`, `max_chars` |

`get_metadata(rfc: 4271)` returns:

```json
{
  "rfc": 4271,
  "title": "A Border Gateway Protocol 4 (BGP-4)",
  "status": "Draft Standard",
  "stream": "IETF",
  "date": "2006-01",
  "page_count": 104,
  "wg": "idr",
  "area": "rtg",
  "authors": ["Y. Rekhter", "T. Li", "S. Hares"],
  "keywords": ["BGP-4", "routing"],
  "abstract": "This document discusses the Border Gateway Protocol (BGP), ...",
  "draft": "draft-ietf-idr-bgp4-26",
  "doi": "10.17487/RFC4271",
  "errata_url": "https://www.rfc-editor.org/errata/rfc4271",
  "obsoletes": [1771],
  "updated_by": [4724, 6286, 6608, 6793, 7606, 7607, 7705, 8212, 8654, 9072, 9687, 9774],
  "errata": [
    { "id": 150, "status": "Verified", "type": "Editorial", "section": "9.1.1" },
    { "id": 1332, "status": "Rejected", "type": "Technical", "section": "4.5" }
  ]
}
```

`status` is title-cased for readability even though `rfc-index.xml` stores it
upper-case (`DRAFT STANDARD`); `list_rfcs` returns the raw upper-case form.
Errata are a compact summary (id/status/type/section only) — follow
`errata_url` for the full original/corrected text of a specific erratum.

### Searching

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `search` | Full-text search across all RFCs | `query` (required), `rfc`, `rfcs`, `limit` (default 10) |

The `search` tool supports [SQLite FTS5](https://www.sqlite.org/fts5.html) query syntax:

- Phrase search: `"three way handshake"`
- Boolean operators: `AMF AND UE`, `retransmission OR retransmit`, `NOT deprecated`
- Prefix matching: `retransmi*`
- Column filter: `title:security`, `content:handshake`
- Proximity: `NEAR(SYN ACK, 5)`
- Hyphenated terms (e.g. `three-way-handshake`) are auto-quoted to avoid FTS5 syntax errors

Example — `search(query: "three way handshake", rfc: 9293, limit: 3)`:

```json
[
  {
    "rfc": 9293,
    "number": "3.5",
    "title": "Establishing a Connection",
    "snippet": "The \"<mark>three</mark>-<mark>way</mark> <mark>handshake</mark>\" is the procedure used to establish a connection. ..."
  }
]
```

### Cross-references

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `get_references` | Get cross-references between RFCs | `rfc` (required), `section_number`, `direction` (`outgoing` default, or `incoming`), `include_subsections` |

`outgoing` requires `section_number` and returns the RFCs referenced from
that section; `incoming` only requires `rfc` and returns every section (in
any RFC) that references it. Example —
`get_references(rfc: 9293, section_number: "3.10.7.4", direction: "outgoing")`:

```json
[
  {
    "source_rfc": 9293,
    "source_section": "3.10.7.4",
    "target_rfc": 793,
    "context": "...the original behavior described in RFC 793 follows in this paragraph. ..."
  },
  {
    "source_rfc": 9293,
    "source_section": "3.10.7.4",
    "target_rfc": 5961,
    "target_section": "3",
    "target_title": "Blind Reset Attack Using the RST Bit",
    "context": "...RFC 5961 [9], Section 3 describes a potential blind reset..."
  }
]
```

`target_section`/`target_title` are only present when the reference could be
resolved to a specific section (e.g. via a numeric bracket citation like
`[9]` resolved through the References section); a bare `RFC 793` mention
without a section number omits them.

## Command Reference

### `serve`

Start the MCP server.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Path to SQLite database | `rfc.db` |
| `--transport` (`-t`) | Transport type: `stdio` or `http` (env: `RFC_MCP_TRANSPORT`; defaults to `http` when `PORT` is set) | `stdio` |
| `--addr` | HTTP listen address (env: `RFC_MCP_ADDR`, or `PORT` interpreted as `:$PORT`) | `:8080` |
| `--bearer-token` | Bearer token for HTTP auth (env: `RFC_MCP_BEARER_TOKEN`) | |

### `build`

Download and import RFCs into the database (recommended for initial setup).

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `data/rfc.db` |
| `--workers` | Number of parallel workers | NumCPU |
| `--timeout` | HTTP timeout | `30s` |
| `--from` | Only process RFCs numbered >= this (0 = no lower bound) | `0` |
| `--to` | Only process RFCs numbered <= this (0 = no upper bound) | `0` |
| `--raw-dir` | Directory to cache downloaded RFC `.txt` files | `$XDG_CACHE_HOME/rfc-mcp/raw` |
| `--base-url` | Override the RFC Editor root URL | `https://www.rfc-editor.org` |

### `download`

Download RFC plain-text bodies without importing them.

| Flag | Description | Default |
|------|-------------|---------|
| `--raw-dir` | Directory to save downloaded RFC `.txt` files | `$XDG_CACHE_HOME/rfc-mcp/raw` |
| `--workers` | Number of parallel downloads | NumCPU |
| `--timeout` | HTTP timeout | `30s` |
| `--from` | Only download RFCs numbered >= this | `0` |
| `--to` | Only download RFCs numbered <= this | `0` |
| `--base-url` | Override the RFC Editor root URL | `https://www.rfc-editor.org` |

### `import`

Import a single RFC `.txt` file into the database.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `data/rfc.db` |

Usage: `rfc-mcp import --db data/rfc.db path/to/rfcNNNN.txt`

### `import-dir`

Import all RFC `.txt` files in a directory into the database.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `data/rfc.db` |
| `--workers` | Number of parallel parse workers | NumCPU |

Usage: `rfc-mcp import-dir --db data/rfc.db ./raw`

### `update`

Refresh an existing database in place: fetch RFCs newly issued since the
last build, and refresh metadata and errata for the entire corpus (both are
small, live-fetched documents, so a wholesale refresh is cheap). Already
cached RFC bodies are never re-fetched, since bodies are immutable once
published. The refresh happens on a `VACUUM INTO`'d copy of the database and
is only swapped into place atomically once it succeeds, so a database
`serve` is reading concurrently is never left partially written.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | SQLite database path | `data/rfc.db` |
| `--workers` | Number of parallel workers | NumCPU |
| `--timeout` | HTTP timeout | `30s` |
| `--raw-dir` | Directory to cache downloaded RFC `.txt` files | `$XDG_CACHE_HOME/rfc-mcp/raw` |
| `--base-url` | Override the RFC Editor root URL | `https://www.rfc-editor.org` |

### `completion`

Generate shell completion scripts: `rfc-mcp completion <bash|zsh|fish>`.

## Data sources & update cadence

- **`rfc-index.xml`** (~13.6 MB) - metadata for every RFC: 9,794 issued
  entries plus 188 allocated-but-never-issued entries. Cached for 24 hours
  by default (`RFC_MCP_CACHE_TTL_HOURS`).
- **`errata.json`** (~11.5 MB) - all published errata reports (7,961 as of
  this writing). Same 24-hour cache.
- **Per-RFC plain text** (`https://www.rfc-editor.org/rfc/rfcN.txt`) -
  fetched one file at a time; rfc-editor.org publishes no bulk tarball and
  rejects `HEAD` requests, so every fetch is a `GET`. RFC bodies never
  change once published, so the local cache under `--raw-dir` is valid
  forever and is never re-fetched or expired.
- **`update`** keeps a database current without a full rebuild: it fetches
  only RFC numbers not already in the database, and refreshes metadata and
  errata for the whole corpus unconditionally (see the `update` command
  above for the atomic-swap mechanics).

A full `build` of the entire corpus (measured 2026-07-11, 16 workers) takes
about 8 minutes and produces an ~865 MB SQLite database: 9,982 RFC rows
(9,794 issued + 188 not-issued), 321,372 sections, 284,869 extracted
cross-references, and 7,961 errata records.

## Limitations

- **7 RFCs have no plain-text body at all** and are metadata-only (title,
  status, dates, etc. from `rfc-index.xml`, but no sections, no full-text
  search, no `get_section`/`get_toc` content): RFC 8, 9, 51, 418, 500, 530,
  and 598. These are 1969–1973 documents that were only ever distributed as
  scanned images or on paper; rfc-editor.org has no `.txt` for them. The
  pipeline detects this from the `<format>` list in each entry's
  `rfc-index.xml` record and skips fetching their bodies rather than
  retrying a guaranteed 404 on every `build`/`update` run (reported as
  `TEXT_UNAVAILABLE` in the completion summary).
- **~684 RFCs (~7% of the corpus, overwhelmingly pre-1000)** degrade to a
  single whole-body section numbered `body` rather than a proper section
  breakdown. This is the parser's documented Tier-3 fallback for documents
  whose layout doesn't match either the column-0 heading pattern or a
  matchable in-document table of contents (free-form 1970s memos, unusual
  typesetting, etc.). Full text is still searchable and retrievable via
  `search`, `get_section(rfc, "body")`, or `get_document`; it's just not
  addressable by a fine-grained section number.
- **Plain-text parsing only** - RFCs are parsed from the `.txt` rendition
  published by rfc-editor.org, not the XML source. Section content is
  stored verbatim (no dedent or reflow), preserving ABNF and packet-diagram
  alignment, but any leftover pagination artifact the cleanup heuristics
  miss will appear as-is.
- **Section numbering conventions**: dotted numeric (`4.1`), lettered
  appendices (`A.2`), or a slug for unnumbered headings — lowercase the
  heading and replace spaces/apostrophes with hyphens (`abstract`,
  `security-considerations`, `iana-considerations`, `acknowledgments`,
  `authors-address`, etc.). A synthetic `header` slug covers the RFC's
  title-block content (document series header, title, author list, date)
  that precedes the first real heading, and `body` covers the Tier-3
  whole-body fallback described above.

## License

MIT
