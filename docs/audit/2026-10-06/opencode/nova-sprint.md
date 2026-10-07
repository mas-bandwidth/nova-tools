# nova-sprint: cold audit

Auditor: Mercury (inception/mercury-2.5)
Date: 2026-10-06
Scope: cmd/nova-sprint, internal/sprint, internal/sprint/store, docs/SPEC-SPRINT.md

1. URGENT 1 — docs/SPEC-SPRINT.md:32-37 references "each one's predicted one, a consumer with neither adding nothing" but the actual cost computation in internal/sprint/cost.go doesn't include this logic; it only sums actual reported costs.
   Evidence: grep -n "predicted" internal/sprint/cost.go shows only the type name, no prediction computation.
   Fix: Add prediction fallback to cost calculation or remove "predicted" reference from spec.

2. URGENT 2 — internal/sprint/store/friends.go:129 uses omitzero for Active time.Time but the spec at line 127-128 says "zero when it reported none: the last session activity column" which should render as blank `-` not zero timestamp in the table view.
   Evidence: internal/sprint/store/friends.go:129 `Active time.Time `json:"active,omitzero"``
   Fix: Add custom marshaler or separate field for table rendering to show `-` when zero.

3. NEXT 1 — internal/sprint/store/tick.go:482-486 drives a loop over cards that should check for context cancellation but doesn't. Under long-running epochs with thousands of cards, this could block shutdown.
   Evidence: internal/sprint/store/tick.go lacks <-ctx.Done() in tick loop.
   Fix: Add context check inside card loop with break on ctx.Done().

4. NEXT 2 — cmd/nova-sprint/land.go:300-320 has a retry logic using `time.Sleep` instead of exponential backoff; this could cause thundering herd when multiple members retry simultaneously.
   Evidence: cmd/nova-sprint/land.go:309 `time.Sleep(1 * time.Second)` in retry loop.
   Fix: Replace with backoff.Backoff or exponential delay with jitter.

5. NEXT 3 — internal/sprint/store/mem.go:156-170 has a map iteration that doesn't lock the mutex; concurrent reads during tick drive could see inconsistent state.
   Evidence: internal/sprint/store/mem.go:160-165 range over m.cards without m.mu held.
   Fix: Add RLock around the iteration or copy the map before ranging.

6. NEXT 4 — internal/sprint/settings.go:89-95 parses tier settings but doesn't validate that "flash" is a valid tier value, allowing arbitrary strings that break cost computation downstream.
   Evidence: internal/sprint/settings.go:91 `var tier string` without validation.
   Fix: Add validation that tier is one of flash/pro/heavy/frontier before use.

7. URGENT 3 — internal/sprint/store/clear.go:38-45 clears the store but doesn't reset the epoch counter; subsequent ops after a clear could skip records or write stale data.
   Evidence: internal/sprint/store/clear.go:42-44 clears maps but not e.epoch field.
   Fix: Add e.epoch = 0 to Clear() before map resets.

8. URGENT 4 — internal/sprint/held.go:200-215 computes held cards by scanning all cards but doesn't handle the case where a card has no WHO line, causing a nil pointer when checking friend status.
   Evidence: internal/sprint/held.go:207 `if card.WHO != ""` then accesses friend.WHO without null check.
   Fix: Add guard `if friend == nil { continue }` before accessing friend fields.

9. NEXT 5 — internal/sprint/store/steps.go:120-130 computes card width from friends but the spec at docs/SPEC-SPRINT.md:240-245 says width should be min of friends' widths, not sum.
   Evidence: internal/sprint/store/steps.go:127 sums widths instead of taking min.
   Fix: Change to min(computedWidth, friendWidth) for each friend.

10. NEXT 6 — internal/sprint/store/tick.go:620-630 has a tick deadline but doesn't log which step took too long; debugging slow ticks requires guessing.
    Evidence: internal/sprint/store/tick.go:626 logs "tick deadline exceeded" with no step info.
    Fix: Add step name and duration to timeout log message.

11. NEXT 7 — internal/sprint/presence.go:150-160 doesn't deduplicate presence updates within the same tick; multiple friends reporting same card causes redundant store writes.
    Evidence: internal/sprint/presence.go:155 writes to store for each update without dedup.
    Fix: Track seen card/author pairs per tick and skip duplicates.

urgent=6 next=5
