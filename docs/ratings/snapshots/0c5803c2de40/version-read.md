# nova-version READ rating, current baseline 0c5803c2de40

Rater: GLM, the model running this card
Build: 0c5803c2de40
Score: 8/10
README: 8/10

## Reasons
The sentence a newcomer meets first is the table row's "which version of each tool is installed, recorded and compared" (README.md:39), and it is the banner's own first line (internal/update/versiontool.go:21), so the README and the tool say one thing. Reading cold, the first place I was confused: that row's first command reads a manifest checked into the sources (README.md:39), which exists only in a checkout (README.md:56 says so), while the tool's own example block writes one with the binary alone (cmd/nova-version/main.go:8) — two first-run doors for one tool. The first place I was bored: four paragraphs keeping snapshot's two shapes apart (docs/CLI.md:1676) before the help itself prints both usage lines side by side (internal/update/versiontool.go:58). The first claim I doubted: "the adopted 16" (docs/CLI.md:1644), a fleet's fixed count stated as the reader's expectation, when the manifest I had been handed held one tool and the code counts whatever the manifest names (internal/update/snapverb.go:297).
Why 8 and not more: the entry point, verbs and data are found in a minute (cmd/nova-version/main.go:22 into internal/update/versiontool.go:29), each file does one thing, names are a stranger's words (ladder, recordedVersion, mixed stamps), every bound carries its measured why beside its rejected alternative, refusals name every problem at once with the next command, and the tests execute the documents. The costs: the package's largest file opens with the sibling binary's whole verb dispatch (internal/update/cli.go:287); a second line renderer stands beside the skeleton's, its merge note written but not taken (internal/update/out.go:17); the help states a default the --file shape does not hold (internal/update/snapverb.go:141); the state file's reader is about 290 lines of hand-walked JSON (internal/update/snapshot.go:53); and fixed fleet counts sit in the general reference (docs/CLI.md:1644). A 10 needs: this tool's surface readable without wading through the sibling's dispatch, one renderer for every verb, a default that is true in both shapes, and no fleet's numbers where a stranger's manifest is the truth.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/update/cli.go:287 | a cold reader of this tool opens the package's largest file and meets the sibling binary's whole verb dispatch first; this tool's route is one early return at internal/update/cli.go:291 | move the sibling's dispatch to its own file so this tool's reader lands on its own verbs first | M |
| 2 | internal/update/snapshot.go:53 | about 290 lines of hand-walked JSON guard the state file; the reason stands beside it (duplicate keys, case folding, depth), yet it is the deepest machinery a version report carries | lift the walk into the small reading package the delivery tests own, or shrink it to the refusals its two readers make | M |
| 3 | internal/update/out.go:17 | a second line renderer beside the skeleton's, kept because items cannot carry a prose tail yet; the note says the merge should happen and it has not | carry prose tails in the skeleton's items and delete emit, capped and text | M |
| 4 | internal/update/snapverb.go:141 | the help states a thirty-second default for --timeout (internal/update/versiontool.go:65), but the --file shape probes at five seconds unless the flag is given; the stated default is not true in one of the two shapes | state the shape's real default in the flag description, or give each shape its own flag block | S |
| 5 | docs/CLI.md:1644 | the general reference explains the count with a fleet's fixed numbers ("the adopted 16", "the 32" again at docs/CLI.md:1685); a reader whose manifest names one tool meets a number that is not theirs, and the prose goes stale when the count moves | write "the adopted tools the manifest names", as docs/SPEC-UPDATE.md:32 does | S |
| 6 | cmd/nova-version/main.go:3 | the contract pointer names one spec file, but report and send are governed by the other (docs/CLI.md:1619) | name both contracts in the package comment | S |

## Good, keep
The measured why beside every bound, with the rejected alternative written out: the cold-first-exec cost that sets the thirty-second deadline (internal/update/snapverb.go:21) and the same reasoning where the moved verb takes its bounds (internal/update/cli.go:690).
Tests that run the documents: the recorded transcript executed line for line with its normalisations declared (cmd/nova-version/firstrun_test.go:75), and every example line run as printed from the binary (cmd/nova-version/slow_test.go:36).
A refusal that names both shapes, every missing flag and the next command in one line (internal/update/versiontool.go:78).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| lives inside package update | STILL THERE | the whole tool is internal/update's VersionTool (internal/update/versiontool.go:14), stated as the design where two names share one reader (docs/SPEC-UPDATE.md:270) |
| a 345-line verb in cli.go | CHANGED | the moved verb spans internal/update/cli.go:715-895 with its help parser, differ and name folder extracted below it (internal/update/cli.go:910, internal/update/cli.go:945, internal/update/cli.go:1044) |
| a doc section explained by fleet numbers | STILL THERE | the command reference still explains the count by the fleet's adopted and installed totals (docs/CLI.md:1644 and docs/CLI.md:1685) |
| stale fixed-count prose | STILL THERE | the root map counts 16 command-line tools (AGENTS.md:161) while the command map lists 18 (cmd/AGENTS.md table) |
| split verb homes | CHANGED | each spec now names the other: docs/SPEC-VERSION.md:6 hands the manifest verbs over, docs/SPEC-UPDATE.md:32 states both snapshot shapes, and docs/CLI.md:1676 keeps them apart in one place |
