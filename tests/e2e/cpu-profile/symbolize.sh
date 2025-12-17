#!/usr/bin/env bash
# Symbolize CPU profile addresses using addr2line
#
# Usage: ./symbolize.sh <binary> <address1> [address2 ...]
#
# Example:
#   bin/coral debug cpu-profile -s localhost -d 5 > profile.txt
#   # Extract first address from a stack trace
#   ./symbolize.sh /app/cpu-app 0x8a454

set -e

BINARY="$1"
shift

if [ ! -f "$BINARY" ]; then
    echo "Error: Binary not found: $BINARY"
    exit 1
fi

echo "Symbolizing addresses from: $BINARY"
echo ""

for ADDR in "$@"; do
    echo "Address: $ADDR"
    go tool addr2line -e "$BINARY" "$ADDR" 2>/dev/null || \
        addr2line -e "$BINARY" "$ADDR" 2>/dev/null || \
        echo "  (unable to symbolize)"
    echo ""
done
