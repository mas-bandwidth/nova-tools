RESULT work4p-E06-F02-03 sha=5298f6be12ea — nova-work E06-F02: does the green MOVE? criterion E06-F02-03: Maintain rolling 24-hour default window with at most two day partitions; pending SPEC-WORK.md@a0cfcf5 correction adds noon/midnight/over-limit witnesses while explicit historical queries remain bounded
DONE
CRITERION E06-F02-03 VERDICT HOLDS
REPO mas-bandwidth/nova-tools
NO-BRANCH
Test file: lisp/nova-work/tests/acceptance/slice-04-doing-and-journal.lisp
Test name: default-window-opens-two-days
Baseline: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Mutation #1 disabled: closed-history.lisp:359 — "(if (stamp-midnight-p now)" changed to "(if nil …" so midnight no longer yields a single-partition window
Mutation #1 result: NOVA-WORK SLICE1 total=419 pass=410 fail=9
Red output verbatim: TEST default-window-opens-two-days FAIL spec=docs/SPEC-WORK.md:5561 expected=at-most-two-day-partitions;midnight=one;no-partition-older-than-window;--from=only-days-holding-records: at midnight the window opens exactly one partition: expected ("2026-09-14") got ("2026-09-14" "2026-09-15")
Mutation #2: closed-history.lisp:350 — removed "(string< d to)" bound in %present-days-in-range, leaving only "(string<= from d)"
Mutation #2 result: NOVA-WORK SLICE1 total=419 pass=411 fail=8 (unchanged — test data is too narrowly bounded to expose over-fetching when only 3 history rows exist)
Restored git status --short: (empty)
Restored count line: NOVA-WORK SLICE1 total=419 pass=411 fail=8
Noticed all 8 pre-existing failures are permission/socket-related (session-dir 0700, socket 0600, Can't create directory /tmp/nova-work-reqline-*). They do not touch closed-history or window-days logic. The midnight guard on line 359 is necessary and sufficient: removing it causes the named test to go red immediately. The historical-query bound removal did not fire because the test fixture contains exactly three contiguous days within the query range; an over-limit witness test would need older history beyond the :to sentinel to catch that particular fault.
