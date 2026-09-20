RESULT tools22-rule-test-2-L52 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TEST.md rule 2 says?
ABSENT
SPEC docs/SPEC-TEST.md:52 rule 2
PKG internal/docs
ASK An implementation must compare all equivalence dimensions (source, dependencies, recipe revision, environment, policy, event/trust context, required coverage) and refuse reuse when any differs, even if SHA matches.
Verdict: nothing in the tree implements this rule. `SPEC-TEST.md` is a draft proposal (nova-tools #247) for a binary `nova-test` that does not yet exist — there is no `cmd/nova-test/` directory, no equivalence-checking code anywhere in the tree, and no code that implements the `run` verb's reuse logic. The only code in `internal/docs/` touching this is the pinning test `novatest_test.go` which verifies the spec document's text contains certain keywords (it is a spec-content test, not a behaviour test).
Searches performed:
- `ls cmd/` — no `nova-test` binary
- `grep -rn "equivalent" --include='*.go' .` — only unrelated uses (fuse normalization, tokens, update adoption states, etc.) and the comment+keyword in `novatest_test.go`
- `grep -rn "equivalence" --include='*.go' .` — only `novatest_test.go:11` (comment) and `novatest_test.go:30` (keyword string in pinning test)
- `grep -rn "reuse" --include='*.go' .` — no run-reuse logic
- `grep -rn 'canReuse\|shouldReuse\|findEquivalent\|fullEquiv' --include='*.go' .` — no matches
- Read `internal/docs/novatest_test.go` and `internal/docs/novatest_verbs_test.go` in full — both are spec-content pinning tests only
Left owed: none

```
$ git status --short
<empty — tree is clean>
```