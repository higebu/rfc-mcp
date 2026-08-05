package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/internal/testutil"
)

// TestDraftsEnabled covers the RFC_MCP_DISABLE_DRAFTS knob cmdServe reads
// before registering search_drafts/get_draft_metadata/get_draft_toc/
// get_draft_section: unset or any value other than exactly "1" leaves the
// draft tools registered, only "1" disables them.
func TestDraftsEnabled(t *testing.T) {
	tests := []struct {
		name string
		val  string
		set  bool
		want bool
	}{
		{"unset defaults to enabled", "", false, true},
		{"disabled", "1", true, false},
		{"any other value stays enabled", "true", true, true},
		{"empty string stays enabled", "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("RFC_MCP_DISABLE_DRAFTS", tt.val)
			}
			if got := draftsEnabled(); got != tt.want {
				t.Errorf("draftsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNewHTTPServer asserts the serve HTTP transport uses an http.Server
// with slowloris protections (header read deadline, idle timeout, bounded
// header size) while leaving ReadTimeout/WriteTimeout unset so long-lived
// MCP streaming responses are never cut off.
func TestNewHTTPServer(t *testing.T) {
	mux := http.NewServeMux()
	srv := newHTTPServer(":9999", mux)

	if srv.Addr != ":9999" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":9999")
	}
	if srv.Handler == nil {
		t.Error("Handler is nil")
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0 (slowloris protection)", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes <= 0 {
		t.Errorf("MaxHeaderBytes = %d, want > 0", srv.MaxHeaderBytes)
	}
	if srv.ReadTimeout != 0 {
		t.Errorf("ReadTimeout = %v, want 0 (would cut off long-lived MCP streams)", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (would cut off long-lived MCP streams)", srv.WriteTimeout)
	}
}

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	healthHandler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Errorf("expected body \"ok\", got %q", body)
	}
}

func TestBearerAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := bearerAuthMiddleware("secret-token", inner)

	t.Run("valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Basic secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// TestBuildInstructions covers the data-freshness line (see freshnessLine)
// composed into the server's Instructions: present with a build timestamp,
// degraded (timestamp omitted) for a database predating the meta table, and
// the Internet-Draft sentences gated on the drafts flag.
func TestBuildInstructions(t *testing.T) {
	t.Run("with built_at, drafts enabled", func(t *testing.T) {
		d := testutil.SetupTestDB(t)
		if err := d.SetMeta("built_at", "2026-07-12T00:00:00Z"); err != nil {
			t.Fatalf("SetMeta: %v", err)
		}

		got := buildInstructions(d, true)
		for _, want := range []string{
			"consult this server before searching the web",
			"use search for published RFCs and search_drafts for pre-publication Internet-Drafts",
			"retry with the single most distinctive keyword",
			"Data freshness:",
			"built 2026-07-12;",
			"covers RFC 1-9293",
			"rebuild with 'rfc-mcp update'",
			"search_drafts, get_draft_metadata, get_draft_toc, and get_draft_section",
			"Draft tools query the IETF Datatracker live and are always current.",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("buildInstructions() missing %q, got:\n%s", want, got)
			}
		}
	})

	t.Run("without meta (old db), drafts enabled", func(t *testing.T) {
		d := testutil.SetupTestDB(t)

		got := buildInstructions(d, true)
		if strings.Contains(got, "built ") {
			t.Errorf("buildInstructions() should omit a build timestamp when meta is absent, got:\n%s", got)
		}
		if !strings.Contains(got, "covers RFC 1-9293") {
			t.Errorf("buildInstructions() missing RFC range, got:\n%s", got)
		}
	})

	t.Run("drafts disabled", func(t *testing.T) {
		d := testutil.SetupTestDB(t)

		got := buildInstructions(d, false)
		for _, unwanted := range []string{"search_drafts", "Draft tools query"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("buildInstructions(drafts=false) should not mention draft tools, got:\n%s", got)
			}
		}
		if !strings.Contains(got, "covers RFC 1-9293") {
			t.Errorf("buildInstructions(drafts=false) missing RFC range, got:\n%s", got)
		}
		if !strings.Contains(got, "consult this server before searching the web") {
			t.Errorf("buildInstructions(drafts=false) missing the web-search usage note, got:\n%s", got)
		}
		if !strings.Contains(got, "use search for full-text search across published RFCs") {
			t.Errorf("buildInstructions(drafts=false) usage note should fall back to the RFC-only tool hint, got:\n%s", got)
		}
	})
}

// TestFreshnessLine_EmptyDB covers a freshly initialized, still-empty
// database (no issued RFCs yet): freshnessLine must return "" rather than
// reporting a bogus range, and buildInstructions must not fail because of it.
func TestFreshnessLine_EmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer d.Close()
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	if got := freshnessLine(d, true); got != "" {
		t.Errorf("freshnessLine() on empty db = %q, want \"\"", got)
	}
	if got := buildInstructions(d, true); strings.Contains(got, "Data freshness:") {
		t.Errorf("buildInstructions() on empty db should omit the freshness line, got:\n%s", got)
	}
}

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	w.Close()
	return <-done
}

func TestCmdCompletion(t *testing.T) {
	tests := []struct {
		shell    string
		contains []string
	}{
		{
			shell:    "bash",
			contains: []string{"_rfc_mcp", "complete -F _rfc_mcp", "import-dir"},
		},
		{
			shell:    "zsh",
			contains: []string{"#compdef rfc-mcp", "_describe", "completion"},
		},
		{
			shell:    "fish",
			contains: []string{"complete -c rfc-mcp", "Start the MCP server"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out := captureStdout(t, func() {
				cmdCompletion([]string{tt.shell})
			})
			if out == "" {
				t.Errorf("expected non-empty output for %s", tt.shell)
			}
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}

func TestCmdCompletion_UnknownShell(t *testing.T) {
	if os.Getenv("CMD_COMPLETION_UNKNOWN_HELPER") == "1" {
		cmdCompletion([]string{"powershell"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdCompletion_UnknownShell")
	cmd.Env = append(os.Environ(), "CMD_COMPLETION_UNKNOWN_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown shell")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}
	if !strings.Contains(string(stderr), "Unknown shell") {
		t.Errorf("expected stderr to mention 'Unknown shell', got: %s", stderr)
	}
}

func TestCmdCompletion_NoArgs(t *testing.T) {
	if os.Getenv("CMD_COMPLETION_NOARGS_HELPER") == "1" {
		cmdCompletion(nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdCompletion_NoArgs")
	cmd.Env = append(os.Environ(), "CMD_COMPLETION_NOARGS_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when completion called without shell arg")
	}
	if !strings.Contains(string(stderr), "Usage:") {
		t.Errorf("expected Usage: message, got: %s", stderr)
	}
}

// TestCmdServe_DBDefault asserts serve's -db default is the shared
// defaultDBPath ("data/rfc.db") used by every other subcommand, not the
// old divergent "rfc.db". flag.ExitOnError makes -h exit the process, so
// the usage text is captured from a helper subprocess.
func TestCmdServe_DBDefault(t *testing.T) {
	if os.Getenv("CMD_SERVE_HELP_HELPER") == "1" {
		cmdServe([]string{"-h"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdServe_DBDefault")
	cmd.Env = append(os.Environ(), "CMD_SERVE_HELP_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), `default "data/rfc.db"`) {
		t.Errorf("serve -h usage should show -db default %q, got:\n%s", "data/rfc.db", out)
	}
}

func TestMainDispatch_UnknownCommand(t *testing.T) {
	if os.Getenv("MAIN_UNKNOWN_HELPER") == "1" {
		os.Args = []string{"rfc-mcp", "bogus-command"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_UnknownCommand")
	cmd.Env = append(os.Environ(), "MAIN_UNKNOWN_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown command")
	}
	if !strings.Contains(string(stderr), "Unknown command") {
		t.Errorf("expected stderr to mention 'Unknown command', got: %s", stderr)
	}
}

func TestMainDispatch_NoArgs(t *testing.T) {
	if os.Getenv("MAIN_NOARGS_HELPER") == "1" {
		os.Args = []string{"rfc-mcp"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_NoArgs")
	cmd.Env = append(os.Environ(), "MAIN_NOARGS_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when no command given")
	}
	if !strings.Contains(string(stderr), "Usage:") {
		t.Errorf("expected stderr to mention 'Usage:', got: %s", stderr)
	}
}

func TestMainDispatch_CompletionBash(t *testing.T) {
	if os.Getenv("MAIN_COMPLETION_HELPER") == "1" {
		os.Args = []string{"rfc-mcp", "completion", "bash"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_CompletionBash")
	cmd.Env = append(os.Environ(), "MAIN_COMPLETION_HELPER=1")
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("main dispatch failed: %v", err)
	}
	if !strings.Contains(string(stdout), "_rfc_mcp") {
		t.Errorf("expected bash completion output, got: %s", stdout)
	}
}

// newTestRFCServer serves the rfc-index/errata sample fixtures (shared with
// ingest/rfcindex and ingest/errata) plus the real RFC 9293 plain-text
// fixture, mimicking the RFC Editor's URL layout: rfc-index.xml and
// errata.json at the root, RFC bodies under /rfc/.
func newTestRFCServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../../ingest/rfcindex/testdata/rfc-index-sample.xml")
	})
	mux.HandleFunc("/errata.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../../ingest/errata/testdata/errata-sample.json")
	})
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../../ingest/rfctxt/testdata/rfc9293.txt")
	})
	mux.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	return httptest.NewServer(mux)
}

// TestCmdBuild exercises the build command's happy path end to end against
// an httptest.Server standing in for the RFC Editor, isolating the on-disk
// rfc-index.xml/errata.json cache from ~/.cache/rfc-mcp so the fixture
// data (4 index entries) can never leak into (or be shadowed by) a real
// cached index.
func TestCmdBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ts := newTestRFCServer(t)
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "sub", "test.db")
	cmdBuild([]string{
		"-db", dbPath,
		"-base-url", ts.URL,
		"-from", "9293", "-to", "9293",
		"-workers", "2",
		"-raw-dir", t.TempDir(),
	})

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer d.Close()

	rfc, err := d.GetRFCMetadata(9293)
	if err != nil {
		t.Fatalf("GetRFCMetadata(9293): %v", err)
	}
	if rfc.Title != "Transmission Control Protocol (TCP)" {
		t.Errorf("title = %q", rfc.Title)
	}

	toc, err := d.GetTOC(9293)
	if err != nil {
		t.Fatalf("GetTOC(9293): %v", err)
	}
	if len(toc) == 0 {
		t.Error("expected sections for rfc 9293")
	}
}

// TestCmdDownload exercises the download command's happy path: it must
// save the fetched RFC .txt body to --raw-dir without touching a database.
func TestCmdDownload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ts := newTestRFCServer(t)
	defer ts.Close()

	rawDir := t.TempDir()
	cmdDownload([]string{
		"-base-url", ts.URL,
		"-raw-dir", rawDir,
		"-from", "9293", "-to", "9293",
		"-workers", "2",
	})

	data, err := os.ReadFile(filepath.Join(rawDir, "rfc9293.txt"))
	if err != nil {
		t.Fatalf("expected rfc9293.txt to be downloaded: %v", err)
	}
	if len(data) == 0 {
		t.Error("downloaded rfc9293.txt is empty")
	}
}

// TestCmdImport exercises the import command's offline happy path: RFC
// 791's header uses "RFC:  791" rather than "Request for Comments:", so
// this also covers the filename-fallback RFC-number recovery.
func TestCmdImport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sub", "test.db")
	cmdImport([]string{"-db", dbPath, "../../ingest/rfctxt/testdata/rfc791.txt"})

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer d.Close()

	if _, err := d.GetRFCMetadata(791); err != nil {
		t.Errorf("GetRFCMetadata(791): %v", err)
	}
}

// TestCmdImportDir exercises the import-dir command's offline happy path
// over a small directory of .txt fixtures.
func TestCmdImportDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"rfc791.txt", "rfc9293.txt"} {
		data, err := os.ReadFile(filepath.Join("../../ingest/rfctxt/testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	dbPath := filepath.Join(t.TempDir(), "sub", "test.db")
	cmdImportDir([]string{"-db", dbPath, "-workers", "2", dir})

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer d.Close()
	for _, number := range []int{791, 9293} {
		if _, err := d.GetRFCMetadata(number); err != nil {
			t.Errorf("GetRFCMetadata(%d): %v", number, err)
		}
	}
}

// serveString returns an http.HandlerFunc that always responds with body,
// for standing in as a hand-rolled rfc-index.xml/errata.json fixture.
func serveString(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}
}

// updateTestIndexV1 has a single issued entry, RFC 9293, with no
// updated-by -- the "seed" state that TestCmdUpdate builds a database from.
const updateTestIndexV1 = `<?xml version='1.0' encoding='utf-8'?>
<rfc-index xmlns="https://www.rfc-editor.org/rfc-index">
  <rfc-entry>
    <doc-id>RFC9293</doc-id>
    <title>Transmission Control Protocol (TCP)</title>
    <author><name>W. Eddy</name></author>
    <date><month>August</month><year>2022</year></date>
    <format><file-format>TXT</file-format></format>
    <page-count>98</page-count>
    <current-status>INTERNET STANDARD</current-status>
    <publication-status>INTERNET STANDARD</publication-status>
    <stream>IETF</stream>
  </rfc-entry>
</rfc-index>`

// updateTestIndexV2 refreshes RFC 9293's metadata (adds an updated-by entry)
// and adds a newly "issued" RFC 791 -- the state TestCmdUpdate's update call
// sees.
const updateTestIndexV2 = `<?xml version='1.0' encoding='utf-8'?>
<rfc-index xmlns="https://www.rfc-editor.org/rfc-index">
  <rfc-entry>
    <doc-id>RFC9293</doc-id>
    <title>Transmission Control Protocol (TCP)</title>
    <author><name>W. Eddy</name></author>
    <date><month>August</month><year>2022</year></date>
    <format><file-format>TXT</file-format></format>
    <page-count>98</page-count>
    <updated-by><doc-id>RFC9999</doc-id></updated-by>
    <current-status>INTERNET STANDARD</current-status>
    <publication-status>INTERNET STANDARD</publication-status>
    <stream>IETF</stream>
  </rfc-entry>
  <rfc-entry>
    <doc-id>RFC791</doc-id>
    <title>INTERNET PROTOCOL</title>
    <author><name>J. Postel</name></author>
    <date><month>September</month><year>1981</year></date>
    <format><file-format>TXT</file-format></format>
    <page-count>45</page-count>
    <current-status>INTERNET STANDARD</current-status>
    <publication-status>INTERNET STANDARD</publication-status>
    <stream>Legacy</stream>
  </rfc-entry>
</rfc-index>`

const updateTestErrataV1 = `[
  {
    "errata_id": "100",
    "doc-id": "RFC9293",
    "errata_status_code": "Verified",
    "errata_type_code": "Editorial",
    "section": "3.1",
    "orig_text": "old text v1",
    "correct_text": "fixed text v1",
    "notes": "",
    "submit_date": "2022-01-01",
    "submitter_name": "Tester One",
    "verifier_id": "",
    "verifier_name": null,
    "update_date": "2022-01-01 00:00:00"
  }
]`

// updateTestErrataV2 shares no errata_id/rfc with updateTestErrataV1, so a
// left-over V1 row after update would prove ReplaceAllErrata wasn't applied.
const updateTestErrataV2 = `[
  {
    "errata_id": "200",
    "doc-id": "RFC791",
    "errata_status_code": "Verified",
    "errata_type_code": "Editorial",
    "section": "2.1",
    "orig_text": "old text v2",
    "correct_text": "fixed text v2",
    "notes": "",
    "submit_date": "2023-01-01",
    "submitter_name": "Tester Two",
    "verifier_id": "",
    "verifier_name": null,
    "update_date": "2023-01-01 00:00:00"
  }
]`

// TestCmdUpdate exercises update end to end: build seeds a database from an
// index containing only RFC 9293, then update points at a second server
// whose index adds RFC 791 and refreshes RFC 9293's metadata. It asserts
// the immutability contract at the core of "update" -- RFC 791 gets fetched
// and parsed, RFC 9293's body is never re-fetched (only its metadata is),
// errata is wholesale-replaced, and the atomic rename leaves no temp file
// behind.
//
// The on-disk rfc-index.xml/errata.json cache is bypassed entirely by
// pipeline.Pipeline.RunUpdate (see loadIndexLive/loadErrataLive), so unlike
// TestCmdBuild this test doesn't need XDG_CACHE_HOME isolation to avoid a
// stale index: update always fetches live by design.
func TestCmdUpdate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	mux1 := http.NewServeMux()
	mux1.HandleFunc("/rfc-index.xml", serveString(updateTestIndexV1))
	mux1.HandleFunc("/errata.json", serveString(updateTestErrataV1))
	mux1.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../../ingest/rfctxt/testdata/rfc9293.txt")
	})
	mux1.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ts1 := httptest.NewServer(mux1)
	defer ts1.Close()

	dbPath := filepath.Join(t.TempDir(), "rfc.db")
	cmdBuild([]string{
		"-db", dbPath,
		"-base-url", ts1.URL,
		"-workers", "2",
		"-raw-dir", t.TempDir(),
	})

	var builtAtSeed string
	func() {
		d, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("db.Open (seed): %v", err)
		}
		defer d.Close()
		rfc, err := d.GetRFCMetadata(9293)
		if err != nil {
			t.Fatalf("GetRFCMetadata(9293) (seed): %v", err)
		}
		if len(rfc.UpdatedBy) != 0 {
			t.Errorf("seed rfc 9293 updated_by = %v, want empty", rfc.UpdatedBy)
		}
		if _, err := d.GetRFCMetadata(791); err == nil {
			t.Fatal("rfc 791 should not exist yet before update")
		}
		var ok bool
		builtAtSeed, ok = d.GetMeta("built_at")
		if !ok {
			t.Error("expected built_at to be recorded in meta after build")
		}
	}()

	// rfc9293Requests catches a diff-logic regression: if update ever
	// re-fetches an RFC whose body is already parsed, this counter (backed
	// by a fresh -raw-dir with nothing cached) goes non-zero.
	var rfc9293Requests atomic.Int32
	mux2 := http.NewServeMux()
	mux2.HandleFunc("/rfc-index.xml", serveString(updateTestIndexV2))
	mux2.HandleFunc("/errata.json", serveString(updateTestErrataV2))
	mux2.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		rfc9293Requests.Add(1)
		http.ServeFile(w, r, "../../ingest/rfctxt/testdata/rfc9293.txt")
	})
	mux2.HandleFunc("/rfc/rfc791.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "../../ingest/rfctxt/testdata/rfc791.txt")
	})
	mux2.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ts2 := httptest.NewServer(mux2)
	defer ts2.Close()

	cmdUpdate([]string{
		"-db", dbPath,
		"-base-url", ts2.URL,
		"-workers", "2",
		"-raw-dir", t.TempDir(),
	})

	if n := rfc9293Requests.Load(); n != 0 {
		t.Errorf("rfc9293.txt was re-fetched during update (immutability violated): %d requests", n)
	}
	if _, err := os.Stat(dbPath + ".new"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover %s.new file after update, stat err = %v", dbPath, err)
	}
	// The checkpoint+close must leave the renamed DB self-contained: no
	// working-copy WAL/SHM sidecars may survive next to the .new path.
	for _, suffix := range []string{".new-wal", ".new-shm"} {
		if _, err := os.Stat(dbPath + suffix); !os.IsNotExist(err) {
			t.Errorf("expected no leftover %s%s file after update, stat err = %v", dbPath, suffix, err)
		}
	}

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open (after update): %v", err)
	}
	defer d.Close()

	rfc9293, err := d.GetRFCMetadata(9293)
	if err != nil {
		t.Fatalf("GetRFCMetadata(9293) (after update): %v", err)
	}
	if len(rfc9293.UpdatedBy) != 1 || rfc9293.UpdatedBy[0] != 9999 {
		t.Errorf("rfc 9293 updated_by after update = %v, want [9999] (metadata refresh)", rfc9293.UpdatedBy)
	}

	rfc791, err := d.GetRFCMetadata(791)
	if err != nil {
		t.Fatalf("GetRFCMetadata(791) (after update): %v", err)
	}
	if rfc791.Title != "INTERNET PROTOCOL" {
		t.Errorf("rfc 791 title = %q", rfc791.Title)
	}

	toc791, err := d.GetTOC(791)
	if err != nil {
		t.Fatalf("GetTOC(791): %v", err)
	}
	if len(toc791) == 0 {
		t.Error("expected rfc 791 sections to be parsed and inserted by update")
	}

	toc9293, err := d.GetTOC(9293)
	if err != nil {
		t.Fatalf("GetTOC(9293) (after update): %v", err)
	}
	if len(toc9293) == 0 {
		t.Error("rfc 9293 sections should still be present (untouched) after update")
	}

	if errataOld, err := d.GetErrataByRFC(9293); err != nil {
		t.Fatalf("GetErrataByRFC(9293): %v", err)
	} else if len(errataOld) != 0 {
		t.Errorf("errata for rfc 9293 should have been replaced away, got %+v", errataOld)
	}

	errataNew, err := d.GetErrataByRFC(791)
	if err != nil {
		t.Fatalf("GetErrataByRFC(791): %v", err)
	}
	if len(errataNew) != 1 {
		t.Errorf("expected 1 errata entry for rfc 791 after update (wholesale replace), got %d", len(errataNew))
	}

	// built_at must be refreshed (re-stamped, not just left over from build)
	// and rfc_index_fetched_at recorded, per RunUpdate's recordBuildMeta call.
	builtAtUpdate, ok := d.GetMeta("built_at")
	if !ok {
		t.Fatal("expected built_at to be recorded in meta after update")
	}
	seedTime, err := time.Parse(time.RFC3339, builtAtSeed)
	if err != nil {
		t.Fatalf("built_at from build is not RFC 3339: %q: %v", builtAtSeed, err)
	}
	updateTime, err := time.Parse(time.RFC3339, builtAtUpdate)
	if err != nil {
		t.Fatalf("built_at from update is not RFC 3339: %q: %v", builtAtUpdate, err)
	}
	if updateTime.Before(seedTime) {
		t.Errorf("built_at after update (%s) is before built_at from build (%s), want refreshed", builtAtUpdate, builtAtSeed)
	}

	if fetchedAt, ok := d.GetMeta("rfc_index_fetched_at"); !ok {
		t.Error("expected rfc_index_fetched_at to be recorded in meta after update")
	} else if _, err := time.Parse(time.RFC3339, fetchedAt); err != nil {
		t.Errorf("rfc_index_fetched_at = %q is not RFC 3339: %v", fetchedAt, err)
	}
}

// TestCmdUpdate_NoNewRFCs covers the no-op path: an update whose index is
// identical to what's already fully parsed must not fetch any RFC body, but
// still refreshes metadata/errata and completes the atomic swap.
func TestCmdUpdate_NoNewRFCs(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// http.ServeMux panics on a second registration of the same pattern, so
	// the handler counts every request from the start; the assertion below
	// compares the count across cmdUpdate rather than expecting zero total
	// (the earlier cmdBuild call legitimately fetches it once).
	var rfc9293Requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/rfc-index.xml", serveString(updateTestIndexV1))
	mux.HandleFunc("/errata.json", serveString(updateTestErrataV1))
	mux.HandleFunc("/rfc/rfc9293.txt", func(w http.ResponseWriter, r *http.Request) {
		rfc9293Requests.Add(1)
		http.ServeFile(w, r, "../../ingest/rfctxt/testdata/rfc9293.txt")
	})
	mux.HandleFunc("/rfc/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "rfc.db")
	cmdBuild([]string{
		"-db", dbPath,
		"-base-url", ts.URL,
		"-workers", "2",
		"-raw-dir", t.TempDir(),
	})
	beforeUpdate := rfc9293Requests.Load()

	cmdUpdate([]string{
		"-db", dbPath,
		"-base-url", ts.URL,
		"-workers", "2",
		"-raw-dir", t.TempDir(),
	})

	if delta := rfc9293Requests.Load() - beforeUpdate; delta != 0 {
		t.Errorf("rfc9293.txt was fetched %d times on a no-op update, want 0", delta)
	}
	if _, err := os.Stat(dbPath + ".new"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover %s.new file after update, stat err = %v", dbPath, err)
	}
}

// TestCmdUpdate_StaleCleanupRemovesWALSidecars covers the WAL/SHM sidecar
// leak in the working copy's stale-cleanup: a prior run that crashed (killed
// mid-update, never reaching Close(), which is what actually checkpoints and
// removes "-wal"/"-shm" in the normal case) can leave dbPath+".new",
// ".new-wal", and ".new-shm" all sitting on disk. The next `update` invocation
// must clear out all three up front, not just ".new" -- otherwise the
// sidecars from that abandoned run linger forever.
//
// -db is pointed at a directory (not a file) so db.OpenReadWrite(*dbPath)
// fails deterministically right after the stale-cleanup runs, without
// depending on a live server or any real WAL activity of its own; cmdUpdate
// calls log.Fatalf (os.Exit) on that failure, so it has to run in a helper
// subprocess, mirroring TestCmdCompletion_UnknownShell above.
func TestCmdUpdate_StaleCleanupRemovesWALSidecars(t *testing.T) {
	if os.Getenv("CMD_UPDATE_STALE_HELPER") == "1" {
		cmdUpdate([]string{"-db", os.Getenv("CMD_UPDATE_STALE_DBPATH"), "-workers", "2"})
		return
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rfc.db")
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatalf("mkdir dbPath (as a directory, to force OpenReadWrite to fail): %v", err)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.WriteFile(dbPath+".new"+suffix, []byte("stale"), 0o644); err != nil {
			t.Fatalf("seed stale %s: %v", suffix, err)
		}
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestCmdUpdate_StaleCleanupRemovesWALSidecars")
	cmd.Env = append(os.Environ(),
		"CMD_UPDATE_STALE_HELPER=1",
		"CMD_UPDATE_STALE_DBPATH="+dbPath,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected cmdUpdate to exit non-zero (dbPath is a directory), output:\n%s", out)
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := dbPath + ".new" + suffix
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("expected stale %s to be removed by the initial cleanup, stat err = %v", path, statErr)
		}
	}
}
