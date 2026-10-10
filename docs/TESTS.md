# TESTS.md: the first-run transcripts the tests execute

Every `$` line under a `### First run` heading below is run by a test against the fixture named beside it, and what the tool prints is compared with what is written here by SHAPE: the two-token event prefix and the field names in order, per [docs/ONBOARDING.md](ONBOARDING.md) point 5(c). The values are deliberately not compared, so that this file stays a document instead of becoming a fixture -- but every block below was produced by RUNNING the tool, so the values are a run's own and not anybody's memory of one. [docs/CLI.md](CLI.md) explains the tools; this file is what they do today. Change a tool, change this file in the same commit, or the test says so.

## Reading a block

**Which stream a line is on.** A transcript block shows both of a tool's
streams and says which is which. A line as written is what the tool wrote to
**standard output**. A line whose first two characters are `! ` is what it wrote
to **standard error**: strip the marker and you have the line the tool printed.
Nothing else about a line says anything about its stream.

The two streams are compared apart, because they are not the same kind of
promise:

- **Standard output is protocol, and is compared whole.** Every unmarked line
  must appear on standard output, in the order written here, and standard
  output must carry nothing else.
- **Standard error is progress, and is compared only for the lines shown.**
  Every `!` line must appear on standard error, in the order written here;
  standard error may carry more, because how loudly a tool narrates its own work
  is not a promise to a caller. An `INBOX WALK commits=1/1 notes=0 elapsed=11ms`
  arriving where this file shows none is not drift.

Run the lines with the two streams kept apart. Merging them with `2>&1` drops a
progress line into the middle of a protocol one and makes a correct run look
like a defect: some defect readings in a dogfood run were only that, across
different tools. A harness that grades this file grades standard output
against the unmarked lines and standard error against the `!` lines, and records
which stream each expectation was on.

The marker is being applied section by section. Until a
section carries it, read an unmarked line as *not yet checked* rather than as
*checked and found to be standard output*.

**Preconditions: what a step needs that the machine may not have.** Some steps
cannot run everywhere, and a reader is owed that before the fence rather than by
a failure. A section states each one in a single line of its own prose,
beginning with a keyword, exactly as `Platform:` already does:

- `Platform:` — the machine the block was recorded on, and what a different
  machine prints instead. See [`## nova-sandbox`](#nova-sandbox); checked by
  `internal/ci/firstrun_platform_test.go`.
- `Requires:` — something a step needs that the machine running it may not have,
  and in a sandboxed test bench must sometimes *not* have: a key, a forge
  credential, a posting credential, or another tool's binary. The line names the
  thing and the verbs it gates.

A harness that cannot meet a stated precondition reports
`SKIP-PRECONDITION <verb> why=<the stated line>` and that step is not a defect.
A section counts as clean when every step it could run is clean and every step
it skipped names a precondition stated here. **A step skipped for a reason this
file does not state is a defect in this file, not a pass** — being able to tell
those two apart is the whole value of writing the line down.

The `nova-secrets` fixture invokes `nova-check` in its child-command examples;
those steps need that binary on PATH.

### tests-reexec-guard-everywhere

A test binary that runs itself (`os.Executable()` or `os.Args[0]`) with words it does not answer runs the whole suite again in the child, which reaches the same test, which runs the binary again: 289 processes deep on one machine in one afternoon. Every `cmd/<tool>` whose tests run their own binary calls `testbin.Enter(tool, handled)` from an `init` in `cmd/<tool>/reexec_test.go` (`cmd/nova-sprint` from its `TestMain`), and a start of the binary is one of three things:

- **the suite**: a `go test` run, a child a test started with `-test.run`, or CLI words typed by hand with no test binary above;
- **handled**: CLI words the package's own dispatch answers (a helper process, a marked CLI child), which that dispatch runs and exits;
- **refused**, exit 3 and one line ending `refusing to recurse`: CLI words from a test binary nothing in the package answers, or a chain of test binaries `testbin.MaxDepth` deep. `testbin.DepthEnv(tool)` counts the starts of one tool, so a chain of one tool's binaries is never counted against another's.

`TestEveryReexecOfTheTestBinaryHasTheGuard` (`internal/ci/reexec_guard_class_test.go`, docs/SPEC-CI.md) refuses a `_test.go` under `cmd/` that runs its own binary in a package with no such call, naming the file and the line. The guard itself is proved by `TestDecideRunsTheSuiteHandlesItsOwnWordsOrRefuses` and `TestEnterRefusesARecursionAndAChainTooDeepInAChild` (`internal/testbin`).

### The CL shards fit two minutes

A CI shard of the CL tier is cancelled at two minutes (`timeout-minutes: 2` in `.github/workflows/ci.yml`), and the Makefile's `test` target stops a package at 110 s (`GOTEST_TIMEOUT`). The cap stays; the tests fit it. A unit test takes under a minute, ideally far under, and a CL package takes at most 60 s.

`internal/ci/testdata/shard-walls.tsv` records each CL package's wall: the package-level `Elapsed` of `go test -json` on the last green run that ran it uncached, with where it was measured (`@run<id>` for the GitHub Actions run whose log holds it, `@local` for a local run on a bench machine, one package at a time) and the runner class that measured it, read from the run's job that logged the package (`self-hosted` for the fleet's own runners, else the hosted label, `macos-latest` or `ubuntu-latest`; `-` where it is not known), since one run's jobs mix classes. `TestEveryCLPackageFitsItsShardWall` (`internal/ci/shard_walls_test.go`) refuses a row over 60 s, a live package with tests and no row, and a row for a package the tree no longer holds; `TestTheShardWallRuleRefusesGrowthAndGaps` holds the rule's reversed witnesses. A package that grows past 60 s is made to fit, or its slow tests move behind the `slow` build tag, which `.github/workflows/nightly-slow.yml` runs; then its row records the wall it has. A row is never raised past the cap.

TODO: the ledger's walls are typed numbers that the test checks and never measures; a measured wall (each CL package timed on a named runner class, compared with its row) is owed.

The ledger ratchet (`TestClassRuleLedgersOnlyShrinkAgainstMergeBase`, `internal/ci/ledger_ratchet_test.go`) reads the merge base once: one `git ls-tree -r -z` over `internal/ci/testdata` and the two slowtests ledgers, and one `git cat-file --batch` over the `.txt` files it names (`readMergeBase`). It used to start two git processes per file, some 1,200 a run; on a self-hosted runner's reused workspace that was 1m37s to 1m42s and a cancelled shard on every pull request. `TestTheLedgerTestReadsTheBaseOnce` counts the git processes through a fake runner and allows two; `TestTheSinglePassReadsWhatTheTwoCallPathRead` holds the single pass to `ListAtCommit`'s answer, path by path, on a fixture repository, and to the same shard list and findings.

## nova-bus

Run by `cmd/nova-bus/firstrun_test.go` on a throwaway redis-server whose
`friends` set names ada and bob (what `nova-config apply` writes for two friend
rows), each with a proven inbox push on `bus2:push` (what each one's friend
daemon writes when its session answers the SESSION CHECK; without it every send
and recv carries a `push=none` NOTE and lands all the same), its address in `NOVA_BUS_REDIS`, so the lines
read as a reader types them.
The sitting is the loop: bob first waits on his own empty stream and, nothing
coming within the second he gave it, is told `WAIT NONE` at exit 1 (the wait
took nothing; the arm on an empty stream is the cursor `0-0`); ada sends bob one
message; bob peeks (new, not yet
delivered), receives it through `--exec` (the header line and the body go to the
command, acked when it exits 0), acks an id that is not pending (false, exit 0: ack is idempotent), reads the
log, and lists the names. The run-owned values are the message's `id=` (a ULID
from the store's time) and its `at=`. The throwaway store has no users, so every
write says `login=none`: on the fleet's store the identity is the login user and
`--as` may be left out.

### First run

```text
$ nova-bus wait --as bob --timeout 1s
WAIT ARMED after=0-0
! WAIT NONE after=0-0 waited=1s

$ nova-bus send --as ada --to bob --subject hello --body "are you there?"
SEND OK id=01M42BA18Y1K3SE57HE26SY8T0 to=bob cc=- at=2026-10-04T02:18:54Z bytes=14 sha256=cf97adc337983a14daab1089bf14c6ab50e658f0136517e0048407e786b6e745 login=none

$ nova-bus peek --as bob
PEEK OK pending=0 new=1
PEEK MESSAGE state=new id=01M42BA18Y1K3SE57HE26SY8T0 from=ada at=2026-10-04T02:18:54Z subject="hello"

$ nova-bus recv --as bob --exec true
RECV OK id=01M42BA18Y1K3SE57HE26SY8T0 from=ada to=bob cc=- re=- at=2026-10-04T02:18:54Z login=none acked=true exec_exit=0 subject="hello"

$ nova-bus ack --as bob --id 01ARZ3NDEKTSV4RRFFQ69G5FAV
ACK OK acked=0 asked=1 login=none
ACK ID id=01ARZ3NDEKTSV4RRFFQ69G5FAV acked=false

$ nova-bus log --max 5
LOG OK total=1
LOG MESSAGE id=01M42BA18Y1K3SE57HE26SY8T0 from=ada to=bob cc=- re=- at=2026-10-04T02:18:54Z subject="hello"

$ nova-bus names
NAMES OK count=2 proven=2
NAMES NAME name=ada push=proven age=0s harness=claude
NAMES NAME name=bob push=proven age=0s harness=claude
```

## nova-friend

Run by `cmd/nova-friend/firstrun_test.go` on a throwaway redis-server whose
`friends` set names ada and bob (what `nova-config apply` writes for two friend
rows), its address in `NOVA_BUS_REDIS`, so the lines read as a reader types
them. The sitting is the canary by hand, with no daemon running: a dry-run
install prints the plan for bob's agent, OpenCode's project config first; a
dry-run uninstall the plan to undo it; a dry-run host the tmux line that would
host bob's terminal harness; ada, as the coordinator, pings bob with a nonce; bob's session answers
with `pong` (one note to ada, and the pong file under the home directory,
`./home/.nova-friend/bob`); `wait-pong` finds it on the log from bob's own
stream; `status` says no daemon has run as bob (exit 1). `./` is a directory of the test's own, with bob's directory `./bob` made in it
first (install, `--dry-run` too, refuses a `--dir` that is not a real
directory and never makes one), and so are the
home directory and the uid the plan names. The run-owned values are the
message `id=` (a ULID from the store's time), `at=`, and `took=`.

`serve`, the coordinator's ping loop, is not a step of this sitting: it runs
until a signal, so the banner has no example of it to run here. What it prints
is pinned in `cmd/nova-friend/serve_test.go` on the in-memory store with an
injected clock: `SERVE OK friends= every= down_after=` once (with `dry_run=true`
and nothing sent under `--dry-run`), then one `SERVE UP` or `SERVE DOWN` line
per state change, and `SERVE STOP interrupted` at a signal (docs/CLI.md, "The
coordinator's ping loop").

`watch`, the coordinator's wake, is not a step either: it waits for a message
or a wake line that arrives after it starts, or for `--timeout`, so the banner
has no example of it; its `-h` example is run on the in-memory store with an
injected clock in `cmd/nova-friend/watch_test.go`.

### First run

```text
$ nova-friend install --as bob --harness opencode --dir ./bob --dry-run
INSTALL OK label=com.nova.friend-bob plist=./home/Library/LaunchAgents/com.nova.friend-bob.plist launchd_log=./home/Library/Logs/nova-friend-bob.log dry_run=true
INSTALL PLAN command="write ./bob/opencode.json permission.external_directory../bob/**=allow"
INSTALL PLAN command="write ./home/Library/LaunchAgents/com.nova.friend-bob.plist"
INSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"
INSTALL PLAN command="launchctl bootstrap gui/501 ./home/Library/LaunchAgents/com.nova.friend-bob.plist"
INSTALL NOTE the agent runs: nova-friend run --as bob --harness opencode --dir ./bob --width 0, with --redis and --server as given here

$ nova-friend uninstall --as bob --dry-run
UNINSTALL OK label=com.nova.friend-bob plist=./home/Library/LaunchAgents/com.nova.friend-bob.plist dry_run=true
UNINSTALL PLAN command="launchctl bootout gui/501/com.nova.friend-bob"
UNINSTALL PLAN command="rm ./home/Library/LaunchAgents/com.nova.friend-bob.plist"

$ nova-friend host --as bob --harness aider --dir ./bob --dry-run -- aider
HOST DRY-RUN session=friend-bob dir=./bob dry_run=true command="tmux new-session -d -s friend-bob -c ./bob -- aider"

$ nova-friend ping --as ada --to bob --nonce abc123
PING OK nonce=abc123 id=01M42EJZ1D4JEFR6ESF1YJ3YJA to=bob at=2026-10-04T03:40:12Z
PING NOTE wait for it: nova-friend wait-pong --from bob --nonce abc123

$ nova-friend pong --as bob --nonce abc123 --to ada --queue 2 --working 1 --width 4
PONG OK nonce=abc123 to=ada id=01M42EJZ1F8FXB5T0F6EXJCRS1 at=2026-10-04T03:40:12Z

$ nova-friend wait-pong --from bob --nonce abc123 --timeout 2s
WAIT-PONG OK nonce=abc123 from=bob at=2026-10-04T03:40:12Z took=1ms queue=2 working=1 width=4 daemon=false

$ nova-friend status --as bob --dir ./bob
! STATUS NONE: no daemon has run as bob (no status file in ./home/.nova-friend/bob); run: nova-friend install --as bob --harness <h> --dir ./bob
```

## nova-sandbox

Fixture: a job directory of yours. Every path below is one you name — this tool has no defaults and guesses nothing — so the transcript is a worked example with `/path/to/pool` standing in for yours, and the lines are what the platform prints with the paths shortened.

Platform: recorded on macOS (darwin) — the `backend=sandbox-exec` and `abi=-` fields and the `/path/to/pool` fixture below are that Mac's; a Linux bench prints `backend=landlock`, an `abi=` value, and, where the wall is built below the ABI the kernel reports, a `used=` field this transcript has no slot for.

`read_root` reads the probe's own executable, `os.Executable()`, because the root it
exercises is "the directory of the resolved command" and the probe's child is this
binary; a transcript that named a shell there would be measuring `/bin`, which the
profile grants verbatim. `TestTheTranscriptNamesTheToolsOwnBinary` holds that line here.
The probe sets `HOME` because `HOME` must resolve inside a `--write`, and
the dispatcher's own `HOME` does not.

### First run

```
$ nova-sandbox check
CHECK OK backend=sandbox-exec abi=- net=enforceable hosts=none note=sandbox-exec is deprecated by Apple and works on macOS 26; the wall is the profile it applies; backend at /usr/bin/sandbox-exec

$ HOME=/path/to/pool/jobs/j1/home nova-sandbox probe --read /path/to/pool/ref --write /path/to/pool/jobs/j1 --secret /path/to/.config/anthropic/env
PROBE STEP name=write_outside_control expect=allow got=allow path=/path/to/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=write_outside expect=deny got=deny path=/path/to/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=read_secret expect=deny got=deny path=/path/to/.config/anthropic/env
PROBE STEP name=write_inside expect=allow got=allow path=/path/to/pool/jobs/j1/.nova-sandbox-probe-inside
PROBE STEP name=read_root expect=allow got=allow path=/path/to/bin/nova-sandbox
PROBE OK backend=sandbox-exec abi=- steps=5 passed=5 net=nopromise gpu=none

$ HOME=/path/to/pool/jobs/j1/home nova-sandbox --read /path/to/pool/ref --write /path/to/pool/jobs/j1 -- /bin/sh -c 'echo hello > report.md; cat /path/to/.config/anthropic/env'
SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AGENT_PID SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies
SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/path/to/pool/jobs/j1 cwdb64=L3BhdGgvdG8vcG9vbC9qb2JzL2ox ancestors=11 cmd=sh gpu=none deletes=/path/to/pool/jobs/j1
cat: /path/to/.config/anthropic/env: Operation not permitted
SANDBOX DONE exit=1 cmd=sh
```

The last run is the whole tool: the wall named, the job's own write landed, the same command could not read the key that was in neither list, and the closing line gives the wrapped command's own status, 1 here because `cat` failed, which is the status the tool exits with.

### The disposable volume, and the one test that touches a disk

`nova-sandbox run` makes an APFS volume per run and deletes it on every path out (SPEC-SANDBOX, "The run verb"). Its logic is unit-tested against a fake `diskutil`, so an ordinary `go test ./cmd/nova-sandbox/` creates no volumes. The one real end-to-end test is behind the `novadisk` build tag, because eight CI runners share the Mac this repository is built on and a suite that made and destroyed volumes on every run would be a hazard rather than a test. Run it by hand on a Mac when the disposable-volume body changes — it needs no `sudo`:

    go test -tags novadisk -run TestARealRunLeavesNothingBehind ./cmd/nova-sandbox/

`nova-sandbox run --go` is what a card that builds Go uses; a plain `go build` inside a disposable volume was measured working with no flags at all once the optional roots' ancestors were granted (`internal/sandbox`, `TestAnOptionalRootsAncestorsAreGranted`).

The disposable-volume check makes a volume, runs `sh -c 'echo hi > out; sleep 1'` inside the wall with the volume as its only writable directory, and removes the volume from `/Volumes` and `diskutil apfs list` afterwards — `SANDBOX DONE name=e2e63562 exit=0 wall=9.500 freed=32768`.

## nova-secrets

Fixture: a throwaway secrets store git working copy and age private keys, as in
[SPEC-SECRETS.md](SPEC-SECRETS.md). The seat names `example` and `reader` are
fixture identities. Replace `/path/to/home` with your fixture home and
`/path/to/bin` with the directory holding your `age-keygen` and `sops` binaries.
Run the commands from that fixture home. The key directory already exists with
mode 0700. The store at `./secrets` lives
under that home, holds the reader seat and its recovery recipient, and is on a
clean branch equal to its upstream ref. Its sealed `GH_TOKEN` is synthetic;
`gh` on the fixture's PATH is a stand-in that prints `fake-gh` without making a
network call. Public keys and the commit id below belong to the recorded run.

### First run

```
$ nova-secrets keygen --as example --key /path/to/home/.config/nova-secrets/example.key --age-keygen /path/to/bin/age-keygen --store ./secrets
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^example\.yaml$
SECRETS RULE       age: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk,age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
SECRETS RULE NEXT: add these two lines to .sops.yaml (or run `nova-secrets seat add`)
SECRETS KEYGEN OK as=example key=/path/to/home/.config/nova-secrets/example.key mode=0600 pub=age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk
Done. Your new key is at /path/to/home/.config/nova-secrets/example.key. Nothing failed.
Next: send this public key to whoever seals your seat: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk

$ nova-secrets check --store ./secrets --as reader --key /path/to/home/.config/nova-secrets/reader.key --sops /path/to/bin/sops
SECRETS CHECK OK as=reader recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=9750ba9

$ nova-secrets names --store ./secrets --as reader
SECRETS NAME key=GH_TOKEN clear=false
SECRETS NAMES OK as=reader keys=1 shown=1 sealed=1 clear=0

$ nova-secrets exec --store ./secrets --as reader --key /path/to/home/.config/nova-secrets/reader.key --sops /path/to/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
! SECRETS EXEC OK as=reader keys=1 only=1 required=1 file=secrets/reader.yaml head=9750ba9 cmd=gh
fake-gh
```

`nova-secrets seat inject` is measured against the real sops and age
(`cmd/nova-secrets/seat_inject_functional_test.go`, functional tier): a store with
two seats, one holding the new value and the other holding the to-be-replaced
one; the verb run with the coordinator's key and `--no-pr`; then the seal branch's
file opens with the bench's key alone and holds the new value beside the names it
had, the store is back on `main`, and `nova-secrets gate` approves the branch. The
help banner's own `seat inject` example is run through the one comparator in the
same package, including its committed-branch receipt. The transcript is held
beside that test, with the branch's timestamp the one declared run-owned value.

A bench whose store file omits the `swarm-` prefix must be asked for under that
file's name, or the launcher reads an empty store.

## nova-check

Fixture: `cmd/nova-check/testdata/example-self`.

### First run

```
$ nova-check quickstart --dir ./self
QUICKSTART RUN dir=./self checks=2: links, then nocode
LINKS OK dir=./self files=4 links=3 excluded=0 broken=0
NOCODE OK dir=./self files=5 deny-list=floor-list findings=0
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)

$ nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
KERNEL OK file=./self/docs/SEED-CORE.md bytes=771 budget=4000 findings=0
```

The included `example-self` fixture has `SEED-CORE.md` but no `SEED.md`, so it
cannot demonstrate `floors` by itself. That check compares a derived core with
the matching source seed it came from; name a core and source pair you own rather
than borrowing an unrelated `SEED.md` merely to make the command pass.

### hygiene, on a branch

The four checks the accept gate runs, over a two-commit lab: `main` with one
file, `card` with the fix on it and then a commit by somebody outside the pool
that also strays outside the card's paths.

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**"
HYGIENE OK repo=. base=main head=card paths=sign/** findings=0

$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**" --max 2
HYGIENE FAILED repo=. base=main head=card paths=sign/** findings=4
HYGIENE FINDING reason=identity at=0a19082d2973 why="author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity"
HYGIENE FINDING reason=out-of-path at=elsewhere.go why="this path matches none of the card's declared PATHS: sign/**"
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
```

The `MORE` line is the same run with the cap lifted, quoted so it can be pasted
back — it is the command that prints the rest, and it carries the
`--identity`, `--paths` and `--kind` without which it would not run at all:

```
$ nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
HYGIENE FAILED repo=. base=main head=card paths=sign/** findings=4
HYGIENE FINDING reason=identity at=0a19082d2973 why="author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity"
HYGIENE FINDING reason=out-of-path at=elsewhere.go why="this path matches none of the card's declared PATHS: sign/**"
HYGIENE FINDING reason=out-of-path at=elsewhere/x.go why="this path matches none of the card's declared PATHS: sign/**"
HYGIENE FINDING reason=stray-file at=sign/RESULT.md why="an added file matching the stray list's RESULT.md"
```

`--identity` takes one pair of angle brackets. A second pair is refused rather
than matched against nobody:

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <<ada@example.com>>"
HYGIENE REFUSED: --identity "Ada <<ada@example.com>>": the email carries an angle bracket; want `Name <email>`, one pair; run: nova-check help
```

`--kind` is a card kind the toolchain declares, and there is no default one. One
it does not hold is refused by name rather than left to unlock nothing:

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --kind fix-with-red-test
HYGIENE REFUSED: --kind "fix-with-red-test" is not a kind this tool declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, ledger, read, probe, text, tone, report; run: nova-check help
```

## nova-self-talk

Fixture: `cmd/nova-self-talk/testdata/example-pages`, built into the binary:
`nova-self-talk example ./pages` writes it to `./pages`, which is what the lines below type.

A line below opening `! ` is one this tool writes to standard ERROR: the
findings go there and the protocol lines go to standard output, and the order a
terminal interleaves the two in is not the same twice — the second block's last
finding arrived after the `NOTE` line on one bench and before it on another.
That is why the block cannot be read as one stream.
`# Stderr: whole` on a command line says the marked lines are ALL it
writes there: these are findings, not narration, and a transcript that quietly
lost one would be hiding the thing the tool exists to say.

### First run

```
$ nova-self-talk ./pages/journal.md   # Stderr: whole
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
! SELFTALK FAIL ./pages/RULES.md:8: VERDICT-IDIOM match="dead as a practice": A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=2
SELFTALK FAIL files=2 claims=2 standing=1 installations=2 dated=1 shown=3
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --skip RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK SKIP ./pages/RULES.md (--skip)
! SELFTALK FAIL ./pages/journal.md:4: STANDING match="cannot check": I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: RANKING match="worst habit I have": It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only (list them: nova-self-talk shapes): register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.
```

## nova-fuse

Fixture: `cmd/nova-fuse/testdata/example-box.json`.

### First run

```
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAILED quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-public-issue-tracker')

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell the person you work with now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAILED quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')

$ nova-fuse lift quarantine --box ./fuse-box.json a-forum
LIFT OK quarantine=a-forum was since=2026-09-09T18:27:40Z: a post addressed me and asked for a token
LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)
```

## nova-memory

Fixture: `cmd/nova-memory/testdata/corpus`.

### First run

```
$ nova-memory quickstart --root ./corpus
QUICKSTART RUN root=./corpus steps=3 channels=bm25 k=3/2 words-source=corpus-top-terms candidate=corpus-first-paragraph words="glazing minutes pressure"
$ nova-memory stats --root ./corpus
STATS OK schema=nova-memory/2 files=6 chunks=20 bytes=4866 vocab=380 avg-terms=39.3 build=822.917µs
STATS OK class=. chunks=3
STATS OK class=log chunks=4
STATS OK class=notes chunks=13
$ nova-memory search --root ./corpus --channels bm25 --k 3 glazing minutes pressure
SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="glazing minutes pressure"
SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=3.48 score-channel=bm25 fused=0.01667 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:13 "Measured over one winter: glazing washed weekly held its polish; glazing\nwashed monthly needed grinding twice. The weekl…"
SEARCH HIT rank=2 score=3.12 score-channel=bm25 fused=0.01639 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
SEARCH HIT rank=3 score=2.97 score-channel=bm25 fused=0.01613 class=notes name=fog-signal type=measured root=./corpus: notes/fog-signal.md:8 "The diaphone runs on compressed air, and the compressor needs eleven minutes\nto bring the receiver to working pressure f…"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: HANDBOOK.md:3
$ nova-memory check --root ./corpus --channels bm25 --k 2 -
MEMORY OK candidates=1 source=- k=2 channels=bm25 files=6 chunks=20
MEMORY CAL score=4.05 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs…"
MEMORY HIT cand=1 rank=1 score=90.20 score-channel=bm25 fused=0.01667 class=. name=- type=- root=./corpus: HANDBOOK.md:3 "This fixture corpus belongs to an invented lighthouse station. It exists so\nthat nova-memory's verbs can be exercised …"
MEMORY HIT cand=1 rank=2 score=15.01 score-channel=bm25 fused=0.01639 class=log name=- type=- root=./corpus: log/1974-03-11.md:11 "Left a note to write up the [[storm-glass]] readings against the barometer\none day, because the two disagree in a way th…"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
QUICKSTART OK done=3
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

```
$ nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
SEARCH OK hits=3 k=3 channels=bm25 files=6 chunks=20: query="lantern glazing brass"
SEARCH CAL score=4.05 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=5.33 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
SEARCH HIT rank=2 score=5.02 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:8 "The lantern glazing collects a salt haze on every onshore wind, and the haze\nis not visible from inside the lightroom at…"
SEARCH HIT rank=3 score=3.14 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:3 "Onshore gale most of the day, easing after dark. Washed the glazing at first\nlight before the wind got up again — see …"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic

$ nova-memory check --root ./corpus --channels bm25 --k 3 draft.md
MEMORY OK candidates=1 source=draft.md k=3 channels=bm25 files=6 chunks=20
MEMORY CAL score=4.05 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "The lantern glazing is cleaned with two cloths, one for the brass and one for the glass, before the …"
MEMORY HIT cand=1 rank=1 score=13.62 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:8 "- [lantern-care](lantern.md) — the glazing, the brass, and the two cloths\n- [tide-tables](tides.md) — the jetty's ei…"
MEMORY HIT cand=1 rank=2 score=11.40 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:8 "The lantern glazing collects a salt haze on every onshore wind, and the haze\nis not visible from inside the lightroom at…"
MEMORY HIT cand=1 rank=3 score=7.56 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:3 "Onshore gale most of the day, easing after dark. Washed the glazing at first\nlight before the wind got up again — see …"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
```

## nova-swarm

Fixture: owned directories under `t.TempDir()` and fake harnesses. The transcript
comparators invoke the dispatcher with fixture paths and compare its output with
the examples below. The [quickstart guide](nova-swarm-quickstart.md) shows a
`native` run and a sprint member.

### The budget word on the native route

Every `nova-swarm native` launch carries `--tokens <n>` or `--tokens unmetered`
(SPEC-SWARM). Recorded against the fake harness, with the paths
abridged:

```
$ nova-swarm native --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
nova-swarm native: --tokens is required; it wants a token budget for this job, or the word `unmetered` when this provider has no live accounting and the deadline is the only stop; refusing to guess

$ nova-swarm native --tokens 0 --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
nova-swarm native: --tokens is a budget and is at least 1, got 0; `unmetered` is how a caller says there is no accounting

$ nova-swarm native --tokens unmetered --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE NOTE: no harness store: looked at ./root/slot-1/data/opencode/opencode.db and ./root/slot-1/data/.local/share/opencode/opencode.db
NATIVE OK label=card job=./root/slot-1/jobs/card tmp=./root/slot-1/tmp/card rc=0 wall=0.18s sandbox=none-by-flag card_sha256=ab6468b200da0b3d5a0175e863d1b1cf772f010abdf4fe70b3ea39926f3fc826 binary_sha256=13c788f4813d7d81152460f6a16d45123314342ce0269f3fb43e6c8438192852 config=68719609 harness=ok budget=unmetered usage=none reason=no-store path=./root/slot-1/data/opencode/opencode.db
```

Both refusals exit 2 and make no directory: `<slot>/jobs`, `<slot>/data` and `<slot>/tmp`
do not exist afterwards. `budget=` follows `harness=` on every `NATIVE OK` line.

### A budget nothing can observe, refused before anything is made

A budget wants a source this tool can read. The source is the worker
description's `usage`, and `opencode` — read with `sqlite3` — when there is no `--worker`:

```
$ nova-swarm native --tokens 100000 --worker ./usage-none.json --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: a numeric --tokens wants a usage source this tool can read, and the worker description says `usage: none`, which reports nothing; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Give the description `usage: opencode`, or launch with --tokens unmetered and no max_turns or max_cache_read

$ PATH=./empty nova-swarm native --tokens 100000 --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: a numeric --tokens is read from the harness's own database with `sqlite3 -readonly`, and sqlite3 is on no PATH entry of this bench; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Install sqlite3 on this bench, or launch with --tokens unmetered and no max_turns or max_cache_read
```

The card's own budget is read from the same source, so it meets the same refusal whatever
`--tokens` says — `unmetered` included:

```
$ nova-swarm native --tokens unmetered --worker ./usage-none-max-turns.json --harness ./fakeharness --model fake/fake-model --card ./card.md --slot ./root/slot-1 --root ./root --deadline 30s --no-wall --slots-store ./store --owner me
! NATIVE REFUSED: this worker description's max_turns wants a usage source this tool can read, and the worker description says `usage: none`, which reports nothing; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Give the description `usage: opencode`, or launch with --tokens unmetered and no max_turns or max_cache_read
```

`--tokens unmetered` with no such description runs under both conditions, as it does today.
After every refusal above, `<slot>` is empty: nothing was made.

### What the line reports against the number

The fake harness writes a **real sqlite database** in the harness's own shape
(`FAKE-USAGE-DB`, the five token counts in order then `usd`, with `-` for a type
the provider did not report). Under `--tokens 50000`, the `NATIVE OK` line's `budget=`:

```
# a harness that reported nothing
harness=ok budget=-/50000
# only tokens_in
harness=ok budget=900+/50000
# every column a reported zero
harness=ok budget=0/50000
# tokens_in 100, tokens_out 50, cache_write 9000, cache_read 90000, reasoning 7
harness=ok budget=157/50000
```

The last is the whole of the sum rule: `tokens_in + tokens_out + reasoning` is 157, and the
99,000 of cache stands in the usage row and never in the budget. A reported `0` is a
measurement and prints `0/50000` — never `unmetered`. `--tokens unmetered` prints the word
whatever the harness reported.

### The stop

A card that publishes a report, spends past `--tokens 100000` and then declines the
terminate, under `--deadline 120s` so that the budget is what ends it:

```
$ nova-swarm native --tokens 100000 --usage-interval 1s … --deadline 120s
NATIVE OK label=card job=./root/slot-1/jobs/card tmp=./root/slot-1/tmp/card rc=-1 wall=5.05s sandbox=none-by-flag card_sha256=8e1f… binary_sha256=ad88… config=ffdf555f harness=ok budget=100000/100000 stopped=tokens
```

Exit 1. The launch's own row carries `end=budget` and a dash for `rc`, while the line prints
`rc=-1`:

```
$ cut -f1-8 ./root/slot-1/jobs/card/usage.tsv
job	attempt	started	ended	end	rc	provider	model
card	1	2026-09-19T13:16:05Z	2026-09-19T13:16:10Z	budget	-	fake	fake-model
```

And what the card published is kept byte for byte — the tool writes nothing into it:

```
$ grep -c PROMPT-DEFECT ./root/slot-1/jobs/card/RESULT.md
0
$ grep "findings:" ./root/slot-1/jobs/card/RESULT.md
findings: 2
```

### The sample interval's floor and ceiling

```
$ nova-swarm native --tokens unmetered --usage-interval 900ms … --deadline 30s
! nova-swarm native: --usage-interval is at least 1s, got 900ms; each sample launches sqlite3 against the harness's own live database, and under a second that is more launches than there is anything new to read

$ nova-swarm native --tokens unmetered --usage-interval 30s … --deadline 30s
! nova-swarm native: --usage-interval is shorter than --deadline, got 30s against a deadline of 30s; at or past the deadline no sample would ever run and the budget could not fire
```

`1s` exactly is accepted — the floor is inclusive — and the ceiling is exclusive.

### First run

```
$ nova-swarm template --name read-pr
read-pr — read one pull request against the rules

1. READ THE PR BODY'S OWED LIST FIRST, before reading any code, and for every
   finding you report, say whether it is already on that list. A finding that
   is already owed is marked `dup:` and is not a new finding.
   [batch 1: 25 of 67 findings were duplicates of the owed list]
2. QUOTE EVERY RULE VERBATIM, with `file:line`. Never paraphrase a rule from
   memory, and never assert a rule you did not open.
   [batch 1: 5 of 67 findings were wrong, each a paraphrase]
3. APPEND EACH FINDING TO RESULT.md THE MOMENT IT EXISTS. Not at the end.
   You may be killed at your deadline; what is on disk is what you found.
4. A FILE BUDGET: read at most <n> files (the limit stated in the card). When the budget
   is spent, write what you have and stop. Say in RESULT.md which files you
   did not open.
   [batch 3: with a budget, 2 of 3 tasks complete; without, 0 of 3]
5. A RESULT.md CONTAINING ONLY A PLAN IS A FAILED TASK. The plan belongs at
   the top, before the work; the findings are the work. A finished read that
   found nothing is NOT a failed task: write the `## Head` with `findings: 0`.
   Never report a finding to have something to report.
6. If a board was supplied, check it before reporting: a card that already names
   this is a `dup:`. Do not search for an unspecified board.
7. A SEVERITY FLOOR: emit only findings at or above `HIGH`. A finding below the
   floor is not emitted at all. State the floor in RESULT.md's `## Head`
   paragraph as `floor: HIGH`, and mark each emitted finding with its
   severity. The floor decides which findings are emitted, not how they are
   written: every emitted finding still quotes its rule verbatim with `file:line`.

Keep RESULT.md concise: omit progress narration, praise, repeated task text, and a
separate summary. Each finding keeps its proof in compact form: severity, `file:line`,
the exact quoted rule, the fix, and `dup:` status when applicable. Retain every valid
finding, its context and evidence, and any coverage limitation; do not drop context or
evidence by default. Brevity is a soft target: never hard-truncate findings or proof; if
the report overflows, preserve the proof and say so. Preserve the complete RESULT.md
shape and its mandatory `## Head`, `## Findings`, `## Per item`, `## Gates`,
`## Left owed`, and `## One line` sections.
In Gates, distinguish source checks from tests and report-writing commands.
Mark only checks actually performed as pass; no tests run does not mean no commands run.

BOUND THE REPORT: findings only. No narration of the clone, no restated
task, no praise, no summary. One line per finding: `file:line`, the rule
quoted verbatim in at most twelve words (a longer rule by the twelve of its
own words the finding rests on, never a paraphrase: rule 2 holds), the
severity, and the fix in one clause. Keep RESULT.md under 40 lines and
every line under 300 characters, and no pipe inside backticks: a `|` in a
quote breaks the report's table grammar, so quote the rule without it. Put
the verdict line last. When there is nothing to report, write `findings: 0`.
```

## nova-tokens

Fixture: `cmd/nova-tokens/testdata/example-bench` (copied into a temp directory first, because a first run WRITES; the bus lane is `example.com`).

`fold` writes the token tables under `--out`, which must already exist; make it
first:

```sh
mkdir -p ./out
```

### First run

```
$ nova-tokens fold --out ./out --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --bus ./bus
TOKENS FOLD at=2026-09-11T23:55:02Z build=devel out=./out sources=3 days=2026-09-11 repos=./repos.tsv
TOKENS SOURCE label=claude:bench kind=claude path=./transcripts reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=3 dup=1 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=2
TOKENS SOURCE label=bus:peer1 kind=bus path=bus/from-peer1 reports=input,output day_basis=utc files=1 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=1 redated=0 superseded=0 rows=1
TOKENS SOURCE label=bus:peer2 kind=bus path=bus/from-peer2 reports=- day_basis=utc files=0 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=0 redated=0 superseded=0 rows=0
TOKENS TOUCHED label=bus:peer1 day=2026-09-11 repos=schema,serialize
TOKENS DAY day=2026-09-11 rows=3 models=2 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=6 nonutc=0 sources=bus:peer1,claude:bench written=true
TOKENS OK days=1 rows=3 sources=3 unreadable=0 unparsed=0 mixed=0 conflict=0 shrank=0 partial=0 quiet=0
TOKENS NOTE nothing was wrong; nova-tokens check --out ./out is the gate

$ nova-tokens check --out ./out
CHECK OK at=2026-09-11T23:55:02Z build=devel files=1 rows=3 first=2026-09-11 last=2026-09-11 missing=0 stray=0 gap=0 notes=0

$ nova-tokens sum --out ./out --month 2026-09
SUM MONTH month=2026-09 at=2026-09-11T23:55:02Z build=devel days=1 first=2026-09-11 last=2026-09-11 missing=0 rows=3 turns=3
SUM PAIR model=claude-fable-5-1 repo=schema input=908 output=1535 cache_write=1200 cache_read=242000 reasoning=- rough=0 dashes=0,0,0,0,1 nonutc=0 days=1
SUM PAIR model=gemini-2.5-pro repo=schema input=123456 output=7890 cache_write=- cache_read=- reasoning=- rough=0 dashes=0,0,1,1,1 nonutc=0 days=1
SUM PAIR model=claude-fable-5-1 repo=serialize input=430 output=58 cache_write=- cache_read=4000 reasoning=- rough=0 dashes=0,0,1,0,1 nonutc=0 days=1
SUM MODEL model=claude-fable-5-1 input=1338 output=1593 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,1,0,2 nonutc=0 repos=2
SUM MODEL model=gemini-2.5-pro input=123456 output=7890 cache_write=- cache_read=- reasoning=- rough=0 dashes=0,0,1,1,1 nonutc=0 repos=1
SUM TOTAL input=124794 output=9483 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,2,1,3 nonutc=0 turns=3 pairs=3 models=2
SUM OK month=2026-09 days=1 missing=0 pairs=3 models=2 nonutc=0
```


## nova-update

### First run

With the binary alone, in an empty directory: `example` writes the one-tool Go
manifest and `report` reads it. This writes `versions.tsv` and performs no update
or bus action.

```text
$ nova-update example --out versions.tsv
EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false
EXAMPLE NOTE next: nova-update report --file versions.tsv
$ nova-update report --file versions.tsv
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=23ms file=versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-02T02:55:03Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
```


## nova-version

### First run

With the binary alone, in an empty directory: `example` writes the one-tool Go
manifest and `report` reads it. This writes `versions.tsv` and performs no update
or bus action.

```text
$ nova-version example --out versions.tsv
EXAMPLE OK wrote=versions.tsv entries=1 unchanged=false
EXAMPLE NOTE next: nova-version report --file versions.tsv
$ nova-version report --file versions.tsv
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=24ms file=versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-02T02:55:03Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
```


## nova-config

No database: the first run keeps its rows in `./try.json` (`--file`), the
same kinds, refusals and history as PostgreSQL, and
`cmd/nova-config/firstrun_test.go` runs each `$` line in `t.TempDir()`. The
history's `at=` is the instant of the run, the one value that differs on a
second run. The real runs need a Postgres (`nova-config migrate`) and a Redis
(`nova-config apply`); `docs/nova-config/README.md` walks them, and
`cmd/nova-config/config_functional_test.go` runs them against a throwaway
Postgres and a throwaway Redis.

### First run

```text
$ nova-config migrate --file try.json
CONFIG MIGRATE file=try.json from=0 to=37 applied=37

$ nova-config machine add m1 --user nova --seat s1 --slots 8 --width 4 --actor a1 --file try.json
CONFIG ADD kind=machine name=m1 rev=1
UNAPPLIED rev=1: --redis is required: host:port (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-config apply

$ nova-config machine set m1 --width 6 --actor a1 --file try.json
CONFIG SET kind=machine name=m1 rev=2 changed=width
UNAPPLIED rev=2: --redis is required: host:port (or NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, or a seat); run: nova-config apply

$ nova-config machine list --file try.json
MACHINE name=m1 user=nova seat=s1 slots=8 runners=0 width=6 tla=false note=-
CONFIG LIST kind=machine rows=1

$ nova-config machine history m1 --file try.json
HISTORY id=1 kind=machine name=m1 op=add actor=a1 at=2026-10-02T03:18:20Z note=- runners=0 seat=s1 slots=8 tla=false user=nova width=4
HISTORY id=2 kind=machine name=m1 op=set actor=a1 at=2026-10-02T03:18:20Z width=4>6
CONFIG HISTORY kind=machine name=m1 changes=2
```

`migrate --file` makes the file at this binary's schema; each write prints
its history id (`rev=`); `list` is one typed line per row and a count;
`history` is every change, who made it and when, a set as
`<field>=<before>><after>`.

`kinds` (no store) is one line per kind: its table under schema `config`, its fields in
the order every line prints them, the fields `add` requires, and whether the
kind is many rows or one (`rows=one`: the fleet and the sprint, a row
`migrate` creates and `set` changes, with no add, remove or list). `migrate --print` lists the
migrations this binary carries and connects to nothing; `migrate --pg <dsn>`
applies the ones the database lacks, each in its own transaction, and applies
nothing twice.


## nova-cairn

No fixture: the store is created by the run itself. Every line below is local
— plain files under the named store, no Redis, no remote, no network — and
`cmd/nova-cairn/firstrun_test.go` runs each `$` line in `t.TempDir()`, so the
`./cairns` below is a fresh directory per run. The stamps come from `--now`
because a transcript must read the same twice; a stranger's first run omits
it and the real clock answers instead.

### First run

```text
$ nova-cairn open --store ./cairns --session s1 --source bench-a/session-7 --publish manual --now 2026-09-17T12:00:00Z
OPEN OK session=s1 store=./cairns source=bench-a/session-7 publish=manual stamp=2026-09-17T12:00:00Z

$ nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep" --source bench-a/session-7#L3 --publish manual --now 2026-09-17T12:05:00Z
APPEND OK session=s1 entry=e1 source=bench-a/session-7#L3 persisted=true published=false publish=manual duplicate=false stamp=2026-09-17T12:05:00Z

$ nova-cairn index --store ./cairns
INDEX OK sessions=1 entries=1
INDEX SESSION session=s1 publish=manual opened=2026-09-17T12:00:00Z entries=1
INDEX ENTRY session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3

$ nova-cairn receipt --store ./cairns --session s1 --entry e1
RECEIPT OK session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3 persisted=true published=false publish=manual
```

## nova-decide

Fixture: `cmd/nova-decide/testdata/`: a schema and a state, a card and its
diff, a child's RESULT.md, a red gate's go test output, a card to add
(`greet.md`), the fixed backend's answers for each decision (ask, read, score,
attempt, grade, gate, brief), and a record of eight labelled read decisions and
five score decisions of landed diffs. Every line below uses the fixed backend,
so it needs no network and no key; `cmd/nova-decide/firstrun_test.go` runs each
`$` line from a checkout root in one sitting, with `./decisions.jsonl` a file in
the test's own directory. The ids come from `--op`, so every line reads the same
twice.

### First run

```text
$ nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first
ASK OK id=first decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
ASK ANSWER question=asks_something type=noul value=yes p=yes:0.94
ASK ANSWER question=kind type=choice value=request p=question:0.08,report:0.05,request:0.87

$ nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op card-1
READ OK id=card-1 decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new
READ ANSWER question=defect type=noul value=no p=yes:0.04
READ ANSWER question=does_task type=noul value=yes p=yes:0.96
READ ANSWER question=inside_paths type=noul value=yes p=yes:0.99
READ ANSWER question=lines_changed type=noul value=yes p=yes:0.97
READ ANSWER question=verdict type=choice value=LAND p=BOUNCE:0.05,LAND:0.92,UNSURE:0.03

$ nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab
SCORE OK id=card-1@landed@0123456789ab decision=score backend=fixed top=record_made_claim p=0.08 tokens_in=0 tokens_out=0 recorded=new
SCORE ANSWER question=asserted_data_cut type=noul value=no p=yes:0.05
SCORE ANSWER question=comment_contradicts_code type=noul value=no p=yes:0.05
SCORE ANSWER question=cut_citation type=noul value=no p=yes:0.07
SCORE ANSWER question=defect type=noul value=no p=yes:0.06
SCORE ANSWER question=does_task type=noul value=yes p=yes:0.95
SCORE ANSWER question=fenced_block_edit type=noul value=no p=yes:0.03
SCORE ANSWER question=inside_paths type=noul value=yes p=yes:0.99
SCORE ANSWER question=invented_reason type=noul value=no p=yes:0.06
SCORE ANSWER question=ledger_ceiling type=noul value=no p=yes:0.03
SCORE ANSWER question=lines_changed type=noul value=yes p=yes:0.96
SCORE ANSWER question=load_bearing_word_cut type=noul value=no p=yes:0.04
SCORE ANSWER question=record_made_claim type=noul value=no p=yes:0.08
SCORE ANSWER question=renamed_file_assumed type=noul value=no p=yes:0.02
SCORE ANSWER question=stranded_fragment type=noul value=no p=yes:0.04
SCORE ANSWER question=test_weakened type=noul value=no p=yes:0.02
SCORE ANSWER question=verdict type=choice value=LAND p=BOUNCE:0.05,LAND:0.92,UNSURE:0.03

$ nova-decide attempt --brief ./cmd/nova-decide/testdata/card.md --result ./cmd/nova-decide/testdata/result.md --reason "verdict not-done: tests red in internal/decide" --backend fixed --answers ./cmd/nova-decide/testdata/attempt-answers.json --record ./decisions.jsonl --op c1@1
ATTEMPT OK id=c1@1 decision=attempt backend=fixed class=needs-pro p=0.78 tokens_in=0 tokens_out=0 recorded=new
ATTEMPT ANSWER question=class type=choice value=needs-pro p=done:0.04,needs-pro:0.78,no-result:0.08,nothing-to-do:0.02,provider-failure:0.02,wrong-scope:0.06

$ nova-decide grade --brief ./cmd/nova-decide/testdata/card.md --backend fixed --answers ./cmd/nova-decide/testdata/grade-answers.json --record ./decisions.jsonl --op c1@grade
GRADE OK id=c1@grade decision=grade backend=fixed grade=flash p=0.71 tokens_in=0 tokens_out=0 recorded=new
GRADE ANSWER question=grade type=choice value=flash p=flash:0.71,pro:0.08,script:0.21

$ nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate
GATE OK op=c1@1@gate decision=gate backend=fixed failures=2 route=caused
GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=c1@1@gate/example/tools/internal/serve.TestPortInUse class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new
GATE FAILURE key=example/tools/internal/greet.TestGreetNamesTheReader id=c1@1@gate/example/tools/internal/greet.TestGreetNamesTheReader class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new

$ nova-decide brief --card ./cmd/nova-decide/testdata/greet.md --backend fixed --answers ./cmd/nova-decide/testdata/brief-answers.json --record ./decisions.jsonl
BRIEF OK decision=brief backend=fixed cards=1 asked=1 existing=0 failed=0
BRIEF CARD id=greet op=greet@brief-825042ac p_converges=0.72 minutes=under-10 failed=- uncalibrated=true recorded=new

$ nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"
OUTCOME OK id=card-1 decision=read label=ok changed=true

$ nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok
CALIBRATE OK decision=read schema=505bd379c3753631 question=defect option=yes positives=3 negatives=5 skipped=0 auc=0.933
CALIBRATE BAR at=0.5 caught=2 of=3 bounced=1 of_negatives=5
CALIBRATE BAR at=0.7 caught=2 of=3 bounced=0 of_negatives=5
CALIBRATE BAR at=0.9 caught=0 of=3 bounced=0 of_negatives=5
CALIBRATE CATCH-ALL at=0.45 caught=3 of=3 bounced=1 of_negatives=5

$ nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --since 2026-10-01
FINDINGS OK scored=5 classes=4 bar=0.5 since=2026-10-01T00:00:00Z
FINDINGS FINDING class=stranded_fragment count=2 cards=s1-1,s1-2
FINDINGS FINDING class=cut_citation count=2 cards=s1-1,s1-3
FINDINGS FINDING class=invented_reason count=1 cards=s1-2
FINDINGS FINDING class=unnamed count=1 cards=s1-4
```

The ask, read, score, attempt, grade, gate, brief and outcome lines write
`./decisions.jsonl`; calibrate and findings read the fixture record, because a
calibration wants positives and negatives both and findings wants scores to
cluster. The fixed backend answers every failure of the gate alike, and with no
`--bars` (the sprint row's default) every failure is recorded with its class and
routed caused, the take as reported; `--bars 0.8,0.8` routes both flaky. With
`--backend jev` the same `ask`, `read`, `score`, `attempt`, `grade`, `gate` and
`brief` lines ask the model instead, under `nova-secrets exec --only
JEV_API_KEY`, and all but the brief's lines carry the tokens spent, each failure
of a gate on its own. The gate decision's calibration records (base run, and
base not run) are `internal/decide/testdata/gate-calibration-*.jsonl`. The
brief's op id ends in the hex of the schema and the card, so a reworded schema
or card changes it and this transcript names the change.

## nova-local

Fixture: `cmd/nova-local/testdata/`: the worker directory (`home/`) and the
placeholder key file (`local.key`). `cmd/nova-local/firstrun_test.go` runs each
`$` line from a checkout root in one sitting against a fake ollama daemon that
has `gemma4:12b` in the shared store of the AI root `/ai`, a box at load 14.2
with 61 GiB free of 128, and a clock the warm-up advances by three seconds; no
engine, network or real time is used. `./gemma.json` is a file in the test's
own directory.

### First run

```text
$ nova-local status
STATUS OK engines=1 answering=1 loaded=0 models=1 mem_used=71940702208 mem_free=65498251264 mem_total=137438953472 wired_cap=unset load1=14.20
STATUS ENGINE name=ollama state=up base=http://127.0.0.1:11434/v1 loaded=0 advertised=1 store=/ai/shared/models/ollama shared=yes

$ nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7
SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:c0e0c3e5b4a1 num_ctx=32768 keep_alive=30m temperature=0 seed=7 created=yes load=3s mem_used=71940702208 mem_free=65498251264 mem_total=137438953472 wired_cap=unset load1=14.20 engines=1 store=/ai/shared/models/ollama shared=yes

$ nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
WORKER OK engine=ollama model=gemma4-32k out=./gemma.json workers=1 provider=ollama harness=opencode deadline=20m base=http://127.0.0.1:11434/v1
WORKER NOTE run it one worker at a time (--workers 1): an engine is a queue, and two workers interleave it
```

## nova-redis

No fixture and no instance: the lines below are refusals `spill` and
`fn load` make BEFORE they dial anything, so they read the same on every bench.
`cmd/nova-redis/firstrun_test.go` runs each `$` line and compares the output.
A write with no owner, or with no TTL, is refused and nothing is stored; the
round trip against an instance (`spill`, `recall`, and `recall` refusing an
expired key under a controlled clock) is in `cmd/nova-redis/spill_test.go`
over a miniredis fake. `fn load` and `fn check` on a store are in
`cmd/nova-redis/fn_test.go` over a fake and, against a throwaway
redis-server, in `cmd/nova-redis/fn_functional_test.go`.

### First run

```text
$ nova-redis spill --addr 127.0.0.1:6379 --name note --ttl 10m --value hi
SPILL REFUSED: --owner is required and may not be empty or hold ':' or whitespace; every key carries an owner prefix; run: nova-redis help

$ nova-redis spill --addr 127.0.0.1:6379 --owner ada --name note --ttl 0s --value hi
SPILL REFUSED: --ttl is required and must be above zero; an unbounded key is a bug; run: nova-redis help

$ nova-redis fn load
FN-LOAD REFUSED: --addr is required: the store's address as <host:port>, such as 127.0.0.1:6379 (no default); refusing to guess; run: nova-redis help
```

Each refusal names every problem with the line, one line each, and the
help to read next. `spill --dry-run` with a good line needs no store either:
it prints `SPILL OK key=<k> ttl=<d> expires=<t> bytes=<n> store=<a>
written=0 dry_run=true` and dials nothing (the banner's `example:` block
runs it, `cmd/nova-redis/examples_test.go`).

## nova-ci

Fixture: `cmd/nova-ci/testdata/example-events.jsonl`, built into the binary:
`--example` reads it in place of stdin, so the lines below run from the binary
alone, and they are the usage banner's `example:` block line for line. The
transcript was produced by running the built binary, not written by hand.

### First run

```text
$ nova-ci slowtests --example --budget 60 --load 4 --cpus 16
CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=TestSlowThing:63.4s,TestAlsoSlow:1.5s
CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict

$ nova-ci slowtests --example --budget 120 --load 4 --cpus 16
CI-SLOW OK packages=2 slowest=github.com/mas-bandwidth/nova-tools/internal/example:65.1s
CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict
```

The input is the newline-delimited `go test -json` stream the test step already
produces. Each package's total is its package-level `Elapsed`, and the
`slowest=` list names the few test-level rows that spent it, so the first run
tells the reader whether one test or the whole package is the cost. `--budget`
is whole seconds and defaults to 60. Both runs exit 0: a CI-SLOW line is a
measurement, and only `--enforce` makes it exit 2. The
CI-LOAD line is the host's load average, printed and never judged; `--load` and
`--cpus` hand it in here so the transcript is the same on every machine. On
your own module the events come on stdin: `go test -json <packages> |
nova-ci slowtests --budget 60`. The common mistake is forgetting the pipe: with a
terminal on stdin the verb refuses at once and names both ways in; with an
empty stream it reads zero packages and prints `CI-SLOW OK packages=0
slowest=none`, which is why the test step always tees the stream first
(`.github/workflows/ci.yml`).


## nova-table

Run by `cmd/nova-table/firstrun_test.go` on a throwaway redis-server holding
the nova_sprint function library (`cell move` is one call of `ns_oset_move`),
so it runs in the functional tier. The documented lines name no `--redis`: on
a bench the seat's address is the default (`NOVA_SPRINT_REDIS`, then
`NOVA_REDIS_ADDR`, then the seat's row), and the test appends the throwaway
server's. Every value below reproduces; nothing is normalised. The usage
banner's `example:` block is this same sitting, line for line.

### First run

```text
$ nova-table create demo --columns ready,working,done
TABLE CREATE table=demo columns=3 trips=1

$ nova-table row add demo build
TABLE ROW ADD table=demo row=build cols=3 bound=0 trips=1

$ nova-table cell add demo build ready b1
TABLE CELL table=demo row=build col=ready n=1 trips=1

$ nova-table cell add demo build ready b2
TABLE CELL table=demo row=build col=ready n=2 trips=1

$ nova-table cell move demo build ready working b1
TABLE MOVE table=demo row=build member=b1 from=ready to=working n=1 trips=1

$ nova-table show demo
TABLE table=demo columns=3 rows=1 trips=1 epoch=0 revision=5
TABLE ROW table=demo row=build ready=1 working=1 done=0

$ nova-table render demo
demo  | ready | working | done
------+-------+---------+-----
build |     1 |       1 |    0
------+-------+---------+-----
      |     1 |       1 |    0
```

## nova-sprint

The first run needs no Redis. `--redis mem:<file>` loads an in-memory twin
from a file and saves it after each command. The twin is for learning and
tests; commands run one at a time. This transcript follows the card flow in
`nova-sprint help`, with `NOVA_SPRINT_REDIS=mem:sprint.twin` and
`NOVA_SPRINT_ACTOR=boss` set. It uses `finish` without `--head` and `merge`
to record a landing without git. The help's final `tick` moves the card to
landed, and `where` shows the sprint. Those two commands are omitted here
because they print clock-dependent times; `cmd/nova-sprint/twin_test.go`
runs them.

A card's move is queued until the next tick prints `MOVED drain`. A member
that comes up in one tick receives cards in the next.
`cmd/nova-sprint/firstrun_test.go` runs this transcript in the unit tier over
a twin file in a temporary directory. The twin counts its operation ids
(`t1`, `t2`), so every value reproduces without normalization. The functional
tests beside it (`cmd/nova-sprint/*_functional_test.go`) run against a real
store.

### First run

```text
$ nova-sprint init --readers reader-a,reader-b --members m1
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b
MOVED m1 added, down until it beats
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-t1-1
STOPPED
NOTE a twin beats every member at every verb: each member added is up after the next nova-sprint tick

$ nova-sprint add --stream s1 --count 1 --one
MOVED s1-1 -> ready stream=s1 score=1
ADD OK stream=s1 cards=1 before=- moved=1 refused=0 notes=0 op=add-t2-1
NOTE the cards have no brief, so a worker is handed no task with them; give each one before it is dealt, on a STOPPED machine: nova-sprint brief <id> --brief-file <path>
STOPPED  0/1 0.0%

$ nova-sprint start
START OK before=STOPPED after=RUNNING changed
nothing is ticking between commands in a twin: tick by hand: nova-sprint tick
0/1 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED presence: m1 up
MOVED presence: status seen: m1 up
TABLES rows changed: work=0 readers=0 merge=0 fleet=1
TICK OK state=RUNNING idle=no moved=2 notes=2
0/1 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED deal: s1-1 work ready -> working card=s1-1.w1 member=m1 (fleet ready)
TABLES rows changed: work=1 readers=0 merge=0 fleet=1
TICK OK state=RUNNING idle=no moved=1 notes=1
0/1 0.0% -> ETA -  machine: running

$ nova-sprint take --as m1 --epoch 0
MOVED s1-1.w1 fleet ready -> working member=m1 gen=1
PACKET s1-1.w1 attempt=1 gen=1 epoch=0
  tier: flash
  branch: sprint/s1-1.w1.g1.e0
  base: the stream's base
  notes: none
  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE OK moved=1 refused=0 notes=0 op=take-t27-1
0/1 0.0% -> ETA -  machine: running

$ nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report done
MOVED s1-1.w1 working -> done ok; s1-1 working -> review
FINISH OK moved=1 refused=0 notes=1 op=finish-t28-1
0/1 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED drain: s1-1.w1 working -> done ok; s1-1 working -> review (finish by m1)
MOVED ask: s1-1 asked of reader-a
TABLES rows changed: work=1 readers=1 merge=0 fleet=0
TICK OK state=RUNNING idle=no moved=2 notes=0
0/1 0.0% -> ETA -  machine: running

$ nova-sprint read --as reader-a --begin --epoch 0
MOVED s1-1.r1.reader-a asked -> reading
READ OK moved=1 refused=0 notes=0 op=read-t31-1
0/1 0.0% -> ETA -  machine: running

$ nova-sprint read --as reader-a --ok --epoch 0
MOVED s1-1.r1.reader-a reading -> ok
READ OK moved=1 refused=0 notes=0 op=read-t32-1
0/1 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED drain: s1-1 asked of reader-a (tick ask by machine); s1-1.r1.reader-a reading -> ok (read by reader-a)
MOVED accept: s1-1 review -> merging queued (ok from reader-a)
TABLES rows changed: work=1 readers=0 merge=1 fleet=0
TICK OK state=RUNNING idle=no moved=2 notes=2
0/1 0.0% -> ETA -  machine: running

$ nova-sprint merge --stream s1 --batch 1
MOVED s1-1 merging -> landed
MERGE OK moved=1 refused=0 notes=2 op=merge-t36-1
0/1 0.0% -> ETA -  machine: running
```

### Answered by nova-decide

The routine judgments answered by the judgment decision
([SPEC-SPRINT.md section 8](SPEC-SPRINT.md#answered-by-nova-decide)): two cards
come back failed in one note, and `answer` asks the decision for each
card. With no `decide_judgment_bar` set (the sprint row ships it empty) and no
`--bar`, it applies nothing: it records each decision and lists what a bar would
apply. Given `--bar 0.8` it applies the recorded decisions, asking nothing again,
and reworks each card by the line the inbox prints for it alone, each line carrying
the decision's op id (`--op decide.<decision id>`), recorded as `applying` before it
runs and `applied` after, so a pass stopped between the two is finished by the next
through the same op and nothing is applied twice. The backend is
the fixed one (`--backend fixed`), answering from
`cmd/nova-sprint/testdata/judgment-answers.json` whatever the state, so no key or
network is needed; with Jev it is `nova-secrets exec --only JEV_API_KEY --
nova-sprint answer`. Run from a checkout root over a fresh twin, with
the first run's environment, by `cmd/nova-sprint/answer_transcript_test.go`,
which keeps the record in a temporary directory and prints it as
`./judgment.jsonl`; nothing else is normalised.

```text
$ nova-sprint init --readers reader-a,reader-b --members m1
INIT OK tables=work,readers,merge,fleet view=sprint readers=reader-a,reader-b
MOVED m1 added, down until it beats
FLEET-UP OK moved=1 refused=0 notes=0 op=fleet-release-t1-1
STOPPED
NOTE a twin beats every member at every verb: each member added is up after the next nova-sprint tick

$ nova-sprint add --stream s1 --count 2
MOVED s1-1 -> ready stream=s1 score=1
MOVED s1-2 -> ready stream=s1 score=2
ADD OK stream=s1 cards=2 before=- moved=2 refused=0 notes=0 op=add-t2-1
NOTE the cards have no brief, so a worker is handed no task with them; give each one before it is dealt, on a STOPPED machine: nova-sprint brief <id> --brief-file <path>
STOPPED  0/2 0.0%

$ nova-sprint start
START OK before=STOPPED after=RUNNING changed
nothing is ticking between commands in a twin: tick by hand: nova-sprint tick
0/2 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED presence: m1 up
MOVED presence: status seen: m1 up
TABLES rows changed: work=0 readers=0 merge=0 fleet=1
TICK OK state=RUNNING idle=no moved=2 notes=2
0/2 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED deal: s1-1 work ready -> working card=s1-1.w1 member=m1 (fleet ready)
MOVED deal: s1-2 work ready -> working card=s1-2.w1 member=m1 (fleet ready)
TABLES rows changed: work=1 readers=0 merge=0 fleet=1
TICK OK state=RUNNING idle=no moved=2 notes=1
0/2 0.0% -> ETA -  machine: running

$ nova-sprint take --as m1 --max 2 --epoch 0
MOVED s1-1.w1 fleet ready -> working member=m1 gen=1
MOVED s1-2.w1 fleet ready -> working member=m1 gen=1
PACKET s1-1.w1 attempt=1 gen=1 epoch=0
  tier: flash
  branch: sprint/s1-1.w1.g1.e0
  base: the stream's base
  notes: none
  report it: nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --branch sprint/s1-1.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
PACKET s1-2.w1 attempt=1 gen=1 epoch=0
  tier: flash
  branch: sprint/s1-2.w1.g1.e0
  base: the stream's base
  notes: none
  report it: nova-sprint finish --as m1 s1-2.w1@1 --epoch 0 --branch sprint/s1-2.w1.g1.e0 --head <commit> --report '<what you did>' [--failed]
TAKE OK moved=2 refused=0 notes=0 op=take-t27-1
0/2 0.0% -> ETA -  machine: running

$ nova-sprint finish --as m1 s1-1.w1@1 s1-2.w1@1 --epoch 0 --failed --report 'the tests went red'
MOVED s1-1.w1 working -> done failed; s1-1 working -> review
MOVED s1-2.w1 working -> done failed; s1-2 working -> review
FINISH OK moved=2 refused=0 notes=1 op=finish-t28-1
0/2 0.0% -> ETA -  machine: running

$ nova-sprint tick
MOVED drain: s1-1.w1 working -> done failed; s1-1 working -> review (finish by m1)
MOVED drain: s1-2.w1 working -> done failed; s1-2 working -> review (finish by m1)
TABLES rows changed: work=1 readers=0 merge=0 fleet=0
TICK OK state=RUNNING idle=no moved=2 notes=0
0/2 0.0% -> ETA -  machine: running

$ nova-sprint answer --backend fixed --answers ./cmd/nova-sprint/testdata/judgment-answers.json --record ./judgment.jsonl
judgment        card  kind    verb    p     act     why
finish-t28-1.1  s1-1  failed  rework  0.91  listed  no decide_judgment_bar is set, so nothing is applied; at a bar at or under 0.91 it would apply: nova-sprint rework s1-1 --one
finish-t28-1.1  s1-2  failed  rework  0.91  listed  no decide_judgment_bar is set, so nothing is applied; at a bar at or under 0.91 it would apply: nova-sprint rework s1-2 --one
ANSWER OK rows=2 applied=0 would_apply=0 listed=2 refused=0 failed=0 left=0 outcomes=0 bar=- record=./judgment.jsonl; run: nova-sprint inbox

$ nova-sprint answer --bar 0.8 --backend fixed --answers ./cmd/nova-sprint/testdata/judgment-answers.json --record ./judgment.jsonl
judgment        card  kind    verb    p     act      why
finish-t28-1.1  s1-1  failed  rework  0.91  applied  nova-sprint rework s1-1 --one --op decide.finish-t28-1.1_s1-1
finish-t28-1.1  s1-2  failed  rework  0.91  applied  nova-sprint rework s1-2 --one --op decide.finish-t28-1.1_s1-2
ANSWER OK rows=2 applied=2 would_apply=0 listed=0 refused=0 failed=0 left=0 outcomes=0 bar=0.80 record=./judgment.jsonl; run: nova-sprint inbox
```

## nova-work

Run by `cmd/nova-work/firstrun_test.go` against a recorded conversation with
GitHub (`internal/workgh/testdata/reliable`: one public repository of twenty
issues, read at fifteen a page), so no network is used and no process is
started. `$ORG` and `$REPO` are yours: the test stands them for the recording's
organization and repository, and the counts below are that repository's. `gh=`
names the gh a run used; yours is the gh on your PATH, and here it is `./gh`,
where the test says it found one, while every call is answered from the
recording. The `sha256` is the tree file's, and the tree records the instant it
was fetched, so it differs on every real run; the test passes a fixed time in
so the value below reproduces. `./tree.lisp` is a file in a directory of the
test's own. The usage banner's `example:` block is this same sitting, line for
line.

Requires: a gh login that can read the repository (`gh auth status`); import
(the dry run too) and verify read GitHub through gh and write nothing there.

### First run

```text
$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --dry-run
IMPORT OK org=$ORG out=- repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 calls=3 points=3 rest=0 seconds=0.0 gh=./gh dry_run=true
IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15
IMPORT REPO repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2
IMPORT NOTE the dry run read GitHub as the import does (calls=3, read-only) and wrote nothing

$ nova-work import --org $ORG --repo $ORG/$REPO --page-size 15 --out ./tree.lisp
IMPORT OK org=$ORG out=./tree.lisp repos=1 issues=20 comments=74 references=4 linked_prs=2 bytes=65206 sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 calls=3 points=3 rest=0 seconds=0.0 gh=./gh
IMPORT PLAN repos=1 issues=20 est_calls=3 max_calls=1500 page_size=15
IMPORT REPO repo=$ORG/$REPO issues=20 comments=74 references=4 linked_prs=2 calls=2

$ nova-work verify --tree ./tree.lisp --repo $ORG/$REPO --page-size 15
VERIFY OK tree=./tree.lisp sha256=492ee7e0aaeb987b3c8935a01dd5194bb8826f9742d20575d24d090d04fb4a71 repos=1 issues=20 comments=74 calls=3 points=3 rest=0 seconds=0.0 differences=0 missing=0 extra=0 drift=0 gh=./gh
```

The dry run is not offline: it reads GitHub exactly as the import does (every
issue, read-only, the same calls) and writes nothing, and its last line says so.
`est_calls` is the calls the import will spend, checked against `max_calls`
before any issue is read, and `out=-` says no file was written. The import
prints the same plan, one `IMPORT REPO` per repository, and the `sha256` of the
file it wrote; verify names the same `sha256`, and when the two differ it says
`VERIFY FAILED` with one `VERIFY MISSING`, `EXTRA` or `DRIFT` line per difference.
`differences=0` is the proof the tree holds what GitHub holds.

## nova-card

Fixture: `cmd/nova-card/testdata/findings.tsv`, a reader's findings on two
files, typed as `./cmd/nova-card/testdata/findings.tsv` from the root of a
checkout; `./cards` is a directory the first line creates.

### First run

```
$ nova-card generate --from findings --file ./cmd/nova-card/testdata/findings.tsv --repo example/repo --base dev --sha 0123456789abcdef0123456789abcdef01234567 --out ./cards
CARDS OK dir=./cards cards=2 waves=1 tier=pro

$ nova-card lint --card ./cards/finding-internal-bus-send.md
LINT OK file=./cards/finding-internal-bus-send.md

$ nova-card lint --card ./cards/finding-cmd-nova-bus-main.md
LINT OK file=./cards/finding-cmd-nova-bus-main.md
```

## nova-up

A linux login, typing from its home `/home/you`, with every program the setup
runs on its PATH: the first run is the dry run, which plans every step on the
machine as it is, prints one `UP <step>` line each, and writes nothing. Run by
`cmd/nova-up/firstrun_test.go` over a machine whose programs answer only their
version, so no step can apply.

### First run

```
$ nova-up --local --dry-run --root ./nova-try
UP OK root=/home/you/nova-try steps=9 changes=7 applied=0 dry_run=true
UP NOTE dry run: nothing written; run it without --dry-run to apply
UP platform ok linux: loops are systemd user units
UP dirs create /home/you/nova-try: . stores keys logs smoke
UP binaries ok 8 on PATH, each answering its version
UP sprint create mem:/home/you/nova-try/stores/sprint.twin
UP secrets create /home/you/nova-try/secrets seat=coordinator
UP ssh create known_hosts missing; run `ssh-keyscan <bench> >> ~/.ssh/known_hosts` for each bench
UP redis create 127.0.0.1:6390 loop=redis-local for nova-bus: unit create, passwords to seal=4, acl apply, fn load
UP seat create /home/you/nova-try/seat.env seat=coordinator
UP smoke create one card on /home/you/nova-try/smoke/sprint.twin beside the throwaway repository /home/you/nova-try/smoke/repo

$ nova-up --dry-run --root ./nova-try
! UP REFUSED: --local is required; it wants nothing after it: the mode that sets up this one machine with no config; run: nova-up help
```

## nova-doctor

A login with no friend rows, `/home/you`: the harness check alone, as lines and
as JSON, then a check name that is none. Each run reads and changes nothing. Run
by `cmd/nova-doctor/firstrun_test.go`.

### First run

```
$ nova-doctor run --check harness
DOCTOR harness ok no friend rows at /home/you/.nova/friends

$ nova-doctor run --check harness --json
{"exit":0,"results":[{"check":"harness","dependency":"the friend harnesses","status":"ok","evidence":"no friend rows at /home/you/.nova/friends"}]}

$ nova-doctor run --check nosuch
! RUN REFUSED: no check named "nosuch"; the checks are dashboard, gosdk, harness, providers, secrets, self, ssh, tailnet; run: nova-doctor help
```

## One-shot lanes at parity (internal/friend/lane_parity_test.go, cmd/nova-friend/lane_parity_test.go)

`TestOpencodeLanesDoWhatTheRunnerStopgapsDid` has one subtest per behaviour the two runner scripts had (card filter, row
rules, take back, generation job names, width under load, token cap, provider stop, cost line, tokens from opencode's
database, route row, go shims, bus note); `TestLanesTakeBackACardOutsideTheRowsTiersAndRunOnlyTheRest`,
`TestLanesAreHeldToTheLoadWidthWhileTheLoadIsHigh`, `TestACardOverTheTokenCapIsHeldAndItsCostPublished`,
`TestAFinishedCardPublishesItsCostOrWhyNot`, `TestAProviderFailureHoldsTheFriendDownUntilAPersonClearsIt`,
`TestAProviderFailureStopsEveryLaneUnderWayAndKeepsItsCard` and `TestAPauseMarkerNotWrittenIsNotAResume` run them through
the lane rig on its fake clock (synctest, no socket, no wall-clock sleep), the take-back's fake server refusing a
coordinator verb as the real one does; `TestResumeClearsTheLanesPauseAPersonBringsUp` and
`TestRefuseGoRefusesWithTheWayToABench` run the two new verbs, and `TestRunBeatsDownWhileTheLanesArePausedUntilAPersonResumes`
the daemon's down beat while the pause stands.
