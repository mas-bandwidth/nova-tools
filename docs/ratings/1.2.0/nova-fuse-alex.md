# nova-fuse READ and USE rating, nova-tools 1.2.0

Rater: openai/gpt-6-luna via OpenCode (cold reader)
Build: 5025750dba23
READ: 7.5/10
USE: 7/10

Built from the recorded commit and exercised against disposable JSON boxes in a throwaway directory. No live store or server was used.

## Reasons

READ. The opening sentence matches the README tool row, and the banner explains the box, hard lockdown, soft quarantine, and that the caller's harness must run `check`. The six-command example is concrete, and `help <verb>` gives each leaf verb's effect and exit codes. However, `help` is more than 100 lines before its example, repeating exit and flag material available through per-verb help. The normative output samples disagree with observed failure tokens, status ordering has conflicting descriptions, and the README still presents 1.0.0 commands. The tool refuses `--json`, so callers must parse its custom line grammar.

USE. The real run created a box, checked a clear surface, quarantined it, observed exit 1 from `check`, dry-ran and performed a lift, then blew lockdown and confirmed the gate stayed closed. Writes are verified and dry-runs did not change the box. The exit contract is easy to act on. Use is held back by dry-run no-op lines that omit `dry_run=true`, a blown `check` result on stderr only, and a refusal that does not mention missing `--box` when an unknown flag is also present. A 10 needs the interface and spec findings below fixed and a machine-readable output.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2043-2054 | Normative failure samples say `FAIL`, but the binary prints `FAILED` for a blown check and a repeated init; callers matching the documented token miss real failures. | Make every normative sample use the exact `FAILED` token the binary emits. | S |
| 2 | docs/SPEC.md:2185 | Status says quarantines are listed in the box's own order, but a box inserted as `topic-one`, `zeta`, `alpha` prints `alpha`, `topic-one`, then `MORE`; the spec earlier says they sort. | State the alphabetical normalized order consistently. | S |
| 3 | docs/SPEC.md:2062; cmd/nova-fuse/main.go:75 | Every verb refuses `--json`; an AI must parse typed text and cannot use the shared machine-readable result contract. | Render the same result as JSON when `--json` is supplied. | M |
| 4 | docs/SPEC.md:2061; `lockdown --dry-run` / `quarantine --dry-run` on an already-blown target | The no-op `OK already=...` lines omit `dry_run=true`, although the spec requires it on every dry-run OK line. | Include `dry_run=true` on every dry-run success, including already-in-state paths. | S |
| 5 | `nova-fuse check --bogus` | With both unknown `--bogus` and required `--box` missing, the refusal reports only the unknown flag; the reader needs another invocation to discover the required input. | Report the missing required `--box` alongside independent parse errors. | S |
| 6 | cmd/nova-fuse/main.go:37-108 | The banner is over 100 lines and places repeated exit-code, flag and box-format prose before its example, despite detailed per-verb help. | Keep the three onboarding answers, verb summary and runnable example in the banner; move repeated detail to verb help. | M |
| 7 | cmd/nova-fuse/main.go:140-141; cmd/nova-fuse/help.go:108 | Unknown top-level verbs list `lift` and `help`, while unknown verbs under `help` list `lift quarantine` and `lift lockdown` and omit `help`; the available-verb answer changes by entry point. | Derive both refusal lists from one canonical verb list and show the same leaf commands. | S |
| 8 | README.md:86-91 | The trial section still says these are the 1.0.0 commands and installs the 1.0.0 release, although this rating targets 1.2.0. | Update the trial text and install tag to the release described by the README. | S |
| 9 | docs/SPEC.md:2065; `nova-fuse check --box <blown-box> <surface>` | A blown result is written only to stderr while a clear result is on stdout; a caller consuming stdout sees no result line for the common denied case. | Send both check outcomes through the result stream and preserve the exit code as the gate. | S |

## Good, keep

- The box has no guessed path or environment fallback; a missing or unreadable box cannot be mistaken for clear.
- `check` exits 0 only for the requested clear state, and the observed quarantine and lockdown failures exited 1.
- Writes are re-read and verified. The throwaway run confirmed `--dry-run` left the box unchanged before quarantine, lift, and lockdown writes.
- The quarantine refusal/remedy and lift output name the surface, and the hard lockdown cannot be mechanically lifted.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| Re-quarantining silently replaced the standing reason | FIXED | The repeated call says `already=quarantined`, preserves the first reason, and states that the new reason was not recorded. |
| Failure grammar says `FAIL` while the binary says `FAILED` | STILL THERE | docs/SPEC.md:2043-2054 versus the observed `FUSE FAILED` and `INIT FAILED` lines. |
| README trial version is stale | STILL THERE | README.md:86-91 still names 1.0.0. |
| The banner buries the runnable example | STILL THERE | The usage output exceeds 100 lines before the example at line 95 of cmd/nova-fuse/main.go. |
| No JSON rendering | STILL THERE | The banner says `There is no --json`; all verbs use typed text. |
| Dry-run on no-op writes always says `dry_run=true` | STILL THERE | Already-blown lockdown and already-quarantined dry-runs omit the token. |
| Unknown-flag refusal reports every independent problem | STILL THERE | `check --bogus` does not say that `--box` is required. |
| Status order is consistent with the spec | CHANGED | The output is sorted, but SPEC.md:2185 says the box's own order while 2074 says quarantines sort. |
