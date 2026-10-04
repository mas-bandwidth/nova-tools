# nova-sprint READ rating, current baseline 0c5803c2de40

Rater: opencode on the model z-ai/glm-5.3-flash
Build: 0c5803c2de40
Score: 7.5/10
README: 8/10

## Reasons
Cold READ, nothing run: README.md top to bottom, then the nova-sprint entry
in docs/CLI.md and docs/SPEC-SPRINT.md, then cmd/nova-sprint from main.go
through the internals it leans on. Rated source: 0c5803c2de406c1b0b2b0841f579c9bf73406b1c,
the staged checkout's HEAD, read as a visitor arriving cold.

The README's line for the tool (README.md:26) is "a sprint of work cards,
dealt to a fleet of workers and read before they land", and the code does
that: a deal, reads, a merge, a landing, and the decisions the machine
cannot make going to the coordinator's inbox. First impressions on the way
in:
- Confused first at README.md:26: "Follow its local twin walkthrough" names
  no link and no place. The walkthrough turns out to be the First run
  section of the tool's entry in docs/CLI.md (docs/CLI.md:944), and "twin"
  arrives undefined, later to name two different things.
- Bored first at docs/CLI.md:984-1038: the verb reference is a wall of 55
  syntax lines; the day's shape (init, add, tick, take, finish, read, merge,
  where) is inside it but cannot be seen from it.
- Doubted first at README.md:48-50: the install section anchors the suite at
  v1.0.0 while the tree ships docs/RELEASE-NOTES-1.1.0.md, whose upgrade
  step reloads the store's function library; which release is current is
  guesswork.

What holds the score up: the lifecycle is one table (docs/SPEC-SPRINT.md:358-404)
mirrored by the TLA+ model and by internal/sprint/lifecycle.go:10-30, and the
store refuses any step whose plan leaves that table. But the table itself
disagrees with the spec row it mirrors: the code marks accept
coordinator-class (internal/sprint/lifecycle.go:57) while the spec says the
tick accepts mechanically (docs/SPEC-SPRINT.md:372) and the tick does
(internal/sprint/steps_tick.go:268) — the one place the mirror cracks.
Every refusal carries a
remedy in one grammar (cmd/nova-sprint/main.go:400-413); the store-free twin
first run (docs/CLI.md:944-981) needs no Redis, and
cmd/nova-sprint/firstrun_test.go:29 runs the published transcript line for
line, unnormalized; the contract's honesty section (docs/CLI.md:1065) states
what a landing records and what it does not prove. The internal core
(internal/sprint) reads as small types with comments that say why and cite
their section: a plan of units, one lifecycle table, one fence.

What keeps it from 9 and 10: a stranger must absorb 61 verbs and a 27-term
glossary (cmd/nova-sprint/helpwords.go:40-81) before the help reads
fluently; the command reference has drifted behind the binary; the contract
settles details with dated quotations instead of rules; and "twin" names two
unrelated things. A 10 needs: a stated day's path of the few verbs that
carry a normal day, in the banner; the contract leading with the state
machine, written as present-tense rules; the verb block generated from the
verb table, as the banner already is; one name per concept.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-sprint/verbs.go:39 | 61 verbs and a 27-term glossary (cmd/nova-sprint/helpwords.go:40-81) stand between a cold reader and a first day; the daily path (init, add, tick, take, finish, read, merge, where) is present but never named as the path | state "the day's verbs" as one short list in the banner, and let the rest live behind the group help the banner already names | L |
| 2 | docs/SPEC-SPRINT.md:25 | the contract settles details by dated quotation from its owner, 29 of them in this file, and the same voice sits in code comments (cmd/nova-sprint/friends.go:20, cmd/nova-sprint/reads.go:547, internal/sprint/round.go:35): a cold reader gets chat history where a rule should be | rewrite each quotation as the present-tense rule it decided, and keep no dated voice in the contract or the comments | L |
| 3 | internal/sprint/lifecycle.go:57 | the mirrored table marks accept coordinator-class, while the spec row it mirrors says the tick accepts mechanically (docs/SPEC-SPRINT.md:372) and the tick does (internal/sprint/steps_tick.go:268): a stranger who trusts the one lifecycle table waits for a verb the machine already runs, and the table the store enforces contradicts its spec | mark the move mechanical, with the row's cause naming when the coordinator's verb is the one that moves it | S |
| 4 | docs/CLI.md:984 | the verb block lists 55 verbs and the binary has 61: friend clean, stream set, set, dashboard, handover and coordinator are undocumented, and init's --owner flag (cmd/nova-sprint/verbs.go:40) is absent from the reference line | generate the block from the verb table, as the banner already does (cmd/nova-sprint/verbs.go:153-156) | M |
| 5 | internal/sprint/store/twin.go:18 | "twin" names two unrelated things: the mem store behind --redis mem:<file> (cmd/nova-sprint/twin.go:18) and the tick's read cache, admitted side by side in cmd/nova-sprint/main.go:59 and main.go:90-93, where a comment must say "it is not the mem twin" | keep twin for the store, and rename the tick's read cache to what it is: a warm read | M |
| 6 | cmd/nova-sprint/main.go:52 | the app struct is 34 fields of wiring, server control and test seams in one type (30 at the earlier read); run's state (serial, serving, landFailed) sits beside the clock and the screen | split the server's control and the land loop's state into their own small types | M |
| 7 | README.md:48 | the install section anchors the suite at v1.0.0 while docs/RELEASE-NOTES-1.1.0.md ships a store migration upgraders must run; the reader cannot tell which release is current | point the section at the newest release and name the function-library reload | S |
| 8 | cmd/nova-sprint/reads.go:33 | comments settle choices by citing "errata 3 amendment 6" (and an issue number at cmd/nova-sprint/reads.go:37), authorities a stranger cannot open | keep the rule and its reason as prose; drop the unreachable citation | S |
| 9 | cmd/nova-sprint/main.go:274 | a removed feature still costs three guards: the NOVA_SPRINT_PREFIX refusal here, the --prefix scan at cmd/nova-sprint/main.go:360-363, and the noPrefix text at cmd/nova-sprint/main.go:376 | keep one refusal line in the help and delete the other two | S |
| 10 | README.md:26 | "Follow its local twin walkthrough" names no link; the reader hunts for what is the First run section of the tool's CLI entry | link the First run section from the table's setup cell | S |

## Good, keep
The store-free twin first run with a transcript the tests execute line for
line (docs/CLI.md:944-981, cmd/nova-sprint/firstrun_test.go:29) is the
reason a cold reader can trust the rest. The lifecycle as one table mirrored
by the TLA+ model, with the store refusing any step that leaves it
(internal/sprint/lifecycle.go:10-30), is the reason the code can change. The
one refusal grammar with a remedy on every line, and the honesty section
(docs/CLI.md:1065), must not be lost.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| 52 verbs (read at 1aac13259) | STILL THERE | 61 verb rows in cmd/nova-sprint/verbs.go:39-101; docs/CLI.md:984-1038 lists 55 and trails the binary |
| a 30-field app struct | STILL THERE | cmd/nova-sprint/main.go:52-134 counts 34 fields |
| owner quotations as comments | STILL THERE | docs/SPEC-SPRINT.md:25-90 quotes dated owner chat 29 times; cmd/nova-sprint/friends.go:20 and internal/sprint/round.go:35 quote it in comments |
| leftovers of removed features | STILL THERE | the prefix feature is gone yet guarded in three places: cmd/nova-sprint/main.go:274-276, cmd/nova-sprint/main.go:360-363, cmd/nova-sprint/main.go:376 |
| the first contract buries the workflow under display details and history | CHANGED | the lifecycle is one table at docs/SPEC-SPRINT.md:358-404 and the runbook is split out (docs/SPRINT-COORDINATOR.md), but sections 1 and 2 still lead with table layout and cost detail |
| README read 6.5-7.0 by one rater and 8.4 by another | CHANGED | the task-first table (README.md:19-44) and the honest limits hold; the stale v1.0.0 anchor (README.md:48) and the unlinked walkthrough (README.md:26) hold it at 8 |
