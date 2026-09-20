RESULT work4r3-E10-F07-68 sha=5298f6be12ea — nova-work E10-F07 criterion E10-F07-68 (sexp id E10-F07-03): Support session start --repair only when findings strictly decrease, preserving the unmodified source and emitting a repair diff
DONE
CRITERION E10-F07-03 STATE verified
BRANCH rowan/work4r3-E10-F07-68
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/session.lisp lisp/nova-work/src/package.lisp lisp/nova-work/tests/replays-8650.lisp
docs/SPEC-WORK.md:2430 "`session start --repair` loads and validates the same way, prints `SESSION OK … findings=<n>`, and admits mutations under a different gate — the whole validation ... the event accepted only when its finding count is strictly below the current one and refused otherwise at exit 1, `<MUTATION> FAIL node=<id> findings=<n> was=<n>: no repair`." (also :2438 "A red load's own exit is 1, with or without `--repair`")
TestE10F07SupportSessionStartRepairOnly
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE10F07SupportSessionStartRepairOnly FAIL spec=docs/SPEC-WORK.md:2430 expected=red-load-names-its-findings;source-unmodified;strict-decrease-admitted;otherwise-refused-with-no-repair: Unknown &KEY argument: :REPAIR
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
NEGATIVE-CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9
NEGATIVE-CONTROL TEST TestE10F07SupportSessionStartRepairOnly FAIL spec=docs/SPEC-WORK.md:2430 expected=red-load-names-its-findings;source-unmodified;strict-decrease-admitted;otherwise-refused-with-no-repair: Unknown &KEY argument: :REPAIR
GIT-STATUS  M lisp/nova-work/src/package.lisp
GIT-STATUS  M lisp/nova-work/src/session.lisp
GIT-STATUS  M lisp/nova-work/tests/replays-8650.lisp
head 7bf87aa375666f44cde8dbba1e3da7bd89eeb6aa
Noticed The whole-state validator of SPEC-WORK.md:5713 (rules 1-17/19) is not implemented in this slice; the only whole-load finding check available is rule 18 (cow-load-findings), so --repair loads and gates on that count alone. The repair gate is exposed as session-repair-gate and not yet wired into session-submit's mutation path (no per-event candidate construction here yet), and the resident session server (start-session-server) does not yet plumb --repair through to the CLI launch of session start.
Left owed Wire --repair from the CLI `session start` verb through the resident server, run the full validator (rules 1-19) at load so findings= is not just rule 18, apply the repair gate inside the per-mutation candidate admission (constructing the candidate O as it would be with the event applied), and print `SESSION NOTE repaired findings=0` on the transition to zero.
