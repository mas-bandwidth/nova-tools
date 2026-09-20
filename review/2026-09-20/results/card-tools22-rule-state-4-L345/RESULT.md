RESULT tools22-rule-state-4-L345 sha=5298f6be12ea — does the code at this base do what docs/SPEC-STATE.md rule 4 says?
GAP internal/pulse/watch.go:26
SPEC docs/SPEC-STATE.md:345 rule 4
PKG internal/docs
ASK Cut the verbs over so that `watch` subscribes to `nova:events:*` (not polls), `status --fleet` queries the `nodes`/`receipts` projection and the counters, the puller reads `nova:queue:*` streams, caps read the in-flight sorted-set counters, and `report` queries `token_ledger`.

Deciding lines:
  internal/pulse/watch.go:26-28: "// watchPollFloor is the fastest watch ever polls: the 30 s floor the spec names, and the // smallest --cap that is not refused." and "const watchPollFloor = 30 * time.Second" — the watch verb still polls the queue, the bus and the job dirs every 30 s; it never SUBSCRIBEs to `nova:events:*` (SPEC-STATE.md:317 says it should "instead of polling the queue, the bus and the job dirs"). The red test `a-watch-subscriber-wakes-on-a-job-done-event-within-a-second` does not exist.
  internal/pulse/cli.go:103-113: the status flag set names --queue --roots --slots-store --day --timeout --max --expanding-hours; there is no `--fleet`, so `nova-pulse status --fleet` reading the nodes/receipts projection and counters is not implemented.
  internal/tokens/reportledger.go:26: "func ReadPoolLedger(path string)" — `nova-tokens report` reads the fold-pool day-TSV ledger (SPEC-TOKENS), not a query over Postgres `token_ledger`; no `token_ledger` table appears anywhere in internal/ or cmd/.
  internal/swarm/capacity.go: no `caps` verb exists in cmd/nova-swarm/main.go (verb list runs doctor/add/batch/run/supervise/status/stop/.../slots/.../worker; "caps" is absent), so the "nova-swarm caps reads the in-flight sorted sets" verb is not cut over.

What the base DOES implement and test (the slice-1 primitives rule 4 leans on):
  internal/redisq/redisq.go:143 Pull (XREADGROUP, one `workers` group per stream), :167 Claim (XAUTOCLAIM), :188 Ack (XACK on clip), :240 TakeLease (SET NX PX fencing token), :275/:290 Renew/ReleaseLease (Lua compare-and-release, no bare EXPIRE), :449 Admit (one atomic Lua INCR-with-limit script), :460 Inflight (ZCARD of the sorted set), :355/:388/:404 SlotLeases Take/Renew/Release with one mode per bench.
  Guarded by internal/redisq/redisq_test.go: TestOneCardIsDeliveredToExactlyOneConsumer (:51), TestAStreamConsumerThatDiesMidCardHasItsCardReclaimed (:75), TestACapCounterRefusesThe41stInFlightMuseCall (:118), TestACapAdmissionIsOneAtomicScriptThatRefusesThe41st (:160), TestAStaleLeaseTokenCannotRenewOrReleaseASlot (:197). (Running them here failed only because the sandbox denies miniredis a socket: "listen tcp 127.0.0.1:0: bind: operation not permitted".)
  internal/record (nova-work record/results over cards:done -> card_results) also exists, guarded by internal/record/record_test.go.

So the puller and the cap/lease primitives conform, but three of the five verbs rule 4 names are not cut over (watch still polls, status has no --fleet, report still reads TSVs not token_ledger, and there is no caps verb), and the two red tests for the missing verbs do not exist.

Greps ran:
  grep -rn "a-stream-consumer-that-dies...|a-cap-counter...|a-watch-subscriber...|the-monthly-token-report...|one-card-is-delivered...|a-stale-lease-token...|a-cap-admission-is-one-atomic" -> 5 tests in internal/redisq/redisq_test.go, none elsewhere
  grep -rn "Subscribe|PSubscribe|\.Publish\(|nova:events" --include='*.go' -> only internal/ci (merge react path), nothing in internal/pulse
  grep -rn "status --fleet|--fleet" --include='*.go' -> only cmd/nova-post (a different flag)
  grep -rn "func Test" internal/redisq/redisq_test.go -> the 5 named tests
  grep -rn "token_ledger|card_results|ci_pr_outcomes|capability_inventory" --include='*.go' -> only internal/record's card_results
  grep -rn '"caps"' cmd/nova-swarm/main.go internal/swarm/*.go -> none
  sed -n '325,371p' docs/SPEC-STATE.md; read internal/redisq/redisq.go, internal/pulse/watch.go, internal/pulse/cli.go, internal/tokens/reportledger.go, internal/ci/events.go, internal/record/*.go

Left owed: the watch-subscriber and monthly-token-report red tests, the `status --fleet` verb, the `caps` verb, and the token_ledger query — none of which the base implements.