# The Windows bench standard

A Windows machine joins the fleet as a **Linux bench under WSL2**: an Ubuntu
distro on the Windows box is the bench, with Linux CI runners labelled
`linux,X64,threadripper`, held to the Linux standard, witnessed by `tools/benchstandard`,
like every other Linux bench. The Windows side is only the host of that distro,
and its whole setup is one script, `tools/bench-wsl2.ps1`, run once.

**Scope.** This page is the standard for Windows hardware that does fleet work.
[BENCH-WINDOWS.md](BENCH-WINDOWS.md) is a different thing: the contract of a
**native** Windows machine as an install target of
`nova-update release adopt --platform windows-amd64` (its paths, its ssh shell,
its checks). No fleet bench runs native Windows.

## The host half: `tools/bench-wsl2.ps1`

**First, the fleet's public keys.** An ordinary checkout carries no fleet public-key file,
so the script refuses until the operator obtains the approved fleet public keys and places
them, as one `authorized_keys`-format file, at the path the script's `$KeysFile` assignment
names (near the top of `tools/bench-wsl2.ps1`, relative to the checkout root). Only public
halves go in that file: no private key is ever copied to the box, by the operator or by the
script, and the script only reads the file.

Then run it once from an **elevated** PowerShell in that checkout:

```
.\tools\bench-wsl2.ps1 -AuthKey <tailscale auth key> -GoVersion go1.26.6
```

`-GoVersion` names the toolchain the distro gets. Choose one that meets `go.mod`'s `go`
line (`go 1.26.6` in this tree): the script's default, `go1.26.5`, is below it.

The run is the one approval: a UAC prompt to open it, and a Tailscale auth key
minted once in the admin console. The key reaches no printed line. The script
refuses at exit 2, with one line naming what was wrong, when it is not elevated,
when that public-key file is missing (it reads it, never writes it and never generates a
key), or when `-GoVersion` is not of `go.mod`'s major.minor. That check is exactly what the
script's regex reads: it takes only the major.minor of `go.mod`'s `go` line (`1.26`) and
requires `-GoVersion` to match `go1.26.*`, so it does not compare the patch, and a version
below `go.mod`'s patch (the default `go1.26.5`) passes it.

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
`nova-update release adopt --platform linux-amd64`, and `tools/benchstandard`
is its witness: run it as a static binary built elsewhere
(`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/benchstandard ./tools/benchstandard`),
or with `go run ./tools/benchstandard` where the distro's Go is good.
