# nova-bus READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 17b4f6ee35bb
Score: 8.5/10
README: 9/10

## Reasons
Read as an essay, this is a tool written by somebody who has watched readers fail and written the failure out at each place it happened. The README's row sentence ("notes between AIs, over a git repository", README.md:24) is also the banner's first line (cmd/nova-bus/main.go:61), so the two doors a cold reader opens say one thing. The command reference opens with a First run that walks a real sitting, the refusal lines a first sitting actually prints included, and names which lines are the tool's and which are git's (docs/CLI.md:429-474). The spec states each claim with the test that pins it, says plainly what is still O(m) rather than hiding it (docs/SPEC.md:4488-4517), and ends with what the tool deliberately does not do. The code keeps the house style almost everywhere: comments that say why, present tense, a refusal grammar that collects every problem in one run, and the entry point and ten verbs findable in a minute (cmd/nova-bus/main.go:345-389). The tests teach the contract, 272 of them in the package, and the one performance claim is proved by a test that counts parses instead of timing them (cmd/nova-bus/cursor_functional_test.go:39). The example bus ships a README of its own that teaches the whole form in twenty lines (cmd/nova-bus/testdata/example-bus/README.md:1).

Cold-reader stumbles, each named as the card asks. The first place I was confused is the README's bus row, whose first command cannot run until the setup forty lines below it makes the trial bus, with flags the row does not name (README.md:24). The first place I was bored is the spec's third restatement of the date-at-midnight argument, made as well two sections earlier (docs/SPEC.md:4421). The first claim I doubted is the try-one section naming the 1.0.0 commands and installing v1.0.0 binaries (README.md:48) in a tree whose release notes are at 1.1.0 (docs/RELEASE-NOTES-1.1.0.md:1).

What holds the score down is weight and drift, not writing. The one command file runs 3,823 lines holding every verb, and inboxListing alone is 467 of them (cmd/nova-bus/main.go:1618). The spec's check grammar names tokens the code no longer prints and misses the ones it does (docs/SPEC.md:2966). The stdout prefix registry holds a token nothing prints and lacks the one check prints (internal/bus/protocol.go:59). And no verb renders its result as JSON, where the family standard in the repository says every verb does. A 10 needs the verbs in files a reader can hold, the spec's grammar equal to the code, the small staleness pins refreshed, and either the family shape or one sentence saying why this tool stands off it.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC.md:2966 | the output grammar documents BUS WARN and BUS FAIL, the code prints BUS NOTE and BUS FAILED plus a BUS CHECK count line the grammar does not name, so a reader parsing by the spec misparses every check run | rewrite the check block of the grammar to the tokens the code prints, and add BUS MORE and BUS CHECK to it | S |
| 2 | cmd/nova-bus/main.go:1618 | inboxListing runs 467 lines: the listing, the bodies frame, the cursor advance and the repairs in one function, so a reader hunting one rule walks all of them | split it along the return's three parts, with the bodies frame its own function | M |
| 3 | cmd/nova-bus/main.go:61 | no verb takes a JSON rendering and the tool hand-rolls the dispatch and help the shared skeleton gives the other tools, so a consumer must parse line grammar where every other tool in the set offers the same value as JSON | render the result value behind a JSON flag on the listing verbs, or state in the banner why this tool stands off the family shape | L |
| 4 | internal/bus/protocol.go:59 | the stdout prefix registry lists BUS WARN, which nothing prints, and lacks BUS NOTE, which check prints on stdout, and the stream test never runs check, so the drift is unguarded | swap the entry to BUS NOTE and add a check run to the protocol-stream test | S |
| 5 | README.md:48 | the try-one section names the 1.0.0 commands and installs v1.0.0 binaries while the tree and its release notes are at 1.1.0, so a cold reader installs one version and reads another's docs | name the current release, or say in one line why the pin stands at 1.0.0 | S |
| 6 | docs/SPEC-BUS-DELIVERY.md:3 | the status line says the prepared delivery is not implemented, while prepare and send with a prepared artifact are shipped verbs named in the banner and the command reference | set the status line to what is implemented and name the part that is not | S |
| 7 | README.md:24 | the bus row's first command runs before the trial bus exists, and the setup it points to stands forty lines down | put the two setup lines in the row's own cell, or say after the setup below in the same cell | S |

## Good, keep
The First run section walks a real sitting with the refusals it actually prints, and the tolerance and refusal tables in the spec each say what the tool does and why, so a cold reader meets the tool's mind before its edge cases.
The O(new) claim is stated as a property, proved by a named test that counts parses instead of timing them, with the costs that stay O(m) named rather than hidden.
Every refusal names the next command to run, and a draft's refusals all print in one run, so recovery is one turn.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 467-line function | STILL THERE | cmd/nova-bus/main.go:1618-2084, inboxListing |
| 16-parameter calls | CHANGED | cmd/nova-bus/main.go:2171, the widest call now takes 13 |
| war-story comments | STILL THERE | cmd/nova-bus/main.go:4-9 opens with the lost-notes story |
| REFUSED at exit 1 | FIXED | cmd/nova-bus/draft.go:56 exits 2, and docs/SPEC.md:3364 says every draft refusal does |
| retired flags still declared | STILL THERE | cmd/nova-bus/draft.go:40, the retired draft flag is still declared |
| the prose promises more than bounded retries deliver | STILL THERE | docs/CLI.md:420 promises no rejected push ever reaches a person; docs/SPEC.md:3936 exits 1 out of attempts |
