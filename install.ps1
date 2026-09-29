param(
  [string]$Owner = "sairaph",
  [string]$Repo = "creality-slicer-mcp",
  [string]$Bin = "creality-slicer-mcp",
  [string[]]$ConfigureArgs = @()
)

$ErrorActionPreference = "Stop"

function Get-Arch {
  $arch = $env:PROCESSOR_ARCHITECTURE
  switch ($arch) {
    { $_ -in 'AMD64','x64' } { return 'amd64' }
    'ARM64'                  { return 'arm64' }
    default                  { Write-Host "  Unsupported architecture: $arch" -ForegroundColor Red; exit 1 }
  }
}

function Get-OS {
  return "windows"
}

$os = Get-OS
$arch = Get-Arch
$asset = "$Bin-$os-$arch.exe"
$url = "https://github.com/$Owner/$Repo/releases/latest/download/$asset"
$localAppData = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { Join-Path $env:USERPROFILE "AppData\Local" }
$installDir = Join-Path $localAppData "$Repo\bin"
$target = "$installDir\$Bin.exe"

New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Write-Host "  $Bin installer"
Write-Host "  Downloading $asset..."

try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  Invoke-WebRequest -Uri $url -OutFile "$target.new" -UseBasicParsing -ErrorAction Stop
} catch {
  Write-Host "  Download failed: $_" -ForegroundColor Red
  exit 1
}

if (-not (Test-Path "$target.new")) {
  Write-Host "  Download did not complete." -ForegroundColor Red
  exit 1
}

# Verify SHA256 checksum
$checksumUrl = $url.Substring(0, $url.LastIndexOf('/')) + '/SHA256SUMS.txt'
$verified = $false
try {
    $checksums = (Invoke-WebRequest -Uri $checksumUrl -UseBasicParsing).Content
    $pattern = ' \*?' + [regex]::Escape($asset) + '\s*$'
    $line = $checksums -split "`n" | ForEach-Object { $_.TrimEnd("`r") } | Where-Object { $_ -match $pattern } | Select-Object -First 1
    if ($line) {
        $expectedHash = ($line -split '\s+')[0].ToLower()
        $actualHash = (Get-FileHash -Path "$target.new" -Algorithm SHA256).Hash.ToLower()
        if ($expectedHash -ne $actualHash) {
            Write-Host "  SHA256 mismatch." -ForegroundColor Red
            Remove-Item "$target.new" -ErrorAction SilentlyContinue
            exit 1
        }
        $verified = $true
    }
} catch { }
if (-not $verified) {
    Write-Host "  Warning: could not verify SHA256SUMS.txt; the download was not verified." -ForegroundColor Yellow
}

# Swap the new binary into place using move-aside
$oldTarget = "$target.old-$([System.Guid]::NewGuid().ToString('N').Substring(0,8))"
if (Test-Path $target) {
    try {
        Move-Item $target $oldTarget -Force
    } catch {
        Write-Host "  Could not replace binary. Close any running processes and retry." -ForegroundColor Red
        Remove-Item "$target.new" -ErrorAction SilentlyContinue
        exit 1
    }
}
try {
    Move-Item "$target.new" $target -Force
} catch {
    Write-Host "  Could not replace binary. Close any running processes and retry." -ForegroundColor Red
    if (Test-Path $oldTarget) { Move-Item $oldTarget $target -Force }
    Remove-Item "$target.new" -ErrorAction SilentlyContinue
    exit 1
}
Remove-Item $oldTarget -Force -ErrorAction SilentlyContinue

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
  $newPath = "$installDir;$userPath"
  [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
  $env:Path = "$installDir;$env:Path"
  Write-Host "  Added $installDir to your PATH. Restart your terminal."
}

# --- launch the configurer ---
try {
    & $target configure @ConfigureArgs
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
} catch {
    Write-Host "  configure did not complete: $_" -ForegroundColor Red
    Write-Host "  Re-run ``$Bin configure`` later to finish setup." -ForegroundColor Yellow
}

Write-Host "  Installed $Bin to $target"
