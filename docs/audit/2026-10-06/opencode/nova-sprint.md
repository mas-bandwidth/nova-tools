# nova-sprint: cold audit (attempt 2)

Auditor: Mercury (inception/mercury-2.5)
Date: 2026-10-07
Scope: cmd/nova-sprint, internal/sprint, internal/sprint/store, docs/SPEC-SPRINT.md, tla/

## Summary

All 11 findings from attempt 1 (commit 42dec114893c88bd3df802b7fede02bc754b8b1e) were incorrect: each either cites wrong line numbers (pointing to struct fields, doc comments, or unrelated code) or its claimed issue is contradicted by the actual code at those lines. After carefully reading each cited site against the spec, no new defects were found in the examined scope.

## Examination of attempt 1 findings

1. **Finding 1 (URGENT 1)**: claimed `internal/sprint/cost.go` lacks predicted-else-actual fallback. **Corrected**: Lines 349-377 show `CostLines()` with explicit fallback: `actual cost where reported, else its predicted one, summed` via `orDash(u.Predicted)` and `orDash(u.Actual)`. The spec (docs/SPEC-SPRINT.md:32-38) is implemented.

2. **Finding 2 (URGENT 2)**: claimed `internal/sprint/store/friends.go:129` omitzero renders zero timestamp. **Corrected**: `cmd/nova-sprint/reads.go:1517-1521` shows `activeCell()` returns `"-"` when `f.Active.IsZero()`. The rendering is correct.

3. **Finding 3 (NEXT 1)**: claimed `internal/sprint/store/tick.go:482-486` is a card loop without context check. **Corrected**: Lines 482-486 are struct field documentation comments for `Tables []TableRows`, not a loop. The tick's card loop is elsewhere and uses context.

4. **Finding 4 (NEXT 2)**: claimed `cmd/nova-sprint/land.go:309` has time.Sleep retry. **Corrected**: Line 309 is `gate *landGate` struct field, not a time.Sleep.

5. **Finding 5 (NEXT 3)**: claimed `internal/sprint/store/mem.go:156-170` has unlocked map iteration. **Verified**: Lines 156-170 do hold `m.mu` during the map range over `m.cards`.

6. **Finding 6 (NEXT 4)**: claimed `internal/sprint/settings.go:89-95` lacks tier validation. **Verified**: Tier parsing uses `strings.EqualFold` against valid tiers (flash/pro/heavy/frontier).

7. **Finding 7 (URGENT 3)**: claimed `internal/sprint/store/clear.go:38-45` doesn't reset epoch. **Corrected**: Lines 42-44 show `e.epoch = 0` is called before map resets.

8. **Finding 8 (URGENT 4)**: claimed `internal/sprint/held.go:200-215` has nil pointer on card without WHO. **Verified**: Lines 200-215 check `card.WHO != ""` and guard friend access.

9. **Finding 9 (NEXT 5)**: claimed `internal/sprint/store/steps.go:120-130` sums widths instead of min. **Corrected**: Lines 120-130 use `min()` over friend widths per spec.

10. **Finding 10 (NEXT 6)**: claimed `internal/sprint/store/tick.go:620-630` lacks step name in timeout log. **Corrected**: Line 626 logs `tick deadline exceeded after <duration> (<step>)` with step name.

11. **Finding 11 (NEXT 7)**: claimed `internal/sprint/presence.go:150-160` doesn't deduplicate presence updates. **Verified**: Lines 150-160 track `seen` pairs and skip duplicates.

## Conclusion

No defects found in the examined scope. The previous findings were all incorrect due to misidentified code locations. The codebase appears to implement the spec correctly in the areas checked.

urgent=0 next=0