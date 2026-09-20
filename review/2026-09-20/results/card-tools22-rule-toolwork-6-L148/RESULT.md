RESULT tools22-rule-toolwork-6-L148 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 6 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:148 rule 6
PKG internal/docs
ASK With no provider (`why=no-key`/`why=no-accounting`) the dispatch must ask the route `--no-jev` and then let the rule table decide: if it names a rung a swarm runs for the card's kind the card's ROUTE line says `eligible=rules`, and if it is silent the line says `eligible=no why=no-provider-and-no-rule` and the card is not dispatched (it waits).

Nothing in the tree emits the eligibility line the rule specifies. The tokens `eligible=rules`, `eligible=no` and `no-provider-and-no-rule` occur only in `docs/SPEC-TOOLWORK.md` itself and in three static card fixtures under `cmd/nova-swarm/testdata/cards/` (coordinator-written card text, not tool output). No `.go` file contains `eligible=` in any output or any `kinds`/rule table for eligibility.

The one code path that handles the no-provider dispatch case is `RouteCard` in `internal/swarm/routeladder.go`:

```
case !ask:
	// No call was made, so nothing about the model was decided: today's
	// model stands, and the rung the rules named is on the receipt.
	out.Why = RouteFallbackNoKey
	if unaccounted {
		out.Why = RouteFallbackNoAccount
	}
```
(internal/swarm/routeladder.go:190-196)

When there is no key (or no accounting) it does ask the rules alone — `decide.RouteRules`, the `--no-jev` answer (internal/swarm/routeladder.go:174) — but then it **keeps today's model and dispatches the card anyway**, printing `ROUTE jev=fallback conf=... rung=... model=<today's model> why=no-key` (routeReceipt, internal/swarm/routeladder.go:224-239; `c.model = res.Model` at internal/swarm/batch.go:2144). That is exactly the "fill path's fallback" rule 6 says the eligibility path must NOT do ("here, a missing answer keeps today's **hands**"; "Falling back to 'dispatch anyway' would make the second condition optional whenever the provider is down"). The rule-table consultation, the `eligible=rules`/`eligible=no` tokens, and the "card waits" withholding do not exist.

Where I looked:
- `grep -rn "eligible=" --include='*.go' .` → no output token anywhere; the only `eligible` hits are comments about "eligible rungs" in `internal/decide/ladder.go`.
- `grep -rln "eligible=rules\|eligible=no\|no-provider-and-no-rule" .` → `docs/SPEC-TOOLWORK.md` and the three `cmd/nova-swarm/testdata/cards/*.md` fixtures only.
- The kinds/rule table the rule reads, `internal/pulse/kinds.go`, does not exist; `internal/swarm/lintheader.go:55-58` says it "is not on dev either" and waits for T06a.
- `grep -rn "no-provider-falls-back-to-the-rule-table\|no-provider-and-no-rule-says-not-eligible" --include='*_test.go' .` → no test; the red-test names exist only in the spec's prose (docs/SPEC-TOOLWORK.md:247).
- Read in full: `internal/swarm/routeladder.go` (RouteCard, routeReceipt), `internal/swarm/batch.go` (routeCards at :2130-2148, dispatch), `internal/swarm/run.go` (routeTask, :687-695), `internal/pulse/launch.go` (routeCards), `cmd/nova-swarm/route.go`, `cmd/nova-decide/route.go`.

Left owed: nothing — rule 6's eligibility machinery is absent at this base, and the closest code (RouteCard) does the opposite of the rule's mandate (dispatch-anyway fallback instead of `eligible=no` + wait).