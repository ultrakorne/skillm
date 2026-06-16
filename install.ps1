# install.ps1 — download and install the skillm binary on Windows.
#
# Usage:
#   irm https://raw.githubusercontent.com/ultrakorne/skillm/main/install.ps1 | iex
#
# Honours:
#   $env:SKILLM_VERSION   pin a release tag (default: latest)
#   $env:SKILLM_BIN_DIR   override install dir (default: %LOCALAPPDATA%\skillm)

$ErrorActionPreference = 'Stop'

$repo   = 'ultrakorne/skillm'
$binary = 'skillm'

# Detect architecture.
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default  { throw "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE). skillm supports amd64 and arm64." }
}

# Resolve the version tag.
$version = $env:SKILLM_VERSION
if (-not $version) {
    Write-Host 'Resolving latest release...' -ForegroundColor Cyan
    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name
    if (-not $version) {
        throw 'Could not determine the latest release tag. Set $env:SKILLM_VERSION to pin a version.'
    }
}

$verNoV = $version.TrimStart('v')
$asset  = "${binary}_${verNoV}_windows_${arch}.zip"
$url    = "https://github.com/$repo/releases/download/$version/$asset"

Write-Host "Downloading $binary $version (windows/$arch)..." -ForegroundColor Cyan

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    $zipPath = Join-Path $tmp $asset
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing

    Expand-Archive -Path $zipPath -DestinationPath $tmp -Force

    $exePath = Join-Path $tmp "$binary.exe"
    if (-not (Test-Path $exePath)) {
        throw "Archive did not contain $binary.exe"
    }

    # Choose install directory.
    $bindir = $env:SKILLM_BIN_DIR
    if (-not $bindir) {
        $bindir = Join-Path $env:LOCALAPPDATA 'skillm'
    }

    if (-not (Test-Path $bindir)) {
        New-Item -ItemType Directory -Path $bindir | Out-Null
    }

    $dest = Join-Path $bindir "$binary.exe"
    Copy-Item -Path $exePath -Destination $dest -Force

    Write-Host "Installed $binary to $dest" -ForegroundColor Green

    # Warn if the install dir is not on PATH, and offer to add it.
    $userPath = [System.Environment]::GetEnvironmentVariable('PATH', 'User')
    if ($userPath -notlike "*$bindir*") {
        Write-Host ''
        Write-Host "Note: $bindir is not on your PATH." -ForegroundColor Yellow
        Write-Host 'Add it permanently with:' -ForegroundColor Yellow
        Write-Host "  [System.Environment]::SetEnvironmentVariable('PATH', `"`$env:PATH;$bindir`", 'User')" -ForegroundColor Yellow
        Write-Host 'Then restart your terminal.' -ForegroundColor Yellow
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
