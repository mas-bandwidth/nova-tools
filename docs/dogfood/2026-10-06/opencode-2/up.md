# nova-up dogfood, 2026-10-06 (opencode-2)

Tool: nova-up. Build: `nova-up v1.0.1-0.20261007161649-992a0122daac linux/amd64 go1.26.6`.
Run cold, from the binary's own help (`nova-up -h`, `nova-up help`, `nova-up <verb> -h`) and its
page under `docs/` (`docs/SPEC-UP.md`) only, with every verb (`up`, `version`, `help`) at least
once with its real flags against scratch roots under one temporary directory, the refusals
included. No engine or service was started: this card's rules forbid it ("Never start a server on
this machine"), so the full apply of `up --local` (which installs one service-manager unit and
starts the loopback Redis) was not run. The real applies below stop before any write, at a missing
program or at a root the plan cannot make; the applied success path (`UP UNCHANGED`, a second run
all `ok`, `seat.env`, the smoke card landed) is reported not done. These findings were recorded at
`sprint/mechanical-2026-10-02`'s tip `2bf2be83e5c2`, the base this card starts from; the carried
attempt-1 commit `41ad1b2c` was re-verified there.

Commands were typed with

    U=/tmp/nova-up-dogfood
    export PATH=/tmp/updog/bin:/usr/bin:/bin   # symlinks to the same tools, so paths carry no names
    ROOT=/tmp/updog/try7
    : > /tmp/updog/afile

so `/tmp/updog` is scratch and `afile` is one regular file; no real store was written.

## 1. A `--root` that is an existing file is planned `ok`, and the real run's remedy cannot work — URGENT

**Command:**

    $U up --local --dry-run --root /tmp/updog/afile

**Printed:**

    UP OK root=/tmp/updog/afile steps=8 changes=4 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok linux: loops are systemd user units

and the two lines that matter, under those:

    UP dirs ok /tmp/updog/afile
    UP sprint ok mem:/tmp/updog/afile/stores/sprint.twin

exit 0. **Expected:** `dirs change` or a refusal: the root exists but is not a directory, so
`stores/`, `keys/`, `logs/` and `smoke/` do not stand and `sprint`'s store cannot be `ok`; the
plan reads only that the path exists. The real run says the same and then fails late:

**Command:**

    $U up --local --root /tmp/updog/afile

**Printed** (stderr, exit 1):

    UP FAILED root=/tmp/updog/afile steps=5 changes=1 applied=0: step secrets: /tmp/updog/bin/nova-secrets keygen --as coordinator --key /tmp/updog/afile/keys/coordinator.key --age-keygen /tmp/updog/bin/age-keygen: exit status 2: SECRETS KEYGEN REFUSED: key directory /tmp/updog/afile/keys is absent; run: mkdir -m 700 -p /tmp/updog/afile/keys; the steps before it are applied, and a run again starts from what they left
    UP platform ok linux: loops are systemd user units
    UP dirs ok /tmp/updog/afile

The remedy `mkdir -m 700 -p /tmp/updog/afile/keys` cannot succeed while `/tmp/updog/afile` is a
file, so the refusal names no action a reader can take. Grade: URGENT.

## 2. `--root` with an empty value silently becomes the default `~/nova` — URGENT

**Command:**

    HOME=/tmp/updog/home $U up --local --dry-run --root ""

**Printed:**

    UP OK root=/tmp/updog/home/nova steps=8 changes=6 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok linux: loops are systemd user units

exit 0. **Expected:** a refusal naming `--root`'s value: an explicit empty value is a missing input,
not the default; the standard forbids a default that stands in for a missing input. Instead the
empty value is discarded and the one root the write verb touches silently becomes `~/nova`, which a
caller passing an unset variable (`--root "$ROOT"`) would not see until the summary line — or, on a
run without `--dry-run`, after it wrote. Grade: URGENT.

## 3. `--root ~/dir` is not expanded, so it writes under a literal `~` below the cwd — NEXT

**Command:**

    $U up --local --dry-run --root "~/zhi-nova-x"

**Printed:**

    UP OK root=/tmp/updog/~/zhi-nova-x steps=8 changes=6 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok linux: loops are systemd user units
    UP dirs create /tmp/updog/~/zhi-nova-x: . stores keys logs smoke

**Expected:** `~` expanded the way the default `~/nova` is, or a refusal; a quoted or scripted
`--root ~/nova-alt` silently targets `./~/nova-alt`. The help says the default is `~/nova` and does
not say that a `~` in the value is literal. Grade: NEXT.

## 4. `--json` renders the step lines only as one opaque `payload` string — NEXT

**Command:**

    $U up --local --dry-run --root /tmp/updog/try --json

**Printed** (one stdout line):

    {"result":{"verb":"up","status":"ok","exit":0},"facts":{"root":"/tmp/updog/try","steps":8,"changes":6,"applied":0,"dry_run":true},"notes":["dry run: nothing written; run it without --dry-run to apply"],"payload":"UP platform ok linux: loops are systemd user units\nUP dirs create /tmp/updog/try: . stores keys logs smoke\nUP binaries ok 8 on PATH, each answering its version\nUP sprint create mem:/tmp/updog/try/stores/sprint.twin\nUP secrets create /tmp/updog/try/secrets seat=coordinator\nUP redis create 127.0.0.1:6390 loop=redis-local for nova-bus: unit create, passwords to seal=4, acl apply, fn load\nUP seat create /tmp/updog/try/seat.env seat=coordinator\nUP smoke create one card on /tmp/updog/try/smoke/sprint.twin beside the throwaway repository /tmp/updog/try/smoke/repo"}

exit 0. **Expected:** the per-step lines as typed rows from the one value (`items`, each with step,
status and detail), per the one-value-two-renderings rule; a JSON caller has to split `payload` on
newlines and re-parse the `UP <step> <status> <detail>` text it already had in the line rendering.
Grade: NEXT.

## 5. The bare command's diagnosis changes when only `--json` is added — NEXT

**Command:**

    $U

**Printed** (stderr, exit 2):

    UP REFUSED: no verb and no file given; the verbs are up, version; run: nova-up help

**Command:**

    $U --json

**Printed** (stdout, exit 2):

    {"result":{"verb":"up","status":"refused","exit":2,"remedy":"nova-up help","why":["--local is required; it wants nothing after it: the mode that sets up this one machine with no config"]},"facts":{}}

**Expected:** the same empty invocation reported the same way; adding `--json` alone changes the
diagnosis from "no verb" to "verb `up`, `--local` required", so the two renderings disagree about
what the reader did wrong. Grade: NEXT.

## 6. A failed apply says the steps before it are applied while `applied=0` — NEXT

**Command:** (the real run of finding 1)

    $U up --local --root /tmp/updog/afile

**Printed** (stderr, exit 1):

    UP FAILED root=/tmp/updog/afile steps=5 changes=1 applied=0: step secrets: ... SECRETS KEYGEN REFUSED: key directory /tmp/updog/afile/keys is absent; run: mkdir -m 700 -p /tmp/updog/afile/keys; the steps before it are applied, and a run again starts from what they left
    UP platform ok linux: loops are systemd user units
    UP dirs ok /tmp/updog/afile

**Expected:** the closing sentence should match the summary: `applied=0` with "the steps before it
are applied" tells a reader something was left behind when nothing was. Grade: NEXT.

## 7. `help`'s usage omits the `up` verb that the refusals and the example name — NEXT

**Command:**

    $U help

**Printed** (first three lines):

    nova-up: sets up nova on one machine, from nothing to a first sprint

    how it works: steps in order: platform, dirs, binaries, sprint, secrets, redis, seat, smoke.

and, lower, the usage block:

    usage:
      nova-up --local [--root <dir>] [--dry-run] [--json]
      nova-up version
      nova-up help [<verb>]

exit 0. **Expected:** the usage names the verb the refusals name ("the verbs are up, version") and
that the example runs (`nova-up up -h`); a reader cannot tell that `up` and `--local` are one mode,
so the door the refusals send them to is absent from the door's own list. Grade: NEXT.

## 8. A short flag is echoed as a long one in its refusal — NEXT

**Command:**

    $U -v

**Printed** (stderr, exit 2):

    UP REFUSED: unknown flag --v; the flags of up are --dry-run, --json, --local, --root; run: nova-up up -h

**Expected:** the refusal quotes the token the reader typed (`-v`), or says short flags are not
supported; `--v` was never typed and is not a name to search the help for. Grade: NEXT.

## 9. The dry run plans `ok` for a root it cannot make — NEXT

**Command:**

    $U up --local --dry-run --root /proc/nova-nope

**Printed:**

    UP OK root=/proc/nova-nope steps=8 changes=6 applied=0 dry_run=true
    UP NOTE dry run: nothing written; run it without --dry-run to apply
    UP platform ok linux: loops are systemd user units
    UP dirs create /proc/nova-nope: . stores keys logs smoke

exit 0. **Expected:** the plan can see the parent is not writable (the real run exits 1 with
`step dirs: mkdir /proc/nova-nope: no such file or directory`); a dry run that says `UP OK` and
`changes=6` for a root it cannot create sends the reader past the one thing the plan is for. Grade:
NEXT.

## 10. The tool's page has no first run, and `docs/CLI.md` has no `nova-up` section — NEXT

**Command:**

    grep -n '^## nova-up' docs/CLI.md

**Printed:** no lines (exit 1); the nearest section the file has is `1924:## nova-update`.

and `docs/SPEC-UP.md` (the tool's page under `docs/`) contains no `### First run`. **Expected:** the
command reference's section for the tool is the first run, with the transcript a stranger prints;
here the only first runs are the banner's `example:` block and the `## First run` the tool's own
`cmd/nova-up/README.md` grew, and the page under `docs/` opens with the design. Grade: NEXT.

## Ratings

READ 6/10: the banner answers the three questions and every `example:` line runs, but `--root`'s
contract is not true in three ways (an empty value defaults, a `~` is literal, a file root is
planned `ok`) and the usage hides the `up` verb the refusals name.

USE 6/10: the plan-then-apply shape is trustworthy on a fresh root and the missing-program stop
writes nothing, but the write verb's one root can silently point somewhere else and the JSON a
caller would depend on is one opaque string.

urgent=2 next=8
