RESULT tools22-rule-swarm-profiles-10 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SWARM-PROFILES.md rule 10 says?
GAP internal/swarm/cost.go:56
SPEC docs/SPEC-SWARM-PROFILES.md:1046 rule 10
PKG internal/swarm
ASK An implementation would route every attempt kind (legacy, profiled, nova-local, direct one-shot) through the one Finalize→WriteUsage accounting pipeline, keep per-attempt evidence (attempt/from), report usd=0 for local inference and "-" for absent usage, classify source coverage as observed/missing/unsupported, reconcile cache_read against input per the source's declared inclusive vs exclusive basis so a cache/reasoning subtype is never added twice and aggregate input stays correct, and expose daily bench/repo/model/actor receipt views.
Deciding lines — internal/swarm/cost.go:56-62, the summary aggregation:
		for _, c := range TokenColumns {
			if n, ok := row.Int(c); ok {
				totals[c] += n
			} else {
				dashes[c]++
			}
		}
GAP detail — the one-accounting-pipeline basics are present (Finalize at internal/swarm/finalize.go:66 writes one UsageRow through WriteUsage; attempt/from columns at finalize.go:72-73 and freshAttempt at finish.go:502; absent-token dash at usage.go:71-72 and opencode.go:314-320). But rule 10's accuracy requirements are not implemented anywhere in the package:
  - No source-declared cache-read basis. There is no inclusive/exclusive normalization and no "aggregate input" concept: cost.go:56 sums each of the five TokenColumns (tokens_in, cache_read, reasoning, ...) independently, so cache_read and reasoning are always added on top of a parent total with no reconciliation of cache_read vs input. The 1000/800 inclusive and 200/800 exclusive fixtures both degrade to ordinary independent summing; "basis" in templates.go:305 is the unrelated per-token cost class (zero|flat|metered), not a cache-read basis.
  - No source coverage classification (observed/missing/unsupported) — the string trio does not appear in the package.
  - No local-inference usd=0 path: usd is copied verbatim from Usage.Values (finalize.go:78-80), and a `usage: none`/unmetered source yields an empty Values map, so usd is written "-" (a dash), never "0".
  - Receipt views: Cost's --by accepts only "", model, day, repo (cost.go:25); the rule's bench and actor dimensions are absent.
Greps run: `grep -rn "inclusive|exclusive|basis|aggregate|subtype" --include='*.go' internal/swarm/`; `grep -rn "usd=0|UsageNone|unmetered" --include='*.go' internal/swarm/`; `grep -rn "coverage|observed|missing|unsupported" --include='*.go' internal/swarm/`; `grep -rn "actor|bench" internal/swarm/cost.go internal/swarm/usage.go`; read cost.go, usage.go, finalize.go, finish.go, opencode.go in full.
Left owed
