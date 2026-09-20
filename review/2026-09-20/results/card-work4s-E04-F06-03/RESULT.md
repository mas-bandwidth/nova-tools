RESULT work4s-E04-F06-03 sha=5298f6be12ea — nova-work E04-F06: does the contract say it? criterion E04-F06-03: Keep historical O answers separate from indexed C as-of queries and report unavailable history honestly
DONE
CRITERION E04-F06-03 SPEC STATED

REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "historical\|as.of\|unavailable\|honest" docs/SPEC-WORK.md | head-20 : hit counts from individual greps below
grep -in "as.of\|as_of\|asof" docs/SPEC-WORK.md | head-20 : above
grep -n "unavailable\|not.available" docs/SPEC-WORK.md | head-20 : above
grep -n "separate\|apart.*O\|apart.*C" docs/SPEC-WORK.md | head-20 : above
grep -n "\"historical O\"\|historical.*O\b\|O.*as.of\|O.*query" docs/SPEC-WORK.md | head-20 : above
grep -n "O.*C.*query\|open.*closed.*query\|--branch.*select" docs/SPEC-WORK.md | head-20 : above

SPEC-WORK.md:753-755 (both clauses stated here together): "**A query that asks what an item's state was, as of a window end whose partition it cannot read, refuses rather than answering from a newer row**: exit 1, `QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable`, naming the one partition it would need, so the caller reads a refusal it can act on instead of a number it cannot check."

SPEC-WORK.md:747-753 (clauses about honest reporting of availability): "**An absent day and a missing segment are two different answers, and the manifest is what tells them apart.** A day with no manifest inside a complete manifested range **means no events that day**: the answer is the rows there are, `gap=0`, and no note. **A manifest or a segment the committed root names that is missing or corrupt is a coverage gap**: the listing prints the rows it can answer, `gap=<n>` and one `QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>`, and **never an empty closed set, because *a missing archive produces an honest coverage gap, not an empty completed set*** (Glenn, 23:34Z)."

SPEC-WORK.md:1792-1811 (keeping O and C queries separate via --branch): "**`--branch` selects the rows a listing prints and never what the counts fold**: the counts on a `QUERY OK` line are the scope's and have always folded both branches — a done leaf is counted by its container today and is counted the same way from C — so no percentage, no `rows=`, no `required=` and no rollup changes its value because an item settled. **Each id is counted once and printed once**: `open=<n>` and `closed=<n>` partition the scope's counted ids... **A closed listing never loads all of history**: its rows come from the closed index in event-revision order..."

SPEC-WORK.md:1821-1824 (O vs C separation said once): "**Which branch a sentence of this document is about, said once.** Every sentence about loading, validating, journaling, clipping, ownership, fencing, the one coordinator and the one live reader/writer is about **O** and the closed index the same session opens: the session loads O whole and reads C's index in bounded pages, never C's history."

SPEC-WORK.md:1597-1617 (append-only C index enabling as-of reconstruction): "**The closed index's rows are append-only, one per transition, and that is what makes a past answer stay past.** Each `:settle` and each `:revive` writes **its own immutable row**, keyed `<event-rev>:<id>` and written into the day partition of that event's stamp; a row is never rewritten and never replaced."

SPEC-WORK.md:1611-1614 (refusal as floor under as-of): "**The refusal is the floor under it and not the design**: where a partition such a lookup needs is missing or corrupt, the ask refuses by the retention section's one line naming that partition, and never answers an earlier moment from a later row."

E04-F06 :by-feature entry at docs/roadmaps/nova-work.sexp:91:
:tests "as-of-reconstructs-settle-revive-settle; cursor-pinned-across-a-new-settle; default-window-opens-two-days; rule-2-unavailable-is-not-green"

Test existence in tree:
- as-of-reconstructs-settle-revive-settle : PRESENT (deftest in lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:279)
- cursor-pinned-across-a-new-settle : PRESENT (deftest in lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:331)
- default-window-opens-two-days : PRESENT (deftest in lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:416)
- rule-2-unavailable-is-not-green : PRESENT (deftest in lisp/nova-work/tests/replays-8649.lisp:120)

git status --short : (empty)
Noticed: E04-F06's :state in the sexp at line 800 is "missing" but :evidence is () while line 91 records 3 verified out of 3 tests with 4 test names. All 4 named tests exist in the tree. The subfeature text at line 794 matches the roadmap card verbatim. The spec covers both clauses fully — O/C separation is enforced by `--branch` selection and the architectural split (resident O tree vs paged closed index), and honest reporting of unavailable history is enforced by refusing with `historical window unavailable` rather than returning stale or empty results.
