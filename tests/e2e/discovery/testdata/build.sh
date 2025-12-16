#!/bin/bash
# Build test applications with different configurations
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$SCRIPT_DIR/bin"

mkdir -p "$OUTPUT_DIR"

echo "Building test applications..."

# App with SDK + DWARF symbols
echo "  [1/6] Building app_with_sdk (with DWARF symbols)..."
go build -o "$OUTPUT_DIR/app_with_sdk_dwarf" "$SCRIPT_DIR/app_with_sdk.go"

# App with SDK + symbol table only (DWARF stripped, symbols intact)
echo "  [2/6] Building app_with_sdk (symbol table only, -w)..."
go build -ldflags="-w" -o "$OUTPUT_DIR/app_with_sdk_symtab_only" "$SCRIPT_DIR/app_with_sdk.go"

# App with SDK + fully stripped (no DWARF, no symbols)
echo "  [3/6] Building app_with_sdk (fully stripped, -w -s)..."
go build -ldflags="-w -s" -o "$OUTPUT_DIR/app_with_sdk_stripped" "$SCRIPT_DIR/app_with_sdk.go"

# App WITHOUT SDK + DWARF symbols
echo "  [4/6] Building app_no_sdk (with DWARF symbols)..."
go build -o "$OUTPUT_DIR/app_no_sdk_dwarf" "$SCRIPT_DIR/app_no_sdk.go"

# App WITHOUT SDK + symbol table only (DWARF stripped, symbols intact)
echo "  [5/6] Building app_no_sdk (symbol table only, -w)..."
go build -ldflags="-w" -o "$OUTPUT_DIR/app_no_sdk_symtab_only" "$SCRIPT_DIR/app_no_sdk.go"

# App WITHOUT SDK + fully stripped (no DWARF, no symbols)
echo "  [6/6] Building app_no_sdk (fully stripped, -w -s)..."
go build -ldflags="-w -s" -o "$OUTPUT_DIR/app_no_sdk_stripped" "$SCRIPT_DIR/app_no_sdk.go"

echo "✓ All test binaries built successfully:"
ls -lh "$OUTPUT_DIR"
