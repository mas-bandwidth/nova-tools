# nova-fuse USE rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 2c02b2aa2042
Score: 8.5/10

## Reasons

The banner's six-line `example:` block ran verbatim on a box made under the scratch directory — init, status, check, quarantine, check, lift — and every line printed the shape docs/CLI.md promises. `init` created the box and refused nothing; the first `check` answered `FUSE OK lockdown=clear (no surface named; no quarantine checked)`, saying out loud what it did not check.

Two real jobs, end to end. Job one, the quarantine dial: `quarantine` recorded the surface, `check` failed at exit 1 with a pasteable remedy (`nova-fuse lift quarantine --box './fuse-box.json' -- 'a-forum'`), `status` reported it, `lift` printed each stored entry and `verified`, and the next `check` was clear again. Job two, the hard fuse: `lockdown` blew it, `check` failed, `status` reported blown at exit 0 (the one inversion to remember), and `lift lockdown` refused forever at exit 2 even with extra arguments, before reading anything. Every write printed "verified by re-reading the box".

Four refusals provoked, and each is model quality. Missing `--box`: names what is missing, "refusing to guess", the next command, and a hint explaining the no-default rule. `quarantine` with nothing at all named `--box` AND the surface AND the reason in one run — every problem at once. An unknown flag named the offending flag and the flags there are. An unknown verb named the verb and listed all eight. A bad `--max -1` named the value and the fix. Each ends in a runnable next command.

`--json` and `--dry-run` are not offered: `--json` is refused as an unknown flag on `check`, `--dry-run` likewise on `quarantine`, and neither appears in `help`. The interface is the one-line-per-event grammar, which is deterministic and documented, but an AI that scripts the tool must parse text rather than read a value.

Where I had to guess: almost nowhere — the example ran as printed. The two things I learned by running rather than reading were the silent re-blow (re-quarantining a surface overwrites its time and reason with no announcement) and the `--` requirement for a dash-leading surface, which the help does state.

This is a tool I would depend on: the gate is exact, refusals recover in one turn, writes verify themselves, and the remedies are commands I can paste. It loses a point and a half for three things a 10 would not have: the silent re-blow losing an audit trail, and the absence of `--json` and `--dry-run`, each a divergence from the family standard an AI has to remember for this one binary.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse quarantine --box ./fuse-box.json a-forum "second reason"` | Re-blowing an already-quarantined surface silently overwrites its time and reason: the OK line does not say a prior quarantine existed, and `status` shows only the new reason. | When the surface is already quarantined, print the replaced stamp and reason in the OK line, or refuse and point at `lift` first. | S |
| 2 | `nova-fuse check --box ./fuse-box.json --json a-forum` | The family rule "every verb accepts `--json`" is refused here ("unknown flag --json; flags: --box"); an AI must parse the line grammar instead of reading a value. | Add `--json` as the second rendering of the one result value, or state in `help` why this binary omits it. | M |
| 3 | `nova-fuse quarantine --box ./fuse-box.json --dry-run x "r"` | Writes have no `--dry-run` ("unknown flag --dry-run"); the plan cannot be previewed, only verified after the fact by re-read. | Add `--dry-run` printing the planned write, or note in `help` that verification-by-reread stands in for it. | S |

## Good, keep
- The refusal grammar: every refusal names what was wrong, what the input wants, and a pasteable next command, and one run reports every independent problem at once.
- Verify-by-reread: every write prints "verified by re-reading the box", and the gate's exit codes are exact (0 clear, 1 blown, 2 cannot prove).
- The `FUSE FAILED` remedy is a runnable POSIX command that quotes the box and surface and puts `--` before a dash-leading surface.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a re-blow silently rewrites the fuse's time and reason (2026-10-02, 1aac13259) | STILL THERE | `nova-fuse quarantine --box ./fuse-box.json a-forum "second reason"` → `QUARANTINE OK a-forum since=…: second reason (…)` with no note that "first reason" was replaced; `nova-fuse status` then shows only "second reason". |
| the unknown-option refusal omits the offending flag | FIXED | `nova-fuse check --box ./fuse-box.json --nonsense` → `nova-fuse check: unknown flag --nonsense; flags: --box; run: nova-fuse help check`. |
