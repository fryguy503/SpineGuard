#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v go >/dev/null || { echo 'Install Go 1.26 or later.' >&2; exit 1; }
if ! command -v wails >/dev/null; then
  go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
  export PATH="$(go env GOPATH)/bin:$PATH"
fi
go test ./...
go vet ./...
go run ./scripts/icon
arch="$(go env GOARCH)"
wails build -clean -platform "darwin/$arch" -trimpath
go build -trimpath -ldflags '-s -w' -o build/bin/spineguard-cli ./cmd/spineguard-cli
codesign --force --deep --sign - build/bin/SpineGuard.app
ditto -c -k --sequesterRsrc --keepParent build/bin/SpineGuard.app "build/bin/SpineGuard-macos-$arch.zip"
echo "Built app bundle and ZIP in build/bin. Public distribution requires Developer ID signing and notarization."
