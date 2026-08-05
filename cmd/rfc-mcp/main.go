package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/ingest/drafts"
	"github.com/higebu/rfc-mcp/ingest/pipeline"
	"github.com/higebu/rfc-mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

const usage = `Usage: rfc-mcp <command> [options]
Commands: serve, build, download, import, import-dir, update, completion`

// defaultDBPath is the default SQLite database path shared by every
// subcommand that takes a -db flag (serve, build, update, import, import-dir).
const defaultDBPath = "data/rfc.db"

// newHTTPServer wraps handler in an http.Server with explicit limits so a
// client cannot hold a connection open indefinitely while trickling request
// headers (slowloris). ReadTimeout/WriteTimeout are deliberately left unset:
// MCP streamable HTTP responses can be long-lived streams, and those timeouts
// would cut them off.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(auth, expected) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	pipeline.Version = version
	drafts.Version = version

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "serve":
		cmdServe(args)
	case "completion":
		cmdCompletion(args)
	case "build", "pipeline":
		cmdBuild(args)
	case "download":
		cmdDownload(args)
	case "import", "convert":
		cmdImport(args)
	case "import-dir", "convert-dir":
		cmdImportDir(args)
	case "update":
		cmdUpdate(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
}

// defaultRawDir returns ~/.cache/rfc-mcp/raw (or $XDG_CACHE_HOME/rfc-mcp/raw),
// the default location build/download cache downloaded RFC .txt files.
func defaultRawDir() string {
	dir, err := pipeline.CacheDir()
	if err != nil {
		return filepath.Join("rfc-mcp-cache", "raw")
	}
	return filepath.Join(dir, "raw")
}

func cmdBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath, "Output SQLite database path")
	workers := fs.Int("workers", pipeline.DefaultConcurrency(), "Number of parallel workers")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	from := fs.Int("from", 0, "Only process RFCs numbered >= this (0 = no lower bound)")
	to := fs.Int("to", 0, "Only process RFCs numbered <= this (0 = no upper bound)")
	rawDir := fs.String("raw-dir", defaultRawDir(), "Directory to cache downloaded RFC .txt files")
	baseURL := fs.String("base-url", "", "Override the RFC Editor root URL (default https://www.rfc-editor.org)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}
	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()
	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := &pipeline.Pipeline{
		DB:      d,
		Workers: *workers,
		Timeout: *timeout,
		RawDir:  *rawDir,
		BaseURL: *baseURL,
	}
	if err := p.Run(ctx, *from, *to); err != nil {
		log.Fatalf("Build failed: %v", err)
	}
}

// removeWorkingCopy removes the update working-copy database file at path
// along with its WAL/SHM sidecars. The working copy runs in WAL mode (see
// db.OpenReadWrite), so any failure between opening it and the final
// wal_checkpoint leaves "-wal"/"-shm" files behind unless they're removed
// alongside the main file.
func removeWorkingCopy(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

// cmdUpdate refreshes an existing database in place: it works on a VACUUM
// INTO'd copy so the live database (which serve may be reading concurrently)
// is never mutated mid-update, then atomically renames the copy over the
// original once it succeeds. See pipeline.Pipeline.RunUpdate for what
// "refresh" means (live-fetched metadata/errata, new RFC bodies only).
func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath, "SQLite database path")
	workers := fs.Int("workers", pipeline.DefaultConcurrency(), "Number of parallel workers")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	rawDir := fs.String("raw-dir", defaultRawDir(), "Directory to cache downloaded RFC .txt files")
	baseURL := fs.String("base-url", "", "Override the RFC Editor root URL (default https://www.rfc-editor.org)")
	_ = fs.Parse(args)

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}

	newPath := *dbPath + ".new"
	removeWorkingCopy(newPath) // remove stale copy from any previous failed run

	src, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	fmt.Println("Creating working copy of database...")
	if err := src.VacuumInto(newPath); err != nil {
		_ = src.Close()
		log.Fatalf("Failed to create working copy: %v", err)
	}
	_ = src.Close()

	d, err := db.OpenReadWrite(newPath)
	if err != nil {
		removeWorkingCopy(newPath)
		log.Fatalf("Failed to open working copy: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		_ = d.Close()
		removeWorkingCopy(newPath)
		log.Fatalf("Failed to init schema: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := &pipeline.Pipeline{
		DB:      d,
		Workers: *workers,
		Timeout: *timeout,
		RawDir:  *rawDir,
		BaseURL: *baseURL,
	}
	if err := p.RunUpdate(ctx); err != nil {
		_ = d.Close()
		removeWorkingCopy(newPath)
		log.Fatalf("Update failed: %v", err)
	}

	// Checkpoint WAL into the main file so the renamed DB is self-contained.
	// Only remove the -wal/-shm sidecars once the checkpoint and close are
	// confirmed; on failure abort before the rename so the old DB stays live.
	if err := d.WALCheckpointTruncate(); err != nil {
		_ = d.Close()
		removeWorkingCopy(newPath)
		log.Fatalf("Failed to checkpoint working copy: %v", err)
	}
	if err := d.Close(); err != nil {
		removeWorkingCopy(newPath)
		log.Fatalf("Failed to close working copy: %v", err)
	}
	_ = os.Remove(newPath + "-wal")
	_ = os.Remove(newPath + "-shm")

	// Same-directory rename is atomic: the served DB path always resolves to
	// either the fully-old or fully-new file, never a partial write.
	if err := os.Rename(newPath, *dbPath); err != nil {
		removeWorkingCopy(newPath)
		log.Fatalf("Failed to replace database: %v", err)
	}
	fmt.Println("Database updated successfully.")
}

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	rawDir := fs.String("raw-dir", defaultRawDir(), "Directory to save downloaded RFC .txt files")
	workers := fs.Int("workers", pipeline.DefaultConcurrency(), "Number of parallel downloads")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	from := fs.Int("from", 0, "Only download RFCs numbered >= this (0 = no lower bound)")
	to := fs.Int("to", 0, "Only download RFCs numbered <= this (0 = no upper bound)")
	baseURL := fs.String("base-url", "", "Override the RFC Editor root URL (default https://www.rfc-editor.org)")
	_ = fs.Parse(args)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := &pipeline.Pipeline{
		Workers: *workers,
		Timeout: *timeout,
		RawDir:  *rawDir,
		BaseURL: *baseURL,
	}
	if err := p.Download(ctx, *from, *to); err != nil {
		log.Fatalf("Download failed: %v", err)
	}
}

func cmdImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath, "Output SQLite database path")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: rfc-mcp import [options] <rfc-txt-file>")
		os.Exit(1)
	}
	path := fs.Arg(0)

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}
	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()
	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	p := &pipeline.Pipeline{DB: d}
	fmt.Printf("Importing %s...\n", path)
	if err := p.ImportFile(path); err != nil {
		log.Fatalf("Import failed: %v", err)
	}
	fmt.Printf("Written to %s\n", *dbPath)
}

func cmdImportDir(args []string) {
	fs := flag.NewFlagSet("import-dir", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath, "Output SQLite database path")
	workers := fs.Int("workers", pipeline.DefaultConcurrency(), "Number of parallel parse workers")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: rfc-mcp import-dir [options] <directory>")
		os.Exit(1)
	}
	dir := fs.Arg(0)

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("Failed to create database directory: %v", err)
	}
	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()
	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	p := &pipeline.Pipeline{DB: d}
	if err := p.ImportDir(dir, *workers); err != nil {
		log.Fatalf("Import dir failed: %v", err)
	}
}

// draftsEnabled reports whether the Internet-Draft tools (search_drafts,
// get_draft_metadata, get_draft_toc, get_draft_section, get_ipr) should be
// registered. Set RFC_MCP_DISABLE_DRAFTS=1 to skip them entirely for
// offline/no-egress deployments -- unlike the RFC tools (SQLite only),
// draft tools perform live network requests to the IETF Datatracker/
// archive at call time.
func draftsEnabled() bool {
	return os.Getenv("RFC_MCP_DISABLE_DRAFTS") != "1"
}

// buildInstructions composes the MCP server's Instructions string: static
// tool-usage guidance plus a data-freshness line (see freshnessLine)
// reporting how current the baked SQLite snapshot is. drafts controls
// whether the Internet-Draft tool sentence is included, since those tools
// are only registered when draftsEnabled() is true (see cmdServe).
func buildInstructions(d *db.DB, drafts bool) string {
	instructions := "IETF RFC specification server. Use list_rfcs to find RFCs, get_metadata for status/obsoletes/updates/errata summary, get_errata for full errata detail (original/corrected text, notes), get_toc to browse structure, get_section to read content, get_document to read an entire RFC as one paginated document, search for full-text search across all RFCs, and get_references to explore cross-references between RFCs."

	toolHint := "use search for published RFCs and search_drafts for pre-publication Internet-Drafts"
	if !drafts {
		toolHint = "use search for full-text search across published RFCs"
	}
	instructions += " For any question about IETF protocols or their extensions (BGP, SRv6, SAFIs, transport, security, etc.), consult this server before searching the web: " + toolHint + ". If a multi-word search returns nothing, retry with the single most distinctive keyword (e.g. a protocol acronym)."

	if freshness := freshnessLine(d, drafts); freshness != "" {
		instructions += " " + freshness
	}

	if drafts {
		instructions += " For pre-publication Internet-Drafts, use search_drafts, get_draft_metadata, get_draft_toc, and get_draft_section -- unlike the RFC tools (SQLite only, fully offline), these fetch from the IETF Datatracker/archive over the network on every call and are unavailable when RFC_MCP_DISABLE_DRAFTS=1. Use get_ipr for patent disclosures against an RFC or draft."
	}

	return instructions
}

// freshnessLine reports how current the baked SQLite snapshot is, so an
// LLM client can judge whether a recently published RFC might be missing:
// the build timestamp (db.DB.GetMeta("built_at")), the highest RFC number,
// and the latest publication date in the database. It degrades to omitting
// the build timestamp for a database built before the meta table existed,
// and returns "" if the database has no issued RFCs at all (nothing to
// report) -- either way, serve startup never fails because of this.
func freshnessLine(d *db.DB, drafts bool) string {
	result, err := d.ListRFCs("", "", "", "", -1, 0)
	if err != nil || len(result.RFCs) == 0 {
		return ""
	}

	maxNumber := 0
	maxDate := ""
	for _, r := range result.RFCs {
		if r.Number > maxNumber {
			maxNumber = r.Number
		}
		if r.Date > maxDate {
			maxDate = r.Date
		}
	}
	rangePart := fmt.Sprintf("covers RFC 1-%d", maxNumber)
	if maxDate != "" {
		rangePart += fmt.Sprintf(" (latest dated %s)", maxDate)
	}

	var built string
	if builtAt, ok := d.GetMeta("built_at"); ok {
		if t, err := time.Parse(time.RFC3339, builtAt); err == nil {
			built = "built " + t.Format("2006-01-02") + "; "
		}
	}

	line := fmt.Sprintf("Data freshness: RFC database %s%s. RFCs published after the build date are absent -- rebuild with 'rfc-mcp update'.", built, rangePart)
	if drafts {
		line += " Draft tools query the IETF Datatracker live and are always current."
	}
	return line
}

// newServer builds the MCP server with all tools registered. drafts controls
// whether the network-backed Internet-Draft tools are included (see
// draftsEnabled).
func newServer(d *db.DB, drafts bool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "rfc-mcp",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: buildInstructions(d, drafts),
	})

	mcp.AddTool(s, tools.ListRFCsTool, tools.HandleListRFCs(d))
	mcp.AddTool(s, tools.GetMetadataTool, tools.HandleGetMetadata(d))
	mcp.AddTool(s, tools.GetErrataTool, tools.HandleGetErrata(d))
	mcp.AddTool(s, tools.GetTOCTool, tools.HandleGetTOC(d))
	mcp.AddTool(s, tools.GetSectionTool, tools.HandleGetSection(d))
	mcp.AddTool(s, tools.GetDocumentTool, tools.HandleGetDocument(d))
	mcp.AddTool(s, tools.SearchTool, tools.HandleSearch(d))
	mcp.AddTool(s, tools.GetReferencesTool, tools.HandleGetReferences(d))

	if drafts {
		draftClient := &http.Client{Timeout: 30 * time.Second}
		mcp.AddTool(s, tools.SearchDraftsTool, tools.HandleSearchDrafts(draftClient))
		mcp.AddTool(s, tools.GetDraftMetadataTool, tools.HandleGetDraftMetadata(draftClient))
		mcp.AddTool(s, tools.GetDraftTOCTool, tools.HandleGetDraftTOC(draftClient))
		mcp.AddTool(s, tools.GetDraftSectionTool, tools.HandleGetDraftSection(draftClient))
		mcp.AddTool(s, tools.GetIPRTool, tools.HandleGetIPR(draftClient))
	}
	return s
}

func cmdServe(args []string) {
	defaultTransport := "stdio"
	if v := os.Getenv("RFC_MCP_TRANSPORT"); v != "" {
		defaultTransport = v
	} else if os.Getenv("PORT") != "" {
		// PaaS like Cloud Run / Heroku inject PORT and expect an HTTP server.
		defaultTransport = "http"
	}

	defaultAddr := ":8080"
	if v := os.Getenv("RFC_MCP_ADDR"); v != "" {
		defaultAddr = v
	} else if p := os.Getenv("PORT"); p != "" {
		defaultAddr = ":" + p
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", defaultDBPath, "Path to SQLite database")
	transport := fs.String("transport", defaultTransport, "Transport type: stdio or http (env: RFC_MCP_TRANSPORT, or PORT to force http)")
	fs.StringVar(transport, "t", defaultTransport, "Shorthand for -transport")
	addr := fs.String("addr", defaultAddr, "HTTP listen address (env: RFC_MCP_ADDR, or PORT)")
	bearerToken := fs.String("bearer-token", "", "Bearer token for HTTP auth (env: RFC_MCP_BEARER_TOKEN)")
	_ = fs.Parse(args)

	// Environment variable takes precedence if flag is not set
	if *bearerToken == "" {
		*bearerToken = os.Getenv("RFC_MCP_BEARER_TOKEN")
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	s := newServer(d, draftsEnabled())

	switch *transport {
	case "stdio":
		log.Println("Starting rfc-mcp server on stdio...")
		if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "http":
		// Stateless mode is required to serve MCP protocol 2026-07-28; the
		// server keeps no per-session state, so nothing is lost and no
		// session affinity is needed behind load balancers.
		mcpHandler := mcp.NewStreamableHTTPHandler(
			func(r *http.Request) *mcp.Server { return s },
			&mcp.StreamableHTTPOptions{Stateless: true},
		)
		var mcpH http.Handler = mcpHandler
		if *bearerToken != "" {
			mcpH = bearerAuthMiddleware(*bearerToken, mcpHandler)
		} else {
			log.Println("WARNING: HTTP transport running without authentication. Set -bearer-token or RFC_MCP_BEARER_TOKEN to secure the server.")
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/health", healthHandler)
		mux.Handle("/", mcpH)
		log.Printf("Starting rfc-mcp server on %s (HTTP)...", *addr)
		srv := newHTTPServer(*addr, mux)
		if err := srv.ListenAndServe(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	default:
		log.Fatalf("Unknown transport: %s", *transport)
	}
}

func cmdCompletion(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: rfc-mcp completion <bash|zsh|fish>")
		os.Exit(1)
	}
	switch args[0] {
	case "bash":
		fmt.Print(`# bash completion for rfc-mcp
_rfc_mcp() {
    local commands="serve build download import import-dir update completion"
    local cur="${COMP_WORDS[COMP_CWORD]}"
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
    fi
}
complete -F _rfc_mcp rfc-mcp
`)
	case "zsh":
		fmt.Print(`#compdef rfc-mcp

_rfc_mcp() {
    local -a commands
    commands=(
        'serve:Start the MCP server'
        'build:Download and import RFCs into database'
        'download:Download RFC plain-text files'
        'import:Import a single RFC text file into database'
        'import-dir:Import a directory of RFC text files into database'
        'update:Update database with newly issued RFCs and refreshed errata'
        'completion:Generate shell completion scripts'
    )
    _describe 'rfc-mcp command' commands
}

_rfc_mcp
`)
	case "fish":
		fmt.Print(`# fish completion for rfc-mcp
complete -c rfc-mcp -f
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a serve      -d 'Start the MCP server'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a build      -d 'Download and import RFCs into database'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a download   -d 'Download RFC plain-text files'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a import     -d 'Import a single RFC text file into database'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a import-dir -d 'Import a directory of RFC text files into database'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a update     -d 'Update database with newly issued RFCs and refreshed errata'
complete -c rfc-mcp -n "not __fish_seen_subcommand_from serve build download import import-dir update completion" -a completion -d 'Generate shell completion scripts'
`)
	default:
		fmt.Fprintf(os.Stderr, "Unknown shell: %s (supported: bash, zsh, fish)\n", args[0])
		os.Exit(1)
	}
}
