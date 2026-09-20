RESULT work4m-E03-F03-23 sha=5298f6be12ea — nova-work E03-F03 criterion E03-F03-23 (sexp id E03-F03-02): Record author, reason, scope revision and exact member delta
DONE
CRITERION E03-F03-02 STATE verified
BRANCH rowan/work4m-E03-F03-23
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/roadmap.lisp lisp/nova-work/tests/acceptance.lisp
SPEC docs/SPEC-WORK.md:1092 "the scope log: a baseline records the required set of its node **as the tool computed it at that moment**, member by member; a split names the new children; a supersede names `:superseded-by`; every one of them increments the node's scope revision, because the revision is the count of scope events, and each changes the required set by **its own stated delta, in the table below, which is what rule 11 compares against**."
TEST TestE03F03RecordAuthorReasonScopeRevision
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE03F03RecordAuthorReasonScopeRevision FAIL spec=docs/SPEC-WORK.md:1092 expected=scope-event-records-author+reason+scope-revision+exact-member-delta: Unknown &KEY argument: :BY
RED COUNT NOVA-WORK SLICE1 total=420 pass=411 fail=9
GREEN TEST TestE03F03RecordAuthorReasonScopeRevision PASS spec=docs/SPEC-WORK.md:1092 expected=scope-event-records-author+reason+scope-revision+exact-member-delta
GREEN COUNT NOVA-WORK SLICE1 total=420 pass=412 fail=8
CONTROL TEST TestE03F03RecordAuthorReasonScopeRevision FAIL spec=docs/SPEC-WORK.md:1092 expected=scope-event-records-author+reason+scope-revision+exact-member-delta: Unknown &KEY argument: :BY
CONTROL COUNT NOVA-WORK SLICE1 total=420 pass=411 fail=9
STATUS git status --short:
 M lisp/nova-work/src/roadmap.lisp
 M lisp/nova-work/tests/acceptance.lisp
head 869de8863dc1e41577b03a4e0811fb692d5c667e
Noticed The previous card's finding (no author, no reason on a scope event) is confirmed at this revision. `scopes-on-move` still had no `:by` and no `:reason`. The baseline shows `correct-is-a-linked-segment` PASSES here (the card text said it was a pre-existing red; the tree has moved), and the eight baseline failures are the described bench failures (reqline /tmp + unix-socket bind).
Left owed Wire the `:by`/`:reason` through the real `node move` verb that calls `scopes-on-move`, so the author and reason travel from the request into the emitted scope events (the pure kernel now accepts and records them; the caller-side wiring is not yet exercised end-to-end).
