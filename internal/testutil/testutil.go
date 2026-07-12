// Package testutil provides shared test helpers for the rfc-mcp project.
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/higebu/rfc-mcp/db"
)

// SeedData is the canonical seed SQL used across test packages. It seeds all
// four content tables (rfcs, sections, rfc_references, errata) with a small
// set of real RFCs so tests can exercise filtering, full-text search, and
// cross-reference lookups without re-deriving fixture data per package.
const SeedData = `
INSERT INTO rfcs (number, title, status, stream, date, page_count, authors, keywords, abstract, wg, area, doi, errata_url, obsoletes, obsoleted_by, updates, updated_by, also, not_issued) VALUES
    (9293, 'Transmission Control Protocol (TCP) Specification', 'INTERNET STANDARD', 'IETF', '2022-08', 160,
     '["W. Eddy"]', '["TCP","transport protocol"]',
     'This document specifies the Transmission Control Protocol (TCP).',
     'tcpm', 'tsv', '10.17487/RFC9293', 'https://www.rfc-editor.org/errata/rfc9293',
     '[793,879,2873,6093,6429,6528,6691]', '[]', '[1122]', '[]', '["STD 7"]', 0),
    (793, 'TRANSMISSION CONTROL PROTOCOL', 'INTERNET STANDARD', 'Legacy', '1981-09', 85,
     '["J. Postel"]', '[]',
     'This document describes the DoD Standard Transmission Control Protocol.',
     '', '', '', '', '[]', '[9293]', '[]', '[]', '[]', 0),
    (4271, 'A Border Gateway Protocol 4 (BGP-4)', 'DRAFT STANDARD', 'IETF', '2006-01', 104,
     '["Y. Rekhter","T. Li","S. Hares"]', '["BGP","routing"]',
     'This document defines the Border Gateway Protocol version 4 (BGP-4).',
     'idr', 'rtg', '10.17487/RFC4271', '', '[1771]', '[]', '[]', '[]', '[]', 0),
    (9999, '', '', '', '', 0, '[]', '[]', '', '', '', '', '', '[]', '[]', '[]', '[]', '[]', 1);

INSERT INTO sections (rfc, number, title, level, parent_number, content) VALUES
    (9293, '1', 'Introduction', 1, NULL,
     '# 1 Introduction
This document specifies the Transmission Control Protocol (TCP). TCP is a reliable, connection-oriented transport layer protocol.'),
    (9293, '3', 'Functional Specification', 1, NULL,
     '# 3 Functional Specification
TCP uses a three-way handshake to establish a connection between two endpoints.'),
    (9293, '3.1', 'Header Format', 2, '3',
     '## 3.1 Header Format
The TCP header contains Source Port, Destination Port, Sequence Number, and Acknowledgment Number fields. See RFC 793 for the original header format.'),
    (9293, '3.1.1', 'Source Port', 3, '3.1',
     '### 3.1.1 Source Port
The source port number, as discussed in Section 3.1 of this document.'),
    (793, '1', 'Introduction', 1, NULL,
     '# 1 Introduction
This document describes the DoD Standard Transmission Control Protocol (TCP).'),
    (4271, '5', 'Path Attributes', 1, NULL,
     '# 5 Path Attributes
Each path attribute is a triple <attribute type, attribute length, attribute value>.'),
    (4271, '5.1', 'Path Attribute Usage', 2, '5',
     '## 5.1 Path Attribute Usage
This section is based on RFC 9293 Section 3.1 and the original specification in RFC 793.');

INSERT INTO rfc_references (source_rfc, source_section, target_rfc, target_section, context) VALUES
    (4271, '5.1', 9293, '3.1', '...based on RFC 9293 Section 3.1 and the original...'),
    (4271, '5.1', 793, '', '...original specification in RFC 793...');

INSERT INTO errata (id, rfc, status, type, section, orig_text, correct_text, notes, submitted_date, submitter_name, verifier_name, updated_date) VALUES
    (1, 4271, 'Verified', 'Technical', '5.1', 'triple <attribute type, attribute length>', 'triple <attribute type, attribute length, attribute value>', 'Missing field in original text.', '2020-01-01', 'Jane Doe', 'John Smith', '2020-02-01'),
    (2, 4271, 'Reported', 'Editorial', '5', 'Path Attribute', 'Path Attributes', '', '2021-03-15', 'Alex Kim', '', '');
`

// SetupTestDB creates a temporary SQLite database with the standard schema and seed data.
func SetupTestDB(t testing.TB) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := d.ExecScript(db.Schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	if err := d.ExecScript(SeedData); err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}
