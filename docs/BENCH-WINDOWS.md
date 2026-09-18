# BENCH-WINDOWS — Provisioning and Standard for Windows Fleet Benches

*The operational contract, provisioning checklist, and verification standard for Windows bench machines (e.g. AMD Ryzen Threadripper workstations) in the Nova fleet.*

## 1. Role and Principles

Windows bench machines run native Windows workloads, cross-platform verification, and swarm worker execution under hardware-accelerated virtualization.

1. **Native Win32, Never WSL for the Bench**:
   The bench environment is strictly native Windows. WSL (`Microsoft-Windows-Subsystem-Linux`) is neither the bench environment nor the build toolchain. A tool running on a Windows bench executes as a native Win32/x64 process.
2. **Containment Below the Model**:
   Pull workers execute cards inside containerized or sandbox boundaries (Hyper-V / Windows Containers / Windows Sandbox), preventing unsandboxed host mutation.
3. **Dedicated Service Account**:
   Work and runner daemons run under the unprivileged service user `nova`, never under a desktop interactive user or `SYSTEM`.
4. **Autonomous Power & Remote Management**:
   OpenSSH Server (`sshd`) provides headless remote orchestration. Wake-on-LAN (Magic Packet) allows power-saving sleep and instant wake by `nova-pulse fleet wake`.

---

## 2. Hardware and OS Baseline

| Attribute | Standard Value |
|---|---|
| Workstation | AMD Ryzen Threadripper (x64 / AMD64) |
| Operating System | Windows 11 Pro / Enterprise (Build 22631+) |
| Service User | `nova` (Member of `Users`; dedicated non-admin profile) |
| Shell for SSH | Git Bash (`C:\Program Files\Git\bin\bash.exe`) or native Windows OpenSSH with Bash in `sshd_config` |
| Virtualization Flags | Hyper-V, Containers, Windows Sandbox enabled |
| Network / Management | Tailscale interface + OpenSSH Server (`sshd`) on port 22 |
| Power Management | Wake-on-LAN (Magic Packet) enabled on primary NIC; S3/Modern Standby sleep supported |

---

## 3. Toolchain & Environment

### Go SDK
- Version matches repository `go.mod` (default `go1.26.5`).
- Installed at `C:\sdk\go\bin` (or `C:\Program Files\Go\bin`).
- Placed on the System `PATH` so non-interactive OpenSSH sessions inherit it automatically without invoking a user shell profile.

### Git
- Native Windows Git (Git for Windows, `git version ...windows...`).
- Installed at `C:\Program Files\Git\cmd\git.exe` and `C:\Program Files\Git\bin\git.exe`.
- Not a WSL shim or MSYS standalone package.

### GitHub Actions Runner
- Installed as a Windows Service under the `nova` service account.
- Service Name pattern: `actions.runner.*`.
- Startup Type: `Automatic`.
- Runner directory: `C:\Users\nova\runner-nova-tools-1\` (and subsequent instances per core tier).
- Environment file (`.path`) carries `C:\sdk\go\bin`, `C:\Program Files\Git\cmd`, and `C:\Users\nova\.local\bin`.

### Nova Tools & Seat Secrets
- Bounded binaries (`nova-swarm`, `nova-pulse`, `nova-secrets`, etc.) installed in `C:\Users\nova\.local\bin` or `C:\nova\bin`.
- Bench Seat Key: Exactly one secret key under `C:\Users\nova\.config\nova-secrets\<seat>.key`.
- Disk Floor: Free space under `C:\Users\nova` must stay strictly above 25 GB (`launch` and `fleet standard` refuse below this floor).

---

## 4. The Checklist: `nova-pulse fleet standard --platform windows`

When `nova-pulse fleet standard --bench <name> --platform windows` probes the bench over SSH, it verifies the following checklist:

| Check Name | Target / Match | Requirement |
|---|---|---|
| `go` | `contains:go1.26.5` | `go version` reports the required toolchain; executable in `C:\sdk\go\bin` or system PATH. |
| `git` | `contains:windows` | Native Git for Windows (`git version ...windows...`), not WSL. |
| `no-wsl` | `equals:ok` | Session is not inside WSL (`WSL_DISTRO_NAME` and `WSL_INTEROP` unset). |
| `features` | `contains:Containers` | Hyper-V and Containers virtualization features enabled for sandboxing. |
| `runner-service`| `contains:Running` | GitHub Actions runner Windows Service is active and running under `nova`. |
| `wol` | `equals:enabled` | Network adapter has Wake-on-Magic-Packet enabled in driver properties. |
| `nova-stamp` | `nonempty` | `nova-swarm version` answers cleanly. |
| `seat` | `equals:1` | Exactly one `*.key` present in `.config/nova-secrets/`. |
| `disk-free` | `at-least:25` | At least 25 GB free disk space on the primary volume. |

---

## 5. Provisioning Script (Reference)

```powershell
# Run in elevated PowerShell on fresh Windows Threadripper bench
# 1. Enable Virtualization Features
Enable-WindowsOptionalFeature -Online -FeatureName Microsoft-Hyper-V-All, Containers -NoRestart
Enable-WindowsOptionalFeature -Online -FeatureName "Containers-DisposableClientVM" -NoRestart

# 2. Create nova service account
net user nova /add /active:yes /passwordreq:yes
net localgroup "Remote Desktop Users" nova /add

# 3. Setup Go SDK
New-Item -ItemType Directory -Force -Path "C:\sdk"
Expand-Archive -Path "go1.26.5.windows-amd64.zip" -DestinationPath "C:\sdk"
[Environment]::SetEnvironmentVariable("PATH", $env:PATH + ";C:\sdk\go\bin", [EnvironmentVariableTarget]::Machine)

# 4. Enable OpenSSH Server
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType 'Automatic'

# 5. Configure Wake-on-LAN
Get-NetAdapter | Enable-NetAdapterPowerManagement -WakeOnMagicPacket
```
