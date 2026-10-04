# nova-cairn READ rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 7.5/10
README: 8/10

## Reasons

The rated source is exactly 0c5803c2de406c1b0b2b0841f579c9bf73406b1c; the staged HEAD is that same commit, so nothing later is scored here. The README's sentence for the tool is "a session's words, kept durably as plain files you can come back to" (README.md:33), and the banner repeats it word for word (cmd/nova-cairn/main.go:47), which the onboarding standard demands.

nova-cairn is the easy half of a hard problem done plainly: four verbs, plain files, no lifecycle, one value rendered as lines or JSON, refusals that name the whole remedy, writes that are fsync-durable through internal/atomicfile, and a first-run transcript a test actually executes (cmd/nova-cairn/firstrun_test.go:118). I would reach for it, and I would enjoy working in cmd/nova-cairn/. The reasons it is not a 10 are below.

First confusion: the banner's how text names one store shape only — "append keeps an entry's exact words in entries/<session>/<entry>.json" (cmd/nova-cairn/main.go:50) — while the same tool deliberately reads a second shape, one markdown file per session directly under the store, with no entries and no log, which the command reference spends a long paragraph on (docs/CLI.md:2115). A cold reader of `nova-cairn help` never learns the second shape exists.

First boredom: the same historical hurt is told twice, at length, in the voice of a war story rather than a rule: internal/cairn/cairn.go:158 and internal/cairn/benchstore_test.go:4. The rule it defends is real; the retelling is weight.

First doubt: the banner promises "The same entry id with the same words is a duplicate; with other words a conflict (exit 1)" (cmd/nova-cairn/main.go:51), and the spec says the same id with different prose is "never an overwrite" (docs/SPEC-CAIRN.md:80). The code reads the entry, compares, then writes with no lock (internal/cairn/cairn.go:500), so two writers that both see no file both rename over it and the loser's words are gone.

A 10 would: keep the conflict promise true under two writers; hold one store shape per file; state its rules in the present tense without ticket numbers or dates; give the command reference the `### First run` opening the other tools have; and stop reading the store twice to print one index.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:500 | The conflict check reads the entry file, compares prose, then writes, with no lock: two writers can both see no file and both rename over it, so a same-id concurrent append loses the other's words and the promise that different prose never overwrites is false | Publish the first entry with atomicfile.NoReplace and, when the path already exists, re-read and compare, so the loser reports duplicate or conflict | M |
| 2 | internal/cairn/cairn.go:154 | One 737-line file holds both store shapes: the nested sessions/ entries/ log.jsonl writes and the flat one-file-per-session writes, the tangle an earlier rating named | Move benchFile, benchHeadingRe, benchHeading, benchSection and appendBench to their own file, leaving cairn.go the nested store | M |
| 3 | internal/cairn/cairn.go:158 | Comments and test headers narrate the tool's history in the past tense with dates and ticket numbers, against the standard's present-tense rule and its ban on tickets and dates | State the rule the code keeps in the present tense and drop the ticket numbers and dates | M |
| 4 | docs/CLI.md:2088 | The tool's command-reference section opens with prose and a command block, not the `### First run` heading the onboarding standard requires of every tool's section | Add the heading and move the first-run transcript under it, as the neighbouring sections do | S |
| 5 | cmd/nova-cairn/main.go:216 | The index verb reads every entry file twice: once in cairn.Index and again inside cairn.Coverage, which calls Index to count | Return the coverage from the Index call, or count sessions without a second full read | S |
| 6 | internal/cairn/cairn.go:278 | appendBytes and appendLine hand-roll the same open-append-sync-close sequence, and fsyncDir repeats atomicfile's directory sync | Keep one append helper used by both, and rely on the shared file helper for the directory sync | S |
| 7 | docs/USAGE.md:1 | The guide the README calls start here has a section per tool but none for nova-cairn, so the cold reader's chosen guide never mentions the tool | Add the tool's why-and-limits entry, or say in the guide why it is left to the command reference | S |
| 8 | internal/cairn/cairn.go:562 | readEntry trusts the stored id and session fields, so an entry file named e.json whose body names another id is indexed and receipted under that other id | Refuse an entry file whose stored id or session does not match its path | S |

## Good, keep

The refusal that names the whole open remedy (internal/cairn/open_remedy.go:12), proven by a test that runs the printed command through a real shell (cmd/nova-cairn/open_remedy_functional_test.go:15): a fix is a paste, not a search.

The first-run test executes every documented line and compares values, not shapes (cmd/nova-cairn/firstrun_test.go:118), so the transcript cannot drift away from the tool.

Writes are atomic and fsync-durable through internal/atomicfile, and a crash between the entry file and its pointer line is healed on retry (internal/cairn/cairn.go:437).

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | CHANGED | The flat readers were split into internal/cairn/read_existing.go:1, but the flat writer and both shape locators still sit in internal/cairn/cairn.go:154 |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | internal/cairn/cairn.go:500 reads then writes with no lock; the banner at cmd/nova-cairn/main.go:51 still promises a conflict |
