RESULT work4p-E04-F01-01 sha=5298f6be12ea — nova-work E04-F01: does the green MOVE? criterion E04-F01-01: Maintain root open-item counters in mutation envelopes
DONE
CRITERION E04-F01-01 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
TEST lisp/nova-work/tests/acceptance/slice-01-reader.lisp (open-count-is-read-not-computed), lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp (cow-root-partition, findings-across-c-and-o), lisp/nova-work/tests/replays-8664.lisp (indexes-and-counters)
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
MUTATION-1 lisp/nova-work/src/state.lisp:320
BEFORE (incf (wstate-root-open state) delta)
AFTER  (incf (wstate-root-open state) 0)
MUTATION-1 COUNT NOVA-WORK SLICE1 total=419 pass=388 fail=31
MUTATION-1 RED TEST open-count-is-read-not-computed FAIL spec=docs/SPEC-WORK.md:3229 expected=visits=0,parses=0,replays=0,counter=independent-full-count,separate-counters=yes: |O| at the seed: expected 5 got 0
MUTATION-1 RED TEST cow-root-partition FAIL spec=docs/SPEC-WORK.md:1386-1389,5044 expected=open+closed=total;each-id-one-branch;second-settle-refused: seed |O|: expected 5 got 0
MUTATION-1 RED TEST findings-across-c-and-o FAIL spec=docs/SPEC-WORK.md:2022-2092,5099 expected=open=1-closed=3-four-ids-once: open=1: expected 1 got 0
MUTATION-1 RED TEST indexes-and-counters FAIL spec=docs/SPEC-WORK.md:6242 expected=counters-and-indexes-equal-reconstruction;closure-reopen-reparent-never-double-count: the real seeded counters and indexes agree with reconstruction: expected NIL got ("|O| maintained=0 reconstructed=6")
MUTATION-2 lisp/nova-work/src/state.lisp:645
BEFORE (%adjust-counters state id -1)
AFTER  (%adjust-counters state id 0)
MUTATION-2 COUNT NOVA-WORK SLICE1 total=419 pass=387 fail=32
MUTATION-2 RED TEST open-count-is-read-not-computed FAIL spec=docs/SPEC-WORK.md:3229 expected=visits=0,parses=0,replays=0,counter=independent-full-count,separate-counters=yes: |O| after the close: expected 4 got 5
MUTATION-2 RED TEST cow-root-partition FAIL spec=docs/SPEC-WORK.md:1386-1389,5044 expected=open+closed=total;each-id-one-branch;second-settle-refused: |O| after close: expected 4 got 5
MUTATION-2 RED TEST findings-across-c-and-o FAIL spec=docs/SPEC-WORK.md:2022-2092,5099 expected=open=1-closed=3-four-ids-once: open=1: expected 1 got 4
MUTATION-2 RED TEST indexes-and-counters FAIL spec=docs/SPEC-WORK.md:6242 expected=counters-and-indexes-equal-reconstruction;closure-reopen-reparent-never-double-count: real step 6 keeps counters and indexes equal: expected NIL got ("container r/g/three maintained=1 reconstructed=0"
RESTORED git status --short: (empty)
RESTORED COUNT NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed The baseline tree is red before any probe: fail=8, all environmental (endpoint-is-local-and-private Socket bind EACCES; seven session reqline tests cannot create /tmp/nova-work-reqline-*). None of the four named tests is among them; all four named tests PASS at baseline. Restored count equals baseline exactly, so the suite is not order-dependent or stateful across these runs.