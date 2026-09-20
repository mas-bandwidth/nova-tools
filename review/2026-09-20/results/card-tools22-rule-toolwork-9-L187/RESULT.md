RESULT tools22-rule-toolwork-9-L187 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 9 says?
ABSENT
SPEC docs/SPEC-TOOLWORK.md:187 rule 9
PKG internal/docs
ASK An implementation would have to run a mechanical accept gate (§1) on every code-changing card after its work is done and before harvest pushes anything, letting the gate's ACCEPT OK / REJECT verdict — never the worker's word nor any eligibility readiness or confidence — decide whether the result is kept.
WHERE I LOOKED
- `grep -rn '"accept"' --include='*.go' cmd/ internal/`: only cmd/nova-work/socketverbs.go:94,661 (an unrelated socket verb), internal/sandbox/egress.go:446 (firewall accept), internal/decide/harvestclass.go:339 (an evidence struct field). No `nova-pulse accept`.
- `grep -rn 'ACCEPT OK|ACCEPT REJECT|ACCEPT ABSTAIN|ACCEPT REFUSED|ACCEPT SELFTEST' --include='*.go' .`: one hit, internal/swarm/lintheader.go:76, prose help text explaining the abstain token — not an implementation.
- `grep -rn 'case "accept"|"accept":' cmd/ internal/`: nothing in cmd/nova-pulse; cmd/nova-pulse/main.go's verb dispatch (lines ~272-290) has no accept case.
- Files read: internal/pulse/harvestguard.go, internal/pulse/gate.go, internal/check/attest.go, docs/SPEC-TOOLWORK.md §1 and the work list.
DECIDING LINES
- internal/pulse/harvestguard.go:28-29: "WHAT THIS STILL DOES NOT DO: #1650's `accept` gate between verify and push. That is #1650's lane (toolwork T05), and Stella holds the adoption word on it."
- docs/SPEC-TOOLWORK.md:5 (preamble): "**No code lands with this document**; the work list at the end is the order." T3 (#1648) `nova-pulse accept` and T5 (#1650) `harvest` runs `accept` are both "builder: no gate yet" (lines 967, 969).
- internal/pulse/gate.go:1-19 is only the pre-existing dev-red STOP gate, which the spec itself distinguishes: "`nova-pulse gate` is taken (it is the dev-red STOP gate)" (docs/SPEC-TOOLWORK.md:264-265).
GUARDED-BY none — nothing tests a behavior that does not exist
Left owed none