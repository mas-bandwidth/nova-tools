# nova-swarm dogfood — the tool (inception/mercury-2.5), 2026-10-07

Read as a stranger: only `nova-swarm -h`, `nova-swarm help`, `nova-swarm <verb> -h`, and
the tool's pages under `docs/` (`docs/SPEC-SWARM.md`, the `nova-swarm` section of
`docs/CLI.md`, and `docs/nova-swarm-quickstart.md`). Built from
`sprint/mechanical-2026-10-02` at `bba7b0ca8` and used as
`nova-swarm v1.0.1-0.20261007` on a darwin bench.

Every verb ran with its real flags against scratch directories and a scratch slot store;
`native` ran a fake harness end to end (a card, a slot, a root, a published `RESULT.md`);
the refusals ran too. A member loop was not run: the card forbids starting one, and
`member` needs a sprint server, so only its help and its flag refusals were exercised.

The bench name, the login name and absolute paths are redacted below as `<bench>`, `~` and
`<job>`.

## Findings

### 1. `native` cannot launch the relative `--harness` its own example prints — URGENT

Command (the `example:` line of `nova-swarm native -h`):

    nova-swarm native --harness ./harness --model provider/model --card card.md --slot slots/1 --root jobs --deadline 30m --tokens unmetered

Printed (exit 2):

    NATIVE REFUSED: the harness binary ./harness is missing; run: nova-swarm native -h

With a relative path that does exist at the caller, the same run stages the card, then dies
inside the child's working directory (first 3 lines, exit 2):

    STAGE OK bench=<bench> repo=https://github.com/mas-bandwidth/nova-tools.git base=c76fcb24 secs=2 clone=1.3 fetch=0.0 checkout=0.9
    NATIVE NOTE catalog: no catalog at <job>/roota/catalog/models.json (a member refreshes it at its start); the harness fetches its own at this start
    NATIVE REFUSED: the child could not be started: fork/exec ../bin/fakeharness: no such file or directory; run: nova-swarm native -h

Expected: the tool resolves `--harness` to an absolute path (or starts the child with the
caller's working directory), so `./harness` runs. `--harness` is checked for existence
against the caller's directory but executed with the child's working directory set to the
job directory, so every relative harness path — the form the `native -h` example and the
`docs/TESTS.md` transcript both print — is a path that cannot launch. An absolute path
(`--harness "$PWD/../bin/fakeharness"`) runs the same card to `NATIVE OK ... rc=0`.
Grade: URGENT (help that lies: its own first-run example cannot run).

### 2. A missing input file is refused with no remedy — URGENT

Commands and their whole output (each exit 2):

    nova-swarm lint --card ./nope.md
    nova-swarm lint: --card wants a readable file of the card text: open ./nope.md: no such file or directory

    nova-swarm verify --result ./nope --contract X --label l
    nova-swarm verify: --result wants a readable RESULT.md: lstat ./nope: no such file or directory

    nova-swarm native --tokens unmetered --harness /bin/echo --model a/b --card ./nope.md --slot ./r/s --root ./r --deadline 30s
    nova-swarm native: --card wants a readable file: open ./nope.md: no such file or directory

    nova-swarm lint --fleet ./nope.sh
    nova-swarm lint: --fleet wants a readable launcher script of shell text to check: open ./nope.sh: no such file or directory

Expected: the one refusal grammar ends `; run: <remedy>`, and the same verbs' missing-flag
refusals do (`nova-swarm lint: --card is required; ...; refusing to guess`). These
file-open refusals stop after the operating system's own words: no `run:`, no `refusing to guess`, nothing a stranger can act on beyond the path they already typed. `step` is the one
verb that does print a remedy on its unreadable-card line, so the omission is not a house
style.
Grade: URGENT (a refusal with no remedy).

### 3. `slots release` prints its success word on a KEPT refusal — URGENT

Command:

    nova-swarm slots release --store ./slots2 --owner ada --label live1

Printed (exit 2, while `/bin/sleep 8` still held the lease):

    SLOTS RELEASED owner=ada released=0 held=1 live=1
    SLOTS KEPT owner=ada live=1: a lease whose holder is still running is not freed, because freeing it does not stop the holder -- it only lets a second run take the same seat; stop the holder (slots list names its pid) or pass --force

Expected: the refusal leads, `SLOTS KEPT`, with no success word on a non-zero exit (the
standard's "no OK word on a non-zero exit"). Instead the second line is the refusal and the
first line is the verb's success status word. `--force` on the same lease prints only
`SLOTS RELEASED ... released=1`, so a reader or a parser keying on `RELEASED` takes the
refused run for a done one.
Grade: URGENT (wrong result).

### 4. No verb accepts `--json` — NEXT

Command (each verb in turn; all exit 2):

    nova-swarm doctor --json
    nova-swarm doctor REFUSED: unknown flag --json; the flags of doctor are --local, --path; run: nova-swarm help doctor
    nova-swarm native --tokens unmetered ... --json
    nova-swarm native REFUSED: unknown flag --json; the flags of native are --auth, --bench, --card, ... and 11 more; run: nova-swarm help native
    nova-swarm slots list --store ./slots1 --json
    nova-swarm slots list REFUSED: unknown flag --json; the flags of slots list are --store; run: nova-swarm help slots

Expected: the repository standard's "Every verb accepts `--json`" and "one value, rendered
as lines or as JSON of the same value" (`AGENTS.md`, "One shape across the set"). `version`,
`doctor`, `verify`, `lint`, `step`, `template`, `profile`, `native`, `member`, `disk-guard`,
`install`, `uninstall`, `worker` and every `slots` subverb refuse it. A tool that is a runner
for other AIs has no machine rendering at all.
Grade: NEXT (a missing flag on every verb).

### 5. `step` prints its program name as one token, `nova-swarmstep` — NEXT

Command:

    nova-swarm step --card ./nope --dir . --dry-run

Printed (exit 2):

    nova-swarmstep REFUSED: --card wants a readable card file: open ./nope: no such file or directory; run: nova-swarm help step

Expected: `nova-swarm step REFUSED: ...`, the refusal grammar's `<tool> <verb> REFUSED:`
(the tool name and the verb are joined by a lost byte). The same one-token name appears on
`step`'s other refusal, the card without a script step. Exit code and remedy are right.
Grade: NEXT (a grammar break in the printed identity).

### 6. `profile --jobs` on a glob that names nothing succeeds with `jobs=0` — NEXT

Command:

    nova-swarm profile --jobs /nonexistent/*

Printed (exit 0):

    PROFILE SUMMARY jobs=0 mean_wall=0.0 clone=0.0 deps=0.0 read=0.0 edit=0.0 test=0.0 retry=0.0 result=0.0

Expected: a refusal naming the glob that matched nothing, or a note beside the summary.
`--jobs` is required and its whole job is to read job directories; a typo in the glob is
reported as a clean run over zero jobs. The same silent zero answers for
`--jobs ./roottl/slot-1/timeline.tsv`, a file that does not exist.
Grade: NEXT (a silent fallback that a script reads as success).

### 7. `disk-guard --dry-run` does not report the bytes it would free — NEXT

Command:

    nova-swarm disk-guard --dry-run --cache "$PWD/caches" --cache-max-gb 1 --logs "$PWD/logs" --log-max-mb 10 --disk-floor 0

Printed (exit 0):

    WOULD-ROTATE log <job>/logs/loop.log freed=0 size=62914560 keep=3
    WOULD-TRIM go-build ~/.cache/go-build size=7524706688 cap=1073741824
    DISK-GUARD OK freed=0 free=2490389479424

The real run of the same command printed:

    TRIMMED go-build ~/.cache/go-build freed=6781229606 size=743477082 cap=1073741824
    DISK-GUARD OK freed=6781229606 free=2497076232192

Expected: the dry run's own prediction of `freed` — the number the run exists to show before
it is allowed to remove anything. The `WOULD-TRIM` line carries no `freed=` at all and the
summary says `freed=0`, while the run it is previewing frees 6.78 GB; only the `size=` and
`cap=` fields let a reader work the number out. The dry run judges the same rules, but the
number a person reads first is wrong.
Grade: NEXT (a dry run that under-reports its own action).

### 8. `slots list` is a listing with no `--max` — NEXT

Command:

    nova-swarm slots list --store ./slots1 --max 5

Printed (exit 2):

    nova-swarm slots list REFUSED: unknown flag --max; the flags of slots list are --store; run: nova-swarm help slots

Expected: the standard's "A listing is cut by `--max` with a `MORE shown=<n> total=<n>`
line". `slots list` is the one plain listing in the tool, it prints one line per lease and
one line per owner, and it has no bound and no total beyond `leases=<n>`.
Grade: NEXT (a listing without its standard bound).

### 9. The `slots` write verbs have no `--dry-run` — NEXT

Command:

    nova-swarm slots init --store ./sd --owner me --capacity 2 --share 1 --dry-run

Printed (exit 2):

    nova-swarm slots init REFUSED: unknown flag --dry-run; the flags of slots init are --capacity, --owner, --share, --store; run: nova-swarm help slots

Expected: the standard's "A verb that writes has a dry run" that prints the plan from the
same code path. `slots init`, `take`, `release` and `run` all write to the store (they
create `shares.tsv` and lease directories, and `run` starts a command) and none accepts
`--dry-run`; `install`, `uninstall`, `step` and `disk-guard` do, so the shape exists in the
tool.
Grade: NEXT (a missing dry-run on four write verbs).

### 10. `install --dry-run` prints the unit as one quoted line — NEXT

Command:

    nova-swarm install disk-guard --dry-run --every 15m --dir ./unit

Printed (exit 0, first 2 lines; the second is the whole unit):

    INSTALL DISK-GUARD DRY-RUN unit=unit/nova-swarm-disk-guard.service; nothing was written or loaded
    "[Unit]\nDescription=nova disk-guard: the machine's disk upkeep, one pass every --every (nova-swarm disk-guard)\nStartLimitIntervalSec=0\n\n[Service]\nExecStart=\"<job>/bin/nova-swarm\" \"disk-guard\"\nRestart=always\nRestartSec=900\n\n[Install]\nWantedBy=default.target\n"

Expected: the unit text laid out to be read, since the dry run exists so "a reader can see
what a verb does before letting it". Instead the unit is a Go-quoted single line with `\n`
escapes, which is neither the file that would be written nor a readable rendering of it.
Grade: NEXT (a dry run a person cannot read).

### 11. `install bogus` fixes the call with another tool's check — NEXT

Command:

    nova-swarm install bogus

Printed (exit 2):

    nova-swarm install REFUSED: no unit kind bogus; the kinds are disk-guard|mirror-refresh; run: nova-sprint units --check

Expected: the remedy that fixes this call, `nova-swarm install disk-guard|mirror-refresh`.
`nova-sprint units --check` is a different tool's check of units already installed; it
neither lists the two kinds nor repairs the argument. `uninstall bogus` on the same input
points back at `nova-swarm help uninstall`, so the wrong breadcrumb is `install`'s alone.
Grade: NEXT (a remedy that does not answer the refusal).

### 12. `slots take --kind` names neither the kinds nor a workable remedy — NEXT

Command:

    nova-swarm slots take --store ./slots1 --owner ada --n 1 --for 30m --kind schema --label schema1

Printed (exit 2):

    SLOTS REFUSED owner=ada want=4 held=0 share=1 free=2 holders=- remedy="nova-swarm slots list --store ./slots1"

Expected: the refusal says the kind's admission weight (`want=4`) but not where 4 comes
from, and the remedy is `slots list`, which only prints state: the reader cannot learn the
weights, the kind names, or that the fix is a larger share (or a lighter kind).
`slots take -h` describes `--kind` only as "the card's kind, charged at its admission
weight" — the weights and the kind list are nowhere in the help. `want=4` against a share of
1 is otherwise a mystery number.
Grade: NEXT (unclear help and a remedy that does not fix).

### 13. `lint -h` prints its `--card` value as `<lint <file>>` — NEXT

Command:

    nova-swarm lint -h

Printed (exit 0, the flag line):

      --card <lint <file>>  the card file to lint before any spend (the bare lint <file> is the same)

Expected: `--card <file>`, a value placeholder. The angle-bracket value wraps the verb's own
usage fragment, so the flag's metavar is a phrase, not the value it wants. `template -h`
prints `--name <name>` and `native -h` prints `--harness <path>`, so the malformed one is
`lint`'s.
Grade: NEXT (help that misstates a flag's value).

### 14. Two shipped card fixtures DRIFT at the base they ship with — NEXT

Command:

    nova-swarm lint --card cmd/nova-swarm/testdata/cards/tools11-c1-links-specpulse.md

Printed (exit 1, first line):

    LINT DRIFT card=tools11-c1-links-specpulse.md kind-declared: 2: KIND: "dogfood" is not a kind this toolchain declares; one of: fix-red, transcript-test, rebase, sweep, mutation-kill, guard, read, probe, text, tone, report remedy=the card carries `KIND: <kind>` as the first typed line under the contract line, and the kind is one the pool's kinds list names; the cutter writes it from the pool row and a model never does; a declared kind whose row names no instruction kind is this finding too: name the instruction kind on that row of the one kinds list

The same DRIFT, with the same `KIND: "dogfood"`, is on
`cmd/nova-swarm/testdata/cards/tools11-c2-links-toplevel.md`; the third fixture,
`queue-1282-bench-hygiene-home-guard.md`, lints `LINT OK card=... checks=45`. The
`nova-sprint add` path that `lint --child-rules` stands in for would refuse both briefs.
Expected: the tool's own fixtures lint clean at the commit that ships them, or the two
stale cards are re-cut. `dogfood` is also a kind the dogfood cards in `docs/dogfood/` name,
so the two tools' kind lists disagree.
Grade: NEXT (shipped fixtures the tool refuses).

### 15. `docs/SPEC-SWARM.md` and `docs/CLI.md` undercount and omit living verbs — NEXT

Command:

    grep -n "eleven living verbs" docs/SPEC-SWARM.md

Printed:

    48:The tool exposes eleven living verbs, dispatched directly from `cmd/nova-swarm/main.go`:

Expected: the count and the usage block to match the binary. `nova-swarm help` prints
fourteen top-level verbs (`version`, `doctor`, `verify`, `lint`, `step`, `template`,
`profile`, `native`, `member`, `disk-guard`, `install`, `uninstall`, `slots`, `worker`) plus
`slots run`; the spec says eleven and its usage block omits `step`, `install`, `uninstall`
and `slots run`, while `docs/CLI.md`'s usage block omits `install`, `uninstall` and
`slots run`. A stranger reading the normative page does not learn that `step` or `install`
exist; `docs/CLI.md` is the reference the README links to.
Grade: NEXT (stale normative docs).

### 16. Some verb helps omit the example or the effect line — NEXT

Commands:

    nova-swarm worker -h

Printed (exit 0, the whole help after the usage lines):

    exit codes: 0 WORKER OK; 1 the description was read and drifts, each on its WORKER
      DRIFT line; 2 it cannot be read, or a bad invocation
    effect: inspection: reads, writes nothing

`worker -h` carries no `example:`; `slots init -h`, `slots take -h` and `slots release -h`
carry an `example:` but no `effect:` line, which every other verb's help has
(`effect: local write: ...`, `effect: delivery: ...`). `slots run -h` carries neither an
`example:` nor an `effect:`.
Expected: every verb's help names its effect and its first run, the standard's onboarding
points 1 and "its effects are explicit". The group helps (`slots -h`) are complete, so the
omission is per-subverb.
Grade: NEXT (help that omits what the standard asks for).

### 17. `template --name text` prints the deprecated contract line — NEXT

Command:

    nova-swarm template --name text

Printed (exit 0, line 1):

    RESULT <label> sha=<sha12>

Expected: the current contract form `RESULT: <label> sha=<sha12>`, the shape `lint --rules`'s
`result-first` rule calls the form to write; the rule accepts the colon-less line only as a
stopgap. Every other card template (`card`, `read-pr`, `probe-row`, `fix-card`) prints the
colon. A worker that copies the text template writes the stopgap.
Grade: NEXT (a shipped template that teaches the deprecated form).

### 18. `slots run` propagates a command's exit with no line naming it — NEXT

Command:

    nova-swarm slots run --store ./slots3 --owner ada --for 1m --label f -- /bin/false

Printed: no line; exit 1.

Expected: a `SLOTS` line naming the command and its `rc=1` before the tool exits with it.
The verb "propagates the command exit status", but a reader sees exit 1 with no output and
cannot tell the command's own failure from the tool's, which the standard's "the status word
leads every line" and "nothing fails silently" both want named. The success case prints
nothing either (`slots run ... -- /bin/echo hi` prints only the echo).
Grade: NEXT (a nonzero exit with no line).

## What held

The banner answers its three questions, names the exit table, and its
`example:` block runs as printed (`template --name read-pr`, `template --name worker`,
`lint --rules` all exit 0). `version` and `version --version` print the one identity line.
`doctor` reads two binary stamps, refuses an unreadable one with a remedy, and adds the
three `DOCTOR HARNESS` lines. `template --name card` lints `LINT OK ... checks=45` and
`--child-rules` on it exits 0; a drifted fixture card exits 1 with the rule quoted and a
remedy. `verify` writes `RESULT.md.receipt`, exits 1 on a contract mismatch and 0 on a
match. `native` with an absolute harness path ran the fake harness end to end and printed
`NATIVE OK ... rc=0 sandbox=none-by-flag budget=unmetered` with a published `RESULT.md`.
`slots init`/`take`/`list`/`release`/`run` lease, refuse an over-share take with the holder
list, keep a lease whose holder still runs, free it under `--force`, and propagate a
command's exit status. `disk-guard` judged, trimmed, rotated and reported
`DISK-GUARD OK freed=<bytes>`, and its `--dry-run` removed nothing. `install`/`uninstall --dry-run` wrote nothing, and `install mirror-refresh` refused with the reason (no mirror
verb yet). `worker check` named a missing harness and a bad JSON shape with the field list.
The lane stayed clean: the refusals and dry runs left no file outside the scratch
directories.

READ 6/10 — the banner, the exit table, and most verb helps are clear and paste-ready, but the normative `docs/SPEC-SWARM.md` usage block is
stale, `slots`' write verbs and `worker` omit the example or effect lines siblings carry,
`lint -h` prints a phrase as its flag value, and the `native -h` example it hands a stranger
cannot run.

USE 5/10 — with an absolute harness path the core loop works and its store verbs lease,
refuse and release honestly, but the documented first `native` run dies on a relative
`--harness`, four refusal paths strand the reader with no remedy, `disk-guard --dry-run`
misreports what it would free, `slots release` leads a refusal with its success word, and no
verb offers the `--json` rendering every other tool in the set has.

urgent=3 next=15
