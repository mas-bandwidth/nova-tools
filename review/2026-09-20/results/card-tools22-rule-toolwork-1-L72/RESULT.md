RESULT tools22-rule-toolwork-1-L72 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 1 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:72 rule 1
PKG internal/swarm, internal/decide, cmd/nova-pulse
ASK Compute a per-kind-and-area track record from the last N gated cards in `<queue>/decide/outcomes.jsonl` and expose it through `nova-pulse trust --queue <dir> [--kind <kind>] [--area <area>] [--max <n>]`.
The code has none of this computation. Supporting scaffolding exists:
  - `internal/swarm/lintheader.go:89-91` defines `TrustState` (the per-kind state type)
  - `internal/swarm/lintheader.go:325-371` defines `ReadTrustFixture` (reads a pre-made TRUST-format fixture file, not outcomes.jsonl)
  - `internal/decide/harvestclass.go:324-361` defines `AppendOutcomeRow` (appends rows to outcomes.jsonl)
  - `cmd/nova-pulse/main.go:258-311` has no `case "trust":` — the verb is absent
  - The code explicitly states at `internal/swarm/lintheader.go:331`: "THE VERB DOES NOT EXIST YET"
  - `internal/pulse/` has no trust-related production code
Greps run: `grep -rn "trust\|TRUST\|track.*record\|outcomes" --include='*.go' internal/docs/ cmd/nova-swarm/ cmd/nova-pulse/ internal/pulse/ internal/swarm/ internal/decide/` and `grep -rn "nova-pulse trust" --include='*.go' .`
Left owed
git status --short