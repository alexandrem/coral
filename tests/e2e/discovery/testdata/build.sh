#!/bin/bash
# Build test applications with different configurations
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUTPUT_DIR="$SCRIPT_DIR/bin"

mkdir -p "$OUTPUT_DIR"

echo "Building test applications..."

# App with SDK + DWARF symbols
echo "  [1/4] Building app_with_sdk (with DWARF symbols)..."
go build -o "$OUTPUT_DIR/app_with_sdk_dwarf" "$SCRIPT_DIR/app_with_sdk.go"

# App with SDK + NO DWARF symbols (stripped)
echo "  [2/4] Building app_with_sdk (stripped)..."
go build -ldflags="-w -s" -o "$OUTPUT_DIR/app_with_sdk_stripped" "$SCRIPT_DIR/app_with_sdk.go"

# App WITHOUT SDK + DWARF symbols
echo "  [3/4] Building app_no_sdk (with DWARF symbols)..."
go build -o "$OUTPUT_DIR/app_no_sdk_dwarf" "$SCRIPT_DIR/app_no_sdk.go"

# App WITHOUT SDK + NO DWARF symbols (stripped)
echo "  [4/4] Building app_no_sdk (stripped)..."
go build -ldflags="-w -s" -o "$OUTPUT_DIR/app_no_sdk_stripped" "$SCRIPT_DIR/app_no_sdk.go"

echo "✓ All test binaries built successfully:"
ls -lh "$OUTPUT_DIR"
