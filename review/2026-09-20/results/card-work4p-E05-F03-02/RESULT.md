RESULT work4p-E05-F03-02 sha=5298f6be12ea — nova-work E05-F03: does the green MOVE? criterion E05-F03-02: Support attested criteria with reviewer identity and result
DONE
CRITERION E05-F03-02 VERDICT UNEARNED
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST-FILE lisp/nova-work/tests/replays-785-gate.lisp
TEST-NAME an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
MUTATION-1 lisp/nova-work/src/verifier.lisp:263
  BEFORE: (and (stringp reviewer) (plusp (length reviewer)))
  AFTER:  t
  (the :attested branch's reviewer-identity requirement in verify-qualifies-p removed)
COUNT-AFTER-1 NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED-1 none — the named test still reports PASS
MUTATION-2 lisp/nova-work/src/verifier.lisp:264
  BEFORE: (equal (getf attestation :result-pointer) pointer)
  AFTER:  t
  (the :attested branch's result-pointer requirement in verify-qualifies-p removed)
COUNT-AFTER-2 NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED-2 none — the named test still reports PASS
RESTORED-STATUS (git status --short empty)
RESTORED-COUNT NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed the baseline suite is not 0-fail: 8 tests fail for environmental reasons only (socket bind returns Permission denied on endpoint-is-local-and-private; session-status-over-the-socket-with-its-flags, the-status-answer-is-the-grammars-full-session-ok-line, the-bare-in-process-spellings-still-answer, a-verb-the-session-does-not-answer-is-refused-by-name, a-refused-verb-answers-exit-two, a-request-with-no-verb-is-refused-saying-so, a-refusal-echoes-a-callers-verb-capped-and-with-no-control-characters fail because /tmp is not writable inside the sandbox — mkdir /tmp/nova-work-reqline-* returns Permission denied). These failures are present before, during and after the probes and are unrelated to E05-F03-02. The named test asserts the happy path of the :attested criterion (a cached result of the current generation meets the need; an unanswered result pointer and an older generation meet nothing; a different reviewer name changes nothing). Its evidence is always constructed with a present non-empty reviewer, a result-pointer equal to the evidence pointer and a revision equal to :against, so removing any one of the three required checks in src/verifier.lisp:263-265 cannot make any assertion fail: neither the reviewer-identity requirement nor the result-pointer match is individually asserted by any negative case. A test that would catch either removal must assert a refusal: an attestation record carrying no reviewer (or an empty reviewer) must meet nothing, and an attestation whose :result-pointer does not equal its evidence pointer must meet nothing.