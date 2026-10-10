# nova-redis READ rating, nova-tools 1.1.0

Rater: deepseek-v4.1-flash
Build: c28448a54d56
Score: 8/10
README: 7.5/10

## Reasons

Read cold, running nothing. The README row (README.md:28) says the tool's
purpose in one sentence, and the banner's first line (cmd/nova-redis/main.go:113)
is that sentence word for word, so the family rule holds. The section in
docs/CLI.md:2118-2178 is a dense but honest reference: the verb table opens it,
the login flags are explained once, the fn and acl grammars are printed whole,
and the exit table is repeated where a reader needs it. From the spec
(docs/SPEC-REDIS.md) the code reads as one entry point (main.go:109), one file
per concern — serve.go, fn.go, acl.go, report.go — and the two internal
neighbours that matter (pkg/redisconn, pkg/redisfn) name their own
contracts well. Four verbs a stranger can try with the dry run, and the scratch
keys carry an owner and a TTL by construction (main.go:507-523).

The writing is mostly excellent. The comments say why, in present tense, and
each names the hurt its rule answers: the serve config's no-eviction comment
(serve.go:224-225), the lost-reply comment on unconfirmed (main.go:437-443),
the classifier's note on a refused HELLO (open.go:58-60). The tests teach the
contract rather than mirror the code: the first-run transcript is executed line
by line (cmd/nova-redis/firstrun_test.go:25), the banner's example block is run
against a miniredis fake (cmd/nova-redis/examples_test.go:19), and the status
grammar is pinned verb by verb (cmd/nova-redis/status_grammar_test.go:32).

What costs the score. The README row (README.md:28) still calls the tool a
scratch-value store and never names the ACL verbs or the function-library
verbs, so a reader whose problem is "which user may read which key" or "put the
shared Lua on the store" cannot find the tool from the README; the spec
(docs/SPEC-REDIS.md:15-25) does not list `acl render|check|apply` in its own
verb block either, although the code carries them (main.go:127-129). The help
text is where the ticket numbers survive: the package doc is clean, but
cmd/nova-redis/serve_test.go:4, spill_test.go:4 and others still open with
issue ids, and the spec's own demanded proof of persistence
(docs/SPEC-REDIS.md:121) points at a test that opens with an unconditional
`t.Skip` (serve_functional_test.go:29), so a promised check never runs. The
section in docs/CLI.md has no `### First run` block although the repo's own
onboarding standard and the executed transcript in docs/TESTS.md:865 both
exist, and the banner's example block shows no acl or fn line, so a reader
whose job is the store's users or its library sees no first command. A 10
needs the README row and the section to say what the tool is for in three lines
including the ACL and function work, the spec's verbs brought level with the
code, the skipped persistence proof made real or declared, and the CLI section
to open with its First run.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:28 | the row calls nova-redis a scratch-value store only; the ACL verbs and the function-library verbs — half the tool — are absent, so a reader with the store-permissions problem cannot find it and the banner's promise overstates what the row says | name both jobs in one sentence ("run the local store, keep scratch values, and keep its users and function library in shape") | S |
| 2 | cmd/nova-redis/serve_test.go:4 | the file opens with issue numbers ("nova-tools #2281 and #3879") in its comment; the standard asks a comment to say why in present tense, and a cold reader cannot resolve a ticket, so it is noise the code's own doc would not need | replace the ids with the rule they stand for ("the store's binding, auth and persistence rules") | S |
| 3 | docs/SPEC-REDIS.md:15-25 | the spec's verb block omits `acl render`, `acl check` and `acl apply`, which the binary ships (main.go:127-129) and docs/FLEET.md:240 uses; a normative spec that disagrees with the code is a bug, and the tests decide which | add the three verb forms to the block and one paragraph on the ACL rules | M |
| 4 | docs/CLI.md:2118 | the section carries no `### First run`, which every verb's onboarding rule and the CLI style (docs/CLI-STYLE.md:74) say opens the section; the transcript in docs/TESTS.md:865-876 already holds the refusals to paste | add the `### First run` block the executed transcript already carries | S |
| 5 | cmd/nova-redis/serve_functional_test.go:29 | the spec's demanded proof of persistence, `TestRestartOnTheSameDirKeepsTheStore` (docs/SPEC-REDIS.md:121, behaviour 10), opens with an unconditional `t.Skip`, so the spec promises a check the tree never runs and a reader cannot tell a regression in the AOF rules from the skip | re-cut it as a mocked-clock unit test or a program the container runs, per the comment's own plan | M |
| 6 | docs/CLI.md:2118 | the section's usage table and the tool's own banner (cmd/nova-redis/main.go:114-118) never show any `acl` or `fn` line in an `example:` block, and no acl verb carries an example (cmd/nova-redis/acl.go:174,188,203), so a reader whose job is the store's users or its library sees no first command for either | add one dry or store-free line for the ACL and function verbs to the example block, or say in one line why they have none | S |

## Good, keep

The unconfirmed-write path (main.go:437-458): a spill whose reply is lost exits
1, says the write may have committed, and ends with a recall remedy rather than
a blind retry — the honest answer a store tool owes.

The scratch invariant is enforced twice, at the CLI (main.go:480-496) and at the
package seam (main.go:508), so a caller that skips the parser still cannot write
a key without an owner and a TTL.

The scratch verbs build one value rendered as lines or as the JSON of the same
value, with the refusal grammar and the exit table inherited from pkg/tool
(cmd/nova-redis/main.go:221,255); the verbs that print their own receipts
(cmd/nova-redis/fn.go:65 and acl.go:177) mark themselves `Prints` and drop
`--json`, so the banner's "every verb but these takes --json" stays true.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-rolled skeleton | FIXED | cmd/nova-redis/main.go:109 builds on pkg/tool, the one shape; the unit tests meet Problems() through the package's own checks and internal/ci holds every such package |
| a 50-line prose wall in help | CHANGED | cmd/nova-redis/main.go:114-118 is a five-line how-text; the long prose moved to the package doc (main.go:1-27) and docs/SPEC-REDIS.md |
| ticket numbers in the package doc | FIXED | cmd/nova-redis/main.go:1-27 names no issue id; the ids that remain are in test comments, not the shipped doc (serve_test.go:4) |
| three line grammars in one help | CHANGED | the invocation grammar is one (pkg/tool), but the fn and acl receipts keep their own bare-head printers (cmd/nova-redis/fn.go:231-259), so the count is two, not one |
