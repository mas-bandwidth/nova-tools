# The Windows bench standard

> **PARKED 2026-09-18. Glenn: "drop the native windows CI runners. WSL only from
> now on." WSL2 is the way.**
>
> This page was written for a NATIVE Windows bench and a host of self-hosted
> **Windows** CI runners. Neither is being built. The Threadripper Pro joins the
> fleet under **WSL2**, as a Linux bench with Linux CI runners labelled
> `linux,X64,threadripper` — registry line `threadripper-wsl … linux/x64
> bench,runner`, the Linux `tools/bench-standard.sh`, the Linux `fleet standard`
> list, and nothing on this page. See `docs/spec-pulse/03-fleet.md`, "The runners
> are Linux runners, everywhere, including on the Windows box".
>
> `ci.yml` dropped every `windows-latest` leg in the same ruling; the four class
> tests that held them shut are parked in `docs/SPEC-CI.md` under "Parked class
> tests", and what remains on the CL path is one cross-vet, `make vet-windows`.
> `nova-update release build --platform windows-amd64` still builds — the code is
> on `dev` via #1386 — but the fleet release no longer ships the platform, and
> #1410 is parked with it.
>
> **The ruling overrides `W11` below**, the *"NEVER WSL"* rule this document is
> written around: the argument there is that WSL2 brings a second operating
> system, a second filesystem and a second toolchain to every friend on Windows,
> and that containment which only holds inside WSL is containment somewhere else.
> That argument is not refuted, it is OVERRULED — the fleet does not want a
> Windows bench enough to pay for a second sandbox implementation, and the box we
> have is worth more as 32 Linux cores than as the first native Windows one.
> Everything below stands as the record of what a native Windows bench would have
> needed, and is the page to read first if one is ever wanted again. Nothing
> below is a live requirement.

Everything a Windows machine needs before the loop may put work on it: the
Windows half of `tools/bench-standard.sh` and of the provisioning standard in
`docs/spec-pulse/03-fleet.md`, written **before the bench arrives** so that it is
not designed under fire.

The machine this is written for is the Threadripper Pro Glenn is setting up: one
box carrying two roles, a card **bench** and a host for self-hosted **Windows CI
runners**. It is the fleet's first Windows machine, so nothing here has been
measured on one yet. Every section says what it stands on, and what will prove
it.

### Where the Windows rules live, today

`W1`…`W12` are the *Windows — the disposable place* section of
`docs/SPEC-SANDBOX.md`, **draft 2, in PR #1337** — read by the security lane
(`johnny-860d359211aa`, 2026-09-18) and not yet on `dev`. `nova-sandbox run` on
Windows is PR #1357, unproven on real Windows. Section 8 quotes the rules so this
page stands on its own; when #1337 lands, that section of the spec is the
authority and this one is the bench's side of it.

What is on `dev` today is the Windows **wall**: `## Windows — AppContainer, no
admin`, rules 0–4 — `--name <container>` required, `CreateAppContainerProfile`,
`--acl tool|caller`, launch through `PROC_THREAD_ATTRIBUTE_SECURITY_CAPABILITIES`
+ `CreateProcessW`, ACE cleanup. That section's *"Not chosen, and why"* rejects
both a Job Object ("no filesystem scope") and Windows Sandbox ("a VM per run and
Pro/Enterprise only"). Both rejections stand **as the wall**, and #1337 is not a
reversal of them: it brings them back as **the place**, which is the different
question of *create → run → kill → delete*. The wall is still AppContainer.

> **NEVER WSL.** Not as the wall, not as the place, not as a fallback, not "just
> for the toolchain". This is rule **W11** of the Windows section and it is
> the whole reason this document exists rather than a page saying "install WSL2
> and follow the Linux standard". WSL2 brings a second operating system, a second
> filesystem, a second toolchain and a second set of credentials to every friend
> on Windows, and containment that only holds inside WSL is not containment on
> Windows — it is containment somewhere else, on a machine the card was not sent
> to. A Windows bench that cannot do something natively **refuses**; it does not
> reach sideways into Linux.

---

## 0. What the standard is, and how it is checked

The Linux standard is `tools/bench-standard.sh`: it prints `STANDARD OK …`, or
one `DRIFT <what>` line per finding followed by `STANDARD DRIFT (see lines
above)`. `nova-pulse fleet standard` is that standard **as data** —
`internal/pulse/fleetstandard.go` holds one `[]StandardCheck` table per operating
system, the remote side prints `CHECK<TAB>name<TAB>value` and nothing else, and
the verdict is decided in Go.

**There is no `windows` table yet.** `FleetStandardChecks` carries a `linux` list
and a `darwin` list; `StandardCheck.OS` is a string field and a third value costs
nothing structurally, but every `Probe` in the table is *one line of POSIX
shell*, and that is the real work of adding Windows: either the probes are
written for `cmd.exe`, or the machine answers a PowerShell script. Section 9 says
which, and why.

Until that table exists, a Windows bench is certified by hand against this
document, the way the fleet was on 2026-09-18.

---

## 1. The local user

One local, **non-administrator** user named `nova`. It owns the bench's work, the
runner services' working directories, and nothing else.

```powershell
New-LocalUser -Name nova -Description "the fleet's bench account" -NoPassword:$false
# NOT Add-LocalGroupMember -Group Administrators -Member nova. See section 2.
```

`nova` is deliberately **not** in `Administrators`:

- **The `administrators_authorized_keys` trap.** `C:\ProgramData\ssh\sshd_config`
  ships with two `Match Group administrators` lines at the bottom that override
  `AuthorizedKeysFile` for every member of that group and point it at
  `C:\ProgramData\ssh\administrators_authorized_keys`. Put `nova` in
  `Administrators` and its key in `C:\Users\nova\.ssh\authorized_keys` and the key
  is **silently ignored** — sshd reads a file nobody wrote to. It is silent
  because a public-key failure that falls through to the next method looks
  exactly like no key at all.
- **`nova-sandbox` rule 2 forbids elevation**: *"No dedicated users, no root, no
  admin, no VM. Every backend chosen here is usable by an ordinary unprivileged
  user in their own session"* (`docs/SPEC-SANDBOX.md`, Glenn's ruling). A bench
  account with administrator rights hides every place the tool would have needed
  them — which is precisely what W6 refuses to hide (`--size` is
  `reason=size_unenforceable`, not a quota quietly created with rights a real run
  will not have).
- Local administrator rights are needed exactly **twice**: installing the
  OpenSSH server and the Windows features (section 8), and installing the runner
  as a service (section 7). Both are one-time, done from a separate admin
  session, and neither leaves `nova` elevated.

---

## 2. OpenSSH Server

Every fleet verb reaches a machine over `ssh` from `--ssh`, so sshd is the first
thing on and the last thing to break.

```powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Set-Service -Name sshd -StartupType Automatic
Start-Service sshd
New-NetFirewallRule -Name sshd -DisplayName "OpenSSH Server (sshd)" `
  -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22
```

`sshd` **Automatic**, not "Automatic (Delayed Start)": a bench that wakes from
suspend (section 6) and is not reachable for two minutes is a bench the loop has
already given up on. The wake path and the ssh path must come up together.

### `DefaultShell`: use `cmd.exe` — that is, leave it unset

`HKLM:\SOFTWARE\OpenSSH\DefaultShell` chooses the shell sshd hands a session.
**Do not set it.** The default is `cmd.exe`, and for a runner host that is the
right answer:

- **The exit status survives.** `cmd.exe` returns the command's own exit code.
  PowerShell does not: a native command that fails leaves `$LASTEXITCODE` set but
  the *session* exits 0 unless the script checks it. `nova-pulse fleet standard`
  and `fleet certify` both decide on what the machine SAID and on its status;
  a shell that turns every failure into 0 breaks the half that is a status.
- **The stream stays clean.** The protocol between the fleet verbs and a machine
  is `CHECK<TAB>name<TAB>value` and nothing else. PowerShell writes a banner
  unless `-NoLogo`, emits progress records as ANSI escapes on a non-interactive
  stream, wraps anything a native command writes to stderr in a multi-line red
  `NativeCommandError` object, and — worst — its default output encoding is not
  the console's, so a tab-separated line comes back with a BOM or as UTF-16. Each
  of those turns a one-line remedy into something the Go side cannot parse.
- **Quoting stops being a guess.** With `DefaultShell` set to `pwsh.exe`, an
  argv sent as `ssh host <command>` is re-parsed by PowerShell's own rules, so a
  command composed for POSIX or for `cmd` is mangled differently depending on a
  registry value on the far end. One argv for three platforms is the point.

PowerShell is much better for the *provisioning* in this document, so it is
called **explicitly** where it is wanted, exactly as the Linux side calls
`bash -s`:

```
powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:\nova\probe.ps1
```

`-NoProfile` because a profile is a second configuration nobody audits;
`-NonInteractive` because a prompt on a machine with nobody at it is a hang, not
a question.

`pwsh.exe` (PowerShell 7) may be installed and called by name for a script that
needs it. It is never `DefaultShell`.

### The `ssh -n` trap

From the fleet's own hurt (2026-09-17): `ssh -n host bash -s < script` runs
**nothing** — `-n` redirects stdin from `/dev/null` and the script never arrives.
The same is true of `ssh -n host powershell -Command -`. Anything that feeds a
script on stdin must not pass `-n`.

### `authorized_keys`

`nova` is not an administrator (section 1), so the ordinary per-user path is the
one sshd reads:

```
C:\Users\nova\.ssh\authorized_keys
```

Its ACL must give **no write access to anyone but `nova` and `SYSTEM`**, with
inheritance disabled, or sshd refuses it and says so only in the sshd event log:

```powershell
$acl = Get-Acl C:\Users\nova\.ssh\authorized_keys
$acl.SetAccessRuleProtection($true, $false)   # break inheritance, drop inherited ACEs
$acl.Access | ForEach-Object { $acl.RemoveAccessRule($_) | Out-Null }
$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule("nova","FullControl","Allow")))
$acl.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule("SYSTEM","FullControl","Allow")))
Set-Acl C:\Users\nova\.ssh\authorized_keys $acl
```

If a key must be installed for an administrative account, it goes in
`C:\ProgramData\ssh\administrators_authorized_keys` with the same ACL restricted
to `Administrators` and `SYSTEM` — and that file is the *only* place sshd will
look for one. Knowing which of the two files applies is a property of group
membership, not of where the key was put.

---

## 3. Go

The version is **`go.mod`'s `go` line**, not "whatever is current". `go.mod` says
`go 1.26`, so the fleet is on `go1.26.5` — the same `$NOVA_GO` the Linux and
darwin standards default to.

This is not cosmetic. A toolchain below `go.mod`'s line does not fail loudly: it
refuses the module by name, in one line, and only if something is watching. That
is the fault `wall-toolchain` exists for — hulk silently compiled a go1.26 module
with go1.22 and said so in a line nobody read.

Install **under `C:\sdk\go1.26.5`**, mirroring `~/sdk/go1.26.5` on Linux and
darwin, so one path shape describes the whole fleet:

```powershell
# the zip, not the msi: the msi installs to C:\Program Files\Go and there is
# then no way to hold two versions during an upgrade.
Expand-Archive .\go1.26.5.windows-amd64.zip -DestinationPath C:\sdk\staging
Move-Item C:\sdk\staging\go C:\sdk\go1.26.5
```

### PATH: the **Machine** PATH, never the user profile

```powershell
$p = [Environment]::GetEnvironmentVariable('Path','Machine')
[Environment]::SetEnvironmentVariable('Path', "C:\sdk\go1.26.5\bin;$p", 'Machine')
```

`'Machine'` is `HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment`.
`'User'` is `HKCU\Environment` for whoever is setting it. The distinction is the
whole reason three of the fleet's Linux machines had no `go` at all over a plain
ssh while a login shell on each of them had one:

- **A Windows service gets the Machine environment plus its own account's.** The
  runner (section 7) runs as a service. If it runs under `NT AUTHORITY\NETWORK
  SERVICE`, a virtual account, or `LocalSystem`, `nova`'s `HKCU\Environment` is
  **never read**, so a `Path` set on the user profile is invisible to every CI
  shard the machine will ever take.
- **An sshd session is not an interactive logon.** It composes an environment
  from the machine and user registry at session creation; it runs no profile
  script, so anything a `$PROFILE` or a `setx` in someone's interactive session
  added is not there.
- **Services cache their environment block at start.** Editing the Machine
  `Path` does *not* reach a running service — the `WM_SETTINGCHANGE` broadcast
  only reaches processes with a message loop on the desktop. **Restart `sshd` and
  every runner service after changing it**, or the machine will pass an
  interactive check and fail every real one.

This is the Windows equivalent of the `.path` file trap the `runner-path`
workload catches on Linux: on Linux a runner reads its `PATH` from the `.path`
file beside it and from no shell; on Windows it reads it from the service's
environment block and from no shell. Both are invisible to every check that runs
in a shell.

Verify the way a card will see it, not the way a person does:

```
ssh nova@<bench> "where go && go version"        # a non-interactive session
```

---

## 4. git

```powershell
winget install --id Git.Git -e --source winget    # or the standalone installer
```

Then, as `nova`:

```
git config --global user.name  "<the bench's name>"
git config --global user.email "<seat>@mas-bandwidth.com"
git config --global core.autocrlf false
git config --global core.longpaths true
```

- **`user.name`/`user.email` are part of the standard**, not a nicety:
  `git-identity` is a certify class because all four Linux machines had them
  empty, and a `git-push` workload (and any card that commits) dies at the commit
  rather than at the push.
- **`core.autocrlf false`** — a bench that rewrites line endings on checkout
  produces a diff against every other machine in the fleet, and every
  byte-for-byte test in this repo goes red on the Windows leg only.
- **`core.longpaths true`** plus the machine-wide long-path switch below: Go's
  module cache under `C:\Users\nova\go\pkg\mod` exceeds `MAX_PATH` routinely, and
  the failure is a confusing "cannot find the path specified" on an existing path.

```powershell
Set-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\FileSystem' `
  -Name LongPathsEnabled -Value 1 -Type DWord
```

---

## 5. The actions runner, as a service

```
C:\actions-runner-<name>-1\config.cmd --url https://github.com/mas-bandwidth/nova-tools `
  --token <registration token> --name <machine>-nova-1 `
  --labels windows,X64,<machine> --work _work --runasservice --windowslogonaccount nova
```

- **`--runasservice`.** A runner started from a console dies with the console,
  and a machine that stops taking shards after a reboot is a machine that will be
  discovered by a stalled merge queue. The service is created with start type
  Automatic; check it with `Get-Service "actions.runner.*"`.
- **`--labels windows,X64,<name>`** — the same three-part shape the Linux and
  darwin runners carry, so `runs-on` selects a platform, an architecture or one
  named machine, and adding a second Windows box later changes nothing in the
  workflow.
- **One runner directory per runner**, numbered, matching the Linux
  `runner-nova-tools-<i>` convention so one glob finds them all.
- **The service account is `nova`** (`--windowslogonaccount`), which is why
  section 3 puts Go on the Machine PATH: the service reads the machine
  environment and `nova`'s, not an interactive profile.

### `_diag` grows, and it is a WARN

`diag-size` is a certify class because the fleet accumulated 15.7 GB of `_diag`
at ~1.8 GB/day. Windows runners do the same thing in
`C:\actions-runner-*\_diag`. It gates nothing — it is a WARN on purpose — but a
bench that fills its system volume stops being a bench.

### After a PATH change

Restart the runner services, or the shards keep the environment the machine had
when the service started (section 3).

---

## 6. Wake on magic packet

The fleet runs on solar (Glenn, 2026-09-17): idle benches suspend and wake on
demand, so a Windows bench that cannot be woken is a Windows bench that must stay
on, and that is the whole saving lost.

Three settings, all of which must be right, in the NIC's power settings:

```powershell
$nic = (Get-NetAdapter -Physical | Where-Object Status -eq 'Up').Name
Set-NetAdapterPowerManagement -Name $nic -WakeOnMagicPacket Enabled -WakeOnPattern Disabled
Set-NetAdapterAdvancedProperty -Name $nic -RegistryKeyword '*WakeOnMagicPacket' -RegistryValue 1
# Device Manager → the NIC → Power Management:
#   [x] Allow this device to wake the computer
#   [x] Only allow a magic packet to wake the computer
```

`WakeOnPattern Disabled` is deliberate: pattern wake means any unicast on the
segment wakes the box, which on a busy network is a machine that never sleeps.

**Turn Fast Startup off.** It is the single most common reason wake-on-LAN "works
on Linux and not on Windows": with Fast Startup a shutdown is a hybrid hibernate,
and most NIC drivers drop the wake-armed state on the way into it, so the machine
can be woken from sleep but never from a shutdown.

```powershell
powercfg /h off          # also disables Fast Startup
powercfg /a              # what sleep states this machine actually has
```

Record the NIC's MAC in the machine's registry row's notes (section 10) — the
magic packet is addressed to it, and hunting for it needs the machine awake.

---

## 7. Windows features

Three, all requiring Windows **Pro or Enterprise** and virtualization enabled in
firmware:

```powershell
Enable-WindowsOptionalFeature -Online -All -NoRestart -FeatureName Containers-DisposableClientVM  # Windows Sandbox
Enable-WindowsOptionalFeature -Online -All -NoRestart -FeatureName Microsoft-Hyper-V-All
Enable-WindowsOptionalFeature -Online -All -NoRestart -FeatureName Containers
Restart-Computer
```

- **Windows Sandbox** (`Containers-DisposableClientVM`) is `nova-sandbox
  --place wsb` (W8), and it is **only** for the opt-in. Without the feature,
  `--place wsb` is `SANDBOX REFUSED reason=no_wsb` at 125, naming the edition and
  the feature — which is correct behaviour, not a bug, and is why the feature is
  part of the standard rather than a guess the tool makes. Rule 2 (*no admin, no
  VM*) is exactly why **`--place job` is the default**: the default place needs
  no optional feature, no Hyper-V and no elevation, so a machine that has lost a
  feature can still take cards. Enabling the feature here buys the review place
  (W9) and buys nothing else.
- **Hyper-V** is what Windows Sandbox is built on. Note that enabling it takes
  the machine's hypervisor: VMware Workstation and VirtualBox will run degraded
  or not at all afterwards. On a bench that is the right trade.
- **Containers** is for the container work the roadmap has for the pull worker
  (`allow-shared` in the registry exists to be retired when the worker runs cards
  in containers).

---

## 8. What `nova-sandbox` expects — W1 … W12

The *Windows — the disposable place* section (draft 2, PR #1337 — see *Where the
Windows rules live* above) is the contract, implemented by PR #1357. **No rule of
it has been measured on a Windows bench**; this machine is the first that can.
The contract itself does not change across platforms: the same five steps (look,
create, run, **kill**, delete), the same `SANDBOX DONE name=<n> exit=<code>
wall=<s> freed=<bytes>` receipt, the same `SANDBOX LEAK` at exit 3, 124 on
`--timeout` and 125 for the tool's own refusals. What the machine must provide:

| rule | what the bench must be able to do |
|---|---|
| **W1** | make a **Job Object and a per-run scratch directory as one unit** — a job with no scratch leaves the run's files on the profile; a scratch with no job leaves a survivor holding a handle to the directory the tool is about to delete |
| **W2** | `CreateJobObjectW` + `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` set **before** any process is in the job, so closing the last handle kills the whole tree. `JOB_OBJECT_LIMIT_BREAKAWAY_OK` and `_SILENT_BREAKAWAY_OK` are **never** set — breakaway is exactly how a tree escapes the kill |
| **W3** | put the child in the job **at creation**: `PROC_THREAD_ATTRIBUTE_JOB_LIST` on the same attribute list that carries the AppContainer's `SECURITY_CAPABILITIES`, one `CreateProcessW` with `EXTENDED_STARTUPINFO_PRESENT`. `AssignProcessToJobObject` after the fact leaves a window in which a spawned grandchild is a survivor |
| **W4** | enforce `--memory` (`ProcessMemoryLimit`/`JobMemoryLimit`) and `--cpu` (`JOBOBJECT_CPU_RATE_CONTROL_INFORMATION`, hard cap). These are the **only** CPU/memory limits in the tool and they belong to the `run` verb alone; the bare wrapper refuses them as unknown flags |
| **W5** | `--scratch` is **required and absolute**, with no default — no `%TEMP%`, no `%USERPROFILE%`. `<scratch>\nova-<n>` is the run's only `--write`, holding `work`, `home` and the temp directory; the AppContainer SID is granted read+write on it and nothing else |
| **W6** | `--size` is **refused** (`reason=size_unenforceable`, 125): NTFS quotas are per user per volume, directory quotas are FSRM, and a VHDX needs elevation rule 2 forbids. The bench must therefore be able to offer a `--scratch` on a volume the caller has already sized, or `--place wsb` |
| **W7** | delete after the job is closed and the processes are reaped, retrying `ERROR_SHARING_VIOLATION`/`ERROR_ACCESS_DENIED`/`ERROR_DIR_NOT_EMPTY` for a bounded window — **Defender and the search indexer hold transient handles on files a run just wrote**. Only then `SANDBOX LEAK …` and exit 3 |
| **W8** | run `WindowsSandbox.exe <file>` from a per-run `.wsb` — needs the feature of section 7 and Pro/Enterprise |
| **W9** | **`--place` defaults to `job`; `wsb` is an explicit opt-in.** Windows Sandbox permits one instance per machine, so a pool of workers each wanting one is a queue of one. `wsb` is the review place — an untrusted branch, a first card image, an unread dependency — never the swarm's. A second is `reason=wsb_busy`, never a silent wait |
| **W10** | under `wsb`, carry the exit status back through `<scratch>\.nova-sandbox-exit`. A configuration that cannot write it **refuses before the VM starts**; a clean close is never reported as 0 |
| **W11** | **no path reaches WSL** — not `wsl`, not `wsl.exe`, not a `\\wsl$\` path, not as a fallback when a backend is missing. Missing backend ⇒ `reason=no_sandbox` with a remedy naming the edition or feature |
| **W12** | identity is `QueryFullProcessImageNameW` on the parent handle **plus** `IsProcessInJob`. There is no `proc_pidpath`, no inode and no device number, and a check written against one of those compiles and answers nothing |

Two bench-level consequences worth stating on their own:

- **Defender.** W7's retry window exists because of it. A Defender exclusion for
  the scratch root makes runs faster and leaks rarer, and is a defensible
  exception for a directory the tool creates and deletes per run. It is not a
  substitute for the retry.
- **A scratch root on a sized volume.** Because of W6 there is no per-run disk
  ceiling. The `--scratch` the bench is configured with should therefore live on
  a volume that is not the system volume, so a runaway run fills a partition and
  not `C:`.

---

## 9. What `fleet certify` will probe on Windows

`fleet survey` asks a machine what it **has**; `fleet certify` makes it **do** the
work its roles imply, under the same wall a card gets, and writes down that it
did. A workload is a file: front matter (`roles`, `expect`, `wall`, `reads`,
`forge`, `report`), a blank line, then the body the machine runs. The class is the
file's name.

The Windows equivalents of the classes that caught the 2026-09-18 faults:

| class | roles | what the Windows body must establish |
|---|---|---|
| `go-on-path` | bench, runner | `where.exe go` over a **non-interactive** ssh session resolves a Go at least `go.mod`'s line. Three Linux machines had no `go` at all non-interactively while every login shell had one; on Windows the same fault is a `Path` set on the user profile instead of the Machine one (section 3) |
| `runner-path` | runner | the Windows form of the `.path` fault. `Get-Service "actions.runner.*"` finds one service per runner directory, each `Automatic`; **one** `Runner.Listener.exe` per service (a double registration is a worse fault than an unmanaged runner, and the first Linux version of this check could not see it); and the Go that the **service's** environment block resolves — not the console's — is at least `go.mod`'s line |
| `wall-toolchain` | bench | build **and run** a two-file module **inside** `nova-sandbox --place job`, and assert the version. This is the hurt itself: hulk compiled a go1.26 module with go1.22 inside the wall and said so in one line nobody read. On Windows the read root at issue is `C:\sdk\go1.26.5` reachable by the AppContainer SID |
| `no-wsl` | bench, runner | **Windows-only.** W11's tripwire as a probe: no argv the tool composes names `wsl`, `wsl.exe` or a `\\wsl$\` path, and nothing on the loop's PATH resolves to one |
| `path-resolves` | all | every nova bin resolves on the non-interactive PATH, with no stale shadow ahead of it (`where.exe` prints every match in order; the first one wins, and on Linux there were 18 stale `~/go/bin` shadows per machine) |
| `git-identity` | bench | `user.name` and `user.email` are set for `nova` (section 4) |
| `sandbox-job` | bench | **Windows-only.** A run under `--place job` kills a grandchild when the job handle closes, and leaves nothing behind under `--scratch` (W1, W2, W7 as one probe — the three that make the place a place) |
| `diag-size` | runner | `C:\actions-runner-*\_diag` in MB, with the age of the oldest file and the growth rate. A **WARN** |
| `registry-truth` | all | what the machine is matches the registry row (section 10) — the roles, the `os/arch`, the runner count |

Three notes on writing them:

1. **`expect:` is a line the machine prints**, and the verdict is what the
   machine SAID, never the exit code alone. That matters more on Windows, where
   a shell can return 0 for a failed native command (section 2).
2. **A `wall: yes` workload naming no `reads:` is a refusal.** The Windows read
   roots are `C:\sdk`, `C:\Users\nova\go`, `C:\Program Files\Git`, and
   `%WINDIR%` / `%ProgramFiles%` — the last two are implicit
   (`docs/SPEC-SANDBOX.md`'s default read set for windows).
3. **The probes are currently POSIX shell.** Adding a `windows` list to
   `FleetStandardChecks` means either writing each `Probe` for `cmd.exe` — which
   keeps the one-line shape but is miserable — or changing the transport for
   windows to a single `powershell -NoProfile -NonInteractive -File` invocation
   that prints the whole `CHECK<TAB>name<TAB>value` block. The second is
   recommended: the wire format does not change, only who prints it, and the
   `CHECK` contract is what the Go side actually depends on.

---

## 10. The registry row

`internal/fleet/testdata/machines.tsv` is the shape; the fleet's own file is the
one that counts. Tab separated:

```
name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes
```

For this machine:

```
<name>	<name>	windows/x64	bench,runner	swarm-<name>	<cores>	allow-shared=<YYYY-MM-DD> Threadripper Pro, the fleet's only Windows bench; N CI runners beside the cards. MAC <aa:bb:cc:dd:ee:ff> for wake-on-LAN
```

- **`os/arch` is `windows/x64`.** `internal/fleet/registry.go` already documents
  `OS` as "linux, darwin, windows"; this is the first row to use the third.
- **`roles` is `bench,runner`, and that is the exception, not the shape.** THE
  LOCK (Glenn, 2026-09-18): runner hosts are CI-only, because a card and a CI
  shard on one host make the shard slow, the gate red and the queue stop. A
  machine that is both **must** say why in its notes with a dated
  `allow-shared=<YYYY-MM-DD>`, exactly as hulk and vision do. If this machine is
  to be a bench only at first, the row is `bench` and the runner role is added
  with its own dated note when the runners go on.
- **`seat`** is the `nova-secrets` seat, or `-` if it carries none. Secrets are
  never copied between machines (Glenn, 2026-09-16): the seat is sealed once in
  the store, and exactly one `*.key` lives on the machine.
- **`notes` is free text**, and the MAC belongs in it — section 6's magic packet
  is addressed to it, and looking it up needs the machine awake.

---

## The order to do it in

1. Windows Pro/Enterprise, virtualization on in firmware.
2. The local user `nova` (§1) — **not** an administrator.
3. OpenSSH Server, `sshd` Automatic, `DefaultShell` unset, `authorized_keys`
   with its ACL (§2). Prove `ssh nova@<bench> "echo ok"` from the coordination
   machine before anything else.
4. Go under `C:\sdk\go1.26.5`, on the **Machine** PATH (§3). Prove it with a
   non-interactive `ssh nova@<bench> "where go && go version"`.
5. git, with identity, `autocrlf false`, long paths (§4).
6. Windows features, then reboot (§7).
7. The runner(s) as services, labelled `windows,X64,<name>` (§5). Restart them
   after any PATH change.
8. Wake on magic packet, Fast Startup off (§6). Prove a wake from another
   machine before trusting the bench to suspend.
9. The registry row (§10).
10. Certification (§9) — by hand against this document until a `windows` list
    exists in `FleetStandardChecks`.
