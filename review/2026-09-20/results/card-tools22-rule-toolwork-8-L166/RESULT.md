RESULT tools22-rule-toolwork-8-L166 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 8 says?
CONFORMS cmd/nova-decide/route.go:173 internal/decide/ladder.go:840
SPEC docs/SPEC-TOOLWORK.md:166 rule 8
PKG github.com/mas-bandwidth/nova-tools/internal/decide (core machinery), github.com/mas-bandwidth/nova-tools/cmd/nova-decide (route verb), github.com/mas-bandwidth/nova-tools/internal/swarm (dispatch path)
ASK Every eligibility decision must compute mechanical preconditions first, then treat the provider's answer as only one additional condition that can fail — never sufficient alone, never able to override a mechanical fact.

---

## What the rule says

Rule 8 states "The classifier's yes never suffices alone" — an affirmative answer from Jev/classifier is ONE MORE CONDITION in an eligibility list, not sufficient by itself. Before gate changes: kind state (trial/trusted/paused), certified bench, and route answer are independent conditions. After gate changes: accept gate negative control (§1), hygiene (§3), read condition and hold fold (§6) — none of which any answer at any confidence can satisfy or lift. Route state is built from card's typed header and issue public text in #1627's untrusted-text frame; nothing in an issue body can make a card eligible.

## Key conformance points in the code

### 1. `routeJev` calls `routeRules` FIRST (ladder.go:840-870)
```go
// ladder.go:870
rules, err := routeRules(reg, u, floor, excluded)
if err != nil {
    return rules, err
}
```
The machinery result is computed BEFORE any provider call. Even when Jev answers, the mechanical determination exists independently.

### 2. Provider not asked for security/designated work (ladder.go:871)
```go
// ladder.go:871
if d == nil || rules.Designated || rules.AwaitingTermination() || u.Security() {
    return rules, nil
}
```
Security work (`u.Security()` → guard/secrets/touches/kind-guard), designations (fresh-take), and open timeouts bypass the provider entirely — the rules' answer stands. This is the strongest expression of "classifier's yes never suffices alone."

### 3. Provider answer constrained by offered set (ladder.go:896-902)
```go
// ladder.go:896
chosen, ok := findMind(offered, a.Choice)
if !ok {
    rules.Reason += fmt.Sprintf("; the provider named %s, which was not offered, so the rules answer stands", oneline.Field(a.Choice))
    return rules, nil
}
```
Even if Jev answers, if it names a rung nobody offered, the rules answer stands.

### 4. Below-floor = suggestion, not authorization (route.go:225)
```go
// route.go:225
case !res.Dispatchable():
    return 1   // A wait is NOT permission
case res.Confidence < *floor:
    return 3   // Below floor = suggestion, not authorization
```
Exit 3 means "below floor" — the classifier answered but its confidence was insufficient. This is the classic case of "yes alone does not suffice."

### 5. Swarm dispatch adds more gates (routeladder.go:189-212)
```go
// routeladder.go:189
case !ask:                    // no key → today's model
    out.Why = RouteFallbackNoKey
case !res.Dispatchable():     // wait ≠ permission
    out.Why = RouteFallbackRefused
case res.Confidence < floor:  // below floor → fallback
    out.Why = RouteFallbackBelow
default:
    model, ok := in.Registry.ModelFor(res.Rung.Name)
    if !ok {                   // ask-child/ask-bus rung → fallback
        out.Why = RouteFallbackNoModel
    }
```
The swarm dispatch layer adds even more mechanical checks: no-key, no-dispatch, below-floor, and no-model-for-rung all fall back to today's model regardless of what the classifier said.

### 6. MandatoryReader replaces provider answer entirely (readers.go:117-125)
```go
// readers.go:117-125
func MandatoryReader(s ReadState) (Role, string, bool) {
    if s.SecurityShapedPackage {
        return RoleSecurity, "the changed paths are security-shaped...", true
    }
    return "", "", false
}
```
When the package is security-shaped, the provider answer is REPLACED — not constrained alongside, but overridden. "At any confidence" is enforced.

### 7. Hold fold preserves holder priority (readers.go:167-169)
```go
// readers.go:167-169
if s.HoldIsOpen {
    d.Reason = fmt.Sprintf("%s holds these files and the hold is unresolved...", s.HolderOfTheArea)
    return d, nil
}
```
An unresolved hold keeps its position regardless of provider answer. The answer names who reads FIRST but lifts NOTHING.

## GUARDED-BY

Tests that would go red if this mechanism stopped working:

- `internal/decide/hold_test.go:89 TestSecurityNeverFallsThrough` — THE definitive test: runs every combination of touch types (guard, secrets, sandbox, sudo, deploy-keys, network), every attempt history, and every floor against both RouteRules AND RouteJev. Proves even with `fakeDecider{choice: "flash", conf: 0.99}`, security work always resolves to the designated mind with zero provider calls. If this test goes red, the classifier's yes IS becoming sufficient.

- `cmd/nova-decide/route_test.go:29 TestRouteNoJevIsDeterministic` — Proves --no-jev gives deterministic results, establishing the mechanical baseline that Jev is measured against.

- `internal/swarm/routeladder_test.go:166 TestRouteCardBelowTheFloorKeepsTodaysModel` — Proves below-floor classification still falls back to today's model in dispatch. The classifier said yes but the card did not run there.

- `internal/decide/ladder_test.go:343 TestJevIsNotAskedForADesignation` — Proves no provider call is made when the machinery has already settled a designation.

## Greps run

```
grep -rn "<path>:<line>" --include='*.go' .    (to find specific lines in source)
grep -rn "Dispatchable\|AwaitingTermination" --include='*.go' .
grep -rn "security\|Security\|Guard\|Guardian\|Secrets\|touches\|Touches" --include='*_test.go' cmd/nova-decide/ internal/decide/
grep -rn "MandatoryReader\|ConstrainRead" --include='*.go' .
grep -rn "below.floor\|below-floor\|RouteFallbackBelow" cmd/ internal/
grep -rn "func Test" --include='*_test.go' cmd/nova-decide/ internal/decide/ internal/swarm/
grep -rn "eligible\|Eligib" --include='*.go' cmd/ internal/
```

Left owed: None found — the core principle is implemented through multiple overlapping guards (routes-first, security-always-designated, hold-priority, below-floor-as-suggestion, swarm-fallback-on-every-edge). No single point of failure where "classifier's yes" could bypass mechanics.

git status --short (printed above: clean)
