RESULT work4s-E04-F05-01 sha=5298f6be12ea — nova-work E04-F05: does the contract say it? criterion E04-F05-01: Resolve settled dependencies and closed rows through indexed lookup
DONE
CRITERION E04-F05-01 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "settled dependencies" docs/SPEC-WORK.md | head -20 => 0 hits
grep -in "indexed lookup" docs/SPEC-WORK.md | head -20 => 13 hits (lines 36,709,7525; also lines 134,653,709-712,1667,2109,2473,2514,2719,2937,5331,5731,5876,5882,5895,6134,6322,7525,7858)
grep -n "indexed" docs/SPEC-WORK.md | head -20 => 20 hits (lines 36,134,653,709,712,1667,2109,2473,2514,2719,2937,5331,5731,5876,5882,5895,6134,6322,7525,7858)
grep -n "closure\|closed row\|closed-dependency\|depend.*resolve" docs/SPEC-WORK.md | head -20 => multiple hits across 30+ lines
grep -n "Resolve settled dependencies\|settled dependencies and closed rows" docs/SPEC-WORK.md | head -10 => 0 hits (no exact roadmap phrase match)
grep -n "E04-F05" docs/roadmaps/nova-work.sexp | head -10 => 2 hits (lines 90,778)

VERDICT: STATED

The spec covers both sub-clauses of the criterion without using the roadmap's exact phrasing:

**(a) Resolved settled dependencies via indexed lookup:**
docs/SPEC-WORK.md:709: `--ask` whose `--from` reaches before the window, and a **required indexed dependency lookup** —
docs/SPEC-WORK.md:710: rule 2 resolving a name that is in C, `released=` reading a settled release task through the
docs/SPEC-WORK.md:711: reverse-dependency index, a rollup reading a settled member's row — each of which is one bounded
docs/SPEC-WORK.md:712: indexed access for the one id it needs and never a day opened whole.

docs/SPEC-WORK.md:36: an explicit historical query or a required indexed dependency lookup; and **no history is ever deleted automatically**.

docs/SPEC-WORK.md:5731: resolution against C is a required indexed dependency lookup**, one of the two ways the

**(b) Closed rows through indexed lookup:**
docs/SPEC-WORK.md:5869: grouped by repository, so a rollup reads a settled member's newest row in one bounded lookup, a
docs/SPEC-WORK.md:5870: closed listing reads the pages of one revision range, and `--after` continues it. **A
docs/SPEC-WORK.md:5871: settled item therefore costs a row and never a body**: the O(V+E) of a full validation is O's
docs/SPEC-WORK.md:5872: edges plus one bounded lookup per settled member, and neither the fold nor the load grows with how
docs/SPEC-WORK.md:2179: parses=0 replays=0, because the closed rows came from the closed index and the open one from the resident tree, and `pages=4`
docs/SPEC-WORK.md:2180: because the index was read in pages and never loaded — **the same ask run against a set carrying a
docs/SPEC-WORK.md:2181: year of older closed history prints the same four rows, the same `pages=4` (if the depth is unchanged) and the same startup resident bytes**

docs/SPEC-WORK.md:7525: an explicit query or a required indexed lookup, the append-only per-transition closed rows and the

Cross-check: sexp line 90 lists these :tests for E04-F05:
- closed-paged-without-full-load → EXISTS at lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:499
- absent-day-is-not-a-gap → EXISTS at lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp:142
- missing-segment-is-a-gap → EXISTS at lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp:160
- as-of-refuses-unavailable-partition → EXISTS at lisp/nova-work/tests/acceptance/slice-09-replays-8603.lisp:17
- closed-row-with-archive-absent → EXISTS at lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:485
- rule-2-unavailable-is-not-green → EXISTS at lisp/nova-work/tests/replays-8649.lisp:120

All six named tests exist in the tree. The sexp entries at lines 90 and 778 confirm E04-F05's evidence references are populated with test names matching the three subfeatures (including this criterion's).

git status --short

Noticed: The spec uses "required indexed dependency lookup" rather than the roadmap's "indexed lookup", but defines it identically — it is the mechanism by which older history beyond the rolling window is reached without traversing days. Lines 709-712 enumerate exactly the three cases the criterion calls out: resolving names in C, reading settled release tasks through reverse-dependency index, and rollups reading settled member rows. Lines 2179-2181 and 5869-5872 provide the closed-index mechanics that make "closed rows through indexed lookup" concrete.
