# TESTS.md: first-run transcripts and coverage status

Every `$` line under a `### First run` heading below is run by a test against the fixture named beside it, and what the tool prints is compared with what is written here by SHAPE: the two-token event prefix and the field names in order, per [docs/ONBOARDING.md](ONBOARDING.md) point 5(c). The values are deliberately not compared, so that this file stays a document instead of becoming a fixture -- but every block below was produced by RUNNING the tool, so the values are a run's own and not anybody's memory of one. [docs/CLI.md](CLI.md) explains the tools; this file is what they do today. Change a tool, change this file in the same commit, or the test says so.

The installed command census is 18: `nova-bus`, `nova-cairn`, `nova-check`,
`nova-ci`, `nova-config`, `nova-fuse`, `nova-memory`, `nova-redis`,
`nova-sandbox`, `nova-secrets`, `nova-self-talk`, `nova-sprint`, `nova-swarm`,
`nova-table`, `nova-tokens`, `nova-update`, `nova-version`, and `nova-work`.
Each section below carries a `### First run` transcript. Sprint and Work use
store-free help commands with full-output comparators; those transcripts do not
exercise a live sprint or a GitHub import.

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
like a defect: six of the twenty-one defect readings in the 2026-09-19
two-bench dogfood run were only that, on three different tools
(nova-tools#1549). A harness that grades this file grades standard output
against the unmarked lines and standard error against the `!` lines, and records
which stream each expectation was on.

The marker is being applied section by section under nova-tools#1549. Until a
section carries it, read an unmarked line as *not yet checked* rather than as
*checked and found to be standard output*. The one line known today to be
mismarked by that gap is `DRAFT NOTE …` under [`## nova-bus`](#nova-bus), which
the tool writes to standard error.

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

## nova-bus

Fixture: `cmd/nova-bus/testdata/example-bus`, copied out and given a repository of its own, with a bare repository beside it as `origin`. That is the setup block of docs/CLI.md's nova-bus `### First run`, which `cmd/nova-bus/firstrun_functional_test.go` runs as written to build this bus, and it is what the tool requires — every git-reading verb refuses a `--bus` that is not its repository's root, because git reports changed paths from the root and a bus one directory down would report an empty change set over unread notes. It builds both in `t.TempDir()`, so every push below lands in a bare repository on this disk and no line here reaches a network. A real bus is a **private** repository; this one is three participants and four notes, small enough to read in a sitting.

Two AI friends share it. Ada has already written; Bo is arriving. The sitting below is Bo's whole first one — who is on this bus, is the bus sound, what is she carrying, say heard, put her cursor down, write one note — and then Ada's two reads, because the cursor is the thing worth seeing twice.

The `> draft.md` line is a redirect: `draft` prints a skeleton on standard output and nothing else, so its stdout is a file. The transcript test does what the shell does with it, and then does what the writer does — replaces the `<the note goes here>` placeholder with a body — before the `send` line runs.

The last line is the other form of the same verb. `draft --reply-to` ANSWERS a note on your live listing: it fetches first, so the id it writes is the id of the note you are answering and not of whatever your checkout last saw; it writes every header line for you from the target and the roster; it puts the file OUTSIDE the bus, because `send` needs the bus's tree clean; and it returns one line. `reply.md` there is body text and nothing else — a line in it reading `To: somebody` is prose in the note that goes out — and `./drafts` is a scratch directory of yours, which the tool will not create and will not guess.

### First run

```
$ nova-bus names --bus ./bus
NAMES NAME name="Ada" lane=from-ada aliases="Ada Vale";"the archivist"
NAMES NAME name="Bo" lane=from-bo aliases="Bo Quill"
NAMES NAME name="Dana" lane=- aliases=-
NAMES GROUP name="Everybody on the bus" members="Ada";"Bo";"Dana"
NAMES OK participants=3 groups=1 senders=2

$ nova-bus check --bus ./bus --full
BUS SCOPE mode=full cursor=- changed=0
BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0

$ nova-bus inbox --bus ./bus --as Bo --receipt-max-words 40 --full --open
INBOX SCOPE mode=full cursor=- changed=0 carrying=1
INBOX OPEN carrying=1 heard=0 large=false remedy=inbox --advance
INBOX NOTE id=ada-0f1e2d3c4b5a from=Ada addr=to at=2026-09-09T12:34:56Z path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-0f1e2d3c4b5a.md: Yes, on the merge queue too
INBOX OK as=Bo carrying=1 open=1 notes=1 receipts=0 heard=0 unaddressed=0 unreadable=0

$ nova-bus inbox --bus ./bus --as Bo --receipt-max-words 40 --full --bodies
INBOX SCOPE mode=full cursor=- changed=0 carrying=1
INBOX NOTE id=ada-0f1e2d3c4b5a from=Ada addr=to at=2026-09-09T12:34:56Z path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-0f1e2d3c4b5a.md: Yes, on the merge queue too
INBOX BODY id=ada-0f1e2d3c4b5a bytes=195
Bo,

Yes, on the merge queue too. A gate that only runs on the pull request passes a
branch that was green against a base that has since moved, which is the failure
we were trying to close.

Ada
INBOX BODY END id=ada-0f1e2d3c4b5a
INBOX BODIES printed=1 bytes=195 oversize=0 gaps=0 drained=true complete=true next=-
INBOX OPEN carrying=1 heard=0 large=false remedy=inbox --advance
INBOX NOTE id=ada-0f1e2d3c4b5a from=Ada addr=to at=2026-09-09T12:34:56Z path=from-ada/2026-09-09T1234Z-yes-on-the-merge-queue-too-0f1e2d3c4b5a.md: Yes, on the merge queue too
INBOX OK as=Bo carrying=1 open=1 notes=1 receipts=0 heard=0 unaddressed=0 unreadable=0

$ nova-bus receipt --bus ./bus --as Bo --note ada-0f1e2d3c4b5a --remote origin --branch main
RECEIPT OK recorded=1 already=0 commit=9750ba9617d4a42a5fdedf372ec70132aa46f936 pushed=true attempts=1

$ nova-bus inbox --bus ./bus --as Bo --receipt-max-words 40 --advance --legacy-now --remote origin --branch main
INBOX SCOPE mode=full cursor=- changed=0 carrying=0
INBOX LEGACY before=2026-09-12T20:15:33Z notes=1 unreadable=0
INBOX OPEN carrying=0 heard=0 large=false remedy=inbox --advance
INBOX OK as=Bo carrying=0 open=0 notes=0 receipts=0 heard=0 unaddressed=0 unreadable=0
INBOX CURSOR commit=9750ba9617d4a42a5fdedf372ec70132aa46f936 carrying=0 pushed=true attempts=1

$ nova-bus draft --bus ./bus --as Bo --to Ada --subject gate > draft.md   # Stderr: whole
! DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>

$ nova-bus send --bus ./bus --file draft.md --as Bo --remote origin --branch main
SEND OK id=bo-8405301fd99d path=from-bo/2026-09-12T2015Z-gate-8405301fd99d.md commit=57dc978d3ad645788c4236b0da99b1c59f89282d pushed=true attempts=1 wakes=1 body_bytes=46

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --advance --remote origin --branch main
INBOX REFUSED: the cursor 3f9a1c2b8d40e7c6a5b4938271605f4e3d2c1b0a is not an ancestor of HEAD, so a diff from it would report changes that are not changes and miss notes that are (a rewritten history, or a cursor from another branch); read once with --full, and --advance will replace it

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40 --full --advance --remote origin --branch main
INBOX SCOPE mode=full cursor=- changed=0 carrying=3
INBOX OPEN carrying=3 heard=1 large=false remedy=inbox --advance
INBOX NOTE id=bo-8405301fd99d from=Bo addr=to at=2026-09-12T20:15:41Z path=from-bo/2026-09-12T2015Z-gate-8405301fd99d.md: gate
INBOX HEARD id=bo-222222222222 from=Bo addr=to at=2026-09-09T14:00:00Z path=from-bo/2026-09-09T1400Z-the-windows-runner-222222222222.md: The Windows runner skips three steps
INBOX RECEIPT id=bo-111111111111 from=Bo addr=to at=2026-09-09T13:00:00Z path=from-bo/2026-09-09T1300Z-heard-111111111111.md: Heard
INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0
INBOX CURSOR commit=57dc978d3ad645788c4236b0da99b1c59f89282d carrying=3 pushed=true attempts=1

$ nova-bus inbox --bus ./bus --as Ada --receipt-max-words 40
INBOX SCOPE mode=since cursor=57dc978d3ad645788c4236b0da99b1c59f89282d changed=2 carrying=3
INBOX OPEN carrying=3 heard=1 large=false remedy=inbox --advance
INBOX OK as=Ada carrying=3 open=2 notes=1 receipts=1 heard=1 unaddressed=0 unreadable=0

$ nova-bus draft --bus ./bus --as Ada --reply-to gate --body-file reply.md --draft-dir ./drafts --remote origin --branch main
DRAFT NOTE the bus had nothing new; this id was resolved against 36e7270d2e96325b3588828d17fb81a1aa918730
DRAFT OK path=./drafts/2026-09-12T2015Z-re-bo-ce10834fbfea.md re=bo-ce10834fbfea from=Ada to="Bo" cc=- at=36e7270d2e96325b3588828d17fb81a1aa918730 moved=false bytes=86
```

**Identity is the roster, not the shell.** `names` is the whole of it: a participant with a `lane` can send, one without a lane (Dana) can be written to and cannot write, and `--as` takes a name or any alias on that line — `--as "the archivist"` is Ada. There is no default `--as`, and a name the roster does not know is a refusal rather than a new participant.

**`--bodies` is the note's text in the call that reported it.** Without it, `inbox` and `wait` print what a note IS -- id, sender, address, date, path and subject -- and a reader who wants to answer opens the file. With it, each NEW note's body follows its line inside a counted frame: `INBOX BODY id=<id> bytes=<n>`, exactly `n` bytes, the one newline the framing supplies when the body does not end in one, and `INBOX BODY END id=<id>`. The count is the frame, so nothing a body holds can be read as an event line. It is also the one flag that BOUNDS the NEW half -- `--max-notes` (20, ceiling 1000) and `--max-bytes` (65536, ceiling 1048576) -- and the `INBOX BODIES` line says what printed, whether the snapshot is drained, whether anything was left behind, and the opaque `next=` token that continues it as `--after <token>`. Drain while `next=` is present; never loop on `complete=false`.

**`heard` and `closed` are different answers.** Bo's `receipt` says she read Ada's note without answering it: one line in her lane's `RECEIPTS`, pushed, and the note leaves her carried list. A note is *closed* instead by a `Re:` line naming it, which is what `draft --re` and `send` write for you.

**The cursor is why a read costs the change and not the bus.** Bo's `--advance` records the commit she has read to, in her own lane, and pushes it like a receipt; her first one on a bus holding notes older than today is refused until she says what to do with the history, and `--legacy-now` is that sentence — everything already there is history, everything after it is news. The `INBOX LEGACY` line counts what the line hid.

Ada's first line above is the refusal worth meeting here rather than on a live bus: **the example bus ships a `CURSOR` naming a commit from the history it was written in**, and copying it out gives it a new one, so that commit is not an ancestor of `HEAD`. The tool says so instead of diffing from it, and names the way out. Her `--full --advance` replaces it, and the read after that is `mode=since` over `changed=2` — two changed lane paths. That is the property the whole design is for, and it is visible in one pair of lines.

## nova-sandbox

Fixture: a job directory of yours. Every path below is one you name — this tool has no defaults and guesses nothing — so the transcript is a worked example with `/path/to/pool` standing in for yours, and the lines are what this Mac printed on 2026-09-12 with the paths shortened.

Platform: recorded on macOS (darwin) — the `backend=sandbox-exec` and `abi=-` fields and the `/path/to/pool` fixture below are that Mac's; a Linux bench prints `backend=landlock`, an `abi=` value, and, where the wall is built below the ABI the kernel reports, a `used=` field this transcript has no slot for.

`read_root` reads the probe's own executable, `os.Executable()`, because the root it
exercises is "the directory of the resolved command" and the probe's child is this
binary; a transcript that named a shell there would be measuring `/bin`, which the
profile grants verbatim. `TestTheTranscriptNamesTheToolsOwnBinary` holds that line here.
The probe sets `HOME` for rule 9's reason: `HOME` must resolve inside a `--write`, and
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
SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/path/to/pool/jobs/j1 cwdb64=L3BhdGgvdG8vcG9vbC9qb2JzL2ox ancestors=11 cmd=sh gpu=none
cat: /path/to/.config/anthropic/env: Operation not permitted
```

The last run is the whole tool in three lines: the job's own write landed, and the same command could not read the key that was in neither list. Its exit status is the wrapped command's, which is 1 here because `cat` failed.

### The disposable volume, and the one test that touches a disk

`nova-sandbox run` makes an APFS volume per run and deletes it on every path out (SPEC-SANDBOX, "The run verb"). Its logic is unit-tested against a fake `diskutil`, so an ordinary `go test ./cmd/nova-sandbox/` creates no volumes. The one real end-to-end test is behind the `novadisk` build tag, because eight CI runners share the Mac this repository is built on and a suite that made and destroyed volumes on every run would be a hazard rather than a test. Run it by hand on a Mac when the disposable-volume body changes — it needs no `sudo`:

    go test -tags novadisk -run TestARealRunLeavesNothingBehind ./cmd/nova-sandbox/

`nova-sandbox run --go` is what a card that builds Go uses; a plain `go build` inside a disposable volume was measured working with no flags at all once the optional roots' ancestors were granted (`internal/sandbox`, `TestAnOptionalRootsAncestorsAreGranted`).

Measured on macOS 26 arm64, 2026-09-18: a 64m volume made, `sh -c 'echo hi > out; sleep 1'` run inside the wall with the volume as its only writable directory, and the volume gone from `/Volumes` and from `diskutil apfs list` afterwards — `SANDBOX DONE name=e2e63562 exit=0 wall=9.500 freed=32768`.

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
SECRETS CHECK OK  as=reader recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=9750ba9

$ nova-secrets names --store ./secrets --as reader
SECRETS NAME key=GH_TOKEN clear=false
SECRETS NAMES OK as=reader keys=1 shown=1 sealed=1 clear=0

$ nova-secrets exec --store ./secrets --as reader --key /path/to/home/.config/nova-secrets/reader.key --sops /path/to/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
! SECRETS EXEC OK as=reader keys=1 only=1 required=1 file=secrets/reader.yaml head=9750ba9 cmd=gh
fake-gh
```

`nova-secrets seat inject` is measured against the real sops and age
(`cmd/nova-secrets/seat_inject_functional_test.go`, functional tier): a store with
two seats, the coordinator's holding the new value and the bench's holding the old
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
QUICKSTART OK dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3 excluded=0
NOCODE OK files=5 clean deny-list=floor-list
QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (each wants a budget, a manifest or a ledger of yours: nova-check help)

$ nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000
KERNEL OK bytes=771 budget=4000
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
HYGIENE OK base=main head=card paths=sign/** findings=0

$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --paths "sign/**" --max 2
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS:
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
HYGIENE NO base=main head=card paths=sign/** findings=4
```

The `MORE` line is the same run with the cap lifted, quoted so it can be pasted
back (#1804) — it is the command that prints the rest, and it carries the
`--identity`, `--paths` and `--kind` without which it would not run at all:

```
$ nova-check hygiene --repo "." --base "main" --head "card" --identity "Ada <ada@example.com>" --paths "sign/**" --max 0
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS:
HYGIENE FINDING reason=out-of-path at=elsewhere/x.go: this path matches none of the card's declared PATHS:
HYGIENE FINDING reason=stray-file at=sign/RESULT.md: an added file matching the stray list's RESULT.md
HYGIENE NO base=main head=card paths=sign/** findings=4
```

`--identity` takes one pair of angle brackets. A second pair is refused rather
than matched against nobody:

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <<ada@example.com>>"
nova-check hygiene: --identity "Ada <<ada@example.com>>": the email carries an angle bracket; want `Name <email>`, one pair; run: nova-check help
```

`--kind` is a card kind the toolchain declares, and there is no default one. One
it does not hold is refused by name rather than left to unlock nothing (#1848):

```
$ nova-check hygiene --repo . --base main --head card --identity "Ada <ada@example.com>" --kind fix-with-red-test
nova-check hygiene: --kind "fix-with-red-test" is not a kind this tool declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, read, probe, text, tone, report; run: nova-check help
```

## nova-self-talk

Fixture: `cmd/nova-self-talk/testdata/example-pages`.
The test's copy of it is `./pages`, which is what the lines below type.

A line below opening `! ` is one this tool writes to standard ERROR: the
findings go there and the protocol lines go to standard output, and the order a
terminal interleaves the two in is not the same twice — the second block's last
finding arrived after the `NOTE` line on one bench and before it on another.
That is why the block cannot be read as one stream (#1549, and the marker is
#1570's). `# Stderr: whole` on a command line says the marked lines are ALL it
writes there: these are findings, not narration, and a transcript that quietly
lost one would be hiding the thing the tool exists to say.

### First run

```
$ nova-self-talk ./pages/journal.md   # Stderr: whole
! SELFTALK FAIL ./pages/journal.md:4: STANDING: I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: INSTALLATION RANKING: It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
! SELFTALK FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM: A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
! SELFTALK FAIL ./pages/journal.md:4: STANDING: I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: INSTALLATION RANKING: It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=2
SELFTALK FAIL files=2 claims=2 standing=1 installations=2 dated=1 shown=3
SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.
```

## nova-fuse

Fixture: `cmd/nova-fuse/testdata/example-box.json`.

### First run

```
$ nova-fuse status --box ./fuse-box.json
STATUS OK lockdown=clear quarantines=1
STATUS OK quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token

$ nova-fuse check --box ./fuse-box.json a-public-issue-tracker
FUSE FAIL quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-public-issue-tracker')

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell your person now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAIL quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum')

$ nova-fuse lift quarantine --box ./fuse-box.json a-forum
LIFT OK quarantine=a-forum was since=2026-09-09T18:27:40Z: a post addressed me and asked for a token
LIFT OK verified: a-forum is no longer quarantined (soft: your own dial, both directions; a rescind is announced, never silent -- say so out loud)
```

## nova-memory

Fixture: `cmd/nova-memory/testdata/corpus`.

### First run

```
$ nova-memory quickstart --root ./corpus
QUICKSTART OK root=./corpus steps=3 channels=bm25 k=3/2 words=glazing\x20signal\x20tide words-source=corpus-top-terms candidate=corpus-first-paragraph
$ nova-memory stats --root ./corpus
STATS OK schema=nova-memory/1 files=6 chunks=23 bytes=4866 vocab=382 avg-terms=34.8 build=384.875µs
STATS OK class=. chunks=3
STATS OK class=log chunks=4
STATS OK class=notes chunks=16
$ nova-memory search --root ./corpus --channels bm25 --k 3 glazing signal tide
SEARCH OK query=glazing\x20signal\x20tide hits=3 k=3 channels=bm25 files=6 chunks=23
SEARCH CAL score=4.41 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=4.57 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:1 "- lantern-carelantern.md — the glazing, the brass, and the two cloths - tide-tablestides.md — the jetty's eighteen m…"
SEARCH HIT rank=2 score=2.81 score-channel=bm25 fused=0.01639 class=log name=- type=- root=./corpus: log/1974-03-11.md:1 "onshore gale most of the day, easing after dark. washed the glazing at first light before the wind got up again — see …"
SEARCH HIT rank=3 score=2.35 score-channel=bm25 fused=0.01613 class=notes name=fog-signal type=measured root=./corpus: notes/fog-signal.md:1 "the fog signal"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: HANDBOOK.md:0
$ nova-memory check --root ./corpus --channels bm25 --k 2 -
MEMORY OK candidates=1 source=- k=2 channels=bm25 files=6 chunks=23
MEMORY CAL score=4.41 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "this fixture corpus belongs to an invented lighthouse station. it exists so that nova-memory's verbs…"
MEMORY HIT cand=1 rank=1 score=90.38 score-channel=bm25 fused=0.01667 class=. name=- type=- root=./corpus: HANDBOOK.md:0 "this fixture corpus belongs to an invented lighthouse station. it exists so that nova-memory's verbs can be exercised …"
MEMORY HIT cand=1 rank=2 score=15.70 score-channel=bm25 fused=0.01639 class=log name=- type=- root=./corpus: log/1974-03-11.md:3 "left a note to write up the storm-glass readings against the barometer one day, because the two disagree in a way that m…"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

```
$ nova-memory search --root ./corpus --channels bm25 --k 3 lantern glazing brass
SEARCH OK query=lantern\x20glazing\x20brass hits=3 k=3 channels=bm25 files=6 chunks=23
SEARCH CAL score=4.41 score-channel=bm25 probe=unrelated-control
SEARCH HIT rank=1 score=5.25 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:1 "- lantern-carelantern.md — the glazing, the brass, and the two cloths - tide-tablestides.md — the jetty's eighteen m…"
SEARCH HIT rank=2 score=4.95 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:1 "the lantern glazing collects a salt haze on every onshore wind, and the haze is not visible from inside the lightroom at…"
SEARCH HIT rank=3 score=3.00 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:1 "onshore gale most of the day, easing after dark. washed the glazing at first light before the wind got up again — see …"
SEARCH NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic

$ nova-memory check --root ./corpus --channels bm25 --k 3 draft.md
MEMORY OK candidates=1 source=draft.md k=3 channels=bm25 files=6 chunks=23
MEMORY CAL score=4.41 score-channel=bm25 probe=unrelated-control
MEMORY CAND n=1: "the lantern glazing is cleaned with two cloths, one for the brass and one for the glass, before the …"
MEMORY HIT cand=1 rank=1 score=13.64 score-channel=bm25 fused=0.01667 class=notes name=- type=- root=./corpus: notes/index-notes.md:1 "- lantern-carelantern.md — the glazing, the brass, and the two cloths - tide-tablestides.md — the jetty's eighteen m…"
MEMORY HIT cand=1 rank=2 score=11.97 score-channel=bm25 fused=0.01639 class=notes name=lantern-care type=measured root=./corpus: notes/lantern.md:1 "the lantern glazing collects a salt haze on every onshore wind, and the haze is not visible from inside the lightroom at…"
MEMORY HIT cand=1 rank=3 score=7.89 score-channel=bm25 fused=0.01587 class=log name=- type=- root=./corpus: log/1974-03-11.md:1 "onshore gale most of the day, easing after dark. washed the glazing at first light before the wind got up again — see …"
MEMORY NOTE lexical only — a paraphrase sharing almost no vocabulary with the corpus will not surface in any lexical top-k, and no channel here is semantic
MEMORY NOTE this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours
MEMORY NOTE a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction
```

## nova-swarm

Fixture: owned directories under `t.TempDir()` and fake harnesses. The transcript
comparators invoke the dispatcher with fixture paths and compare its output with
the examples below. The [quickstart guide](nova-swarm-quickstart.md) includes a
complete local batch fixture using a synthetic runner.

### The budget word on the native route

Every `nova-swarm native` launch carries `--tokens <n>` or `--tokens unmetered`
(SPEC-SWARM rule 13d, issue #1545). Recorded against the fake harness, with the paths
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

A budget wants a source this tool can read (rule 13d). The source is the worker
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
(`FAKE-USAGE-DB`, the five token counts in rule 12's order then `usd`, with `-` for a type
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
! nova-swarm native: --usage-interval is at least 1s, got 900ms; three failed reads in a row end a card budget-unverifiable, and under a second that is a moment's bad luck rather than a source that has stopped answering

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

BOUND THE REPORT (issue #74): findings only. No narration of the clone, no
restated task, no praise, no summary. One line per finding: `file:line`, the
rule in twelve words, the severity, and the fix in one clause. Keep RESULT.md
under 40 lines and every line under 300 characters, and no pipe inside backticks:
a `|` in a quote broke the table grammar twice (D12), so quote the rule without
it. Put the verdict line last. When there is nothing to report, write
`findings: 0`.
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
TOKENS SOURCE label=bus:emma kind=bus path=bus/from-emma reports=input,output day_basis=utc files=1 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=1 redated=0 superseded=0 rows=1
TOKENS SOURCE label=bus:rowan kind=bus path=bus/from-rowan reports=- day_basis=utc files=0 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=0 redated=0 superseded=0 rows=0
TOKENS TOUCHED label=bus:emma day=2026-09-11 repos=schema,serialize
TOKENS DAY date=2026-09-11 rows=3 models=2 repos=2 turns=3 unknown=0.0% other=0.0% rough=0 dashes=6 nonutc=0 sources=bus:emma,claude:bench written=true
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
SUM TOTAL input=124794 output=9483 cache_write=1200 cache_read=246000 reasoning=- rough=0 dashes=0,0,2,1,3 nonutc=0 turns=3 pairs=3 models=2 units=1
SUM OK month=2026-09 days=1 missing=0 pairs=3 models=2 units=1 nonutc=0
```


## nova-update

### First run

From the nova-tools checkout, using the declared Go-version fixture. This reads
local stdout only and performs no update or bus action.

```text
$ nova-update report --file cmd/nova-update/testdata/example.tsv
REPORT at=2026-09-12T17:29:33Z file=cmd/nova-update/testdata/example.tsv host=- as=- entries=1 kinds=engine,harness,model,pin,tool timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=8ms file=cmd/nova-update/testdata/example.tsv
```


## nova-version

### First run

From the nova-tools checkout, using the declared Go-version fixture. This reads
local stdout only and performs no update or bus action.

```text
$ nova-version report --file cmd/nova-version/testdata/example.tsv
REPORT at=2026-09-12T17:29:33Z file=cmd/nova-version/testdata/example.tsv host=- as=- entries=1 kinds=engine,harness,model,pin,tool timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=8ms file=cmd/nova-version/testdata/example.tsv
```


## nova-config

No fixture and no store: the first run reads the kind descriptors and the
migrations compiled into the binary, so every value below reproduces on
every bench. The real runs need a Postgres (`nova-config migrate`) and a
Redis (`nova-config apply`); `docs/nova-config/README.md` walks them, and
`cmd/nova-config/config_functional_test.go` runs them against a throwaway
Postgres and a throwaway Redis.

### First run

```text
$ nova-config kinds
CONFIG KIND name=machine table=config.machines fields=user,seat,slots,runners required=user,seat,slots rows=many
CONFIG KIND name=fleet table=config.fleet fields=store,coordinator required=- rows=one
CONFIG KIND name=friend table=config.friends fields=slots,tiers,roles required=slots,tiers rows=many
CONFIG KIND name=sprint table=config.sprint fields=coordinator required=- rows=one
CONFIG KINDS count=4

$ nova-config migrate --print
MIGRATION version=1 file=0001_schema.sql lines=23
MIGRATION version=2 file=0002_machine.sql lines=16
MIGRATION version=3 file=0003_friend.sql lines=13
MIGRATION version=4 file=0004_fleet.sql lines=14
MIGRATION version=5 file=0005_sprint.sql lines=12
CONFIG MIGRATE print=5 pg=-
```

`kinds` is one line per kind: its table under schema `config`, its fields in
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
INDEX ENTRY session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3
INDEX COVERAGE sessions=1 entries=1 shown=1

$ nova-cairn receipt --store ./cairns --session s1 --entry e1
RECEIPT OK session=s1 entry=e1 stamp=2026-09-17T12:05:00Z bytes=17 source=bench-a/session-7#L3 persisted=true published=false publish=manual
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
nova-redis spill: --owner is required; refusing to guess; run: nova-redis help

$ nova-redis spill --addr 127.0.0.1:6379 --owner rowan --name note --ttl 0s --value hi
nova-redis spill: --ttl is required and must be above zero; an unbounded key is a bug; run: nova-redis help

$ nova-redis fn load
nova-redis fn load: --addr is required; refusing to guess; run: nova-redis help
```

## nova-ci

Fixture: `cmd/nova-ci/testdata/example-events.jsonl`. The verb reads on stdin and
writes nothing, so each `$` line below pipes the fixture in; the transcript was
produced by running the built binary, not written by hand.

### First run

```text
$ nova-ci slowtests --budget 60 --load 4 --cpus 16 < cmd/nova-ci/testdata/example-events.jsonl
CI-SLOW package=github.com/mas-bandwidth/nova-tools/internal/example seconds=65.1s budget=60s slowest=TestSlowThing:63.4s,TestAlsoSlow:1.5s
CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict

$ nova-ci slowtests --budget 120 --load 4 --cpus 16 < cmd/nova-ci/testdata/example-events.jsonl
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
`--cpus` hand it in here so the transcript is the same on every machine. The common mistake is forgetting the
redirect: with an empty stdin the verb reads zero packages and prints
`CI-SLOW OK packages=0 slowest=none`, which is why the test step always tees
the stream first (`.github/workflows/ci.yml`).


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

### First run

These help commands open no store, use no credentials and write no records.
The complete output was captured from the tool. Its command-local
`firstrun_test.go` executes these commands and compares the whole transcript.

```text
$ nova-sprint help
nova-sprint: the sprint table: four tables on nova-table, the moves between them, and the coordinator's inbox

usage:
  nova-sprint init [--readers <a,b,...>] [--members <m1[:<width>],m2,...>] [--coordinator <name>]
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id>) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>]
  nova-sprint release <sentinel>... --reason <text> [--answers <note>]
  nova-sprint resolve [<id>...] [--stream <s>] [--limit <n>]
  nova-sprint start
  nova-sprint stop
  nova-sprint run
  nova-sprint tick
  nova-sprint goal set <name> [--file <path>] [--to file:<path>]
  nova-sprint goal show [<name>]
  nova-sprint goal drop <name>
  nova-sprint take --as <member> [<card>@<gen>...] [--limit <n>]
  nova-sprint finish --as <member> <card>@<gen>... [--failed] [--head <h>] [--report <text>]
  nova-sprint ask [<id>... | --group <id> [--expect <n>]] [--stream <s>] [--limit <n>] [--another] [--answers <note>]
  nova-sprint queue --as <reader|member> | --stream <s>
  nova-sprint read --as <reader> (--begin | --ok | --broken) [<card>...] [--limit <n>] [--finding <text>]
  nova-sprint accept (<id>... | --stream <s> | --read-ok | --group <id> [--expect <n>]) [--answers <note>]
  nova-sprint rework (<id>... | --group <id> [--expect <n>]) [--fix <text>] [--answers <note>]
  nova-sprint return (<id>... | --group <id> [--expect <n>]) [--reason <text>] [--answers <note>]
  nova-sprint drop (<id>... | --stream <s> --col <state> | --group <id> [--expect <n>]) --reason <text> [--answers <note>]
  nova-sprint rank <id>... (--score <n> | --first) [--answers <note>]
  nova-sprint merge --stream <s> [--batch <n>] [--conflict <id> | --cross <id>=<other> | --red [--suspect <id>...] | --rejected] [--note <text>]
  nova-sprint resume --stream <s> [--did <text>] [--answers <note>]
  nova-sprint fleet beat <member> [--load <percent>]
  nova-sprint fleet up <member> [--width <n>]
  nova-sprint fleet down <member>
  nova-sprint fleet sync [--check] [--pg <dsn>]
  nova-sprint fleet level
  nova-sprint reader add <reader>...
  nova-sprint ci <id>... (--red | --green) [--head <h>] [--run <id>] [--source <s>] [--note <text>]
  nova-sprint wait <note> (--for <duration> | --until <RFC3339>)
  nova-sprint ack <note>... --reason <text>
  nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>]] [--deadline <duration>] [--stale <duration>]
  nova-sprint card <id>
  nova-sprint log [--card <id>] [--stream <s>] [--member <m>] [--since <10m|RFC3339>] [--at-epoch <n>]
  nova-sprint check
  nova-sprint repair
  nova-sprint where [--watch] [--every <duration>]
  nova-sprint play [--simulation] [--seed <n>] [--every <duration>] [--broken <p>] [--fail <p>] [--stuck <p>] [--cross <p>] [--down <p>] [--up <p>] [--red <p>] [--flap <p>] [--batch <n>] [--hold] [--silent <member>@<from>+<for>]... [--ticks <n>]
  nova-sprint clear --confirm sprint
  nova-sprint teardown --confirm sprint

Every store verb takes --redis <addr> (else NOVA_SPRINT_REDIS, then
NOVA_REDIS_ADDR), --actor <name> (else NOVA_SPRINT_ACTOR; no
default: a verb that writes wants one), --op <id> (the same id again returns
the recorded result), --json and --max <n> (listed items; 0 is all). The
coordinator's verbs are the coordinator's alone (the first init names it:
--coordinator, else the actor); take, finish, read and fleet beat are the
workers', whose actor is the member or reader named; merge and ci are
reports; tick and run are the machine's; the reads need no actor (inbox
--read, which moves the coordinator's cursor, is the coordinator's). A set is
ids, a stream, a column, --limit n, or an inbox group: --group <id>, the id
inbox prints, which does not move, with --expect <n>, the size it printed,
which refuses a group that has changed. Each verb prints what moved (MOVED),
what did not and why (REFUSED, on stderr), its summary line, and the sprint's
line: landed/all percent -> ETA (a stopped machine has no ETA: STOPPED, then
landed/all and the percent when there are cards; every card landed, no ETA:
done in <time from the first start> while it runs, and STOPPED ... done once
the machine has stopped itself).

The tables are work, merge, readers and fleet, and the view is sprint; a store
holds one sprint (a second sprint is a second store). clear and teardown want
--confirm sprint, the name of the view, and refuse anything else.

A work card is named with its generation, <card>@<gen>: the generation the
worker holds, from queue --as <member> (--json: "gen"). take by id and finish
name it for every card; a card named without one is refused, naming the live
generation, and a generation that is not the live one is refused as stale.
take with no card takes the member's oldest ready cards (--limit n, default 1)
and prints each one's generation.

reading the inbox and answering a judgment:

  $ nova-sprint inbox
  JUDGMENT finish-0314a1b2-1.1   work came back failed  stream=s1  size=2  waited=4m0s  due=10:14:00  (s1-3,s1-7)  the tests went red
    rework with a fix:
      nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1
    drop:
      nova-sprint drop --group finish-0314a1b2-1.1 --expect 2 --reason '<why>' --answers finish-0314a1b2-1.1
  JUDGMENT merge-0315c3d4-1.1   stream stopped: stream branch red  stream=s2  size=10  waited=1m0s  due=10:25:00  (s2-1,s2-2,s2-3,s2-4,s2-5,s2-6,s2-7,s2-8,... all: nova-sprint inbox --open merge-0315c3d4-1.1)  suspects: s2-4 (of the batch of 10)
    take the suspect off and resume:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint resume --stream s2 --did 'returned s2-4' --answers merge-0315c3d4-1.1
    rework the suspect:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint rework s2-4 --fix '<fix>'
      nova-sprint resume --stream s2 --did 'returned s2-4 for rework' --answers merge-0315c3d4-1.1
    resume with what you did:
      nova-sprint resume --stream s2 --did '<what you did>' --answers merge-0315c3d4-1.1
  HAPPENED finish-0316e5f6-1.1   work came back ok  stream=s1  size=5  (s1-1,s1-2,s1-4,s1-5,s1-6)
  INBOX OK judgments=2 happened=1 cursor=-

A group is named by its id (its oldest notification's), which does not move
as groups come and go; a group number is refused. size is what --expect
takes: when the group has another size now the verb is refused, names what
was added or is gone, and changes nothing. Each decision is its commands, one
per line, in order: copy them, filling in a '<...>' first. inbox --open <id>
lists every member of a group, and every need a blocked group names; card <id> is everything about one primary.

The sprint done is no judgment: the tick that finds nothing open says it, one
HAPPENED line addressed to the coordinator and shown first, and stops the
machine (DONE):
  HAPPENED tick-done-0317a1b2-1.1   the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took 1h2m0s from the first start
    to continue: add work, then nova-sprint start

one answer to each judgment (every one prints its own, filled in):
  ready to accept             accept --group <id> --expect <n> --answers <notes>
  work came back failed       rework --group <id> --expect <n> --answers <notes>  (each fix is the work's report; --fix for all)
  a reader found it broken    rework --group <id> --expect <n> --answers <notes>  (each fix is the reader's finding)
  conflict on a card          resume --stream <s> --did 'rebased <card>' --answers <note>
  stream branch red           return <suspect> --answers <note>, then resume --stream <s> --did 'returned <suspect>' --answers <note>
  needs another stream first  rank <other> --first, then resume --stream <s> once <other> has landed
  merge queue rejected        resume --stream <s> --did '<what you did>' --answers <note>
  ci red                      rework --group <id> --expect <n> --fix '<fix>' --answers <notes>
  blocked on a dropped card   drop --group <id> --expect <n> --reason '<why>' --answers <notes>
  blocked on a missing card   drop <ids> --reason '<why>' or ack <notes> --reason '<why the named missing needs can be waived>'
  reads exhausted             ask --group <id> --expect <n> --another --answers <notes>
  repair skipped changes      card <primary>, then rework, return or drop --group <id> --expect <n> --answers <notes>
  an operation was stuck      check, then ack <note> --reason '<what you found>'
  a repeat: stop and look     card <primary>
  overdue: act                a decision above, or wait <note> --for 30m
  a stream not moving: look   where, then queue --stream <s>
  sentinel reached            release <sentinel> --reason '<what you found>' --answers <note>
  returned to review          rework, accept (its reads standing) or drop --group <id> --expect <n> --answers <notes>
  stranded in review          rework or drop (or ask, if never asked) --group <id> --expect <n> --answers <notes>
  stalled                     card <primary> (HELD says what holds it), then the decision it prints, or ack <note> --reason '<why>'

The machine: nova-sprint start sets it RUNNING, nova-sprint stop sets it
STOPPED; nova-sprint run ticks as soon as a line comes on the log (a verb's
step: a finish, a merge, a start), at most every 100ms, and once a second
while the log is quiet; nova-sprint tick is one tick by hand. Each tick deals
ready primaries, asks readers, resolves waiting primaries whose needs landed,
and writes the judgments that need the coordinator; a judgment open past its
due time is marked overdue, once. A stop halts the tick before its next part.
When nothing is left open (every card landed or dropped) the tick says "the
sprint is done" to the coordinator and stops the machine itself: DONE, in
where and the view; work added after leaves it STOPPED until nova-sprint
start. Every verb works in both states.

The fleet: each member says it is there with nova-sprint fleet beat <member>,
run on the machine every few seconds; a beat writes the time and the
machine's load. A member is up while its last beat is under 15s old and
down past that or when it has never beaten; the tick applies each change
(a member down has its unfinished work cards dealt to the members up; a
member up levels the ready queues). fleet down holds a member down whatever
it beats (status held); fleet up releases the hold, adding a member the
sprint does not know. The load cell is the machine's CPU busy percent of all
its cores (the one-minute load average over the cores where that cannot be
measured), the highest of the last 10s.

fleet sync makes the fleet match nova-config's machine rows in one step (--pg,
else NOVA_PG_DSN, as nova-config takes it): a member the table lacks is added
at its width (its slots less its friends'), a width that differs is set, a row
the inventory no longer names is held and its cards are dealt to the members
that stay; nothing else changes, and a second sync writes nothing. --check
prints the drift and writes nothing: exit 0 none, 2 some, 3 the config cannot
be read.

goal: the people who work on the sprint, each with a goal (what to keep
doing) and a route. While the machine is RUNNING the tick delivers each
person's goal down its route once when the machine starts and then every 5
minutes of running time (time STOPPED does not count); nothing is delivered
while it is STOPPED. The route file:<absolute path> gets the text, after one
header line REMINDER <n> to <name> at <time>, epoch <e>, in
place of what the file held, so a watcher of the file sees one current
reminder. The text is at most 8 KiB and is set from a file, and can be
different for each person. A route that fails is one judgment on the inbox
(goal set <name> --to <route> changes it, goal drop <name> removes the
person), closed when a delivery arrives. nova-sprint goal show says each
person's last push and whether it failed (where --json carries them). A clear
keeps the people and their goals.

exit codes: 0 done, 1 refused, 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync could not read the config

example:
  nova-sprint help
  nova-sprint help add
  nova-sprint help inbox

$ nova-sprint help add
usage: nova-sprint add [flags]
from `nova-sprint help`:
  nova-sprint add --stream <s> (<id>... | --count <n> | --sentinel <id>) [--needs <a,b>] [--before <id> | --after <id> | --score <n>] [--brief <text> | --brief-file <path>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --after <string>  place the cards in line after this primary of the stream
  --before <string>  place the cards in line in front of this primary of the stream
  --brief <string>  the brief: a child's whole brief, held to the card lint (every rule the coordinator gives a child; nova-swarm template --name card prints a card that passes, nova-swarm lint --rules lists them) and refused, exit 2, nothing written, when it fails; a card with no brief is not linted
  --brief-file <string>  the brief, read from this file: its bytes as they are, its one trailing newline cut (a brief of many paragraphs), then held to the card lint like --brief; not with --brief
  --count <int>  admit n primaries with generated ids <stream>-<n>
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --needs <string>  primaries that must land first, comma separated; each is a primary on the table
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)
  --score <string>  the first primary's score; the rest follow it (default: after every primary)
  --sentinel <string>  admit a sentinel with this id: a stop the coordinator releases; what sorts after it waits for it
  --sentinel-every <int>  with --count: a sentinel <stream>-gate-<n> after every k cards (a stop by its place in line)
  --sentinel-last  with --sentinel-every: a sentinel after the last card too
  --stream <string>  the stream the primaries belong to, for life; with --count, several streams comma separated, one step
exit codes: 0 done, 1 refused, 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync could not read the config

$ nova-sprint help inbox
usage: nova-sprint inbox [flags]
from `nova-sprint help`:
  nova-sprint inbox [--open <group>] [--read] [--wait [--timeout <duration>]] [--deadline <duration>] [--stale <duration>]
flags:
  --actor <string>  who is acting, recorded with every change (else NOVA_SPRINT_ACTOR; no default: a verb that writes wants one; a worker's verb is its --as name's)
  --at-epoch <int>  the inbox as it was at an earlier epoch (before a clear)
  --deadline <duration>  a judgment open longer is overdue
  --epoch <int>  the sprint epoch the caller holds (a worker's cards, from queue); a sprint cleared since refuses the step, naming the clear; the coordinator's verbs need none
  --json  print one JSON object for a program instead of the lines
  --max <int>  listed items of each kind; 0 is all
  --op <string>  the caller's operation id: the same id again returns the recorded result and changes nothing
  --open <string>  list every member, need and notification of the group of this id
  --read  move the cursor past what is shown: happened notifications before it are not shown again (open judgments always are)
  --redis <string>  the Redis address, host:port (else NOVA_SPRINT_REDIS, then NOVA_REDIS_ADDR)
  --stale <duration>  a stream with no progress for longer is shown stalled
  --timeout <duration>  with --wait, the longest wait; the inbox is shown when it passes
  --wait  block until the next tick-end note (the tick addressed the coordinator something), then show the inbox
exit codes: 0 done, 1 refused, 2 usage or a store that did not answer (fleet sync --check: there is drift), 3 fleet sync could not read the config

reading the inbox and answering a judgment:

  $ nova-sprint inbox
  JUDGMENT finish-0314a1b2-1.1   work came back failed  stream=s1  size=2  waited=4m0s  due=10:14:00  (s1-3,s1-7)  the tests went red
    rework with a fix:
      nova-sprint rework --group finish-0314a1b2-1.1 --expect 2 --answers finish-0314a1b2-1.1
    drop:
      nova-sprint drop --group finish-0314a1b2-1.1 --expect 2 --reason '<why>' --answers finish-0314a1b2-1.1
  JUDGMENT merge-0315c3d4-1.1   stream stopped: stream branch red  stream=s2  size=10  waited=1m0s  due=10:25:00  (s2-1,s2-2,s2-3,s2-4,s2-5,s2-6,s2-7,s2-8,... all: nova-sprint inbox --open merge-0315c3d4-1.1)  suspects: s2-4 (of the batch of 10)
    take the suspect off and resume:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint resume --stream s2 --did 'returned s2-4' --answers merge-0315c3d4-1.1
    rework the suspect:
      nova-sprint return s2-4 --reason 'suspect of the red batch' --answers merge-0315c3d4-1.1
      nova-sprint rework s2-4 --fix '<fix>'
      nova-sprint resume --stream s2 --did 'returned s2-4 for rework' --answers merge-0315c3d4-1.1
    resume with what you did:
      nova-sprint resume --stream s2 --did '<what you did>' --answers merge-0315c3d4-1.1
  HAPPENED finish-0316e5f6-1.1   work came back ok  stream=s1  size=5  (s1-1,s1-2,s1-4,s1-5,s1-6)
  INBOX OK judgments=2 happened=1 cursor=-

A group is named by its id (its oldest notification's), which does not move
as groups come and go; a group number is refused. size is what --expect
takes: when the group has another size now the verb is refused, names what
was added or is gone, and changes nothing. Each decision is its commands, one
per line, in order: copy them, filling in a '<...>' first. inbox --open <id>
lists every member of a group, and every need a blocked group names; card <id> is everything about one primary.

The sprint done is no judgment: the tick that finds nothing open says it, one
HAPPENED line addressed to the coordinator and shown first, and stops the
machine (DONE):
  HAPPENED tick-done-0317a1b2-1.1   the sprint is done  x1  for=coordinator  9 landed, 0 dropped, took 1h2m0s from the first start
    to continue: add work, then nova-sprint start

one answer to each judgment (every one prints its own, filled in):
  ready to accept             accept --group <id> --expect <n> --answers <notes>
  work came back failed       rework --group <id> --expect <n> --answers <notes>  (each fix is the work's report; --fix for all)
  a reader found it broken    rework --group <id> --expect <n> --answers <notes>  (each fix is the reader's finding)
  conflict on a card          resume --stream <s> --did 'rebased <card>' --answers <note>
  stream branch red           return <suspect> --answers <note>, then resume --stream <s> --did 'returned <suspect>' --answers <note>
  needs another stream first  rank <other> --first, then resume --stream <s> once <other> has landed
  merge queue rejected        resume --stream <s> --did '<what you did>' --answers <note>
  ci red                      rework --group <id> --expect <n> --fix '<fix>' --answers <notes>
  blocked on a dropped card   drop --group <id> --expect <n> --reason '<why>' --answers <notes>
  blocked on a missing card   drop <ids> --reason '<why>' or ack <notes> --reason '<why the named missing needs can be waived>'
  reads exhausted             ask --group <id> --expect <n> --another --answers <notes>
  repair skipped changes      card <primary>, then rework, return or drop --group <id> --expect <n> --answers <notes>
  an operation was stuck      check, then ack <note> --reason '<what you found>'
  a repeat: stop and look     card <primary>
  overdue: act                a decision above, or wait <note> --for 30m
  a stream not moving: look   where, then queue --stream <s>
  sentinel reached            release <sentinel> --reason '<what you found>' --answers <note>
  returned to review          rework, accept (its reads standing) or drop --group <id> --expect <n> --answers <notes>
  stranded in review          rework or drop (or ask, if never asked) --group <id> --expect <n> --answers <notes>
  stalled                     card <primary> (HELD says what holds it), then the decision it prints, or ack <note> --reason '<why>'
```

## nova-work

### First run

These help commands open no store, use no credentials and write no records.
The complete output was captured from the tool. Its command-local
`firstrun_test.go` executes these commands and compares the whole transcript.

```text
$ nova-work help
nova-work: every issue of every repository in one tree file, verified field for field (docs/SPEC-WORK-V1.md)

usage:
  nova-work import --org <org> (--out <tree.lisp> | --dry-run) [--repo <owner/name>]... [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>]
  nova-work verify --tree <tree.lisp> [--repo <owner/name>]... [--max <n>] [--max-calls <n>] [--page-size <n>] [--gh <path>] [--timeout <d>] [--max-bytes <n>]
  nova-work help [<verb>]
  nova-work version

import reads every issue of every repository of --org (or of each --repo)
from GitHub, read-only, with its full contents, and writes the tree to --out.
verify reads the same repositories again and prints one line for every
difference from the tree: MISSING (on GitHub, not in the tree), EXTRA (in the
tree, not on GitHub), DRIFT (a field that differs). Zero lines is the proof.

exit: 0 done, or verify found no difference; 1 verify found differences, or
import's own round trip through the file failed; 2 could not run.

example:
  nova-work help import
  nova-work version
```
