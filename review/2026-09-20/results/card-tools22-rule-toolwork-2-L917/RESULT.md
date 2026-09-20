RESULT tools22-rule-toolwork-2-L917 sha=5298f6be12ea
GAP internal/onboarding/transcript.go:627 — three things the spec names do not exist in the code: `CompareTranscript(doc, got, volatile)` is not present (the comparator is `Compare(s Step, res Result, norms []Norm)` at transcript.go:627); `onboarding.Volatile` as a shared table of run-owned values does not exist (Norm constructors `Instant`, `HexID`, `Path` serve that role individually via variadic args); and `printed map[string]bool` was not fully deleted — it still exists in `cmd/nova-bus/firstrun_test.go:165` and `cmd/nova-work/firstrun_test.go:155`. The set-of-shapes helper is absent (deleted), but two firstrun tests still use the printed-map pattern instead of calling Compare/Execute.

SPEC docs/SPEC-TOOLWORK.md:917 rule 2
PKG internal/onboarding
ASK An implementation must provide one comparison function (`CompareTranscript`) that a firstrun_test.go may only call, comparing output lines as written except for run-owned values declared in a shared `Volatile` table; and must delete both the set-of-shapes helper and every `printed map[string]bool`.

The deciding lines from the spec (docs/SPEC-TOOLWORK.md:917):
```
2. **One comparator, in one place.** `onboarding.CompareTranscript(doc, got, volatile)`
   is the only comparison a `firstrun_test.go` may make. Values are compared **as
   written**; the only values matched by shape are the fields in one shared table of
   run-owned values (`at=`, `took=`, `created=`, a temp path, a fresh sha) —
   `onboarding.Volatile` — and a test may name a field from that table and may not
   invent one. The set-of-shapes helper and every `printed map[string]bool` are
   deleted.
```

Gaps found:
- `CompareTranscript` — not found. The comparator is `Compare(s Step, res Result, norms []Norm) []Problem` at internal/onboarding/transcript.go:627, with different signature and no wrapper accepting doc/got/volatile.
- `Volatile` — not found. No `type Volatile` or `func Volatile()` exists. Run-owned values use individual `Norm` types (Instant, HexID, Path, GoBuild, Version, Elide) passed as variadic `[]Norm` — not a shared named table.
- `printed map[string]bool` — NOT deleted everywhere. Still present in:
  - `cmd/nova-bus/firstrun_test.go:165` — `var printed map[string]bool` with shape-checking loop at lines 175-207
  - `cmd/nova-work/firstrun_test.go:155` — `var printed map[string]bool` with shape-checking at lines 163-177
- Test `TestEveryTranscriptIsExecutedLineForLine` (rule 3's class test) — not found in `internal/ci/`. Rule 2's enforcement ("the class test ... fails for any firstrun_test.go that compares any other way") has no corresponding test.

NOT-GUARDED-BY: These gaps mean several firstrun tests (nova-bus, nova-work) still compare by shape rather than by the single comparator.

Greps run:
- `grep -rn "CompareTranscript\|compareTranscript" --include='*.go' repo/` → no output
- `grep -rn "Volatile" --include='*.go' repo/internal/onboarding/` → no type/func named Volatile
- `grep -rn "printed\s*map\[string\]bool" --include='*.go' repo/cmd/*/firstrun_test.go` → nova-bus, nova-work
- `grep -rn "set.of.shapes\|shapes" --include='*.go' repo/internal/onboarding/` → no set-of-shapes helper
- `grep -rn "volatile-field-outside-the-table-is-refused" --include='*.go' repo/` → no output
- `grep -rn "TestEveryTranscriptIsExecutedLineForLine" --include='*.go' repo/` → no output

Left owed

git status --short
(on empty tree)
