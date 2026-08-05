.PHONY: build install import import-dir check-import-file check-import-dir build-db download update test db-info clean

DB_PATH ?= data/rfc.db
BIN_DIR ?= bin
# FROM/TO restrict build-db/download to a numeric RFC range, e.g.
# `make build-db FROM=9290 TO=9295`. Empty (the default) means the full corpus.
FROM ?=
TO ?=
from_flag = $(if $(FROM),--from "$(FROM)",)
to_flag = $(if $(TO),--to "$(TO)",)

# Build the MCP server
build:
	go build -o "$(BIN_DIR)/rfc-mcp" ./cmd/rfc-mcp

# Install the MCP server
install:
	go install ./cmd/rfc-mcp

# Argument guards run as prerequisites listed before `build`, so a missing
# FILE/DIR fails fast without compiling first (prerequisites run left to
# right in non-parallel make, the default here).
check-import-file:
	@test -n "$(FILE)" || { echo "FILE required (usage: make import FILE=path/to/rfcNNNN.txt)"; exit 1; }

check-import-dir:
	@test -n "$(DIR)" || { echo "DIR required (usage: make import-dir DIR=raw)"; exit 1; }

# Import a single RFC .txt file (usage: make import FILE=path/to/rfcNNNN.txt)
import: check-import-file build
	"./$(BIN_DIR)/rfc-mcp" import --db "$(DB_PATH)" "$(FILE)"

# Import all RFC .txt files in a directory (usage: make import-dir DIR=raw)
import-dir: check-import-dir build
	"./$(BIN_DIR)/rfc-mcp" import-dir --db "$(DB_PATH)" "$(DIR)"

# Download + import in one step (recommended). Builds the full RFC corpus by
# default (~8 min, ~865 MB); pass FROM/TO to restrict to a numeric range.
build-db: build
	"./$(BIN_DIR)/rfc-mcp" build $(from_flag) $(to_flag) --db "$(DB_PATH)"

# Download RFC .txt bodies only, no database import.
download: build
	"./$(BIN_DIR)/rfc-mcp" download $(from_flag) $(to_flag)

# Refresh an existing database: fetch newly issued RFCs and refresh
# metadata/errata for all of them.
update: build
	"./$(BIN_DIR)/rfc-mcp" update --db "$(DB_PATH)"

# Show database info. built_at/rfc_index_fetched_at are run as separate
# sqlite3 invocations (rather than one multi-statement command) so a
# database predating the meta table still prints the row counts instead of
# failing silently.
db-info:
	@sqlite3 "$(DB_PATH)" "SELECT COUNT(*) || ' rfcs' FROM rfcs; SELECT COUNT(*) || ' sections' FROM sections;" 2>/dev/null || echo "Database not found: $(DB_PATH)"
	@sqlite3 "$(DB_PATH)" "SELECT 'built_at: ' || value FROM meta WHERE key = 'built_at';" 2>/dev/null || true
	@sqlite3 "$(DB_PATH)" "SELECT 'rfc_index_fetched_at: ' || value FROM meta WHERE key = 'rfc_index_fetched_at';" 2>/dev/null || true

# Run Go tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf "$(BIN_DIR)"
	rm -f "$(DB_PATH)"
