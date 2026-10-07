# nova-tools cold audit — grok/b

Base read: `5844884e267cf0bc0c599aa3e0d7c40310d8428e` (`sprint/mechanical-2026-10-02`).
Scope: nova-tools without the sprint (nova-friend, nova-bus, nova-config, nova-redis,
nova-secrets, nova-swarm, nova-cairn, nova-local, nova-tokens, nova-fuse, nova-ci, the
rest), the docs tree and the TLA+ models. Read as a stranger, no live store or server
touched; the one functional command below ran a scratch in-memory twin
(`bustest.NewFake` + `store.NewMem`) and no socket.

The gate this card names (`go test -count=1 -timeout 600s ./internal/docs ./internal/ci`)
is green once `origin/dev` is fetched: `ok internal/docs` and `ok internal/ci`, last lines
`ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.628s` and
`ok  	github.com/mas-bandwidth/nova-tools/internal/ci	9.048s`. The whole unit tier
(`go test -count=1 -timeout 600s $(go list ./internal/... | grep -v /internal/sprint)`,
on the bench) is also green. What the gate does not cover is where the findings are: the
`functional` friend suite, the race detector, and specs a machine does not read.

1. **URGENT** `internal/friend/chaos_functional_test.go:297` — the five recovery journeys
   `internal/release/journeygate.go:59-63` promises for the tag cannot pass, because the rig
   asserts a friend reads `up` from her first beat while the status rule makes `up` rest on
   the session's own evidence, never a beat. `newChaosRig` (`:295-297`) steps two seconds and
   `require.Equal(t, sprint.Up, r.friendStatus("bob"), "bob is up from the start")` runs
   before the first `r.ping()` (`:299`); `sprint.FriendEvidence` (`internal/sprint/presence.go:208-233`)
   returns `Up` only for a live `session pong` (`:215-217`) or a `finish` (`:219-220`) and its
   last line says "her beat ... is not evidence" (`:230-232`), and nothing in the rig records
   a `friend health` observation or a finish before that assertion. Evidence:
   `go test -tags functional -count=1 -timeout 600s -run TestEveryFriendFailureShowsWithinItsBound -v ./internal/friend/`
   prints all five subtests `--- FAIL`, each `expected: "up"; actual: "down"` with
   `Messages: bob is up from the start` (`chaos_functional_test.go:297`, reached from `:505`,
   `:518`, `:547`, `:573`, `:591`). The suite's own `owed.settle` (`:463-477`) only turns an
   unmet part into a named skip, so a failure here is not the "owed" path the release gate
   excuses. Fix: in the rig, drive one session pong (or a finish) and wait for the row to read
   `up` before asserting, or declare the five journeys incomplete at the tag.

2. **URGENT** `docs/SPEC-BUS.md:54-57` (with `internal/bus/bus.go:40-45`) — the spec is
   false where it reasons about the claim window: it says a reader keeps a message fifteen
   minutes, "longer than the longest delivery any reader makes, nova-friend's ten minute turn
   and the kill that ends it, so a live reader mid-turn is never handed its message twice",
   but there is no ten-minute turn any more. Evidence: `internal/friend/daemon.go:49` sets
   `DefaultSilentStop = 20 * time.Minute`, and `docs/SPEC-FRIEND.md:338-341` states "No clock
   bounds a batch turn: a turn that prints keeps running however long it takes" while a silent
   turn is stopped only after twenty minutes; `internal/bus/bus.go:51` gives every reader the
   one consumer name, so a session's own `nova-bus recv` running beside the daemon's long turn
   can `XAUTOCLAIM` (`internal/bus/redis.go:148-155`) the in-flight message after 900 s and be
   handed it again. A claim in a spec that the code and its own sister spec contradict is a lie
   a cold reader will plan against. Fix: say what is true — `ClaimAfter` is fifteen minutes and
   a turn that outlives it may be redelivered to another reader — or raise `ClaimAfter` above
   the rule that bounds a turn and name that rule.

3. **NEXT** `internal/bus/bus_test.go:21-27` — the test rig's `Rand` closure increments one
   captured `byte` `n` from every goroutine with no lock, so the bus package cannot be run
   under the race detector. Evidence: `go test -race -count=1 -timeout 600s ./internal/bus`
   prints `WARNING: DATA RACE` (read at `bus_test.go:24`, previous write at `:25`, through
   `Bus.ulid`, `internal/bus/bus.go:680`, from `TestRacingRetriesMakeOneMessage`,
   `internal/bus/token_test.go:201-203`) and fails every parallel test in the package with
   `race detected during execution of test`; the production path uses `crypto/rand` and is not
   the racy one. Fix: derive the rig's bytes without shared mutable state (a per-bus counter
   under a mutex, or `crypto/rand` with the ids still compared structurally).

4. **NEXT** `internal/tokens/opencode.go:191` — `query` builds its deadline from
   `context.Background()`, and `ReadOpenCode` (`:84`) takes no context, so the `sqlite3` child
   is not cancelled when the run is interrupted; each of the three queries can run to the full
   `--timeout` (`:107`, `:112`, `:117`, up to 3×120 s by default, `DefaultTimeout` at `:38`)
   after Ctrl-C. The skeleton promises `Call.Ctx` is cancelled on interrupt
   (`internal/tool/tool.go:148-157`, `:677-700`), and this verb never sees it. Fix: thread
   `Call.Ctx` through `ReadOpenCode` into `query` and use it for `exec.CommandContext`.

5. **NEXT** `internal/update/fleetdrift.go:31` — `fleetBeatKey` re-spells the machine beat
   key (`"bench:" + bench + ":beat"`) instead of using `internal/config/redis.go:80`
   `BeatKey`, so the two copies can drift with nothing to notice. Evidence: both lines spell
   the same key; `internal/update` imports no `internal/config` (nothing holds them equal), and
   `internal/config` does not import `internal/update`, so there is no cycle to avoid. Fix:
   call `config.BeatKey`, or put the key in a leaf package both use.

6. **NEXT** `internal/diffcheck/diffcheck.go:63` — `newStart` does
   `strconv.Atoi(strings.FieldsFunc(plus, ...)[0])` with no guard, so any `@@ ` line whose
   `+` side yields no field (`@@ `, `@@ -1 +`, a hand-written or foreign diff) panics the
   lander rather than refusing. Evidence: `Parse` (`:50-51`) calls `newStart` for every line
   with the `@@ ` prefix, and five callers feed it a diff
   (`cmd/nova-swarm/nativedecide.go:93`, `cmd/nova-sprint/land.go:1274`,
   `internal/decide/gate.go:184`, `internal/sprint/land_repair.go:74`,
   `internal/sprint/land_records.go:33`). Fix: parse the hunk header with a regexp and return
   an error (or a named refusal) when it does not read.

7. **NEXT** `internal/bus/pushproof.go:135` — `proofs` indexes `hashes[0]` after
   `b.Store.Marks(ctx, PushKey)` (`:128`) and trusts the store to answer exactly the one map
   asked for; the interface promises only "the whole hash at each key" (`internal/bus/bus.go:203-205`),
   so a store that answers short panics a `nova-bus names`/`send` path instead of treating the
   missing proof as `PushNone`. Fix: read `hashes` with a length check and default the missing
   map to empty.

urgent=2 next=5
