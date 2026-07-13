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
	"github.com/higebu/rfc-mcp/ingest/pipeline"
	"github.com/higebu/rfc-mcp/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

const usage = `Usage: rfc-mcp <command> [options]
Commands: serve, build, download, import, import-dir, update, completion`

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
	dbPath := fs.String("db", "data/rfc.db", "Output SQLite database path")
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
	dbPath := fs.String("db", "data/rfc.db", "SQLite database path")
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
	_ = d.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	_ = d.Close()
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
	dbPath := fs.String("db", "data/rfc.db", "Output SQLite database path")
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
	dbPath := fs.String("db", "data/rfc.db", "Output SQLite database path")
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
	dbPath := fs.String("db", "rfc.db", "Path to SQLite database")
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

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "rfc-mcp",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "IETF RFC specification server. Use list_rfcs to find RFCs, get_metadata for status/obsoletes/updates/errata summary, get_errata for full errata detail (original/corrected text, notes), get_toc to browse structure, get_section to read content, get_document to read an entire RFC as one paginated document, search for full-text search across all RFCs, and get_references to explore cross-references between RFCs.",
	})

	mcp.AddTool(s, tools.ListRFCsTool, tools.HandleListRFCs(d))
	mcp.AddTool(s, tools.GetMetadataTool, tools.HandleGetMetadata(d))
	mcp.AddTool(s, tools.GetErrataTool, tools.HandleGetErrata(d))
	mcp.AddTool(s, tools.GetTOCTool, tools.HandleGetTOC(d))
	mcp.AddTool(s, tools.GetSectionTool, tools.HandleGetSection(d))
	mcp.AddTool(s, tools.GetDocumentTool, tools.HandleGetDocument(d))
	mcp.AddTool(s, tools.SearchTool, tools.HandleSearch(d))
	mcp.AddTool(s, tools.GetReferencesTool, tools.HandleGetReferences(d))

	switch *transport {
	case "stdio":
		log.Println("Starting rfc-mcp server on stdio...")
		if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "http":
		mcpHandler := mcp.NewStreamableHTTPHandler(
			func(r *http.Request) *mcp.Server { return s },
			nil,
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
		if err := http.ListenAndServe(*addr, mux); err != nil {
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
