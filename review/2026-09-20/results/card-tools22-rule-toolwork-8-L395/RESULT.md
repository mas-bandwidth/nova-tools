RESULT tools22-rule-toolwork-8-L395 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 8 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:395 rule 8
PKG internal/docs
ASK An implementation would have to add the `nova-pulse accept` verb with `control=<id>` = first twelve hex of the SHA-256 of (gate binary build identity, fixture tree digest, bench certification id), refusing to print `ACCEPT OK` unless a passing `ACCEPT SELFTEST` for that same id is on file under `<root>/accept/control/<id>` (running the selftest itself when none is, `control-red` on failure), and have `harvest` copy `control=` onto the PR body with an `ACCEPT OK` lacking a control on file read as `control-stale`/`ABSTAIN`.

The whole rule 8 machinery is absent. `nova-pulse`'s verb dispatch has no `accept` case (cmd/nova-pulse/main.go:259-307 lists help/version/pool/launch/fill/cut/harvest/beat/watch/manager/status/progress/gate/run/triage/sweep/reap/hygiene/fleet/wake/sleep/width; `gate` is the unrelated dev-red STOP gate). The spec's own work list marks this item unbuilt: "T4 (#1649) | builder: no gate yet | `accept --selftest`: the fixture repository, the twelve one-edit seeds, `control=<id>`, OK refused without a control on file, and `gate-weakened` with its own red test | §1 rules 6-8" (docs/SPEC-TOOLWORK.md:979).

Greps run (all `--include='*.go'` over the whole tree):
- `grep -rn "control-red\|control-stale\|accept/control\|control="` -> no code hits
- `grep -rn "ACCEPT OK\|ACCEPT SELFTEST\|ACCEPT REJECT\|ACCEPT ABSTAIN\|gate-weakened"` -> only internal/swarm/lintheader.go:76, a docstring quoting the spec, no implementation
- `grep -rn "HARVEST OK"` -> no hits
- `grep -rn "case \"accept\""` -> only internal/sandbox/egress.go:446, unrelated
- `grep -rn "control" cmd/nova-pulse/ cmd/nova-merge/` -> only the queue/control/lanes.tsv and machines.tsv registries, no accept-gate control id
- `ls cmd/nova-pulse/testdata/accept` -> does not exist

UNGUARDED does not apply: there is no code, hence no test. Verdict ABSENT, not NOT-CODE: the rule binds the `accept` gate binary and `harvest`, which are code obligations the spec itself schedules as builder work not yet landed.

Left owed