# nova-memory READ and USE rating, nova-tools 1.2.0

Rater: Freddy, a sprint worker on a friend's re-rate card
Build: bba7b0ca85a6d2e4f3c7b8a9d0e1f2a3b4c5d6e7
READ: 8/10
USE: 8/10

This rates the release candidate at the head of sprint/mechanical-2026-10-02. Built and run on a Linux bench machine, in a scratch directory made for the trial. No live store, no server.

## Reasons

READ. The banner answers the three questions clearly. Line 1 says what the tool does. The how-it-works section names the memory index and says nothing is written. The example block builds a corpus and runs queries. Every required flag explains why it is required. Every verb's -h has its synopsis, worked example, effect line, and flags. Exit codes are spelled per verb. An AI can run every verb from the help alone.

What keeps READ at 8. The CAL rule at line 54 says a hit at or below CAL "is no better than noise", but the banner's own example shows its rank-1 hit at score=0.99 under CAL 1.46. docs/CLI.md:416 repeats the rule with quickstart answers all under CAL 4.05. A reader following the rule discards right answers. eval's gold.tsv format is not documented. boot says it "loads" memories but only checks files exist. verify -h says --root is repeatable but verify refuses two.

USE. The quickstart verb gives a natural first run. Missing flags are named with hints. Unknown flags list the verb's options. -floor 0 and -k 0 are refused with reasons. A miss is exit 0 with a SEARCH MISS line. verify bounds findings with totals. eval refuses impossible floors.

What keeps USE at 8. The CAL rule breaks trust. Snippets stop at 120 bytes in text and JSON. boot refuses at the first missing pin entry and names only that one. verify's coverage passes on mentions, not links. text-mode eval MISS escapes prints query=zebra\x20stripes. Missing-flag refusals point to the whole banner instead of the verb's -h.

A 10 would fix the CAL rule to match the examples or replace it with a calibration a right hit clears. Document gold.tsv format. Say boot validates files without loading content. Show whole passages with --snippet 0. Report every bad pin entry in one run. Require links for coverage. Quote escaped fields. Point missing-flag refusals at the verb's -h.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:54 | The banner says a hit at or below CAL is no better than noise, but shows its own example's rank-1 hit at score=0.99 under CAL 1.46. docs/CLI.md:416 has the same problem. | Calibrate CAL so a right short-query hit clears it, or document it as an upper bound for long probes and fix the example. | M |
| 2 | `nova-memory eval -h` | gold.tsv format is not documented; the banner only shows `<gold.tsv>`. | Add: query, tab, expected path relative to --root (comma-separated), one row per line, with an example in setup. | S |
| 3 | cmd/nova-memory/main.go:132 | boot says it "loads" memories but only checks files exist and prints `BOOT OK files=2 bytes=338`. | Say boot validates each pinned file exists, is regular, and is inside --root; loading is the caller's. | S |
| 4 | internal/memindex/channels.go:321 | Snippets stop at 120 bytes in text and JSON, cutting answers mid-sentence. | Add `--snippet <n>` with 0 meaning the whole paragraph. | S |
| 5 | cmd/nova-memory/boot.go:123 | With multiple missing pin entries, only the first is named. | Collect and report all bad entries in one refusal. | S |
| 6 | internal/memindex/verify.go:267 | Coverage passes on any text mention, not a link; the help says "named in some file" which suggests links. | Require markdown or wikilinks for coverage matches. | S |
| 7 | cmd/nova-memory/main.go:376 | verify -h lists --root as repeatable but verify refuses two roots. | Give verify its own --root flag text without "repeatable". | S |
| 8 | cmd/nova-memory/main.go:143 | Every -h repeats the full exit-code paragraph; the first sentence is hard to parse. | Print only the verb's own exit codes in its -h. | S |
| 9 | cmd/nova-memory/main.go:252 | version -h and boot -h mention "the index lives in memory" for verbs that build no index. | Print the index clause only for verbs that build one. | S |
| 10 | cmd/nova-memory/eval.go:97 | Text-mode eval MISS prints `query=zebra\x20stripes` with escapes visible. | Quote the field like the SEARCH line does. | S |
| 11 | cmd/nova-memory/search.go:90 | Missing-flag refusals point to `nova-memory help` (the banner), not `nova-memory search -h`. | Point every missing-flag refusal at the verb's -h. | S |

## Good, keep

The index is built in memory per run and nothing is written; every -h says so. Every required flag explains why it is required. Missing flags are named with hints in one run. Unknown flags list the verb's options. quickstart prints commands with outputs and explains its choices. Every receipt carries class, root, and file:line. verify bounds findings per kind with totals. eval refuses impossible floors.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format not documented | STILL THERE | `nova-memory eval -h` shows only `<gold.tsv>` |
| boot's purpose unclear | STILL THERE | cmd/nova-memory/main.go:132 says boot "loads" memories |
| snippet cuts answers | STILL THERE | 120 bytes limit in channels.go:321 |
| unknown-flag refusal missing offending flag | FIXED | `search --bogus` names `--bogus` and points at `search -h` |
| main.go monolith | CHANGED | main.go is 588 lines; verbs are in separate files |
