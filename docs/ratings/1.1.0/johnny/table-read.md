# nova-table READ rating, nova-tools 1.1.0

Rater: Grok
Build: 75c8e4680221
Score: 6.5/10
README: 7/10

## Reasons

The README line is README.md:25: tables whose cells are ordered sets, kept in Redis and drawn as text. That sentence is also the banner's first line, and the code does draw a table of ordered sets.

The first confusion is README.md:25. The row says the command writes a table and that the tool loads the functions it needs. It does not name the library, and it does not say what happens when the store already holds a different one.

The first boredom is AGENTS.md:39. The working rules start again there, after the same rules have already been stated above them.

The first doubt is README.md:47. It calls the commands in the table the 1.0.0 commands. This tree's guide, at docs/nova-table/README.md:26, requires Redis 7 or later and a function library the row never names. The tagged release is not opened here.

The matched sentence and the first-run transcript would be an 8. docs/CLI.md:2147 and docs/TESTS.md:834 show the same six commands, and cmd/nova-table/firstrun_test.go executes them. Refusals are one line and name the next command.

It is not an 8 because the essay contradicts itself. Root help says an empty render prints nothing (cmd/nova-table/main.go:62). The renderer always prints the header and the footer (internal/ntable/render.go:37). The command reference says that (docs/CLI.md:2220). The guide says the text is empty (docs/nova-table/README.md:318). The spec's key table puts the epoch and the revision on the definition hash (docs/SPEC-NOVA-TABLE.md:95). The code keeps the revision on its own key (internal/ntable/ntable.go:357) and the epoch domain on the identity hash (internal/ntable/ntable.go:361). The package comment lists neither (internal/ntable/ntable.go:38) and still says the footer label defaults to total (internal/ntable/ntable.go:24), while the constant is empty (internal/ntable/ntable.go:186).

It is not a 7 because an AI cannot run the example as printed, and the tool is not the shared shape this tree requires. The first-run line demands a Redis address, and the example lines name no address and no file-backed store (cmd/nova-table/help.go:132), against the store-free trial at AGENTS.md:64. --json exists only on batch and on member read. No verb offers --dry-run. The guide's verb list omits batch (docs/nova-table/README.md:262), and --idem records a string without deduplicating (cmd/nova-table/write.go:17), while a batch operation id does replay. The guide opens on design quotations (docs/nova-table/README.md:3) before it says what a cell is. The adoption guide the README opens first never names this tool (README.md:16, docs/USAGE.md:135).

A 10 says the job in three lines, keeps one key list and one empty-table sentence, runs the example with no separate server, and gives every verb --json and a write a --dry-run, from the shared skeleton. The column grammar is written once. The guide's verb list matches help.

The README is 7. The row at README.md:25 is one true sentence and a first command. It loses the rest because the first link never comes back to this tool, the page still frames the table as the 1.0.0 commands, and that first command needs a store the page does not start.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-table/help.go:132 | The first-run line demands a Redis address, and the example lines name no address and no file-backed store. There is no quickstart and no dry run, so the lines do not run as printed. | Give the example a file-backed store, or a dry run that builds a table without dialing. | L |
| 2 | cmd/nova-table/batch.go:43 | --json is only on batch and on member read. show, list, render and the writes have no JSON, and no verb offers --dry-run, so an AI cannot take one structure or preview a write. | Put --json and --dry-run on every verb from the shared skeleton, one value rendered twice. | L |
| 3 | cmd/nova-table/main.go:62 | Root help says an empty render prints nothing. The renderer always prints the header and the footer. The command reference says that. The guide says the text is empty. | Make the three sentences say what the renderer does. | S |
| 4 | docs/SPEC-NOVA-TABLE.md:95 | The key table says the definition hash holds the epoch and the revision. The revision lives on its own key and the epoch domain on the identity hash. The package comment lists neither and still says the footer label defaults to total, while the constant is empty. | Publish one key list, the code's, in the spec, the guide and the package comment, and say the footer label is blank unless set. | M |
| 5 | docs/nova-table/README.md:262 | The guide's verb list omits batch, the atomic write whose operation id replays. --idem on the other writes only records a string and the help says it does not deduplicate, so the retry story splits in two. | List batch beside the other verbs and say which identifier retries and which only records. | S |
| 6 | README.md:16 | The first link opens an adoption guide that never names this tool. The choosing section lists other tools and not this one. | Add a short choosing entry that points at the command reference and says the trial needs a store. | S |

## Good, keep

The banner's first line matches the README row word for word, at cmd/nova-table/help.go:125 and README.md:25.
A refusal is one line and names the next command, at cmd/nova-table/main.go:102, and a missing table names create, at internal/ntable/store.go:335.
The first-run transcript at docs/TESTS.md:834 is the same six lines as the example, and cmd/nova-table/firstrun_test.go executes them.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the guide and package doc open on dated quotations of a named person | STILL THERE | docs/nova-table/README.md:3 still opens on those quotations, and internal/ntable/ntable.go:2 still quotes the same lines in the package comment |
| the banner is a wall | CHANGED | cmd/nova-table/help.go:125 the opening is four lines that answer what it does, how it works and the first run; the long essay remains at cmd/nova-table/main.go:29 |
| personal design quotations and duplicate command plumbing | STILL THERE | docs/nova-table/README.md:5 keeps the quotations, and cmd/nova-table/help.go:23 is its own command list rather than the shared skeleton |
| the README scored 6.5 to 7, and 8.4 | CHANGED | README.md:25 this rating gives the README 7 |
