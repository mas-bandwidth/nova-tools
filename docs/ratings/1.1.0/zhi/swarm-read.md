# nova-swarm READ rating, nova-tools 1.1.0

Rater: a large language model, cold to this tool
Build: 99e4a903966c
Score: 7.5/10
README: 8/10

## Reasons

The README row says the tool runs one-task AI workers through a harness with a deadline and a token budget, and the help's first-run block says the examples print two templates and lint rules with no setup; the code does that. The first place I was confused: cmd/nova-swarm/main.go:34, the banner usage block, where the member line alone carries eleven parenthetical notes; a cold reader has to parse a paragraph before the verb's flags. The first place I was bored: internal/swarm/resultformat.go:12, the contract-history comments, which explain what changed across ticket numbers rather than what the current shape is. The first claim I doubted: the help says a card "is handed to the child byte for byte", but the wall and shims rewrite paths and commands before the child sees them; the claim is true only for the card file, and a reader has to hold that scope.

The tool is split into files by idea now: native.go for the single run, member.go for the sprint member loop, lint*.go for the lint rules, worker.go for worker descriptions. The package comments at cmd/nova-swarm/main.go:1 and internal/swarm/contract.go:1 say what each part owns, and the verbs are named what they do (template, lint, verify, native, member, doctor, profile, slots). The tests are extensive and pin the contract, the wall, and the lint rules.

What keeps it from a 10: internal/swarm/contract.go:82 slices `lines[2:]` with no length guard, so verify panics on a one-line result file; the legacy --auth path still threads through native.go and main.go; and the help's member and native usage lines are dense enough to need their own parser.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/swarm/contract.go:82 | `evidence := lines[2:]` has no length guard, so a one-line result file makes verify panic | refuse a result with fewer than two lines before slicing | S |
| 2 | cmd/nova-swarm/main.go:34 | the banner usage block is dense, with the member line carrying eleven parenthetical notes | move the notes into member's own help and keep the banner to one line per verb | M |
| 3 | cmd/nova-swarm/native.go:549 | the legacy --auth path still threads through native and is called legacy in its own notes | delete the legacy path now that worker descriptions carry secrets | M |

## Good, keep

The one-file-per-idea layout (native.go, member.go, lint*.go, worker.go) and the package comments that say what each owns. The lint rules that name the card drift and the remedy per rule. The doctor check that refuses a shadowed binary before a launch.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,195-line function | CHANGED | cmd/nova-swarm/main.go is 1041 lines total and the verbs live in their own files |
| 142 ticket numbers in non-test code | CHANGED | 16 nova-tools# mentions remain in non-test files, all in comments |
| a 1,900-character usage line | FIXED | longest usage line in cmd/nova-swarm/main.go is 453 characters |
| legacy paths still named | STILL THERE | cmd/nova-swarm/native.go:549 and authmode.go:12 name the legacy --auth path |
| an intimidating, historically narrated interface | CHANGED | cmd/nova-swarm/main.go:1 package comment is present-tense and one paragraph |
