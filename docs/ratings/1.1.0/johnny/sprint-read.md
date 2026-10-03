# nova-sprint READ rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 6.5/10
README: 7/10

## Reasons

The README row at README.md:26 says what this tool is in one sentence, and that sentence is the banner's first line. The same cell says to follow a local twin walkthrough and does not show the commands or say what a twin is. That is the first confusion.

The first boredom is README.md:46. The page turns into a 1.0.0 install essay and a bus recipe before the three sprint lines at README.md:73. AGENTS.md is the generated standard of the whole tree, not a map of this tool. The first link the README offers, the usage guide, does not mention this tool at all.

The first doubted claim is README.md:48, which calls the commands on the page the 1.0.0 set. The sprint row already describes a server, a fleet, and a file-backed twin. An install pinned to that tag is not this tree.

The contract's first lines do the job the question asks: one store, cards in streams, a tick that deals and asks readers, decisions in an inbox. The code does that. internal/sprint/lifecycle.go keeps six states in one table, a test checks the pairs, and a model sits beside it. The command layer refuses in one line, names the next command, and suggests a near flag. The command reference's first-run block is a real card flow with no store server. The closing section says what a landing does not prove. That honesty is worth keeping.

The cost is weight and drift, not a missing purpose. cmd/nova-sprint is 29504 lines, the store binding is 35068, and the pure core is 21131. verbs.go is 2551 lines and is the registry, the parser, and the banner essay. The process struct in main.go holds 34 fields, from the clock to the landing queue. The command reference's usage fence lists 55 forms and omits six verbs the registry has, while several of those lines still spell the cap as --limit and the registry's name is --max. Help prints every syntax line and a long glossary before an example that is the seat's day on a live store, while the twin flow a newcomer can run is only named. Section 11 of the contract puts the rules for add, land, and queue into single cells too long to scan. Comments in 44 places quote a dated spoken request instead of stating the rule. The standard says a tool starts from the shared command skeleton; this command imports none of it. The move table marks accept as the coordinator's class, and the tick accepts a primary with two ok reads by itself. The opening still says four tables; the default frame draws friends and hides readers and merge.

A 10 needs the usage fence generated from the verb registry, the help example equal to the twin first run, accept's class matching the tick, verb rows short enough to scan, comments that state the rule, the process struct split by duty, and the shared skeleton. The purpose, the refusal line, and the six states are already at that bar.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:984 | The usage fence lists 55 forms and omits friend clean, stream set, set, dashboard, handover, and coordinator. The exit table at docs/CLI.md:1063 already names friend clean and dashboard. Several fence lines still spell the cap as --limit. An AI that copies the fence cannot hand over the seat. | Generate the fence from the verb registry, including each verb's flags, and drop the hand copy. | M |
| 2 | cmd/nova-sprint/verbs.go:142 | Help points at a heading, trying it without a Redis, and prints none of that flow. The example block at cmd/nova-sprint/verbs.go:222 is the seat's day: a live loop, a lint-clean brief, and land. The runnable twin flow lives only in the command reference. | Put the twin card flow in the example block, and move the glossary behind help words. | M |
| 3 | internal/sprint/lifecycle.go:57 | The one move table marks review to merging as the coordinator's class. The tick at internal/sprint/steps_tick.go:268 accepts a primary with two ok reads and queues it, and says accept is mechanical. A stranger who trusts the table waits for a verb the machine already runs. | Mark the tick's accept mechanical, and say in the same row when the coordinator's verb is the one that moves it. | S |
| 4 | cmd/nova-sprint/main.go:52 | One struct is the connection cache, the landing queue, the server lock, the inbox home, and the tick clock. A verb's behavior is not in one place, and a change to land touches the same type as help. | Split the process by duty: store, server, landing, and the long reads. | L |
| 5 | docs/SPEC-SPRINT.md:1220 | The add row, and the land and queue rows below it, are single cells of rules, sizes, and history. The lifecycle in section 3 is a table a stranger can scan. Section 11 is not. | Give each verb a short row and a subsection for the rules that do not fit a cell. | L |
| 6 | internal/sprint/schema.go:38 | The default frame draws work, friends, and fleet, and hides readers and merge, the two tables a card must pass. schema.go:12 and the contract opening still say four tables. Friends is drawn, not stored. | Say that in the opening and in where's help, and name --all as the way to see readers and merge. | S |

## Good, keep

The banner's first line is the README row's sentence, and the six states in internal/sprint/lifecycle.go are one table a test checks. The refusal line names the problem and the next command. The command reference's first-run block, and the section on what a landing does not prove, tell the truth about a twin.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 52 verbs and a 30-field process struct | STILL THERE | cmd/nova-sprint/verbs.go:39 registers 61 verb rows, and cmd/nova-sprint/main.go:52 holds 34 fields |
| dated spoken requests kept as comments | STILL THERE | internal/sprint/schema.go:28, and 44 sites under the command and the core |
| leftovers of removed features | STILL THERE | cmd/nova-sprint/verbs.go:440 keeps --limit as --max for one release, and cmd/nova-sprint/main.go:376 still refuses a prefix flag because there is no prefix |
| the first contract buries the workflow under display details and history | STILL THERE | docs/SPEC-SPRINT.md:12 opens on the view drawing and the cost column, and the lifecycle starts at docs/SPEC-SPRINT.md:358 |
| README read as 6.5 to 7, and as 8.4 | CHANGED | README.md:26 is still one sentence, and this read is 7/10 because the twin walk is named and not shown |
