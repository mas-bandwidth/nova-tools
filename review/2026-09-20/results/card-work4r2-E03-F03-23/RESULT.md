RESULT work4r2-E03-F03-23 sha=5298f6be12ea — nova-work E03-F03 criterion E03-F03-23 (sexp id E03-F03-02): Record author, reason, scope revision and exact member delta
DONE
CRITERION E03-F03-02 STATE verified
BRANCH rowan/work4r2-E03-F03-23
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/roadmap.lisp, lisp/nova-work/tests/decide.lisp
docs/SPEC-WORK.md:956;1094-1105 — "`:by` is the event's author on every kind and never anything else"; "every one of them increments the node's scope revision, because the revision is the count of scope events, and each changes the required set by its own stated delta, in the table below, which is what rule 11 compares against"; "Each scope kind's own fields, in the order the payload digest above serializes them" (each carrying `:reason`)
TestE03F03RecordAuthorReasonScopeRevision
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE03F03RecordAuthorReasonScopeRevision FAIL spec=docs/SPEC-WORK.md:956;1094-1105 expected=scope-event-records-author-reason-scope-revision-and-member-delta: Unknown &KEY argument: :BY
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9
CONTROL-FAIL TEST TestE03F03RecordAuthorReasonScopeRevision FAIL spec=docs/SPEC-WORK.md:956;1094-1105 expected=scope-event-records-author-reason-scope-revision-and-member-delta: Unknown &KEY argument: :BY
git status --short:
 M lisp/nova-work/src/roadmap.lisp
 M lisp/nova-work/tests/decide.lisp
head 72b3d0e45e0ce197ed131cbc6511291db64ae9aa
Noticed: the same author/reason gap is present on the other scope-event writers — `axis` accepts a `by` argument but `(declare (ignore reason))`s it and records no author on its `:axis-log` scope event; `roadmap-row` records `:reason` and the member in its view log but no `:by` author. The `:scope` event kind also has no row in `*kind-field-order*` (src/event.lisp), so these raw roadmap scope events are not yet canonicalized to the full event record form. Left those untouched, out of this card's scope.
Left owed: record the author (and, where missing, the reason) on the remaining scope-event writers — `roadmap-row` (no `:by`) and `axis` (ignores `reason`, no `:by`) — so every scope event carries author, reason, scope revision and exact member delta; and give the `:scope` kind an ordered field list so the scope events round-trip through the journal's canonical record form.
