# nova-fuse AI-usability review summary, 2026-10-15

Reviews: 4 (R1..R4, in byte order of file name)
Build: 15302296f439, 52ada6aff220, 453ce3a00131, 4540d7fd37ca
Score: 8.75/10 (range 7.5-9)

## Rubric
| # | criterion | median | range | the evidence that decided it |
|---|---|---|---|---|
| 1 | cold | 5 | 4-5 | `nova-fuse init --box ./fuse-box.json` -> `INIT OK box=./fuse-box.json: an empty box, no fuse blown (verified by re-reading the box)`; the six-line `example:` block runs as printed |
| 2 | refusals | 3.5 | 3-4 | `nova-fuse status --box ./no.json --max abc --foo bar` -> only `unknown flag --foo`; the missing `--box` and the bad `--max` are not named in the same run |
| 3 | output | 4 | 3-5 | `nova-fuse check --box ./q.json --json discord` parses as one JSON value; but a blown `FUSE FAILED` result goes to stderr only, so stdout capture sees empty at exit 1 |
| 4 | dry-run | 5 | 4-5 | `nova-fuse quarantine --box ./q.json --dry-run slack "w"` -> `QUARANTINE OK ... dry_run=true: nothing written`, and the box is unchanged |
| 5 | idempotent | 3 | 1-5 | no write verb takes `--op`; `nova-fuse lockdown --box ./q.json --op a "r"` -> `REFUSED: unknown flag --op` (R1 scored `n/a` =5, R3 scored 1) |
| 6 | no prompts | 5 | 5-5 | no interactive surface anywhere; every verb ran to completion with no input |
| 7 | exit codes | 5 | 4-5 | `check` is 0 clear / 1 blown / 2 unreadable; but `status` exits 2 on a missing box while the banner says "status, path: 0 only" |
| 8 | one word | 4.5 | 3-5 | the bare refusal lists `lift`; `help nosuch` lists `lift quarantine, lift lockdown`; the two verb lists disagree |
| 9 | push | 5 | 5-5 | local file-backed box with no daemon or waited-on state; `n/a: no store to poll or push` |
| 10 | no leaks | 5 | 5-5 | paths echo only caller-supplied data, escaped; no output line carried a secret |
| 11 | true help | 3.5 | 3-4 | help promises a dry-run line that "says dry_run=true", but a dry-run of an already-blown box prints `LOCKDOWN OK already=blown` without it; docs/SPEC.md still says `FUSE FAIL` while the binary prints `FAILED` |

## Consensus 10/10 blockers
| # | where | what an AI meets | fix | size | named by |
|---|---|---|---|---|---|
| 1 | `nova-fuse status --box ./no.json --max abc --foo bar` and `nova-fuse check --bogus` | only the first problem is named (`unknown flag --foo`/`--bogus`); the missing required `--box` and bad value `--max` are not reported in the same run | report every flag and positional problem in one refusal | S | R1,R2,R3,R4 |
| 2 | `nova-fuse lockdown|quarantine --box <box> --dry-run "..."` on an already-blown or already-quarantined box | prints `LOCKDOWN OK already=blown ...` (or `already=quarantined`) with no `dry_run=true`, the same text as a real write, contradicting the help's promise | carry `dry_run=true` and "nothing written" on the standing lines when `--dry-run` is set | S | R1,R2,R3,R4 |
| 3 | `nova-fuse check --box <locked-down box> <surface>` | the blown `FUSE FAILED` result goes to stderr; a caller capturing stdout sees empty at exit 1 | print the blown result on stdout too, keep exit 1, and state the stream contract on `help check` | S | R1,R4 |
| 4 | `ls` after any write | a 0-byte `<box>.json.lock` sidecar appears, named nowhere in help, and is left even when the verb refuses over a missing box | name `<box>.lock` in help for writes, and do not leave a lock when the verb refuses | S | R1,R3,R4 |
| 5 | `nova-fuse nosuchverb` vs `nova-fuse help nosuch` (and `nova-fuse help help`) | the verb lists disagree (`lift` vs `lift quarantine, lift lockdown`), and `help help` is refused as an unknown verb | one verb list feeding the dispatcher, the banner, every refusal and `help <verb>` | S | R3,R4 |
| 6 | `nova-fuse status` (no `--box`) | the REFUSED line is printed twice; the second copy has no hint | print the refusal once | S | R3 |
| 7 | `nova-fuse quarantine --box <box> <surface>` with a missing/blank reason or surface | the identical refusal "needs a surface and a reason" is printed for each case; it never names which positional is missing or blank | name the specific missing or empty positional (`<surface>`, `<reason>`) | S | R2 |
| 8 | `nova-fuse check --box <box> --json <surface>` | the JSON result carries no `remedy` while the text headline names the lift command | put the same next command in `result.remedy` | S | R3 |
| 9 | `nova-fuse check -h` and `nova-fuse status -h` | no mention of surface normalization/folding or recommended spelling | state that surfaces are folded and to use the stored spelling in check/status help | S | R1 |
| 10 | `nova-fuse init --box <existing>` and `nova-fuse init --box <dir>` | init exits 1 and suggests `status --box`, which exits 2 on a directory or a malformed box | make init idempotent on a valid box, or check the target is a regular file before suggesting status | S | R2 |
| 11 | `nova-fuse init --box ./broken-box.json` | `INIT FAILED ... read it with nova-fuse status --box './broken-box.json'`; that status exits 2 | say the file is not a box and to repair it by hand, not to run status | S | R3 |
| 12 | `nova-fuse lift lockdown --box <box> <surface>` | the refusal ends "Stop, and go talk with them now" with no `run:` command | add `run: nova-fuse init --box <a new path>` | S | R3 |
| 13 | `nova-fuse status --box ./missing-box.json` | exits 2 but the banner states "status, path: 0 only" | drop "0 only", or state status exits 2 when the box cannot be read | S | R3 |
| 14 | the grammar block in docs/SPEC.md | still writes `FUSE FAIL`, `LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL`, `INIT FAIL` while the binary emits `FAILED` | make the cited grammar the bytes the tool prints | M | R3 |
| 15 | `nova-fuse path --box 'FUSE OK lockdown=clear'` | echoes the value verbatim at exit 0, indistinguishable from the gate's clear line | prefix the echo with `PATH OK` so it cannot read as the gate | S | R3 |

## Top 3 fixes
1. Report every flag and positional problem in one refusal, so a missing `--box` and a bad value are named beside an unknown flag (raises refusals, the second-lowest criterion).
2. Carry `dry_run=true` and "nothing written" on every dry-run line, including an already-standing fuse, so a preview cannot be read as a write.
3. Emit the blown check result on stdout as well (keeping exit 1) and document the streams, so a caller capturing stdout is never empty on a denial.

## Disagreements
- Criterion 5 (idempotent): R1 scored 5 as `n/a` (no `--op` surface) while R3 scored 1 and R2/R4 scored 3. All four agree no write verb takes `--op`; the dispute is whether the absence is a defect or an `n/a`. Rerun: `nova-fuse lockdown --box ./q.json --op a "r"` -> `REFUSED: unknown flag --op`.
- Criterion 8 (one word): R1 scored 3 objecting to the two-word verb `lift quarantine`; R2/R4 scored 5. Rerun shows the verb lists disagree across surfaces (`lift` vs `lift quarantine, lift lockdown`), which is the real defect (blocker 5).
- Criterion 3 (output): R1 scored 5 (clean JSON); R3 scored 3 (`path` echo, stderr-only blown result). Rerun: `check --json` parses, but the blown result goes to stderr only (blocker 3 stands).
- R2's blocker that `check --json` drops `surface` and `state` no longer reproduces: on rerun the JSON `facts` for a quarantined surface include `"surface":"discord"` and `"checked":"surface"`. It is dropped from the blocker list.
- docs/SPEC.md's claim that there is no `--json` is already fixed (SPEC:129 states every verb takes it); only the `FAIL`/`FAILED` grammar divergence remains (blocker 14).

## Nice-to-haves
- Add the standard `--op` idempotency id to init, lockdown, quarantine and lift quarantine, returning the recorded result on retry (a new flag; idempotent is the lowest-scoring criterion).
- Move the README "Nova Tools 1.0.0 commands" sentence and install example to the shipped release.
- Rewrite the internal/fuse package doc from shouted numbered headings into ordinary present-tense prose.
- Trim the ~74-line banner; move the repeated per-verb exit-code prose to `help <verb>` and docs/CLI.md.
- Prefix the init missing-flag hint with the `NOTE` token (or fold it into the refusal line).
- Make the `STATUS MORE` remedy a runnable command: `nova-fuse status --box <path> --max <n>`.
- Name the `FUSE` result token in `help check`, since success and failure open with `FUSE`, not `CHECK`.
- Accept `help help` as an alias for `nova-fuse help` and add `effect:` and `Help:` lines to `help lift`.
- Print the `--` positional separator row only on verbs that take positional arguments.
