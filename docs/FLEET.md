# Fleet Runner Standard & Ansible Provisioning

This document describes the fleet runner architecture, machine roles, and automated
provisioning for benches in the fleet, specifically focusing on Windows WSL2 bench
runners ([#1458](https://github.com/mas-bandwidth/nova-tools/issues/1458), PR #4601).

---

## 1. Fleet Machine Architecture

The fleet is comprised of dedicated runner benches managed through the tailnet:

1. **macOS Benches (Darwin ARM64 / Apple Silicon):**
   Run native Darwin runners (e.g. Darwin coordinator and runner instances).
2. **Linux Benches (Linux x64 / ARM64):**
   Run native Linux bench environments (`tools/bench-standard.sh`).
3. **Windows WSL2 Benches (Linux x64 under WSL2):**
   Windows machines (such as the Threadripper Pro) join the fleet as **Linux benches under WSL2**.
   Native Windows CI runners were retired on 2026-09-18; all Windows hardware runs Ubuntu distros
   in WSL2 with mirrored networking and systemd enabled.

---

## 2. Windows WSL2 Provisioning Flow

Provisioning a Windows WSL2 bench runner proceeds in two phases:

### Phase 1: Host Bootstrap (`tools/bench-wsl2.ps1`)
Executed once on the Windows host from an elevated PowerShell prompt:
```powershell
.\tools\bench-wsl2.ps1 -AuthKey <tailscale auth key> -GoVersion go1.27.1
```
This script:
- Configures Tailscale on the Windows host and joins the tailnet.
- Disables Windows hibernation (`powercfg /h off`).
- Sets Hyper-V firewall rules for mirrored WSL2 interfaces.
- Writes `.wslconfig` (`networkingMode=mirrored`, `memory=75%`).
- Registers Windows startup task `nova-bench-sshd` to launch WSL2 sshd on boot.
- Installs the Ubuntu WSL2 distro, creates the runner user `nova` with passwordless sudo,
  and enables `systemd=true` in `/etc/wsl.conf`.

### Phase 2: Ansible Convergence (`roles/wsl2_bench_runner`)
Once the host bootstrap completes and sshd is reachable over the tailnet, the bench is
configured, hardened, and maintained via Ansible:
```bash
# Check syntax
ansible-playbook --syntax-check playbooks/wsl2_bench_runner.yml

# Dry-run check mode with diff
ansible-playbook -i inventory.py playbooks/wsl2_bench_runner.yml --check --diff

# Converge configuration
ansible-playbook -i inventory.py playbooks/wsl2_bench_runner.yml
```

---

## 3. Role Specification: `roles/wsl2_bench_runner`

The Ansible role `roles/wsl2_bench_runner` (also available via `fleet/roles/bench-wsl2`
and `fleet/bench-wsl2.yml`) enforces the bench contract:

### Order of Operations
1. **`tasks/preflight.yml` (Preflight & Platform Validation):**
   - Asserts host OS is Linux.
   - Detects WSL2 kernel environment.
   - Validates Windows host filesystem mount (`/mnt/c` or `bench_wsl2_windows_mount`).
   - Validates Windows System32 integration and WSL distro registration via `wsl.exe -l -v`.
   - Validates `/etc/wsl.conf`.

2. **`tasks/runner.yml` (Runner User & Environment):**
   - Creates runner group and user with dynamic home path (`{{ bench_wsl2_user_home }}`).
   - Grants passwordless sudo in `/etc/sudoers.d/{{ bench_wsl2_user }}`.
   - Writes `/etc/wsl.conf` with `[boot] systemd=true`, default user, and interop settings.
   - Configures system PATH in `/etc/environment`.
   - Creates standard bench directories (`~/sdk`, `~/go/bin`, `~/.local/bin`, `~/.cargo/bin`, `~/nova-bench/slots`, `~/nova-bench/run`, `~/nova-bench/cache`).
   - Sets git identity in `~/.gitconfig`.

3. **`tasks/packages.yml` (System Packages):**
   - Installs build tools: `build-essential`, `openssh-server`, `ca-certificates`, `curl`, `git`, `sbcl`, `gh`, `redis-tools`, `jq`.
   - Ensures `ssh` service is enabled and running under systemd.

4. **`tasks/toolchains.yml` (Toolchains without Hardcoded Homes):**
   - **Go SDK:** Installed under `{{ bench_wsl2_go_sdk_root }}/{{ bench_wsl2_go_version }}` and linked to `~/go/bin/go` and `/usr/local/bin/go`.
   - **Rust:** `rustc` and `cargo` installed with user directories `~/.cargo` and `~/.rustup`, linked into `~/.local/bin`.
   - **Nova Tools:** Links installed binaries (`nova-bus`, `nova-check`, `nova-update`, `nova-sandbox`, `nova-swarm`, `nova-tokens`, `nova-config`) into `~/.local/bin`.
   - **.NET SDK:** Installed via apt or official `dotnet-install.sh` into `~/.dotnet`, linked to `/usr/local/bin/dotnet` and `~/.local/bin/dotnet`.

5. **`tasks/redis.yml` (Redis Server):**
   - Configures `/etc/redis/redis.conf` (`bind`, `port`, `protected-mode`).
   - Manages `redis-server` under systemd with restart handler.

6. **`tasks/tailnet.yml` (Machine Identity):**
   - Probes tailnet IPv4 (via Linux `tailscale` or Windows `tailscale.exe`).
   - Probes machine hostname via `tailscale status --self --json`.
   - Writes swarm pool identity file: `{{ bench_wsl2_user_home }}/nova-bench/identity.tsv`.
   - Writes environment file: `/etc/nova-bench/identity.env`.

7. **`tasks/check_mode.yml` (Receipt & Verification):**
   - Probes Go, Rust, .NET, Redis, and Nova tools versions.
   - In check mode (`--check`), prints receipt:
     `BENCH-WSL2 CHECK host=<host> user=<user> tailnet=<name> ip=<ip> ... mode=dry-run`
   - In live execution, prints receipt:
     `BENCH-WSL2 OK host=<host> user=<user> tailnet=<name> ip=<ip> ... mode=converged`

---

## 4. Check Mode and Idempotency Guarantees

- **Check Mode (`--check --diff`):**
  All tasks are check-mode aware. Probing tasks declare `check_mode: false` and `changed_when: false`.
  Mutation tasks use declarative modules or guard execution with `when: not ansible_check_mode`.
- **Idempotency (`changed=0`):**
  Running the playbook twice against a converged machine results in 0 changed tasks.
  Downloads and extractions are protected by file presence checks (`stat` and `creates:`).
  Configuration updates use idempotent block and line managers (`blockinfile`, `lineinfile`).

---

## 5. Automated Verification

Run the verification test suite locally:
```bash
./scripts/verify-wsl2-bench-role.sh
```
Or run the Go integration tests:
```bash
go test -v -run TestWSL2 ./internal/docs/...
```
