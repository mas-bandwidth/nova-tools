RESULT tools22-rule-swarm-profiles-1 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SWARM-PROFILES.md rule 1 says?
ABSENT
SPEC docs/SPEC-SWARM-PROFILES.md:981 rule 1
PKG internal/swarm
ASK An implementation would have to dispatch each worker/attempt against its single resolved profile (a mixed mocked Go/Zen pool receiving only the profile that resolved it), record the requested and the observed provider/model identity distinctly in every attempt record while leaving legacy worker behavior unchanged, and enforce an explicit per-profile concurrency cap on top of the existing `--workers` limit.

Greps run (all from <JOBDIR>/repo):
- `grep -rni "concurrency" internal/swarm/ --include='*.go'` -> only capacity/templates/batch comments; no profile cap.
- `grep -rni "requested\|observed" internal/swarm/result.go finish.go decide.go reap.go usage.go` -> all usage/budget "observed", none identity.
- `grep -rn "LoadProfile" --include='*.go' .` -> only internal/swarm/profile.go and profile_test.go; no production caller.
- `grep -rn "profile" internal/swarm/batch.go run.go worker.go decide.go pool.go` -> no profile reference in the dispatch path.
- `grep -rn "profiles\|--profiles" --include='*.go' .` -> only nova-tokens profiles verb (unrelated) + profile.go; no `run --profiles`.
- `grep -rni "selector\|snapshot\|frozen" internal/swarm/ --include='*.go'` -> only process-CPU snapshots; no profile selector/snapshot.
- Read internal/swarm/profile.go in full: Profile has six binding fields (worker, route, env_var, model, allowed_models, prompt); WorkerProfile/workerAllowed has no "concurrency" member; RouteProfile has no requested/observed identity.

What is there vs absent: internal/swarm/profile.go implements only strict profile parsing/validation and a Preimage hash (ProfileExitCode=2 refusals). Nothing dispatches a resolved profile to a mixed Go/Zen pool, nothing records requested-versus-observed provider/model identity, and no per-profile concurrency cap exists (the only cap is the pre-existing swarm.WorkerCap=64 on `--workers`, cmd/nova-swarm/main.go:782-822).

Left owed
`git status --short` (run in <JOBDIR>/repo) printed nothing.
