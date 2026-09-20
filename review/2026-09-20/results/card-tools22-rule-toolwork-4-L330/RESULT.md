RESULT tools22-rule-toolwork-4-L330 sha=5298f6be12ea
ABSENT
SPEC docs/SPEC-TOOLWORK.md:330 rule 4
PKG internal/docs
ASK An implementation would have to run, before any push, a gate that checks in a fixed order with the first failure deciding — the §3 hygiene checks (identity, stray-file, secret, out-of-path), then the kind's shape check (named-test-missing, no-test), then the base's tests surviving (test-weakened at=<test>), then build/vet/red-at-head on the changed packages, then the negative control (`nova-review mutate` printing PASS, MUTATE GREEN being reason=vacuous-test, the named TEST red as named-test-not-red), then the kind's own control — and emit each rejection tokenized.
GUARDED-BY: n/a (ABSENT)
Deciding evidence:
  The gate itself is not in the tree. No `accept` verb in the nova-pulse CLI (cmd/nova-pulse/main.go), no `ACCEPT OK`/`ACCEPT REJECT`/`ACCEPT SELFTEST` output anywhere, no `cmd/nova-pulse/testdata/accept/` fixture, and none of the rule's reject tokens (`test-weakened`, `named-test-missing`, `no-test`, `named-test-not-red`, `red-at-head`, `vacuous-test`) occur in any Go source — only `no-tests-changed` in cmd/nova-review/mutate.go:96 and a forward-looking comment, internal/review/mutate.go:672 ("the accept gate rejects such a file as `vacuous-test`").
  The code that does exist for the gate's subject says it is not built: internal/pulse/harvestguard.go:28 — "WHAT THIS STILL DOES NOT DO: #1650's `accept` gate between verify and push. That is #1650's lane (toolwork T05), and Stella holds the adoption word on it."
  The spec's own work list agrees: docs/SPEC-TOOLWORK.md:978 — T3 (#1648) `nova-pulse accept`: "builder: no gate yet"; the red tests rule 4 demands (SPEC-TOOLWORK.md:435-442, e.g. `accept-runs-before-any-push`, `a-deleted-base-test-is-test-weakened`) exist in no *_test.go.
  The one built block is a §3 ingredient, not the gate: internal/hygiene/hygiene.go:202,278,301,411 emits the `identity`/`out-of-path`/`stray-file`/`secret` tokens, and internal/review/mutate.go:134-139 already prints `PASS`/`FAIL` for the negative control — but nothing orchestrates them into the ordered, first-failure-decides gate rule 4 describes.
Greps run:
  grep -rn "test-weakened" --include='*.go' .          -> no Go matches
  grep -rn "named-test-not-red\|named-test-missing\|red-at-head\|no-test" --include='*.go' .  -> only cmd/nova-review/mutate.go:96 no-tests-changed
  grep -rn "vacuous" --include='*.go' .               -> only internal/review/mutate.go:672 comment
  grep -rn "ACCEPT OK\|ACCEPT REJECT\|ACCEPT SELFTEST\|testdata/accept\|accept-runs-before" --include='*.go' . -> only internal/swarm/lintheader.go:76 comment
  grep -rn '"accept"\|accept\b' internal/pulse/cli.go cmd/nova-pulse/main.go -> no accept verb
  ls cmd/nova-pulse/testdata/ -> example-pulse pool.tsv roadmap.sexp sources.tsv templates (no accept/)
Files read: docs/SPEC-TOOLWORK.md (310-356, 360-460, 960-1000), internal/pulse/gate.go, internal/pulse/harvestguard.go, internal/review/mutate.go (1-160, 660-673), internal/pulse/cutkind.go, internal/hygiene/hygiene.go (token sites).
Left owed: 0