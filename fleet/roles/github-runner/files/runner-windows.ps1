# runner-windows.ps1: a WSL machine's Windows-side GitHub Actions runners, driven by the github-runner role's
# tasks/wsl-windows.yml through WSL interop (powershell.exe run from the machine's WSL login, which acts with the Windows
# account that started WSL, elevated). The role stages this file to C:\ProgramData\nova-runner-stage and calls one verb
# at a time; every line it prints starts with a word the role matches. fleet/README.md, "Windows runners of a WSL machine".
#
#   install   -Dir -Cache -Version -Sha [-PathList]   the pinned runner zip in -Cache (its sha256 checked before it is
#             used), unpacked into -Dir where config.cmd is missing (a dir with files but no config.cmd, an
#             interrupted unpack, is moved aside first: PARTIAL <dir> -> <where>), and the .env lines the role owns (PATH, GOCACHE,
#             DOTNET_INSTALL_DIR, DOTNET_CLI_TELEMETRY_OPTOUT) set in place, every other line (the CI-rules hooks) kept:
#             DOWNLOADED, EXTRACTED, ENV-CHANGED <dir> when something moved
#   register  -Dir -Url -Name -Labels                 stdin carries ONE registration token; it reaches config.cmd as
#             ACTIONS_RUNNER_INPUT_TOKEN, never an argument, a file or a line of output. Registers the runner as a
#             Windows service (config.cmd --runasservice --replace: NT AUTHORITY\NETWORK SERVICE, automatic start):
#             REGISTERED. The role calls it only for a runner that is not whole (no .runner, no service, or not in
#             GitHub's list): a half registration (.runner there) is repaired first, its .runner and credentials moved
#             into <dir>\retired-<yyyymmdd>\ and its service removed: REPAIR <dir>
#   ensure    -Dir                                    its service starts at boot and is running: SERVICE <dir> ok|started,
#             or NO-SERVICE <dir> (the role counts it as not registered, and the next run repairs it)
#   stop      -Dir                                    the runner's service stopped, so it takes no new job: STOPPED <dir>
#   retire    -Dir                                    stdin carries ONE removal token: config.cmd remove --unattended (GitHub
#             and the service), the service gone, the dir deleted: RETIRED <dir>
#   delete    -Path                                   each item -Path names (a wildcard is allowed) deleted, never moved:
#             DELETED <path>, or ABSENT <path>
#   untask    -Task                                   the scheduled task \<name> unregistered: UNTASKED or ABSENT
param(
  [Parameter(Mandatory = $true)][ValidateSet('install', 'register', 'ensure', 'stop', 'retire', 'delete', 'untask')][string]$Action,
  [string]$Dir = '',
  [string]$Cache = '',
  [string]$Version = '',
  [string]$Sha = '',
  [string]$PathList = '',
  [string]$Url = '',
  [string]$Name = '',
  [string]$Labels = '',
  [string]$Path = '',
  [string]$Task = ''
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Need-Admin {
  $p = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { 'NEEDS-ELEVATION'; exit 3 }
}
function Read-Token {
  $t = [Console]::In.ReadLine()
  if (-not $t -or $t.Trim().Length -lt 20) { 'NO-TOKEN on stdin'; exit 4 }
  return $t.Trim()
}
# Service-Of DIR: the runner's Windows service, found by its directory in the service's path.
function Service-Of([string]$d) {
  Get-CimInstance Win32_Service | Where-Object { $_.PathName -like "*$d\*" } | Select-Object -First 1
}
# Move-Aside PATH ROOT: PATH moved under ROOT, keeping its path without the drive (C:\a\b ->
# <root>\a\b); never deleted.
function Move-Aside([string]$p, [string]$root) {
  $to = Join-Path $root ($p -replace '^[A-Za-z]:\\', '')
  if (Test-Path -LiteralPath $to) { $to = "$to.$([DateTime]::UtcNow.ToString('HHmmss'))" }
  New-Item -ItemType Directory -Force -Path (Split-Path $to) | Out-Null
  Move-Item -LiteralPath $p -Destination $to
  return $to
}
# Invoke-Config ARGS: config.cmd in $Dir with the token from stdin in its environment only; its exit code.
function Invoke-Config([string[]]$a) {
  $tok = Read-Token
  Push-Location $Dir
  try {
    $env:ACTIONS_RUNNER_INPUT_TOKEN = $tok
    # its lines go straight to stdout, so the function's only output is the exit code
    # config.cmd prints in the console code page (a check mark is not UTF-8): ASCII only, or ansible refuses the result
    & .\config.cmd @a | ForEach-Object { [Console]::Out.WriteLine("CONFIG " + ("$_" -replace '[^\x20-\x7E]', '?')) }
    return $LASTEXITCODE
  } finally {
    Remove-Item Env:\ACTIONS_RUNNER_INPUT_TOKEN -ErrorAction SilentlyContinue
    $tok = $null
    Pop-Location
  }
}

switch ($Action) {
  'install' {
    Need-Admin
    if (-not $Dir -or -not $Cache -or -not $Version -or $Sha -notmatch '^[0-9a-f]{64}$') { 'BAD-PIN'; exit 2 }
    New-Item -ItemType Directory -Force -Path $Cache | Out-Null
    $zip = Join-Path $Cache "actions-runner-win-x64-$Version.zip"
    if (-not (Test-Path $zip) -or (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower() -ne $Sha) {
      $part = "$zip.part"
      Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/actions/runner/releases/download/v$Version/actions-runner-win-x64-$Version.zip" -OutFile $part
      $got = (Get-FileHash $part -Algorithm SHA256).Hash.ToLower()
      if ($got -ne $Sha) { Remove-Item -Force $part; "SHA-MISMATCH want $Sha got $got"; exit 5 }
      Move-Item -Force $part $zip
      "DOWNLOADED $zip"
    }
    if (-not (Test-Path (Join-Path $Dir 'config.cmd')) -and @(Get-ChildItem -LiteralPath $Dir -Force -ErrorAction SilentlyContinue).Count -gt 0) {
      "PARTIAL $Dir -> $(Move-Aside $Dir (Join-Path (Split-Path $Dir) "retired-$(Get-Date -Format yyyyMMdd)"))"
    }
    if (-not (Test-Path (Join-Path $Dir 'config.cmd'))) {
      New-Item -ItemType Directory -Force -Path $Dir | Out-Null
      Add-Type -AssemblyName System.IO.Compression.FileSystem
      [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Dir)
      "EXTRACTED $Dir"
    }
    # each runner's own Go build cache and dotnet install dir, never shared; DOTNET_INSTALL_DIR because NETWORK SERVICE
    # cannot write C:\Program Files\dotnet, where actions/setup-dotnet installs by default
    $own = [ordered]@{ GOCACHE = "$Dir\_cache\go-build"; DOTNET_INSTALL_DIR = "$Dir\_cache\dotnet"; DOTNET_CLI_TELEMETRY_OPTOUT = '1' }
    $pathText = ($PathList -split ';' | Where-Object { $_ }) -join ';'
    if ($pathText) { $own.Insert(0, 'PATH', $pathText) }
    $envFile = Join-Path $Dir '.env'
    $lines = @(); if (Test-Path $envFile) { $lines = @(Get-Content $envFile) }
    $kept = @($lines | Where-Object { $_ -notmatch '^([A-Z_]+)=' -or -not $own.Contains($Matches[1]) })
    $new = @($own.Keys | ForEach-Object { "$_=$($own[$_])" }) + $kept
    $changed = $false
    if (($new -join "`n") -ne ($lines -join "`n")) {
      [IO.File]::WriteAllLines($envFile, [string[]]$new, (New-Object System.Text.UTF8Encoding $false)); $changed = $true
    }
    $pathFile = Join-Path $Dir '.path'
    if ($pathText -and (-not (Test-Path $pathFile) -or (Get-Content -Raw $pathFile).TrimEnd() -ne $pathText)) {
      [IO.File]::WriteAllText($pathFile, "$pathText`n"); $changed = $true
    }
    if ($changed) { "ENV-CHANGED $Dir" }
  }
  'register' {
    Need-Admin
    if (Test-Path (Join-Path $Dir '.runner')) {
      $to = Join-Path $Dir "retired-$(Get-Date -Format yyyyMMdd)"
      New-Item -ItemType Directory -Force -Path $to | Out-Null
      foreach ($f in '.runner', '.credentials', '.credentials_rsaparams', '.service') {
        $p = Join-Path $Dir $f
        if (Test-Path -LiteralPath $p) { Move-Item -Force -LiteralPath $p -Destination (Join-Path $to $f) }
      }
      $s = Service-Of $Dir
      if ($s) { Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue; & sc.exe delete $s.Name | Out-Null }
      "REPAIR $Dir -> $to"
    }
    # A directory made through WSL carries an ACL that is not in canonical form, and config.cmd's service step cannot
    # change it ("This access control list is not in canonical form"): put it back to the inherited ACL first.
    & icacls.exe $Dir /reset /T /C /Q | Out-Null
    if ($LASTEXITCODE -ne 0) { "ACL-RESET-FAILED $Dir rc=$LASTEXITCODE"; exit $LASTEXITCODE }
    $rc = Invoke-Config @('--unattended', '--url', $Url, '--name', $Name, '--labels', $Labels, '--work', '_work', '--replace', '--runasservice')
    if ($rc -ne 0) { "REGISTER-FAILED $Dir rc=$rc"; exit $rc }
    "REGISTERED $Name $Dir"
  }
  'ensure' {
    Need-Admin
    $s = Service-Of $Dir
    if (-not $s) { "NO-SERVICE $Dir"; exit 0 }
    $did = 'ok'
    if ($s.StartMode -ne 'Auto') { Set-Service -Name $s.Name -StartupType Automatic; $did = 'started' }
    if ($s.State -ne 'Running') { Start-Service -Name $s.Name; $did = 'started' }
    "SERVICE $Dir $did $($s.Name)"
  }
  'retire' {
    Need-Admin
    if (-not (Test-Path -LiteralPath $Dir)) { "ABSENT $Dir"; exit 0 }
    if (Test-Path (Join-Path $Dir '.runner')) {
      $rc = Invoke-Config @('remove', '--unattended')
      if ($rc -ne 0) { "RETIRE-FAILED $Dir rc=$rc"; exit $rc }
    }
    $s = Service-Of $Dir
    if ($s) { Stop-Service -Name $s.Name -Force -ErrorAction SilentlyContinue; & sc.exe delete $s.Name | Out-Null }
    if (Service-Of $Dir) { "SERVICE-LEFT $Dir"; exit 6 }
    Remove-Item -LiteralPath $Dir -Recurse -Force
    "RETIRED $Dir"
  }
  'stop' {
    Need-Admin
    $s = Service-Of $Dir
    if ($s) { Stop-Service -Name $s.Name -Force }
    "STOPPED $Dir"
  }
  'delete' {
    Need-Admin
    # One literal path, never a pattern, and never a drive root or a system folder: a v1 leftover is a folder below one.
    if ($Path -notmatch '^[A-Za-z]:\\[^\\*?]+(\\[^\\*?]+)*$' -or $Path -match '^[A-Za-z]:\\(Windows|Program Files|Program Files \(x86\)|ProgramData|Users)(\\|$)') {
      "REFUSED $Path (a literal path below a drive root, outside the system folders)"; exit 5
    }
    $i = Get-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
    if (-not $i) { "ABSENT $Path"; exit 0 }
    Remove-Item -LiteralPath $i.FullName -Recurse -Force; "DELETED $($i.FullName)"
  }
  'untask' {
    Need-Admin
    $t = Get-ScheduledTask -TaskPath '\' -TaskName $Task -ErrorAction SilentlyContinue
    if (-not $t) { "ABSENT \$Task"; exit 0 }
    Unregister-ScheduledTask -TaskPath '\' -TaskName $Task -Confirm:$false
    "UNTASKED \$Task"
  }
}
