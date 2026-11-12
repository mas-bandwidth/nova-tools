# nova-ci-rules on Windows: the same rules, verbs and lines as nova-ci-rules.sh (fleet/README.md, "CI rules").
#
# The runner runs job-started.ps1 before each job and job-completed.ps1 after it (ACTIONS_RUNNER_HOOK_JOB_STARTED and
# _COMPLETED in its .env); both call this script, which lives beside them in <runner dir>\.nova-ci-rules\.
#
#   started             on an administrator runner, every job but a push, schedule or workflow_dispatch job of
#                       refs/heads/main running the one workflow in admin-workflow is refused, as is any job while a
#                       folder above the runner is owned by an account ordinary jobs run as (Get-AdminRefusal);
#                       a push outside the allowed branches (push-branches) and tags is refused; then NOVA_CI_CHAIN_JOB_STARTED runs;
#                       then a push, pull_request or merge_group job gets a watchdog
#   completed           the watchdog is stopped; NOVA_CI_CHAIN_JOB_COMPLETED runs; a job the watchdog stopped fails
#   watchdog W DEADLINE (internal) at DEADLINE (unix seconds) kills every process under Runner.Worker W but the hooks
#   apply LIMIT BRANCHES [DIR..]
#                       (nova-fleet) puts the hooks, LIMIT and BRANCHES (the allowed push branch globs, one word, split
#                       at blanks) into each runner directory (DIR, or every actions.runner.* service) and restarts an
#                       idle runner whose .env changed
#
# The pure parts take their inputs as arguments (the unit tests call them with no clock):
#   push-ok REF, limited EVENT, overdue NOW DEADLINE, limit-text SECONDS,
#   ancestor PID NAME < "pid ppid name" lines, tree ROOT EXCLUDE < "pid ppid command" lines, env-edit HOOKDIR < .env
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$state = Join-Path $here 'state'
$limit = 120
if (Test-Path (Join-Path $here 'limit')) { $limit = [int](Get-Content -Raw (Join-Path $here 'limit')).Trim() }
$branches = 'main sprint/*'
if (Test-Path (Join-Path $here 'push-branches')) { $branches = (Get-Content -Raw (Join-Path $here 'push-branches')).Trim() }

# Test-PushOk REF: a tag, or a branch matching one of the globs in push-branches, where * and ? stop at a / and no
# segment is empty.
function Test-PushOk([string]$ref) {
    if ($ref -cmatch '^refs/tags/.+') { return $true }
    if ($ref -cnotmatch '^refs/heads/[^/]+(/[^/]+)*$') { return $false }
    $b = $ref.Substring('refs/heads/'.Length)
    foreach ($g in ($branches -split '\s+' | Where-Object { $_ })) {
        $re = '^' + (($g -split '/' | ForEach-Object { [regex]::Escape($_) -replace '\\\*', '[^/]*' -replace '\\\?', '[^/]' }) -join '/') + '$'
        if ($b -cmatch $re) { return $true }
    }
    return $false
}
function Test-Limited([string]$event) { return @('push', 'pull_request', 'merge_group') -ccontains $event }
function Test-Overdue([long]$now, [long]$deadline) { return $now -ge $deadline }
function Get-LimitText([int]$s) { if ($s % 60 -eq 0) { return "$($s / 60)m" } return "${s}s" }

# rows: objects with Id, Parent, Text (the command name for ancestor, the command line for tree) and Created (a
# number, may be 0 when unknown). Windows never reparents and reuses ids, so a row is a child of its Parent only when
# it was created after that parent: a process whose parent exited long ago is never taken for the worker's child.
function Test-ChildOf($r, $byId) {
    $p = $byId[[long]$r.Parent]
    if (-not $p) { return $false }
    return -not ([long]$r.Created -and [long]$p.Created -and [long]$r.Created -lt [long]$p.Created)
}
function Get-ById($rows) { $m = @{}; foreach ($r in $rows) { $m[[long]$r.Id] = $r }; return $m }
function Find-Ancestor([long]$start, [string]$name, $rows) {
    $byId = Get-ById $rows
    $r = $byId[$start]
    for ($i = 0; $i -lt 64 -and $r; $i++) {
        if (([string]$r.Text -replace '\.exe$', '').EndsWith($name)) { return [long]$r.Id }
        if (-not (Test-ChildOf $r $byId)) { break }
        $r = $byId[[long]$r.Parent]
    }
    return $null
}
# Everything under ROOT but the hooks (with all under them) and the worker's own console host.
function Get-Tree([long]$root, [string]$exclude, $rows) {
    $byId = Get-ById $rows
    $keep = @{ $root = $true }
    do {
        $more = $false
        foreach ($r in $rows) {
            $id = [long]$r.Id; $t = [string]$r.Text
            if ($keep.ContainsKey($id) -or -not $keep.ContainsKey([long]$r.Parent) -or -not (Test-ChildOf $r $byId)) { continue }
            if ($exclude -and $t.Contains($exclude)) { continue }
            if ([long]$r.Parent -eq $root -and $t -match 'conhost\.exe') { continue }
            $keep[$id] = $true; $more = $true
        }
    } while ($more)
    return @($rows | Where-Object { [long]$_.Id -ne $root -and $keep.ContainsKey([long]$_.Id) } | ForEach-Object { [long]$_.Id })
}
# Test-Gone PATH: a hook that names a file that is not there is never chained (it would fail before every job).
function Test-Gone([string]$p) {
    return -not ($p -and (Test-Path -LiteralPath $p -PathType Leaf))
}

# env-edit HOOKDIR: the .env with the two hooks pointing into HOOKDIR. A hook that pointed elsewhere moves to
# NOVA_CI_CHAIN_JOB_STARTED / _COMPLETED (unless one is already there), so it still runs, from ours; a chain (or a hook
# about to be chained) that Test-Gone names is dropped: the unchain rule.
function Edit-Env([string]$dir, [string[]]$lines) {
    $old = @{}; $chain = @{}; $out = New-Object System.Collections.Generic.List[string]
    $ours = [ordered]@{ STARTED = "$dir\job-started.ps1"; COMPLETED = "$dir\job-completed.ps1" }
    foreach ($l in $lines) {
        if ($l -match '^ACTIONS_RUNNER_HOOK_JOB_(STARTED|COMPLETED)=(.*)$') {
            $k, $v = $Matches[1], $Matches[2]
            if ($v -eq $ours[$k] -or -not (Test-Gone $v)) { $old[$k] = $v }
            continue
        }
        if ($l -match '^NOVA_CI_CHAIN_JOB_(STARTED|COMPLETED)=(.*)$') {
            $k, $v = $Matches[1], $Matches[2]
            if (Test-Gone $v) { continue }
            $chain[$k] = $true
        }
        $out.Add($l)
    }
    foreach ($k in $ours.Keys) {
        if ($old[$k] -and $old[$k] -ne $ours[$k] -and -not $chain[$k]) { $out.Add("NOVA_CI_CHAIN_JOB_$k=$($old[$k])") }
    }
    foreach ($k in $ours.Keys) { $out.Add("ACTIONS_RUNNER_HOOK_JOB_$k=$($ours[$k])") }
    return $out.ToArray()
}

function Get-Rows([string]$what) {
    Get-CimInstance Win32_Process | ForEach-Object {
        [pscustomobject]@{ Id = $_.ProcessId; Parent = $_.ParentProcessId; Text = $(if ($what -eq 'name') { $_.Name } else { [string]$_.CommandLine })
            Created = $(if ($_.CreationDate) { $_.CreationDate.ToUniversalTime().Ticks } else { 0 }) }
    }
}
function Get-Now { [DateTimeOffset]::UtcNow.ToUnixTimeSeconds() }
# Invoke-Chain HOOK: the hook the .env held before ours, in its own process as the runner would run it; its exit code.
function Invoke-Chain([string]$hook) {
    if (-not $hook) { return 0 }
    Write-Host "CI rule: running the chained hook $hook"
    $shell = (Get-Process -Id $PID).Path
    if ($hook -like '*.ps1') { & $shell -NoProfile -ExecutionPolicy Bypass -File $hook | Write-Host }
    else { & bash -e $hook | Write-Host }
    return $LASTEXITCODE
}
# Invoke-Disarm: stop a watchdog left from an earlier job (checked by its command line) and forget its verdict.
function Invoke-Disarm {
    $pidFile = Join-Path $state 'watchdog.pid'
    if (Test-Path $pidFile) {
        $w = [long](Get-Content -Raw $pidFile).Trim()
        $p = Get-CimInstance Win32_Process -Filter "ProcessId = $w"
        if ($p -and ([string]$p.CommandLine).Contains('nova-ci-rules.ps1') -and ([string]$p.CommandLine).Contains('watchdog')) {
            Stop-Process -Id $w -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -Force $pidFile
    }
}

# Add-AdminApi: NovaCiAdmin, an administrator runner's rules on the job (the tests compile it on its own, with fake inputs).
function Add-AdminApi {
    if ('NovaCiAdmin' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
public static class NovaCiAdmin {
  // The SIDs every ordinary runner's job, or any signed-in account, acts as: NETWORK SERVICE, SERVICE, LOCAL,
  // Authenticated Users, Users, Everyone.
  public static readonly string[] Openers = { "S-1-5-20", "S-1-5-6", "S-1-2-0", "S-1-5-11", "S-1-5-32-545", "S-1-1-0" };
  // Refuse: null when an administrator runner may run this job, else why. Only a push, schedule or workflow_dispatch
  // job of refs/heads/main, and only of the one workflow want names (owner/repo/.github/workflows/<file>@refs/heads/main;
  // owner and repository compared without case, as GitHub does, the rest exactly); no want refuses every job.
  public static string Refuse(string evt, string gitRef, string workflowRef, string want) {
    if (string.IsNullOrEmpty(want)) { return "this administrator runner has no allowed workflow (.nova-ci-rules\\admin-workflow); run the windows-runners play"; }
    if (evt != "push" && evt != "schedule" && evt != "workflow_dispatch") { return "an administrator runner runs push, schedule and workflow_dispatch jobs only, not " + evt; }
    if (gitRef != "refs/heads/main") { return "an administrator runner runs jobs of refs/heads/main only, not " + gitRef; }
    string got = workflowRef ?? "";
    int a = want.IndexOf("/.github/", StringComparison.Ordinal);
    int b = got.IndexOf("/.github/", StringComparison.Ordinal);
    if (a < 0 || b < 0 || !string.Equals(want.Substring(0, a), got.Substring(0, b), StringComparison.OrdinalIgnoreCase) || want.Substring(a) != got.Substring(b)) {
      return "this administrator runner runs " + want + " only, not " + got;
    }
    return null;
  }
  // Planted: the first of the folders paths whose owner (owners, in the same order; empty where it could not be read)
  // is one of Openers, or unknown on a folder that is not a hidden system one (systemHidden; System Volume Information
  // is one), as "<path> owner=<sid>|unreadable"; else null. A folder some job made above the runner (C:\ lets any
  // account make one) may hold a configuration its builds read as they walk up from the checkout.
  public static string Planted(string[] paths, string[] owners, bool[] systemHidden) {
    for (int i = 0; i < paths.Length; i++) {
      if (string.IsNullOrEmpty(owners[i])) {
        if (systemHidden[i]) { continue; }
        return paths[i] + " owner=unreadable";
      }
      if (Array.IndexOf(Openers, owners[i]) >= 0) { return paths[i] + " owner=" + owners[i]; }
    }
    return null;
  }
}
'@
}
# Get-AdminRefusal: on an administrator runner (its .nova-ci-rules holds admin-workflow, or this hook runs as an
# administrator), why this job may not run there, else nothing; on any other runner, nothing. The rule lives here, in
# the runner's sealed directory, never in the workflow it guards.
function Get-AdminRefusal {
    $wf = Join-Path $here 'admin-workflow'
    $want = if (Test-Path -LiteralPath $wf) { (Get-Content -Raw -LiteralPath $wf).Trim() } else { '' }
    $me = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    if (-not $want -and -not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { return $null }
    Add-AdminApi
    $why = [NovaCiAdmin]::Refuse("$env:GITHUB_EVENT_NAME", "$env:GITHUB_REF", "$env:GITHUB_WORKFLOW_REF", $want)
    if ($why) { return $why }
    $paths = New-Object System.Collections.Generic.List[string]
    $owners = New-Object System.Collections.Generic.List[string]
    $sh = New-Object System.Collections.Generic.List[bool]
    for ($d = Split-Path $here; $d; $d = Split-Path $d) {
        # a folder above that cannot be listed is refused, never passed
        try { $list = @(Get-ChildItem -LiteralPath $d -Directory -Force -ErrorAction Stop) }
        catch { return "$d is above this administrator runner and could not be listed ($($_.Exception.GetType().Name)); the fleet admin looks" }
        foreach ($c in $list) {
            $o = ''
            try { $o = (Get-Acl -LiteralPath $c.FullName).GetOwner([Security.Principal.SecurityIdentifier]).Value } catch { $o = '' }
            $paths.Add($c.FullName); $owners.Add($o)
            $sh.Add(($c.Attributes -band [IO.FileAttributes]'Hidden, System') -eq [IO.FileAttributes]'Hidden, System')
        }
    }
    $p = [NovaCiAdmin]::Planted($paths.ToArray(), $owners.ToArray(), $sh.ToArray())
    if ($p) { return "$p is above this administrator runner and was made by an account any job runs as; the fleet admin removes it" }
    return $null
}

function Invoke-Started {
    # a check that throws is a refusal, never a pass
    try { $why = Get-AdminRefusal }
    catch { $why = "the administrator runner check failed ($($_.Exception.Message)); the fleet admin looks" }
    if ($why) {
        "::error::CI rule: $why"
        # A failed job-started hook fails the job but does not stop it: the runner adds the hook as a pre-job step, and
        # a step with if: always() or failure() still runs after it, here as an administrator. So the job's
        # Runner.Worker is ended now, before any step of the job runs; a process listing that fails is tried again.
        $worker = $null
        for ($i = 0; $i -lt 3 -and -not $worker; $i++) {
            try { $worker = Find-Ancestor $PID 'Runner.Worker' (Get-Rows 'name') } catch { $worker = $null }
        }
        if ($worker) { Stop-Process -Id $worker -Force -ErrorAction SilentlyContinue }
        else { "::error::CI rule: no Runner.Worker above this hook was found to end, so the job's always() steps may still run; the fleet admin looks" }
        exit 1
    }
    Invoke-Disarm
    Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $state 'fired')
    $event = "$env:GITHUB_EVENT_NAME"; $ref = "$env:GITHUB_REF"
    if ($event -eq 'push' -and -not (Test-PushOk $ref)) {
        $list = @($branches -split '\s+' | Where-Object { $_ })
        "::error::CI rule: push CI runs only on $($list -join ', ') and tags, not $ref; set on.push.branches to [$(($list | ForEach-Object { "'$_'" }) -join ', ')] and let this branch get CI through its pull request"
        exit 1
    }
    $rc = Invoke-Chain "$env:NOVA_CI_CHAIN_JOB_STARTED"
    if ($rc) { exit $rc }
    if (-not (Test-Limited $event)) { "CI rule: a $event job has no time limit"; exit 0 }
    $worker = Find-Ancestor $PID 'Runner.Worker' (Get-Rows 'name')
    if (-not $worker) {
        "::error::CI rule: no Runner.Worker above this hook, so the $(Get-LimitText $limit) limit cannot be enforced; tell the fleet admin"
        exit 1
    }
    New-Item -ItemType Directory -Force -Path $state | Out-Null
    $deadline = (Get-Now) + $limit
    $shell = (Get-Process -Id $PID).Path
    $w = Start-Process -FilePath $shell -WindowStyle Hidden -PassThru -ArgumentList @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $here 'nova-ci-rules.ps1'), 'watchdog', $worker, $deadline)
    Set-Content -Path (Join-Path $state 'watchdog.pid') -Value $w.Id
    "CI rule: a $event job finishes in $(Get-LimitText $limit); longer work belongs in a nightly (schedule) workflow"
    exit 0
}

function Invoke-Completed {
    Invoke-Disarm
    $rc = Invoke-Chain "$env:NOVA_CI_CHAIN_JOB_COMPLETED"
    $fired = Join-Path $state 'fired'
    if (Test-Path $fired) {
        Remove-Item -Force $fired
        "::error::CI rule: per-commit jobs finish in $(Get-LimitText $limit); move longer work to a nightly (schedule) workflow"
        exit 1
    }
    exit $rc
}

function Invoke-Watchdog([long]$worker, [long]$deadline) {
    while (Get-Process -Id $worker -ErrorAction SilentlyContinue) {
        if (Test-Overdue (Get-Now) $deadline) {
            $fired = Join-Path $state 'fired'
            if (-not (Test-Path $fired)) { Set-Content -Path $fired -Value "watchdog: $worker passed its deadline $deadline" }
            foreach ($id in (Get-Tree $worker $here (Get-Rows 'command'))) { Stop-Process -Id $id -Force -ErrorAction SilentlyContinue }
        }
        Start-Sleep -Seconds 1
    }
}

# apply LIMIT BRANCHES [DIR..]: one line per runner, CI-RULES <dir> hooks=<ok|set> restart=<no|done|busy|stopped|failed>.
function Invoke-Apply([int]$lim, [string]$br, [string[]]$dirs) {
    if (-not $br) { [Console]::Error.WriteLine('nova-ci-rules: apply wants LIMIT BRANCHES [DIR..]'); exit 2 }
    $services = @(Get-CimInstance Win32_Service | Where-Object { $_.Name -like 'actions.runner.*' } | ForEach-Object {
            [pscustomobject]@{ Name = $_.Name; Dir = (Split-Path (Split-Path ($_.PathName.Trim('"')))) }
        })
    if (-not $dirs) { $dirs = @($services | ForEach-Object { $_.Dir } | Sort-Object -Unique) }
    $bad = 0
    foreach ($dir in $dirs) {
      try {
        $hd = Join-Path $dir '.nova-ci-rules'
        New-Item -ItemType Directory -Force -Path $hd | Out-Null
        $hooks = 'ok'
        foreach ($f in 'nova-ci-rules.ps1', 'job-started.ps1', 'job-completed.ps1') {
            $to = Join-Path $hd $f
            if (-not (Test-Path $to) -or (Get-FileHash (Join-Path $here $f)).Hash -ne (Get-FileHash $to).Hash) {
                Copy-Item -Force (Join-Path $here $f) $to; $hooks = 'set'
            }
        }
        $lf = Join-Path $hd 'limit'
        if (-not (Test-Path $lf) -or (Get-Content -Raw $lf).Trim() -ne "$lim") { Set-Content -Path $lf -Value $lim; $hooks = 'set' }
        $bf = Join-Path $hd 'push-branches'
        if (-not (Test-Path $bf) -or (Get-Content -Raw $bf).Trim() -ne $br) { Set-Content -Path $bf -Value $br; $hooks = 'set' }
        $envFile = Join-Path $dir '.env'
        $lines = @(); if (Test-Path $envFile) { $lines = @(Get-Content $envFile) }
        $new = Edit-Env $hd $lines
        if (($new -join "`n") -ne ($lines -join "`n")) {
            [IO.File]::WriteAllLines($envFile, [string[]]$new, (New-Object System.Text.UTF8Encoding $false)); $hooks = 'set'
        }
        $applied = Join-Path $hd 'env.applied'
        $now = (Get-Content -Raw $envFile)
        $r = 'no'
        if (-not ((Test-Path $applied) -and (Get-Content -Raw $applied) -eq $now)) {
            $svc = $services | Where-Object { $_.Dir -eq $dir } | Select-Object -First 1
            $listener = Get-CimInstance Win32_Process -Filter "Name = 'Runner.Listener.exe'" | Where-Object { $_.ExecutablePath -eq (Join-Path $dir 'bin\Runner.Listener.exe') } | Select-Object -First 1
            if (-not $svc -or -not $listener) { $r = 'stopped'; Set-Content -Path $applied -Value $now -NoNewline }
            elseif (Get-CimInstance Win32_Process -Filter "ParentProcessId = $($listener.ProcessId) AND Name = 'Runner.Worker.exe'") { $r = 'busy' }
            else {
                try {
                    Restart-Service -Name $svc.Name -Force
                    $exe = Join-Path $dir 'bin\Runner.Listener.exe'
                    $seen = $false
                    for ($i = 0; $i -lt 60 -and -not $seen; $i++) {
                        $seen = [bool](Get-CimInstance Win32_Process -Filter "Name = 'Runner.Listener.exe'" | Where-Object { $_.ExecutablePath -eq $exe -and $_.ProcessId -ne $listener.ProcessId })
                        if (-not $seen) { Start-Sleep -Seconds 1 }
                    }
                    if (-not $seen) { throw 'no new listener' }
                    $r = 'done'; Set-Content -Path $applied -Value $now -NoNewline
                }
                catch { $r = 'failed'; $bad = 1 }
            }
        }
        "CI-RULES $dir hooks=$hooks restart=$r"
      }
      catch { "CI-RULES $dir denied=$($_.Exception.Message -replace '\s+', ' ')"; $bad = 1 }
    }
    exit $bad
}

$verb = if ($args.Count -gt 0) { $args[0] } else { '' }
$rest = @($args | Select-Object -Skip 1)
switch -CaseSensitive ($verb) {
    'started' { Invoke-Started }
    'completed' { Invoke-Completed }
    'watchdog' { Invoke-Watchdog ([long]$rest[0]) ([long]$rest[1]) }
    'apply' { Invoke-Apply ([int]$rest[0]) $rest[1] @($rest | Select-Object -Skip 2) }
    'push-ok' { if (Test-PushOk $rest[0]) { exit 0 } exit 1 }
    'limited' { if (Test-Limited $rest[0]) { exit 0 } exit 1 }
    'overdue' { if (Test-Overdue ([long]$rest[0]) ([long]$rest[1])) { exit 0 } exit 1 }
    'limit-text' { Get-LimitText ([int]$rest[0]) }
    'ancestor' {
        $rows = @($input | ForEach-Object { $f = "$_".Trim() -split '\s+', 3; [pscustomobject]@{ Id = $f[0]; Parent = $f[1]; Text = $f[2] } })
        $a = Find-Ancestor ([long]$rest[0]) $rest[1] $rows; if ($a) { $a; exit 0 } exit 1
    }
    'tree' {
        $rows = @($input | ForEach-Object { $f = "$_".Trim() -split '\s+', 3; [pscustomobject]@{ Id = $f[0]; Parent = $f[1]; Text = $f[2] } })
        Get-Tree ([long]$rest[0]) $rest[1] $rows
    }
    'env-edit' { Edit-Env $rest[0] @($input) }
    default { [Console]::Error.WriteLine('nova-ci-rules: want started, completed, watchdog, apply, push-ok, limited, overdue, limit-text, ancestor, tree or env-edit'); exit 2 }
}
