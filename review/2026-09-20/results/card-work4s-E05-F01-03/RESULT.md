RESULT work4s-E05-F01-03 sha=5298f6be12ea — nova-work E05-F01: does the contract say it? criterion E05-F01-03: Make correction generation invalidate prior qualification
DONE
CRITERION E05-F01-03 SPEC DRIFTED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "correction generation" docs/SPEC-WORK.md | head -20: 0 hits
grep -n "invalidate prior qualification" docs/SPEC-WORK.md | head -20: 0 hits
grep -n "qualification" docs/SPEC-WORK.md | head -20: 2 hits
SPEC-WORK.md:941-943: so a `correct` event makes every earlier qualification void for the done claim (Stella's finding 2, comment 5654659093)
SPEC-WORK.md:6122-6124: a `correct` event voiding an earlier qualification for the done claim; a correction bumping the generation and an older attempt's result refused at `state --to done`
SEXP E05-F01 tests: verify-job-criterion-reads-the-revision (exists), verify-resolver-identity-is-the-command (exists), a-removed-or-corrected-need-is-unmet (exists), regression-opens-repair-work (exists)
git status --short: (empty)
Noticed The roadmap uses "correction generation" while the spec uses "`correct` event"; roadmap says "invalidate" while spec says "void"