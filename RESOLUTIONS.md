# Integration of 2026-10-04: merge order and conflict resolutions

Branch `integration-2026-10-04` (under the coordinator's prefix), built from `origin/dev` (bc60d1f260) for the
coordinator's server and the fleet to run while the PRs land on dev through the merge
queue. Each resolution keeps both sides' intent; where they truly collide, the
later PR's change wins.

## Order

1. promo/2026-10-04-a (5302), clean; merged again at the end for its newest repair
   (a4c08b9b0e, pkgselect).
2. store-cpu (5287), lander-ledgers (5289), reads-by-width (5285),
   friend-ready (5304), staging-budget (5297), child-path (5291),
   fleet-tooling (5288): clean.
3. readers-lint (5293): conflicts, below.
4. sprint-comfort (5298): clean.
5. rework-bound (5283): conflicts, below.
6. claude-harness (5299): conflicts, below.
7. friend-take (5306): merged here, after 5299, not after 5304 as asked: the
   request arrived once 5304 was already in; it conflicted only with later
   additions to the same verb lists, below.
8. spend-rules (5300): SKIPPED, below.
9. script-go-exec (5301), card-generator (5282): clean.
10. nova-friend (5280), with friend-grok, friend-antigravity,
    friend-codex and friend-survey merged into it first (local branch,
    not pushed): conflicts, below.
11. friend-health (5305): conflicts, below.
12. bus-rename (5303): conflicts, below.
13. release-unreached-sentinel (5234): conflicts, below.
14. auto-sentinel: no such branch when the merges ended.

## Skipped: spend-rules (5300)

Not mergeable without real work: 25 conflict regions in 12 files, and the core
of them is two different designs of how reads are asked. 5285/5293 ask each read
of the reader with the greatest share of room (`readerRooms`, `pickByRoom`,
`freeReaders`, TickAsk rehearsing the placement), and the reference model orders
by load; 5300 asks a card's reads one at a time (`ReadsWanted`), asks the finder
first out of turn (`finderFirst`, `spent`/`passed` in the round), with its own
reference model (`nextReaders` returning `passed`). Combining them means choosing
how the finder's out-of-turn read interacts with room (the round's "passed over
once" has no counterpart in a room ordering), in the engine, the reference model
and SPEC-SPRINT section 6 together. The rest of 5300 (attempt cap, per-machine
deadline, cost columns, read tiers, `promoted --returned`) is mechanical against
this branch. 5306's deadline rule (a friend's card's deadline follows her median
run wall) is therefore in alone, under its own names.

## Resolutions

### nova-friend family (local, before 5280 went in)
- pkg/friend/adapter.go: `NewDeliverer` keeps every adapter: codex (5280
  codex), grok, antigravity, dsh and gemini (survey); `claude` alone is a plain
  Stub; the survey's refused harnesses keep their reasons. `Harnesses` is
  opencode, codex, claude, antigravity, dsh, gemini, grok, then the refused ones.
- adapter_test.go: the honest-refusal loop is `claude` alone; the unknown-harness
  line lists the merged `Harnesses`.
- docs/SPEC-FRIEND.md: the opening says six adapters are real (OpenCode; Codex,
  Antigravity, Grok below; DSH and Gemini from the survey) and Claude passive;
  the Codex paragraph, the Antigravity section and the Grok paragraph all kept
  ("is another real adapter" for Grok's "the second").
- docs/CLI.md: the harness list matches `Harnesses` (verbhelp_test pins it).

### readers-lint (5293)
- internal/sprint/steps_review.go: the later reads-by-width code
  (`s.freeReaders`) kept; 5293's loop used `have[rd]`, a map the newer code no
  longer has. 5293's comment on "a reader is asked an attempt once" moved onto
  `freeReaders` in readers.go.
- steps_tick.go TickAsk comment: the room rule (5285) and the one judgment per
  tick for the primaries that cannot be asked (5293, `cannotAskCond`).

### rework-bound (5283)
- coordinator.go verb classes: `reader retire` and `promoted` both.
- reads.go groupLine: the CRITICAL prefix (5283) and the alias suffix both.
- verbhelp.go effects: `reader retire`, `held`, `sentinels` and `promoted` all.
- verbs.go: drop takes `--one` (5283), rank keeps `--before` (HEAD); add keeps
  the SHARED: text of `--allow-shared-paths` and gains `--one`.
- steps_tick.go notify: the per-subject `fresh` list (HEAD) and the in-place
  update for the starving, overloaded, readers-behind and dev-behind judgments
  with their decisions (5283).
- SPEC-SPRINT.md verb table: add is HEAD's row with 5283's waves clause
  appended; drop is 5283's; rank and brief are HEAD's (5283 left them as on dev).

### claude-harness (5299)
- verbs.go: brief gains `--tier` (5299) beside the DEPENDS-ON re-point (HEAD);
  rework keeps HEAD's `answersWords` and takes 5299's tier text (heavy).
- internal/sprint/steps_edit.go: `BriefReq` has `Needs` and `Tier`; `Brief`
  tries `Tier` first, then the DEPENDS-ON-only path.
- cmd/nova-swarm/native.go: `nativeChildEnv(..., toolPath)` (HEAD's rename) and
  5299's headless private home after it.
- SPEC-SPRINT.md rows (rework, brief, stream set, set) merged word by word: the
  heavy tier added to HEAD's rows; brief is HEAD's row with 5299's `--tier`
  sentence appended.
- docs/TESTS.md: migrate to 28 (5299's 0028 after dev's 0027).

### friend-take (5306)
- coordinator.go: `friend take` and `friend level` beside `reader retire` and
  `promoted`.
- reads.go whereView: the ready-buffer fields (HEAD) and `Friends` (5306) both.

### friend-health (5305)
- store/friends.go: the roster entry keeps `Class` (5306) and `Reason`/`Until`
  (5305); `FriendRow` carries Class, Load, Report (5306) and Health, Reason,
  Until (5305); `SetFriendHeld(ctx, friend, held, who, reason, until, width)`
  takes both sides' arguments; `FriendRows` builds the row by 5305's
  `FriendPresence` status with 5306's class, load and report;
  `FriendBeatOf` (5306) and `HealthStep`/`FriendHealth` (5305) both.
- store/steps.go: `FriendTakeStep`, `FriendLevelStep` (5306) and `NoteStep` (5305).
- cmd/nova-sprint/friends.go: `cmdFriendHold` takes `--reason`/`--until` on
  down (5305) and `--width` on up (5306); down still gives back her unstarted
  cards (5306), its first say line carrying the reason and until; the help
  words combine both; `friendClass` and `cmdFriendHealth` both.
- verbs.go, coordinator.go, coordinator_test.go, CLI.md, SPEC-SPRINT.md: every
  friend verb of both (beat with its counts, down with reason/until, up with
  width, take, level, health, seat).

### bus-rename (5303)
- Every file the git bus had and 5303 deletes, which HEAD had edited: deleted
  (5303's intent; the git bus is gone).
- pkg/bus is the Redis bus: the nova-sprint friend-card sender (5304) and
  its tests import `pkg/bus` and say `nova-bus`, not `nova-bus2`; the
  friend Stub refusal says `nova-bus recv`.
- docs/CLI.md: the old nova-bus section dropped for the renamed one; the passive
  harness sentence names claude and the surveyed harnesses with no route.
- tla/CASES.tsv, tla/RUNS.tsv: HEAD's MCCairnStore rows kept, MCBusCursor rows
  dropped with the deleted module.
- catalog.go: 5303's pkg/bus entry and HEAD's internal/cardgen; no bus2.
- compared_examples.txt: nova-card (HEAD) and nova-bus (5303) examples.
- sleeps-skips allowlist: HEAD's (rows dev already deleted stay deleted).
- generality and serial-tests ledgers, pkgselect_test.go, tools/ci/sel_test.go:
  5303's, then the ledgers regenerated by the update run where the gate asks.
- docs/TESTS.md: migrate to 28.

### release-unreached-sentinel (5234)
- verbs.go release: 5234's `--reason` text, HEAD's `answersWords`.
- store/steps.go ReleaseStep: HEAD's `Answers` with 5234's fleet table load and
  extras.

## Fixes after the merges (the gate's merge reds)

- pkg/config/migrations: the bus-rename branch's `0027_fleet_bus.sql` is
  `0029_fleet_bus.sql`, after dev's `0027_row_name_checks.sql` and the
  claude-harness `0028_heavy_tier_route_harness.sql` (two migrations shared
  version 27); docs/TESTS.md migrates to 29.
- cmd/nova-sprint tests of other PRs that add one card (one id, or `--count 1`
  on one stream) say `--one`, which rework-bound's batch rule requires.
- pkg/pkgselect and tools/ci/sel_test.go: the expected legs are the newest
  promotion's one-leg-per-package Functional with the heavy package bus-rename
  names (nova-swarm).
- The generality and other counted ledgers are regenerated by the update run
  (NOVA_CI_UPDATE=1) where the merged tree only shrank them.

### origin/dev 06f7b5ef3 merged into the integration (head f3ae87421)
- cmd/nova-swarm/native.go: dev decomposed nativeRun into phase functions (22ff78bd7,
  093425343). Resolved by taking dev's file and porting every integration change since
  the merge base onto the phases: the headless harnesses (cfg.headless, the resolved
  binary, the private home in initRunState, no live sampler, the capture-read usage
  row, the headless argv, providerEnd by kind, the program root read, nativeNetAllow),
  the whole sdk on PATH (BenchPath, nativeChildEnv takes toolPath; nativePrepared
  carries toolPath beside goBin, which the gate runner keeps), and stoppedWhy set in
  watch. The two change sets were compared line by line: they differ only by the
  phase receivers (p., s., st.).
- cmd/nova-sprint/verbhelp.go: dev's `reader retire` effect (reading reads taken back)
  and the integration's `promoted` both.
- docs/SPEC-SPRINT.md: dev's `reader retire` row; the integration's `stream set` and
  `set` rows (the heavy tier).
- Merge reds the gate found: dev's nova-bus READ rating names four git-bus files the
  integration removed (allowlisted as records of their day, as the others are); and
  pkg/gocache/gocache.go named a machine in a comment (PR 5318), dropped.

### origin/dev 06c8bb115 (PR 5321, promotion 2026-10-04-b) merged into the integration (head f45296afd)
- The git bus: the integration removed it (84d517dfb); dev's generality edits to its files
  (pkg/bus cursor.go, git.go, note.go, protocol.go, bus.go's ReceiptStampLayout comment)
  and the debt stream's new cover tests of it (cmd/nova-bus/draft_cover_test.go and seven
  pkg/bus/*_cover_test.go) test or edit code that no longer exists: removed, with the
  ledger internal/ci/testdata/generality/pkg/bus.txt. pkg/bus/bus.go is the
  integration's (the bus2 rename).
- docs/SPEC.md: the git bus sections stay removed; dev's other generality edits merged.
- docs/SPEC-SPRINT.md (grade and tier): the integration's rule (a brief's tier is the start;
  the grade decides no tier, as Snapshot.startTier is) and dev's gate's-wall paragraph both.
- internal/sprint/steps_work.go Add: the integration's weights and dev's gate walls both.
- cmd/nova-cairn/read_existing_test.go: dev's form of the same fix (reads add nothing to
  what the appends left).
- tools/ci/sel_test.go: dev's file (eight macOS legs on a pull request) with the
  integration's heavy package, cmd/nova-swarm, for cmd/nova-bus.
- AGENTS.md and docs/STANDARD.md regenerated (make map); the counted ledgers
  (generality, generality-text, fixtures, named paths) unioned, then regenerated by the
  update run (NOVA_CI_UPDATE=1).
