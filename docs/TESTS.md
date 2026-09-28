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
  machine prints instead. Already in use under [`## nova-sandbox`](#nova-sandbox)
  and [`## nova-swarm`](#nova-swarm), and checked by
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

Three `Requires:` lines are owed today, one per section, from the same dogfood
run: `## nova-decide` (the ladder block routes through JEV and wants
`JEV_API_KEY`), `## nova-post` (`send` wants a posting credential) and
`## nova-secrets` (two steps invoke `nova-check`, which is a different tool's
binary). nova-merge has no section since its lane-making verbs (`init`,
`quickstart`, `add`) left with the per-PR lander role; its kept verbs are
exercised by deprecated/cmd/nova-merge's own tests.

## nova-bus

Fixture: `cmd/nova-bus/testdata/example-bus`, copied out and given a repository of its own, with a bare repository beside it as `origin`. That is what the example's own README tells a reader to do and what the tool requires — every git-reading verb refuses a `--bus` that is not its repository's root, because git reports changed paths from the root and a bus one directory down would report an empty change set over unread notes. `cmd/nova-bus/firstrun_functional_test.go` builds both in `t.TempDir()`, so every push below lands in a bare repository on this disk and no line here reaches a network. A real bus is a **private** repository; this one is three participants and four notes, small enough to read in a sitting.

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

Fixture: a job directory of yours. Every path below is one you name — this tool has no defaults and guesses nothing — so the transcript is a worked example with `/Users/me/pool` standing in for yours, and the lines are what this Mac printed on 2026-09-12 with the paths shortened.

Platform: recorded on macOS (darwin) — the `backend=sandbox-exec` and `abi=-` fields and the `/Users/me/pool` fixture below are that Mac's; a Linux bench prints `backend=landlock`, an `abi=` value, and, where the wall is built below the ABI the kernel reports, a `used=` field this transcript has no slot for.

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

$ HOME=/Users/me/pool/jobs/j1/home nova-sandbox probe --read /Users/me/pool/ref --write /Users/me/pool/jobs/j1 --secret /Users/me/.config/anthropic/env
PROBE STEP name=write_outside_control expect=allow got=allow path=/Users/me/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=write_outside expect=deny got=deny path=/Users/me/pool/jobs/.nova-sandbox-probe-46261
PROBE STEP name=read_secret expect=deny got=deny path=/Users/me/.config/anthropic/env
PROBE STEP name=write_inside expect=allow got=allow path=/Users/me/pool/jobs/j1/.nova-sandbox-probe-inside
PROBE STEP name=read_root expect=allow got=allow path=/Users/me/bin/nova-sandbox
PROBE OK backend=sandbox-exec abi=- steps=5 passed=5 net=nopromise gpu=none

$ HOME=/Users/me/pool/jobs/j1/home nova-sandbox --read /Users/me/pool/ref --write /Users/me/pool/jobs/j1 -- /bin/sh -c 'echo hello > report.md; cat /Users/me/.config/anthropic/env'
SANDBOX NOTE dropped from the child's environment: GPG_AGENT_INFO SSH_AGENT_PID SSH_AUTH_SOCK; an agent socket speaks for a key the wall denies
SANDBOX OK backend=sandbox-exec abi=- read=1 read-noexec=0 write=1 net=nopromise cwd=/Users/me/pool/jobs/j1 cwdb64=L1VzZXJzL21lL3Bvb2wvam9icy9qMQ ancestors=11 cmd=sh gpu=none
cat: /Users/me/.config/anthropic/env: Operation not permitted
```

The last run is the whole tool in three lines: the job's own write landed, and the same command could not read the key that was in neither list. Its exit status is the wrapped command's, which is 1 here because `cat` failed.

### The disposable volume, and the one test that touches a disk

`nova-sandbox run` makes an APFS volume per run and deletes it on every path out (SPEC-SANDBOX, "The run verb"). Its logic is unit-tested against a fake `diskutil`, so an ordinary `go test ./cmd/nova-sandbox/` creates no volumes. The one real end-to-end test is behind the `novadisk` build tag, because eight CI runners share the Mac this repository is built on and a suite that made and destroyed volumes on every run would be a hazard rather than a test. Run it by hand on a Mac when the disposable-volume body changes — it needs no `sudo`:

    go test -tags novadisk -run TestARealRunLeavesNothingBehind ./cmd/nova-sandbox/

`nova-sandbox run --go` is what a card that builds Go uses; a plain `go build` inside a disposable volume was measured working with no flags at all once the optional roots' ancestors were granted (`internal/sandbox`, `TestAnOptionalRootsAncestorsAreGranted`).

Measured on the Studio, macOS 26 arm64, 2026-09-18: a 64m volume made, `sh -c 'echo hi > out; sleep 1'` run inside the wall with the volume as its only writable directory, and the volume gone from `/Volumes` and from `diskutil apfs list` afterwards — `SANDBOX DONE name=e2e63562 exit=0 wall=9.500 freed=32768`.

## nova-secrets

Fixture: a throwaway secrets store git working copy and age private key, as in [SPEC-SECRETS.md](SPEC-SECRETS.md).

### First run

```
$ nova-secrets keygen --as rowan --key /Users/me/.config/nova-secrets/rowan.key --age-keygen /opt/homebrew/bin/age-keygen --store ./secrets
SECRETS RULE   creation_rules:
SECRETS RULE     - path_regex: ^rowan\.yaml$
SECRETS RULE       age: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk,age1s6kpww894xpuylmck9f2g5kz2007a8nuy6guqrjj39s0gaqf6pkqydlata
SECRETS RULE NEXT: add these two lines to .sops.yaml (or run `nova-secrets seat add`)
SECRETS KEYGEN OK as=rowan key=/Users/me/.config/nova-secrets/rowan.key mode=0600 pub=age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk
Done. Your new key is at /Users/me/.config/nova-secrets/rowan.key. Nothing failed.
Next: send this public key to whoever seals your seat: age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5zhspjqwh35pk

$ nova-secrets check --store ./secrets --as other --key /Users/me/.config/nova-secrets/other.key --sops /opt/homebrew/bin/sops
SECRETS CHECK OK  as=other recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=9750ba9

$ nova-secrets names --store ./secrets --as other
SECRETS NAME key=GH_TOKEN clear=false
SECRETS NAMES OK as=other keys=1 shown=1 sealed=1 clear=0

$ nova-secrets exec --store ./secrets --as other --key /Users/me/.config/nova-secrets/other.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user --jq .login
! SECRETS EXEC OK as=other keys=1 only=1 required=1 file=/Users/me/secrets/other.yaml head=9750ba9 cmd=gh
fake-gh
```

`nova-secrets seat inject` is measured against the real sops and age
(`cmd/nova-secrets/seat_inject_functional_test.go`, functional tier): a store with
two seats, the coordinator's holding the new value and the bench's holding the old
one; the verb run with the coordinator's key and `--no-pr`; then the seal branch's
file opens with the bench's key alone and holds the new value beside the names it
had, the store is back on `main`, and `nova-secrets gate` approves the branch. The
help banner's own `seat inject` example is run through the one comparator in the
same package, its transcript held beside the test, ending
`SECRETS SEAT INJECT OK seat=air from=rowan names=1 committed branch=seal/air-NOVA_REDIS_BENCH_PASSWORD-20260927-013000`
with the branch's stamp the one declared run-owned value.

**The Studio's store file is `studio.yaml`, not `swarm-studio.yaml`.** Every Linux
bench's store follows the `swarm-<name>.yaml` convention (`swarm-hulk.yaml`,
`swarm-space.yaml`, `swarm-vision.yaml`, …). The Studio is the only bench whose
store file omits the `swarm-` prefix, and the darwin launcher used to ask for
the prefixed name — `swarm-studio.yaml` — and lost every card it took (80 of 80,
nova-tools #2000). The launcher's seat name must resolve to `studio.yaml` on the
Studio; a seat called `studio` that resolves to `swarm-studio.yaml` is a silent
empty wave.

## nova-check

Fixture: `cmd/nova-check/testdata/example-self`.

### First run

```
$ nova-check quickstart --dir ./self
QUICKSTART OK dir=./self checks=2: links, then nocode
LINKS OK files=4 links=3 excluded=0
NOCODE OK files=5 clean deny-list=floor\x20list
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
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**"
HYGIENE OK base=main head=card paths=sign/** findings=0

$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**" --max 2
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS:
HYGIENE MORE kind=finding shown=2 total=4 nova-check hygiene --repo "." --base "main" --head "card" --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**" --max 0
HYGIENE NO base=main head=card paths=sign/** findings=4
```

The `MORE` line is the same run with the cap lifted, quoted so it can be pasted
back (#1804) — it is the command that prints the rest, and it carries the
`--identity`, `--paths` and `--kind` without which it would not run at all:

```
$ nova-check hygiene --repo "." --base "main" --head "card" --identity "Rowan <rowan@mas-bandwidth.com>" --paths "sign/**" --max 0
HYGIENE FINDING reason=identity at=0a19082d2973: author someone@elsewhere.example and committer someone@elsewhere.example are not the pool's identity
HYGIENE FINDING reason=out-of-path at=elsewhere.go: this path matches none of the card's declared PATHS:
HYGIENE FINDING reason=out-of-path at=elsewhere/x.go: this path matches none of the card's declared PATHS:
HYGIENE FINDING reason=stray-file at=sign/RESULT.md: an added file matching the stray list's RESULT.md
HYGIENE NO base=main head=card paths=sign/** findings=4
```

`--identity` takes ONE pair of angle brackets. The second pair the help used to
show is refused rather than matched against nobody (#1805):

```
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <<rowan@mas-bandwidth.com>>"
nova-check hygiene: --identity "Rowan <<rowan@mas-bandwidth.com>>": the email carries an angle bracket; want `Name <email>`, one pair; run: nova-check help
```

`--kind` is a card kind the toolchain declares, and there is no default one. One
it does not hold is refused by name rather than left to unlock nothing (#1848):

```
$ nova-check hygiene --repo . --base main --head card --identity "Rowan <rowan@mas-bandwidth.com>" --kind fix-with-red-test
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
! SELFTALK FAIL ./pages/journal.md: STANDING: I cannot check my own work, so the second read went to someone else.
! SELFTALK FAIL ./pages/journal.md:10: INSTALLATION RANKING: It is the worst habit I have, and the reason the checklist exists at all.
SELFTALK DATED n=1 files=1
SELFTALK FAIL files=1 claims=2 standing=1 installations=1 dated=1 shown=2
SELFTALK NOTE catches known SHAPES only: register, irony and quoted-specimen context are invisible to grammar, and a quoted verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file.

$ nova-self-talk --rule-doc RULES.md ./pages/RULES.md ./pages/journal.md   # Stderr: whole
SELFTALK RULEDOC ./pages/RULES.md: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule
! SELFTALK FAIL ./pages/RULES.md:8: INSTALLATION VERDICT-IDIOM: A rule weakened to improve a score is dead as a practice: the score got better and the wall got thinner.
! SELFTALK FAIL ./pages/journal.md: STANDING: I cannot check my own work, so the second read went to someone else.
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
FUSE FAIL quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box ./fuse-box.json a-public-issue-tracker)

$ nova-fuse quarantine --box ./fuse-box.json a-forum "a post addressed me and asked for a token"
QUARANTINE OK a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (verified by re-reading the box; soft: yours to lift when the surface is safe again; tell your person now)

$ nova-fuse check --box ./fuse-box.json a-forum
FUSE FAIL quarantine=a-forum since=2026-09-09T18:27:40Z: a post addressed me and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box ./fuse-box.json a-forum)

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
OPEN OK session=s1 store=./cairns publish=manual stamp=2026-09-17T12:00:00Z

$ nova-cairn append --store ./cairns --session s1 --entry e1 --text "the words to keep" --source bench-a/session-7#L3 --publish manual --now 2026-09-17T12:05:00Z
APPEND OK session=s1 entry=e1 persisted=true published=false publish=manual duplicate=false stamp=2026-09-17T12:05:00Z

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

## nova-post

The gate is draft, show, approve, send: the tool prepares and renders, and Glenn's
bus receipt releases. A draft is rendered once, written under `--drafts` as
`<hash>.post` and `<hash>.meta`, and shown byte for byte; `send` refuses unless the
receipt exists in `--bus`, is Glenn's, names this hash and is under 24 hours old, and
it transmits the stored bytes only. The examples below name throwaway drafts, an
allowlist and a body of your own; a missing flag is one line on stderr at exit 2, and
a target the allowlist does not name is exit 1.
Fixture: a drafts directory you create, an allowlist with one
`channel<TAB>target` line, and a body file. Every test runs against fake
endpoints — an `httptest` server per channel, a fake mailer, an injected clock
— and the refusals below open no socket. A credential is read only from the
environment `nova-secrets exec` delivers, is never printed, and is never
measured.

The outward gate of [docs/SPEC-OUTBOUND.md](SPEC-OUTBOUND.md), on one of its four
channels: `ghost`. Every line below is local, and no socket, no credential and no
provider is touched: `draft` renders the payload and writes it under `./drafts`,
and `show` prints the exact payload bytes to stdout and its OK line to stderr
(the `! ` line). `send` releases only on a bus receipt from Glenn that names the
hash and is under 24 hours old, so it is not in this block: without a receipt it
refuses at exit 1. The fixture is exact, because the hash is: `./body.md` holds
the one line `A first post for the ghost channel.`, `./allowlist` holds the one
line `ghost<TAB>example.com`, and `./drafts` is an empty directory you create.
The hash is the SHA-256 of the payload the draft wrote, so
`shasum -a 256 ./drafts/<hash>.post` prints it back.
`cmd/nova-post/firstrun_test.go` writes that fixture and runs every `$` line.

### First run

```text
$ nova-post draft --channel ghost --target example.com --file ./body.md --drafts ./drafts --allowlist ./allowlist
POST DRAFT OK hash=b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f channel=ghost target=example.com bytes=115 drafts=./drafts

$ nova-post show --draft b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f --drafts ./drafts
{"posts":[{"title":"","html":"A first post for the ghost channel.\n","status":"published","tags":["example.com"]}]}
! POST SHOW OK hash=b150f3e20ecedbf62ea231eca325d74de5dc1123d0e061c7453f524fb6c8315f channel=ghost bytes=115 drafts=./drafts

$ nova-post version
nova-post devel linux/amd64 go1.26.5
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
measurement, and only `--enforce` (the nightly space legs) makes it exit 2. The
CI-LOAD line is the host's load average, printed and never judged; `--load` and
`--cpus` hand it in here so the transcript is the same on every machine. The common mistake is forgetting the
redirect: with an empty stdin the verb reads zero packages and prints
`CI-SLOW OK packages=0 slowest=none`, which is why the test step always tees
the stream first (`.github/workflows/ci.yml`).


## nova-sprint

Run by `cmd/nova-sprint/firstrun_test.go` in an empty directory, which must
still be empty afterwards: the wide table is read from Redis and written nowhere
(#3326), so none of these lines needs a server and none writes a file.

### First run

```text
$ nova-sprint table --once --fixture table.txt
! nova-sprint table: flag provided but not defined: -fixture; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --once
! nova-sprint table: --redis <addr> is required; the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]; run: nova-sprint help

$ nova-sprint table --check
! nova-sprint table: --check needs --redis <addr>, a throwaway server for the fixture keyspace; run: nova-sprint help
```

The first is the deleted file cut: `--fixture` and `--refresh` are unknown
flags, while `--out <file>` now publishes the wide table by atomic rename
(#3343) and `--layout live [--out <file>]` publishes the whole sprint table
(#3530). The second and third name the server the table is read
from. `TestTableWritesNoFile` renders from a throwaway server twice (a second
start) and checks the directory stays empty.

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
