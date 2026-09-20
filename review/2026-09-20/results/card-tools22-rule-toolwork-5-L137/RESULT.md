RESULT tools22-rule-toolwork-5-L137 sha=5298f6be12ea
GAP internal/swarm/routeladder.go:147
SPEC docs/SPEC-TOOLWORK.md:137 rule 5
PKG internal/docs
ASK To satisfy rule 5, the code must derive a card's swarm-eligibility from the card's own ROUTE line — eligible only when the line reads `jev=<rung>` with `why=-` on a rung whose registry row carries a `model`, treating `why=below-floor`, `refused`, `rung-is-asked-not-run`, `card-names-no-kind` or `no-ladder` as not eligible — and the route's floor must be the tuned floor (a row in the floors file with its trial behind it), refused as untuned when the floor has no rows.

The classifier's half of condition (b) is genuinely the route, and the ROUTE line encodes exactly what the rule describes:

- internal/swarm/routeladder.go:203-215 — the runnable-rung test is `in.Registry.ModelFor(res.Rung.Name)`; on success the card gets the model and `why=-`/`jev=<rung>`:
  ```
  model, ok := in.Registry.ModelFor(res.Rung.Name)
  ...
  out.Model = model
  out.Fallback = false
  out.Why = ""
  ```
- internal/swarm/routeladder.go:224-238 — routeReceipt prints `ROUTE jev=<rung|fallback> conf=<x> rung=<name> model=<id> why=<-|token>`, `why=-` when the answer decided the model.
- internal/swarm/routeladder.go:129-138 — the enumerated not-eligible tokens exist verbatim: `below-floor`, `refused`, `rung-is-asked-not-run`, `no-ladder` (plus `no-key`/`no-accounting`).
- internal/swarm/batch.go:2140-2141 — `card-names-no-kind` is emitted on the fill path for an unroutable card.
- internal/swarm/batch.go:2095-2150 (routeCards) and internal/swarm/run.go:672-694 (routeTask) — the fill/dispatch path asks the ladder once per card before dispatch, exactly as the rule's premise states.

The GAP is the last two obligations of the rule, which the code does not do:

1. internal/swarm/routeladder.go:146-148 — the dispatch floor is the constant `DefaultRouteFloor` (routeladder.go:33, `const DefaultRouteFloor = 0.9`) or whatever `--route-floor` the caller passes; nothing reads a floors file, nothing requires a trial behind the floor, and there is no "refused as untuned" on the route path.
2. The "not eligible: the card is not dispatched to a swarm" verdict does not exist. No Go code writes an `eligible=` token (grep over `*.go` finds `eligible=` nowhere), so the card's ROUTE line never records `eligible=rules` / `eligible=no` as the toolwork rules 5-6 specify (only the fixture cards under cmd/nova-swarm/testdata/cards carry it). The only non-dispatch behavior is the run dispatcher parking a `rung-is-asked-not-run` card in routed-out/ (internal/swarm/run.go:429-435); below-floor/refused/card-names-no-kind/no-ladder cards still launch on today's model.

This matches SPEC-TOOLWORK.md:981, which lists "the route check at dispatch — answer, rule table or `eligible=no`" as work item T6 (builder, no gate yet): the eligibility layer that consumes the ROUTE line is not landed at this base.

GUARDED-BY (of the conforming half): the route record is real and tested — `TestRouteCardWithNoModelOnTheRungKeepsTodaysModel` (internal/swarm/routeladder_test.go:351), `TestRouteCardsOnTheFillPathOverridesTheTSVModel` (internal/swarm/routeladder_test.go:406), `TestRunParksACardTheLadderAsksInsteadOfRunning` (internal/swarm/routeladder_test.go:487). The SPEC-TOOLWORK red tests `route-below-floor-is-not-eligible` / `asked-not-run-rung-is-not-eligible` / `no-provider-and-no-rule-says-not-eligible` (SPEC-TOOLWORK.md:247-248) do not exist anywhere in the tree — the eligibility verdict itself is UNGUARDED/absent.

Greps run:
- `grep -rn "card-names-no-kind|rung-is-asked-not-run|no-ladder" --include='*.go' .`
- `grep -rn "eligible=" . --include='*.go'` (no Go source writes it)
- `grep -rn "eligible" --include='*.go' . | grep -v _test` (no toolwork eligibility verdict)
- `grep -rn "transcript-test|fix-red" cmd/nova-pulse/ internal/pulse/` (no kinds table)
- `grep -rn "untuned|floors" internal/swarm/*.go cmd/nova-swarm/*.go internal/decide/*.go` (no floors file on the route path)
- `grep -rn "below-floor-is-not-eligible|asked-not-run-rung|no-provider-and-no-rule|route-below-floor" --include='*_test.go' .` (none)
- `grep -rn "SPEC-TOOLWORK|toolwork" internal/docs/*_test.go` (none)
- `grep -rn "func Test" internal/docs/*_test.go` (no eligibility test)
- read internal/swarm/routeladder.go, internal/swarm/batch.go:2095-2161, internal/swarm/run.go:380-751, cmd/nova-decide/route.go, internal/decide/ladder.go:860-979, internal/decide/tune.go, docs/SPEC-TOOLWORK.md:90-260

Left owed: the eligibility verdict that consumes the ROUTE line (`eligible=` on the card, "not dispatched to a swarm" for the non-run tokens) and the tuned floor / untuned-refusal for the route — SPEC-TOOLWORK work item T6 (docs/SPEC-TOOLWORK.md:981) and its red tests.

git status --short: