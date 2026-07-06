<#
.SYNOPSIS
    Build Project:Nova Windows binaries locally.

.DESCRIPTION
    Mirrors the GitHub Actions Windows build (see .github/workflows/build-windows.yml)
    so local developers on Windows can produce identical binaries.

    Produces in .\dist\:
      - nova.exe            (amd64, console subsystem)        always
      - nova-tray.exe       (amd64, GUI subsystem, no console) when -Tray
      - nova-arm64.exe      (arm64, console subsystem)        when -Arch arm64
      - nova-tray-arm64.exe (arm64, GUI subsystem)             when -Arch arm64 -Tray

    Version metadata is injected via -ldflags into
    github.com/project-nova/nova/internal/version.

.PARAMETER Version
    Version string to embed (e.g. "0.1.0"). If omitted, computed from
    `git describe --tags --always --dirty`, falling back to "0.0.0-dev".

.PARAMETER Arch
    Target GOARCH. Defaults to "amd64". Set to "arm64" for Windows-on-ARM.

.PARAMETER Tray
    Switch. When set, also builds the GUI-subsystem tray binary
    (nova-tray.exe or nova-tray-arm64.exe).

.PARAMETER OutputDir
    Output directory. Defaults to ".\dist".

.PARAMETER SkipVet
    Switch. Skip `go vet ./...` before building.

.EXAMPLE
    .\scripts\build-windows.ps1
    Builds dist\nova.exe for amd64 with version from git.

.EXAMPLE
    .\scripts\build-windows.ps1 -Tray -Version 0.1.0
    Builds dist\nova.exe and dist\nova-tray.exe tagged 0.1.0.

.EXAMPLE
    .\scripts\build-windows.ps1 -Arch arm64 -Tray
    Builds arm64 console + tray binaries for Windows-on-ARM.

.NOTES
    Requires Go 1.22+ on PATH (`go version`). Run from the repo root.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [switch]$Tray,
    [string]$OutputDir = (Join-Path $PSScriptRoot '..\dist'),
    [switch]$SkipVet
)

$ErrorActionPreference = 'Stop'
$VerbosePreference = 'Continue'

# --- Helper: write coloured status lines -------------------------------------
function Write-Step([string]$msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok([string]$msg)   { Write-Host "[OK]  $msg" -ForegroundColor Green }
function Write-Warn([string]$msg) { Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Write-Err([string]$msg)  { Write-Host "[ERR]  $msg" -ForegroundColor Red }

# --- Resolve repo root (parent of scripts/) ---------------------------------
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location -Path $RepoRoot
Write-Step "Repo root: $RepoRoot"

# --- Preflight: Go -----------------------------------------------------------
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    Write-Err "Go was not found on PATH. Install Go 1.22+ from https://go.dev/dl/."
    exit 1
}
$goVersion = (& go version)
Write-Step "Using $($goVersion)"

# --- Compute version metadata ------------------------------------------------
if (-not $Version) {
    Write-Step "Computing version from git describe..."
    & git -C $RepoRoot rev-parse --is-inside-work-tree 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) {
        $Version = (& git -C $RepoRoot describe --tags --always --dirty 2>$null)
        if (-not $Version) { $Version = '0.0.0-dev' }
    } else {
        $Version = '0.0.0-dev'
    }
}
$Commit = 'unknown'
try {
    $Commit = (& git -C $RepoRoot rev-parse --short HEAD 2>$null)
    if (-not $Commit) { $Commit = 'unknown' }
} catch { $Commit = 'unknown' }

# Build date in ISO-8601 UTC (PowerShell 5.1 compatible).
$BuildDate = ([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ'))

Write-Step "Version    : $Version"
Write-Step "Commit     : $Commit"
Write-Step "Build date : $BuildDate"
Write-Step "Arch       : $Arch"
Write-Step "Tray build : $Tray"

# --- Prepare output dir ------------------------------------------------------
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir -Force | Out-Null
}
$OutputDir = (Resolve-Path $OutputDir).Path
Write-Step "Output dir : $OutputDir"

# --- Module cache ------------------------------------------------------------
Write-Step "Downloading Go module dependencies..."
& go mod download
if ($LASTEXITCODE -ne 0) { Write-Err "go mod download failed"; exit $LASTEXITCODE }

# --- go vet ------------------------------------------------------------------
if (-not $SkipVet) {
    Write-Step "Running go vet ./..."
    & go vet ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "go vet reported issues (continuing; CI treats vet as fatal)."
    } else {
        Write-Ok "go vet clean"
    }
}

# --- ldflags builder ---------------------------------------------------------
function New-LdFlags {
    param(
        [string]$Version,
        [string]$Commit,
        [string]$BuildDate,
        [switch]$Gui
    )
    $base = @(
        '-s', '-w',
        "-X github.com/project-nova/nova/internal/version.Version=$Version",
        "-X github.com/project-nova/nova/internal/version.Commit=$Commit",
        "-X github.com/project-nova/nova/internal/version.BuildDate=$BuildDate"
    )
    if ($Gui) { $base += '-H=windowsgui' }
    return ($base -join ' ')
}

# --- Build one binary --------------------------------------------------------
function Invoke-GoBuild {
    param(
        [string]$OutName,
        [string]$Arch,
        [switch]$Gui
    )
    $outPath = Join-Path $OutputDir $OutName
    $ldflags = New-LdFlags -Version $Version -Commit $Commit -BuildDate $BuildDate -Gui:$Gui

    Write-Step "Building $OutName (GOARCH=$Arch, GUI=$([bool]$Gui))..."
    Write-Verbose "  ldflags: $ldflags"

    $env:GOOS = 'windows'
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'

    & go build -trimpath -ldflags $ldflags -o $outPath ./cmd/nova
    if ($LASTEXITCODE -ne 0) {
        Write-Err "Build failed for $OutName"
        exit $LASTEXITCODE
    }

    $info = Get-Item $outPath
    Write-Ok ("{0,-22} {1,12:N0} bytes" -f $OutName, $info.Length)
}

# --- Build requested binaries ------------------------------------------------
$built = @()

# Console (CLI) binary — always built.
$consoleName = if ($Arch -eq 'arm64') { 'nova-arm64.exe' } else { 'nova.exe' }
Invoke-GoBuild -OutName $consoleName -Arch $Arch
$built += $consoleName

# Tray (GUI subsystem) binary — only when -Tray is passed.
if ($Tray) {
    $trayName = if ($Arch -eq 'arm64') { 'nova-tray-arm64.exe' } else { 'nova-tray.exe' }
    Invoke-GoBuild -OutName $trayName -Arch $Arch -Gui
    $built += $trayName
}

# --- Sanity check: --version should report the embedded version --------------
Write-Step "Verifying nova --version..."
$probe = Join-Path $OutputDir $consoleName
$probeOut = & $probe --version 2>&1
Write-Host "  $($probeOut)"

# --- Summary -----------------------------------------------------------------
Write-Host ''
Write-Host '================ Nova build summary ================' -ForegroundColor White
Write-Host ("  version : {0}" -f $Version)
Write-Host ("  commit  : {0}" -f $Commit)
Write-Host ("  arch    : {0}" -f $Arch)
Write-Host ("  output  : {0}" -f $OutputDir)
Write-Host '  binaries:'
foreach ($b in $built) {
    $p = Join-Path $OutputDir $b
    $sz = (Get-Item $p).Length
    Write-Host ("    {0,-22} {1,12:N0} bytes" -f $b, $sz)
}
Write-Host '====================================================' -ForegroundColor White
Write-Host ''
Write-Ok 'Build complete.'
Write-Host 'Next steps:'
Write-Host "  - Run the server:    $OutputDir\$consoleName serve"
if ($Tray) {
    $trayName = if ($Arch -eq 'arm64') { 'nova-tray-arm64.exe' } else { 'nova-tray.exe' }
    Write-Host "  - Launch the tray:   $OutputDir\$trayName"
}
Write-Host "  - Package an MSI:    powershell -File $PSScriptRoot\build-msi.ps1 -Version $Version"
