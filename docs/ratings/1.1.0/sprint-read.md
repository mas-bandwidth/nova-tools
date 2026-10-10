# nova-sprint READ rating, nova-tools 1.1.0

Rater: a flash-tier model, one rater of this wave
Build: 99cdd54ff15a
Score: 7/10
README: 7/10

## Reasons
Read cold as a visitor: the README top to bottom, then the nova-sprint section of docs/CLI.md and its spec, then cmd/nova-sprint from main.go and the internal packages it leans on, the help read as text, nothing run. The README line met first (README.md:27): "Coordinate work cards, readers and landing."

First place confused: README.md:27, whose setup cell says "Follow its local twin walkthrough" before the word twin is ever defined, and whose only runnable command is help, where every other row prints or writes something. First place bored: README.md:21-44, twenty rows whose cells are each a paragraph. First claim doubted: README.md:50, "These are the Nova Tools 1.0.0 commands" over a tree mid-way into 1.1.0; honest (1.0.0 is the last release) but the reader must leave the page to learn that.

The findable half is excellent. Banner line 1 is the README's sentence, how it works is five lines, and the first run is named in the first fifteen (cmd/nova-sprint/verbs.go:138). The twin (`--redis mem:<file>`) runs a whole card flow with no Redis and no git, and the transcript is executed line by line (docs/TESTS.md:979). The inbox walkthrough teaches judgments as copy-paste commands (cmd/nova-sprint/verbs.go:239); a glossary of the sprint's nouns rides in the help (cmd/nova-sprint/helpwords.go:39); every verb's -h carries its own exit codes; "What it does not prove" (docs/CLI.md:1157) states the limits of a landed card in one honest paragraph. The code I read after the docs says what the docs promised: refusals in one grammar, moves reported, one store, tick by hand on a twin.

The score sits at 7 because of what reading costs. The 2715-line spec tells its rules through 48 dated quotations of the owner; the command reference has dropped five verbs the binary has; the banner runs over 400 lines and every verb's -h prints it whole; comments quote the owner with dates and cite tool-ledger numbers a stranger cannot resolve. Every defect is findable and has an obvious fix, which keeps the score up; together they make a reader work hard for a tool whose onboarding core is the best of the set.

A 10 needs: the spec written as the rules of now, its history moved to a design log; the verb block of the reference generated from the verbs table so it cannot go stale; a verb's -h carrying only that verb's words; one glossary in docs that the help links to; every exit table (package doc, banner, per-verb, reference) derived from one source.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-SPRINT.md:32 | The contract quotes the owner's chat with dates throughout (48 mentions, e.g. 32, 49, 97, 118, 2196): rule and history share one register, and a cold reader cannot tell which quoted demand still holds | State each rule as it stands now; move every quotation with its date to a design log the spec links once | L |
| 2 | cmd/nova-sprint/verbs.go:155 | The banner runs over 400 lines, and every verb's -h prints it whole with only the exit line swapped (cmd/nova-sprint/verbhelp.go:71): `take -h` ships the whole sprint essay to reach five flags | Print a verb's -h as its usage, flags, its own words and its codes; keep the full essay under help alone | L |
| 3 | docs/CLI.md:1006 | The command reference's verb block has dropped five verbs the binary has (stream set, set, friend clean, handover, coordinator; cmd/nova-sprint/verbs.go:79, 85, 86, 99, 106): a stranger reading the reference never learns how the seat moves or how a stream's read tier is set | Generate the block from the verbs table the banner prints, as the transcripts are already generated | M |
| 4 | docs/SPEC-SPRINT.md:2187 | The spec's verb table rows are paragraph walls: the add row passes 4,500 characters, the land row spills across three cells (2209-2214), and one verb's shape cannot be scanned | One line per verb in the table; each verb's detail moves to a section of its own, as friend clean's algorithm already deserves | M |
| 5 | README.md:27 | The table row hands the cold reader jargon and nothing to run: its only command is help, "local twin walkthrough" is undefined here, and the tool's name links the 2715-line contract while every other row links its docs/CLI.md section | Point the name at the docs/CLI.md section and give the two-line twin flow (export, then init) as the row's first command | M |
| 6 | cmd/nova-sprint/friends.go:18 | The comment that explains the friends table opens with four dated quotations of the owner inside the code it documents | State the rule (the roster is nova-config's friend rows, the hold and the beat) and leave the history to the design log | M |
| 7 | docs/TERMINOLOGY.md:41 | The family glossary defines slot but none of the sprint's nouns (width, primary, epoch), and its slot gloss (how wide she wants to run) conflicts with nova-config's own field help, which says slots is not the sprint's width (pkg/config/kind.go:337); the sprint's real glossary lives only in the binary (cmd/nova-sprint/helpwords.go:39) | Move the words section into the terminology page and have the help link to it | M |
| 8 | docs/CLI.md:1099 | The command reference quotes the owner's dated sentence as the reason the balance poll exists; a reference should say what run does, not who asked for it | Drop the attribution; the section already states what the poll does | S |
| 9 | cmd/nova-sprint/verbs.go:178 | The ETA sentence runs about 120 words with three nested parentheses inside the banner, so the one line every verb prints is the hardest to read | Split it: one sentence for the estimate, one for held=N, one for the stopped machine's forms | S |
| 10 | cmd/nova-sprint/verbhelp.go:23 | The banner's exit line is thinner than the tool's own package doc (cmd/nova-sprint/main.go:10) and the reference (docs/CLI.md:1155): it drops friend clean's exit 3, the missing friend rows and the dashboard's replaced-binary code, so `friend clean -h` prints a table that contradicts the docs | Derive the line from one exit table that the package doc, the banner and each -h all read | S |
| 11 | cmd/nova-sprint/main.go:443 | Comments cite tool-ledger numbers (P4 here; P7, X11 at cmd/nova-sprint/main.go:408; X2 at cmd/nova-sprint/verbs.go:446; P5, X9 at cmd/nova-sprint/verbhelp.go:22) that no reader of the tree can resolve | Cite the rule's name only, as the onboarding citations beside them already do | S |
| 12 | cmd/nova-sprint/main.go:412 | The run loop walks every word of every group verb hunting a --prefix flag no verb defines, to refuse it with a fixed paragraph (cmd/nova-sprint/main.go:429) while the flag seam already answers unknown flags with the nearest name | Fold the refusal into the flag seam and drop the hand-rolled scan | S |

## Good, keep
The twin first run: `--redis mem:<file>` plays a whole card flow with no Redis and no git, and its transcript is executed line by line (cmd/nova-sprint/twin.go:88, docs/TESTS.md:979).
The inbox walkthrough: every judgment printed with its copy-paste answer commands, and one line per routine decision (cmd/nova-sprint/verbs.go:239, 273).
The honesty: "What it does not prove" (docs/CLI.md:1157) and per-verb exit codes (cmd/nova-sprint/verbhelp.go:26).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 52 verbs | STILL THERE | 63 verbs now: cmd/nova-sprint/verbs.go:42 |
| owner quotations as comments | STILL THERE | cmd/nova-sprint/friends.go:18 |
| a 30-field app struct | STILL THERE | cmd/nova-sprint/main.go:55, the app holds over 40 fields |
| leftovers of removed features | STILL THERE | the --prefix scan and its fixed refusal: cmd/nova-sprint/main.go:412, 429 |
| the first contract buries the workflow under display details and history | STILL THERE | docs/SPEC-SPRINT.md:10, section 1 opens on the owner's table requests before the lifecycle at line 749 |
