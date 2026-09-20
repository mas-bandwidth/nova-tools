RESULT work4r3-E10-F04-62 sha=5298f6be12ea — nova-work E10-F04 criterion E10-F04-62 (sexp id E10-F04-01): Orchestrate process-level runs against temporary remotes and fake providers, including crash/restart, partition, stale owner and handoff
DONE
CRITERION E10-F04-01 STATE verified
BRANCH rowan/work4r3-E10-F04-62
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/control.lisp lisp/nova-work/src/package.lisp lisp/nova-work/tests/replays-8648.lisp
docs/SPEC-WORK.md:7226-7227 "(2) process-level fault injection and restart-and-replay against temporary Git remotes and fake providers, two-process fencing and interrupted I/O included."
TestE10F04OrchestrateProcessLevelRunsAgainst
NOVA-WORK SLICE1 total=419 pass=411 fail=8
TEST TestE10F04OrchestrateProcessLevelRunsAgainst FAIL spec=E10-F04-01 (docs/SPEC-WORK.md:7223-7227) expected=vacant-take-gen1;stale-owner-fenced;handoff-successor-takes-gen2;partition-refuses-the-stale-push;one-push-lands;provider-consulted-once;restart-answers-once-only: The function NOVA-WORK/TESTS::ORCHESTRATE-PROCESS-LEVEL-RUN is undefined.
NOVA-WORK SLICE1 total=420 pass=412 fail=8
NOVA-WORK SLICE1 total=420 pass=412 fail=8
NOVA-WORK SLICE1 total=420 pass=411 fail=9
TEST TestE10F04OrchestrateProcessLevelRunsAgainst FAIL spec=E10-F04-01 (docs/SPEC-WORK.md:7223-7227) expected=vacant-take-gen1;stale-owner-fenced;handoff-successor-takes-gen2;partition-refuses-the-stale-push;one-push-lands;provider-consulted-once;restart-answers-once-only: The function NOVA-WORK/TESTS::ORCHESTRATE-PROCESS-LEVEL-RUN is undefined.
M  lisp/nova-work/src/control.lisp
M  lisp/nova-work/src/package.lisp
M  lisp/nova-work/tests/replays-8648.lisp
head 7c713b3b6e2453be275a33cc8fda68c3fbc434d6
Noticed a pre-existing sibling test TestE10F04RunTheAuthorizedReadOnly (E10-F04-03) already lives in the suite; the src/replays-8648.lisp and other src/replays-*.lisp files are orphaned (never loaded by nova-work.asd) — the live code is folded into src/control.lisp etc.; neither was touched.
Left owed E10-F04-02 (collect release-level results from fencing/recovery/paging/as-of without re-owning their assertions) and E10-F04-03 (authorized read-only real-repository pilot, disposable import, reconciliation disposition) remain.
