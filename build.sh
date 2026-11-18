#!/bin/bash
set -e

cd "$(dirname "$0")"

DIST="dist"
rm -rf "$DIST"
mkdir -p "$DIST"

LDFLAGS="-s -w"

# Fyne needs CGO, and macOS binaries can only be built on macOS.
if [ "$(uname -s)" = "Darwin" ]; then
  echo "=== Building macOS arm64 ==="
  CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
    go build -ldflags "$LDFLAGS" -o "$DIST/mywhoosh2garmin-darwin-arm64" .
else
  echo "=== Building Linux amd64 ==="
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -ldflags "$LDFLAGS" -o "$DIST/mywhoosh2garmin-linux-amd64" .

  echo "=== Building Windows amd64 ==="
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
    go build -ldflags "-s -w -H=windowsgui" -o "$DIST/mywhoosh2garmin-windows-amd64.exe" .
fi

echo ""
ls -lh "$DIST"/
echo ""
echo "Done ✓"
