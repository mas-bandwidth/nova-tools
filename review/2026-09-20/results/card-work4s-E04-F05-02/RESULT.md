RESULT work4s-E04-F05-02 sha=5298f6be12ea — nova-work E04-F05: does the contract say it? criterion E04-F05-02: Distinguish absent days, missing segments and unavailable as-of partitions
DONE
CRITERION E04-F05-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH

SEARCH 1: grep -n "Distinguish absent days, missing segments and unavailable as-of partitions" docs/SPEC-WORK.md | head -20 → 0 hits
SEARCH 2: grep -in "absent day\|missing segment\|unavailable as-of\|as-of partition" docs/SPEC-WORK.md | head -20 → 2 hits (lines 747, 7527)
SEARCH 3: grep -n "absent\|missing\|unavailable\|as-of\|segment" docs/SPEC-WORK.md | head -40 → numerous hits
SEARCH 4: grep -n "as-of.*unavail\|unavail.*as-of\|unavailable.*partition\|partition.*unavail\|as-of.*partition" docs/SPEC-WORK.md | head -20 → 1 hit (line 755)
SEARCH 5: grep -n "day.*gap\|gap.*day\|segment.*gap\|gap.*segment\|partition.*refus\|refus.*partition" docs/SPEC-WORK.md | head -20 → multiple hits

SETTLED LINE: docs/SPEC-WORK.md:747-760

  **An absent day and a missing segment are two different answers, and the manifest is what tells
  them apart.** A day with no manifest inside a complete manifested range **means no events that
  day**: the answer is the rows there are, `gap=0`, and no note. **A manifest or a segment the
  committed root names that is missing or corrupt is a coverage gap**: the listing prints the rows
  it can answer, `gap=<n>` and one `QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>`, and
  never an empty closed set, because *a missing archive produces an honest coverage gap, not an
  empty completed set* (Glenn, 23:34Z). **A query that asks what an item's state was, as of a window
  end whose partition it cannot read, refuses rather than answering from a newer row**: exit 1,
  `QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable`,
  naming the one partition it would need, so the caller reads a refusal it can act on instead of a
  number it cannot check. **A cached summary may answer an independent query with its provenance and
  can never make missing evidence read as verified.** These three are one rule stated three ways: an
  answer says which question it answered and over what it could actually read (replays
  `absent-day-is-not-a-gap`, `missing-segment-is-a-gap`, `as-of-refuses-unavailable-partition`).

VERDICT: STATED — The spec explicitly distinguishes all three:
1. absent day (line 748-749: no-manifest → `gap=0`, no note)
2. missing segment (line 749-751: manifest/segment missing → `gap=<n>` + `QUERY NOTE coverage-gap`)
3. unavailable as-of partition (line 753-755: as-of query on unreadable partition → exit 1 `QUERY FAIL … historical window unavailable`)

BY-FEATURE TEST NAMES (docs/roadmaps/nova-work.sexp:90):
- closed-paged-without-full-load → found in lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp:731 and slice-04-doing-and-journal.lisp:349
- absent-day-is-not-a-gap → found in lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp:142
- missing-segment-is-a-gap → found in lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp:160
- as-of-refuses-unavailable-partition → found in lisp/nova-work/tests/acceptance/slice-09-replays-8603.lisp:17
- closed-row-with-archive-absent → found in lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp:485
- rule-2-unavailable-is-not-green → found in lisp/nova-work/tests/replays-8649.lisp:120

All six named tests exist in the tree. No stale evidence.

git status --short: (empty)

Noticed: The sexp `:features` entry (lines 778-789) has `:state "missing"` and `:evidence ()` despite the `:by-feature` entry (line 90) showing 3/3 verified with 6 named tests. The feature is marked "missing" in the detailed feature tree but "verified 3 of 3" in the summary row.