RESULT work4-E10-F01-58 sha=5298f6be12ea — nova-work E10-F01 criterion E10-F01-58 (sexp id E10-F01-01): Attach stable error code, stage, verb, request/operation ID and known revisions
DONE
CRITERION E10-F01-01 STATE verified
BRANCH rowan/work4-E10-F01-58
REPO mas-bandwidth/nova-tools
PATHS (none changed — criterion already verified and pinned in the base tree)
docs/SPEC-WORK.md:2682 "response is `{\"request\": \"<id>\", \"ok\": true|false, \"exit\": \"<0|1|2>\", \"lines\": [ … ], \"rev\": \"<n>\", \"pushed\": \"<rev>|-\"}`" (and the siblings it names: :2695 "Every ordinary response echoes its request id", :2727-2730 the OPERATION FAIL id=/op=/state=/reason line, Output grammar :5921-5934 the verb as every line's first token and rev=/pushed= as the two known revisions)
TestE10F01AttachStableErrorCodeStage
baseline: NOVA-WORK SLICE1 total=419 pass=410 fail=9
RED (STEP 4): none — the criterion-fixing test was already GREEN at base with no production change, so this is the STEP-4 "already met" case, not a red-then-green.
GREEN (STEP 5): NOVA-WORK SLICE1 total=419 pass=410 fail=9 (run 1), NOVA-WORK SLICE1 total=419 pass=410 fail=9 (run 2) — identical; the 9 failures are all pre-existing environmental sandbox refusals (2 socket `bind` EPERM, 7 "Can't create directory /tmp"), none touching E10-F01.
NEGATIVE CONTROL (STEP 6): skipped — no production change was made, so there is nothing to revert; the green is earned by pre-existing production code.
git status --short: (clean) lisp/nova-work/nova-work.asd does NOT appear; nothing staged or modified.
head 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
Noticed The criterion is a duplicate of a card already landed in the base. Commit cb0fc593 "nova-work: pin E10-F01 criterion — attach-stable-error-code-stage-verb-requ" (card-tools20-work-E10-F01-58, merged via integration-16aq) added the identical deftest `TestE10F01AttachStableErrorCodeStage` to `lisp/nova-work/tests/acceptance.lisp` (not the `replays-8660.lisp` this card names), finding it already GREEN. That test is present at the pinned base and passes. The card's own TEST path (`replays-8660.lisp`) and its claim "No previous card has measured this criterion" are both stale. The production code that satisfies the reading is `src/transport.lisp` (wire-frame-response, wire-session-mutate), `src/operations.lisp` (make-operation-registry, registry-operation-state) and `src/replays-wire-and-operations.lisp` — all present before the test. I added no duplicate test (it would shadow the existing one and never be red) and changed no production code. Also: `docs/roadmaps/nova-work.sexp` still records E10-F01 as `:verified 1 :total 3` and `:state "missing"`, and ROADMAP.md:974 still renders the criterion unchecked, even though criteria E10-F01-01 and E10-F01-03 both carry passing pinned tests at base.
Left owed A follow-up card must refresh the roadmap record (docs/roadmaps/nova-work.sexp and the ROADMAP.md view) to mark E10-F01-01 (and E10-F01-03, `provide-bounded-inspect-diagnose-drill`) verified with their test names, and correct the stale `:verified 1 :total 3` count to 3/3.
