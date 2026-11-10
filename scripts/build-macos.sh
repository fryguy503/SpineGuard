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
arch="${1:-$(go env GOARCH)}"
case "$arch" in amd64|arm64|universal) ;; *) echo 'Choose amd64, arm64, or universal.' >&2; exit 1 ;; esac
export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=13.0"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=13.0"
wails build -clean -platform "darwin/$arch" -o SpineGuard -trimpath
if [[ "$arch" == universal ]]; then
  for slice in amd64 arm64; do
    CGO_ENABLED=0 GOOS=darwin GOARCH="$slice" go build -trimpath -ldflags '-s -w' -o "build/bin/spineguard-cli-$slice" ./cmd/spineguard-cli
  done
  lipo -create -output build/bin/spineguard-cli build/bin/spineguard-cli-amd64 build/bin/spineguard-cli-arm64
  rm -f build/bin/spineguard-cli-amd64 build/bin/spineguard-cli-arm64
  lipo build/bin/SpineGuard.app/Contents/MacOS/SpineGuard -verify_arch x86_64 arm64
  lipo build/bin/spineguard-cli -verify_arch x86_64 arm64
  file build/bin/SpineGuard.app/Contents/MacOS/SpineGuard build/bin/spineguard-cli
else
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -trimpath -ldflags '-s -w' -o build/bin/spineguard-cli ./cmd/spineguard-cli
fi
codesign --force --deep --sign - build/bin/SpineGuard.app
codesign --force --sign - build/bin/spineguard-cli
codesign --verify --deep --strict build/bin/SpineGuard.app
codesign --verify --strict build/bin/spineguard-cli
if [[ "$arch" == universal || "$arch" == "$(go env GOHOSTARCH)" ]]; then
  build/bin/spineguard-cli version
fi
ditto -c -k --sequesterRsrc --keepParent build/bin/SpineGuard.app "build/bin/SpineGuard-macos-$arch.zip"
echo "Built app bundle and ZIP in build/bin. Public distribution requires Developer ID signing and notarization."
