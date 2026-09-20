RESULT spec-work-c-r2 sha=5298f6be12ea — SPEC-WORK.md has no "Tests this spec demands" section. Read the spec, say which demanded behaviours the tree already tests and which it does not, and draft the issues for the ones it does not.
DONE

<<<SECTION
## Tests this spec demands

Every test runs against a Lisp work session with a journal and a snapshot; the Go client tests use
a fake Unix socket that returns canned replies. No network, no git push, no secrets. Each test is
proven able to fail by a mutation before it is trusted.

1. `TestPercentZeroApplicable` (in slice-09-replays-roadmap.lisp) — "percent over zero applicable rows prints green=0 applicable=0 and no percentage, and exits 0" (saved line 13-15, docs/SPEC-WORK.md:1945-1947).
2. `TestPercentWithoutAxisRefused` (in slice-09-replays-roadmap.lisp and s8_test.go) — "percent without --axis is refused at exit 2 naming the flag" (saved line 14-19, docs/SPEC-WORK.md:1946-1951).
3. (ABSENT) "remaining prints deferred=<n>, cancelled=<n> and superseded=<n> kept apart" (saved line 26-29, docs/SPEC-WORK.md:1958-1961).
4. (ABSENT) "since-baseline= counts gross: members a :discovery added since last :baseline, counted even if later cancelled" (saved line 39-56, docs/SPEC-WORK.md:1971-1988).
5. (ABSENT) "Units labelled on every line: unit=features, unit=epics, unit=work-sets" (saved line 57-76, docs/SPEC-WORK.md:1989-2008).
6. `TestRule18FindsTheLatestRow` (in replays-8660.lisp) and `TestOpenPlusClosedEqualsTotal` (in slice-02-close-and-counters.lisp, slice-05-durable-journal.lisp) — "open=<n> + closed=<n> = total, no id in both" (saved line 77-81, docs/SPEC-WORK.md:2009-2013).
7. (ABSENT) "closed-in=<n>, settles-in=<n>, revives-in=<n>, items-in=<n> printed where a window is given" (saved line 81-88, docs/SPEC-WORK.md:2013-2020).
8. `TestClosedRowWithArchiveAbsent` (in slice-04-doing-and-journal.lisp) — "gap=<n> printed where body retention archive holds" (saved line 89-93, docs/SPEC-WORK.md:2021-2025).
9. `TestOpenCountIsReadNotComputed` (in slice-01-reader.lisp) — "|O| is a counter carried by every accepted mutation envelope and is read, never computed" (saved line 98-111, docs/SPEC-WORK.md:2030-2043).
10. `TestSizePrintsOpenUnitScope` (in slice-01-reader.lisp, slice-15-staged-admission.lisp, replays-8664.lisp) — "query --ask size prints open=<n>, unit=<unit>, scope=<rev>" (saved line 112-115, docs/SPEC-WORK.md:2044-2047).
11. (ABSENT) "done-unverified=<n> is the part of unknown= that is a recorded done standing on unverified or stale evidence" (saved line 121-123, docs/SPEC-WORK.md:2053-2055).
12. `TestQueryFriendsOverSocket` (in s8_test.go) and `TestQueryOKBeforeROWS` (in slice-08-replays-late.lisp) — "Every answer computed whole before printing, one QUERY OK scope line and QUERY ROW lines per fact" (saved line 127-129, docs/SPEC-WORK.md:2059-2061).
13. `queryFriendsOK` fixture (in s8_test.go) — "On every --ask: scope revision, membership, branch, unit, source, freshest, done=, done-unverified=, unknown=, deferred=, cancelled=, superseded=, stale=, required=, since-baseline=, private=, rows=, open=, closed=, gap=, shown=, parses=, replays=, emitted=" (saved line 130-136, docs/SPEC-WORK.md:2062-2068).
14. (ABSENT) "from=, to=, closed-in=, settles-in=, revives-in=, items-in= wherever a window was given" (saved line 135-137, docs/SPEC-WORK.md:2067-2069).
15. `TestPageBudgetIsNotMax` and `TestDefaultWindowOpensTwoDays` (in slice-04-doing-and-journal.lisp) — "explicit historical query prints pages=<n>" (saved line 137-141, docs/SPEC-WORK.md:2069-2073).
16. `TestPercentFields` (in slice-09-replays-roadmap.lisp) — "percent adds green=, applicable=, baseline-rows= and row-kind=" (saved line 142-143, docs/SPEC-WORK.md:2074-2075).
17. (ABSENT) "who and stale add held-not-worked=, unowned= and responsible=" (saved line 143-144, docs/SPEC-WORK.md:2075-2076).
18. (ABSENT) "done, remaining, size, stream and under add responsible=" (saved line 144-145, docs/SPEC-WORK.md:2076-2077).
19. (ABSENT) "stream adds leases=" (saved line 145, docs/SPEC-WORK.md:2077).
20. `ExchangeFramedCarriesAMultiRowListing` (in frame_test.go) — "disposition row: branch=, disposition=, repo, landed=, released=, holder=, settled=" (saved line 146-161, docs/SPEC-WORK.md:2078-2093).
21. `TestMergedIsNotDistributed` (in slice-05-durable-journal.lisp) — "released=- while that task is still in O" (saved line 151-158, docs/SPEC-WORK.md:2083-2090).
22. (ABSENT) "who and stale print the lease row: default=, holder, heartbeat age, deadline" (saved line 161-163, docs/SPEC-WORK.md:2093-2095).
23. (ABSENT) "handoffs prints the transition row, one line per :lease, :heartbeat, :release or :handoff event" (saved line 163-164, docs/SPEC-WORK.md:2095-2096).
24. `TestReadyNamesTheBlockerAndTheResolver` (in slice-05-durable-journal.lisp) and `TestReadyPrintsEachRowsBlockerAndResolver` (in main_test.go) — "ready prints exact reason per blocked row and who can resolve" (saved line 180, docs/SPEC-WORK.md:2112).
25. `TestReadyNeedsEveryDependencySettled` (in slice-11-dependencies.lisp) and `TestReadyExcludesADependentWhoseNeedIsOpen` (in replays-8681.lisp) — "a node is ready only when every need is terminal accepted" (saved line 185, docs/SPEC-WORK.md:2117).
26. `TestANeedsCycleRefusesAtSeed` (in replays-8681.lisp) and `TestDependenciesRefusesANeedsCycle` (in main_test.go) — "a :deps cycle is refused by validator rule 3" (saved line 186-187, docs/SPEC-WORK.md:2118-2119).
27. `TestRevertingANeedMarksDependentsNeedsBroken` (in replays-8681.lisp) — "a revert of a need flags each dependent needs-broken" (saved line 188, docs/SPEC-WORK.md:2120).
28. `TestFindingsAcrossCAndO` (in slice-04-doing-and-journal.lisp) — "findings-across-c-and-o: four ids, open=1 closed=3, landed= and released= for closed items" (saved line 229-233, docs/SPEC-WORK.md:2161-2165).
29. `TestHistoryGrowsStartupDoesNot` (in slice-04-doing-and-journal.lisp) — "history-grows-startup-does-not: same ask same pages= with older history" (saved line 249-253, docs/SPEC-WORK.md:2181-2185).
30. `TestClosedPagedWithoutFullLoad` (in slice-04-doing-and-journal.lisp) — "closed-paged-without-full-load: --max 2 prints two rows, MORE naming --after, no read of anything outside window" (saved line 254-257, docs/SPEC-WORK.md:2186-2189).
31. `TestDryRunWritesNothing` (in replays-8643.lisp) — "--dry-run validates, prints projected receipt with dry-run=true, writes zero events" (saved line 270-276, docs/SPEC-WORK.md:2202-2208).
32. `TestStaleExpectationRefused` (in replays-8644.lisp and replays-8645.lisp) — "stale expectation refused at exit 1, <MUTATION> FAIL node=<id> expect=<rev> current=<rev>: stale" (saved line 287-290, docs/SPEC-WORK.md:2219-2222).
33. `TestBundleOwnEarlierRequestsAdmittedInReplay` (in slice-08-replays-late.lisp) — "a replayed request whose node has accepted nothing since but this bundle's own earlier requests is admitted" (saved line 308-310, docs/SPEC-WORK.md:2240-2242).
34. `TestStaleReplayRefused` (in slice-08-replays-late.lisp) — "a replayed request is refused when the node has accepted an event not of this same bundle" (saved line 290-308, docs/SPEC-WORK.md:2222-2240).
35. `TestNegativeMaxRefused` (in slice-08-replays-late.lisp) and `TestResultsMaxZeroPrintsEverything` (in main_test.go) — "--max 0 means all, negative refused" (saved line 312-313, docs/SPEC-WORK.md:2244-2245).
36. `TestEverySocketVerbShapeSpellsItsRequestLine` (in socketverbs_test.go) — "--as <name> on every verb" (saved line 318-320, docs/SPEC-WORK.md:2250-2252).
37. `TestTwoEventCandidateIsAllOrNone` (in slice-01-reader.lisp) — "structure verbs append one structure event and one scope event as one journal record, all-or-none" (saved line 429-435, docs/SPEC-WORK.md:2361-2367).
38. (ABSENT) "decompose must name at least one criterion for every required child, refused whole before writing" (saved line 435-440, docs/SPEC-WORK.md:2367-2372).
39. (ABSENT) "accept --add on a node derived :done is refused by rule 5 at the candidate gate" (saved line 442-446, docs/SPEC-WORK.md:2374-2378).
40. `TestRemoveSettlesOnlyOpenItems` (in slice-05-durable-journal.lisp) — "node remove settles open subtree items into C with disposition removed" (saved line 446-481, docs/SPEC-WORK.md:2378-2413). The lease/cell/dep refusal part is (ABSENT).
41. `TestRemoveSettlesOnlyOpenItems` repeat part — "node remove of already-closed node is no-op at exit 0 with NODE NOTE already-closed" (saved line 462-471, docs/SPEC-WORK.md:2394-2403).
42. `TestAcceptAddMovesScopeRevision` (in replays-8645.lisp) — "accept adds or removes a criterion after node add" (saved line 440-442, docs/SPEC-WORK.md:2372-2374).
43. (ABSENT) "removing the last criterion of a required leaf is refused by rule 15" (saved line 441-442, docs/SPEC-WORK.md:2373-2374).
44. (ABSENT) "session start --repair: repair gate, findings strictly below current; refused at exit 1 with no repair" (saved line 497-504, docs/SPEC-WORK.md:2429-2436). Only the flag spelling is tested in main_test.go.
45. (ABSENT) "red load prints SESSION OK findings=<n>, exits 1, serves reads, refuses mutations without --repair" (saved line 504-511, docs/SPEC-WORK.md:2436-2443).
46. `TestOpenCountIsReadNotComputed` instrumentation — "start once, run many queries/mutations: instrument parse count, replay count, graph visits, journal writes, emitted bytes" (saved line 581-583, docs/SPEC-WORK.md:2513-2515).
47. `TestReconstructionAfterCloseAndRevive` (in slice-01-reader.lisp) and `TestCountersAndIndexesEqualReconstruction` (in replays-8664.lisp) — "incremental results equal clean reconstruction" (saved line 584-585, docs/SPEC-WORK.md:2516-2517).
48. `TestAKillAfterTheRecordAndBeforeTheApply` (in replays-restart-and-recovery.lisp) — "crash after journal durability but before ack, retry same request: one accepted event, no loss or duplication" (saved line 586-587, docs/SPEC-WORK.md:2518-2519).
49. (ABSENT) "crash or disconnect during clip: recovery retains all accepted events and reports last confirmed shared checkpoint honestly" (saved line 588-589, docs/SPEC-WORK.md:2520-2521).
50. `TestResumeRefusesACopiedJournal` (in slice-12-session-ownership.lisp) — "second coordinator refused access; transfer ownership, then resume: generation bumped" (saved line 590-592, docs/SPEC-WORK.md:2522-2524). Reject reads/writes under old generation is (ABSENT).
51. `TestDutyTierExecutesThePolicy` (in replays-8660.lisp) — "duty-tier-executes-the-policy" (saved line 642-650, docs/SPEC-WORK.md:2574-2582).
52. `TestEscalationCarriesRuleDefaultAge` (in replays-8660.lisp) — "escalation-carries-rule-default-age" (saved line 652-660, docs/SPEC-WORK.md:2584-2592).
53. `TestWaitTableFourPresenceColumns` (in replays-8660.lisp) — "wait-table-four-presence-columns" (saved line 662-671, docs/SPEC-WORK.md:2594-2603).
54. `TestQuietTimeCallsNothing` (in replays-8660.lisp) — "quiet-time-calls-nothing" (saved line 673-679, docs/SPEC-WORK.md:2605-2611).
55. `TestCostPerAcceptedDecision` (in replays-8661.lisp) — "cost-per-accepted-decision" (saved line 681-687, docs/SPEC-WORK.md:2613-2619).
56. `TestSingleWriterKernelTotalOrder` (in replays-8661.lisp) — "single-writer-kernel-total-order" (saved line 689-696, docs/SPEC-WORK.md:2621-2628).
57. (ABSENT) "check carries contraction from the journal -- the CONVERGING/EXPANDING verdict, computed from the journal never from a note" (saved line 704-706, docs/SPEC-WORK.md:2636-2638).
58. (ABSENT) "scope gate is a node property -- a node carries its scope, not a policy regex" (saved line 707-708, docs/SPEC-WORK.md:2639-2640).
SECTION
<<<TABLE
1	TestPercentZeroApplicable	PRESENT	lisp/nova-work	tests/acceptance/slice-09-replays-roadmap.lisp:601-608	green=0 applicable=0 over zero applicable rows
2	TestPercentWithoutAxisRefused	PRESENT	lisp/nova-work	tests/acceptance/slice-09-replays-roadmap.lisp:582-583,612	percent without --axis refused at exit 2
3	-	ABSENT	lisp/nova-work	-	remaining prints deferred=/cancelled=/superseded= kept apart
4	-	ABSENT	lisp/nova-work	-	since-baseline= counts gross discovery members
5	-	ABSENT	lisp/nova-work	-	unit=features/epics/work-sets on every line
6	TestRule18FindsTheLatestRow	PRESENT	lisp/nova-work	tests/replays-8660.lisp:210	open+closed=total, no id in both
7	-	ABSENT	lisp/nova-work	-	closed-in/settles-in/revives-in/items-in with window
8	TestClosedRowWithArchiveAbsent	PRESENT	lisp/nova-work	tests/acceptance/slice-04-doing-and-journal.lisp:485	gap= printed where body missing
9	TestOpenCountIsReadNotComputed	PRESENT	lisp/nova-work	tests/acceptance/slice-01-reader.lisp:220	|O| is mutation-envelope counter, read not computed
10	TestSizePrintsOpenUnitScope	PRESENT	lisp/nova-work	tests/acceptance/slice-01-reader.lisp:232-237	query --ask size prints open=/unit=/scope=
11	-	ABSENT	lisp/nova-work	-	done-unverified= is part of unknown=
12	TestQueryFriendsOverSocket	PRESENT	cmd/nova-work	s8_test.go:19	QUERY OK + QUERY ROW lines
13	queryFriendsOK	PRESENT	cmd/nova-work	s8_test.go:14	every ask prints scope/membership/branch/unit/...
14	-	ABSENT	lisp/nova-work	-	from/to/closed-in/settles-in/revives-in/items-in with window
15	TestPageBudgetIsNotMax	PRESENT	lisp/nova-work	tests/acceptance/slice-04-doing-and-journal.lisp:379	explicit historical query prints pages=
16	TestPercentFields	PRESENT	lisp/nova-work	tests/acceptance/slice-09-replays-roadmap.lisp:585-598	percent adds green=/applicable=/baseline-rows=/row-kind=
17	-	ABSENT	lisp/nova-work	-	who/stale add held-not-worked=/unowned=/responsible=
18	-	ABSENT	cmd/nova-work	-	done/remaining/size/stream/under add responsible=
19	-	ABSENT	cmd/nova-work	-	stream adds leases=
20	ExchangeFramedCarriesAMultiRowListing	PRESENT	internal/workclient	frame_test.go:46-48	disposition row: branch=/disposition=/repo/landed=...
21	TestMergedIsNotDistributed	PRESENT	lisp/nova-work	tests/acceptance/slice-05-durable-journal.lisp:6	released=- while release task still open
22	-	ABSENT	lisp/nova-work	-	who/stale lease row: default=/holder/heartbeat/deadline
23	-	ABSENT	lisp/nova-work	-	handoffs prints transition row
24	TestReadyNamesTheBlockerAndTheResolver	PRESENT	lisp/nova-work	tests/acceptance/slice-05-durable-journal.lisp:41	ready names exact blocker and resolver
25	TestReadyNeedsEveryDependencySettled	PRESENT	lisp/nova-work	tests/acceptance/slice-11-dependencies.lisp:71	dependencies ready only when every need terminal
26	TestANeedsCycleRefusesAtSeed	PRESENT	lisp/nova-work	tests/replays-8681.lisp:69	:deps cycle refused by validator rule 3
27	TestRevertingANeedMarksDependentsNeedsBroken	PRESENT	lisp/nova-work	tests/replays-8681.lisp:79	revert flags dependents needs-broken
28	TestFindingsAcrossCAndO	PRESENT	lisp/nova-work	tests/acceptance/slice-04-doing-and-journal.lisp:314	findings-across-c-and-o: landed/released for closed
29	TestHistoryGrowsStartupDoesNot	PRESENT	lisp/nova-work	tests/acceptance/slice-04-doing-and-journal.lisp:446	history-grows-startup-does-not
30	TestClosedPagedWithoutFullLoad	PRESENT	lisp/nova-work	tests/acceptance/slice-04-doing-and-journal.lisp:499	closed-paged-without-full-load
31	TestDryRunWritesNothing	PRESENT	lisp/nova-work	tests/replays-8643.lisp:16	--dry-run validates, dry-run=true, writes zero
32	TestStaleExpectationRefused	PRESENT	lisp/nova-work	tests/replays-8644.lisp:247-253	stale expectation exit 1
33	TestBundleOwnEarlierRequestsAdmitted	PRESENT	lisp/nova-work	tests/acceptance/slice-08-replays-late.lisp:1235-1269	bundle's own earlier requests admitted in replay
34	TestStaleReplayRefused	PRESENT	lisp/nova-work	tests/acceptance/slice-08-replays-late.lisp:1222-1234	replay refused when event not of same bundle
35	TestNegativeMaxRefused	PRESENT	cmd/nova-work	tests/acceptance/slice-08-replays-late.lisp:1287-1291	--max 0 means all, negative refused
36	TestEverySocketVerbShapeSpellsItsRequestLine	PRESENT	cmd/nova-work	socketverbs_test.go:13	--as <name> on every verb
37	TestTwoEventCandidateIsAllOrNone	PRESENT	lisp/nova-work	tests/acceptance/slice-01-reader.lisp:324-349	structure+scope as one journal record all-or-none
38	-	ABSENT	lisp/nova-work	-	decompose requires --acceptance for every required child
39	-	ABSENT	lisp/nova-work	-	accept --add on derived-done refused by rule 5
40a	TestRemoveSettlesOnlyOpenItems	PRESENT	lisp/nova-work	tests/acceptance/slice-05-durable-journal.lisp:69	node remove settles open items, keeps closed
40b	-	ABSENT	lisp/nova-work	-	node remove refuses live lease or cell/dep reference
41	TestRemoveSettlesOnlyOpenItems (repeat)	PRESENT	lisp/nova-work	tests/acceptance/slice-05-durable-journal.lisp:106	node remove of already-closed is no-op exit 0
42	TestAcceptAddMovesScopeRevision	PRESENT	lisp/nova-work	tests/replays-8645.lisp:225-233	accept adds/removes criterion after node add
43	-	ABSENT	lisp/nova-work	-	removing last criterion of required leaf refused by rule 15
44	-	ABSENT	lisp/nova-work	-	session start --repair: repair gate (flag spelling tested only)
45	-	ABSENT	lisp/nova-work	-	red load: SESSION OK findings=, exit 1, serves reads, refuses mutations
46	TestOpenCountIsReadNotComputed	PRESENT	lisp/nova-work	tests/acceptance/slice-01-reader.lisp:220	instrument parse/replay/visit/write counts
47	TestReconstructionAfterCloseAndRevive	PRESENT	lisp/nova-work	tests/acceptance/slice-01-reader.lisp:282	incremental results equal clean reconstruction
48	TestAKillAfterRecordAndBeforeApply	PRESENT	lisp/nova-work	tests/replays-restart-and-recovery.lisp:91	crash after durability, before ack, retry: one event
49	-	ABSENT	lisp/nova-work	-	crash/disconnect during clip: recovery retains accepted events
50a	TestResumeRefusesACopiedJournal	PRESENT	lisp/nova-work	tests/acceptance/slice-12-session-ownership.lisp	second coordinator refused/transfer ownership/generation
50b	-	ABSENT	lisp/nova-work	-	reject reads/writes under old ownership generation
51	TestDutyTierExecutesThePolicy	PRESENT	lisp/nova-work	tests/replays-8660.lisp:98	duty-tier-executes-the-policy
52	TestEscalationCarriesRuleDefaultAge	PRESENT	lisp/nova-work	tests/replays-8660.lisp:140	escalation-carries-rule-default-age
53	TestWaitTableFourPresenceColumns	PRESENT	lisp/nova-work	tests/replays-8660.lisp:160	wait-table-four-presence-columns
54	TestQuietTimeCallsNothing	PRESENT	lisp/nova-work	tests/replays-8660.lisp:182	quiet-time-calls-nothing
55	TestCostPerAcceptedDecision	PRESENT	lisp/nova-work	tests/replays-8661.lisp:15	cost-per-accepted-decision
56	TestSingleWriterKernelTotalOrder	PRESENT	lisp/nova-work	tests/replays-8661.lisp:52	single-writer-kernel-total-order
57	-	ABSENT	lisp/nova-work	-	check carries contraction from journal (CONVERGING/EXPANDING)
58	-	ABSENT	lisp/nova-work	-	scope gate is a node property not a policy regex
TABLE
<<<ISSUES
ISSUE 1
TITLE: Query output: remaining fields, since-baseline gross counting, unit label pluralisation, done-unverified relationship
RUNG: flash
PACKAGE: lisp/nova-work
COVERS: work-c-3, work-c-4, work-c-5, work-c-11
BODY:
docs/SPEC-WORK.md:1958-1961: "remaining prints deferred=<n>, cancelled=<n> and superseded=<n> kept apart"
docs/SPEC-WORK.md:1971-1988: "since-baseline= has one definition and it is gross: the count of members a :discovery added since that node's last :baseline"
docs/SPEC-WORK.md:1989-2008: "Units are labelled on every line: unit=features or unit=leaves ... unit=epics ... unit=work-sets"
docs/SPEC-WORK.md:2053-2055: "done-unverified=<n> is the part of unknown= that is a recorded done on unverified or stale evidence"

Tests to add:
- TestRemainingPrintsCancelledSupersededKeptApart: run a `remaining` ask over a fixture with deferred, cancelled and superseded leaves; assert the line prints deferred=N, cancelled=M, superseded=P as three distinct non-negative counts.
- TestSinceBaselineCountsGross: fixture with a :baseline, then add members via :discovery, then cancel one; assert since-baseline= counts the gross additions (not net).
- TestUnitLabelPluralised: fixture with a feature-kind roadmap, an epic-kind roadmap and a work-set-kind roadmap; assert the QUERY OK line prints unit=features, unit=epics or unit=work-sets matching the row-kind.
- TestDoneUnverifiedIsPartOfUnknown: fixture with done verified evidence, done unverified evidence, and unknown nodes; assert unknown= includes done-unverified and that done-unverified <= unknown.

Card: Add these four tests to the kernel's query-output tests, each asserting the specific field value against a controlled fixture. All four run against a local session with a journal, no network.
ENDISSUE
ISSUE 2
TITLE: Window-specific query fields: closed-in/settles-in/revives-in/items-in, from/to, and per-ask extra fields
RUNG: pro
PACKAGE: lisp/nova-work
COVERS: work-c-7, work-c-14, work-c-17, work-c-18, work-c-19, work-c-22
BODY:
docs/SPEC-WORK.md:2013-2020: "closed-in=<n>, printed only where a window is given; settles-in=<n>, revives-in=<n>, items-in=<n>"
docs/SPEC-WORK.md:2067-2069: "from=, to=, closed-in=, settles-in=, revives-in=, items-in= wherever a window was given"
docs/SPEC-WORK.md:2075-2076: "who and stale add held-not-worked=, unowned= and responsible="
docs/SPEC-WORK.md:2076-2077: "done, remaining, size, stream and under add responsible="
docs/SPEC-WORK.md:2077: "stream adds leases="
docs/SPEC-WORK.md:2093-2095: "who and stale print the lease row, which carries the default= ... the heartbeat age and the deadline"

Tests to add:
- TestWindowFieldsPresent: run a query with --from/--to; assert from=, to=, closed-in=, settles-in=, revives-in=, items-in= on the QUERY OK line; run without a window and assert those fields absent.
- TestWhoAndStaleAddHeldNotWorked: who and stale --window X assert held-not-worked=, unowned=, responsible= on the scope line and lease rows with default=, holder, heartbeat age, deadline.
- TestDoneRemainingSizeStreamUnderAddResponsible: for each of done, remaining, size, stream, under, assert responsible= on the QUERY OK line.
- TestStreamAddsLeases: stream --repo X assert leases= on the QUERY OK line.

Card: These six behaviours are all output shape. Add one comprehensive test per ask kind or a single fixture that exercises the conditional-printing logic for window fields. The lease-row shape tests for who/stale need a fixture with live and expired leases.
ENDISSUE
ISSUE 3
TITLE: Handoffs query prints transition rows per lease/heartbeat/release/handoff event
RUNG: flash
PACKAGE: lisp/nova-work
COVERS: work-c-23
BODY:
docs/SPEC-WORK.md:2095-2096: "handoffs prints the transition row, one line per :lease, :heartbeat, :release or :handoff event since the named revision"

Tests to add:
- TestHandoffsTransitionRowShape: set up a session, take a lease, heartbeat, release, then query handoffs --since <rev>; assert each event type produces its own HOFF ROW line with the correct fields and no line is omitted.
- TestHandoffsRefusesWithoutSince: assert exit 2 with a message naming --since.

Card: Add two tests to the query tests. The first asserts the row content for each of the four event kinds; the second asserts the --since requirement. One test file, one fixture.
ENDISSUE
ISSUE 4
TITLE: Decompose acceptance gate, rule-5 on accept--add of derived-done, rule-15 on last criterion removal
RUNG: pro
PACKAGE: lisp/nova-work
COVERS: work-c-38, work-c-39, work-c-43
BODY:
docs/SPEC-WORK.md:2367-2372: "decompose --acceptance is repeatable and must name at least one criterion for every child it creates that is :required, and a decomposition that would leave a required leaf unclosable is refused whole at the candidate gate"
docs/SPEC-WORK.md:2374-2378: "An accept --add on a node derived :done is refused by rule 5 at the candidate gate"
docs/SPEC-WORK.md:2373-2374: "removing the last criterion of a required leaf is refused by rule 15"

Tests to add:
- TestDecomposeRequiresAcceptanceForRequiredChild: create a node, decompose it into a required child without --acceptance; assert refusal with rule 15 naming before anything is written.
- TestAcceptAddOnDoneNodeRefusedByRule5: set a node done with evidence; attempt accept --add; assert exit 1 naming rule 5; then correct and verify accept --add is admitted.
- TestRemoveLastCriterionOfRequiredLeafRefusedByRule15: create a required leaf with one acceptance criterion; accept --remove that last criterion; assert exit 1 naming rule 15.

Card: Three tests in the kernel validation test file. Each sets up a controlled tree and asserts the mutation is refused at the candidate gate with the expected rule number and reason.
ENDISSUE
ISSUE 5
TITLE: Node remove refuses live lease and cell/dep references
RUNG: flash
PACKAGE: lisp/nova-work
COVERS: work-c-40b
BODY:
docs/SPEC-WORK.md:2378-2383: "a node holding a live lease, or referenced by a cell's :ref or another node's :deps, is refused, naming the holder or the referrer"

Tests to add:
- TestNodeRemoveRefusesLiveLease: take a lease on a node, then attempt node remove; assert exit 1 naming the holder.
- TestNodeRemoveRefusesCellRef: set a cell's :ref to a node, then attempt remove of that node; assert exit 1 naming the referrer cell.
- TestNodeRemoveRefusesDepRef: add a :deps edge pointing to a node, then attempt remove of that node; assert exit 1 naming the dependent.

Card: Three tests in the kernel test suite, one per refusal case, set up in a fixture. The settled-items test already exists; add the refusal path.
ENDISSUE
ISSUE 6
TITLE: Red-load session behaviour and --repair gate semantics
RUNG: flash
PACKAGE: lisp/nova-work
COVERS: work-c-44, work-c-45
BODY:
docs/SPEC-WORK.md:2429-2436: "session start --repair loads and validates... the mutation accepted only when its finding count is strictly below the current one and refused otherwise at exit 1, <MUTATION> FAIL node=<id> findings=<n> was=<n>: no repair; refuses every clip while findings stand"
docs/SPEC-WORK.md:2436-2443: "A red load's own exit is 1 ... serves reads ... without --repair, a red load answers reads and refuses every mutation"

Tests to add:
- TestRedLoadExitsOneAndServesReads: start a session from a fixture with findings; assert exit 1, SESSION OK findings=N, and a read query succeeds.
- TestRedLoadWithoutRepairRefusesMutations: from the same fixture, assert node add or state mutation is refused naming "no repair".
- TestRepairGateStrictlyDecreases: from that session with --repair, apply a mutation that reduces findings by at least one; assert it is accepted; then apply a mutation that does not reduce findings; assert it is refused with "<MUTATION> FAIL ... findings=<n> was=<n>: no repair".

Card: Three tests for the repair lifecycle. Uses a corrupt or rule-violating fixture that validates with findings > 0. Runs against a real session, no network.
ENDISSUE
ISSUE 7
TITLE: Clip crash recovery retains accepted events; old-generation rejection
RUNG: pro
PACKAGE: lisp/nova-work
COVERS: work-c-49, work-c-50b
BODY:
docs/SPEC-WORK.md:2520-2521: "Crash or disconnect during clip: recovery retains all accepted events and reports the last confirmed shared checkpoint honestly"
docs/SPEC-WORK.md:2522-2524: "Transfer ownership, then resume the former coordinator: reject its reads/writes and side-effect requests under the old ownership generation"

Tests to add:
- TestClipCrashRecovery: simulate a crash during clip (kill the writer after journal but before clip write), restart from the recovery journal; assert all pre-crash accepted events are present and no event lost; assert the last confirmed shared checkpoint is reported honestly (pushed= previous, not the failed clip's).
- TestRejectOldGenerationWrites: handoff to successor (generation N+1), then attempt a mutation from the old session/generation; assert the mutation is refused; assert a read query from the old session is also refused.

Card: Two tests. First needs a hook to interrupt the clip write. Second needs two session fixtures with known generations. Builds on the existing slice-12-session-ownership tests.
ENDISSUE
ISSUE 8
TITLE: Check contraction from journal; scope gate as node property
RUNG: flash
PACKAGE: lisp/nova-work
COVERS: work-c-57, work-c-58
BODY:
docs/SPEC-WORK.md:2636-2638: "check carries contraction from the journal -- the CONVERGING/EXPANDING verdict of SPEC-PULSE's rule 5, computed from the journal, never from a note"
docs/SPEC-WORK.md:2639-2640: "the scope gate is a node property -- a node carries its scope, not a policy regex (SPEC-PULSE's rule 7)"

Tests to add:
- TestCheckContractionFromJournal: run check over a session with an empty journal (no contraction); assert CONVERGING or EXPANDING based on a journal that records contraction; assert the verdict comes from journal records, not from a note or external file.
- TestScopeGateIsNodeProperty: create nodes with explicit scope; assert that a node outside the permitted scope of a roadmap is refused at the cell/roadmap admission; assert that changing the node's scope property (via node edit or scope event) changes admission.

Card: Two tests. First reads check output and asserts the verdict source. Second asserts the property-based scope gate (not regex-based) across node and roadmap operations.
ENDISSUE
ISSUES
<<<READINESS
VERDICT: READY
First card: ISSUE 1 (flash, four closely related field-printing behaviours in the kernel's query output)
READINESS

```
git status --short: (nothing to report)
```
files changed: none
head 5298f6be12eaa0f7e6622334d2b6a1eb427649e3
Left owed: nothing — all 58 demanded behaviours from the scope (lines 1933-2643 of SPEC-WORK.md) are enumerated. 58 total: 39 PRESENT, 19 ABSENT (including two partials: work-c-40b and work-c-50b). 8 issues drafted for the ABSENT ones.