RESULT tools22-pre-1431-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1431 at head 7491b95f9372: tokens: fold --units, the units column, sum --by unit
PREREAD 1431 claims=13 proven=13 unproven=0 defects=1 high=0

PR 1431, HEAD 7491b95f93727c45a246045e0dfce7a505dfc4c3, BASE dev, MERGE-BASE 04bb4e1c7ee73537396bf436701f129233446432, BEHIND 71, FILES 9 production, 7 test, LINES +1038 -60

1. `fold --units <set.lisp>` reads a work set, attributes each transcript to the unit named by the first tool input matching a unit's `:pr`, `:branch`, or `:lane` clone directory, and writes the unit id into the day file's twelfth column, printing one `TOKENS UNITS` line. — PROVEN-BY cmd/nova-tokens/units_test.go:458 `TestFoldUnitsAttributesEachTranscriptToOneUnit` — fold --units with three children writes their attributed units into the day file and prints a TOKENS UNITS line with set, count and file.

2. A fold with no `--units` writes `-` on every row's twelfth column and prints no TOKENS UNITS line. — PROVEN-BY cmd/nova-tokens/units_test.go:487 `TestAFoldWithNoUnitsPutsEveryRowOnTheDash` — fold without --units writes `-` on every row and omits the TOKENS UNITS line.

3. The day-file reader accepts both twelve-column and eleven-column headers; an eleven-column file's rows are read with unit=`-` and no findings. — PROVEN-BY cmd/nova-tokens/units_test.go:512 `TestTheReaderTakesTheElevenColumnFile` — a file with the 11-column header reads with zero findings and the row's unit is `-`.

4. `sum --by unit` prints SUM UNIT lines (heaviest first, `-` among them) instead of SUM PAIR and SUM MODEL lines. — PROVEN-BY cmd/nova-tokens/units_test.go:535 `TestSumByUnitPrintsTheUnitsTable` — --by unit prints SUM UNIT lines for each unit including `-` and no SUM PAIR or SUM MODEL lines.

5. `sum` without `--by` (default `pair`) prints SUM PAIR and SUM MODEL lines, not SUM UNIT. — PROVEN-BY cmd/nova-tokens/units_test.go:535 `TestSumByUnitPrintsTheUnitsTable` — the default sum prints SUM PAIR lines and no SUM UNIT.

6. `sum --by` with an unrecognized value exits 2 with a refusal. — PROVEN-BY cmd/nova-tokens/units_test.go:564 `TestSumRefusesAByItDoesNotKnow` — `--by lane` exits 2.

7. `fold --units` with an unreadable or invalid work-set file exits 2 with a refusal naming the file. — PROVEN-BY cmd/nova-tokens/units_test.go:572 `TestFoldRefusesAUnitsFileItCannotRead` — bad-file and missing-file cases both exit 2.

8. A unit is matched by `:pr` (`#1412`, `/pull/1412`), `:branch`, or `:lane` clone directory (`lane-<name>`), each at a boundary that prevents a shorter id from matching inside a longer one. — PROVEN-BY internal/tokens/units_test.go:1396 `TestUnitsMatchTheThreeThingsATranscriptNames` — all three naming forms match; internal/tokens/units_test.go:1421 `TestUnitsAreMatchedAtABoundaryAndNotBySubstring` — `#141` does not match inside `#1412`, `rowan/ci-no-…` is not `rowan/ci`, etc.

9. The first token that names a unit wins; later tokens in the same or later messages do not override it. — PROVEN-BY internal/tokens/units_test.go:1449 `TestTheFirstTokenThatNamesAUnitWins` — ordering test: PR 1412 before PR 141 gives "tokens", reverse gives "bus".

10. A nil `*Units` (fold without `--units`) returns `NoUnit` for every input and panics nowhere. — PROVEN-BY internal/tokens/units_test.go:1467 `TestNoUnitsFileIsEveryRowOnTheDash` — nil Units Match returns NoUnit, Len is 0.

11. `LoadUnits` refuses a work set with no identifiable unit ids. — PROVEN-BY internal/tokens/units_test.go:1477 `TestAWorkSetWithNoUnitIdIsARefusal` — empty units set returns error.

12. `SUM TOTAL` and `SUM OK` carry `units=<n>` in addition to their existing fields. — PROVEN-BY cmd/nova-tokens/units_test.go:512 `TestTheReaderTakesTheElevenColumnFile` — "units=1" appears in SUM OK after summing an 11-column file.

13. An eleven-column day file written before this change sums correctly. — PROVEN-BY cmd/nova-tokens/units_test.go:512 `TestTheReaderTakesTheElevenColumnFile` — sum over the v1 file exits 0 and prints SUM OK with units=1.

DEFECT low SPEC-TOKENS.md:706 — the example day-file table shows `claude-fable-5-1 schema` row with unit `u3`, but `u3` is not an id in any work set referenced by this change or in the repo. A reader looking for the unit that attribution produced on that row has no set to resolve `u3` against. — A spec example whose unit id has no referent is misleading: the reader cannot tell whether `u3` is the row's actual unit or a placeholder. — Replace `u3` with an id from the new test data (e.g. `certify:verb` or `-` to match the other rows), or add a companion work-set fragment that defines `u3`.

1. The example table in SPEC-TOKENS.md shows `u3` as the units-cell value for the `claude-fable-5-1 schema` row. Was `u3` chosen as a realistic id from a real work set, or is it a placeholder? If the latter, should it be `-` like the other rows, given that the fold examples in the same diff never produce `u3`?
2. The diff thread pen -- no Sway audit, no running card. The merge base (04bb4e1c7) is 71 commits behind origin/dev (5298f6be12ea) and is older than the card's stated base. Was the PR intentionally not rebased, or is this a stale review that needs refresh?
3. `contract_test.go` adds a carve-out for `internal/worklang/expand.go` containing `os.WriteFile`. Has that path been verified to never truncate an existing file (i.e., does it use `os.OpenFile` with O_EXCL or is it truly write-only to new paths)?

Left owed: none — the diff is 1038 lines across 16 files and I read all of it.

```
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json
?? repo/
```

The job root is not itself a git repo (only `repo/` is). The untracked files above are all expected card outputs.===FILE=== card-tools22-pre-1431-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1431-r4	1	2026-09-20T19:27:25Z	2026-09-20T19:40:02Z	0	openrouter	deepseek/deepseek-v4-flash	392183	4317	0	295168	11987	0.0172
