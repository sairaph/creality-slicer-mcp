# Installs creality-slicer-mcp from GitHub releases and runs its setup wizard.
# Cancelling the wizard undoes everything this script changed, and so does
# Ctrl+C or an unexpected error before the wizard runs.
#
#   irm https://github.com/sairaph/creality-slicer-mcp/releases/latest/download/install.ps1 | iex
#
# To pass parameters (a specific release, or flags for the wizard):
#   & ([scriptblock]::Create((irm <url>))) -Version v0.1.0 -ConfigureArgs "--yes"
#
# The whole script is one script block: with `irm ... | iex` it runs in the
# caller's own session, and this keeps its variables, functions and
# preference settings out of that session and never calls `exit` in it.
# The TLS 1.2 it enables for its downloads is reset to the session's own
# setting afterwards. What does reach the session: the install directory on
# $env:Path once installed, $LASTEXITCODE from the wizard, the helper type
# CrealitySlicerMcpInstaller.NativeMethods once PATH was changed, and a failure as
# an error record in $Error, which is terminating when the caller runs with
# $ErrorActionPreference = "Stop" or passes -ErrorAction Stop.
& {
  [CmdletBinding(PositionalBinding = $false)]
  param(
    [string]$Owner = "sairaph",
    [string]$Repo = "creality-slicer-mcp",
    [string]$Bin = "creality-slicer-mcp",
    [string]$Version = "latest",
    [string[]]$ConfigureArgs = @()
  )
  # The caller's error preference, or -ErrorAction, before this block sets
  # its own: a failure is reported the way the caller asked for.
  $callerErrorAction = $ErrorActionPreference
  $ErrorActionPreference = "Stop"
  # With "Stop", a profile setting this to $true (PowerShell 7.3+) turns a
  # non-zero exit of `configure` into an exception; its exit code is read
  # from $LASTEXITCODE instead.
  $PSNativeCommandUseErrorActionPreference = $false
  # Windows PowerShell's progress bar slows downloads down many times over.
  $ProgressPreference = "SilentlyContinue"
  # `creality-slicer-mcp configure` exits with 3 when the wizard is left before it
  # changed anything.
  $exitCancelled = 3

  # A failure is also written as an error record, after its cleanup, so a
  # calling script can tell it apart from success. It follows the caller's
  # error preference rather than this block's "Stop": a non-terminating error
  # by default, one that ends the caller's script when it asked for Stop.
  function Write-InstallError([string]$message) {
    Write-Error -Message "$Bin installer: $message" -ErrorAction $callerErrorAction
  }

  # --- user PATH, read and written in the registry as stored ---------------
  # [Environment]::GetEnvironmentVariable expands %VAR% entries and
  # SetEnvironmentVariable writes REG_SZ, which would turn a REG_EXPAND_SZ
  # PATH into fixed paths; the registry API keeps both as they are.
  function Get-UserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment")
    try {
      if ($key.GetValueNames() -notcontains "Path") {
        return @{ Value = ""; Kind = [Microsoft.Win32.RegistryValueKind]::ExpandString; Exists = $false }
      }
      return @{
        Value  = $key.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        Kind   = $key.GetValueKind("Path")
        Exists = $true
      }
    } finally { $key.Close() }
  }
  function Set-UserPath([string]$value, $kind) {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
    try { $key.SetValue("Path", $value, $kind) } finally { $key.Close() }
    Send-EnvironmentChange
  }
  function Remove-UserPath {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
    try { $key.DeleteValue("Path", $false) } finally { $key.Close() }
    Send-EnvironmentChange
  }
  # Tell Explorer and new terminals, as SetEnvironmentVariable would.
  function Send-EnvironmentChange {
    try {
      if (-not ("CrealitySlicerMcpInstaller.NativeMethods" -as [type])) {
        Add-Type -Namespace CrealitySlicerMcpInstaller -Name NativeMethods -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("user32.dll", SetLastError = true, CharSet = System.Runtime.InteropServices.CharSet.Unicode)]
public static extern System.IntPtr SendMessageTimeout(System.IntPtr hWnd, uint Msg, System.UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out System.UIntPtr lpdwResult);
'@
      }
      $result = [System.UIntPtr]::Zero
      [void][CrealitySlicerMcpInstaller.NativeMethods]::SendMessageTimeout([System.IntPtr]0xffff, 0x1a, [System.UIntPtr]::Zero, "Environment", 2, 5000, [ref]$result)
    } catch { }
  }
  function Test-SameDir([string]$a, [string]$b) {
    return $a.Trim().TrimEnd("\") -ieq $b.Trim().TrimEnd("\")
  }

  # A 32-bit PowerShell on 64-bit Windows sees x86 here and the machine's
  # own architecture in PROCESSOR_ARCHITEW6432.
  $machineArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
  switch ($machineArch) {
    { $_ -in 'AMD64', 'x64' } { $arch = 'amd64' }
    'ARM64'                   { $arch = 'arm64' }
    default {
      Write-Host "  Unsupported architecture: $machineArch" -ForegroundColor Red
      Write-InstallError "Unsupported architecture: $machineArch"
      return
    }
  }

  # "--yes --clients cursor" given as one string is split into its flags.
  # The array form ("--yes", "--token", "a b") is passed as it is, so a
  # value containing spaces survives.
  if ($ConfigureArgs.Count -eq 1) {
    $ConfigureArgs = @($ConfigureArgs[0] -split '\s+' | Where-Object { $_ })
  }

  $asset = "$Bin-windows-$arch.exe"
  if ($Version -eq "latest") {
    $base = "https://github.com/$Owner/$Repo/releases/latest/download"
  } else {
    $base = "https://github.com/$Owner/$Repo/releases/download/$Version"
  }
  $localAppData = if ($env:LOCALAPPDATA) { $env:LOCALAPPDATA } else { Join-Path $env:USERPROFILE "AppData\Local" }
  $installRoot = Join-Path $localAppData $Repo
  $installDir = Join-Path $installRoot "bin"
  $target = Join-Path $installDir "$Bin.exe"

  # Record what exists now, so a cancelled setup can put it back.
  $createdRoot = -not (Test-Path $installRoot)
  $createdDir = -not (Test-Path $installDir)
  function Undo-Directories {
    if ($createdDir -and (Test-Path $installDir) -and -not (Get-ChildItem $installDir -Force)) {
      Remove-Item $installDir -Force -ErrorAction SilentlyContinue
    }
    if ($createdRoot -and (Test-Path $installRoot) -and -not (Get-ChildItem $installRoot -Force)) {
      Remove-Item $installRoot -Force -ErrorAction SilentlyContinue
    }
  }

  # The previous version, kept until setup finishes, and whether this run
  # put the install directory on the user PATH.
  $backup = $null
  $addedPath = $false
  $pathExisted = $false
  function Restore-Backup {
    if (-not $backup) {
      Remove-Item $target -Force -ErrorAction SilentlyContinue
    } elseif (Test-Path $backup) {
      Move-Item $backup $target -Force
    }
    # Otherwise the previous binary was not moved aside yet and is in place.
  }
  function Undo-Path {
    if (-not $addedPath) { return }
    # Remove only our entry, keeping any change made to PATH meanwhile.
    $current = Get-UserPath
    $kept = (@($current.Value -split ";" | Where-Object { -not (Test-SameDir $_ $installDir) })) -join ";"
    if ($kept.Trim() -or ($pathExisted -and $current.Exists)) {
      Set-UserPath $kept $current.Kind
    } elseif ($current.Exists) {
      # This run created the value and nothing else was added to it.
      Remove-UserPath
    }
    $env:Path = (@($env:Path -split ";" | Where-Object { -not (Test-SameDir $_ $installDir) })) -join ";"
  }
  function Write-RolledBack {
    if ($backup) {
      Write-Host "  The previously installed $Bin was kept."
    } else {
      Write-Host "  $Bin was not installed."
    }
  }

  # $stage is what Ctrl+C or an unexpected terminating error has to undo, as
  # a cancel does:
  #   download   the new binary is fetched and checked; only it goes
  #   replace    it replaces the installed one, which is kept as $backup
  #   path       the install directory is put on the user PATH
  #   configure  the setup wizard runs; its exit code decides what is kept,
  #              and without one the installed program stays
  #   done       setup finished, or its outcome was handled; nothing is undone
  $stage = "download"
  # Undo-Stage undoes what this run changed up to $stage; every step runs,
  # even if one of them fails.
  function Undo-Stage {
    try { Remove-Item "$target.new" -Force -ErrorAction SilentlyContinue } catch { }
    if ($stage -ne "download") {
      try { Restore-Backup } catch { }
      try { Undo-Path } catch { }
    }
    try { Undo-Directories } catch { }
  }

  try {
    New-Item -ItemType Directory -Force -Path $installDir | Out-Null

    Write-Host "  $Bin installer"
    Write-Host "  Downloading $asset ($Version)..."
    # ServicePointManager is process-wide: TLS 1.2 is added to the protocols
    # the session allows for these downloads only.
    $savedProtocols = [Net.ServicePointManager]::SecurityProtocol
    $checksumUrl = "$base/SHA256SUMS.txt"
    $checksums = $null
    try {
      [Net.ServicePointManager]::SecurityProtocol = $savedProtocols -bor [Net.SecurityProtocolType]::Tls12
      try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile "$target.new" -UseBasicParsing
      } catch {
        Write-Host "  Download failed: $_" -ForegroundColor Red
        Undo-Stage
        $stage = "done"
        Write-InstallError "Download of $base/$asset failed: $_"
        return
      }

      # Verify the SHA256 checksum; install nothing that cannot be verified.
      try {
        $checksums = (Invoke-WebRequest -Uri $checksumUrl -UseBasicParsing).Content
        # GitHub serves release assets as application/octet-stream, for which
        # Windows PowerShell returns the content as bytes rather than text.
        if ($checksums -is [byte[]]) { $checksums = [System.Text.Encoding]::UTF8.GetString($checksums) }
      } catch { }
    } finally {
      [Net.ServicePointManager]::SecurityProtocol = $savedProtocols
    }
    $problem = $null
    if (-not $checksums) {
      $problem = "Could not fetch $checksumUrl to verify the download; nothing was installed.`n  Please check your connection and try again."
    } else {
      $pattern = ' \*?' + [regex]::Escape($asset) + '\s*$'
      $line = $checksums -split "`n" | ForEach-Object { $_.TrimEnd("`r") } | Where-Object { $_ -match $pattern } | Select-Object -First 1
      if (-not $line) {
        $problem = "$checksumUrl lists no checksum for $asset; nothing was installed."
      } else {
        $expectedHash = ($line -split '\s+')[0].ToLower()
        $actualHash = (Get-FileHash -Path "$target.new" -Algorithm SHA256).Hash.ToLower()
        if ($expectedHash -ne $actualHash) { $problem = "SHA256 mismatch; nothing was installed." }
      }
    }
    if ($problem) {
      Write-Host "  $problem" -ForegroundColor Red
      Undo-Stage
      $stage = "done"
      Write-InstallError $problem
      return
    }

    # Keep a previous version until setup finishes. Its name must not match
    # "<bin>.exe.old-*": the program deletes those files when it starts.
    # A backup is deleted after setup, which fails while an AI client still
    # runs it, and a run that was killed can leave one behind.
    $staleBackups = @(Get-ChildItem -LiteralPath $installDir -Filter "$Bin.exe.bak-*" -Force -ErrorAction SilentlyContinue |
        Sort-Object LastWriteTime -Descending)
    if (-not (Test-Path $target) -and $staleBackups.Count -gt 0) {
      # A run killed after moving the installed binary aside left it only as
      # a backup: the newest one is the version installed, so it goes back.
      Move-Item $staleBackups[0].FullName $target -Force
      $staleBackups = @($staleBackups | Select-Object -Skip 1)
    }
    $staleBackups | Remove-Item -Force -ErrorAction SilentlyContinue
    if (Test-Path $target) {
      $backup = "$target.bak-$([System.Guid]::NewGuid().ToString('N').Substring(0, 8))"
    }
    $stage = "replace"
    if ($backup) {
      try {
        Move-Item $target $backup -Force
      } catch {
        # The installed binary is still in place.
        $backup = $null
        Write-Host "  Could not replace $target. Close any running $Bin and retry." -ForegroundColor Red
        Remove-Item "$target.new" -Force -ErrorAction SilentlyContinue
        $stage = "done"
        Write-InstallError "Could not replace $target`: $_"
        return
      }
    }
    try {
      Move-Item "$target.new" $target -Force
    } catch {
      Write-Host "  Could not install $target." -ForegroundColor Red
      Undo-Stage
      $stage = "done"
      Write-InstallError "Could not install $target`: $_"
      return
    }

    # Put the install directory on the user PATH, remembering whether we did
    # and whether a user Path value existed before.
    $stage = "path"
    $userPath = Get-UserPath
    $pathExisted = $userPath.Exists
    if (-not (@($userPath.Value -split ";") | Where-Object { Test-SameDir $_ $installDir })) {
      $newValue = if ($userPath.Value) { "$installDir;$($userPath.Value)" } else { $installDir }
      $addedPath = $true
      Set-UserPath $newValue $userPath.Kind
      $env:Path = "$installDir;$env:Path"
    }

    # Run the setup wizard, in a terminal only: without one it cannot ask,
    # and `configure` would register every detected client unasked.
    $interactive = $false
    try { $interactive = -not [Console]::IsInputRedirected -and -not [Console]::IsOutputRedirected } catch { }
    # configure skips the wizard for --yes, --all, --clients and the credential
    # flags (runsUnattended in wizard.go). Go's flag package reads -flag and
    # --flag alike and is case-sensitive: these are the spellings it reads as
    # true, and the string flags alone (their value follows) or with a value.
    $unattendedFlags = foreach ($dash in "--", "-") {
      foreach ($name in "yes", "all") {
        foreach ($suffix in "", "=1", "=t", "=T", "=true", "=TRUE", "=True") { "$dash$name$suffix" }
      }
      foreach ($name in "token", "clients", "email") { "$dash$name" }
    }
    $unattended = [bool]($ConfigureArgs | Where-Object {
        $_ -cin $unattendedFlags -or $_ -cmatch '^--?(token|clients|email)=.'
      })
    if (-not $interactive -and -not $unattended) {
      $stage = "done"
      if ($backup) { Remove-Item $backup -Force -ErrorAction SilentlyContinue }
      Write-Host "  Installed $Bin to $target."
      Write-Host "  Not running in a terminal. Finish setup in one with: $Bin configure"
      return
    }
    $code = $null
    $startError = $null
    $stage = "configure"
    try {
      & $target configure @ConfigureArgs
      $code = $LASTEXITCODE
    } catch [System.Management.Automation.CommandNotFoundException], [System.Management.Automation.ApplicationFailedException] {
      # The program did not start, so it changed nothing.
      $startError = $_
    } catch {
      # The program ran; its exit code decides what happened.
      $code = if ($null -ne $LASTEXITCODE) { $LASTEXITCODE } else { 1 }
    }

    if ($startError -or $code -eq $exitCancelled) {
      # Put everything back as it was.
      Undo-Stage
      $stage = "done"
      # A cancelled wizard already said "Setup cancelled"; say what was restored.
      if ($startError) { Write-Host "  Could not run $target`: $startError" -ForegroundColor Red }
      Write-RolledBack
      if ($startError) { Write-InstallError "Could not run $target`: $startError" }
      return
    }

    $stage = "done"
    if ($backup) { Remove-Item $backup -Force -ErrorAction SilentlyContinue }
    if ($addedPath) { Write-Host "  Added $installDir to your PATH." }
    if ($code -ne 0) {
      Write-Host "  Setup did not finish (exit code $code). $Bin is installed at $target." -ForegroundColor Yellow
      Write-Host "  Run ``$Bin configure`` to finish, or ``$Bin uninstall --all`` to remove it." -ForegroundColor Yellow
      Write-InstallError "Setup did not finish (exit code $code); $Bin is installed at $target."
      return
    }
    Write-Host "  Installed $Bin to $target"
  } finally {
    # Reached with $stage other than "done" only when Ctrl+C or an
    # unexpected terminating error ended the steps above.
    if ($stage -in "download", "replace", "path") {
      Undo-Stage
      Write-Host "  Setup was interrupted; the changes were undone." -ForegroundColor Yellow
      if ($stage -ne "download") { Write-RolledBack }
    } elseif ($stage -eq "configure") {
      if ($backup) { Remove-Item $backup -Force -ErrorAction SilentlyContinue }
      Write-Host "  Setup did not finish. $Bin is installed at $target." -ForegroundColor Yellow
      Write-Host "  Run ``$Bin configure`` to finish, or ``$Bin uninstall --all`` to remove it." -ForegroundColor Yellow
    }
  }
} @args
