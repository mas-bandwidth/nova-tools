# nova-memory READ and USE rating, nova-tools 1.2.0

Rater: sprint worker on a re-rate card
Build: d665016b9693
READ: 8/10
USE: 8/10

Built and run on a Linux bench machine, in a scratch directory made for the trial: the banner's two-file corpus, then a third note with frontmatter, a wikilink and a long paragraph, a dated log, a second root and a gold.tsv. No live store, no server.

## Reasons

READ. The banner is a good door. One line says what the tool is for, "how it works" says the index is built in memory per run and nothing is written, the setup block makes a corpus in four lines, and the example runs from it unchanged. Every required flag says why it is required, every verb's `-h` carries its synopsis, a worked example, an `effect:` line and its flags, and the exit codes are spelled per verb. An AI could run every verb from the help alone.

What keeps READ at 8. The banner example shows a rank-1 hit at score=0.99 under CAL 1.46, and docs/CLI.md repeats the rule that a hit at or below CAL "is no better than noise". A reader who follows the rule throws away the right answer. eval's `<gold.tsv>` format is not described in the banner or `eval -h`. boot is described as "the memories a session loads" when it reads nothing out. `verify -h` says `--root` is repeatable but verify refuses two. Every `-h` repeats the whole exit-code paragraph which opens with hard-to-parse text. `version -h` says "the index lives in memory for the run" for a verb that builds none.

USE. The first run is the banner's: setup, then `quickstart --root ./corpus` runs stats, one search and one check, printing each command line above its output. search, check (file and stdin), stats, verify (coverage, frontmatter, exempt, links and info), eval (pass and fail), boot (pass and refusal), multi-root, `--exclude`, `--` before a dash word and `--json` on every verb but version all did what the help says. `search --root ./corpus` with nothing else names missing flags with hints. An unknown flag names every flag the verb takes and points at `search -h`. `--floor 0` and `--k 0` are refused with the reason. A miss is exit 0 with a `SEARCH MISS` line. verify kept the frontmatter finding visible next to the wikilink one, and the counts carry the totals.

What keeps USE at 8. The CAL line called every right hit noise in every run. Snippets still stop at 120 bytes, in the JSON too. boot refuses at the first missing pin entry and names only that one. verify's coverage passes on a mention, not a link. Missing-flag refusals send the reader to the whole banner.

A 10 would make the CAL rule match what the tool's own examples show, describe gold.tsv in `eval -h`, say boot validates and prints no content, let a caller see a whole passage, report every bad pin entry in one run, make coverage require a link, and send every refusal after a verb to that verb's `-h`.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-memory/main.go:54 | The banner says a hit at or below CAL is no better than noise, then shows its own example's rank-1 hit at score=0.99 under CAL 1.46. | Calibrate the probe to the query's term count and length, or state that CAL is an upper bound for long probes. | M |
| 2 | `nova-memory eval -h` | The gold file's format is not described in the banner or `eval -h`. | Add one line describing the format with an example. | S |
| 3 | cmd/nova-memory/main.go:132 | boot is described as "the memories a session loads" when it reads nothing out. | Say boot checks every pinned file exists and counts them. | S |
| 4 | internal/memindex/channels.go:321 | Snippets stop at 120 bytes, in the JSON too. | Add `--snippet <n>` with 0 meaning the whole paragraph. | S |
| 5 | cmd/nova-memory/boot.go:123 | boot refuses at the first missing pin entry and names only that one. | Collect every bad entry and refuse once with all of them. | S |
| 6 | internal/memindex/verify.go:267 | verify's coverage passes on a mention, not a link. | Match a markdown link or wikilink to the file. | S |
| 7 | cmd/nova-memory/main.go:390 | `verify -h` says `--root` is repeatable but verify refuses two. | Give verify its own --root flag text without "repeatable". | S |
| 8 | cmd/nova-memory/main.go:144 | Every verb's `-h` repeats the whole exit-code paragraph with hard-to-parse text. | Print only the verb's own exit codes in its `-h`. | S |
| 9 | cmd/nova-memory/main.go:259 | `version -h` and `boot -h` say "(the index lives in memory for the run)"; neither builds an index. | Print the index clause only for verbs that build one. | S |
| 10 | cmd/nova-memory/retrieval.go:83 | The text MISS line prints escaped spaces. | Quote the field as the SEARCH line does. | S |
| 11 | cmd/nova-memory/main.go:229 | Missing-flag refusals send the reader to the whole banner. | Point every refusal after a verb at `nova-memory <verb> -h`. | S |

## Good, keep

The index is built in memory each run and nothing is written; every verb's `-h` says so on its `effect:` line. Every required flag is required for a stated reason, and a refusal names all the missing ones at once with a hint each. An unknown flag lists every flag the verb takes. quickstart prints each command above its output. Every receipt carries class, root and file:line. verify bounds findings per kind with the totals always printed, and eval refuses a floor that cannot fail.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| eval's file format and boot's purpose are not in the help | STILL THERE | `nova-memory eval -h` shows only `<gold.tsv>`; cmd/nova-memory/main.go:132 still says boot is what a session loads |
| the CAL rule calls the right hit noise | STILL THERE | the banner's own example: score=0.99 under CAL 1.46 |
| the snippet cuts the answer | STILL THERE | 120 bytes in text and JSON |
| inspection and verification verbs have no --json | FIXED | stats, verify, eval and boot each printed JSON |
| main.go is a monolith | CHANGED | main.go is ~588 lines; verbs live in separate files |
