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
`$HOME/nova-bench/harness-<ver>/opencode`, that the 16 nova bins in
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

### Certification: what a machine can DO

`fleet survey` asks a machine what it HAS. **Certification makes it DO the work its roles
imply, under the same containment a card gets, and writes down that it did.**

The hurt, 2026-09-18: the first real Go card of the day was dealt to hulk and died inside
the swarm wall. The wall's readable roots did not carry `$HOME/sdk/go1.26.5`, so the only Go
the card could reach was `/usr/bin/go` 1.22, which go.mod refuses by name. hulk met the
provisioning standard and had passed every check ever run on it — because every check ever
run on it ran OUTSIDE the wall, over a plain ssh, as the person who owns the machine. The
hand pass that followed found the same shape everywhere: `ssh <bench> nova-merge version`
answered `command not found` on all four Linux machines (Ubuntu's `~/.bashrc` returns before
any PATH line for a non-interactive shell), eighteen stale `go install` binaries per machine
shadowed the release, the global git identity was empty on all four, sixteen of space's
runner `.path` files carried no Go at all, and space was serving sixteen merge-group runners
while the registry called it `bench,services`.

```
nova-pulse fleet certify --machines <file> (--machine <name> | --all | --status) --certs <file>
  [--workloads <dir>] [--standard <file>] [--build <version>] [--bin <dir>] [--repo <owner/name>]
  [--ssh <path>] [--if-stale] [--max-age <d>] [--log <file>] [--timeout <d>] [--dry-run]
```

**A workload is a file.** `<class>.card` under `--workloads`, or the set embedded in the
tool: front matter of `key: value` lines, one blank line, then the body the machine runs.
The class is the FILE'S NAME and never a key, so two cards cannot claim one class. The keys:

- `roles:` — which of `bench`, `runner`, `services`, `coordination` this applies to. A
  workload that applies to nothing is never run, so an empty list is a refusal.
- `expect:` — a regexp, matched line by line against what the machine said. **The verdict
  comes from what the machine SAID**, never from an exit code alone: the hulk card exited
  non-zero for a reason no exit code could name. The evidence kept is the whole line that
  matched.
- `wall: yes` — run the body inside `nova-sandbox`, with a job directory of its own as the
  only `--write`, a `HOME` inside it, a `--cwd` inside it, and each `reads:` root as a
  `--read`. That is the shape that failed on hulk, and it is why `wall: yes` without
  `reads:` is a refusal.
- `reads:` — the wall's readable roots; `$HOME` is the machine's own.
- `forge: runners|registry` — a question for the forge, not for a machine.
- `report: yes` — a failure is a `WARN`, never a `FAIL`: measured, written, printed, and it
  gates nothing. Only `diag-size` carries it.

**The shipped classes.** bench: `go-test`, `c-build`, `cpp-build`, `sbcl`, `git-push` (a bare
repository made on the machine for the run and deleted after it, never a real remote),
`wall-toolchain`, `go-on-path`, `path-resolves`, `git-identity`, `services-reach`,
`registry-truth`. runner: `runner-online`, `runner-path` (both systemd scopes, both unit
namings, and the listener count held against the unit count, so a double registration is a
FAIL naming it), `path-resolves`, `registry-truth`, `diag-size` (MB, the age of the oldest
log and the rate; report-only). `services-reach` tells `refused` from `denied (protected
mode)` from `PONG`, and only `PONG` is OK: the three have three remedies, and one word for
all of them sends a person to the wrong machine. A name must RESOLVE, by whatever the
machine has -- no tailnet address is required, since the Studio has none. services: `loki-ready`, `redis-ping`, `postgres-ready`. coordination:
`bus-push`, `release-path`. `path-resolves` and `registry-truth` apply to every role.

**One certificate row per machine per class**, appended to `--certs`, seven tab-separated
fields, every field written every time:

```
machine<TAB>build<TAB>standard-hash<TAB>class<TAB>verdict<TAB>evidence<TAB>at
```

`build` is the machine's own `nova-merge version` line, read over ssh unless `--build` names
one. `standard-hash` is the sha256 over the provisioning standard file AND every workload's
bytes, so either half moving expires every certificate written under the old pair. Evidence
goes through `internal/oneline`, so a tab or a newline in what a machine said cannot become
a column or a row.

**The output.** One `CERTIFY <machine> <class> OK|FAIL|WARN evidence="..."` line per
workload, `CERTIFY <machine> CURRENT classes=<n> build=<v>` for a machine `--if-stale`
skipped, and `CERTIFY OK|FAIL machines=<n> ok=<n> fail=<n> warn=<n> skipped=<n>` at the end.
Exit 1 on any FAIL, 2 on a refusal. A forge question with no forge wired is SKIPPED with
`CERTIFY NOTE machine=<m> class=<c> skipped=no-forge` and writes no row — "this tool could
not ask" is not "this machine is wrong".

**`internal/fleet.Certified(certs, machine, class, build, hash)`** is the currency rule, and
`nova-pulse fill` asks it before every card. A card's workload class is its own
`workload: <class>` line, or the class its `LANG:`/`LEG:` line implies, or `go-test`. A card
whose class has no current certificate on the bench it was dealt is refused and **stays
ready**:

```
FILL REFUSED bench=hulk reason=uncertified workload=go-test remedy="nova-pulse fleet certify --machine hulk"
```

One line per bench and class, never one per card. The gate is off when the fill names no
`--certs`, no hash and no build reader, which is the documented narrowing for a loop that has
not adopted certification — and exactly the state hulk was in.

**Mechanized, not remembered** (Glenn, 2026-09-18: *"we want this certification to be
mechanized"*). Three triggers:

1. `nova-update release adopt` **certifies by default**. An adopt changes the build on every
   machine it touches and so invalidates every certificate those machines held. `--certify
   <machines registry> --certs <file> --standard <file>` renews them under the version just
   installed, through the same ssh seam; `--no-certify` waives it and the verdict line says
   `certified=waived`. An adopt that does neither is refused with both roads named — "on by
   default" cannot mean a guessed path (SPEC-UPDATE rule 1).
2. `--if-stale` skips a machine whose every class is current. A certificate is stale when the
   build or the standard hash differ, when the verdict was FAIL, or when it is older than
   `--max-age` (default 24 h) — a machine drifts by the hand of whoever last logged into it,
   and the whole fleet was found drifted with nothing in the tools having changed.
3. `fleet/launchd/com.rowan.fleet-certify.plist` runs `--all --if-stale` every six hours. A
   fleet with nothing to do costs one file read and one `nova-merge version` per machine.

`--status` reads the record and reaches no machine: one line per machine and class, and
exit 1 when any is stale, failed or missing. `--log <file>` writes one structured event per
certificate through `internal/log` — the same Emitter `nova-pulse launch` uses — with
`verb=certify`, `event=certify`, `bench=<machine>`, and the verdict in the level.

Red tests, fake-driven, no network and no wall-clock bound:
`the-standard-workloads-are-the-shipped-classes`,
`go-test-runs-inside-the-wall-with-the-toolchain-as-a-read-root`,
`certified-is-true-only-for-the-current-build-and-hash`,
`fill-refuses-a-card-whose-workload-is-uncertified-on-that-bench`,
`if-stale-skips-a-machine-whose-every-class-is-current`,
`adopt-with-certify-runs-the-workloads-on-each-adopted-machine-under-the-version-just-installed`,
`the-launchd-agent-runs-the-verb-the-loop-needs`.
