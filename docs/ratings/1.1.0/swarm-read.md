# nova-swarm READ rating, nova-tools 1.1.0

Rater: GLM 5.3 flash, a language model reading this tree cold for the first time
Build: 0561ae81e746
Score: 7/10
README: 8/10

## Reasons
The README row (README.md:30) says what the tool is for in one sentence and the banner's line 1 (cmd/nova-swarm/main.go:36) is that same sentence; the first command prints a card template with no store, no key and no setup, which is exactly the onboarding promise the standard makes. Reading on as a visitor: the first place I was confused is that same row (README.md:30): it says running cards needs "a worker description", but the row's first command prints only a card template, and what a worker description looks like appears nowhere until the worker verb far down the reference (docs/CLI.md:794) — a reader cannot see the second input the row demands from anything the row offers. The first place I was bored is the usage entry for member (docs/CLI.md:762): one verb whose explanation runs twenty-one lines of a single parenthetical before the next verb starts, with lint given eight more (docs/CLI.md:749); the verbs and their data stop being findable in a minute at exactly the two verbs that matter most. The first place I doubted a claim is the read-pr transcript (docs/CLI.md:849): the prompt the tool hands a worker embeds "[batch 1: 25 of 67 findings were duplicates of the owed list]" — measurement history addressed to the tool's own reviewers sits inside the instruction text the child is told to follow, and the child can neither verify nor use it.

The score's reasons. The contract side is the best I read in this repository: the spec opens with the failure each rule closes (docs/SPEC-SWARM.md:9), every rule names its red test (docs/SPEC-SWARM.md:284), the exit codes are a table quoted by every verb's -h (cmd/nova-swarm/verbhelp.go:8), effects are stated per verb (cmd/nova-swarm/verbhelp.go:40), and the refusals name every independent problem in one run with what each input wants, never guessing (cmd/nova-swarm/main.go:346). The dispatch is a clean minute-long read: one switch, the verbs listed in the order the usage prints them (cmd/nova-swarm/main.go:179). Against that: the core verb is one 1,291-line function a reader cannot hold in a head (cmd/nova-swarm/native.go:275); the comments cite incidents a stranger cannot resolve (145 ticket-shaped numbers in non-test code, cmd/nova-swarm/main.go:706 among them); the three biggest usage entries are walls rather than lines; and the worker prompts carry reviewer-facing evidence. It is a good tool and code I would trust — the machinery holds the deadline, the budget and the wall, and says so — but not yet one I would enjoy reading at its centre. A 10 needs: nativeRun decomposed into named stages; comments citing rules and sections instead of incident ids; the member, lint and disk-guard usage entries one line each with the rest behind -h; instruction-only prompt text; and a --json rendering beside the line grammar, which the usage block lacks entirely (cmd/nova-swarm/main.go:46).

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-swarm/native.go:275 | nativeRun is 1,291 lines: staging, wall, launch, budget watch, verdict and sweep in one function, so no reader can name its stages or land a fix without holding all of it | split into stage functions the comments already name (stage, fence, launch, watch, verdict, publish), each testable alone | L |
| 2 | cmd/nova-swarm/main.go:706 | comments cite incidents a cold reader cannot resolve: CARD-8349 here, 13d at main.go:687, js-under-20-bytes at main.go:924, tool ledger X2 and X9 elsewhere; 145 ticket-shaped numbers stand in non-test code | cite the rule and the spec section the comment serves; keep the incident's lesson, drop its id | L |
| 3 | cmd/nova-swarm/main.go:84 | the disk-guard usage entry is one 1,280-character line, and member's (main.go:63) runs twenty-one lines: the banner stops being scannable at its own verbs | one line per verb in the banner; the long explanation already lives behind -h, so point there | M |
| 4 | pkg/swarm/templates.go:27 | the read-pr prompt embeds past-batch evidence ([batch 1: 25 of 67 findings were duplicates]) inside instructions the worker must follow; history addressed to reviewers is noise the child pays tokens for | keep the condition, move the measurement that motivated it to the spec, where the reviewer reads | M |
| 5 | cmd/nova-swarm/main.go:46 | no verb accepts --json: the usage block lists every flag and none is the family's rendering flag, so the tool stands outside the one-shape rule its siblings meet | build each verb's one result value and render it as lines or JSON from the same value | M |
| 6 | cmd/nova-swarm/main.go:590 | the legacy --auth and --config flags are still first-class, read, and documented as "the legacy shape" — a new reader learns a superseded credential path before the current one | state --worker as the one shape in the banner and keep --auth behind a deprecation note in -h only | S |
| 7 | docs/SPEC-SWARM.md:91 | the spec quotes the owner with a date and a ticket id (2026-10-03, 8:03 AM ET, nova-tools#5199) as the authority for a normative rule; a stranger reads a changelog, not a spec | state the rule, and let the failure-record table at the top carry the evidence | S |

## Good, keep
The spec's opening table — every rule beside the failure that made it (docs/SPEC-SWARM.md:9) — is the honest contract shape, keep it. The exit table and per-verb effect lines every -h carries (cmd/nova-swarm/verbhelp.go:8) are the family at its best. The refusal grammar that reports every independent problem in one run, with what each input wants and refusing to guess (cmd/nova-swarm/main.go:346), is worth copying to any tool.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a 1,195-line function | STILL THERE | awk over cmd/nova-swarm counts nativeRun at 1,291 lines from cmd/nova-swarm/native.go:275 |
| 142 ticket numbers in non-test code | STILL THERE | grep counts 145 in cmd/nova-swarm and pkg/swarm non-test files, e.g. cmd/nova-swarm/main.go:706 |
| a 1,900-character usage line | CHANGED | the longest usage line is now 1,280 characters, cmd/nova-swarm/main.go:84 |
| legacy paths still named | STILL THERE | the legacy --auth and --config flags remain read and documented, cmd/nova-swarm/main.go:590 |
| an intimidating, historically narrated interface | STILL THERE | incident ids in the comments (cmd/nova-swarm/main.go:706) and batch evidence in the prompt text (pkg/swarm/templates.go:27) |
