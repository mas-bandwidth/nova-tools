# nova-update READ rating, current baseline 0c5803c2de40

Rater: deepseek-v4.1-flash
Build: 0c5803c2de40
Score: 6.5/10
README: 7/10

## Reasons
The README's row for this tool (README.md:40) says it compares installed tools
with their latest releases and updates one when asked. The first place I was
confused is that same row: the binary also carries a whole release pipeline,
cut, build, install, adopt, pull and cycle, documented across
docs/CLI.md:1490-1615 but absent from the table, so a reader choosing from the
README never learns what most of the binary is for. The first place I was bored
is docs/CLI.md:1500-1512, where one paragraph recites tag digests, per-platform
checksum files, a security sign-off and a forge file ceiling before it says
what the verb does. The first place I doubted a claim is docs/CLI.md:1462,
where a parenthesised ticket number is offered as the reason our own tools are
read rather than reported UNKNOWN; a stranger cannot resolve it, and
docs/SPEC-UPDATE.md:894 defers an open question to a named person.

The package is disciplined where it counts. Every verb builds one value rendered
as typed lines or as the JSON of the same value; refusals name every missing
flag at once with a remedy a cold reader can act on; the manifest loader reports
every problem in line order rather than the first; and the first-run transcript
is executed line for line against the binary. The prose is present tense and the
comments say why.

What keeps it from a higher score is weight and scope. The release pipeline is
larger than the update tool it lives inside (internal/release is about 4,900
lines against internal/update's 4,100), one package carries two binaries, a
hand-rolled output renderer sits beside the shared skeleton, the help is a wall
of prose, and the test that would prove the banner's example lines run as
printed is skipped. A 10 would need the release verbs in their own binary or
named in the README, the skeleton able to carry a prose tail so the local
renderer can go, the manifest rules moved under `report -h` instead of the
banner, the public docs and specs freed of people, home paths and ticket
numbers, and the example-lines contract test actually running.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:40 | the row says compare-and-update, yet the binary is also a six-verb release pipeline (docs/CLI.md:1490), so a reader choosing from the table cannot see most of the tool | name the release verbs in the row, or move them to their own binary | L |
| 2 | internal/update/out.go:22 | a hand-rolled renderer (emit, capped, text, typed) sits beside the shared skeleton and is kept only because an item cannot carry a prose tail | teach internal/tool's item a prose tail and delete the local renderer | L |
| 3 | internal/update/cli.go:157 | help prints roughly forty lines: the opening, ten usage lines, a defaults sentence, a note paragraph, a two-binary paragraph, six manifest lines, an exit paragraph and the example | move the manifest rules under `report -h` and cut the banner back to the three onboarding answers | M |
| 4 | docs/CLI.md:1509 | the public reference names an individual's sign-off, a personal home path (docs/CLI.md:1520) and a ticket number (docs/CLI.md:1462); docs/SPEC-UPDATE.md:894 defers a default to a named person | replace each with a role, a generic path and a resolved statement | M |
| 5 | cmd/nova-update/examplelines_test.go:36 | the test that would run the help example lines as printed is skipped, so the banner's promise is unverified, and its comment names a person and a ticket | run it under a mocked clock or as a functional test | M |
| 6 | internal/update/versiontool.go:10 | nova-update and nova-version are two binaries in one package, so the banner needs a paragraph (internal/update/cli.go:178) to tell a reader which to reach for | give nova-version its own package | L |
| 7 | internal/update/manifest.go:13 | the manifest is six tab-separated columns and five kinds with an argv grammar, none of it legible quickly | reduce the columns or derive kind and owner, and state the shape once | M |

## Good, keep
The version ladder (internal/update/read.go:308) asks `version`, then
`--version`, then bare, so our own tools that refuse a bare call are read rather
than reported UNKNOWN; that is a real reading of a real problem.
The manifest loader (internal/update/manifest.go:59) returns every problem of
the file in one refusal, in line order and capped, so the writer fixes it once.
The first-run transcript (docs/TESTS.md:657) is executed line for line against
the binary by cmd/nova-update/firstrun_test.go:71.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| a hand-built CLI beside the skeleton | STILL THERE | internal/update/out.go:22 |
| banner walls | STILL THERE | internal/update/cli.go:157 |
| a release pipeline larger than the tool | STILL THERE | README.md:40 and internal/release against internal/update |
| manifest arguments | STILL THERE | internal/update/manifest.go:13 |
| oversized shared CLI code | STILL THERE | internal/update/versiontool.go:10 |
| the README then 6.5 to 7 from one rater and 8.4 from another | CHANGED | README.md:40 |