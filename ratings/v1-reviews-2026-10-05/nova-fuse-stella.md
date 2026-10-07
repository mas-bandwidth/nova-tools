# nova-fuse review, 2026-10-05

Rater: inception/mercury-2.5
Build: 52046bd9a0eb
Verdict: GOOD WITH FIXES for an AI to use
Score: 7/10

## Reasons
The tool does one emergency job well: blowing fuses and checking them at the ingestion layer. It refuses to guess (`--box` is required everywhere), gives clear exit codes (0 clear, 1 blown, 2 could not run), and its refusals name remedies. First confusion: `-h` after a verb exits 2 with a refusal while other nova tools exit 0 with help; the docs say why (exit 0 means CLEAR) but a cold AI user would expect standard `-h` behavior. First doubted claim: the spec says "an unreadable box is treated as BLOWN" but quarantine refuses on unreadable boxes while lockdown does not—the reasoning is in the spec but the inconsistency could surprise a caller.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-fuse status -h` (cmd/nova-fuse/main.go:152-158) | `-h` after a verb is refused at exit 2; AI users expect help at exit 0 like other tools | accept `-h`/`--help` as a positional argument on any verb, print help at exit 0 | S |
| 2 | `nova-fuse check` output (cmd/nova-fuse/main.go:200-230) | No `--json` flag; AI users must parse free-form text to gate programmatically | add `--json` flag to `check` and `status` that outputs structured JSON with exit codes | S |

## Good, keep
The no-guessing rule (`--box` required, exit 2 when missing) is exactly right for an ingestion fuse. The temp-file + fsync + rename write pattern ensures crash safety. `lift lockdown` refusing forever before reading anything is a critical safety invariant.
