RESULT work4s-E05-F01-02 sha=5298f6be12ea — nova-work E05-F01: does the contract say it? criterion E05-F01-02: Require matching criterion kind, exact subject and predicate
DONE
CRITERION E05-F01-02 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "matching criterion kind" docs/SPEC-WORK.md (0 hits)
grep -n "exact subject" docs/SPEC-WORK.md (0 hits)
grep -n "predicate" docs/SPEC-WORK.md (44 hits)
grep -n "kind matches" docs/SPEC-WORK.md (1 hit)
grep -n "qualifies" docs/SPEC-WORK.md (15 hits)
SPEC-WORK.md:936-941: "an evidence pointer qualifies a criterion only when its kind matches, **its subject is the criterion's subject** (a passing test of another name qualifies nothing) — its predicate holds at the named revision"
:by-feature E05-F01 tests: verify-job-criterion-reads-the-revision (exists in lisp/nova-work/tests/acceptance/slice-13-verifier.lisp), verify-resolver-identity-is-the-command (exists in lisp/nova-work/tests/acceptance/slice-13-verifier.lisp), a-removed-or-corrected-need-is-unmet (exists in lisp/nova-work/tests/replays-785-gate.lisp), regression-opens-repair-work (not found)
git status --short

Noticed The contract requires kind matching, exact subject matching, and predicate holding at the named revision, all stated verbatim.