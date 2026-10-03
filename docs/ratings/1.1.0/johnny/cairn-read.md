# nova-cairn READ rating, nova-tools 1.1.0

Rater: Grok
Build: 2c02b2aa2042
Score: 7.5/10
README: 7.5/10

## Reasons

The README row (README.md:33) says this tool is "a session's words, kept durably as plain files you can come back to", and the first command is an open with an explicit publication policy. That sentence is also the banner (cmd/nova-cairn/main.go:47). The code does the job the sentence names: a directory you pass in, four verbs, exact text in a nested entry file, a retry that does not add a second copy, a conflict instead of a replace, and success only after the bytes are synced. I would hand an AI those four verbs.

First confusion, before any code: docs/CLI.md:2117 introduces the other layout as `cairns/<session>.md` "directly under the store". I read `cairns/` as a subdirectory. The example at docs/CLI.md:2123 shows that name is the store itself.

First boredom: README.md:71 says again to choose the row, after README.md:44 already said to pick the row. The page spends that stretch restating the table instead of showing one tool's contract.

First doubt: README.md:48 calls the table "the Nova Tools 1.0.0 commands", and the install block pins that release. I did not believe the row was this tree. The cell's sentence does match this banner, so the doubt was about the page around the cell, which is why the README score is 7.5 rather than the row alone.

What keeps the tool off a 9 is the second layout and the page that explains it. internal/cairn/cairn.go:14 documents `sessions/`, `entries/` and `log.jsonl`. internal/cairn/cairn.go:20 documents a second shape, one markdown file per session in the store directory, which open must not split. The banner (cmd/nova-cairn/main.go:50) teaches only the nested files. The spec then opens by arguing with an unnamed draft (docs/SPEC-CAIRN.md:13) before it says what the verbs do, and from docs/SPEC-CAIRN.md:110 it lists the same contract again as test names. Any `*.md` in the store directory whose name is a legal id counts as a session (internal/cairn/cairn.go:720).

docs/CLI.md:2132 says index and receipt "cover the tool's own shape only". Receipt takes the flat branch (internal/cairn/cairn.go:621) and the index walks those files (internal/cairn/read_existing.go:88). The same section says the words land byte for byte (docs/CLI.md:2128); the flat append trims and compares trimmed text (internal/cairn/cairn.go:252, internal/cairn/cairn.go:268). A cold reader who trusts the command page leaves with the wrong model of the layout the banner never mentioned.

The command section (docs/CLI.md:2088) does not open with a first-run transcript. The transcript that is actually executed is docs/TESTS.md:740, and the section does not point at it. The adoption guide's choosing list starts at docs/USAGE.md:140 and never names this tool, while README.md:16 sends a new reader there first. The table link still reaches the command section, so this costs discovery, not the verbs.

open and append are local writes (cmd/nova-cairn/main.go:60, cmd/nova-cairn/main.go:73) and neither offers a dry run. The family rule at AGENTS.md:73 says a verb that writes has one, so an AI cannot read the plan before the files change. Four publication values are required (cmd/nova-cairn/main.go:42) and none of them sends anything (cmd/nova-cairn/main.go:52). That split is honest, and it is a tax on every call.

The conflict is check-then-write. The nested path reads the entry (internal/cairn/cairn.go:501) and later writes it (internal/cairn/cairn.go:536) with nothing exclusive between the two. The flat path reads (internal/cairn/cairn.go:247) and appends (internal/cairn/cairn.go:270) the same way. Two creators of one id can replace a nested file, or append a second heading; a repeated heading makes a later read refuse the file (internal/cairn/read_existing.go:73). The banner states the conflict as a fact (cmd/nova-cairn/main.go:51), not as a single-writer fact.

A 10 would keep one layout, put the docs/TESTS.md:740 transcript at the top of the command section, make the index sentence match Receipt, exclude a second writer of the same id, and print a dry-run plan that writes nothing. The comments that say why the bytes are not trimmed, and why a missing session prints the whole open command, would stay.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | docs/CLI.md:2132 | Index and receipt are said to see only the nested files, but both read the flat session file, so the page an AI is sent to describes the wrong store. | State that both verbs read dated sections in the flat file, and that the flat body is the trimmed text, matching the spec. | S |
| 2 | internal/cairn/cairn.go:501 | The same entry id is read and then written with no exclusion, so two creators can replace the nested file or double a flat heading, against the banner's conflict line. | Create the entry exclusively, or lock the session file, and return the duplicate or the conflict to the loser. | M |
| 3 | cmd/nova-cairn/main.go:60 | open and append write local files and do not take a dry run, so an AI cannot see the plan the family rule promises before anything is created. | Add the dry-run flag on both writes and print the paths and the refusal from the same checks, writing nothing. | S |

## Good, keep

The purpose is one sentence, and it is the same sentence in the table and the banner. A missing flag is a refusal, never a guessed directory (cmd/nova-cairn/main.go:17).

Nested text is stored as given. The same id with the same text is a duplicate; different text is a conflict; success says the note was stored here and was not sent (internal/cairn/cairn.go:510, internal/cairn/cairn.go:79).

A session nothing holds is answered with the open command whole, store and session included (internal/cairn/cairn.go:200). The tests are named as the contract and the names are real functions, not a wish list.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | STILL THERE | internal/cairn/cairn.go:14 beside internal/cairn/cairn.go:158, and docs/CLI.md:2132 still misstates the flat one |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | internal/cairn/cairn.go:501 then internal/cairn/cairn.go:536 with no exclusion; cmd/nova-cairn/main.go:51 states the promise |
