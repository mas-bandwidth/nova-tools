# nova-cairn READ rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 17b4f6ee35bb
Score: 8.5/10
README: 8/10

## Reasons

Read cold, running nothing. The README row for this tool (README.md:33) says
what it is for in one sentence — a session's words, kept durably as plain files
you can come back to — and the banner's first line (cmd/nova-cairn/main.go:47)
is that sentence word for word, the family rule kept. The section opener
(docs/CLI.md:2090-2091) and the code's own header (cmd/nova-cairn/main.go:1-9)
tell the same story, and the store's layout is one comment a reader finds in a
minute (internal/cairn/cairn.go:14-18). Four verbs, each one thing, each with
its effect class declared (main.go:60,73,99,111).

Reading impressions from the README, before any code: first confused at
README.md:25 (the nova-table row's "loads the functions it needs" — into what,
and why does the row care?); first bored at README.md:61-69 (three commands of
git ceremony for a trial bus I had not picked); first doubt at README.md:48-50
(the trial commands are pinned to the 1.0.0 release inside a 1.1.0 tree, so a
cold reader cannot tell whether the row's command matches the head).

The spec is the best writing here: it opens with what the tool refuses to be
(docs/SPEC-CAIRN.md:13-22) before what it does, each verb's paragraph opens
with the command it governs (docs/SPEC-CAIRN.md:26,64,88,99), and it turns a
real defect into a named rule with the remedy quoted whole
(docs/SPEC-CAIRN.md:53-62). Comments say why, in present tense, and name the hurt each
rule answers (internal/cairn/cairn.go:158-171 on the bench shape;
internal/cairn/cairn.go:349-356 on corrupt provenance never reading as none).
The tests teach the contract instead of mirroring the code: the first-run
transcript is executed line by line with values compared
(cmd/nova-cairn/firstrun_test.go:95-141), and one run names every problem at
once (cmd/nova-cairn/main_test.go:325).

What it costs: the banner's promise "with other words a conflict (exit 1)"
(cmd/nova-cairn/main.go:51) is a check-then-write rule with no lock, so two
concurrent appends under one new entry id can both return OK while the second
rename silently replaces the first; the concurrency test covers records, not one
id (internal/cairn/cairn_test.go:99). A re-open prints a fresh clock stamp and
no marker, so it reads like a first open (cmd/nova-cairn/main.go:160-171).
The bench heading grammar is parsed twice (internal/cairn/cairn.go:220-237 and
internal/cairn/read_existing.go:59-86), and the section in docs/CLI.md carries
no First run block the repo's own standard promises it opens with. A 10 needs
the never-overwrite promise closed in the code, the re-open marked in the
output, one bench parser, and trial commands that name the tree they ship with.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-cairn/main.go:51 | the banner promises the same entry id with other words is a conflict, never an overwrite, but two concurrent appends under one new id both read no such entry and both write; the second rename silently replaces the first, so an OK line reports words the store no longer holds (internal/cairn/cairn.go:501-536; the concurrency test is over records, not one id) | write the entry file with the atomic no-replace option and treat the exists error as the duplicate-or-conflict read | M |
| 2 | cmd/nova-cairn/main.go:160-171 | a re-open of a session that already stands prints a fresh clock stamp and no duplicate marker, so the second open reads exactly like the first (the no-op returns nil at internal/cairn/cairn.go:408-413 and the line prints the call's clock) | report duplicate=true with the stored open stamp, read back from the log | S |
| 3 | README.md:48-50 | the trial commands are pinned to the 1.0.0 release inside a 1.1.0 tree, so a cold reader cannot tell whether the row's first command matches the head without opening the release notes | pin the trial block to the current release, or say in one line why it stays at 1.0.0 | S |
| 4 | internal/cairn/read_existing.go:59-86 | the bench heading grammar is walked twice: one reader finds a single section (internal/cairn/cairn.go:220-237) and another walks all sections, two loops over the same machine form a change must keep in step | one reader returns the sections and both callers consume it | S |
| 5 | docs/CLI.md:2088 | the section carries no First run block, which the repo's own onboarding standard says it opens with (docs/STANDARD.md:72), and eight of the twenty sections in the same file do carry one | add the block the executed transcript in docs/TESTS.md:731-755 already holds | S |

## Good, keep

The refusal that quotes the whole remedy verb, shell-ready, with control bytes
carried safely through a subshell (internal/cairn/open_remedy.go:12-22) — a
cold reader can paste the fix without rebuilding the invocation.

The store is read, never imposed: a hand-kept record file is appended to in its
own shape and gains no sidecars, no log and no index beside it
(internal/cairn/cairn.go:20-31, 239-274).

Every line splits local durability from delivery: persisted=true with
published=false on every append and receipt (cmd/nova-cairn/main.go:192-194),
so an AI never reads a local write as a delivered one.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| the store keeps two shapes tangled in one file | FIXED | internal/cairn/cairn.go:14-18: the words live in entries/<session>/<id>.json and the readable record only links to them; docs/SPEC-CAIRN.md:33-35 says the entry files are the source of truth |
| same-id concurrent appends can overwrite what the banner promises never is | STILL THERE | internal/cairn/cairn.go:501-536 reads then renames with no lock and no no-replace option; the only concurrency test is over records (internal/cairn/cairn_test.go:99) |
