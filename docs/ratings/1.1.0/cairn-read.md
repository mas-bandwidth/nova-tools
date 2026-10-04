# nova-cairn READ rating, nova-tools 1.1.0

Rater: deepseek/deepseek-v4.1-flash
Build: 63b7e8dbf641
Score: 8/10
README: 7.5/10

## Reasons

The README's line for the tool is "a session's words, kept durably as plain
files you can come back to" (README.md:35), and the banner's line 1 is the same
words (cmd/nova-cairn/main.go:34). That match is the tool's best quality: a
reader arrives knowing exactly what is offered, and the four verbs, the two
store shapes and the plain-file durability all live up to it. The spec
(docs/SPEC-CAIRN.md:24) names its own refusals, the receipt split of local
persistence from remote publication is honest, and the tests execute the
documented transcript value for value instead of reading it, so help and code
cannot quietly part ways.

A 10 would need the same care at the joins. Three things hold it back. First,
the banner promises that a used entry id with other words is a conflict at exit
1 (cmd/nova-cairn/main.go:38), but the write path reads, checks and renames
without a lock (internal/cairn/cairn.go:616), so two racing appends of one id
both pass the check and the later rename wins: the promise is not kept under
concurrency. Second, a cold reader is sent from the README to the command
reference (README.md:16), whose section opens with prose and a command block
but no `### First run` (docs/CLI.md:2180); the standard's onboarding point 3
asks for that subsection, and the exact transcript already exists one file away
in docs/TESTS.md:740. Third, both store shapes still share one 870-line file
(internal/cairn/cairn.go:574 and :288), so the shape boundary is the reader's
to infer.

The first moments a cold reader meets, in order. Confused: the cairn row's
first command passes `--publish never` (README.md:35) and nothing on the page
says what publication would be, so a required flag reads as a mystery until the
tool's own section. Bored: the one HTML table runs twenty-one rows of long
cells before a single sentence of prose (README.md:21); by the cairn row the
eye has stopped reading and is scrolling to a name. Doubted: the hub says these
are the "1.0.0 commands" and installs `@v1.0.0` (README.md:50), while the tree
and this rating are 1.1.0, so a cold reader cannot tell which version the
checkout is.

The README line earns 7.5/10 on its own: its cairn sentence is accurate, the
trial instructions are runnable, and the "what do you want to do" table is a
real map. It loses because the table is a wall, and because the 1.0.0 claim is
stale at a 1.1.0 head.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | internal/cairn/cairn.go:616 | the append reads the entry file, checks for a conflict, then names it: two appends of one id with different words in parallel both read nothing and both write, so the last silently replaces the first, though cmd/nova-cairn/main.go:38 promises a conflict at exit 1 | hold the entry id under an exclusive lock from the read to the rename, so the loser sees the first words and refuses | M |
| 2 | docs/CLI.md:2180 | the nova-cairn section opens with prose and a bare command block, with no `### First run`; the standard's onboarding point 3 asks for the subsection, and the runnable transcript already stands in docs/TESTS.md:740 | move the docs/TESTS.md:743 transcript under a `### First run` heading here | S |
| 3 | internal/cairn/cairn.go:574 | the nested writer and the flat writer (cairn.go:288) share one 870-line file with open, index and coverage, so the shape boundary is invisible; the earlier "two shapes tangled in one file" note still reads true | split the flat format into flat.go and the nested write into append.go | L |
| 4 | internal/cairn/cairn.go:315 | the flat writer lands `strings.TrimRight(text, "\n")`, dropping trailing newlines, while docs/SPEC-CAIRN.md:52 says the words land byte-for-byte and the nested path (cairn.go:659) keeps them | say "trailing newlines trimmed" in the spec, or store the bytes as given | S |
| 5 | cmd/nova-cairn/main_test.go:1 | the test's first line names an issue number; the same appears at internal/cairn/cairn_test.go:1 and cmd/nova-cairn/bare_verb_refusal_names_door_test.go:12, and internal/cairn/benchstore_test.go:4 names a date and a machine | say what the test pins, never where it came from | S |
| 6 | cmd/nova-cairn/main.go:268 | `index` walks and parses the whole store twice: once through `cairn.Index`, then again through `cairn.Coverage`, which calls Index itself (internal/cairn/cairn.go:866) | have Index return the ledger, or count the rows already read | S |
| 7 | cmd/nova-cairn/main.go:281 | `receipt` writes `persisted=true` and `published=false` as literals instead of the `rc.Persisted` and `rc.Published` the library returned, giving the one split the spec cares about two spellings that can drift | print the fields the library returned | S |
| 8 | README.md:50 | the hub says these are the "1.0.0 commands" and points at the v1.0.0 release, while the tree and this rating are 1.1.0, so a cold reader is sent to the wrong version | name the version the tree ships | S |

## Good, keep

- The refusal that names the remedy verb whole, flags and all: `open first:
  nova-cairn open --store <dir> --session <id> --publish <policy>`
  (internal/cairn/cairn.go:214), with a test that runs it through a shell.
- A store read in its own shape and never migrated: `open` on a flat record is
  a no-op, an append lands a dated section in the file's own form, and no side
  directory appears (internal/cairn/cairn.go:12).
- Tests that execute the documented first run and compare every value
  (cmd/nova-cairn/firstrun_test.go:89), so a drifted help line cannot pass.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | CHANGED | the readers moved out (internal/cairn/read_existing.go:30, internal/cairn/open_remedy.go:1); both writers still share internal/cairn/cairn.go |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | cmd/nova-cairn/main.go:38 promises a conflict; internal/cairn/cairn.go:616 reads before it writes, with no lock |
| README rated 6.5-7 and 8.4 | CHANGED | README.md:50 still names 1.0.0 at the 1.1.0 head, and the table at README.md:21 still carries all twenty-one rows; the cairn row itself (README.md:35) reads clearly |
