RESULT work4p-E05-F02-01 sha=5298f6be12ea — nova-work E05-F02: does the green MOVE? criterion E05-F02-01: Verify every evidence event, including those not named by done
DONE
CRITERION E05-F02-01 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
test-file=lisp/nova-work/tests/replays-785-gate.lisp test-name=a-done-need-is-unverified-without-complete-proof
baseline="NOVA-WORK SLICE1 total=419 pass=411 fail=8"
disabled="**         (every (lambda (event-id) t)**)" at src/needs.lisp:172-174
after-mutation="NOVA-WORK SLICE1 total=419 pass=405 fail=14"
red-output="TEST a-done-need-is-unverified-without-complete-proof FAIL spec=docs/SPEC-WORK.md:4780 expected=every-id-the-standing-done-names-is-bound-and-verified-or-the-need-is-unverified: a missing view proves nothing"
second-mutation=none---the first mutation already proved it because the named test went RED
restored-git-status="(empty)"
restored-count="NOVA-WORK SLICE1 total=419 pass=411 fail=8"
Noticed 5 additional tests also went red from this one edit: every-unmet-need-has-one-reason, a-done-need-on-unverified-evidence-admits-nothing, an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less, a-container-need-is-met-with-its-members, a-removed-or-corrected-need-is-unmet. All relate to %need-evidence-verified-p and rule 1's verification gate, confirming this single line is the core check for the criterion. The 8 pre-existing failures are all socket/permission environmental issues unrelated to needs logic.