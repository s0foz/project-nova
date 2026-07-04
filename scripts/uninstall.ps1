<#
.SYNOPSIS
    Uninstall Project:Nova from the current user's machine.

.DESCRIPTION
    Removes:
      - The install directory (default: %LOCALAPPDATA%\ProjectNova)
      - The Nova entry on the user PATH
      - The Start Menu folder/shortcut for the tray app
      - The NOVA_HOME environment variable, if set

    Idempotent: running it twice (or on a machine that never had Nova
    installed) succeeds silently.

.PARAMETER InstallDir
    Override the install directory if Nova was installed somewhere else.

.PARAMETER KeepModels
    Keep the user's model store (%USERPROFILE%\.nova). By default, the
    script leaves models untouched — pass -RemoveModels to delete them.

.PARAMETER RemoveModels
    Also delete %USERPROFILE%\.nova (all downloaded models). Use with care.

.EXAMPLE
    .\scripts\uninstall.ps1
    Remove Nova binaries, PATH entry, and shortcuts.

.EXAMPLE
    .\scripts\uninstall.ps1 -RemoveModels
    Also delete the local model store (~\.nova).

.NOTES
    This script does NOT uninstall an MSI-based install. If you installed
    Nova via the MSI, run:
        msiexec /x {<ProductCode>}
    or use Settings -> Apps -> Project:Nova -> Uninstall.
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'ProjectNova'),
    [switch]$KeepModels,
    [switch]$RemoveModels
)

$ErrorActionPreference = 'Stop'

# --- Helpers -----------------------------------------------------------------
function Write-Step([string]$m){ Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok([string]$m)  { Write-Host "[OK]  $m" -ForegroundColor Green }
function Write-Warn([string]$m){ Write-Host "[WARN] $m" -ForegroundColor Yellow }
function Write-Err([string]$m) { Write-Host "[ERR]  $m" -ForegroundColor Red }

# --- Stop any running nova process (best-effort) ----------------------------
$procs = Get-Process -Name 'nova','nova-tray','nova-arm64','nova-tray-arm64' -ErrorAction SilentlyContinue
if ($procs) {
    Write-Step "Stopping running Nova process(es)..."
    foreach ($p in $procs) {
        try {
            Write-Warn "Stopping $($p.Name) (PID $($p.Id))..."
            $p | Stop-Process -Force -ErrorAction Stop
        } catch {
            Write-Warn "Could not stop $($p.Name) (PID $($p.Id)): $($_.Exception.Message)"
        }
    }
    Start-Sleep -Seconds 1
}

# --- Remove install directory -----------------------------------------------
if (Test-Path $InstallDir) {
    Write-Step "Removing install directory: $InstallDir"
    if ($PSCmdlet.ShouldProcess($InstallDir, 'delete install directory')) {
        try {
            Remove-Item -Path $InstallDir -Recurse -Force -ErrorAction Stop
            Write-Ok "Removed $InstallDir"
        } catch {
            Write-Err "Could not remove $InstallDir : $($_.Exception.Message)"
            Write-Err 'Stop any running nova.exe / nova-tray.exe and try again.'
        }
    }
} else {
    Write-Ok "Install directory not present (already removed): $InstallDir"
}

# --- Remove from user PATH ---------------------------------------------------
Write-Step "Cleaning user PATH..."
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if ($userPath) {
    $parts = $userPath -split ';' | Where-Object { $_ -ne '' }
    $kept  = $parts | Where-Object {
        $_ -ine $InstallDir -and
        $_ -ine ($InstallDir.TrimEnd('\'))
    }
    if ($kept.Count -ne $parts.Count) {
        $newPath = ($kept -join ';')
        if ($PSCmdlet.ShouldProcess('User PATH', 'remove Nova entry')) {
            [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
            Write-Ok 'Removed Nova entry from user PATH'
        }
    } else {
        Write-Ok 'PATH did not contain the Nova install directory'
    }
}

# --- Remove NOVA_HOME environment variable ----------------------------------
$existing = [Environment]::GetEnvironmentVariable('NOVA_HOME', 'User')
if ($existing) {
    if ($PSCmdlet.ShouldProcess('User env:NOVA_HOME', 'remove')) {
        [Environment]::SetEnvironmentVariable('NOVA_HOME', $null, 'User')
        Write-Ok 'Removed NOVA_HOME from user environment'
    }
}

# --- Remove Start Menu shortcut ---------------------------------------------
$startMenu = Join-Path ([Environment]::GetFolderPath('Programs')) 'Project:Nova'
if (Test-Path $startMenu) {
    if ($PSCmdlet.ShouldProcess($startMenu, 'delete Start Menu folder')) {
        try {
            Remove-Item -Path $startMenu -Recurse -Force -ErrorAction Stop
            Write-Ok "Removed Start Menu folder: $startMenu"
        } catch {
            Write-Warn "Could not remove $startMenu : $($_.Exception.Message)"
        }
    }
} else {
    Write-Ok "No Start Menu folder present: $startMenu"
}

# --- Optionally remove models ------------------------------------------------
$modelsDir = Join-Path $env:USERPROFILE '.nova'
if ($RemoveModels -and -not $KeepModels) {
    if (Test-Path $modelsDir) {
        if ($PSCmdlet.ShouldProcess($modelsDir, 'delete model store')) {
            try {
                Remove-Item -Path $modelsDir -Recurse -Force -ErrorAction Stop
                Write-Ok "Removed model store: $modelsDir"
            } catch {
                Write-Err "Could not remove $modelsDir : $($_.Exception.Message)"
            }
        }
    } else {
        Write-Ok "No model store present: $modelsDir"
    }
} else {
    if (Test-Path $modelsDir) {
        Write-Warn "Keeping model store at $modelsDir (re-run with -RemoveModels to delete it)."
    }
}

# --- Final banner ------------------------------------------------------------
Write-Host ''
Write-Host '================ Project:Nova uninstalled =============' -ForegroundColor White
if (Test-Path $InstallDir) {
    Write-Host "  install dir still present (locked?): $InstallDir" -ForegroundColor Yellow
} else {
    Write-Host '  install dir:        removed'
}
Write-Host '  PATH entry:         removed (open a NEW terminal for it to take effect)'
Write-Host '  Start Menu folder:  removed'
Write-Host "  model store:        $(if (Test-Path $modelsDir) { 'kept at ' + $modelsDir } else { 'not present' })"
Write-Host '========================================================' -ForegroundColor White
Write-Host ''
Write-Ok 'Done.'
