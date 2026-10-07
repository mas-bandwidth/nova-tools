# nova-sprint: audit findings
Auditor: freddy (inception/mercury-2.5)
Date: 2026-10-07

This is a cold audit of the sprint command package (cmd/nova-sprint), its engine and store packages (internal/sprint, internal/sprint/store), the dashboard and wire packages (internal/sprintdash, internal/sprintwire), the sprint spec (docs/SPEC-SPRINT.md), and the sprint models under tla (SprintEvents.tla, SprintRules.tla).

## URGENT (must fix before v1.1.0)

1. **docs/SPEC-SPRINT.md:107-112**: The spec states that the `friends` table should include an `active` column showing "how long ago her session last wrote a file". The code at internal/sprint/friend_health.go:285-295 shows FriendBeat handling but does not persist the `active` timestamp in beat records. The beat record lacks the field to carry session activity, so the table cannot show it. Evidence: FriendBeat struct in friend_health.go lacks `Active` field; beat write in friend_deal.go doesn't record session activity. Fix: Add `Active` to FriendBeat struct and persist in beat record.

2. **internal/sprint/steps_tick.go:1840-1858**: The tick's fence rule for friends may allow a card to proceed when a friend's session has gone idle. The FriendFinishWindow check (30 minutes) may be insufficient to prevent work on a dead session. Evidence: FriendFinishWindow = 30 * time.Minute constant at friend_stall.go:5; session pong checks at presence.go:245-252 use 10-minute window but tick doesn't combine this with activity. Fix: Cross-check friend session pong with beat deadline in tick's friend card judgment.

3. **internal/sprint/friend_deal.go:480-500**: When a friend goes `down` due to rate limits, her cards remain in `working` state and may not be properly accounted. The tick's working count for the friend row shows 0 but the cards' state isn't updated. Evidence: friend_deal.go shows friend row's working count resets to 0 when down, but card states remain `working`. Fix: Move friend's working cards to `ready` when she goes down.

4. **cmd/nova-sprint/where_landed_series.go:50-70**: The landedSeries computation may double-count cards that have multiple work attempts. The code tracks "first landing" but the `worker` identification depends on the last ok move, which can be stale for cards that finish on different machines. Evidence: where_landed_series.go line 62 tracks first ok move time but uses last ok row's worker. Fix: Track which worker actually completed the work, not just the last one.

5. **internal/sprint/store/card.go:180-195**: The card state transition from `review` to `merging` does not validate that the card has all required approvals. The spec at SPEC-SPRINT.md:520-530 says approvals are needed but the store schema lacks the field to enforce it. Evidence: Card struct lacks `Approvals` field; steps_review.go shows approval tracking is in memory only. Fix: Add approval state to card schema.

6. **internal/sprint/cost_view.go:95-108**: Cost tier allocation rounding may lose pennies due to Go's float64 arithmetic on small cent values. The spec says "round up to the next cent" but the code uses simple division and rounding. Evidence: cost_view.go:103 uses math.Round on cents which doesn't guarantee rounding up; spec section 2 says "rounded up to the next cent". Fix: Use ceiling function for cent rounding.

## NEXT (for next release)

1. **internal/sprint/land_repair.go:120-145**: The land repair logic has a loop that may run for many seconds when many cards need repair. No deadline is enforced. Evidence: land_repair.go loop lacks context timeout. Fix: Add context deadline to repair pass.

2. **internal/sprint/settings.go:280-300**: Settings persistence uses a single Redis key without backup. A corruption here loses all configuration. Evidence: settings.go:290 shows write to `sprint.settings` with no backup. Fix: Add write-through backup to a second key.

3. **docs/SPEC-SPRINT.md:1100-1120**: The `where` command's `--json` output lacks documentation for the `friend.window` field that appears in rows. Evidence: SPEC-SPRINT.md has no `window` field documented in where JSON. Fix: Add field documentation to spec.

4. **internal/sprint/friend_health.go:150-170**: The friend health check may accept a stale pong if the generation hasn't changed. Evidence: friend_health.go:160 only checks generation, not pong age. Fix: Add age check on pong records.

5. **internal/sprint/store/epoch.go:80-100**: Epoch records are not deleted when a sprint is cleared, causing growth in the store. Evidence: store/epoch.go:95 shows Clear removes cards but not epoch metadata. Fix: Add epoch record cleanup to Clear.

---
urgent=6 next=5
