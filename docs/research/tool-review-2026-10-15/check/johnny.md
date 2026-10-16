# nova-check AI-usability review, 2026-10-15

Reviewer: z-ai/glm-5.3
Build: f6c35d67aaa1
Score: 8/10 (45/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-check quickstart --dir ./self` -> `QUICKSTART OK done=2 worst-exit=0 next=kernel,attest,floors,corpus (kernel wants a size budget, attest a manifest of what a full boot reads, floors a derived copy and its source, corpus a ledger of protected lines: nova-check help)` (first run from the help's own setup and example block, unaltered); the cost is the tangled --json paragraph quoted in blocker 2 |
| 2 | refusals | 5 | `nova-check attest` -> `ATTEST REFUSED: --home is required; it wants --home <dir> is your memory-home directory: the tree the manifest's paths are relative to, and the only place attest reads; refusing to guess; run: nova-check help` (a second line names --manifest the same way: every problem at once, each with the next command) |
| 3 | output | 3 | `nova-check links --dir ./notes` -> `LINKS FAILED dir=./notes files=1 links=1 excluded=0 broken=1` (every line of a failed run, this one included, goes to stderr and stdout is empty, blocker 3; --json does reach stdout on failure; the other two grammar warts are blockers 4 and 5) |
| 4 | dry-run | 4 | `nova-check spelling --file ./notes/notes.md --write --dry-run` -> `SPELLING OK files=1 misspellings=1 written=0 dry_run=true` (the file's hash did not change; dogfood record --dry-run also wrote nothing; convergence --dry-run could not be exercised, blocker 6) |
| 5 | idempotent | 1 | `nova-check dogfood record --tools ./bin --tool nova-check --verb links --by reviewer --ok --notes "probe receipt" --receipts ./receipts` (second identical run) -> `DOGFOOD RECORD OK tool=nova-check verb=links by=reviewer at=2026-10-15T15:36:07Z ok=yes issue=- file=receipts/20261015T153607Z-nova-check-links-reviewer-e9e9e205.json` (a second receipt file joined the first: no write takes an --op) |
| 6 | no prompts | 5 | `nova-check dogfood gate --tools ./bin --receipts ./receipts --allow-empty` -> `DOGFOOD GATE OK verbs=263 by-nonauthor=1 open-edges=0 unfiled=0 unmatched=0 require-all=no` (every verb probed answered in one run; none read stdin) |
| 7 | exit codes | 5 | `nova-check links --dir ./notes` -> `LINKS FAILED dir=./notes files=1 links=1 excluded=0 broken=1` (exit 1; passes exited 0 and refusals 2, exactly the help's one-line table) |
| 8 | one word | 4 | `nova-check spelling -h` -> `--path <value>  file or glob pattern to check (repeatable)` (a third word for the target beside --dir and --file; --fail-max also shadows --max) |
| 9 | push | 5 | `n/a: every verb answers in one run; no verb waits on or polls outside state` |
| 10 | no leaks | 5 | `nova-check version --json` -> `{"result":{"verb":"version","status":"ok","exit":0},"facts":{},"payload":"nova-check v1.2.26-0.20261015153006-f6c35d67aaa1 linux/amd64 go1.26.6"}` (no output line carried anything beyond names and values the caller gave) |
| 11 | true help | 4 | `nova-check spelling --file ./notes/notes.md --json` -> `{"result":{"verb":"spelling","status":"failed","exit":1},"facts":{...},"items":[{"kind":"misspelling","fields":{"file":"notes/notes.md","line":5,"column":46,"original":"Teh","replacement":"The"}}]}` (spelling does take --json, against the first clause of the help's --json paragraph) |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-check dogfood record --tools ./bin --tool nova-check --verb links --by reviewer --ok --notes "probe receipt" --receipts ./receipts` | the same command run twice appends twice: run 1 prints `file=receipts/20261015T153541Z-nova-check-links-reviewer-dd8e8d95.json`, a later identical run prints `file=receipts/20261015T153607Z-nova-check-links-reviewer-e9e9e205.json`, and both files sit in ./receipts, so a retry after a timeout doubles the record; no write verb takes an --op | hash the receipt over its content without the timestamp so the same record lands on one file (or take an --op id whose repeat returns the recorded line and writes nothing) | S |
| 2 | `nova-check help` | the --json paragraph opens `Every verb but dogfood ledger, dogfood record, convergence, spelling takes --json` and then says `spelling and convergence take it too`; the first clause is false (both verbs take --json and run it), so a reader of the first clause alone skips --json on two verbs that have it | one true sentence: every verb takes --json except dogfood ledger and dogfood record, which print typed lines | S |
| 3 | `nova-check links --dir ./notes > out.txt 2> err.txt` | a failed run puts every line, the count line too, on stderr: out.txt is 0 bytes and err.txt holds `LINKS FAILED dir=./notes files=1 links=1 excluded=0 broken=1` and the finding lines, so a caller reading stdout sees nothing at all; the help does not say which stream carries what (a defect the 2026-10-02 ratings already named, still there) | print the result lines, findings and totals, on stdout (refusals may stay on stderr), or state the stream split in help | S |
| 4 | `nova-check spelling --file ./notes/notes.md` | the finding line is a second grammar: `SPELLING FAILED notes/notes.md:5:46: Teh -> The`, while links prints `LINKS BROKEN file=notes.md line=3 target=./gone.md reason="does not exist"`; one parser cannot read both without a special case | print spelling's findings as k=v fields (file= line= column= original= replacement=) like every other verb's items | S |
| 5 | `nova-check dogfood ledger --receipts ./receipts --json` | the subverb prefix changes spelling with the status: this refusal prints `DOGFOOD-LEDGER REFUSED: unknown flag --json; ...` while success lines print `DOGFOOD RECORD OK` and `DOGFOOD GATE OK` unhyphenated; a line grammar keyed on the verb prefix splits on one of the two shapes | print the subverb prefix one way in both printers | S |
| 6 | `nova-check convergence --repo example/project --ledger ./notes/notes.md --receipts ./receipts --retired ./notes/notes.md --since 24h --dry-run` | --dry-run refuses at the service call before printing any plan: `CONVERGENCE REFUSED: gh pr list --state open: exit status 2: gh: REFUSED pr list: no GitHub CLI in a card; ...`, so a reader cannot see what --state would record without the forge (a defect the 2026-10-02 ratings already named, still there) | with --dry-run read the local sources only and print the plan with the forge streams marked absent | M |
| 7 | `nova-check help convergence` | the excerpt from the top help carries a truncated prose line styled as a usage line: `nova-check convergence --state <file> again once the source moves, or nova-check dogfood record the` — indented like a command, cut mid-sentence, not runnable (the same stray-fragment shape the 2026-10-02 ratings named in another verb's help, still there in this one) | drop continuation prose lines from the `from nova-check help:` excerpt, or print them as NOTE lines | S |
| 8 | `nova-check spelling -h` | the target is named three ways — `--dir`, `--file`, `--path` — where links settles for two, and `--path notes.md` repeats `--file notes.md`; a caller must choose between overlapping words for one thing | keep --dir and --file; fold --path's globs into --file | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-check frobnicate` | the unknown-verb refusal lists every verb but suggests no nearest match | add a did-you-mean to the refusal | S |
| 2 | `nova-check help frobnicate` | help for an unknown topic is a refusal at exit 2 rather than the help door at exit 0 | print the same verb list through the help door at exit 0 | S |
| 3 | `nova-check links -h` | `--fail-max` is a second word for `--max`, kept "for one release" (an old spelling the 2026-10-02 ratings already named, still accepted) | remove it once the release passes | S |
| 4 | `nova-check spelling --file ./notes/notes.md --write --dry-run` | the dry run prints `SPELLING OK ... misspellings=1 written=0 dry_run=true` and exits 0 while findings exist; what a dry run exits on findings is unstated in help (a behaviour the 2026-10-02 ratings already named, still there) | say in spelling -h what --write --dry-run exits when it finds misspellings, so a gate does not misread it | S |
| 5 | `nova-check hygiene --repo ./repo --base HEAD~1 --head HEAD --identity "..."` | the identity finding quotes the full author email from git history | print the email hashed or as initials; the finding carries the same weight with less identity data | S |

## Top 3 fixes
1. Make dogfood record safe to retry (blocker 1): hash the receipt over its content without the timestamp, or take an --op id, so one operation lands once — moves idempotent from 1 to 5.
2. Say the true --json rule in `nova-check help` (blocker 2) and drop the truncated excerpt line (blocker 7): help that reads true the first time — moves cold and true help.
3. One stream and one grammar for result lines (blockers 3, 4 and 5): findings and totals on stdout, k=v fields for spelling, one subverb spelling — moves output.

## Good, keep
- The refusal grammar: every missing flag names what it wants, one run reports every problem at once, and each line ends with the next command to run.
- The cold door: the bare command names help, help carries a runnable setup and example block that works as printed, and quickstart's OK line names the next commands to run.
- Bounded listings that keep their totals (`LINKS MORE kind=broken shown=5 total=20`), and exit codes that behave exactly as the help's one-line table states.
- Fixes the 2026-10-02 ratings asked for that held: one run now names every problem at once rather than only the first unknown flag, required flags are marked (required) in -h, and version takes --json as its -h says.

## What could not be tried
- convergence, the whole verb: it reads the forge through gh, which this card may not touch, and even --dry-run refuses at the gh call (blocker 6's line); judged from help and that refusal.
- floors and corpus: the help says the tool ships none of what they read (a numbered-floor door copy with its source, a protected-material ledger), and no such fixture was built in this run; judged from their -h.
- spelling --write for real: this card runs nothing that fixes files in place; only --write --dry-run was run, and the file's hash did not change.
