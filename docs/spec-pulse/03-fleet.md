## Fleet

`tools/bench-standard.sh` is the one admin entry for a Linux bench: it checks
the bench against the standard and prints `STANDARD OK ...`, or one
`DRIFT <what>` line per finding followed by `STANDARD DRIFT (see lines
above)`. It checks that each `$HOME/runner-nova-tools-<i>/` holds exactly one
listener process descending from `nova-runner-<i>.service` (runner checks run
only when `uname` is Linux), that each unit file carries `Environment=PATH`
with `go/bin` and `.local/bin`, `KillMode=control-group` and
`TimeoutStopSec=30s`, that `go version` equals `$NOVA_GO` (default
`go1.26.5`) with `sbcl` on `PATH` and the harness at
`$HOME/nova-bench/harness-<ver>/opencode`, that the toolchain roots the
sandbox wall grants a card are all present (`$HOME/sdk` — one list, `internal/swarm/toolchain.go`, checked against
this script by a test so the standard and the wall cannot drift apart), that the 16 nova bins in
`$HOME/.local/bin` each report `$NOVA_WANT`, that exactly one `*.key` sits
under `$HOME/.config/nova-secrets` with `nova-secrets check` passing for it,
and that no plaintext key file (`$HOME/.local/share/opencode/auth.json`,
`$HOME/.config/deepseek/env`) and no literal `apiKey": "sk-` in
`$HOME/.config/opencode/*.json` survives; `--apply` kills stray runner
listeners not under their unit, nothing else destructive. The network probe
runs inside the real sandbox, never from the host, so what it reports is what
a card would see.

The same probe is the gate that admits a bench to the loop at all, and it is
the CI job that runs it: `fleet-probe` in `ci.yml`, dispatched with `bench` and
`slots`, one job per requested runner slot on that bench's Linux runners. It
builds `nova-sandbox` from the checkout and runs the network fetch inside it,
failing unless the fetch answers 200, so a bench enters the loop only after the
SANDBOXED probe is green. A host probe is never the evidence: #893 is the night
one passed while every sandboxed card died.

### The runners are Linux runners, everywhere, including on the Windows box

Glenn, 2026-09-18: **"drop the native windows CI runners. WSL only from now
on."** Every self-hosted runner in this fleet is a Linux or a macOS runner, and
`ci.yml` runs no `windows-latest` leg at all — its one Windows guard is the
`lint` job's `make vet-windows` (`GOOS=windows go vet ./...`), which
cross-compiles and type-checks the whole tree, tests included, on a Linux runner
in seconds.

The Threadripper Pro is the fleet's Windows box and it joins as a **Linux**
machine. WSL2 is the operating system its cards and its CI runners see, so it
takes the Linux half of `tools/bench-standard.sh` above, the Linux `fleet
standard` list, and the same runner labels every other Linux bench carries with
its own name added:

| bench | runner labels | registry line |
| --- | --- | --- |
| hulk | `linux,X64,hulk` | `hulk … linux/x64 bench,runner` |
| vision | `linux,X64,vision` | `vision … linux/x64 bench,runner` |
| the Threadripper | `linux,X64,threadripper` | `threadripper-wsl … linux/x64 bench,runner` |

That is enforced in the registry rather than remembered:
`internal/fleet/testdata/machines.tsv` carries `threadripper-wsl` as a
`linux/x64` line, and `internal/fleet`'s example test refuses any machine whose
`os` is `windows`. A machine that is both runner and bench still needs its dated
`allow-shared=` note, and this one has it for the same reason hulk and vision
do. The native Windows half of that box is parked: see
`docs/BENCH-STANDARD-WINDOWS.md`.

### The four bench scripts, retired

`fleet standard`, `fleet mirror`, `fleet join` and `fleet sleep` are the last
four hand-run bench scripts as verbs (#1142, "everything sketched becomes a
tool"). Each keeps the fleet rule: one bench per `--bench`, every path from a
flag with no default, ssh from `--ssh`, `studio` and an unknown name refused by
name before any ssh, exit 0/2/3, and one remedy line per refusal.

`nova-pulse fleet standard --benches <file> --bench <name> [--want <stamp>]
[--go <ver>] [--os linux|darwin] [--min-free <gb>] [--ssh <path>]
[--timeout <s>] [--max <n>]` holds one bench against the provisioning standard.
It prints one `STANDARD <bench> <check> OK got=<v>` or
`STANDARD <bench> <check> DRIFT want=<match>:<want> got=<v>` line per check and
then the verdict `FLEET <bench> STANDARD OK checks=<n>` or
`FLEET <bench> STANDARD DRIFT drift=<k>/<n>`. The checks are DATA, one table per
operating system, so the standard is read rather than traced through a shell
script: the Linux list is the Go toolchain at `--go` (default `go1.26.5`),
`sbcl`, the `safe-rm` helper, the nova stamp at `--want`, one seat key that
opens, and the free-space floor `--min-free` (default 25 GB); the darwin list is
the Mac bench standard — the Go SDK and `sbcl` under `~/sdk`, real git ahead of
the Xcode shim (`/usr/bin/git` is the shim, and the sandbox cannot read
`/var/db/xcode_select_link`), and every runner's `.path` carrying the real git
first — with the stamp, seat and space checks shared. `--os` names the list;
left out, the bench is asked with `uname -s`. The remote side prints
`CHECK<TAB>name<TAB>value` and nothing else: the verdict is decided in Go. This
retires `bench-standard.sh`, which refused to run anywhere but ON a Linux bench
and could say nothing at all about a Mac one.

`nova-pulse fleet mirror --benches <file> --bench <name> --repo <url>
--path <remote path> [--ssh <path>] [--timeout <s>]` creates the bare mirror a
card clones from (`git clone --reference`) or fetches the one already there, and
prints `FLEET <bench> MIRROR <path> created|refreshed head=<sha> size=<n>K`. It
deletes nothing. `--repo` must be an https remote and `--path` an absolute clean
path, both free of shell metacharacters, or the verb refuses before any ssh:
both are pasted into a remote command line. This retires `bench-mirror.sh`,
whose mirror root and repository list were hard-coded.

`nova-pulse fleet join --benches <file> --bench <name> --tailscale <path>
--authkey-env <NAME> [--ssh <path>] [--timeout <s>]` joins one bench to the
tailnet and prints `FLEET <bench> JOINED ip=<addr>`. The auth key is never a
flag value: it reaches the process ONLY through the environment variable
`--authkey-env` names, which `nova-secrets exec` fills for the length of the
call, and it reaches the bench on the remote shell's stdin, piped into
`tailscale up --auth-key=file:/dev/stdin --hostname=<bench>`, so it is in no
argv on either machine, and anything the bench says back is scrubbed of it
before a line is printed. An empty variable, a `--tailscale` that is not
absolute, and a bench the file does not carry are refusals before any ssh. This
retires `ts-join-one.sh`.

`nova-pulse fleet sleep --benches <file> --bench <name> [--ssh <path>]
[--if-idle] [--force] [--timeout <s>] [--max <n>]` puts ONE bench to sleep. It
is `fleet suspend` over one name, not a second implementation: one place decides
busy, so a lease or a job directory with a live pid under either swarm root, or
a `Runner.Worker`, is `FLEET <bench> BUSY <what>` and is never suspended. It
retires the Linux half of `fleet-sleep.sh` (the Mac half — `pmset` idle sleep —
is `nova-pulse sleep`, under "Mac bench power"): that script asked GitHub whether
the bench's RUNNERS were busy and knew nothing about the cards in flight on it,
so a bench working through nova-swarm looked idle and slept under its own work.

Red tests, one per verb: `fleet-standard-lists-the-checks` (the two lists as
data, and what both benches must carry in both); `fleet-standard-names-the-check-that-drifted`;
`fleet-mirror-creates-then-refreshes` (clone once, fetch after);
`fleet-join-keeps-the-auth-key-out-of-every-argv` (the key is on the remote
stdin and in no argv log, on neither stream); `fleet-sleep-refuses-a-bench-with-a-live-job`
(and the idle one is suspended through systemctl). Every one of them drives a
fake ssh on the test's own PATH that runs the remote script through `bash -s`
with fake sudo, git, tailscale and systemctl beside it, so no test reaches a
machine or opens a socket.
