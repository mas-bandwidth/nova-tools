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
- `where: machine|coordinator` — where the workload RUNS. The default is `machine`; a forge
  question is always `coordinator`, and saying otherwise is a refusal. `gh` lives where the
  coordinator is: the first real run of this verb went looking for it on a bench that has
  never had it.
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

**Neither a transport failure nor a timeout is a verdict.** `UNREACHABLE` is its own token
and its own count, and so is `TIMEOUT` -- work the run never let finish (`exec sleep 5` under
a one-millisecond `--timeout`) is not a judgement on a machine. Both write no row, are never
repaired, and exit 3:
`CERTIFY <machine> <class> UNREACHABLE reason="Host key verification failed."`, **no
certificate row**, no repair, and exit 3 — what every other fleet verb answers for a machine
it could not reach. The first real run of this verb, by somebody who did not write it, wrote
`CERTIFY hulk go-test FAIL evidence="Host key verification failed."` and a row whose build
column held that sentence; a tool that cannot reach a machine knows nothing about it. The
build column holds a parsed version or `-`, never a line. The first transport failure of a
machine ends that machine's ssh work: its classes carry that reason, and its `coordinator`
classes are still answered.

**One row per (machine, class) per run.** The repair round certifies a class a second time,
and appending both left the record holding two verdicts for one pass -- the first of them a
failure that was no longer true when the run ended. A machine's rows are held and written
once its pass is over, carrying the verdict the run ENDED on, and a class that ends
UNREACHABLE or TIMEOUT writes nothing at all.

**A `where: coordinator` workload never opens an ssh**, whether it asks the forge or runs a
body: its body runs HERE. The branch is taken before the run, not after it.

**A workload body is portable, because the bench is not chosen when it is written.** The
registry has carried a darwin bench since 2026-09-18 — `air  glenn@100.117.59.68
darwin/arm64  bench,runner` — and the first run on it found three faults of which two were
*silent passes*: `find -printf` is GNU (so `diag-size` read no oldest log and reported the
size as the rate), there is no `getent` on darwin (so `services-reach` read the empty output
of a missing program as "the name does not resolve", with the address in `/etc/hosts`), and a
**loaded** launchd job and one that **survives a reboot** are two different facts (so
`runner-path` never read `~/Library/LaunchAgents/actions.runner.*.plist`). Every shipped body
is held to #1415's template class by `internal/fleet/portable.go`: a spelling one OS lacks is
refused unless the same line names its portable other half, and a one-OS tool is refused
unless the body guards it with `command -v`. The allowlist is shrink-only, matched by class
and spelling and never by line, and is empty. A run that reads no workload is red.

**The darwin toolchain roots are #1419's list.** `/opt/homebrew/bin/go` is a symlink into
`/opt/homebrew/Cellar/go/<ver>/libexec`, each toolchain resolves its runtime from the
directory of the launcher that ran it, and the grant is checked against the resolved target —
so `go-test`, `wall-toolchain` and `sbcl` name the Cellar **trees** as `reads:`, never
`/opt/homebrew/bin`, and each body looks in the tree before whatever `PATH` resolves. A root
absent from the machine is skipped, so one card is one card on both operating systems.

**A line in the class's own token is the machine ANSWERING.** Each `expect:` names the word
its class speaks in; a line beginning with it came from the body, and the transport question
is never asked of it. The Air's `SERVICES FAIL redis ... Connection refused` — redis-cli's
words, through an ssh that worked — was read as ssh failing, and a real fault of the fleet
disappeared into a count of machines nobody could reach.

**A forge nobody could ask is `UNREACHABLE`, not `FAIL`**, for the same reason a broken ssh
is: `gh` is not authenticated everywhere the verb runs, and a judgement about a machine made
from a question nobody asked is the `build=Host\x20key\x20verification\x20failed.` row
again. No row, counted apart, never repaired.

**A machine certifies ITSELF without ssh.** When the machine named is the machine running the
verb — by registry name, ssh target, short host name, or one of this machine's own
addresses — the workload runs here through
`bash -s`, and `CERTIFY NOTE machine=<m> transport=local reason=this-is-the-machine` says so
once. hulk certifying hulk went through `ssh hulk` and died on its own host key.

**The output.** One `CERTIFY <machine> <class> OK|FAIL|WARN evidence="..."` line per
workload, `CERTIFY <machine> CURRENT classes=<n> build=<v>` for a machine `--if-stale`
skipped, and `CERTIFY OK|FAIL|UNREACHABLE|TIMEOUT machines=<n> ok=<n> fail=<n> warn=<n> skipped=<n>
unreachable=<n> timeout=<n> fixed=<n>` at the end. A dry run ends `CERTIFY DRY-RUN machines=<n>
would=<n>` and **never** says OK: a run that reached nothing has no passes to report.
Exit 1 on any FAIL, 3 when a machine was only unreachable or out of time, 2 on a refusal. A forge question with no forge wired is SKIPPED with
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

`--status` reads the record and reaches no machine, so it needs **only `--certs`**: with a
registry it reports every machine and class the fleet is meant to hold (a class nobody has
certified is `NONE`), without one it reports what the record carries. One line per machine
and class, and exit 1 when any is stale, failed or missing. `--log <file>` writes one structured event per
certificate AND per escalation through `internal/log` — the same Emitter `nova-pulse launch`
uses — with `verb=certify`, `event=certify`, `bench=<machine>`, and the verdict in the level;
an escalation is ERROR and carries the classes and the remedy.

### Fix, then prove, then escalate

A verdict is not the end of the work. On 2026-09-18 the same four faults were found on four
Linux machines, repaired BY HAND four times, and nothing in the tools remembered how by the
evening. So the repairs are the provisioning standard itself, as remedies:

```
nova-pulse fleet standard --apply --machines <file> --machine <name> [--items <a,b>]
  [--home <dir>] [--git-name <name>] [--git-email <addr>] [--ssh <path>] [--timeout <s>] [--dry-run]
```

`--apply` is the ONE mutating fleet verb. It names its machine through the machines REGISTRY
— the file that decides where cards go — never through the benches file, never `--all`, and
prints one line per item:

```
STANDARD APPLY <machine> <item> changed|unchanged|would|failed remedy=<-|adopt> detail="..."
STANDARD APPLY OK machine=<m> items=<n> changed=<n> failed=<n>
```

The items, each an idempotent remedy for one check of the standard: `path-noninteractive`
(one marker block at the TOP of `~/.bashrc`, above the interactive guard, because that guard
is where a non-interactive shell returns), `gobin-shadow` (the `nova-*` binaries in
`~/go/bin` MOVED to `~/nova-bench/stale-gobin-<date>/` and **never deleted**), `git-identity`
(set when either half is empty, never overwritten), `runner-path-go` (the wanted Go on the
first line of each runner's `.path`, both namings — asking for any `go` is how
`/usr/bin/go` 1.22 passed for a toolchain go.mod refuses by name), and `nova-stamp`, **the
one apply never runs**: a stale build is `nova-update release adopt`, which stops cards,
swaps binaries and re-certifies, so apply says `remedy=adopt` and stops.

Certification runs that apply itself. `--fix` is ON by default and `--no-fix` waives it out
loud:

1. A FAIL whose class maps to a standard item — the mapping is a table in code
   (`internal/fleet.ItemsForClass`): `path-resolves` → `gobin-shadow`,
   `path-noninteractive`; `go-on-path` → `path-noninteractive`; `release-path` →
   `nova-stamp`, `path-noninteractive`; `git-identity` → `git-identity`; `runner-path` →
   `runner-path-go` — is applied, on one `CERTIFY FIX machine=<m> round=<n> classes=<list>
   items=<list> by-hand=<list>` line. **Only a class that FAILED with evidence gathered from
   the machine.** An `UNREACHABLE` class is never repaired: applying a remedy to a machine
   nobody reached is a second ssh to the same closed door.
2. The repaired classes are **certified again**, by the same workloads through the same wall
   (`--max-fix-rounds`, default 1). A repair is never credit: what the class holds after a
   fix is a real certificate or none.
3. Whatever still fails is ONE line per machine, never one per class, and one note to the
   fleet lane through the bus seam (`--bus <clone> --as <name> --to <names>`, `nova-bus
   send`; without a bus the line and the event still happen and the run says
   `escalation=unsent reason=no-bus`):

```
CERTIFY ESCALATE machine=hulk classes=go-test,path-resolves remedy="applied gobin-shadow,path-noninteractive and go-test,path-resolves still fails; go to hulk by hand"
```

A class no item repairs is never "repaired": `go-test` failing is a broken toolchain inside
the wall and no line of `~/.bashrc` fixes it, so the escalation says exactly that rather than
applying something plausible. **The failed classes stay uncertified either way**, so `fill`
refuses cards for them until a real pass proves them.

Red tests, fake-driven, no network and no wall-clock bound:
`the-fix-mapping-names-a-standard-item-per-repairable-class`,
`a-failed-class-is-repaired-and-certified-again`,
`a-class-that-still-fails-escalates-once-and-stays-uncertified`,
`a-failure-no-standard-item-repairs-escalates-without-touching-the-machine`,
`an-escalation-is-an-event-through-the-emitter`,
`apply-is-idempotent`, `apply-never-runs-the-adopt`, `no-remedy-deletes-anything`,
`the-standard-workloads-are-the-shipped-classes`,
`go-test-runs-inside-the-wall-with-the-toolchain-as-a-read-root`,
`certified-is-true-only-for-the-current-build-and-hash`,
`fill-refuses-a-card-whose-workload-is-uncertified-on-that-bench`,
`if-stale-skips-a-machine-whose-every-class-is-current`,
`adopt-with-certify-runs-the-workloads-on-each-adopted-machine-under-the-version-just-installed`,
`the-launchd-agent-runs-the-verb-the-loop-needs`.
