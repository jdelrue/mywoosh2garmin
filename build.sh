#!/bin/bash
set -e

cd "$(dirname "$0")"

DIST="dist"
rm -rf "$DIST"
mkdir -p "$DIST"

LDFLAGS="-s -w"

echo "=== Building Linux amd64 ==="
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  go build -ldflags "$LDFLAGS" -o "$DIST/mywhoosh2garmin-linux-amd64" .

echo "=== Building Windows amd64 ==="
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -ldflags "-s -w -H=windowsgui" -o "$DIST/mywhoosh2garmin-windows-amd64.exe" .

echo ""
ls -lh "$DIST"/
echo ""
echo "Done ✓"
