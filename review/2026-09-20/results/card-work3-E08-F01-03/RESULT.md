RESULT work3-E08-F01-03 sha=f01a0c42d7de — nova-work E08-F01 acceptance criterion, criterion E08-F01-03 (docs/roadmaps/nova-work.sexp): Refuse stale discovery and invalid suggested actions
DONE
CRITERION E08-F01-03 STATE verified
BRANCH rowan/work3-E08-F01-03
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8644.lisp

SPEC-WORK line (primary) docs/SPEC-WORK.md:2219:
  "A request whose expectation is not the current value of its kind is refused
  at exit 1, `<MUTATION> FAIL node=<id> expect=<rev> current=<rev>: stale`,
  printing the current value so the requester can re-read and resubmit
  (5653982211: *apply rejects stale preconditions*)."
Second half of the criterion is pinned to docs/SPEC-WORK.md:2117 ("a dependent
with an unmet need ... is refused by every admission verb until its needs are
met") and docs/SPEC-WORK.md:2110 (`query ready`, the machine list of next valid
actions).

The literal phrase "stale discovery"/"suggested actions" does not appear in
SPEC-WORK.md (it exists only in the roadmap sexp); I mapped the two refusals to
the SPEC-WORK lines above, which state the same behaviour.

TEST: TestE08F01RefuseStaleDiscoveryAndInvalid (in replays-8644.lisp)
  - half 1 "stale discovery": a mutation whose --expect names a revision the
    live object has left is refused at exit 1, the line names `stale` and the
    current revision, and nothing is written (goal-store model).
  - half 2 "invalid suggested actions": `ws/d` needs still-open `ws/n`; it is
    not listed by `ready-nodes`, and `state --to doing` over it is refused at
    exit 1 naming the blocking need, moving nothing (live kernel).

Suite counts (run twice, identical):
  NOVA-WORK SLICE1 total=424 pass=416 fail=8
  NOVA-WORK SLICE1 total=424 pass=416 fail=8
  My test prints: TEST TestE08F01RefuseStaleDiscoveryAndInvalid PASS ...

My test is GREEN. The kernel already satisfies the criterion: stale preconditions
are refused and invalid next actions are refused/unlisted. The roadmap row
E08-F01-03 can be ticked once this test lands at the recorded revision.

The 8 failures are pre-existing and environmental, not this criterion: the
`request-line` suite writes under /tmp/nova-work-reqline-* and this sandbox
denies writes to /tmp (`touch /tmp/...` -> "Permission denied"). None touches my
file, and both runs report the identical 8.

git status --short (after commit): empty (only replays-8644.lisp was modified and committed).
head c724dd8d5e562622abe23120240891db45b61628
Left owed: the 8 request-line failures are a sandbox /tmp-permission issue, not
a code defect; on a bench with a writable /tmp they would go green, and no one
has mapped E08-F01-01/-02 (schema-for-help/examples, bounded family/verb help and
machine discovery with schema hash) — those remainder rows of E08-F01 are unverified.
