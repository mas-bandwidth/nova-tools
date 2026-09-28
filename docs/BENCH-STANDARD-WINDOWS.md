# The Windows bench standard

A Windows machine joins the fleet as a **Linux bench under WSL2**: an Ubuntu
distro on the Windows box is the bench, with Linux CI runners labelled
`linux,X64,threadripper`, held to the Linux standard `tools/bench-standard.sh`
like every other Linux bench. The Windows side is only the host of that distro,
and its whole setup is one script, `tools/bench-wsl2.ps1`, run once.

**Scope.** This page is the standard for Windows hardware that does fleet work.
[BENCH-WINDOWS.md](BENCH-WINDOWS.md) is a different thing: the contract of a
**native** Windows machine as an install target of
`nova-update release adopt --platform windows-amd64` (its paths, its ssh shell,
its checks). No fleet bench runs native Windows.

## The host half: `tools/bench-wsl2.ps1`

Run it once from an **elevated** PowerShell in a checkout of this repository:

```
.\tools\bench-wsl2.ps1 -AuthKey <tailscale auth key>
```

The run is the one approval: a UAC prompt to open it, and a Tailscale auth key
minted once in the admin console. The key reaches no printed line. The script
refuses at exit 2, with one line naming what was wrong, when it is not elevated,
when the fleet's public keys (the `authorized_keys` file it reads from the checkout it
runs in, never writes and never generates) are missing, or when its Go version does not match `go.mod`'s `go` line.

On the host it:

- installs Tailscale with `winget` and brings it up with the auth key;
- sets the clock to `time.windows.com` (`w32tm`);
- turns hibernation off (`powercfg /h off`), so a shutdown is not a hybrid
  hibernate that drops the NIC's wake-armed state;
- sets the Hyper-V firewall in front of the mirrored WSL2 interface to allow
  inbound (`Set-NetFirewallHyperVVMSetting -DefaultInboundAction Allow`);
- writes `.wslconfig` with 75% of RAM as `memory=` and
  `networkingMode=mirrored`, so the tailnet address lands on an interface inside
  WSL2 and the distro runs no tailscaled of its own;
- registers a startup task, `nova-bench-sshd`, that starts the distro's sshd as
  SYSTEM, so the bench comes back after a reboot with nobody logged in.

In the distro (`Ubuntu-24.04` by default) it:

- creates the user `nova` with passwordless sudo, turns systemd on and makes
  `nova` the default user (`/etc/wsl.conf`), and puts `~/.local/bin` and
  `~/go/bin` on the system PATH (`/etc/environment`);
- installs `openssh-server`, `build-essential`, `sbcl`, `gh`, `redis-tools`;
- installs Go under `~/sdk/<go version>` and links it into `~/go/bin`;
- installs those public keys as `nova`'s `authorized_keys`, mode 0600;
- sets the git identity and enables sshd;
- makes the distro the default (`wsl --set-default`).

It prints one `CHECK<TAB>name<TAB>value` line per step and closes with
`BENCH-WSL2 OK distro=<d> user=<u> tailnet=<ip> labels=linux,X64,threadripper`
only once sshd answers on the tailnet address; anything short of that is a
refusal, never a green half-run.

## After the host half

From there the bench is a Linux bench, reached over ssh on the tailnet: its
machine row goes in with `nova-config machine add`, its tools arrive with
`nova-update release adopt --platform linux-amd64`, and `tools/bench-standard.sh`
is its standard.
