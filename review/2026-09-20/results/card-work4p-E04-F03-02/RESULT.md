RESULT work4p-E04-F03-02 sha=5298f6be12ea — nova-work E04-F03: does the green MOVE? criterion E04-F03-02: Report open plus closed totals without double membership
DONE
CRITERION E04-F03-02 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test file: lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp
test name: cow-root-partition (asserts "open plus closed is the counted total")
baseline: NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled line: lisp/nova-work/src/state.lisp:321
  before: (decf (wstate-closed state) delta)
  after:  ;;(decf (wstate-closed state) delta)
count after mutation: NOVA-WORK SLICE1 total=419 pass=400 fail=19
red output verbatim: TEST cow-root-partition FAIL spec=docs/SPEC-WORK.md:1386-1389,5044 expected=open+closed=total;each-id-one-branch;second-settle-refused: open plus closed is the counted total: expected 5 got 4
second mutation: none (first mutation already proved the criterion)
restored git status --short: (empty)
restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed: baseline is already red (fail=8) before any mutation: endpoint-is-local-and-private (Socket error in "bind": 13 Permission denied) plus seven session/request-line tests failing with "Can't create directory /tmp/nova-work-reqline-..." — all environmental (no /tmp write / bind permission in this sandbox), unrelated to E04-F03. The 11 extra failures after mutation were all |C| counter checks across cow-root-partition, open-count-is-read-not-computed, reopen-revives, findings-across-c-and-o, revive-appends-and-counts-latest, indexes-and-counters, container-cascade-journal-rejection-and-retry-stability, second-settle-through-submit-keeps-container-history, remove-settles-only-open-items, index-replayed-after-crash, many-callers-on-the-one-writer-leave-one-serial-order — consistent with the disabled line being the write that moves a settled id into the closed count.