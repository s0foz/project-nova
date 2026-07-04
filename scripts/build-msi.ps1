<#
.SYNOPSIS
    Package Project:Nova into a Windows MSI installer using the WiX Toolset v3.

.DESCRIPTION
    Generates a WiX .wxs file from an inline template (no external template
    file required), then invokes candle.exe + light.exe to compile an MSI
    installer at dist\nova-<version>-<arch>.msi.

    The installer:
      - Installs to %LOCALAPPDATA%\ProjectNova (per-user, no admin required)
      - Adds the install directory to the user PATH
      - Creates a Start Menu shortcut to nova-tray.exe (the tray app)
      - Registers nova.exe as the CLI command
      - Supports major upgrades via a stable UpgradeCode

    Prerequisites:
      - WiX Toolset v3 (candle.exe + light.exe on PATH).
        Install from https://wixtoolset.org/releases/v3.14/stable or via:
          winget install WixToolset.WiX   # if available
          choco install wixtoolset
      - nova.exe (and optionally nova-tray.exe) must already exist in .\dist\.
        Run scripts\build-windows.ps1 -Tray first.

.PARAMETER Version
    Version string for the MSI ProductVersion (e.g. "0.1.0").
    Must match the WiX format: major.minor.build (each 0-65534).
    Defaults to `git describe --tags --always --dirty`, stripped of a
    leading 'v' and any non-numeric suffixes.

.PARAMETER Arch
    Architecture to embed in the MSI filename. Defaults to "amd64".
    Note: WiX handles architecture via the .wxs Platform attribute; this
    script does not cross-compile — build the matching .exe first.

.PARAMETER Manufacturer
    Manufacturer string embedded in the MSI metadata. Defaults to "Project:Nova".

.PARAMETER UpgradeCode
    Stable GUID identifying this product line across versions. DO NOT change
    between releases — it's how Windows knows to upgrade rather than install
    side-by-side.

.PARAMETER DistDir
    Directory containing the built .exe files. Defaults to .\dist.

.EXAMPLE
    .\scripts\build-windows.ps1 -Tray
    .\scripts\build-msi.ps1
    Build binaries then produce dist\nova-0.1.0-amd64.msi.

.EXAMPLE
    .\scripts\build-msi.ps1 -Version 1.0.0
    Produce an MSI tagged 1.0.0 (requires .\dist\nova.exe to exist).

.NOTES
    This script targets WiX v3 (candle/light). WiX v4 has a different CLI
    (`wix build`) — see docs/DEVELOPMENT.md for migration notes.
#>
[CmdletBinding()]
param(
    [string]$Version,
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',
    [string]$Manufacturer = 'Project:Nova',
    [string]$UpgradeCode = '7B5A3F2E-9C41-4D8B-A1E6-3F2C8B7D9E0A',  # stable — do NOT change
    [string]$DistDir = (Join-Path $PSScriptRoot '..\dist')
)

$ErrorActionPreference = 'Stop'
$VerbosePreference = 'Continue'

function Write-Step([string]$m){ Write-Host "==> $m" -ForegroundColor Cyan }
function Write-Ok([string]$m)  { Write-Host "[OK]  $m" -ForegroundColor Green }
function Write-Warn([string]$m){ Write-Host "[WARN] $m" -ForegroundColor Yellow }
function Write-Err([string]$m) { Write-Host "[ERR]  $m" -ForegroundColor Red }

# --- Resolve repo root -------------------------------------------------------
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
Set-Location -Path $RepoRoot
$DistDir = (Resolve-Path $DistDir -ErrorAction SilentlyContinue).Path
if (-not $DistDir) {
    Write-Err "Dist directory not found. Run scripts\build-windows.ps1 first."
    exit 1
}

# --- Preflight: WiX toolset --------------------------------------------------
$candle = Get-Command candle.exe -ErrorAction SilentlyContinue
$light  = Get-Command light.exe  -ErrorAction SilentlyContinue
if (-not $candle -or -not $light) {
    Write-Err 'WiX Toolset v3 not found on PATH (candle.exe / light.exe missing).'
    Write-Err 'Install WiX v3.14 from https://wixtoolset.org/releases/v3.14/stable'
    Write-Err '  winget install WixToolset.WiX'
    Write-Err '  choco install wixtoolset'
    exit 1
}
Write-Step "WiX candle:  $($candle.Source)"
Write-Step "WiX light:   $($light.Source)"

# --- Preflight: built binaries ----------------------------------------------
$cliExe  = Join-Path $DistDir 'nova.exe'
$trayExe = Join-Path $DistDir 'nova-tray.exe'

if (-not (Test-Path $cliExe)) {
    Write-Err "Missing $cliExe — run scripts\build-windows.ps1 first."
    exit 1
}
if (-not (Test-Path $trayExe)) {
    Write-Warn "nova-tray.exe not found in $DistDir — MSI will install CLI only."
    Write-Warn "Run scripts\build-windows.ps1 -Tray to also build the tray app."
    $includeTray = $false
} else {
    $includeTray = $true
}

# --- Compute / normalise version --------------------------------------------
if (-not $Version) {
    Write-Step "Computing version from git describe..."
    $raw = (& git -C $RepoRoot describe --tags --always --dirty 2>$null)
    if (-not $raw) { $raw = '0.0.0' }
    $Version = $raw -replace '^v', ''
}
# WiX ProductVersion must be major.minor.build, all numeric, each <= 65534.
$clean = $Version -replace '^v', ''
$parts = ($clean -split '[\.\-+]') | Where-Object { $_ -match '^\d+$' }
if ($parts.Count -lt 2) { $parts = @('0','0','1') }
elseif ($parts.Count -lt 3) { $parts += '0' }
$wixVersion = ($parts[0..2] -join '.')
# Cap each component at 65534 (WiX limit).
$capped = $wixVersion.Split('.') | ForEach-Object {
    $n = [int]$_
    if ($n -gt 65534) { $n = 65534 }
    "$n"
}
$wixVersion = $capped -join '.'
Write-Step "MSI ProductVersion: $wixVersion (from source version '$Version')"

# --- Product / package identifiers ------------------------------------------
$productId = [Guid]::NewGuid().ToString().ToUpper()
$productName = 'Project:Nova'
$productNameEsc = [System.Security.SecurityElement]::Escape($productName)
$manufacturerEsc = [System.Security.SecurityElement]::Escape($Manufacturer)

# --- Install dir layout -----------------------------------------------------
$installDir     = '[LocalAppDataFolder]ProjectNova'
$installDirId   = 'INSTALLDIR'
$startMenuDirId = 'StartMenuDir'
$desktopDirId   = 'DesktopFolder'

# --- Build the .wxs file from an inline here-string -------------------------
$wxsPath = Join-Path $DistDir 'nova.wxs'
$wixObjPath = Join-Path $DistDir 'nova.wixobj'
$msiName = "nova-$wixVersion-$Arch.msi"
$msiPath = Join-Path $DistDir $msiName

Write-Step "Generating $wxsPath ..."

# Conditionally include the tray component block.
$trayComponentXml = ''
if ($includeTray) {
    $trayComponentXml = @"
            <Component Id="cmp_NovaTrayExe" Guid="$([Guid]::NewGuid().ToString().ToUpper())">
              <File Id="fil_NovaTrayExe" Source="$trayExe" KeyPath="yes" Checksum="yes" />
              <Shortcut Id="sc_StartMenuNovaTray"
                        Directory="$startMenuDirId"
                        Name="Nova Tray"
                        Description="Launch the Project:Nova desktop tray"
                        Target="[$installDirId]nova-tray.exe"
                        WorkingDirectory="$installDirId"
                        Advertise="no"
                        Icon="ico_Nova" />
              <Shortcut Id="sc_DesktopNovaTray"
                        Directory="$desktopDirId"
                        Name="Nova Tray"
                        Description="Launch the Project:Nova desktop tray"
                        Target="[$installDirId]nova-tray.exe"
                        WorkingDirectory="$installDirId"
                        Advertise="no"
                        Icon="ico_Nova" />
            </Component>

"@
}

# PATH update component — writes to HKCU\Environment (per-user PATH).
# Using the RegistryKey/RegistryValue WiX elements avoids the WiX UtilExtension
# dependency for PATH manipulation.
$pathComponentXml = @"
            <Component Id="cmp_PathUpdate" Guid="$([Guid]::NewGuid().ToString().ToUpper())">
              <RegistryKey Root="HKCU" Key="Environment" Action="createAndRemoveOnUninstall">
                <RegistryValue Name="NOVA_HOME" Type="string" Value="[$installDirId]" />
              </RegistryKey>
              <Environment Id="env_Path" Name="PATH" Action="set" Part="last" Permanent="no"
                           System="no" Value="[$installDirId]" />
            </Component>

"@

$wxs = @"
<?xml version="1.0" encoding="UTF-8"?>
<!--
  Auto-generated by scripts\build-msi.ps1 on $([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ'))
  Source version: $Version
  WiX ProductVersion: $wixVersion
  Do not edit by hand — regenerate from the script.
-->
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="$productId"
           Name="$productNameEsc"
           Language="1033"
           Version="$wixVersion"
           Manufacturer="$manufacturerEsc"
           UpgradeCode="$UpgradeCode">
    <Package Id="*"
             Description="$productNameEsc $wixVersion installer"
             Manufacturer="$manufacturerEsc"
             InstallerVersion="405"
             Compressed="yes"
             InstallScope="perUser"
             InstallPrivileges="limited"
             Platform="$($Arch.ToUpper())" />

    <!-- Major upgrade support: replace older versions, don't error. -->
    <MajorUpgrade DowngradeErrorMessage="A newer version of $productNameEsc is already installed."
                  AllowSameVersionUpgrades="yes" />
    <Media Id="1" Cabinet="nova.cab" EmbedCab="yes" />

    <!-- Per-user install — no UAC prompt. -->
    <Property Id="ALLUSERS" Value="2" />
    <Property Id="MSIINSTALLPERUSER" Value="1" />

    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="LocalAppDataFolder">
        <Directory Id="$installDirId" Name="ProjectNova">
          <Component Id="cmp_NovaExe" Guid="$([Guid]::NewGuid().ToString().ToUpper())">
            <File Id="fil_NovaExe" Source="$cliExe" KeyPath="yes" Checksum="yes" />
          </Component>
$trayComponentXml$pathComponentXml        </Directory>
      </Directory>

      <Directory Id="ProgramMenuFolder">
        <Directory Id="$startMenuDirId" Name="Project:Nova" />
      </Directory>

      <Directory Id="$desktopDirId" Name="Desktop" />
    </Directory>

    <DirectoryRef Id="$startMenuDirId">
      <Component Id="cmp_StartMenuUninstall" Guid="$([Guid]::NewGuid().ToString().ToUpper())">
        <Shortcut Id="sc_Uninstall"
                  Name="Uninstall Nova"
                  Description="Uninstall Project:Nova"
                  Target="[SystemFolder]msiexec.exe"
                  Arguments="/x [ProductCode]" />
        <RegistryValue Root="HKCU"
                       Key="Software\ProjectNova\StartMenu"
                       Name="Uninstall"
                       Type="integer"
                       Value="1"
                       KeyPath="yes" />
      </Component>
    </DirectoryRef>

    <Icon Id="ico_Nova" SourceFile="$cliExe" />

    <Feature Id="feat_Core" Title="Project:Nova" Level="1"
             Description="The Nova CLI and tray application."
             Display="expand" ConfigurableDirectory="$installDirId">
      <ComponentRef Id="cmp_NovaExe" />
$($(if ($includeTray) { '      <ComponentRef Id="cmp_NovaTrayExe" />' + "`r`n" } else { '' }))
      <ComponentRef Id="cmp_PathUpdate" />
      <ComponentRef Id="cmp_StartMenuUninstall" />
    </Feature>

    <UI>
      <UIRef Id="WixUI_InstallDir" />
      <Property Id="WIXUI_INSTALLDIR" Value="$installDirId" />
      <UIRef Id="WixUI_FeatureTree" />
    </UI>

    <WixVariable Id="WixUILicenseRtf" Value="license.rtf" Overridable="yes" />
    <Property Id="WIXUI_EXITDIALOGOPTIONALCHECKBOXTEXT" Value="Launch Nova tray" />
    <Property Id="WixShellExecTarget" Value="[$installDirId]nova-tray.exe" />
    <CustomAction Id="LaunchTray"
                  BinaryKey="WixCA"
                  DllEntry="WixShellExec"
                  Impersonate="yes" />
    <InstallExecuteSequence>
      <Custom Action="LaunchTray" After="InstallFinalize">(&amp;feat_Core = 3) AND NOT Installed</Custom>
    </InstallExecuteSequence>
  </Product>
</Wix>
"@

Set-Content -Path $wxsPath -Value $wxs -Encoding UTF8
Write-Ok "Wrote $wxsPath"

# --- Provide a minimal license.rtf if missing -------------------------------
$licenseRtf = Join-Path $DistDir 'license.rtf'
if (-not (Test-Path $licenseRtf)) {
    $mitPath = Join-Path $RepoRoot 'LICENSE'
    $mitText = if (Test-Path $mitPath) { Get-Content $mitPath -Raw } else { 'MIT License. See https://opensource.org/license/mit/' }
    # Minimal RTF wrapper — candle/light don't care about formatting.
    $rtf = "{\rtf1\ansi\deff0 {\fonttbl {\f0 Consolas;}}\f0\fs18 " +
            ($mitText -replace '\\', '\\' -replace '\{', '\{' -replace '\}', '\}' -replace "`r`n", '\line ') +
            "}"
    Set-Content -Path $licenseRtf -Value $rtf -Encoding ASCII
    Write-Ok "Wrote minimal $licenseRtf"
}

# --- Compile with candle.exe ------------------------------------------------
Write-Step "candle.exe $wxsPath"
& $candle.Source -out "$wixObjPath" "$wxsPath" -ext WixUIExtension -ext WixUtilExtension -arch "$($Arch.ToUpper())"
if ($LASTEXITCODE -ne 0) {
    Write-Err "candle.exe failed (exit $LASTEXITCODE)"
    exit $LASTEXITCODE
}

# --- Link with light.exe ----------------------------------------------------
Write-Step "light.exe -> $msiName"
& $light.Source -out "$msiPath" "$wixObjPath" -ext WixUIExtension -ext WixUtilExtension -sice:ICE60 -sice:ICE91
if ($LASTEXITCODE -ne 0) {
    Write-Err "light.exe failed (exit $LASTEXITCODE)"
    exit $LASTEXITCODE
}

# --- Cleanup intermediate files --------------------------------------------
Remove-Item $wixObjPath -ErrorAction SilentlyContinue
Remove-Item (Join-Path $DistDir 'nova.wixpdb') -ErrorAction SilentlyContinue

# --- Summary ----------------------------------------------------------------
$info = Get-Item $msiPath
Write-Host ''
Write-Host '================ MSI build summary ================' -ForegroundColor White
Write-Host ("  product  : {0}" -f $productName)
Write-Host ("  version  : {0} (source: {1})" -f $wixVersion, $Version)
Write-Host ("  arch     : {0}" -f $Arch)
Write-Host ("  upgrade  : {0}" -f $UpgradeCode)
Write-Host ("  msi      : {0}" -f $info.FullName)
Write-Host ("  size     : {0:N0} bytes" -f $info.Length)
Write-Host '===================================================' -ForegroundColor White
Write-Host ''
Write-Ok "MSI built."
Write-Host 'Next steps:'
Write-Host "  - Install:    msiexec /i $msiName"
Write-Host '  - Uninstall:  msiexec /x {<ProductCode>}   (or use Add/Remove Programs)'
Write-Host "  - Silent:     msiexec /i $msiName /quiet /norestart"
