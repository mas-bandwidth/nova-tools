RESULT work4p-E03-F05-01 sha=5298f6be12ea — nova-work E03-F05: does the green MOVE? criterion E03-F05-01: Create typed compensating envelopes from preimages
DONE
CRITERION E03-F05-01 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST-FILE lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp
TEST-NAME undo-appends-and-preserves
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
DISABLED-LINE src/operations.lisp:1122
  before: :kind compensating
  after:  (removed entirely)
MUTATION-COUNT NOVA-WORK SLICE1 total=419 pass=410 fail=9
RED-OUTPUT "TEST undo-appends-and-preserves FAIL spec=docs/SPEC-WORK.md:5643-5644 expected=compensating-envelope-with-lineage;original-event-and-receipts-untouched: the compensating envelope is typed by the table: expected :NODE-REMOVE got NIL"
SECOND-MUTATION none needed (first mutation turned the named test red)
RESTORED-STATUS (empty)
RESTORED-COUNT-LINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
NOTICED The baseline has 8 failures (pass=411, fail=8). All 8 are environment-related: "Permission denied" on socket bind and "Can't create directory /tmp/nova-work-reqline-*". The relevant tests (undo-appends-and-preserves, redo-refuses-a-stale-plan, edit-undo-preserves-later-work, undo-refuses-an-external-effect, undo-names-its-reversible-set) all pass in the baseline.