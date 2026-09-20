RESULT tools22-pre-1840-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1840 at head f9a12b4afc41: toolwork T06a: nova-pulse cut writes the five typed header lines the gate reads (#1651)
PREREAD 1840 claims=11 proven=8 unproven=3 defects=2 high=0

PR 1840
HEAD f9a12b4afc4178a56c1e038400f2a90034819bb8
BASE rowan/toolwork-t05-harvest-accept
MERGE-BASE cf5cb1786236103621aab2fb7432c0a1f0fa9371
BEHIND 0
FILES 4 production, 2 test (plus docs/CLI.md)
LINES +377 -12

Notes on base: the merge-base against origin/rowan/toolwork-t05-harvest-accept is
cf5cb178, which IS the tip of that branch (the PR sits directly on it, BEHIND 0, not
stale). The card's own header names BASE dev@5298f6be12ea, but STEP 2 and the RESULT
template name rowan/toolwork-t05-harvest-accept, and 5298f6be12ea is on a divergent dev
lineage (not an ancestor of the merge base), so the diff below is read against the
rowan branch per STEP 2. The base already declared the `rebase` kind in internal/pulse
(line-1 shape, steps, required flags); this PR's work is the CLI flags that make it
cuttable, the typed header, the nearest-kind refusal, the label fix and the docs.

CLAIMS

1. `cut --kind` writes the five typed header lines (KIND, PATHS, TEST, LEGS, SOURCE)
   directly under the contract line, and the gate reads them back whole.
   PROVEN-BY internal/pulse/cutheader_test.go:52 TestCutWritesTheFiveHeaderLinesReadCardHeaderReads
   — cuts a gated fix card, asserts the five `KEY:` lines sit at lines 2-6 and every
   field (Kind, Paths, TestPkg/TestName, Legs, Source, Label) reads back equal.

2. `cut` refuses a gated `--card-kind` with no `--paths` or no `--test`, naming the kind
   and the missing line, before any card is written.
   PROVEN-BY internal/pulse/cutheader_test.go:109 TestCutRefusesAGatedKindWithoutPathsOrTest
   — asserts exit 2 and that stderr holds "no PATHS"/"no TEST" and "kind=fix-red".

3. `cut` refuses a `--card-kind` the kinds table does not hold; there is no default kind.
   PROVEN-BY internal/pulse/cutheader_test.go:131 TestCutRefusesACardKindTheTableDoesNotHold
   — asserts exit 2 and the refusal names the unknown kind.

4. The refusal of an off-table kind names the table's nearest name and the SPEC-TOOLWORK
   road (fix-with-red-test→fix-red, docs-fix→text, …), while a spec-declared-but-unbuilt
   kind (sweep) is refused without being offered a different kind.
   PROVEN-BY internal/pulse/cutheader_test.go:168 TestCutRefusalNamesTheTablesNearestKind
   and internal/pulse/cutheader_test.go:195 TestCutRefusesADeclaredKindThisBinaryDoesNotBuildWithoutRenamingIt.

5. A card cut with no `--card-kind` carries no KIND line (ungated) and still has a
   readable contract label.
   PROVEN-BY internal/pulse/cutheader_test.go:144 TestCutWritesNoKindLineWhenNoneIsAsked
   — asserts h.Kind == "" and h.Label != "".

6. `cut --kind rebase` is cuttable from the command line with `--branch` and `--base`,
   and its line 1 names the PR, the base and the title.
   PROVEN-BY cmd/nova-pulse/cut_kind_rebase_test.go:19 TestCutKindRebaseIsReachableFromTheCommandLine
   — runs the binary's own cut path against a real queue, asserts exit 0, one card, and
   line 1 holding "PR #1730", "rebased onto dev" and the title.

7. A `cut` card's label reads back non-empty because contractLabel now reads the
   `RESULT:` spelling as well as `RESULT`; a gate that knew only the second keyed every
   line it printed on an empty label for every card a real pulse cuts.
   PROVEN-BY internal/pulse/cutheader_test.go:67 (the round-trip's h.Label != "" would
   go red if contractLabel reverted to the single spelling; the commit names that as the
   negative control).

8. The binary's usage synopsis and docs/CLI.md's synopsis agree on the `cut` verb's flag
   set, including the four new header flags.
   PROVEN-BY-EXISTING internal/docs/cli_usage_flags_test.go:29
   TestCLIDocSynopsisNamesTheFlagsTheBinaryHas — for every verb it compares the documented
   flag set to the binary's usage flag set in both directions; the new flags appear in
   both sources.

9. docs/CLI.md's prose documents the four header flags, what each writes, and the two
   refusals a gated kind draws without PATHS or TEST.
   UNPROVEN — the prose is new text in this diff; the PR's own commit says "the prose
   under it is the part no class test can check", and no test in the diff or the tree
   reads docs/CLI.md prose.

10. The usage synopsis offers `rebase` among the `--kind` choices.
    UNPROVEN — the class test of claim 8 compares flag SETS only, so both sources could
    drop the word `rebase` from `read|fix|replay|spec` and no test would go red; nothing
    pins the string.

11. The SOURCE line is written from `--repo` and the card's own issue or PR number.
    UNPROVEN — the round-trip test asserts only that Source is non-empty
    (cutheader_test.go:82), never its exact `<repo>#<n>` form.

DEFECTS

DEFECT medium internal/pulse/cutkind.go:354 — cut's gated-kind refusal checks PATHS and TEST only for emptiness, so a malformed value (`..` or absolute or over-8 globs, or a `--test` that is not `<pkg> <TestName>`) is written into a card the gate's ReadCardHeader then refuses to read, and the harvest's cardKind() treats an unreadable header as not-gated — the card is pushed as gate=none with the gate that was asked for never having run — this contradicts docs/CLI.md's own line "a card the gate cannot judge is not a card" and SPEC-TOOLWORK §5's "PATHS: … validated at cut" — fix: have cutKindHeaderProblem run the same checks ReadCardHeader applies before writing (split PATHS and call hyg.ValidatePaths with its ≤8 cap; require TEST to parse as `<pkg> <TestName>` with a Go test name).

DEFECT low internal/pulse/cutkind.go:133 — the unknown-`--kind` refusal still says "(pass one of the four kinds)" although CutKinds holds five and this PR's own synopsis and docs advertise five — a message the change itself makes wrong — fix: "pass one of the five kinds" (or print strings.Join(CutKinds, "|")).

QUESTIONS

1. `--card-kind rebase` is refused (rebase is in SPEC-TOOLWORK §5 rule 2 as a gated kind with a control, but not in internal/pulse/kinds.go), so every rebase card this change can cut is ungated — is that deliberate staging until T13 builds the rebase gate, and is "the kinds table does not hold rebase, and there is no default kind" the intended UX for it?

2. cut validates the header only for presence, not content; a malformed gated header then silently harvests as gate=none and is pushed (claim 1's test only proves the round trip on valid input). Is content validation deliberately deferred to the gate / `lint --card`, or is it owed in cutKindHeaderProblem?

3. An explicitly-named ungated `--card-kind text` writes KIND: text plus PATHS: none / TEST: none / LEGS: go, but the docs describe only the no-`--card-kind` ungated case — is that the spec's intended spelling for a named ungated kind, and should `Missing()`'s PATHS (it flags empty PATHS) ever be consulted for such a card?

4. contractLabel now also reads the `RESULT:` spelling, which changes the label of every `cut --kind` card from "" to "CARD-<n>" and of any task file whose first line is `RESULT: <label>` — are there consumers outside this diff (harvest pool matching, the status index, benches.tsv) that relied on the empty label?

Left owed

Read in full at the PR head: cutkind.go, cardheader.go, kinds.go, accept.go,
harvest_accept.go, the two new test files, main.go's usage, the docs/CLI.md section, and
the harvest.go contractLabel region plus its call sites. Not read in full (surveyed by
grep only, none of the changed lines sit there): the rest of harvest.go's ~900 lines, the
existing cut_kind/accept/harvest test corpus, the internal/docs test corpus, and
SPEC-TOOLWORK/SPEC-PULSE beyond the referenced §5 lines. go build and go vet of
internal/pulse and cmd/nova-pulse at the PR head both pass.

git status --short
(prints nothing)

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1840-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1840-r2	1	2026-09-20T19:23:44Z	2026-09-20T19:38:15Z	0	opencode	deepseek-v4-flash	89184	55784	0	4870912	0	0.1645
