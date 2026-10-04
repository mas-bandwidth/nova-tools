# nova-redis READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 56754ede15b
Score: 8/10
README: 7.5/10

## Reasons

Read cold, running nothing: the README whole, the map under AGENTS.md, the usage
guide it points to first, then the tool's CLI.md section, its spec, the entry
point and every file under cmd/nova-redis, the skeleton and the packages the
verbs lean on, the help text as text. The README line, written before any code:
7.5/10. The row states the problem, the tool and an honest first command, and
its What sentence is the banner's first line word for word (README.md:27,
cmd/nova-redis/main.go:118), the family rule kept. The first confusion is the
row's own wording: "run a local Redis store" in What it does, then "Use a
separate running Redis instance" in setup (README.md:27) — which is it, run
the store or use one already running, and only the cramped "serve also needs
redis-server" hints that serve is the verb that starts one. The first bore is the
trial-logistics stretch the README makes every reader cross after the table
(README.md:56-59), and the first doubt is "These are the Nova Tools 1.0.0
commands" at a tree whose docs and ratings live at 1.1.0 (README.md:48), so a
cold reader cannot tell which version the row's command describes.

The tool as writing is strong where it counts. The banner answers what, how,
first run, exit codes and an example block on one screen, and the exit table
matches the code paths I read: UNCONFIRMED at 1, STALE and MISSING at 1, the
login and dial failures at 2 (cmd/nova-redis/main.go:125 against main.go:443-453,
fn.go:118-121). The refusals name every problem in one run and each says what the
flag wants and where its value came from, the flag or the variable
(cmd/nova-redis/main.go:148-167, 343-368). The two dry runs print from the same
parse the real run takes, so plan and run cannot drift (cmd/nova-redis/serve.go:131-166,
main.go:197-207). The UNCONFIRMED design — a spill whose reply was lost exits 1
with a read-back as the remedy, never a blind re-spill (cmd/nova-redis/main.go:467-476) —
is a one-turn recovery story of a quality I have not met in a CLI before, and the
spec backs it (docs/SPEC-REDIS.md:57-59).

What costs the score. The spec is normative by its own line 8, and its verb
list names seven verbs (docs/SPEC-REDIS.md:17-25) — the acl group, three of
the tool's nine verbs, has no promised behavior in the one document that says
"the tests decide which has a bug". The banner's how-it-works names serve,
spill, recall and fn but not acl (cmd/nova-redis/main.go:120-124), so a cold
reader of `nova-redis help` cannot tell what three of the usage lines are for,
and acl render, the one real verb that opens no store, goes unsold while the
first-run line says only "the --dry-run line needs no store". The CLI.md
section opens with the verb table, not a First run, while the family's own
onboarding rule says the section opens with one and the nova-table section
beside it has one (docs/CLI.md:2025 against docs/CLI.md:2146). The example
block's spill and recall lines need a store a stranger does not have, so two of
four pasted lines exit 2 on a store-less machine (cmd/nova-redis/main.go:145),
and serve hides behind the word "version". The entry doc calls the tool "the
Layer 2 binary", which no document defines, and points at serve.go and fn.go
but not acl.go (cmd/nova-redis/main.go:1-10).

Each file one thing holds for serve.go, fn.go, acl.go and report.go; main.go
carries the dispatch, both scratch verbs and the login machinery (574 lines), and
the login could be a file of its own. Comments say why, in present tense,
throughout (serve.go:3-20, main.go:213, acl.go:141-146, fn.go:17-32). The
tests teach the contract rather than the implementation: firstrun_test runs the
TESTS.md transcript line for line and fails if a refusal reads the environment
(cmd/nova-redis/firstrun_test.go:25-50), examples_test runs the banner's
example block and asserts the dry-run lines dial nothing
(cmd/nova-redis/examples_test.go:19-49), coldread_test pins what a cold reader
needs (every problem named in one run, group help, effects stated), and
status_grammar pins word and exit together. One stale citation: spill_test.go's
doc says it proves "behaviours 14, 16, 17 and 27" of a spec whose numbered
list ends at 17 (cmd/nova-redis/spill_test.go:4).

A 10 needs: the spec naming every verb; the banner's how and example carrying
the acl group; a First run in CLI.md; an example block a stranger can paste on
a store-less machine; and the entry doc free of unexplained layering.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/SPEC-REDIS.md:17 | the spec's verb list names serve, spill, recall, fn and help only; acl render, acl check and acl apply, a third of the tool, have no promised behavior in the normative document | add the three acl verbs to the list and one section stating the render, check and apply contract CLI.md already documents | M |
| 2 | cmd/nova-redis/main.go:120 | the banner's how-it-works names serve, spill, recall and fn but not the acl group, and the first-run line does not name acl render, the one real verb that opens no store | one how line for the acl verbs, and a first-run line naming acl render as the store-free try | S |
| 3 | docs/CLI.md:2025 | the section opens with the verb table and carries no First run, while the family's onboarding rule says the section opens with one and the nova-table section beside it has one | open with a First run of the dry-run sitting TESTS.md already holds | S |
| 4 | cmd/nova-redis/main.go:145 | the example block's spill and recall lines need a store a stranger does not have, so two of four pasted lines exit 2 on a store-less machine, and serve hides behind the word "version" | keep the version and dry-run lines, and mark or drop the store-needing pair | S |
| 5 | docs/USAGE.md:127 | the choosing guide the README sends a newcomer to first has no entry for this tool, so the reader with the row's problem never meets it there | add an entry naming when to try it and the store-free first trial spill --dry-run | S |
| 6 | cmd/nova-redis/main.go:1 | the entry doc calls the tool "the Layer 2 binary", which no document defines, and points at serve.go and fn.go but not acl.go | drop the layering word and add the acl.go pointer | S |
| 7 | cmd/nova-redis/spill_test.go:4 | the test doc cites "behaviours 14, 16, 17 and 27" of a spec whose numbered list ends at 17, so the citation names demands the spec does not make | renumber the citation to the spec's current list | S |

## Good, keep

The What sentence matching the README row word for word, and a banner that answers what, how, first run, exit codes and a runnable example block on one screen.

The refusal grammar: every problem named in one run, each saying what the flag wants and where its value came from, the flag or the variable.

The dry runs printing from the same parse as the real run, and the UNCONFIRMED read-back remedy that makes a lost write a one-turn recovery.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-rolled skeleton | FIXED | the tool is verbs over internal/tool: cmd/nova-redis/main.go:115-137 builds tool.Tool and the banner, help, refusals and JSON come from the skeleton |
| a 50-line prose wall in help | FIXED | the how-it-works is five lines bounded by the skeleton (internal/tool/tool.go:281-284), printed as one paragraph at cmd/nova-redis/main.go:120-124 |
| ticket numbers in the package doc | CHANGED | the entry doc cites files, not tickets (cmd/nova-redis/main.go:1-10), though one ticket number survives in a test doc (cmd/nova-redis/spill_test.go:4) |
| three line grammars | CHANGED | spill and recall return one Out the skeleton renders; fn and acl share one line printer (cmd/nova-redis/fn.go:234-259); only serve formats its own lines beside the instance's stream (cmd/nova-redis/serve.go:163-196) |
| onboarding and output grammar still have rough edges | CHANGED | the banner carries what, how, first run, exit codes and a runnable example block (cmd/nova-redis/main.go:118-137), and one run names every problem (cmd/nova-redis/main.go:154-167); the edge that remains is the acl group's absence from the how and example lines (cmd/nova-redis/main.go:120) |
