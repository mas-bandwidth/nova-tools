# nova-swarm dogfood — freddy (inception/mercury-2.5), 2026-10-07

Read as a stranger: only `nova-swarm -h`, `nova-swarm help`, `nova-swarm <verb> -h`, and
the tool's pages under `docs/` (`docs/SPEC-SWARM.md`, the `nova-swarm` section of
`docs/CLI.md`, and `docs/nova-swarm-quickstart.md`). Built from
`sprint/mechanical-2026-10-02` at `1f20fd745e059d5e465a825708f346fb1245a39a` and used as
`nova-swarm v1.2.0-rc1 linux/amd64 go1.27.1` on vision.
Every verb ran with its real flags against scratch directories; the refusals ran too.
A member loop was not run: the card forbids starting one, and `member` needs a sprint server.

## Findings

### 1. `step` prints program name as one token `nova-swarmstep` — NEXT

Command:

    nova-swarm step --card /tmp/nope --dir . --dry-run

Printed (exit 2):

    nova-swarmstep REFUSED: --card wants a readable card file: open /tmp/nope: no such file or directory; run: nova-swarm help step

Expected: `nova-swarm step REFUSED: ...`, with a space between tool name and verb.
Grade: NEXT (a grammar break in the printed identity).

### 2. Missing input files refused with no remedy — URGENT

Commands (each exit 2):

    nova-swarm lint --card /tmp/nope.md
    nova-swarm lint: --card wants a readable file of the card text: open /tmp/nope.md: no such file or directory

Expected: the one refusal grammar ends `; run: <remedy>`, as missing-flag refusals do.
Grade: URGENT (a refusal with no remedy).

### 3. No verb accepts `--json` — NEXT

Command:

    nova-swarm doctor --json

Printed (exit 2):

    nova-swarm doctor REFUSED: unknown flag --json; the flags of doctor are --local, --path; run: nova-swarm help doctor

Expected: every verb accepts `--json` as the repository standard requires.
Grade: NEXT (a missing flag on every verb).

### 4. `slots release` prints success word on non-zero exit when lease held — URGENT

Command:

    nova-swarm slots release --store /tmp/slots2 --owner ada --label live1

Printed (exit 2 while lease held):

    SLOTS RELEASED owner=ada released=0 held=0 live=0
    SLOTS KEPT owner=ada live=0

Expected: the refusal leads, `SLOTS KEPT`, with no success word on a non-zero exit.
Grade: URGENT (wrong result).

### 5. `disk-guard --dry-run` under-reports freed bytes — NEXT

Command:

    nova-swarm disk-guard --dry-run --cache /tmp/cache --cache-max-gb 1 --disk-floor 0

Printed (exit 0):

    WOULD-ROTATE log /tmp/cache/loop.log freed=0 size=...
    DISK-GUARD OK freed=0 free=...

Expected: the dry run should show predicted freed bytes, not zero.
Grade: NEXT (a dry run that under-reports its own action).

### 6. `slots list` lacks `--max` bound — NEXT

Command:

    nova-swarm slots list --store /tmp/slots1 --max 5

Printed (exit 2):

    nova-swarm slots list REFUSED: unknown flag --max; the flags of slots list are --store; run: nova-swarm help slots

Expected: listings should accept `--max` with `MORE shown=<n> total=<n>`.
Grade: NEXT (a listing without its standard bound).

### 7. `slots write` verbs lack `--dry-run` — NEXT

Command:

    nova-swarm slots init --store /tmp/sd --owner me --capacity 2 --share 1 --dry-run

Printed (exit 2):

    nova-swarm slots init REFUSED: unknown flag --dry-run; run: nova-swarm help slots

Expected: write verbs should accept `--dry-run` to preview actions.
Grade: NEXT (a missing dry-run on four write verbs).

### 8. `install bogus` remedy points to different tool — NEXT

Command:

    nova-swarm install bogus

Printed (exit 2):

    nova-swarm install REFUSED: no unit kind bogus; the kinds are disk-guard|mirror-refresh; run: nova-sprint units --check

Expected: the remedy should be `nova-swarm install disk-guard|mirror-refresh`.
Grade: NEXT (a remedy that does not answer the refusal).

### 9. `slots take` refusal names weight but not kind names or remedy — NEXT

Command:

    nova-swarm slots take --store /tmp/slots1 --owner ada --n 1 --for 30m --kind schema --label schema1

Printed (exit 2):

    SLOTS REFUSED owner=ada want=4 held=0 share=1 free=2 holders=- remedy="nova-swarm slots list --store /tmp/slots1"

Expected: the refusal should name kind names and admission weights.
Grade: NEXT (unclear help and a remedy that does not fix).

### 10. `lint -h` prints malformed flag value — NEXT

Command:

    nova-swarm lint -h

Printed (exit 0):

      --card <lint <file>>  the card file to lint before any spend

Expected: `--card <file>`, a value placeholder not wrapped with verb usage.
Grade: NEXT (help that misstates a flag's value).

### 11. `docs/SPEC-SWARM.md` count and usage block stale — NEXT

The spec says eleven living verbs and omits `step`, `install`, `uninstall`, `slots run`
from its usage block. The binary prints fourteen top-level verbs plus `slots run`.

Expected: the count and usage block match the binary.
Grade: NEXT (stale normative docs).

### 12. Some verb helps omit `example:` or `effect:` — NEXT

`worker -h` has no `example:`; `slots init/take/release -h` have `example:` but no `effect:`;
`slots run -h` has neither.

Expected: every verb's help names its effect and first run.
Grade: NEXT (help that omits what the standard asks for).

### 13. `template --name result` prints deprecated contract form — NEXT

Command:

    nova-swarm template --name result

Printed (exit 0, line 1):

    RESULT <label> sha=<sha12>

Expected: `RESULT: <label> sha=<sha12>` with the colon.
Grade: NEXT (a shipped template that teaches the deprecated form).

### 14. `worker` help missing `example:` — NEXT

Command:

    nova-swarm worker -h

Printed: no `example:` block in the help.

Expected: every verb should have an `example:` block per the onboarding standard.
Grade: NEXT (help missing the example block).

## What held

The banner answers its three questions and names the exit table.
`version` and `version --version` print the one identity line.
`doctor` reads stamps and refuses unreadable ones with a remedy.
`verify` writes `RESULT.md.receipt` and exits on contract mismatch.
`slots init/take/list/release/run` work with scratch stores.
`disk-guard` trimmed and reported freed bytes on real runs.
`lint --rules` listed all card contract checks.

READ 5/10 — the banner and exit table are clear, but several verb helps omit
required `example:` or `effect:` blocks, and normative docs are stale.

USE 5/10 — core verbs work with scratch stores but several refusals lack remedies,
`--json` is missing from all verbs, dry-run behavior is inconsistent, and the
program name has a grammar break.

urgent=2 next=12
