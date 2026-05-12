# friday Windows installer.
#
# Usage:
#   iwr -useb https://raw.githubusercontent.com/zhivko-kocev/friday/master/install.ps1 | iex
#
# Override install dir with $env:FRIDAY_INSTALL_DIR before piping to iex.

$ErrorActionPreference = 'Stop'

$Repo       = 'zhivko-kocev/friday'
$BinName    = 'friday.exe'
$InstallDir = if ($env:FRIDAY_INSTALL_DIR) { $env:FRIDAY_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\friday' }

# Architecture: goreleaser ships windows/amd64 only (windows/arm64 is ignored).
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' {
        Write-Host 'error: windows/arm64 is not published as a release archive.' -ForegroundColor Red
        Write-Host '       try: go install github.com/zhivko-kocev/friday/cmd/friday@latest'
        exit 1
    }
    default {
        Write-Host "error: unsupported architecture $env:PROCESSOR_ARCHITECTURE" -ForegroundColor Red
        exit 1
    }
}

# Resolve latest release tag via GitHub's redirect (no auth, no rate-limit
# concerns from anonymous API calls in CI).
$latest = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
if (-not $latest) {
    Write-Host 'error: could not determine latest release' -ForegroundColor Red
    exit 1
}

$archive = "friday_windows_${arch}.zip"
$url     = "https://github.com/$Repo/releases/download/$latest/$archive"

$tmp = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "friday-install-$([guid]::NewGuid())")
try {
    Write-Host "  downloading friday $latest (windows/$arch)..."
    $zipPath = Join-Path $tmp $archive
    Invoke-WebRequest -Uri $url -OutFile $zipPath -UseBasicParsing

    Expand-Archive -Path $zipPath -DestinationPath $tmp -Force
    $src = Join-Path $tmp $BinName
    if (-not (Test-Path $src)) {
        Write-Host "error: $BinName not found in archive" -ForegroundColor Red
        exit 1
    }

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }
    Move-Item -Path $src -Destination (Join-Path $InstallDir $BinName) -Force

    Write-Host ""
    Write-Host "  friday $latest installed to $InstallDir\$BinName" -ForegroundColor Green

    # Path warning — manipulating PATH automatically across shells is brittle.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not ($userPath -split ';' | Where-Object { $_ -ieq $InstallDir })) {
        Write-Host ""
        Write-Host "  $InstallDir is not on your user PATH." -ForegroundColor Yellow
        Write-Host "  Add it once with:"
        Write-Host "    [Environment]::SetEnvironmentVariable('Path', `"`$([Environment]::GetEnvironmentVariable('Path','User'));$InstallDir`", 'User')"
        Write-Host "  Then restart your shell."
    }

    Write-Host ""
    Write-Host "  next steps:"
    Write-Host "    friday init                                  # scaffold a store, or clone an existing config repo"
    Write-Host "    friday push                                  # apply config to ~/.claude, ~/.codex, etc."
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
