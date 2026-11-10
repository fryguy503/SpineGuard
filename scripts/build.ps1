param([switch]$Installer)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.26 or later and add go to PATH.' }
$wails = Get-Command wails -ErrorAction SilentlyContinue
if (-not $wails) {
    go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.0
    if ($LASTEXITCODE -ne 0) { throw 'Could not install Wails.' }
    $wailsPath = Join-Path ((go env GOPATH).Trim()) 'bin\wails.exe'
} else { $wailsPath = $wails.Source }
go test ./...
if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
go vet ./...
if ($LASTEXITCODE -ne 0) { throw 'Go vet failed.' }
go run ./scripts/icon
if ($LASTEXITCODE -ne 0) { throw 'Icon generation failed.' }
$buildArgs = @('build', '-clean', '-platform', 'windows/amd64', '-webview2', 'browser', '-trimpath')
if ($Installer) { $buildArgs += '-nsis' }
& $wailsPath @buildArgs
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed.' }
go build -trimpath -ldflags '-s -w' -o build/bin/spineguard-cli.exe ./cmd/spineguard-cli
if ($LASTEXITCODE -ne 0) { throw 'CLI build failed.' }
Compress-Archive -LiteralPath @('build/bin/SpineGuard.exe', 'build/bin/spineguard-cli.exe', 'README.md', 'LICENSE') -DestinationPath 'build/bin/SpineGuard-windows-amd64.zip' -Force
Write-Host 'Built SpineGuard in build/bin.'
