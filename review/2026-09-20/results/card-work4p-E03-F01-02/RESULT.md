RESULT work4p-E03-F01-02 sha=5298f6be12ea — nova-work E03-F01: does the green MOVE? criterion E03-F01-02: Validate multi-node changes all-or-none
DONE
CRITERION E03-F01-02 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test=lisp/nova-work/tests/acceptance/slice-01-reader.lisp two-event-candidate-is-all-or-none
baseline=NOVA-WORK SLICE1 total=419 pass=411 fail=8
disabled="`(unless accepted`" at src/kernel.lisp:468
after-mutation=NOVA-WORK SLICE1 total=419 pass=404 fail=15
red line: TEST two-event-candidate-is-all-or-none FAIL spec=docs/SPEC-WORK.md:3348 expected=events=2,request-ids=1,on-reject:applied=0,digest=unchanged,history=unchanged,rows=unchanged,journal=empty: a rejected envelope was applied
second mutation=changed `(let ((candidate (copy-state state)))` to `(let ((candidate state))` at src/state.lisp:694
second result=NOVA-WORK SLICE1 total=419 pass=408 fail=11; two-event-candidate-is-all-or-none stayed GREEN (mutation affects acceptance path, not rejection path); 3 other tests went red: reconstructed-kernel-does-not-reissue-ids, durable-journal-replay-failure-isolates-target-kernel, batches-and-pipelines
restored status=(empty)
restored count=NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed the suite had 8 pre-existing failures all unrelated to all-or-none (socket permission denied, reqline temp-dir creation). Two mutations on different lines of the same all-or-none behaviour both cause test regressions: kernel.lisp:468 (journal-accept gate) triggers the named test; state.lisp:694 (state-copy isolation) triggers different but related replay/batch tests. The sexp's verified row is correct for this criterion.
