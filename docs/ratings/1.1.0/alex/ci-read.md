# nova-ci READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 044b5dfe9c1b
Score: 8/10
README: 7.5/10

## Reasons

Read cold: README.md top to bottom, then AGENTS.md and the usage guide the
README points to first, then the nova-ci section of docs/CLI.md, docs/SPEC-CI.md,
docs/TESTS.md's nova-ci sitting, cmd/nova-ci/ from main, and the internal
packages it leans on (slowtests, functional, cireceipt, scaffold, pkgselect),
the help text read as text. Nothing was run.

The tool reads as one idea carried through. The banner's first line is the
README's sentence for it (README.md:38, cmd/nova-ci/main.go:41); the
how-it-works paragraph names the nouns and where state lives inside five lines;
the first-run line needs nothing set up; the example block runs from the binary
alone, and firstrun_test.go:32-48 executes those lines, so the promise is
checked rather than asserted. Every file in cmd/nova-ci does one thing (main
holds dispatch and slowtests; local, newrule, receipt, version, load, timing
one verb or concern each), the internal packages each open with a comment
saying why the package exists, the comments are present tense and cite the spec
section each rule implements, and the refusal grammar is the family's: one
line, every problem at once, the next command at its end
(cmd/nova-ci/main.go:299-337). Exit codes are stated per verb in the banner,
which is the contract an AI scripts against. Weight is right: no repeated
hand-rolled helpers in what I read, and the only dead-looking thing is the
timing script below.

The README, judged before any code: 7.5. Confused first at README.md:38 —
the nova-ci row's setup column names local, new-rule, new-verb and github
receipt as bare words; a stranger cannot tell these are the tool's verbs, or
what any of them does, before opening the command reference. Bored first at
README.md:80-84 — the fourth consecutive paragraph of adoption advice restating
start small. Doubted a claim first at README.md:48 — "These are the Nova
Tools 1.0.0 commands" in a tree whose docs/RELEASE-NOTES-1.1.0.md exists and
whose rating directory says 1.1.0; a stranger cannot tell whether the page
describes the release or the checkout in hand.

What costs the 2: three doc places state the enforced verdict's exit as 2
where the code, the banner and the command reference say 1 — exit codes are
the contract, so drift there is the most expensive kind; the committed timing
script is a fourth face of the tool that no verb and no doc opens; and the
spec's budget section still calls an over-budget package a refusal against the
measured contract everywhere else. A 10 needs the exit codes to agree across
banner, command reference, TESTS.md and the spec; the timing script either a
verb or a doc-named script with a present-tense reason for its shape; and the
spec's invariant paragraph restated in the measured framing the code holds.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/TESTS.md:812 | the enforced verdict's exit is stated as 2 here ("only --enforce makes it exit 2") and at docs/SPEC-CI.md:282 and docs/SPEC-CI.md:309, where the code (internal/ci/slowtests/slowtests.go:610), the banner (cmd/nova-ci/main.go:127) and docs/CLI.md:1845 all say 1; an AI scripting CI around the exit codes meets three wrong answers | change the three 2s to 1s | S |
| 2 | cmd/nova-ci/timing.go:18 | a committed //go:build ignore script self-titled "nova-ci timing": no verb dispatches it, no doc under docs/ names it, its comment justifies its shape by a card's PATHS at line 21 that no reader can see, and its defaults carry the organisation's name as a pinned generality debt (internal/ci/testdata/generality/cmd/nova-ci.txt) | make it a timing verb, or name the script in the command reference's nova-ci section and reword its comment to a present-tense reason for its shape | M |
| 3 | docs/SPEC-CI.md:220 | the budget law's invariant paragraph says an over-budget package "is a refusal", against the measured framing the same page's own exit line (line 232), the banner and docs/CLI.md:1844-1846 hold (a measurement unless --enforce) | reword the invariant to the measured framing | S |
| 4 | cmd/nova-ci/main.go:49 | the banner's own-module line shows piping go test -json into slowtests with no word that the verb judges timing, not whether the tests passed; docs/CLI.md:1842-1844 carries that caveat where the docs show the pipe, the banner does not | add the one-clause caveat to the first-run line | S |
| 5 | docs/CLI.md:1828 | the nova-ci section is one of four without a "### First run" heading; 13 of the 17 tool sections open with one, and a stranger routed from README.md:38 lands on prose before the runnable block | open the section with a First run heading over the existing example block | S |
| 6 | cmd/nova-ci/main.go:75 | the usage lists slowtests twice (lines 54 and 75), two entries for one verb; a stranger scanning the verb index meets the same verb twice with different flag sets | one slowtests entry, the unit-tier budgets as its continuation | S |

## Good, keep

The banner's first run runs from the binary alone, and the tests run it:
firstrun_test.go:32-48 executes every example line, and the TESTS.md sitting is
held line for line (firstrun_test.go:87-126), so the help cannot drift from
the tool.

The why-comments: main.go:5-7 (a slow test must surface the moment it happens),
local.go:11-30 (one implementation, not a copy — the verb owns no selection
rule, no go test flag and no budget of its own).

Refusals name every problem at once and the next command: cmd/nova-ci/main.go:299-337
gathers the flag, file and stdin problems into one line with the remedy at its end.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| this repository's own CI in five output dialects | CHANGED | .github/workflows/ci.yml:757-760: the test step tees one -json stream and reads it with cmd/nova-ci slowtests, one finding grammar; a bare "nothing to test for this change" echo remains at ci.yml:529 |
| a leftover script | STILL THERE | cmd/nova-ci/timing.go:1-29: the //go:build ignore script still sits beside the verbs, justified by a card's PATHS at line 21; it is at least held by a test now (internal/ci/timing/timing_test.go:23-42) |
| a spec of verbs the tool does not have | FIXED | docs/SPEC-CI.md names only verbs nova-ci has: slowtests at line 206, functional at line 1167, local at line 2318, github receipt at line 2347 |
| the banner's pipeline example needs the test-exit caveat where it is used | STILL THERE | docs/CLI.md:1842-1844 carries the caveat where the docs show the pipe; the banner's own first-run line, cmd/nova-ci/main.go:49, still shows it with no caveat |
