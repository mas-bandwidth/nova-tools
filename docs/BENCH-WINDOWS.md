# BENCH-WINDOWS — a native Windows machine as a release target

*What `nova-update release adopt --platform windows-amd64` assumes of the native Windows
machine it installs to: its account, its ssh shell, its paths and the checks that say it is
ready.*

**Scope.** This page is the contract of a **native** Windows install target. No fleet bench
runs native Windows: Windows hardware that does fleet work is a Linux bench under WSL2, and
its standard is [BENCH-STANDARD-WINDOWS.md](BENCH-STANDARD-WINDOWS.md). The release verbs
that read this page are specified in [SPEC-RELEASE.md](SPEC-RELEASE.md), section 11.

## 1. Principles

1. **Native Win32.** The tools run as native Win32/x64 processes; WSL is not this
   machine's environment.
2. **Dedicated service account.** Everything runs as the unprivileged user `nova`, never as
   a desktop interactive user or `SYSTEM`.
3. **A POSIX shell on the far side of ssh.** OpenSSH Server (`sshd`) is the one remote
   door, and its shell is Git Bash, so the command lines `adopt` composes parse the same way
   they do on every other platform.

## 2. Baseline

| Attribute | Standard value |
|---|---|
| Operating system | Windows 11 Pro / Enterprise (build 22631+), x64 |
| Service user | `nova` (member of `Users`; dedicated non-admin profile) |
| Shell for SSH | Git Bash (`C:\Program Files\Git\bin\bash.exe`), set as sshd's shell |
| Network | Tailscale interface + OpenSSH Server (`sshd`) on port 22 |

## 3. Toolchain and paths

- **Git:** native Git for Windows (`git version ...windows...`), at
  `C:\Program Files\Git\cmd\git.exe` and `C:\Program Files\Git\bin\git.exe`; not a WSL shim.
- **Go** (only where a release is built on this machine): the version `go.mod`'s `go` line
  names, at `C:\sdk\go\bin`, on the **system** PATH so a non-interactive OpenSSH session
  inherits it without a user shell profile.
- **Nova tools:** installed in `C:\Users\nova\.local\bin`, the directory `adopt --bin`
  names on this target; `adopt` takes it in the drive form and folds it to
  `C:/Users/nova/.local/bin` before any command is composed. The service account's PATH
  carries `C:\sdk\go\bin`, `C:\Program Files\Git\cmd` and `C:\Users\nova\.local\bin`.
- **Seat key:** exactly one key under `C:\Users\nova\.config\nova-secrets\<seat>.key`.

## 4. The checklist

The machine is ready when every row holds, checked over ssh:

| Check | Target / match | Requirement |
|---|---|---|
| `go` | `contains:` the `go.mod` version | `go version` reports the required toolchain (where a release is built here). |
| `git` | `contains:windows` | Native Git for Windows, not WSL. |
| `no-wsl` | `equals:ok` | The session is not inside WSL (`WSL_DISTRO_NAME` and `WSL_INTEROP` unset). |
| `nova-stamp` | `nonempty` | `nova-secrets version` answers cleanly. |
| `seat` | `equals:1` | Exactly one `*.key` present in `.config/nova-secrets/`. |

## 5. Provisioning (reference)

```powershell
# Run in an elevated PowerShell on the Windows machine
# 1. The nova account
net user nova /add /active:yes /passwordreq:yes

# 2. OpenSSH Server
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType 'Automatic'
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell -Value "C:\Program Files\Git\bin\bash.exe" -PropertyType String -Force

# 3. Go, where releases are built here (the zip for the version go.mod names)
New-Item -ItemType Directory -Force -Path "C:\sdk"
Expand-Archive -Path "go<version>.windows-amd64.zip" -DestinationPath "C:\sdk"
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";C:\sdk\go\bin", [EnvironmentVariableTarget]::Machine)
```
