# nova-work READ rating, nova-tools 1.1.0

Rater: glm-5.3-flash
Build: e997c3ad26cb
Score: 8.5/10
README: 8/10

## Reasons
READ, cold, nothing run: README.md top to bottom, then docs/CLI.md's nova-work
section and its spec docs/SPEC-WORK-V1.md, then cmd/nova-work from main.go
into internal/workfile, internal/workgh and internal/worklang, the help read
as text. The code earns the score: two verbs and one job in 613 lines under
cmd/nova-work, every refusal names what the input wants and the next command,
the dry run is honest about what it reads in the run itself
(cmd/nova-work/import.go:139-143), the GitHub seam refuses any document that
is not a query (internal/workgh/query.go:75-84), the budget is checked before
any issue is read (cmd/nova-work/import.go:85-89), the tree is written only
after its encoded bytes read back equal (cmd/nova-work/import.go:111-120), and
the help carries a tree the strict reader accepts, pinned by a test
(cmd/nova-work/main.go:157-172). The score is short of ten on leftovers, not
on design: the reference docs still name VERIFY FAIL where the tool prints
VERIFY FAILED (first place I doubted a claim: docs/CLI.md:2681), the spec
promises a page of verbs and an index above layer 1 that no file under docs/
holds (first place I was confused: docs/SPEC-WORK-V1.md:10), the plan-era
wording of the shared reader is patched over with a string replacement at its
one caller (internal/workfile/decode.go:29), and the README row is the
densest cell in the table (first place I was bored: README.md:26). A 10 needs
docs and tool saying one status word, the promised page written or the promise
dropped, refusals caller-neutral without a substring patch, and no state kept
that no verb prints. README: 8/10 — the row is honest about the stage and
leads with the no-login door, and the whole page reads in one pass.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:2681 | The docs say VERIFY FAIL in four places (docs/CLI.md:2681, docs/CLI.md:2690, docs/TESTS.md:1194, docs/SPEC-WORK-V1.md:147) while the tool prints VERIFY FAILED (internal/tool/out.go:28, pinned at cmd/nova-work/main_test.go:114) and the verb help was already corrected (cmd/nova-work/main.go:155). A cold reader matching the reference to the output meets a word the tool never prints. | Write VERIFY FAILED in those four doc lines, including the prose under the transcript in docs/TESTS.md. | S |
| 2 | docs/SPEC-WORK-V1.md:10 | The spec says the verbs that query and change the tree, and its index, are specified on their own page, and section 1.2 calls the tree the store the verbs above query and change; no such page exists under docs/ (only SPEC-WORK-V1.md and SPEC-WORKLANG.md), and section 1.7 says the modes above are specified, not built. A reader hunting for what to do with a tree hits a promise the tree does not keep. | Say in both sentences that the verbs above are not yet specified, or write the page. | S |
| 3 | internal/workfile/decode.go:29 | The shared reader still speaks of plans: its refusals render plan file= (internal/worklang/worklang.go:43) and its comments and messages say a plan is data (internal/worklang/worklang.go:32, worklang.go:146-147), so its one caller patches one phrase with strings.ReplaceAll at internal/workfile/decode.go:29. A substring patch as the seam between reader and caller breaks silently if the reader's wording moves. | Give the reader caller-neutral wording (this file is data, never a program) and drop the replacement. | M |
| 4 | internal/worklang/worklang.go:318 | The integer reader accumulates int64 with no range check, so a twenty-digit number wraps and a file the writer would never write still decodes. | Refuse an integer whose digits do not round-trip through strconv.ParseInt. | S |
| 5 | internal/workgh/fetch.go:32 | Fetcher.Remaining is written after every call and read by nothing; no verb prints the points left in the hour. | Drop the field, or print remaining= in the receipt the budget story owes. | S |
| 6 | README.md:26 | The nova-work row is the densest cell in the table: four sentences where its neighbours carry one or two, so the stage warning and the no-login door fight for the eye. | Trim to the stage line, the no-login first command and one sentence on import; leave the rest to docs/CLI.md. | S |

## Good, keep
The honest dry run: it reads GitHub exactly as the import does, and the run
itself says so, not only the help (cmd/nova-work/import.go:139-143).
The offline door: verify --against and the tree in the help that a test
decodes make the first sitting possible with no login and no network
(cmd/nova-work/main.go:152-172).
The seam: every call counted, every failed page retried smaller, and no
document that is not a query can pass (internal/workgh/query.go:75-84,
internal/workgh/fetch.go:62-101).

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| small leftovers only (8/10, 2026-10-02) | STILL THERE | the doc word drift of finding 1 and the plan wording of finding 3 are the same kind of leftovers, at docs/CLI.md:2681 and internal/workfile/decode.go:29 |
| adoption starts on the harder path (8.8/10, 2026-10-02) | FIXED | verify -h prints the grammar and a tree to try, and --against runs the comparison with no login and no network: docs/CLI.md:2678-2684 |
| README 6.5 to 7, and 8.4 (2026-10-02) | CHANGED | README: 8/10 here; the row leads with the stage and the no-login door at README.md:26 |