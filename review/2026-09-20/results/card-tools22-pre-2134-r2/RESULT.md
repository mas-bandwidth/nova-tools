RESULT tools22-pre-2134-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2134 at head d90d2e9952c4: harvest: effect-owner fence epoch and RUN action token verification
PREREAD 2134 claims=7 proven=7 unproven=0 defects=0 high=0

PR 2134
HEAD d90d2e9952c4a76340e24de4a42b7ac81592bbb6
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 3 production, 2 test
LINES +1211 -7

CLAIMS
1. Internal/harvest package provides EffectOwner, CardAuthority, and ControlAuthority for linearization verification.
   PROVEN-BY internal/harvest/effect_test.go:329 TestRequired5_TwoWorkersDifferentEpochs

2. Two live workers presenting different attempt credentials: stale fence epoch is refused, current worker publishes/pushes/accepts.
   PROVEN-BY internal/harvest/effect_test.go:329 TestRequired5_TwoWorkersDifferentEpochs

3. Authoritative RESULT publication, harvest push, and harvest accept each require a separate RUN action token.
   PROVEN-BY internal/harvest/effect_test.go:516 TestActionTokenSeparation

4. During PAUSE, private attempt-local capture runs, but authoritative RESULT, push, and accept are refused.
   PROVEN-BY internal/harvest/effect_test.go:580 TestRequired9_PauseRefusesAuthoritativeResultAndPublication

5. Effect-owner strictly reads projections and does NOT write events.jsonl.
   PROVEN-BY internal/harvest/effect_test.go:739 TestEffectOwnerDoesNotWriteEventsJSONL

6. Refusal consumes no third attempt.
   PROVEN-BY internal/harvest/effect_test.go:465 TestRequired5_TwoWorkersDifferentEpochs

7. Wire optional EffectOwner into pulse.Harvest for push and openPR.
   PROVEN-BY internal/pulse/harvest_effect_test.go:1026 TestHarvestWithEffectOwner_CurrentWorkerSucceeds

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. The integration test harness in internal/pulse/harvest_effect_test.go uses fakeGit, fakeGH, and fakeTool mocks. What is the expected behavior when EffectOwner is nil? Should the old path (without verification) remain as a fallback for backward compatibility?
2. DiskCardAuthority reads card projections from `<lifecycleDir>/cards/<card>.json`. The card file format uses `fence` as a fallback to `fence_epoch`. Is this backward compatibility intentional, and when is `fence` expected to be non-zero while `fence_epoch` is 0?
3. TestRequired5_TwoWorkersDifferentEpochs verifies that the stale worker does not publish, push, or accept. Does the test also need to verify that no state change occurs in the CardAuthority or ControlAuthority after the stale worker's refusal?

Left owed
- Reviewed full diff: 5 files, 1211 additions, 7 deletions
- Read all production code in internal/harvest/ (effect.go, types.go)
- Read all test code in internal/harvest/effect_test.go and internal/pulse/harvest_effect_test.go
- Read modified pulse/harvest.go to understand EffectOwner integration
- Did not read base branch files (not in diff)

git status --short
git rev-parse HEAD d576bf6bbabb39068096a97b4560de9b5e245970
