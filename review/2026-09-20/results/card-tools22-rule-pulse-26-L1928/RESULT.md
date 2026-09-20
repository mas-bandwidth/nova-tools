RESULT tools22-rule-pulse-26 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 26 says?
ABSENT
SPEC docs/SPEC-PULSE.md:1928 rule 26
PKG internal/pulse
ASK An implementation would have to provide a `handoff --to <name>` verb that ends the shift loop (printing `SHIFT END` on stdout), frees `<root>/queue/OWNER`, writes `<root>/queue/HANDOFF` carrying the last width, in-flight-by-bench, pending, escalations, benches and state, records one note to the successor on the fixture bus, and prints `HANDOFF OK to=<name> inflight=<n> pending=<n> escalations=<n>`.

Where I looked:
- `grep -rn "handoff\|HANDOFF" --include='*.go' .` — the only non-test hits are `internal/decide/ladder.go` (a D1 schema-issues comment), `cmd/nova-wake/serve.go` (a prose comment), `cmd/nova-work/*` (`session handoff` / `--ask handoffs`, a different WORK-tier feature). No pulse `handoff --to` verb, no `HANDOFF OK` string, no `OWNER`/`HANDOFF` file writer.
- `grep -rn "SHIFT END\|HANDOFF OK\|OWNER\|HANDOFF" --include='*.go' internal/pulse/` — `OWNER` is only READ in `internal/pulse/watch.go:176-179` (`readWatchOwner`), never written; no `HANDOFF` path is ever written.
- Read `cmd/nova-pulse/main.go:250-314` — the full subcommand switch (pool/launch/fill/cut/harvest/beat/watch/manager/status/progress/gate/run/triage/sweep/reap/hygiene/fleet/wake/sleep/width) has no `handoff` and no `takeover` case.
- `grep -rn "takeover\|TAKEOVER" --include='*.go' .` — zero hits outside tests.
- The nearest code — `internal/pulse/manager.go:217` prints `SHIFT END cycles=… decisions=… escalations=…` when a manager shift ends on its own `--hours` bound, but that is the `manager` verb, not a `handoff --to` verb, and it writes no OWNER/HANDOFF files and sends no handoff note.

Left owed
