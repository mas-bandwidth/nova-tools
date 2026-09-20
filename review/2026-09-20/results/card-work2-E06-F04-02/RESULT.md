RESULT work2-E06-F04-02 sha=a3abdd4ad6dd — nova-work E06-F04 acceptance criterion, criterion E06-F04-02 (docs/roadmaps/nova-work.sexp): Compare prior checkpoint to current state and report gaps
DONE
CRITERION E06-F04-02 STATE verified
BRANCH rowan/work2-E06-F04-02
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8663.lisp

SPEC-WORK line (docs/SPEC-WORK.md:7085):
  | `recovery` | restore the newest valid savepoint plus journal; reject a corrupt savepoint; recover from a prior savepoint **without silent loss**; compare an isolated old restore against current state; a missing tail or an unavailable remote backup **reported as a recovery gap** |

TEST: TestE06F04ComparePriorCheckpointToCurrent (docs/SPEC-WORK.md:7085), added to lisp/nova-work/tests/replays-8663.lisp.

The kernel already satisfies the criterion: `savepoint-compare` (lisp/nova-work/src/savepoint.lisp:197) puts the prior checkpoint's `:local-revision` and the current state's `:against-revision` side by side and reports the gaps — `:missing` names each revision the current state holds beyond the prior checkpoint, and `:unshared` names the work above the shared checkpoint still inside the prior checkpoint, with the shared checkpoint a separate field. The new test asserts exactly that behaviour against a prior savepoint at rev 812 compared to a current session at rev 820 (shared checkpoint 750), and pins the gap revisions (819 820) one-by-one rather than a folded count.

Suite (./run-tests.sh), two runs:
  NOVA-WORK SLICE1 total=408 pass=400 fail=8
  NOVA-WORK SLICE1 total=408 pass=400 fail=8

The two counts agree, so the suite is not order-dependent. My test is GREEN. The 8 failures are pre-existing sandbox/environment refusals (a network `bind` permission denied and `Can't create directory /tmp/nova-work-reqline-*`), all unrelated to this criterion and present before my change. Roadmap row E06-F04-02 is genuinely already satisfied and can now be ticked: the earlier 2-of-3 verified count named only isolate/keep-originals replays, and this criterion's compare-and-report-gaps was unrecorded rather than unmet.

git status --short:
   M lisp/nova-work/tests/replays-8663.lisp

head b2b9ce042b96fab8453534eeb6d9fc0882779a6f
Left owed: none.
