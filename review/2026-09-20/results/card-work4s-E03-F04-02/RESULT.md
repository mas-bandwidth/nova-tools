RESULT work4s-E03-F04-02 sha=5298f6be12ea — nova-work E03-F04: does the contract say it? criterion E03-F04-02: Bump task generation on correction and invalidate older evidence
DONE
CRITERION E03-F04-02 SPEC STATED

REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "bump" docs/SPEC-WORK.md: 17 hits
grep -n "generation" docs/SPEC-WORK.md: 55 hits
grep -n "correction" docs/SPEC-WORK.md: 18 hits
grep -n "evidence of an older" docs/SPEC-WORK.md: 2 hits

docs/SPEC-WORK.md:139: | an old attempt's result quietly satisfying a corrected task (5653982211) | a task carries a `:generation`; a `correct` event bumps it; evidence of an older generation cannot close the task |

:by-feature test names for E03-F04:
- correct-is-a-linked-segment: EXISTS (lisp/nova-work/tests/replays-8640.lisp:162)
- regression-opens-repair-work: EXISTS (lisp/nova-work/tests/replays-8648.lisp:16)
- reopen-revives: EXISTS (lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:503)
- stop-is-a-hold-not-a-cancel: EXISTS (lisp/nova-work/tests/acceptance/slice-09-replays-holds.lisp:9)
- containers-settle-with-their-members: EXISTS (lisp/nova-work/tests/acceptance/slice-02-close-and-counters.lisp:278)
- completed-view-mutation: EXISTS (lisp/nova-work/src/replays-8621.lisp:156)

git status --short: (empty)
Noticed The spec explicitly states the criterion at line 139 with precise wording matching the roadmap requirement.
