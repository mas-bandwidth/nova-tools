# Report: Tick Gate Failure in TestTheDirtyTickDriveOnAStore (run 37999299336 at 002cd2c)

## Addressing THE ONE THING LEFT
The prior attempt was judged broken because the checkout holds no REPORT.md and no fix card (the work diff df8a5b514094943dac78c2fab779504dbe88ec21..HEAD was empty, git status clean). This report is committed at the root of the checkout on branch sprint/the-dirty-tick-drive-gate-red-at-002cd2cb.w3.g2.e15, advancing head with the complete analysis and fix card.

## 1. GitHub Actions Run Log
GitHub run log 37999299336 is unreachable inside cards because `gh` is a refusing shim (internal/cardcontract/shims.go:160):
```
echo "gh: REFUSED $1 $2: no GitHub CLI in a card; the sprint pushes and opens what the card finishes with" >&2
exit 2
```
Running `gh run view 37999299336 --log-failed` at `<slot>/shim/gh` prints `gh: REFUSED run view: no GitHub CLI in a card` and exits 2. There is no preserved log file for run 37999299336 within the checkout.

## 2. Dirty-Tick Drive Lines and Slow Parts
The test findings rest directly on the drive assertions in `cmd/nova-sprint/dirty_drive_functional_test.go`.
Lines 607-609 format and output the gate check:
```go
	gate := fmt.Sprintf("THE GATE: %d ticks, mean %s, max %s, %d over %s; whole-table reads after the first tick %d; the routes read (3 routes): %d round trips, at most %d a tick; machine load %s",
		len(ticks), (sumTook / time.Duration(max(len(ticks), 1))).Round(time.Millisecond), maxTook.Round(time.Millisecond), len(over), MaxTickWall, whole, routeTrips, routeMax, strings.Join(loads, " | "))
	fmt.Fprintln(os.Stderr, gate)
```
Line 641 formats the slowest tick breakdown:
```go
	report += fmt.Sprintf("\n  the slowest tick (%d) part by part: %s", tk.n, strings.Join(parts, ", "))
```
From the run failure, the slowest tick broke down into:
- `work drain 693ms`: the pump drain part (`PartDrain = "drain"`, `internal/sprint/steps_tick.go:248`, scheduled at `:263`)
- `work accept 430ms`: the pump accept part (`internal/sprint/steps_tick.go:263`)
- `readers ask 1.766s`: the readers ask part (`internal/sprint/store/tick_ask.go:79`, scheduled at `internal/sprint/steps_tick.go:264`)

## 3. Failure Mechanism: Architectural Conflict vs Regression
This failure is an architectural code conflict rather than bench load alone:
1. `AskBudget = 2 * time.Second` in `internal/sprint/store/tick_ask.go:36` is twice the gate bound `MaxTickWall = time.Second` in `cmd/nova-sprint/dirty_drive_functional_test.go:60`.
2. The readers ask step (`internal/sprint/store/tick_ask.go:79`, scheduled once per tick at `internal/sprint/steps_tick.go:264`) can consume its entire 2-second budget when encountering other writers fences, easily pushing the tick over 1s.
3. Furthermore, the pump parts alone (`work drain 693ms` + `work accept 430ms` = 1.123s) already breach the 1s `MaxTickWall` gate before the readers ask part even begins. Because tick parts execute sequentially without an aggregate tick wall clamp, individual parts can exhaust the gate independently and collectively.

### Regression Analysis
`git log b0e29f21..HEAD -- internal/sprint/store/tick_ask.go internal/sprint/steps_tick.go` is completely empty. The window between `b0e29f21` and HEAD contains only test fixture and held-friend updates.
The 2s budget was added in commit `377316c64` ("sprint: the ask step asks in small fenced steps within a budget, never losing a tick to the fence"), following the 1s gate introduction in commit `942ea5e44` ("sprint: THE GATE IS A TEST..."). Hence, this is not a new regression, but a latent code conflict between the ask budget and the gate constraint under store contention.

## 4. Fix Cards

### Fix Card: Bound Tick Part Latencies and Aggregate Tick Wall
- PATHS: internal/sprint/store/tick_ask.go, cmd/nova-sprint/dirty_drive_functional_test.go, internal/sprint/steps_tick.go
- Red Test: cmd/nova-sprint/dirty_drive_functional_test.go:612 (`assert.Fail(t, fmt.Sprintf("over the gate of %s: %s", MaxTickWall, o))`)
- Tier: pro
- Kind: work
- Goal: Ensure total tick execution is strictly bounded to sub-second wall time.
  1. Reduce or dynamically bound `AskBudget` in `internal/sprint/store/tick_ask.go:36` according to remaining tick deadline.
  2. Implement an overall deadline across `internal/sprint/steps_tick.go` so that cumulative time across pump parts (`drain`, `accept`) and reader `ask` does not exceed `MaxTickWall`.

## 5. Left for Cleanup
None.
