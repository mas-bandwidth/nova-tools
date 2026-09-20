RESULT work4s-E03-F04-03 sha=5298f6be12ea — nova-work E03-F04: does the contract say it? criterion E03-F04-03: Keep reopen, pause and stop as durable events
DONE
CRITERION E03-F04-03 SPEC ABSENT
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "durable events" docs/SPEC-WORK.md | head -20: 0 hits
grep -n "reopen" docs/SPEC-WORK.md | head -20: 21 hits
grep -n "pause\|stop" docs/SPEC-WORK.md | head -20: 31 hits
grep -n "E03-F04" docs/roadmaps/nova-work.sexp | head -10: 8 hits
No SPEC line states "Keep reopen, pause and stop as durable events"; the closest is EXECUTION OK at line 6020 which mentions "durable intent" for pause|stop|resume|correct|reconcile but not as a durable events criterion.
E03-F04 by-feature test names: correct-is-a-linked-segment, regression-opens-repair-work, reopen-revives, stop-is-a-hold-not-a-cancel, containers-settle-with-their-members, completed-view-mutation
None of these test names exist in lisp/nova-work/tests/ (35 .lisp files found, none match)
git status --short: (empty)
Noticed: E03-F04 has :state "missing" and :evidence () in the sexp, confirming tests are absent; roadmap line 709 has the criterion but no contract line exists.
