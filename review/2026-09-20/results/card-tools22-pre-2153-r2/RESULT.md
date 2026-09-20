RESULT tools22-pre-2153-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2153 at head 863795ba97ec: harvest: verify fence epoch and RUN token at effect owner
PREREAD 2153 claims=5 proven=5 unproven=0 defects=0 high=0
PR 2153
HEAD 863795ba97ec1d782e9e2450535e7500974c106c
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 5 production, 2 test
LINES +1001 -5

CLAIMS
1. The effect owner verifies the card's publication-fence epoch and a bounded RUN action token at its own linearization.
   PROVEN-BY internal/harvest/spec_test.go:114 TestDurableLaunch5_TwoLiveWorkersCurrentFencePublishes — verifies stale workers refuse and current workers publish when fence epoch matches.

2. RESULT, harvest push, and accept each require their own separate RUN action token.
   PROVEN-BY internal/harvest/spec_test.go:183 TestDurableLaunch5_SeparateRUNActionTokens — RESULT token must not push or accept; missing token must refuse.

3. The effect owner does not write lifecycle/events.jsonl.
   PROVEN-BY internal/harvest/spec_test.go:235 TestEffectOwnerDoesNotWriteEventsJSONL — verifies events.jsonl does not exist after successful commit.

4. Empty token scope must refuse the effect.
   PROVEN-BY internal/harvest/spec_test.go:258 TestReview2153EmptyScopeMustRefuse — verifies empty scope returns ErrScope.

5. An identical retry of a consumed token reconciles without running the effect again; a different payload with the same token ID must refuse.
   PROVEN-BY internal/harvest/spec_test.go:281 TestReview2153TokenReplayMustNotRepeatEffect — identical retry reconciles (n stays 1), different payload refuses.

DEFECTS none

QUESTIONS FOR THE REVIEWER
1. How is the RUN action token (ID, generation, scope, expiry) actually minted and distributed at the fleet level before this PR is merged?
2. Why does Owner store a `done map[string]Token` for replay detection but rely on an external Cards interface for card state—could this split cause consistency issues?
3. How does the real internal/control package differ from FakeControl in terms of lock semantics and expiry handling?

Left owed
None. All production files (internal/harvest/capture.go internal internal/harvest/cards.go, internal/harvest/fake.go, internal/harvest/owner.go, internal/pulse/harvest.go) and test files (internal/harvest/spec_test.go, internal/pulse/harvest_effect_test.go) were read in full.

git status --short
git rev-parse HEAD
863795ba97ec1d782e9e2450535e7500974c106c
