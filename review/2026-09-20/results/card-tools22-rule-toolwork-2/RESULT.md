RESULT tools22-rule-toolwork-2 sha=5298f6be12ea — does the code at this base do what docs/SPEC-TOOLWORK.md rule 2 says?
GAP internal/onboarding/onboarding.go:93
SPEC docs/SPEC-TOOLWORK.md:917 rule 2
PKG internal/docs
ASK Make onboarding.CompareTranscript(doc, got, volatile) the one and only comparison a firstrun_test.go may make, compare values as written except for the fields of the one shared onboarding.Volatile table (at=, took=, created=, a temp path, a fresh sha), and delete the set-of-shapes helper and every printed map[string]bool.

The code does not do what the rule says. The comparison machinery exists in internal/onboarding but under different names and without the rule's deletions.

The rule's named API is absent:
- `grep -rn "CompareTranscript" --include='*.go' .` -> no match in any Go file (only the spec doc itself).
- `grep -rn "Volatile" --include='*.go' .` -> no match in any Go file.

The set-of-shapes helper the rule says is deleted is still present and used:
- internal/onboarding/onboarding.go:93 `func Shape(line string) string {` — reduced both sides to a shape; still exported and consumed by 7 firstrun_test.go files (nova-board, nova-bus, nova-swarm, nova-tokens, nova-update, nova-version, nova-work).

`printed map[string]bool` comparators the rule says are deleted are still present:
- cmd/nova-bus/firstrun_test.go:165 `var printed map[string]bool`
- cmd/nova-work/firstrun_test.go:155 `var printed map[string]bool`
- cmd/nova-board/firstrun_test.go:364 `printed := map[string]bool{}`
- cmd/nova-memory/firstrun_test.go:459 `printedShape, printedEcho := map[string]bool{}, map[string]bool{}`

What exists instead is a per-step comparator — `onboarding.Compare(s Step, res Result, norms []Norm)` (internal/onboarding/transcript.go:627) driven by `Execute`/`ExecuteWith` and per-tool `Norm` declarations — which is a different design from a single `CompareTranscript(doc, got, volatile)` over a shared `Volatile` table, and does not compare whole outputs as-written.

Greps run: `grep -rn "CompareTranscript" --include='*.go' .`; `grep -rn "Volatile" --include='*.go' .`; `grep -rn "func Shape\|Shape(" --include='*.go' internal/ cmd/`; `grep -rn "printed map\[string\]bool\|printed := map\[string\]bool\|var printed map\[string\]bool\|printedShape, printedEcho" --include='*_test.go' cmd/ internal/`; `grep -rn "onboarding\." --include='firstrun_test.go' cmd/`.

Left owed: the CompareTranscript/Volatile design itself (no such function or table exists), the deletion of `Shape`, and the sweep of the tools still comparing by shape or by `printed map[string]bool` (nova-bus, nova-work, nova-board, nova-memory, nova-swarm, nova-tokens, nova-update, nova-version) onto the one comparator.