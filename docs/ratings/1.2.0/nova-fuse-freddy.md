# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: Freddy (inception/mercury-2.5, opencode), a friend on a re-rate card
Build: 0389f76634ec6c515ae1c7763412393e1cbd59a0

READ: 8/10
USE: 9/10

Built and run on a Linux bench machine (vision), in a throwaway directory made for the trial; no live store, no server. Every verb ran: version, init, status, check, lockdown, quarantine, lift quarantine, and path. The refusals for missing --box and -h after verbs were tested. All output lines match the expected grammar from SPEC.md.

## Reasons

READ. The banner answers what the tool is in its first line, then how it works, a usage block for all eight verbs, the exit table, the --box rule, the -- rule, and an example block. Every verb answers `nova-fuse help <verb>` with its usage, flags, exit codes, effect, and a help line that points at `nova-fuse help <verb>` and notes that `-h` after a verb is refused at exit 2. That is the right shape.

What keeps READ at 8. There is no `--json` output anywhere; the banner says so explicitly but an AI worker might want machine-readable output for automation. The `-h` refusal after a verb is documented but the message says "run: nova-fuse help init" which is a page, not a verb-specific excerpt. The effect line on each verb help says "local write" or "inspection" but doesn't explicitly say "writes nothing" for inspection verbs.

USE. The six example lines from the banner ran as printed and each exited 0 except check on a quarantined surface which correctly exited 1. The safety rules hold when tested. Missing --box is refused at exit 2 with a clear remedy. `-h` after a verb is refused at exit 2 as designed. `--dry-run` on init, lockdown, quarantine, and lift quarantine prints the plan without writing. The box is verified by re-reading after writes.

What keeps USE at 9. There is no `--json` output for automation. The remedy for missing --box points at `nova-fuse help` (the full page) instead of `nova-fuse help init` (the verb-specific help). The `status` verb says "REPORTS; never gate on it" in the banner but the effect line says "inspection: reads, writes nothing" without emphasizing it's not a gate.

A five-line need: add `--json` output with a status word per line; make missing-flag remedies point at `nova-fuse help <verb>`; clarify in `status` help that it is not a gate; add a `--version` short flag for version; and add a `nova-fuse help all` to list all verb help lines in order.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | no `--json` output anywhere | The banner says "There is no --json" but automation needs machine-readable output. | Add `--json` to all verbs; print a JSON object with `verb`, `status`, `exit`, and `facts`. | M |
| 2 | `nova-fuse init` refusal for missing `--box` | Remedy points at `nova-fuse help` (full page) instead of `nova-fuse help init`. | Point every missing-flag remedy at `nova-fuse <verb> help`. | S |
| 3 | `nova-fuse status` help | Says "REPORTS; never gate on it" in banner but effect line doesn't clarify it's not a gate. | Add "this is not a gate, do not act on its exit" to effect line. | S |
| 4 | `nova-fuse version` | No `--version` short flag; must type full verb. | Accept `--version` in addition to `version` verb. | S |
| 5 | no `nova-fuse help all` | No quick way to list all verb help summaries in order. | Add `help all` that prints short help for each verb in order. | S |
| 6 | inspection verbs' effect lines | `check`, `status`, `path`, `version` say "inspection: reads, writes nothing" but could be clearer. | Add "this is not a gate" to `check` effect line explicitly. | S |

## Good, keep

The `--box` rule is enforced strictly: no default, no environment variable, refusal at exit 2 when missing. The `--` flag ends flag parsing so untrusted surfaces/reasons starting with `-` can be passed. The `--dry-run` flag on write verbs prints the plan without writing. The box is verified by re-reading after every write. Exit codes are consistent: 0 clear/done, 1 the verb ran and said no, 2 usage or cannot prove clear.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no v1.2.0 rating exists | NEW | first rating for nova-fuse in v1.2.0 |
