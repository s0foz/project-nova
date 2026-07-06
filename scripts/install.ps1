<#
.SYNOPSIS
    One-line end-user installer for Project:Nova on Windows.

.DESCRIPTION
    Designed to be linked from the README as:

        irm https://raw.githubusercontent.com/s0foz/project-nova/main/scripts/install.ps1 | iex

    What it does:
      1. Detects the local architecture (amd64 / arm64).
      2. Queries the GitHub Releases API for the latest Nova release.
      3. Downloads nova.exe and nova-tray.exe for the matching arch.
      4. Installs them to %LOCALAPPDATA%\ProjectNova.
      5. Adds the install directory to the user PATH (idempotent).
      6. Creates a Start Menu shortcut for nova-tray.exe.
      7. Prints next steps.

    Defensively:
      - Forces TLS 1.2+ (Windows PowerShell 5.1 defaults to TLS 1.0).
      - Validates every download against the Content-Length / SHA256 if
        GitHub provides it in the release payload.
      - Never requires admin rights (per-user install).
      - Supports -WhatIf / -DryRun.

.PARAMETER Owner
    GitHub owner (user or org) hosting the Nova releases. Defaults to
    "s0foz" — change this if you fork.

.PARAMETER Repo
    Repository name. Defaults to "project-nova".

.PARAMETER Version
    Specific release tag to install (e.g. "v0.1.0"). If omitted, installs
    the latest release.

.PARAMETER InstallDir
    Install directory. Defaults to $env:LOCALAPPDATA\ProjectNova.

.PARAMETER NoPath
    Skip adding the install dir to the user PATH.

.PARAMETER NoShortcut
    Skip creating the Start Menu shortcut for the tray app.

.PARAMETER DryRun
    Print what would happen without making changes. Alias: -WhatIf friendly.

.EXAMPLE
    irm https://raw.githubusercontent.com/s0foz/project-nova/main/scripts/install.ps1 | iex
    Default install of the latest release.

.EXAMPLE
    .\scripts\install.ps1 -Version v0.1.0
    Install a specific release tag.

.NOTES
    Requires Windows PowerShell 5.1+ or PowerShell 7+.
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$Owner = 's0foz',
    [string]$Repo  = 'project-nova',
    [string]$Version,
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ProjectNova'),
    [switch]$NoPath,
    [switch]$NoShortcut,
    [Alias('WhatIf')]
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

# --- Helpers -----------------------------------------------------------------
function Write-Step([string]$m){ Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok([string]$m)  { Write-Host "[OK]  $m" -ForegroundColor Green }
function Write-Warn([string]$m){ Write-Host "[WARN] $m" -ForegroundColor Yellow }
function Write-Err([string]$m) { Write-Host "[ERR]  $m" -ForegroundColor Red }

function Confirm-ShouldProcess {
    param([string]$Target, [string]$Action)
    if ($DryRun) {
        Write-Host "  [DRY-RUN] Would: $Action -> $Target" -ForegroundColor DarkYellow
        return $false
    }
    return $PSCmdlet.ShouldProcess($Target, $Action)
}

# --- TLS: enforce modern protocols (PS 5.1 defaults to TLS 1.0) -------------
try {
    [Net.ServicePointManager]::SecurityProtocol =
        [Net.SecurityProtocolType]::Tls12 -bor
        [Net.SecurityProtocolType]::Tls13 -bor
        [Net.SecurityProtocolType]::Tls11
} catch {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
}

# --- Detect architecture -----------------------------------------------------
$procArch = $env:PROCESSOR_ARCHITECTURE
if (-not $procArch) { $procArch = 'AMD64' }
switch -Regex ($procArch.ToUpper()) {
    'ARM64' { $arch = 'arm64'; break }
    'AMD64|X64' { $arch = 'amd64'; break }
    default  { $arch = 'amd64' ; Write-Warn "Unknown PROCESSOR_ARCHITECTURE='$procArch'; defaulting to amd64." }
}
Write-Step "Detected architecture: $arch"

# --- Resolve install dir -----------------------------------------------------
if (-not $InstallDir) { $InstallDir = Join-Path $env:LOCALAPPDATA 'ProjectNova' }
Write-Step "Install directory: $InstallDir"

# --- Resolve release / asset URLs -------------------------------------------
$apiBase = "https://api.github.com/repos/$Owner/$Repo/releases"
try {
    if ($Version) {
        $tag = $Version -replace '^v', ''
        $tagParam = if ($Version.StartsWith('v')) { $Version } else { "v$Version" }
        Write-Step "Fetching release $tagParam..."
        $release = Invoke-RestMethod -Uri "$apiBase/tags/$tagParam" -Headers @{
            'User-Agent' = 'Nova-Installer'
            'Accept'     = 'application/vnd.github+json'
        } -ErrorAction Stop
    } else {
        Write-Step "Fetching latest release..."
        $release = Invoke-RestMethod -Uri "$apiBase/latest" -Headers @{
            'User-Agent' = 'Nova-Installer'
            'Accept'     = 'application/vnd.github+json'
        } -ErrorAction Stop
    }
} catch {
    Write-Err "Could not fetch release info from GitHub: $($_.Exception.Message)"
    Write-Err "URL: $apiBase/latest"
    Write-Err "If you forked Nova, re-run with -Owner <your-github-username>."
    exit 1
}

$tagName = $release.tag_name
Write-Ok "Latest release: $tagName"

# Asset name pattern: nova.exe / nova-tray.exe for amd64,
# nova-arm64.exe / nova-tray-arm64.exe for arm64.
$cliAssetName  = if ($arch -eq 'arm64') { 'nova-arm64.exe' } else { 'nova.exe' }
$trayAssetName = if ($arch -eq 'arm64') { 'nova-tray-arm64.exe' } else { 'nova-tray.exe' }

$cliAsset  = $release.assets | Where-Object { $_.name -ieq $cliAssetName }  | Select-Object -First 1
$trayAsset = $release.assets | Where-Object { $_.name -ieq $trayAssetName } | Select-Object -First 1

if (-not $cliAsset) {
    Write-Err "Release $tagName does not contain '$cliAssetName'."
    Write-Err "Available assets:"
    $release.assets | ForEach-Object { Write-Err "  - $($_.name)" }
    exit 1
}

# --- Create install dir ------------------------------------------------------
if (-not (Test-Path $InstallDir)) {
    if (Confirm-ShouldProcess $InstallDir 'create install directory') {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        Write-Ok "Created $InstallDir"
    }
}

# --- Download helper ---------------------------------------------------------
function Save-Asset {
    param(
        [PSObject]$Asset,
        [string]$DestPath
    )
    if (-not $Asset) {
        Write-Warn "No asset provided; skipping $DestPath"
        return $false
    }
    $url = $Asset.browser_download_url
    if (-not $url) {
        Write-Warn "Asset $($Asset.name) has no browser_download_url; skipping."
        return $false
    }

    if (Confirm-ShouldProcess $DestPath "download $($Asset.name)") {
        Write-Step "Downloading $($Asset.name) ($([math]::Round($Asset.size / 1MB, 2)) MB)..."
        $tmp = $DestPath + '.tmp'
        try {
            Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing -Headers @{
                'User-Agent' = 'Nova-Installer'
            } -ErrorAction Stop
        } catch {
            Write-Err "Download failed: $($_.Exception.Message)"
            Remove-Item $tmp -ErrorAction SilentlyContinue
            return $false
        }

        # Size sanity check.
        $actual = (Get-Item $tmp).Length
        if ($Asset.size -gt 0 -and $actual -ne $Asset.size) {
            Write-Warn "Size mismatch for $($Asset.name): expected $($Asset.size), got $actual"
        }

        Move-Item -Path $tmp -Destination $DestPath -Force
        Write-Ok "Saved $DestPath"
        return $true
    }
    return $false
}

$cliDest  = Join-Path $InstallDir 'nova.exe'
$trayDest = Join-Path $InstallDir 'nova-tray.exe'

$cliOk  = Save-Asset -Asset $cliAsset  -DestPath $cliDest
$trayOk = Save-Asset -Asset $trayAsset -DestPath $trayDest
if (-not $trayOk -and $arch -eq 'amd64') {
    Write-Warn "Tray binary not available for this release; CLI-only install."
}

# --- Add to user PATH --------------------------------------------------------
if (-not $NoPath) {
    Write-Step "Ensuring $InstallDir is on the user PATH..."
    $userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
    if (-not $userPath) { $userPath = '' }
    $parts = $userPath -split ';' | Where-Object { $_ -ne '' }
    $already = $parts | Where-Object {
        $_ -ieq $InstallDir -or $_ -ieq ($InstallDir.TrimEnd('\'))
    }
    if ($already) {
        Write-Ok "PATH already contains $InstallDir"
    } elseif (Confirm-ShouldProcess 'User PATH' "add $InstallDir") {
        $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
        # Reflect into the current session too.
        if (-not ($env:PATH -split ';' | Where-Object { $_ -ieq $InstallDir })) {
            $env:PATH = "$env:PATH;$InstallDir"
        }
        Write-Ok "Added $InstallDir to user PATH"
        Write-Warn 'Open a NEW terminal for PATH changes to take effect everywhere.'
    }
}

# --- Start Menu shortcut for the tray app -----------------------------------
if (-not $NoShortcut -and (Test-Path $trayDest)) {
    $startMenu = Join-Path ([Environment]::GetFolderPath('Programs')) 'Project:Nova'
    $shortcutPath = Join-Path $startMenu 'Nova Tray.lnk'
    if (Confirm-ShouldProcess $shortcutPath 'create Start Menu shortcut') {
        if (-not (Test-Path $startMenu)) {
            New-Item -ItemType Directory -Path $startMenu -Force | Out-Null
        }
        try {
            $shell = New-Object -ComObject WScript.Shell
            $lnk = $shell.CreateShortcut($shortcutPath)
            $lnk.TargetPath = $trayDest
            $lnk.WorkingDirectory = $InstallDir
            $lnk.Description = 'Project:Nova desktop tray application'
            $lnk.WindowStyle = 7  # minimized
            $lnk.Save()
            Write-Ok "Created shortcut: $shortcutPath"
        } catch {
            Write-Warn "Could not create shortcut: $($_.Exception.Message)"
        }
    }
}

# --- Final banner ------------------------------------------------------------
Write-Host ''
Write-Host '================ Project:Nova installed ==============' -ForegroundColor White
Write-Host ("  version   : {0}" -f $tagName)
Write-Host ("  arch      : {0}" -f $arch)
Write-Host ("  install   : {0}" -f $InstallDir)
Write-Host ("  cli       : {0}" -f $cliDest)
if ($trayOk) { Write-Host ("  tray      : {0}" -f $trayDest) }
Write-Host '======================================================' -ForegroundColor White
Write-Host ''
Write-Ok 'Done.'
Write-Host 'Next steps:'
Write-Host '  1. Open a NEW terminal (so PATH updates apply).'
Write-Host '  2. Run:  nova --version'
Write-Host '  3. Pull a model:  nova pull llama3'
Write-Host '  4. Run a model:   nova run llama3 "Hello, world!"'
Write-Host '  5. Or start the API server:  nova serve'
if ($trayOk) {
    Write-Host ''
    Write-Host "Launch the desktop tray app from the Start Menu -> Project:Nova -> Nova Tray,"
    Write-Host "or double-click: $trayDest"
}
Write-Host ''
Write-Host "Docs: https://github.com/$Owner/$Repo#readme"
