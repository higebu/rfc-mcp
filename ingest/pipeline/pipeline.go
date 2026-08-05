package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/higebu/rfc-mcp/db"
	"github.com/higebu/rfc-mcp/ingest/errata"
	"github.com/higebu/rfc-mcp/ingest/rfcindex"
	"github.com/higebu/rfc-mcp/ingest/rfctxt"
)

const (
	indexCacheKey  = "rfc-index.xml"
	errataCacheKey = "errata.json"
	minConcurrency = 8
)

// DefaultConcurrency returns a sensible default worker count for the
// network-bound fetches this package performs: at least minConcurrency, or
// NumCPU if higher. Ported from 3gpp-mcp's defaultConcurrency.
func DefaultConcurrency() int {
	if n := runtime.NumCPU(); n > minConcurrency {
		return n
	}
	return minConcurrency
}

// Pipeline orchestrates the fetch-parse-store workflow: rfc-index.xml and
// errata.json metadata, plus per-RFC plain-text body parsing and storage.
type Pipeline struct {
	DB      *db.DB
	Client  *http.Client
	Workers int
	Timeout time.Duration
	RawDir  string

	// BaseURL overrides the RFC Editor root (default defaultRFCEditorRoot)
	// so tests can point the pipeline at an httptest.Server; rfc-index.xml
	// and errata.json live directly under it, and RFC txt bodies live
	// under BaseURL+"/rfc/rfcN.txt".
	BaseURL string

	// indexed holds every rfc-index.xml entry by number. Run populates it
	// before processing any RFC, so processOne can look up a title without
	// a round trip through DB (which OpenReadWrite serializes to a single
	// connection, so a read there would queue behind other workers' writes).
	indexed map[int]db.RFC
}

func (p *Pipeline) root() string {
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return defaultRFCEditorRoot
}

func (p *Pipeline) applyDefaults() {
	if p.Workers <= 0 {
		p.Workers = DefaultConcurrency()
	}
	if p.Timeout == 0 {
		p.Timeout = 30 * time.Second
	}
	if p.Client == nil {
		p.Client = &http.Client{Timeout: p.Timeout}
	}
}

// fetchCached fetches path (relative to p.root()) via the on-disk cache,
// falling back to a live GET (and refreshing the cache) on a miss. The
// returned time is when the data was actually obtained: the cache file's
// mtime on a hit, or now (UTC) on a live fetch -- used by loadIndex to
// record rfc_index_fetched_at provenance.
func (p *Pipeline) fetchCached(ctx context.Context, cacheKey, path string) ([]byte, time.Time, error) {
	if data, mtime, err := loadCache(cacheKey, defaultCacheTTL); err == nil && data != nil {
		return data, mtime, nil
	}
	data, err := httpGetWithRetry(ctx, p.Client, p.root()+path)
	if err != nil {
		return nil, time.Time{}, err
	}
	fetchedAt := time.Now().UTC()
	if err := saveCache(cacheKey, data); err != nil {
		log.Printf("warning: failed to cache %s: %v", cacheKey, err)
	}
	return data, fetchedAt, nil
}

func (p *Pipeline) loadIndex(ctx context.Context) ([]db.RFC, time.Time, error) {
	data, fetchedAt, err := p.fetchCached(ctx, indexCacheKey, "/rfc-index.xml")
	if err != nil {
		return nil, time.Time{}, err
	}
	rfcs, err := rfcindex.Parse(bytes.NewReader(data))
	return rfcs, fetchedAt, err
}

func (p *Pipeline) loadErrata(ctx context.Context) ([]db.Errata, error) {
	data, _, err := p.fetchCached(ctx, errataCacheKey, "/errata.json")
	if err != nil {
		return nil, err
	}
	return errata.Parse(bytes.NewReader(data))
}

// fetchLive is fetchCached's counterpart for RunUpdate: it always performs a
// live GET (skipping the on-disk cache read, though it still refreshes the
// cache file on success) since update's whole point is to notice newly
// issued RFCs and refreshed obsoletes/updated_by, which a stale cached index
// would hide for up to defaultCacheTTL.
func (p *Pipeline) fetchLive(ctx context.Context, cacheKey, path string) ([]byte, error) {
	data, err := httpGetWithRetry(ctx, p.Client, p.root()+path)
	if err != nil {
		return nil, err
	}
	if err := saveCache(cacheKey, data); err != nil {
		log.Printf("warning: failed to cache %s: %v", cacheKey, err)
	}
	return data, nil
}

func (p *Pipeline) loadIndexLive(ctx context.Context) ([]db.RFC, time.Time, error) {
	data, err := p.fetchLive(ctx, indexCacheKey, "/rfc-index.xml")
	fetchedAt := time.Now().UTC()
	if err != nil {
		return nil, time.Time{}, err
	}
	rfcs, err := rfcindex.Parse(bytes.NewReader(data))
	return rfcs, fetchedAt, err
}

func (p *Pipeline) loadErrataLive(ctx context.Context) ([]db.Errata, error) {
	data, err := p.fetchLive(ctx, errataCacheKey, "/errata.json")
	if err != nil {
		return nil, err
	}
	return errata.Parse(bytes.NewReader(data))
}

// issuedNumbersInRange returns the sorted, issued (non-not-issued) RFC
// numbers in [from, to] (0 = unbounded on that side) that have a plain-text
// rendition to fetch, plus a count of issued, in-range numbers that don't
// (db.RFC.HasText false -- see ingest/rfcindex). Attempting to fetch those
// would just 404 on every retry, so callers report them separately
// (TEXT_UNAVAILABLE) instead of feeding them to processAll/fetchRFCText.
func issuedNumbersInRange(rfcs []db.RFC, from, to int) (numbers []int, textUnavailable int) {
	for _, r := range rfcs {
		if r.NotIssued {
			continue
		}
		if from != 0 && r.Number < from {
			continue
		}
		if to != 0 && r.Number > to {
			continue
		}
		if !r.HasText {
			textUnavailable++
			continue
		}
		numbers = append(numbers, r.Number)
	}
	sort.Ints(numbers)
	return numbers, textUnavailable
}

// Run fetches rfc-index.xml and errata.json, then downloads and parses the
// plain-text body of every issued RFC number in [from, to] (from == 0 &&
// to == 0 means every issued RFC in the index). Metadata for every entry in
// the index -- issued or not-issued, regardless of range -- is always
// upserted first, since it's cheap and keeps obsoletes/updated_by
// cross-references complete even for RFCs outside the requested range.
//
// Per-RFC fetch/parse failures are logged and counted, not fatal: Run only
// returns an error for a failure that would leave the database globally
// inconsistent (index/errata fetch, or a DB write failure).
func (p *Pipeline) Run(ctx context.Context, from, to int) error {
	p.applyDefaults()

	rfcs, indexFetchedAt, err := p.loadIndex(ctx)
	if err != nil {
		return fmt.Errorf("load rfc-index.xml: %w", err)
	}
	log.Printf("Loaded %d entries from rfc-index.xml", len(rfcs))

	p.indexed = make(map[int]db.RFC, len(rfcs))
	for _, r := range rfcs {
		if err := p.DB.UpsertRFC(r); err != nil {
			return fmt.Errorf("upsert rfc %d: %w", r.Number, err)
		}
		p.indexed[r.Number] = r
	}

	items, err := p.loadErrata(ctx)
	if err != nil {
		return fmt.Errorf("load errata.json: %w", err)
	}
	log.Printf("Loaded %d errata entries", len(items))
	if err := p.DB.ReplaceAllErrata(items); err != nil {
		return fmt.Errorf("replace errata: %w", err)
	}

	numbers, textUnavailable := issuedNumbersInRange(rfcs, from, to)
	log.Printf("Processing %d issued RFCs with %d workers...", len(numbers), p.Workers)

	stats := p.processAll(ctx, numbers)
	stats["TEXT_UNAVAILABLE"] = textUnavailable
	log.Println("Pipeline complete:")
	for _, k := range []string{"OK", "FETCH_FAILED", "PARSE_DEGRADED", "TEXT_UNAVAILABLE"} {
		if stats[k] > 0 {
			log.Printf("  %s: %d", k, stats[k])
		}
	}
	return p.recordBuildMeta(indexFetchedAt)
}

// RunUpdate refreshes rfc-index.xml and errata.json live (see fetchLive),
// upserts metadata for every index entry (refreshing status/obsoleted_by/
// updated_by even for RFCs already in the database), replaces errata
// wholesale, then fetches and parses only the issued RFC numbers that don't
// yet have parsed sections stored -- RFC bodies are immutable once
// published, so existing ones are never re-fetched.
func (p *Pipeline) RunUpdate(ctx context.Context) error {
	p.applyDefaults()

	rfcs, indexFetchedAt, err := p.loadIndexLive(ctx)
	if err != nil {
		return fmt.Errorf("load rfc-index.xml: %w", err)
	}
	log.Printf("Loaded %d entries from rfc-index.xml", len(rfcs))

	p.indexed = make(map[int]db.RFC, len(rfcs))
	for _, r := range rfcs {
		if err := p.DB.UpsertRFC(r); err != nil {
			return fmt.Errorf("upsert rfc %d: %w", r.Number, err)
		}
		p.indexed[r.Number] = r
	}

	items, err := p.loadErrataLive(ctx)
	if err != nil {
		return fmt.Errorf("load errata.json: %w", err)
	}
	log.Printf("Loaded %d errata entries", len(items))
	if err := p.DB.ReplaceAllErrata(items); err != nil {
		return fmt.Errorf("replace errata: %w", err)
	}

	existing, err := p.DB.ExistingRFCNumbers()
	if err != nil {
		return fmt.Errorf("list existing rfc numbers: %w", err)
	}
	existingSet := make(map[int]bool, len(existing))
	for _, n := range existing {
		existingSet[n] = true
	}

	issued, textUnavailable := issuedNumbersInRange(rfcs, 0, 0)
	var newNumbers []int
	for _, n := range issued {
		if !existingSet[n] {
			newNumbers = append(newNumbers, n)
		}
	}
	log.Printf("Found %d new RFCs to fetch (of %d issued)", len(newNumbers), len(issued))

	stats := p.processAll(ctx, newNumbers)
	stats["TEXT_UNAVAILABLE"] = textUnavailable
	log.Println("Update complete:")
	for _, k := range []string{"OK", "FETCH_FAILED", "PARSE_DEGRADED", "TEXT_UNAVAILABLE"} {
		if stats[k] > 0 {
			log.Printf("  %s: %d", k, stats[k])
		}
	}
	return p.recordBuildMeta(indexFetchedAt)
}

// recordBuildMeta stamps the meta table with build provenance once the
// corpus insert/refresh transaction has completed successfully: built_at
// (now, UTC) and rfc_index_fetched_at (when the rfc-index.xml driving this
// run was obtained -- see loadIndex/loadIndexLive). Surfaced to MCP clients
// via the server Instructions and the rfcRangeHint suffix (cmd/rfc-mcp,
// tools package) so an LLM can judge how current the baked snapshot is.
func (p *Pipeline) recordBuildMeta(indexFetchedAt time.Time) error {
	if err := p.DB.SetMeta("built_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("set built_at meta: %w", err)
	}
	if err := p.DB.SetMeta("rfc_index_fetched_at", indexFetchedAt.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("set rfc_index_fetched_at meta: %w", err)
	}
	return nil
}

// processAll runs processOne over numbers with p.Workers goroutines. DB
// writes happen inside each worker without extra locking: db.OpenReadWrite
// caps the connection pool to one connection, so writes are already
// serialized there (mirrors 3gpp-mcp's Pipeline.Run, which relies on the
// same property instead of adding a redundant mutex).
func (p *Pipeline) processAll(ctx context.Context, numbers []int) map[string]int {
	sem := make(chan struct{}, p.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	stats := make(map[string]int)
	total := len(numbers)

	for i, number := range numbers {
		if ctx.Err() != nil {
			break
		}
		number, idx := number, i+1
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			status, err := p.processOne(ctx, number)
			if err != nil {
				log.Printf("[%d/%d] RFC %d: %s (%v)", idx, total, number, status, err)
			} else {
				log.Printf("[%d/%d] RFC %d: %s", idx, total, number, status)
			}
			mu.Lock()
			stats[status]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return stats
}

// processOne fetches, parses, and stores a single RFC's body text.
func (p *Pipeline) processOne(ctx context.Context, number int) (string, error) {
	raw, err := fetchRFCText(ctx, p.Client, number, p.RawDir, p.root()+"/rfc")
	if err != nil {
		return "FETCH_FAILED", err
	}

	rfc, ok := p.indexed[number]
	if !ok {
		return "FETCH_FAILED", fmt.Errorf("rfc %d missing from index", number)
	}

	sections, err := rfctxt.ParseRFCText(raw, number, rfc.Title)
	if err != nil {
		return "FETCH_FAILED", fmt.Errorf("parse rfc %d: %w", number, err)
	}

	dbSections, refs := buildSectionsAndReferences(number, sections)

	if err := p.DB.InsertRFCWithSections(rfc, dbSections); err != nil {
		return "FETCH_FAILED", fmt.Errorf("insert rfc %d: %w", number, err)
	}
	if err := p.replaceReferences(number, refs); err != nil {
		return "FETCH_FAILED", fmt.Errorf("insert references for rfc %d: %w", number, err)
	}

	// ParseRFCText's Tier-3 fallback (whole body as one section) sets
	// Number to "body" -- see its doc comment. That's the "parse degraded"
	// signal: the doc yielded no real section structure.
	if len(sections) == 1 && sections[0].Number == "body" {
		return "PARSE_DEGRADED", nil
	}
	return "OK", nil
}

// Download fetches rfc-index.xml (to learn which RFC numbers are issued)
// and downloads the plain-text body of every issued number in [from, to]
// (0, 0 = all) into p.RawDir, without touching a database (p.DB is unused).
func (p *Pipeline) Download(ctx context.Context, from, to int) error {
	p.applyDefaults()

	data, _, err := p.fetchCached(ctx, indexCacheKey, "/rfc-index.xml")
	if err != nil {
		return fmt.Errorf("load rfc-index.xml: %w", err)
	}
	rfcs, err := rfcindex.Parse(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse rfc-index.xml: %w", err)
	}

	numbers, textUnavailable := issuedNumbersInRange(rfcs, from, to)
	log.Printf("Downloading %d issued RFCs with %d workers...", len(numbers), p.Workers)

	sem := make(chan struct{}, p.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	stats := make(map[string]int)
	total := len(numbers)

	for i, number := range numbers {
		if ctx.Err() != nil {
			break
		}
		number, idx := number, i+1
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			_, err := fetchRFCText(ctx, p.Client, number, p.RawDir, p.root()+"/rfc")
			status := "OK"
			if err != nil {
				status = "FETCH_FAILED"
				log.Printf("[%d/%d] RFC %d: FETCH_FAILED (%v)", idx, total, number, err)
			} else {
				log.Printf("[%d/%d] RFC %d: OK", idx, total, number)
			}
			mu.Lock()
			stats[status]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	stats["TEXT_UNAVAILABLE"] = textUnavailable

	log.Println("Download complete:")
	for _, k := range []string{"OK", "FETCH_FAILED", "TEXT_UNAVAILABLE"} {
		if stats[k] > 0 {
			log.Printf("  %s: %d", k, stats[k])
		}
	}
	return nil
}

// isReferencesSection reports whether s is (a whole, or normative/informative
// half of) an RFC's References section, identified either by the slug
// ParseRFCText's knownUnnumbered table assigns to an unnumbered heading, or
// by title text for documents where References is a numbered section (e.g.
// RFC 9293's "8.1. Normative References" / "8.2. Informative References").
func isReferencesSection(s rfctxt.Section) bool {
	switch s.Number {
	case "references", "normative-references", "informative-references":
		return true
	}
	switch strings.ToLower(s.Title) {
	case "references", "normative references", "informative references":
		return true
	}
	return false
}

// mergeBracketMap merges src into dst (allocating dst if necessary) and
// returns the result.
func mergeBracketMap(dst, src map[string]int) map[string]int {
	if src == nil {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// buildSectionsAndReferences converts parsed rfctxt sections into db rows,
// resolving numeric-bracket citations (e.g. "[16]") via the RFC's own
// References section(s) before extracting cross-references from every
// section.
func buildSectionsAndReferences(number int, sections []rfctxt.Section) ([]db.Section, []db.Reference) {
	var bracketMap map[string]int
	for _, s := range sections {
		if isReferencesSection(s) {
			bracketMap = mergeBracketMap(bracketMap, db.ParseBracketedRefMap(s.Content))
		}
	}

	dbSections := make([]db.Section, len(sections))
	var refs []db.Reference
	for i, s := range sections {
		dbSections[i] = db.Section{
			RFC:          number,
			Number:       s.Number,
			Title:        s.Title,
			Level:        s.Level,
			ParentNumber: s.ParentNumber,
			Content:      s.Content,
		}
		refs = append(refs, db.ExtractReferences(number, s.Number, s.Content, bracketMap)...)
	}
	return dbSections, refs
}

// rfcNumberRE matches the RFC number from a "Request for Comments: N"
// header line -- the modern (and most historical) RFC text header format.
var rfcNumberRE = regexp.MustCompile(`(?i)Request for Comments:\s*(\d+)`)

// rfcFilenameRE extracts the RFC number from a "rfcN.txt"-style filename,
// the fallback for older documents whose header uses some other label
// instead (e.g. RFC 791's "RFC:  791").
var rfcFilenameRE = regexp.MustCompile(`(?i)rfc(\d+)\.txt$`)

func rfcNumberFromText(raw []byte) (int, bool) {
	m := rfcNumberRE.FindSubmatch(raw)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return n, true
}

func rfcNumberFromFilename(path string) (int, bool) {
	m := rfcFilenameRE.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

var (
	// monthYearRE excludes a lone "Month YYYY" header line (the document
	// date, e.g. "November 1987") from title detection: it can land on its
	// own indented line when a preceding column's author name is long
	// enough to push it there, ahead of the real title.
	monthYearRE = regexp.MustCompile(`(?i)^(January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{4}$`)
	// titleLabelRE strips a literal "Title:" label used by some of the
	// earliest RFCs (e.g. RFC 1's "Title:   Host Software" front-matter
	// block) so the recovered title doesn't carry it verbatim.
	titleLabelRE = regexp.MustCompile(`(?i)^Title\s*:\s*`)
	// titleExcludeRE rejects other front-matter fields from that same
	// old-style block ("Author:", "Date:", ...) and the oddball
	// "Network Working Group ... Request for Comment:" repeat line RFC 1
	// carries.
	titleExcludeRE = regexp.MustCompile(`(?i)^(Author|Installation|Date|Editor)\s*:|Request for Comment`)
)

// extractTitleFromHeader makes a best-effort attempt to recover an RFC's
// title from its plain-text header block, for offline import when no
// rfc-index.xml row exists yet to supply the authoritative title.
//
// It scans the first 40 lines for the first one indented by at least 4
// columns (body paragraphs in every fixture checked are indented exactly 3;
// the classic RFC layout centers the title well past that) whose content
// doesn't look like a metadata line: field-labeled lines ("Obsoletes:",
// "Category: ...") sit at column 0 and are excluded by the indent check
// alone, but a few things need an explicit exclusion -- a lone date line
// pushed onto its own indented line, and (for the oldest RFCs) an
// old-style "Author:"/"Date:" front-matter block sitting alongside a
// "Title:" line, which is kept and unlabeled. Verified against the
// rfc9293/4271/2119/1035/791/1 fixtures in ingest/rfctxt/testdata.
func extractTitleFromHeader(raw []byte) string {
	lines := strings.Split(string(raw), "\n")
	limit := len(lines)
	if limit > 40 {
		limit = 40
	}
	for _, line := range lines[:limit] {
		trimmedRight := strings.TrimRight(line, " \t\r")
		content := strings.TrimSpace(trimmedRight)
		if content == "" {
			continue
		}
		indent := len(trimmedRight) - len(strings.TrimLeft(trimmedRight, " "))
		if indent < 4 {
			continue
		}
		if content[0] >= '0' && content[0] <= '9' {
			continue
		}
		if monthYearRE.MatchString(content) || titleExcludeRE.MatchString(content) {
			continue
		}
		return titleLabelRE.ReplaceAllString(content, "")
	}
	return ""
}

// ImportFile parses a single local RFC .txt file (offline, no network
// access) and stores it in the database. The RFC number is recovered from
// the file's own "Request for Comments: N" header line, falling back to
// the filename ("rfcN.txt") for older documents that don't carry that
// label. The title comes from an existing rfcs row if rfc-index.xml has
// already been loaded (e.g. via a prior build), else a best-effort scrape
// of the header (extractTitleFromHeader), else a generic "RFC N" placeholder.
func (p *Pipeline) ImportFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	number, ok := rfcNumberFromText(raw)
	if !ok {
		number, ok = rfcNumberFromFilename(path)
		if !ok {
			return fmt.Errorf("%s: could not determine RFC number from header or filename", path)
		}
	}

	return p.importRaw(number, raw)
}

func (p *Pipeline) importRaw(number int, raw []byte) error {
	rfc, err := p.DB.GetRFCMetadata(number)
	if err != nil {
		title := extractTitleFromHeader(raw)
		if title == "" {
			title = fmt.Sprintf("RFC %d", number)
		}
		// HasText is true unconditionally: importRaw is only ever called with
		// an actual RFC .txt body already in hand.
		rfc = &db.RFC{Number: number, Title: title, HasText: true}
	}

	sections, parseErr := rfctxt.ParseRFCText(raw, number, rfc.Title)
	if parseErr != nil {
		return fmt.Errorf("parse rfc %d: %w", number, parseErr)
	}

	dbSections, refs := buildSectionsAndReferences(number, sections)

	if err := p.DB.InsertRFCWithSections(*rfc, dbSections); err != nil {
		return fmt.Errorf("insert rfc %d: %w", number, err)
	}
	return p.replaceReferences(number, refs)
}

// replaceReferences persists the newly extracted references for an RFC.
// InsertReferences already deletes stale rows for every source RFC present
// in refs, but an empty set gives it nothing to scope that delete to, so a
// re-parse that yields no references must clear the old rows explicitly.
func (p *Pipeline) replaceReferences(number int, refs []db.Reference) error {
	if len(refs) == 0 {
		return p.DB.DeleteReferencesForRFC(number)
	}
	return p.DB.InsertReferences(refs)
}

// ImportDir parses every "*.txt" file in dir (offline, no network access),
// up to workers files in parallel. Database writes are serialized by the
// single-connection DB pool (see db.OpenReadWrite), same as processAll.
func (p *Pipeline) ImportDir(dir string, workers int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no .txt files found in %s", dir)
	}
	sort.Strings(files)
	log.Printf("Found %d .txt files", len(files))

	if workers <= 0 {
		workers = DefaultConcurrency()
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failed int

	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := p.ImportFile(f); err != nil {
				log.Printf("  ERROR: %s: %v", filepath.Base(f), err)
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			log.Printf("  %s: imported", filepath.Base(f))
		}()
	}
	wg.Wait()

	if failed == len(files) {
		return fmt.Errorf("all %d files failed to import", len(files))
	}
	if failed > 0 {
		log.Printf("  %d of %d files failed to import", failed, len(files))
	}
	return nil
}
