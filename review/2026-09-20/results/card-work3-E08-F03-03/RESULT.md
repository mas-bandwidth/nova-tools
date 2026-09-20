RESULT work3-E08-F03-03 sha=f01a0c42d7de34acd4cea8199e2f35510c50cde5 — nova-work E08-F03 acceptance criterion, criterion E08-F03-03 (docs/roadmaps/nova-work.sexp): Include coordinator identity and attribute model, bench, attempt and usage
DONE
CRITERION E08-F03-03 STATE verified
BRANCH rowan/work3-E08-F03-03
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/tests/replays-8646.lisp

SPEC-WORK line found:
  docs/SPEC-WORK.md:3379-3380 — "The coordinator is a friend in `friends` and is tracked by the same indexes as everyone else — her or his own tasks, executions, model, bench, usage and availability, through the same queries."
  docs/SPEC-WORK.md:3406-3408 — an execution instance is "linked to their friend, capability, canonical task and attempt, retaining requested and observed model, bench, status, deadline, provider handle and usage receipts".

Test name: TestE08F03IncludeCoordinatorIdentityAndAttribute (in lisp/nova-work/tests/replays-8646.lisp)

Suite counts (two runs, identical — not order-dependent):
  NOVA-WORK SLICE1 total=424 pass=416 fail=8
  NOVA-WORK SLICE1 total=424 pass=416 fail=8

Test result: GREEN.
  TEST TestE08F03IncludeCoordinatorIdentityAndAttribute PASS spec=docs/SPEC-WORK.md:3379-3380,3406-3408 expected=coordinator-included-among-friends;model,bench,attempt,usage-attributed-distinctly

Meaning for the roadmap row: the kernel already satisfies E08-F03-03. The
   subfeature is asserted by a landed test, so the "missing" row in
   docs/roadmaps/nova-work.sexp can be ticked once this change lands. No
   production change was needed.

The 8 failures are pre-existing environment failures, unrelated to this
   criterion and to replays-8646.lisp:
     1 x endpoint-is-local-and-private — "Socket error in bind: 13 (Permission denied)" (sandbox net=nopromise)
     7 x a-*-request-line/status tests — "Can't create directory /tmp/nova-work-reqline-*" (sandbox write restriction outside the job dir)

git status --short (after commit, one path only):
   (clean; the only change was lisp/nova-work/tests/replays-8646.lisp, committed)

head 51ebde014e780cba4d6c9c9d2ae1e69dc363a717

Left owed
