RESULT tools22-rule-toolwork-5-L512 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOOLWORK.md:512 rule 5
PKG internal/docs
ASK The implementation must check, at `accept` (gate) and `harvest` (count) time, that every leg in the card's LEGS: line is covered by a live certification for the bench, and refuse/abstain (bench-uncertified) when not; the router must also refuse to send a card to a bench missing any of its legs.
The rule specifies mandatory code behaviour in three places (accept, harvest, route), so it is a code question.
What was checked:
- `grep -rn "bench-uncertif" --include='*.go' .` — no match
- `grep -rn "uncertified" --include='*.go' .` — only a comment in internal/fleet/registry.go:75 about the fill pool
- `grep -rn "certif" --include='*.go' . | grep -i "leg\|LEGS"` — no match
- `grep -rn "ACCEPT ABSTAIN" --include='*.go' .` — only a comment in internal/swarm/lintheader.go:76 mentioning reason=paused, never reason=bench-uncertified
- `case` switch in cmd/nova-pulse/main.go:258-308 has no "accept" or "certify" case
- `internal/pulse/harvestguard.go:28` — explicitly says "WHAT THIS STILL DOES NOT DO: #1650's accept gate between verify and push"
- Registry has CertifiedBenchNames() for fill pool only (internal/fleet/registry.go:225-237), but no per-leg certification check against card LEGS
- No tools/legs.tsv exists (T19/#1663, not yet landed)
- No `internal/pulse/cardheader.go` exists (T03/#1721, not yet landed)
- The red tests listed in the spec (accept-abstains-on-an-uncertified-leg, route-refuses-a-bench-without-the-leg, certify-runs-every-probe-inside-the-wall) have no corresponding test functions in the tree
Left owed: implement nova-pulse certify, tools/legs.tsv, nova-pulse accept, and the certification checks in accept, harvest, and route (toolwork T3 #1648, T5 #1650, T19 #1663).

git status --short: (nothing printed)