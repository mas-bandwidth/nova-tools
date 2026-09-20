RESULT work4s-E04-F05-03 sha=5298f6be12ea — nova-work E04-F05: does the contract say it? criterion E04-F05-03: Return gap, provenance or refusal rather than fabricate state
DONE
CRITERION E04-F05-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "fabricate" docs/SPEC-WORK.md -> 1 hit
grep -in "provenance" docs/SPEC-WORK.md -> 40 hits
grep -n "refusal" docs/SPEC-WORK.md -> 83 hits
grep -n "gap" docs/SPEC-WORK.md -> 52 hits
grep -in "invent" docs/SPEC-WORK.md -> 42 hits
grep -in "hallucinat" docs/SPEC-WORK.md -> 0 hits
grep -in "cannot know|does not know|doesn't know|do not know|does not have|cannot check|cannot read" docs/SPEC-WORK.md -> 8 hits
grep -n "E04-F05" docs/roadmaps/nova-work.sexp -> 2 hits
docs/SPEC-WORK.md:749  "day**: the answer is the rows there are, `gap=0`, and no note. **A manifest or a segment the"
docs/SPEC-WORK.md:750  "committed root names that is missing or corrupt is a coverage gap**: the listing prints the rows"
docs/SPEC-WORK.md:751  "it can answer, `gap=<n>` and one `QUERY NOTE coverage-gap file=<name> range=<rev>-<rev>`, and"
docs/SPEC-WORK.md:752  "never an empty closed set, because *a missing archive produces an honest coverage gap, not an"
docs/SPEC-WORK.md:753  "empty completed set* (Glenn, 23:34Z). **A query that asks what an item's state was, as of a window"
docs/SPEC-WORK.md:754  "end whose partition it cannot read, refuses rather than answering from a newer row**: exit 1,"
docs/SPEC-WORK.md:755  "`QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window unavailable`,"
docs/SPEC-WORK.md:756  "naming the one partition it would need, so the caller reads a refusal it can act on instead of a"
docs/SPEC-WORK.md:757  "number it cannot check. **A cached summary may answer an independent query with its provenance and"
docs/SPEC-WORK.md:758  "can never make missing evidence read as verified.** These three are one rule stated three ways: an"
docs/SPEC-WORK.md:759  "answer says which question it answered and over what it could actually read"
CORROBORATING
docs/SPEC-WORK.md:1775  "the index answers but whose bodies are gone prints **`gap=<n>`** and one `QUERY NOTE"
docs/SPEC-WORK.md:1776  "coverage-gap file=<name> range=<rev>-<rev>` and never an empty closed set: a missing archive is"
docs/SPEC-WORK.md:3279  "ones — a cache that recorded unknown is valid, a fact once recorded and now missing is not."
docs/SPEC-WORK.md:5982  "OPERATION FAIL id=<id> op=- state=-: no such operation   (an id the journal does not hold: exit 2, never an invented state)"
docs/SPEC-WORK.md:2735  "operation`, exit 2, never an invented `queued`."
docs/SPEC-WORK.md:583  "answer because the original `OK` line is not retained and an invented one would be a worse"
VERDICT STATED: docs/SPEC-WORK.md:747-759 states the criterion as one rule in three ways — a missing/corrupt archive prints gap=<n> and a QUERY NOTE coverage-gap, an unreadable as-of partition refuses with QUERY FAIL naming the partition, and a cached summary answers with its provenance — closing "an answer says which question it answered and over what it could actually read", i.e. gap, provenance or refusal rather than fabricating state.
EVIDENCE-BY-FEATURE: docs/roadmaps/nova-work.sexp:778-792 :by-feature entry for "E04-F05" records :state "missing" and :evidence () (no test names)
EVIDENCE-SUMMARY: docs/roadmaps/nova-work.sexp:90 (:feature "E04-F05" :verified 3 :total 3 :tests "closed-paged-without-full-load; absent-day-is-not-a-gap; missing-segment-is-a-gap; as-of-refuses-unavailable-partition; closed-row-with-archive-absent; rule-2-unavailable-is-not-green") — all six names exist in the tree:
  closed-paged-without-full-load -> lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp
  absent-day-is-not-a-gap -> lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp
  missing-segment-is-a-gap -> lisp/nova-work/tests/acceptance/slice-09-replays-publication.lisp
  as-of-refuses-unavailable-partition -> lisp/nova-work/tests/acceptance/slice-09-replays-8603.lisp
  closed-row-with-archive-absent -> lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp
  rule-2-unavailable-is-not-green -> lisp/nova-work/tests/replays-8649.lisp
git status --short: (empty)
Noticed the :by-feature entry for E04-F05 (nova-work.sexp:778) carries :state "missing" and empty :evidence, while the feature-summary line at nova-work.sexp:90 records E04-F05 as verified 3/3 with six test names; the two records disagree, and all six names named by the summary are present in the tree. Criterion is STATED in the contract; base confirmed at 5298f6be12eaa0f7e6622334d2b6a1eb427649e3, no writes made.