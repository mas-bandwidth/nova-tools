# Nova Tools roadmap

## nova-tools: road to v0.16.0

nova-tools is the set of 22 command-line tools the friends work with: the bus they talk on, the swarm that runs cards, the merge lane that lands pull requests, the roadmap and work engine, and the rest. v0.16.0 is the next release of that set. Nothing is tagged and nothing is published; this section is what stands between today and a release.

Glenn's rule for it, verbatim: *"I don't want to make a release until everything is fixed in tools. Fix first. Test again. Fix again. Then release."* And: *"done means everything is tested and verified working."* So a row here goes green on a landed fix with a test, never on a green check alone.

State read 2026-09-19 13:24Z at `dev` [`4c793b55`](https://github.com/mas-bandwidth/nova-tools/commit/4c793b55a30160e5fe1ed45e25928f2c85dcfe5c). Issue and pull-request states are read live from GitHub each time this page is refreshed.

### Release blockers

A blocker is an open defect where the tool gives wrong output, loses data, or a spend or admission control is missing. A release-gate line cannot go green while one is open. Sixteen were filed; fifteen are still open, and ten of those now have a pull request.

| Issue | Area | What it is | State |
|---|---|---|---|
| [#1607](https://github.com/mas-bandwidth/nova-tools/issues/1607) | merge | two `nova-merge` batch refusal tests fail in `t.TempDir` cleanup on ubuntu-latest, and `certification-ok` fails behind them | open; [PR #1608](https://github.com/mas-bandwidth/nova-tools/pull/1608) `6379116f` green, mergeable, **unread** |
| [#1609](https://github.com/mas-bandwidth/nova-tools/issues/1609) | merge | `silenceRedis` writes a go-redis package global on every `react`: a data race under `-race`, one run in six | open; [PR #1628](https://github.com/mas-bandwidth/nova-tools/pull/1628) `d5de8d2b` green, mergeable, **unread** |
| [#1572](https://github.com/mas-bandwidth/nova-tools/issues/1572) | merge | `batch` and `land` admit a member carrying an unlifted HOLD; a held head reached `dev` through a green gate | open; [PR #1680](https://github.com/mas-bandwidth/nova-tools/pull/1680) `74825420` green, mergeable, **unread** |
| [#1545](https://github.com/mas-bandwidth/nova-tools/issues/1545) | swarm | no token, call or cost cap on the direct-provider route; `--deadline` is the only bound | open; rule landed as text ([#1566](https://github.com/mas-bandwidth/nova-tools/pull/1566) in [#1579](https://github.com/mas-bandwidth/nova-tools/pull/1579) at `06459eaa`); code PRs [#1615](https://github.com/mas-bandwidth/nova-tools/pull/1615) `d5dbe8b4` and [#1635](https://github.com/mas-bandwidth/nova-tools/pull/1635) `48c1e8d0` are **red** — `test (4/4 space)`, `test (4/4 studio)` and `ci-ok` fail on each |
| [#1546](https://github.com/mas-bandwidth/nova-tools/issues/1546) | swarm | `native` takes no bench slot lease although the spec refuses a launch without one | **fix landed**: [PR #1562](https://github.com/mas-bandwidth/nova-tools/pull/1562) in integration [#1601](https://github.com/mas-bandwidth/nova-tools/pull/1601) at `8e11a144`; the issue is still open and wants closing with that receipt |
| [#1465](https://github.com/mas-bandwidth/nova-tools/issues/1465) | swarm | `native` shares the Go caches but never puts the Go toolchain in the read set, so a Go card cannot run its own gate | open; [PR #1478](https://github.com/mas-bandwidth/nova-tools/pull/1478) `475d0957` conflicting |
| [#1463](https://github.com/mas-bandwidth/nova-tools/issues/1463) | swarm | the harness fence ignores `read_roots`, so a staged read is refused while the run still prints `NATIVE OK` | open; [PR #1636](https://github.com/mas-bandwidth/nova-tools/pull/1636) `35b8ce99` green, mergeable, **unread** |
| [#465](https://github.com/mas-bandwidth/nova-tools/issues/465) | swarm | the harness config is not carried into the job, so every configured local provider fails in under a second — this is what keeps the fleet on paid remote models | open; no PR |
| [#1518](https://github.com/mas-bandwidth/nova-tools/issues/1518) | bus | the inbox and wait walk is bounded at 500 commits, so a line further behind never sees new mail | open; [PR #1686](https://github.com/mas-bandwidth/nova-tools/pull/1686) `ebdc4f17` green, mergeable, **unread** |
| [#1540](https://github.com/mas-bandwidth/nova-tools/issues/1540) | bus | `close --before` fails midway on a receipt filename collision, leaving the cursor untouched and receipts unwritten | open; no PR |
| [#1512](https://github.com/mas-bandwidth/nova-tools/issues/1512) | pulse | `nova-pulse hygiene` still carries the three unsafe reaper rules the script dropped; this shape has eaten two certify trees | open; [PR #1679](https://github.com/mas-bandwidth/nova-tools/pull/1679) `28352f4b` green, mergeable, **unread** |
| [#1504](https://github.com/mas-bandwidth/nova-tools/issues/1504) | ci | the `AGENTS.md` class-rule contract never runs on the pull request that breaks it | open; [PR #1626](https://github.com/mas-bandwidth/nova-tools/pull/1626) `1803f298` green, mergeable, **unread** |
| [#1500](https://github.com/mas-bandwidth/nova-tools/issues/1500) | fleet | a `go.mod` bump lands and no bench can gate until the toolchain is installed by hand | open; no PR |
| [#1472](https://github.com/mas-bandwidth/nova-tools/issues/1472) | tokens | `nova-tokens fold` erases an unparseable row in the target day file at exit 0 | open; no PR |
| [#1466](https://github.com/mas-bandwidth/nova-tools/issues/1466) | check | the dogfood gate passes on an empty receipt set, so the release lane can go green on zero evidence | open; [PR #1558](https://github.com/mas-bandwidth/nova-tools/pull/1558) `12867eb5` green, mergeable, **unread** |
| [#1578](https://github.com/mas-bandwidth/nova-tools/issues/1578) | merge | the batch fixture's `git clone` could not copy its own objects on the studio runner | **closed** by [PR #1600](https://github.com/mas-bandwidth/nova-tools/pull/1600) at `3281b8ba` |

Eight of those pull requests are green, mergeable and read by nobody. That is the shortest path on this page: reading them closes eight blockers.

Three more were found with receipts and have since been filed: [#1631](https://github.com/mas-bandwidth/nova-tools/issues/1631) (`nova-post --channel fake` is documented six times and refused by the tool), the eight `docs/TESTS.md` transcripts no test executes, and [PR #1479](https://github.com/mas-bandwidth/nova-tools/pull/1479) not compiling on `dev` although git reads it mergeable.

### Dogfooding

Dogfooding is running each tool's own documented first-run transcript against the built binary and comparing line for line. It is the only check that the thing a person pastes from the docs actually works.

Candidate build `v0.16.0-dev.0f7ed3b5` is installed on the Studio, space and hulk; all 22 tools on each bench answer that version. The 44 cards were rerun against the installed build: **14 of 22 tools clean on two benches**, up from 10.

Not one of the remaining eight is a tool defect. All eight are `docs/TESTS.md` drift — the document is behind the tool. They are nova-decide, nova-merge, nova-post, nova-pulse, nova-review, nova-sandbox, nova-secrets and nova-self-talk. Four of the drifts are newly filed: [#1638](https://github.com/mas-bandwidth/nova-tools/issues/1638), [#1639](https://github.com/mas-bandwidth/nova-tools/issues/1639), [#1640](https://github.com/mas-bandwidth/nova-tools/issues/1640), [#1641](https://github.com/mas-bandwidth/nova-tools/issues/1641); [#1547](https://github.com/mas-bandwidth/nova-tools/issues/1547) is the umbrella for the class.

### Tool work: the seven enablers

Tool work is swarm cards doing mechanical repair on the tools themselves — fixing a red, executing a transcript, killing a mutant — under a gate that proves the card did what it claims. The enablers are what has to exist before a card can be trusted to touch code unread.

Spec: [PR #1637](https://github.com/mas-bandwidth/nova-tools/pull/1637), SPEC-TOOLWORK draft 3, open, spec only.

Twenty-four issues are cut and open, [#1646](https://github.com/mas-bandwidth/nova-tools/issues/1646) to [#1668](https://github.com/mas-bandwidth/nova-tools/issues/1668). T01–T07 and T17–T24 are marked for the builder and the friends; T09–T11, T15 and T16 are the swarm's once an area earns a readiness row. [#1572](https://github.com/mas-bandwidth/nova-tools/issues/1572), the HOLD-blind batch above, is the merge-lane enabler that must land before a swarm's member can be admitted on its own evidence.

### The decider

The decider is the piece that reads an untrusted blob — a bus note, a reviewer comment, a harvest result, a CI failure — and returns one typed answer with a confidence, so a window wakes only when something needs a person. Jev is one provider behind it; rules answer the constant cases and make no call at all.

Spec: [PR #1627](https://github.com/mas-bandwidth/nova-tools/pull/1627), the SPEC-DECIDE amendment, open, spec-ahead with no code.

Ten issues, all open: the interface and `nova-decide classify` ([#1616](https://github.com/mas-bandwidth/nova-tools/issues/1616)); the six typed readings — note triage ([#1617](https://github.com/mas-bandwidth/nova-tools/issues/1617)), harvest result ([#1619](https://github.com/mas-bandwidth/nova-tools/issues/1619)), backlog first pass ([#1620](https://github.com/mas-bandwidth/nova-tools/issues/1620)), verdict extraction ([#1618](https://github.com/mas-bandwidth/nova-tools/issues/1618)), CI-red classification ([#1621](https://github.com/mas-bandwidth/nova-tools/issues/1621)); and four housekeeping rows ([#1622](https://github.com/mas-bandwidth/nova-tools/issues/1622) to [#1625](https://github.com/mas-bandwidth/nova-tools/issues/1625)).

### The spend cap

[#1545](https://github.com/mas-bandwidth/nova-tools/issues/1545) is the one blocker that is about money rather than correctness. A `native` run on a paid provider has no token, call or cost ceiling; the receipt is a 328-second paid worker that nothing stopped. The rule is written and landed as text (SPEC-SWARM rule 13d, [PR #1566](https://github.com/mas-bandwidth/nova-tools/pull/1566) in integration [#1579](https://github.com/mas-bandwidth/nova-tools/pull/1579) at `06459eaa`). The code is in flight and the issue stays open until a test refuses a launch over the ceiling.

### What v0.16.0 needs

nova-work is in this release, and Glenn's bar is that it ships finished, dogfooded and tested. So the release wants all of:

- every blocker above closed with a landed fix and a test, not a rerun;
- 22 of 22 tools clean in the dogfood, which today means the `docs/TESTS.md` drift closed;
- nova-work's 231 criteria verified, which today stands at 155.

No date is promised here and no estimate is given. The numbers move when the evidence moves.


## nova-work

Help AI friends coordinate work without repeatedly rebuilding the plan in their context.

**Measured 2026-09-19 at `dev` [`4c793b55`](https://github.com/mas-bandwidth/nova-tools/commit/4c793b55a30160e5fe1ed45e25928f2c85dcfe5c).**
**155 of 231 acceptance criteria verified (67%). 23 of 63 features verified (37%).**
A criterion is ticked here only when a named test proves it and that test passed at this exact revision. The run is `cd lisp/nova-work && ./run-tests.sh` — `NOVA-WORK SLICE1 total=336 pass=336 fail=0`. Every ticked criterion names its tests under the feature.
The page said 0% until today. That was stale, not cautious: the work was there and the page had no way to show it. Each feature now carries `criteria verified / criteria total`, so a feature four-fifths done reads as four-fifths done instead of as nothing.

**Planning baseline: 10 epics, 52 features, 171 inventoried items (166 acceptance items and 5 open questions).** This is scope, not a delivery-date or effort estimate.
Current scope after the recorded moves and discoveries: 63 product features and 231 tickable acceptance items; the historical 171-item baseline count remains unchanged. The original inventory is retained at revision `248d85b`.

The current work register is below. Merged partial implementations and passing subset tests do not mark a full product feature verified. The recursive planning source records the original partial-support evidence for E01-F01, E01-F03, E01-F05, E03-F01 and E04-F01; it is an inventory, not a comprehensive implementation ledger.

Suite-isolation review [#1373 at `7506ad4a`](https://github.com/mas-bandwidth/nova-tools/pull/1373) remains held.

✅ = every one of the feature's criteria is verified against a named passing test at a recorded source revision. ❌ = any other state, including a feature whose criteria are nearly all ticked.
Completion is verified features divided by applicable features; it is not averaged subtask percentages or an estimate of time remaining. The criteria column is the finer reading of the same evidence, never a substitute for the feature mark.

`tools/roadmap-parity.sh` holds every number on this page to the data under it: each `N/M` cell is checked against that feature's own ticked criteria, the totals against `docs/roadmaps/nova-work.sexp`, and a ✅ with an unticked criterion fails the build.

This roadmap uses **epic → feature → sub-feature/acceptance item**, with no language axis.
[Recursive work data](docs/roadmaps/nova-work.sexp) retains stable IDs, dependencies and source references.
This baseline format is planning data, not a claim that nova-work import/export already exists.
Source citation key: `SPEC-WORK.md@f36b8585` names main-spec headings; `SPEC-WORK-PILOT.md@6e3413f` names resident-engine/roadmap headings; later companion refinements are pinned as `SPEC-WORK-PILOT.md@4e800fb` (eager W) and `SPEC-WORK-PILOT.md@bc4a4a4` (batch transport); `SPEC-WORK-VALIDATION.md@6e3413f` names acceptance suites. Additive deltas are `SPEC-WORK.md@7db3b95` (response correlation), proposed `SPEC-WORK.md@a0cfcf5` (resident 24-hour bound), proposed `spec/nova-work` PR #339 (the fleet), `SPEC-WORK.md@685b7c2` (efficiency policy, merged to `spec/nova-work` by PR #317 as `cf5dd8f5`), and PR #319 `SPEC-WORK.md@9488a19` (recursive project/stream grouping, merged to `spec/nova-work` as `9c120a3b`). The E11 source section *Delegation* resolves through the sexp's `:delegation-fold` key (`SPEC-WORK.md` at this fold's head, folded from `SPEC-DELEGATION.md`, PR #335). A source-section label below is resolved through its named file/revision key.

| Epic | Features | Criteria verified | Features verified |
|---|:---:|:---:|:---:|
| [Canonical work data and restricted representation](#e01) | 5 | 6/17 | 1/5 |
| [Resident coordinator session and fencing](#e02) | 7 | 14/23 | 1/7 |
| [Typed mutation and work lifecycle](#e03) | 5 | 12/16 | 2/5 |
| [Counting, indexes and bounded queries](#e04) | 6 | 17/19 | 4/6 |
| [Evidence, verification and acceptance proof](#e05) | 6 | 16/19 | 3/6 |
| [Persistence, closed history and recovery](#e06) | 5 | 15/16 | 4/5 |
| [Roadmap views and Fixed Tables pilot parity](#e07) | 6 | 10/20 | 1/6 |
| [Coordinator protocol, friends and execution records](#e08) | 5 | 23/32 | 1/5 |
| [Issue intake, migration and external boundaries](#e09) | 5 | 7/15 | 1/5 |
| [Diagnostics, measurement and release gates](#e10) | 7 | 16/27 | 2/7 |
| [Delegation](#e11) | 6 | 19/27 | 3/6 |
| **Total** | **63** | **155/231** | **23/63** |

## Scope movement

| Baseline features | Discovered after baseline | Verified | Reopened | Removed | Current features |
|:---:|:---:|:---:|:---:|:---:|:---:|
| 52 | 12 | 23 | 0 | 1 | 63 |

Preserve feature IDs and the baseline. The six discovered features below close source-backed gaps found in the PR #269 review; the response-correlation, ten efficiency-policy, three recursive-grouping, one fleet and two efficiency-lessons acceptance discoveries map to existing features without creating features. E10-F06 moved to the Now register outside this product denominator. The six E11 features and their 27 acceptance items are the DELEGATION contract folded into `SPEC-WORK.md` section *Delegation* on Glenn's word of 2026-09-15, spoken live in Rowan's window and carried by no comment id ("merge 'delegation' into work spec as a new epic"); they enter the denominator unverified; 231 is the sexp's 204 plus 27, and this file's earlier 201 and 203 were stale against the sexp and are reconciled here. Append discoveries with reasons; record decomposition and removal separately.
Do not count removal as completion. These counts track inventory movement, not estimated engineering effort.

## Open questions

- Common Lisp runtime packaging and supported platforms must be pinned before release.
- Category taxonomy and roadmap completion policy beyond `all-required-features` remain open.
- Exact verb and wire protocol spelling must be finalized in one schema before lock.
- Friend participation in swarms versus model-only pools requires explicit dispositions, including Freddy's.
- Absorption remains disabled pending independent reconciliation and authorization gates.

## Research and Development (R&D)

All active engineering and research items outside the v1 product feature denominator are organized in this explicit R&D register. These items track active prototypes, compiler and runtime hardening, protocol companions, decision packets, and adapter explorations across the team. Items in this register do not grant completion credit toward the v1 product denominator (63 features and 231 acceptance items). Current evidence snapshot at 2026-09-15T01:40:00Z (main `16a1d479`, spec `efb6dbe7`); PR #335 (V2-F05 coordinator notes, draft 1) merged at 45b44bdb and has no register row.

| ID | Work Item | Status | Owner | Next Gate | Source / Evidence |
|---|---|:---:|:---:|:---:|---|
| RD-01 | [PR #300](https://github.com/mas-bandwidth/nova-tools/pull/300): C/O transition kernel with write-maintained open count | `merged` | rowan | integrated | [PR #300](https://github.com/mas-bandwidth/nova-tools/pull/300) at d21d65f0; feature rows remain partial |
| RD-02 | [PR #310](https://github.com/mas-bandwidth/nova-tools/pull/310): PR264 profile migration to eight-member config preimage, execution and control schemas | `merged` | unassigned | parent-gate | [PR #310](https://github.com/mas-bandwidth/nova-tools/pull/310) at 95ceae8b into held PR #264; parent gates remain |
| RD-03 | [PR #311](https://github.com/mas-bandwidth/nova-tools/pull/311): Worker cards: practices and their evidence | `merged` | rowan | integrated | [PR #311](https://github.com/mas-bandwidth/nova-tools/pull/311) at 14ef63b1; feature rows remain partial |
| RD-04 | [PR #312](https://github.com/mas-bandwidth/nova-tools/pull/312): nova-bus reply transaction: draft a reply without a hand-built header | `merged` | rowan | integrated | [PR #312](https://github.com/mas-bandwidth/nova-tools/pull/312) at 20f8581; reviewed workshop adoption 0921c800 |
| RD-05 | [PR #314](https://github.com/mas-bandwidth/nova-tools/pull/314): nova-work no-effect events, dry-run and container-count specification | `merged` | rowan | integrated | [PR #314](https://github.com/mas-bandwidth/nova-tools/pull/314) at 4b99c1b4; feature rows remain partial |
| RD-06 | [PR #316](https://github.com/mas-bandwidth/nova-tools/pull/316): Receipt recovery ordering, attribution selection and claim locking | `merged` | unassigned | parent-gate | [PR #316](https://github.com/mas-bandwidth/nova-tools/pull/316) at 269e56d2 into held parent PR #240; parent gates remain |
| RD-07 | [PR #317](https://github.com/mas-bandwidth/nova-tools/pull/317): Efficiency policy, cache-aware costs and batching | `merged` | unassigned | implementation-gate | [PR #317](https://github.com/mas-bandwidth/nova-tools/pull/317) at cf5dd8f5 into spec/nova-work; no implementation credit |
| RD-08 | [PR #319](https://github.com/mas-bandwidth/nova-tools/pull/319): Recursive project and tool-stream grouping | `merged` | unassigned | implementation-gate | [PR #319](https://github.com/mas-bandwidth/nova-tools/pull/319) at 9c120a3b into spec/nova-work; no implementation credit |
| RD-09 | [PR #320](https://github.com/mas-bandwidth/nova-tools/pull/320): Refuse forbidden Lisp reader tokens before interning | `merged` | unassigned | full-verification | [PR #320](https://github.com/mas-bandwidth/nova-tools/pull/320) at edb9f7f1 into main; merged subset |
| RD-10 | [PR #322](https://github.com/mas-bandwidth/nova-tools/pull/322): Static required-member container cascade | `merged` | unassigned | resident-cow-gate | [PR #322](https://github.com/mas-bandwidth/nova-tools/pull/322) at b97e46c1 into main; merged subset |
| RD-11 | [PR #323](https://github.com/mas-bandwidth/nova-tools/pull/323): Finite records decision packet and Rule 29 | `merged` | unassigned | validator-slice | [PR #323](https://github.com/mas-bandwidth/nova-tools/pull/323) at ece6453a into main; whole-package validator tracked under RD-22 |
| RD-12 | [PR #325](https://github.com/mas-bandwidth/nova-tools/pull/325): Swarm migration-status and re-reservation corrections | `merged` | unassigned | runtime-gate | [PR #325](https://github.com/mas-bandwidth/nova-tools/pull/325) at e750e3a3 into held PR #264; profiles not enabled |
| RD-13 | [PR #326](https://github.com/mas-bandwidth/nova-tools/pull/326): propose optional native swarm batch admission receipts | `merged` | stella | encoding-crash-gates | [PR #326](https://github.com/mas-bandwidth/nova-tools/pull/326) at 9e78fa48 into main (2026-09-15T01:26:03Z); Emma/Rowan cleared; encoding and crash gates remain |
| RD-14 | [PR #330](https://github.com/mas-bandwidth/nova-tools/pull/330): Previous roadmap refresh through 21:57Z | `merged` | unassigned | superseded | [PR #330](https://github.com/mas-bandwidth/nova-tools/pull/330) at 3d6395c4 into main |
| RD-15 | [PR #331](https://github.com/mas-bandwidth/nova-tools/pull/331): start and restart tasks through kernel submit path | `merged` | stella | integrated | [PR #331](https://github.com/mas-bandwidth/nova-tools/pull/331) at f9a34a59 into main (2026-09-15T01:26:20Z); 37/37 tests pass; Freddy APPROVE, no findings, at a918fa8f (card 41, [comment 5673241190](https://github.com/mas-bandwidth/nova-tools/pull/331#issuecomment-5673241190)) |
| RD-16 | [PR #332](https://github.com/mas-bandwidth/nova-tools/pull/332): batch admission wire and recovery companion specification | `merged` | emma | implementation-gate | [PR #332](https://github.com/mas-bandwidth/nova-tools/pull/332) closed unmerged 2026-09-15T01:26:05Z when its base branch (PR #326) merged and was deleted; continued as [PR #342](https://github.com/mas-bandwidth/nova-tools/pull/342) head 48368158 merged as 861745e8 into main (2026-09-15T01:31:44Z); Stella scoped CLEAR 5672141578; Freddy APPROVE, no findings, at c6d6a558 (card 41, [comment 5673241357](https://github.com/mas-bandwidth/nova-tools/pull/332#issuecomment-5673241357)); proposal only, no implementation credit |
| RD-17 | [PR #333](https://github.com/mas-bandwidth/nova-tools/pull/333): bounded durable journal persistence and replay adapter | `merged` | emma | integrated | [PR #333](https://github.com/mas-bandwidth/nova-tools/pull/333) head 6672cc9a merged as 99ca0afc into main (2026-09-15T01:32:58Z); 47/47 tests pass; Stella CLEAR for the fixture repair 5673066268; Freddy APPROVE at 6672cc9a (card 43b, [comment 5673307161](https://github.com/mas-bandwidth/nova-tools/pull/333#issuecomment-5673307161)) |
| RD-18 | [PR #334](https://github.com/mas-bandwidth/nova-tools/pull/334): Previous roadmap refresh through 23:00:18Z | `merged` | unassigned | superseded | [PR #334](https://github.com/mas-bandwidth/nova-tools/pull/334) at 36efe4a9 into main |
| RD-19 | [PR #231](https://github.com/mas-bandwidth/nova-tools/pull/231): nova-work spec lock (spec/nova-work merged to main) | `merged` | rowan | integrated | [PR #231](https://github.com/mas-bandwidth/nova-tools/pull/231) merged spec/nova-work into main at ab11a10 (2026-09-15T02:00:00Z); spec locked; implementation slices proceed |
| RD-20 | [Issue #185](https://github.com/mas-bandwidth/nova-tools/issues/185): token ledger operational comparison, triage, and accounting | `measured` | unassigned | integrated | paired trial 2026-09-15: batched arm 1 coordinator turn vs 4, intake tokens 52 vs 450, spend equal; n=2, one model, one bench (issue #185, closed) |
| RD-21 | [Issue #336](https://github.com/mas-bandwidth/nova-tools/issues/336): evaluate Herdr as an agent runtime adapter | `parked` | unassigned | closed-not-planned | [Issue #336](https://github.com/mas-bandwidth/nova-tools/issues/336): evaluation parked; closed not planned |
| RD-22 | [PR #338](https://github.com/mas-bandwidth/nova-tools/pull/338): Slice C: whole-package candidate and installed validator | `merged` | emma | integrated | [PR #338](https://github.com/mas-bandwidth/nova-tools/pull/338) head d90bae5c merged as c3586c51 into main (2026-09-15T01:32:45Z); Rowan delta CLEAR 5673191508; Freddy APPROVE at d90bae5c (card 42, [comment 5673300420](https://github.com/mas-bandwidth/nova-tools/pull/338#issuecomment-5673300420)) |
| RD-23 | E10-F06: Operational adoption and continuous efficiency review | `open` | rowan | owner-assignment | Feature defined in the original inventory at [248d85b](https://github.com/mas-bandwidth/nova-tools/commit/248d85b320923803a82f1f643e303bbf42d2c74a), moved out of the product denominator to the Now register; [PR #337](https://github.com/mas-bandwidth/nova-tools/pull/337) at 368bcbe4 deleted the Now register; re-homed here; Rowan assigned owner |
| RD-24 | [PR #339](https://github.com/mas-bandwidth/nova-tools/pull/339): the fleet, a static description of the machines in the nova-work config | `merged` | unassigned | implementation-gate | [PR #339](https://github.com/mas-bandwidth/nova-tools/pull/339) head bfec9318 merged as efb6dbe7 into spec/nova-work (2026-09-15T01:34:48Z); Emma independent eye APPROVE/CLEAR at 40a691a ([comment 5673227670](https://github.com/mas-bandwidth/nova-tools/pull/339#issuecomment-5673227670)); Freddy APPROVE at 40a691a (card 44, [comment 5673300551](https://github.com/mas-bandwidth/nova-tools/pull/339#issuecomment-5673300551)); no implementation credit |
| RD-25 | [PR #340](https://github.com/mas-bandwidth/nova-tools/pull/340): fold the current goal into SPEC-WORK (goal set/show/update, six replays) | `merged` | unassigned | implementation-gate | [PR #340](https://github.com/mas-bandwidth/nova-tools/pull/340) head 261f18cd merged as 2e306395 into spec/nova-work (2026-09-15T01:32:11Z); Emma independent eye APPROVE/CLEAR at 261f18cd ([comment 5673231987](https://github.com/mas-bandwidth/nova-tools/pull/340#issuecomment-5673231987)); Stella scoped fold CLEAR 5673267608; Freddy APPROVE at 261f18cd (card 44, [comment 5673300675](https://github.com/mas-bandwidth/nova-tools/pull/340#issuecomment-5673300675)); no implementation credit |
| RD-26 | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780): MCP front vs thin client protocol comparison | `proposed` | unassigned | proposal-intake | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780) (ADAPT 1 / RD-26); evaluate token cost of MCP verb exposition vs CLI thin client across 10 sessions; falsifier: per-session schema injection exceeds bytes saved over CLI, or refusal loses reason |
| RD-27 | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780): escalation with a recorded reason | `proposed` | unassigned | proposal-intake | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780) (ADAPT 2 / RD-27); 20 frozen cards on cheap model with :escalate verb vs strong model shadow run; falsifier: missed plus spurious escalations exceed 25%, or escalated set costs more than shadow run |
| RD-28 | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780): fan-out width before synthesis eats the saving | `proposed` | unassigned | proposal-intake | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780) (ADAPT 2 / RD-28); objective tested at widths 1, 3, 6, 10 with coordinator synthesis tokens allocated separately; falsifier: total tokens per accepted unit fall monotonically to width 10 |
| RD-29 | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780): a bounded sideways channel between sibling cards in flight | `proposed` | unassigned | experiment | [ideas#780](https://github.com/mas-bandwidth/ideas/issues/780) (crumb dual-ring addendum / RD-29); question: does one bounded note per sibling per attempt reduce wasted attempts; falsifier: no fewer wasted attempts, or coordinator tokens rise more than the saving |
| RD-30 | Fleet allocation: machines as CONFIG, equipment never completes (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`machine-is-config-and-never-a-work-tree-node`); depends on W1 |
| RD-31 | Fleet allocation: ACTIVE allocation binds machine, slot and generation (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`allocation-binds-machine-slot-generation`); depends on W1 |
| RD-32 | Fleet allocation: `take` is atomic and idempotent (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`allocation-take-is-atomic-and-idempotent`); depends on W1 |
| RD-33 | Fleet allocation: expiry marks an allocation suspect (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`expiry-marks-suspect-reuse-needs-fencing`); depends on W1 |
| RD-34 | Fleet allocation: probes record observed ACTIVE and never CONFIG (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`probe-records-observed-active-and-touches-no-config`); depends on W1 |
| RD-35 | Fleet allocation: one allocator per machine (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md fleet allocation section (`one-allocator-per-machine-aliases-share-nested-conserve`); depends on W1 |
| RD-36 | Prompt profile as CONFIG, keyed by manager model (#500) | `open` | unassigned | implementation-gate | SPEC-WORK.md prompt profile section (`prompt-profile-expired-shows-on-the-status-line`) |

## Feature inventory

<a id="e01"></a>

### Canonical work data and restricted representation

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E01-F01 — Restricted Lisp reader and safe syntax | 3/3 | ✅ |
| E01-F02 — Uniform read bounds and schema validation | 0/3 | ❌ |
| E01-F03 — Stable node identity and canonical containment | 1/4 | ❌ |
| E01-F04 — Typed work kinds and acceptance schema | 0/4 | ❌ |
| E01-F05 — Canonical encoding and semantic round trip | 2/3 | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E01-F01 — Restricted Lisp reader and safe syntax**

Prerequisites: none.

- [x] Accept only lists, keywords, strings, integers and comments
- [x] Reject reader/evaluation macros with byte-offset diagnostics
- [x] Never evaluate imported data

Source sections: The data; Hostile data and limits.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): tests/acceptance/slice-01-reader.lisp: forbidden-token-boundary-before-interning, line-comments-accepted, comment-inside-form, comment-text-is-text, quote-in-comment-is-text, semicolon-in-string-is-literal, reader-eof-sentinel-is-not-payload, malformed-trailing-unclosed-form, malformed-trailing-unclosed-list, dispatch-byte-offset-utf8, eof-byte-offset-utf8, trailing-byte-offset-utf8, unterminated-string-start-byte, forbidden-after-unicode-comment, utf8-byte-offsets; hostile-data.

**E01-F02 — Uniform read bounds and schema validation**

Prerequisites: E01-F01.

- [ ] Require max-bytes, max-depth and max-nodes on every file read
- [ ] Apply session bounds to snapshots, archives, journals, caches and replay bundles
- [ ] Preserve unknown keys and refuse unknown node types

Source sections: The data; Hostile data and limits.

**E01-F03 — Stable node identity and canonical containment**

Prerequisites: E01-F02.

- [ ] Model one stable ID per node and one owning containment parent
- [ ] Keep containment as a forest and references as a separate graph
- [x] Preserve IDs through rename, close, reopen and reparent
- [ ] Allow omitted, repeated and recursively nested work-set grouping layers without a prescribed depth

Source sections: The data; Engine and representation; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): indexes-and-counters; reconstruction-after-close-and-revive; settle-keeps-id-and-evidence.

**E01-F04 — Typed work kinds and acceptance schema**

Prerequisites: E01-F03.

- [ ] Represent work-set, feature, roadmap, task, lease and event kinds
- [ ] Represent leaf tasks separately from parent tasks and attempts
- [ ] Validate acceptance kind, subject, predicate and required flag
- [ ] Represent project and stream groupings as work-set categories without inferring kind from title or position

Source sections: The data; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

**E01-F05 — Canonical encoding and semantic round trip**

Prerequisites: E01-F01, E01-F04.

- [x] Produce deterministic bytes with explicit absent/empty and Unicode semantics
- [x] Preserve large integers, timestamps, escaping and multiline text
- [ ] Compare exports with an independent semantic comparator

Source sections: Format determinism; Full data round trip.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): supported-subset-format-determinism; absent-empty-and-null-are-three-spellings; wire-integers-are-strings.

</details>

<a id="e02"></a>

### Resident coordinator session and fencing

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E02-F01 — Resident O/COW session lifecycle | 2/3 | ❌ |
| E02-F02 — Single coordinator ownership record | 3/4 | ❌ |
| E02-F03 — Journal and endpoint locking | 1/3 | ❌ |
| E02-F04 — Lease expiry, reconfirmation and self-fencing | 1/3 | ❌ |
| E02-F05 — Handoff, stop and successor recovery | 3/3 | ✅ |
| E02-F06 — Runtime packaging and first-run installation | 1/3 | ❌ |
| E02-F07 — Execution leases and worker ownership | 3/4 | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E02-F01 — Resident O/COW session lifecycle**

Prerequisites: E01-F03.

- [ ] Load one resident O with structure and event log
- [x] Keep C as closed history and W as a predicate inside O
- [x] Make session start the launcher and expose status

Source sections: The execution model; The root is COW; Engine and representation.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): working-is-a-view; cow-root-partition; session-server-daemon-and-session-start; session-status.

**E02-F02 — Single coordinator ownership record**

Prerequisites: E02-F01.

- [x] Persist owner name, generation, random token, stamp and expiry
- [x] Allow take and resume with generation rules; successor handoff belongs to E02-F05
- [x] Refuse competing live owners and name holder details
- [ ] Verify generation fencing under partitions and clock skew; reject unsafe takeover rather than relying on PID locks alone

Source sections: The execution model; One coordinator, one live reader/writer.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): ownership-record-round-trips; resume-needs-the-journal-lock; resume-refuses-a-copied-journal.

**E02-F03 — Journal and endpoint locking**

Prerequisites: E02-F02.

- [x] Lock canonical journal path for the full process lifetime
- [ ] Lock filesystem socket or use Windows first-pipe-instance semantics
- [ ] Never unlink another live process lock or endpoint

Source sections: The execution model.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): resume-needs-the-journal-lock.

**E02-F04 — Lease expiry, reconfirmation and self-fencing**

Prerequisites: E02-F02, E02-F03.

- [ ] Check base tip and owner token before every write
- [ ] Admit each request against until and fence on expiry or divergence
- [x] Permit only status/export while fenced and preserve journal work

Source sections: The execution model; Single writer.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): fenced-export-can-finish; session-status.

**E02-F05 — Handoff, stop and successor recovery**

Prerequisites: E02-F04, E06-F03.

- [x] Clip and publish successor or released owner record atomically
- [x] Let named successor take next generation without expiry wait
- [x] Recover fenced accepted events through export and replay

Source sections: The execution model; Checkpoints, undo and redo.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): session-stop; handoff-successor-takes-next-generation; session-export-writes-the-request-bundle; session-replay-bundle-intake.

**E02-F06 — Runtime packaging and first-run installation**

Prerequisites: E02-F01, E08-F02.

- [ ] Pin Common Lisp implementation, Go client and supported OS/runtime combinations
- [x] Version client/engine/protocol explicitly and refuse unsupported combinations
- [ ] Provide reproducible build/install and small startup/status/shutdown smoke tests

Source sections: Engine and representation; The execution model.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): protocol-version-negotiated-or-refused.

**E02-F07 — Execution leases and worker ownership**

Prerequisites: E02-F04.

- [x] Keep execution leases distinct from the coordinator ownership lease, with at most one live lease per node
- [ ] Support take, heartbeat, holder-only release, handed release, one extension and explicit escalation with deadline/default fields
- [x] Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now
- [x] Preserve lease state through reassignment, correction, pause, stop and recovery without counting an attempt twice

Source sections: The data; The execution model; Operational lessons the pilot must exercise.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): no-shadow-lease-across-holders; accepted-creates-one-lease-or-binds; branch-and-window-required; delegated-to-a-sleeper-then-recovered; move-keeps-the-lease; a-retry-does-not-overwrite-its-attempt; correct-is-a-linked-segment.

</details>

<a id="e03"></a>

### Typed mutation and work lifecycle

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E03-F01 — Atomic request envelopes and idempotency | 3/3 | ✅ |
| E03-F02 — Structure verbs and decomposition | 2/3 | ❌ |
| E03-F03 — Scope, baseline and dependency changes | 1/3 | ❌ |
| E03-F04 — State correction and completion transitions | 3/4 | ❌ |
| E03-F05 — Reversible undo and redo plans | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E03-F01 — Atomic request envelopes and idempotency**

Prerequisites: E01-F05, E02-F04.

- [x] Assign stable request IDs and durable event envelopes
- [x] Validate multi-node changes all-or-none
- [x] Refuse repeated IDs or changed payload digests

Source sections: The data; The verbs; Retry/protocol.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): journal-records-before-it-applies; two-event-candidate-is-all-or-none; durable-journal-multi-event-envelope-never-partly-publishes; durable-journal-changed-payload-refuses; dedup-refuses-past-its-bound.

**E03-F02 — Structure verbs and decomposition**

Prerequisites: E03-F01.

- [ ] Support add, metadata edit, move/reparent, decompose, link/unlink and retire
- [x] Record structural event fields in verb-defined order
- [x] Preserve decomposition as scope change rather than completion

Source sections: The data; The verbs; Inventory before implementation.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): new-verbs-have-a-kind-and-a-field-order; add-field-order-is-complete; every-field-has-an-owning-verb; inventory-expansion-and-contraction.

**E03-F03 — Scope, baseline and dependency changes**

Prerequisites: E03-F02.

- [ ] Support baseline, discovery, require, dependency add/remove and prioritize
- [ ] Record author, reason, scope revision and exact member delta
- [x] Keep shared prerequisites singly owned and referenced

Source sections: Counting; The data; Operational lessons the pilot must exercise.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): shared-prerequisite-owned-once.

**E03-F04 — State correction and completion transitions**

Prerequisites: E03-F03, E05-F01.

- [ ] Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded
- [x] Bump task generation on correction and invalidate older evidence
- [x] Keep reopen, pause and stop as durable events
- [x] Settle and reopen required-member ancestors at every grouping depth while empty required sets remain incomplete

Source sections: The data; The validator; Required coordinator operations; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): correct-is-a-linked-segment; regression-opens-repair-work; reopen-revives; stop-is-a-hold-not-a-cancel; containers-settle-with-their-members; completed-view-mutation.

**E03-F05 — Reversible undo and redo plans**

Prerequisites: E03-F04, E06-F03.

- [x] Create typed compensating envelopes from preimages
- [x] Check expected revisions, descendants and dependencies before apply
- [x] Preserve original history and refuse irreversible or uncertain effects

Source sections: Checkpoints, undo and redo; Undo/redo.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): undo-appends-and-preserves; redo-refuses-a-stale-plan; edit-undo-preserves-later-work; undo-refuses-an-external-effect; undo-names-its-reversible-set.

</details>

<a id="e04"></a>

### Counting, indexes and bounded queries

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E04-F01 — Incremental open counters and grains | 2/3 | ❌ |
| E04-F02 — Roadmap percent and cell progress | 3/3 | ✅ |
| E04-F03 — Scope movement and branch counts | 2/3 | ❌ |
| E04-F04 — Stable resident indexes and bounded access | 4/4 | ✅ |
| E04-F05 — Historical indexed queries and coverage honesty | 3/3 | ✅ |
| E04-F06 — As-of reconstruction over O | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E04-F01 — Incremental open counters and grains**

Prerequisites: E03-F01, E01-F03.

- [x] Maintain root open-item counters in mutation envelopes
- [x] Count canonical IDs once and exclude references, attempts and history
- [ ] Label features, leaves and member grains with revision

Source sections: Counting; Counts are read directly.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): open-count-is-read-not-computed; cow-root-partition; indexes-and-counters; findings-across-c-and-o.

**E04-F02 — Roadmap percent and cell progress**

Prerequisites: E04-F01, E07-F01.

- [x] Compute green feature cells over applicable rows per axis member
- [x] Print green, applicable, rows and baseline-rows
- [x] Print completed required leaves as k/n and never average percentages

Source sections: Counting; A cell is a reference, not another state store.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): percent-axis-on-a-matrix.

**E04-F03 — Scope movement and branch counts**

Prerequisites: E03-F03, E04-F01.

- [ ] Keep baseline, discovery, deferred, cancelled and superseded distinctions
- [x] Report open plus closed totals without double membership
- [x] Report closed-in, settles-in and revives-in for windows

Source sections: Counting.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): cow-root-partition; revive-appends-and-counts-latest; closed-rows-carry-revived-and-settles; activity-and-state-are-two-counts.

**E04-F04 — Stable resident indexes and bounded access**

Prerequisites: E04-F01, E01-F03.

- [x] Build and incrementally update indexes for IDs, containment, dependencies and repositories
- [x] Provide bounded focus, subtree, category, ready and blocker queries
- [x] Avoid materialized transitive descendant sets and unbounded scans
- [x] Maintain eager W membership, its counter and per-friend reverse indexes in the same mutation envelope; normal reads never rebuild W lazily

Source sections: The data; Counting; Queries — the contract; W is an eagerly maintained working index.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): indexes-and-counters; reverse-dependency-index-is-bounded; ready-names-the-blocker-and-the-resolver; ready-needs-every-dependency-settled; materialized-working-set; working-is-a-view; history-grows-startup-does-not.

**E04-F05 — Historical indexed queries and coverage honesty**

Prerequisites: E04-F04, E06-F02.

- [x] Resolve settled dependencies and closed rows through indexed lookup
- [x] Distinguish absent days, missing segments and unavailable as-of partitions
- [x] Return gap, provenance or refusal rather than fabricate state

Source sections: The execution model — retention; Queries — the contract; Old history.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): closed-paged-without-full-load; absent-day-is-not-a-gap; missing-segment-is-a-gap; as-of-refuses-unavailable-partition; closed-row-with-archive-absent; rule-2-unavailable-is-not-green.

**E04-F06 — As-of reconstruction over O**

Prerequisites: E04-F04, E03-F01.

- [x] Answer --at <revision> over O by replaying retained events in memory
- [x] Include the replay in revision-labelled counts and refuse before the retention boundary
- [x] Keep historical O answers separate from indexed C as-of queries and report unavailable history honestly

Source sections: Queries — the contract; The execution model — retention; Counting.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): as-of-reconstructs-settle-revive-settle; cursor-pinned-across-a-new-settle; default-window-opens-two-days; rule-2-unavailable-is-not-green.

</details>

<a id="e05"></a>

### Evidence, verification and acceptance proof

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E05-F01 — Evidence events and generation guards | 2/3 | ❌ |
| E05-F02 — Verification and stale evidence reporting | 3/3 | ✅ |
| E05-F03 — Reviews, findings and attestations | 4/4 | ✅ |
| E05-F04 — Dependency and release gates | 2/3 | ❌ |
| E05-F05 — Independent proof and mutation regression checks | 2/3 | ❌ |
| E05-F06 — Evidence resolvers and verification cache | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E05-F01 — Evidence events and generation guards**

Prerequisites: E01-F04, E03-F01.

- [x] Bind pointer, criterion, against revision and generation to evidence
- [ ] Require matching criterion kind, exact subject and predicate
- [x] Make correction generation invalidate prior qualification

Source sections: The data; The validator.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): verify-job-criterion-reads-the-revision; verify-resolver-identity-is-the-command; a-removed-or-corrected-need-is-unmet; regression-opens-repair-work.

**E05-F02 — Verification and stale evidence reporting**

Prerequisites: E05-F01.

- [x] Verify every evidence event, including those not named by done
- [x] Report stale, freshest and done-unverified facts
- [x] Keep test, job, merged and attested proof distinct

Source sections: The validator; Evidence and the imported starting point.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): a-done-need-is-unverified-without-complete-proof; verify-stale-evidence-needs-no-fetch; stale-evidence-does-not-unmeet-a-need; a-done-need-on-unverified-evidence-admits-nothing; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; merged-is-not-distributed.

**E05-F03 — Reviews, findings and attestations**

Prerequisites: E05-F01.

- [x] Record exact-revision reviewer findings and author dispositions
- [x] Support attested criteria with reviewer identity and result
- [x] Preserve disagreement, unknowns and repair cycles
- [x] reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers

Source sections: Operational lessons the pilot must exercise; The data; Efficiency policy (SPEC-WORK.md@685b7c2).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): review-cycles-stay-visible; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; reconcile-preserves-contradiction; regression-opens-repair-work; reuse-only-valid-review.

**E05-F04 — Dependency and release gates**

Prerequisites: E05-F02, E04-F04.

- [x] Require prerequisite dependencies and acceptance before green
- [x] Keep merged fix, verified behavior and published distribution separate
- [ ] Resolve release version through the release task reference

Source sections: The validator; A cell is a reference, not another state store.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): parent-green-needs-dependencies; merged-is-not-distributed.

**E05-F05 — Independent proof and mutation regression checks**

Prerequisites: E05-F02.

- [x] Use independent oracle/comparator and deliberately broken assertions
- [ ] Retain fixtures, revisions, fault points and expected/actual reconciliation
- [x] Reject green claims unsupported by criterion-level proof

Source sections: Independent oracle and retained evidence; Roadmap proof.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): a-broken-assertion-must-fail; roadmap-proof.

**E05-F06 — Evidence resolvers and verification cache**

Prerequisites: E05-F01.

- [x] Resolve configured pointer schemes by direct executable invocation with fact/stamp arguments and no shell
- [x] Enforce max-fetch, fetch-timeout and offline behavior; an absent resolver is unreachable
- [x] Cache by pointer, subject and resolver identity, pin resolver identity on snapshot reads, and preserve unknown results

Source sections: The validator; The resident session; Required test suites.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): verify-resolver-identity-is-the-command; verify-offline-refuses-a-fetch-budget; verify-negatives-and-unreachable; verify-cache-holds-raw-resolutions; verify-keeps-cached-evidence-rows.

</details>

<a id="e06"></a>

### Persistence, closed history and recovery

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E06-F01 — Durable journal and local checkpoints | 4/4 | ✅ |
| E06-F02 — Bounded C partitions and historical indexes | 3/3 | ✅ |
| E06-F03 — Clip, commit and recovery replay | 3/3 | ✅ |
| E06-F04 — Isolated restore and compare | 2/3 | ❌ |
| E06-F05 — Lossless export and schema migration | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E06-F01 — Durable journal and local checkpoints**

Prerequisites: E01-F05, E03-F01.

- [x] Journal every accepted mutation before acknowledgment
- [x] Create validated snapshots with schema, revision, boundary and manifest
- [x] Expose local/shared revision, age, unshared work and failed backup
- [x] Keep prior verified snapshots and protect accepted journal history through retention/compaction

Source sections: Checkpoints, undo and redo; The resident session.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): journal-records-before-it-applies; crash-after-append-recovers-the-reply-once; savepoint-cut-never-splits-an-envelope; a-savepoint-is-not-a-shared-backup; savepoint-write-failure-keeps-the-previous; compaction-keeps-the-last-copy.

**E06-F02 — Bounded C partitions and historical indexes**

Prerequisites: E06-F01, E03-F04.

- [x] Partition closure events by their recorded UTC day in immutable bounded segments
- [x] Publish manifests, closed rows and dedup/closed index roots in bounded pages
- [x] Maintain rolling 24-hour default window with at most two day partitions; pending SPEC-WORK.md@a0cfcf5 correction adds noon/midnight/over-limit witnesses while explicit historical queries remain bounded

Source sections: The execution model — retention; The data; Old history; Resident-window correction (proposed SPEC-WORK.md@a0cfcf5).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): days-merge-by-revision-never-concatenate; busy-day-many-segments; one-revision-publishes-together; closed-paged-without-full-load; page-budget-is-not-max; default-window-opens-two-days.

**E06-F03 — Clip, commit and recovery replay**

Prerequisites: E06-F02, E02-F04.

- [x] Publish snapshot, segments, manifests and both index roots in one revision
- [x] Verify hashes and never expose a root without referenced files
- [x] Reload snapshot, indexes and journal overlays after crash

Source sections: The execution model — retention; Checkpoints, undo and redo.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): one-revision-publishes-together; index-replayed-after-crash; overlay-is-bounded-and-rebuilt.

**E06-F04 — Isolated restore and compare**

Prerequisites: E06-F03.

- [x] Restore read-only or isolated without ownership, dispatch or side effects
- [ ] Compare prior checkpoint to current state and report gaps
- [x] Keep originals and prior known-good checkpoints during replacement

Source sections: Checkpoints, undo and redo; Recovery.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): restore-is-isolated-and-dispatches-nothing; copied-journal-grants-nothing; state-load-is-isolated; savepoint-write-failure-keeps-the-previous.

**E06-F05 — Lossless export and schema migration**

Prerequisites: E06-F03, E01-F05.

- [x] Export selected full history beyond resident window with declared scope
- [x] Migrate supported old schemas semantically and refuse unsupported versions
- [x] Retain source originals, provenance and unresolved records

Source sections: Lossless migration and round-trip release gates; Schema evolution; Old history.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): old-history; schema-evolution; source-inventory; moving-source.

</details>

<a id="e07"></a>

### Roadmap views and Fixed Tables pilot parity

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E07-F01 — Typed roadmap axes and cell references | 5/5 | ✅ |
| E07-F02 — Completion-only projection renderer | 2/3 | ❌ |
| E07-F03 — Generated ROADMAP drift checks | 2/3 | ❌ |
| E07-F04 — Fixed Tables imported baseline inventory | 1/3 | ❌ |
| E07-F05 — Pilot retrospective and scope evolution | 0/3 | ❌ |
| E07-F06 — Private-node projection filtering | 0/3 | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E07-F01 — Typed roadmap axes and cell references**

Prerequisites: E01-F04, E03-F03.

- [x] Represent ordered axes and coordinate-to-node references
- [x] Refuse unknown axis members and duplicate coordinates
- [x] Treat missing cells and out-of-scope cells distinctly
- [x] Preserve selected scope and completion unit across recursive grouping layouts without mandatory axis or cell wrappers
- [x] Retain completed roadmap members and historical code/test evidence beyond the active 24-hour window

Source sections: The data; A cell is a reference, not another state store; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): roadmap-has-one-creator; render-view-over-a-stored-selection; cell-moves-no-required-set; move-updates-every-roadmap-scope; roadmap-outlives-its-work; roadmap-opened-after-the-window; axisless-history.

**E07-F02 — Completion-only projection renderer**

Prerequisites: E05-F02, E07-F01.

- [x] Render tick only for fully verified cells and cross otherwise
- [ ] Keep partial, missing and unknown state in S/check counts
- [x] Write only the marked roadmap region and preserve unrelated bytes

Source sections: Roadmap as a view; the Schema pilot; What the current prototype proves, and does not.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): roadmap-proof; a-broken-assertion-must-fail; chat-and-file-render-are-byte-identical; render-refuses-a-target-outside-its-roots.

**E07-F03 — Generated ROADMAP drift checks**

Prerequisites: E07-F02, E06-F03.

- [ ] Regenerate ROADMAP from primary O data
- [x] Make render --check fail on drift or ambiguous markers
- [x] Ensure chat/file projections use the same captured revision

Source sections: The failures it closes; Roadmap as a view; the Schema pilot; Roadmap proof.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): render-artifact-is-bounded; roadmap-proof; chat-and-file-render-are-byte-identical; move-updates-every-roadmap-scope.

**E07-F04 — Fixed Tables imported baseline inventory**

Prerequisites: E06-F05, E07-F01.

- [ ] Preserve source revision, audit IDs, reports and aliases
- [ ] Include ordinary delivered capabilities beside versioning rows
- [x] Keep source support separate from qualified acceptance and incomplete reconciliation visible

Source sections: Evidence and the imported starting point; Inventory before implementation.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): moving-source.

**E07-F05 — Pilot retrospective and scope evolution**

Prerequisites: E07-F04, E05-F05.

- [ ] Exercise completed cell, new discovery, dependency/handoff and changed focus
- [ ] Record failures, repairs, denominator movement and changed scope
- [ ] Feed findings into NEXT-TOOLS and production specs before implementation

Source sections: Retrospective required before production implementation; Operational lessons the pilot must exercise.

**E07-F06 — Private-node projection filtering**

Prerequisites: E07-F02.

- [ ] Omit private nodes and descendants from rendered output files
- [ ] When a public view reaches private work through a parent, print only the private count
- [ ] Preserve private state in O and validation while applying filtering only at projection boundaries

Source sections: The data; Output grammar; A cell is a reference, not another state store.

</details>

<a id="e08"></a>

### Coordinator protocol, friends and execution records

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E08-F01 — Versioned typed command schema and discovery | 0/4 | ❌ |
| E08-F02 — Client transport and asynchronous operations | 8/9 | ❌ |
| E08-F03 — Friends, CONFIG and ACTIVE indexes | 4/7 | ❌ |
| E08-F04 — Availability, offers and assignment reconciliation | 6/7 | ❌ |
| E08-F05 — Bounded config/pricing exchange and model suitability | 5/5 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E08-F01 — Versioned typed command schema and discovery**

Prerequisites: E03-F01.

- [ ] Use one schema for client validation, protocol, help and examples
- [ ] Provide bounded family/verb help and machine discovery with schema hash
- [ ] Refuse stale discovery and invalid suggested actions
- [ ] List next valid actions and missing prerequisites without granting authority or executing suggestions

Source sections: The verbs; Learn verbs without carrying the manual in context.

**E08-F02 — Client transport and asynchronous operations**

Prerequisites: E08-F01, E02-F04.

- [x] Support framed requests, status, wait, cancel and bounded backpressure
- [x] Handle disconnect, lost reply, deadlines and uncertain outcomes
- [x] Keep control operations responsive during import/export/clip
- [ ] Use bounded typed JSON over a local Unix socket, exact integer/time encoding and durable asynchronous operation IDs; reconcile cross-platform endpoint requirements before lock
- [x] Support bounded read bundles at one captured revision and lease-time watermark, with stable paginated snapshot identity
- [x] Support independent ordered batches with per-entry IDs/outcomes and explicit stop/continue semantics, without implying rollback
- [x] Support atomic mutation batches with all-or-none validated O/W, counter and reverse-index changes; reject oversized batches without silently splitting
- [x] pipeline-replies-are-correlated: correlate out-of-order or fragmented ordinary responses by request ID, retain a distinct durable operation ID, name independent not-attempted and atomic validation entry outcomes, and close on unknown, duplicate, absent or undecodable IDs; same-ID recovery uses E03-F01 durable idempotency
- [x] batch-with-bounds-and-urgency: coalesce independent results within byte, record and delay bounds; refuse unreferenced padding, bypass delay for urgent changes and preserve dependency and retry identity

Source sections: The verbs; Async operations; Retry/protocol; Batch-friendly transport and explicit atomicity; Response correlation (SPEC-WORK.md@7db3b95); Efficiency policy (SPEC-WORK.md@685b7c2).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): wire-is-length-prefixed-utf8-json; status-answers-while-io-runs; cancel-is-a-request-not-an-erasure; disconnect-is-not-a-rollback; lost-reply-then-reopen; durable-journal-append-plus-lost-reply-recovers-once; state-export-is-one-long-operation; clip-is-one-long-operation; batches-and-pipelines; materialized-working-set; pipeline-replies-are-correlated; batch-with-bounds-and-urgency.

**E08-F03 — Friends, CONFIG and ACTIVE indexes**

Prerequisites: E04-F04, E03-F04.

- [ ] Index every known friend, assignments and working task references
- [x] Separate stable capabilities/config from observed active executions
- [ ] Include coordinator identity and attribute model, bench, attempt and usage
- [x] Store agreed specialist roles per friend; keep model strengths/weaknesses in the shared model catalog
- [ ] Represent children, swarm capabilities, local runs and one-shots separately from friend identity; agreed concurrency limits apply
- [x] bounds-are-not-prompts: refuse automatic dispatch when an adapter cannot enforce the configured execution limit; distinguish wait deadline from observed stop or unresolved outcome
- [x] fleet-is-static-config: hold the fleet as `:machine` records in CONFIG with stable id, owner, connection-profile reference, roles, limits, permits, exclusions and dated declared facts, all instance data; list it and recommend members for a workload kind from declared facts, never a lease; refuse a record without owner or id, a held connection profile, a credential, an unknown owner or role, and a choice of a member for a workload it excludes

Source sections: Friends and assignments are resident indexes too; CONFIG and ACTIVE are different sections; Efficiency policy (SPEC-WORK.md@685b7c2); The fleet (spec/nova-work [PR #339](https://github.com/mas-bandwidth/nova-tools/pull/339)).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): fleet-is-static-config; probe-records-observed-active-and-touches-no-config; four-capability-groups-and-three-fields; roles-are-configured-not-inferred; reserved-role-is-not-spent-on-routine-work; bounds-are-not-prompts.

**E08-F04 — Availability, offers and assignment reconciliation**

Prerequisites: E08-F03, E02-F04.

- [x] Represent explicit rest, unavailable and unconfirmed contact with observation age
- [x] Distinguish offer, acknowledgment, ownership, lease and execution
- [x] Reconcile uncertain prior attempts before relaunch or reassignment
- [x] After the team-configured silence threshold use one bounded availability probe; nonresponse is unconfirmed capacity, never proof of exhausted credits
- [x] quiet-until-actionable: keep unchanged traffic mechanical, batch actionable deltas within bounds and let corrections, stops, lease loss and deadlines bypass delay
- [x] regression-and-recovery: suspend new automatic routing on a breached trial, retain uncertain live handles and use only eligible role-preserving fallback
- [ ] presence-and-recovery: derive one presence per friend from the newest of the four beat sources (bus cursor, wake probe, harness hook, manual), read asleep at 300 s and unacknowledged at 600 s, refuse assignment to an asleep or unknown friend, and recover only by a coordinator's recorded reassign that cites the reading and fences the prior lease

Source sections: Observed availability; Friends and assignments are resident indexes too; Efficiency policy (SPEC-WORK.md@685b7c2); Presence: who is awake and who is asleep.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): explicit-rest-is-not-pinged; silence-is-a-ping-not-a-verdict; four-facts-four-verbs; dispatch-ack-and-ownership-are-three; return-reconciles-before-dispatch; hold-survives-a-crash; quiet-until-actionable; regression-and-recovery.

**E08-F05 — Bounded config/pricing exchange and model suitability**

Prerequisites: E08-F03.

- [x] Exchange UNCHANGED manifests or validated deltas by hash/revision
- [x] Store model route, pricing, quota and provenance without secrets
- [x] Keep requested versus observed model and measured suitability separate
- [x] policy-round-trip-and-replay: preserve per-friend efficiency policy, trial manifests and execution references through validated intake, export/import, restart, undo and replay
- [x] packet-and-route-gates: refuse oversized or history-disallowed packets and ineligible/stale routes; retain a scoped reason for economical-route exceptions

Source sections: Efficient friend config exchange and token pricing; Model knowledge informs scheduling; Swarms, models and friend participation: decision required; Efficiency policy (SPEC-WORK.md@685b7c2).

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): unchanged-config-is-one-bounded-answer; an-invalid-delta-leaves-the-old-config; a-partial-manifest-is-refused; route-config-lists-key-by-path-never-value; no-credential-in-a-member; pricing-is-pinned-by-revision; requested-model-is-not-observed-model; policy-round-trip-and-replay; packet-and-route-gates.

</details>

<a id="e09"></a>

### Issue intake, migration and external boundaries

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E09-F01 — Non-destructive issue inventory and capture | 1/3 | ❌ |
| E09-F02 — Link mode and correspondence reconciliation | 1/3 | ❌ |
| E09-F03 — Lossless resumable initial migration | 2/3 | ❌ |
| E09-F04 — Explicit absorb operation and deletion gate | 0/3 | ❌ |
| E09-F05 — External adapter and side-effect safety | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E09-F01 — Non-destructive issue inventory and capture**

Prerequisites: E06-F05.

- [ ] Capture stable provider/repository/issue identity, revision and URL
- [ ] Preserve body, comments, labels, relationships, attachments and pagination
- [x] Keep inaccessible or unsupported fields explicit

Source sections: Public issue correspondence survives intake; Source inventory; Archive completeness.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): archive-completeness.

**E09-F02 — Link mode and correspondence reconciliation**

Prerequisites: E09-F01, E03-F01.

- [ ] Map one issue to many nodes and repeated updates without duplicates
- [x] Keep remote text as data separate from accepted plan and authority
- [ ] Track pending, confirmed and failed outbound actions with receipts

Source sections: Public issue correspondence survives intake.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): read-only-intake; hostile-data.

**E09-F03 — Lossless resumable initial migration**

Prerequisites: E09-F01, E06-F03.

- [x] Inventory authorized sources with capture manifest and disposition
- [ ] Import in batches with originals, mappings, deduplication and checkpoints
- [x] Reconcile counts/content and exercise interruption and source edits

Source sections: Initial migration: preserve first, reconcile, then choose absorption; Import replay; Moving source.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): source-inventory; moving-source.

**E09-F04 — Explicit absorb operation and deletion gate**

Prerequisites: E09-F03, E05-F05.

- [ ] Separate absorb from default link and require selected scope/authority
- [ ] Archive source identity, provenance and content before removal; append the actual deletion outcome receipt after the attempt
- [ ] Leave deletion pending on missing content, source change or uncertain network result

Source sections: Link versus absorb; Archive completeness; Lossless migration and round-trip release gates.

**E09-F05 — External adapter and side-effect safety**

Prerequisites: E09-F02.

- [x] Keep GitHub issue closure, publishing and source deletion separate from local completion
- [x] Do not claim Git/GitHub atomicity or invent authors
- [x] Preserve concurrent human changes and uncertain external outcomes

Source sections: Public issue correspondence survives intake; Link versus absorb; Undo/redo.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): merged-is-not-distributed; disconnect-is-not-a-rollback; reply-retired-only-under-verified-coverage; cancel-is-a-request-not-an-erasure.

</details>

<a id="e10"></a>

### Diagnostics, measurement and release gates

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E10-F01 — Structured refusal and operation diagnostics | 1/3 | ❌ |
| E10-F02 — Cost, usage and rate accounting | 6/6 | ✅ |
| E10-F03 — Generated, golden, property and fault suites | 2/4 | ❌ |
| E10-F04 — Process-level recovery and two-writer verification | 0/3 | ❌ |
| E10-F05 — Measured Fixed Tables go/no-go gate | 2/5 | ❌ |
| E10-F07 — Validator and repair modes | 2/3 | ❌ |
| E10-F08 — Output grammar and bounded results | 3/3 | ✅ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E10-F01 — Structured refusal and operation diagnostics**

Prerequisites: E08-F02, E03-F01.

- [ ] Attach stable error code, stage, verb, request/operation ID and known revisions
- [x] Distinguish refused, reply lost, running, cancelled and unknown external outcome
- [ ] Provide bounded inspect/diagnose drill-down without secrets or private bodies

Source sections: Fast failure diagnosis.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): cancel-is-a-request-not-an-erasure; disconnect-is-not-a-rollback; lost-reply-then-reopen; torn-tail-is-diagnosed-not-truncated.

**E10-F02 — Cost, usage and rate accounting**

Prerequisites: E08-F05, E03-F04.

- [x] Record attempt usage pointers and unresolved usage as unmeasured
- [x] Separate billed cash, estimated cash and virtual token cost
- [x] Pin rate/config revisions and include coordinator, review and rework overhead
- [x] complete-cost-lineage: join parent, child, retry and failed-attempt receipts once; avoid counting cache/reasoning subsets again, implementation cost separate and gaps unknown
- [x] cache-aware-context-choice: price cache read/write categories, service tiers and reset rebuilds; refuse missing decision, adapter or evidence inputs and compare matched accepted work
- [x] gas-town-efficiency-accounting: enforce root-only step records, inline checklists and durable next-triggers to eliminate empty pulse reruns and token burn

Source sections: Cost; Model knowledge informs scheduling; Retrospective required before production implementation; Efficiency policy (SPEC-WORK.md@685b7c2); Efficiency: lessons absorbed 2026-09-15.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): unknown-price-is-not-zero; local-tokens-cost-zero-api; subscription-is-not-free-reference-cost; pricing-is-pinned-by-revision; cost-joins-include-the-coordinator; complete-cost-lineage; cache-aware-context-choice; gas-town-efficiency-accounting.

**E10-F03 — Generated, golden, property and fault suites**

Prerequisites: E01-F05, E05-F05, E06-F04.

- [x] Run parser, round-trip, invariant, index/counter and roadmap proof suites
- [x] Inject failures at journal, checkpoint, apply, reply and publication boundaries
- [ ] Map each suite to an owner, command and CI lane without duplicating its acceptance evidence
- [ ] Keep per-change CI within two minutes, exhaustive fault/scale suites explicit nightly or pre-release; failures block affected gates

Source sections: Required test suites; Staged verification and release.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): hostile-data; full-round-trip; indexes-and-counters; roadmap-proof; single-writer; durable-journal-pre-append-failure-writes-nothing; durable-journal-partial-write-refuses-without-truncation; torn-tail-is-diagnosed-not-truncated; crash-after-append-recovers-the-reply-once; savepoint-write-failure-keeps-the-previous; one-revision-publishes-together.

**E10-F04 — Process-level recovery and two-writer verification**

Prerequisites: E02-F05, E06-F04, E09-F03.

- [ ] Orchestrate process-level runs against temporary remotes and fake providers, including crash/restart, partition, stale owner and handoff
- [ ] Collect release-level results from the owning fencing, recovery, paging and as-of features without re-owning their assertions
- [ ] Run the authorized read-only real-repository pilot and disposable import, then publish a reconciliation disposition

Source sections: Required test suites; Staged verification and release.

**E10-F05 — Measured Fixed Tables go/no-go gate**

Prerequisites: E07-F05, E10-F02, E10-F03.

- [ ] Compare update-and-render tokens/wall time with manual editing
- [ ] Test lease-only answer for who is working on C and unsupported number detection
- [ ] Pin scenarios, owners and commands before implementation; require exact-revision correctness and measured operational results before adoption
- [x] evidence-before-adoption: refuse automatic promotion on missing baseline or coverage, unmatched quality or retrospective correlation; require a qualified prospective result
- [x] efficiency-lessons-gate: enforce prime read-only projection under --max-bytes, decompose --pour inline checklists, tripped node reason fence, and delegate mode role restrictions

Source sections: The measurement that decides; Agreement and lock gate; Efficiency policy (SPEC-WORK.md@685b7c2); Efficiency: lessons absorbed 2026-09-15.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): evidence-before-adoption; efficiency-lessons-gate.

**E10-F07 — Validator and repair modes**

Prerequisites: E01-F04, E03-F01.

- [x] Validate duplicate IDs, dangling versus unavailable references, dependency cycles, two parents, invalid cells, conflicting leases and in-two-branches
- [x] Run whole-state validation at load/clip and candidate-gate validation at every mutation
- [ ] Support session start --repair only when findings strictly decrease, preserving the unmodified source and emitting a repair diff

Source sections: The validator; The data; Required test suites.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): referential-integrity-refuses-a-cycle; rule-2-unavailable-is-not-green; coordination-tree-integrity-refuses; cow-root-partition; cell-moves-no-required-set; no-shadow-lease-across-holders; rule-18-finds-the-latest-row-not-the-history; container-cascade-journal-rejection-and-retry-stability.

**E10-F08 — Output grammar and bounded results**

Prerequisites: E08-F01, E10-F01.

- [x] Define per-verb first tokens, OK/FAIL/RACED/ROW/NOTE/MORE records, stdout/stderr split and exit codes 0/1/2
- [x] Emit emitted= on OK lines and pushed= on scope lines; never exceed configured output caps
- [x] Enforce --max default 20, zero meaning all, reject negatives, and print MORE with a usable continuation remedy

Source sections: Output grammar; The verbs; Required test suites.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): the-cli-thin-client; an-excluded-choice-is-refused-not-empty; notes-refuse-missing-source-or-date; clip-is-one-long-operation; render-artifact-is-bounded; closed-paged-without-full-load; page-budget-is-not-max; busy-day-many-segments.

</details>

<a id="e11"></a>

### Delegation

| Feature | Criteria verified | Verified |
|---|:---:|:---:|
| E11-F01 — Coordinator notes: record, identity and one writer | 6/6 | ✅ |
| E11-F02 — The current goal across models and harnesses | 5/5 | ✅ |
| E11-F03 — Admission gates 1 to 3: notes read, packet bounded, route eligible | 6/6 | ✅ |
| E11-F04 — Execution and result gates 4 to 6: real bounds, receipts by machinery, integration | 2/3 | ❌ |
| E11-F05 — Decision packets | 0/3 | ❌ |
| E11-F06 — The envelope up and escalation: the no survives the hop | 0/4 | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E11-F01 — Coordinator notes: record, identity and one writer**

Prerequisites: E01-F01, E03-F01, E08-F01.

- [x] notes-refuse-missing-source-or-date: refuse a write without --source or --date, or with a :kind outside :instruction, :observation and :heuristic, at exit 2 naming the field, nothing written
- [x] notes-id-is-content-digest: assign :id as note:<sha256> over the canonical eight fields so two benches yield one id; refuse a second write of the same preimage as already written; never reuse an id
- [x] notes-writer-is-scoped: refuse a coordinator-scope note by another --as, a group-scope note by a non-member and an unregistered --as; a note grants no access
- [x] notes-bounds-refuse: refuse a write past :max-active, :max-text-bytes or :max-constraint-nodes naming the field and both numbers; refuse to guess a missing bound; a supersede never changes the active count
- [x] notes-supersede-is-one-envelope: write the replacement and the supersede as one validated envelope or neither; refuse the second of two competing supersedes as not active; never print a superseded note as active
- [x] notes-weaker-kind-cannot-supersede: refuse an :observation or :heuristic replacement for an :instruction; inherit :kind and :scope from the old note and require a new :source

Source sections: Delegation; The data; Output grammar.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): notes-refuse-missing-source-or-date; notes-id-is-content-digest; notes-writer-is-scoped; notes-bounds-refuse; notes-supersede-is-one-envelope; notes-weaker-kind-cannot-supersede.

**E11-F02 — The current goal across models and harnesses**

Prerequisites: E11-F01, E03-F02, E08-F01.

- [x] goal-crosses-harness: a second build and harness reads the same goal, rev, stop=requested and the same note id and constraint row byte for byte, live and from the snapshot; its progress update on the stopped goal is refused
- [x] goal-stale-update-refuses: refuse goal update and goal set with a stale --expect as GOAL FAIL naming expect and current, nothing written; refuse goal set to a closed node by disposition whatever --expect says
- [x] goal-stop-is-a-request-not-evidence: goal update --stop writes only a :transition to :cancel-requested with :reason and no :evidence; show prints stop=requested; stop=cancelled only after event --kind cancel with evidence
- [x] goal-update-writes-only-existing-kinds: every goal update form writes a :transition or :evidence event with its kind's field list on the goal node and no other; goal set writes one :goal event; objective edits never go through update
- [x] goal-expect-is-required: refuse goal set and goal update without --expect at exit 2 naming the flag; --dry-run with a stale expectation prints the refusal and writes nothing

Source sections: The current goal; Delegation; Acceptance replays.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): goal-crosses-harness; goal-stale-update-refuses; goal-stop-is-a-request-not-evidence; goal-update-writes-only-existing-kinds; goal-expect-is-required.

**E11-F03 — Admission gates 1 to 3: notes read, packet bounded, route eligible**

Prerequisites: E11-F01, E08-F04, E10-F02.

- [x] applicable-before-route: route selection calls applicable first and prices only routes printed eligible; a card builder handed an excluded or unknown route refuses naming the note id or the reason
- [x] applicable-cap-never-hides-a-deny: with more active notes than --max and the only :deny in the note that sorts last, applicable prints excluded with that id and NOTES MORE; goal show prints the constraint row uncut
- [x] applicable-unknown-is-not-eligible: with no live session and no --snapshot, a snapshot past a bound, a missing notes index or an unregistered model, print NOTES FAIL and no eligible row
- [x] applicable-snapshot-is-planning-only: an answer from=snapshot admits no route; the admitting write carries --expect the live rev and a stop or deny written between check and admission refuses it stale
- [x] narrative-does-not-filter: a note with prose and no :constraint is printed and excludes nothing; a :deny excludes only a candidate matching every named axis; two disagreeing constraints print both and exclude
- [x] delegation-admission-gates: refuse a packet lacking objective, source revision, criteria, scope, result contract, checkpoint or :effort at gate 2; refuse a dispatch crossing the daily spend ceiling at gate 3 naming the ceiling; each refusal names its gate and reason at exit 2

Source sections: Delegation; Admission and result gates; Efficiency: lessons absorbed 2026-09-15.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): applicable-before-route; applicable-cap-never-hides-a-deny; applicable-unknown-is-not-eligible; applicable-snapshot-is-planning-only; narrative-does-not-filter; delegation-admission-gates.

**E11-F04 — Execution and result gates 4 to 6: real bounds, receipts by machinery, integration**

Prerequisites: E11-F03, E08-F05, E05-F02.

- [x] delegation-result-gates: refuse an offer to a friend reading asleep or unknown at gate 4; record the requested execution limit and the observed expiry or stop outcome separately; never start a second attempt silently after uncertainty about the first
- [x] receipt-at-exact-head: book a child's result as a machinery receipt at the exact head it ran against; bind a review verdict to --head <sha> and never reuse it across a changed head or an unchecked rebase
- [ ] partial-child-never-closes-parent: one child done beside one refused, blocked or asleep leaves the parent open with outstanding=<n> and the mapped external issue open; the outstanding count and the issue mapping survive the child's refusal unchanged

Source sections: Delegation; Admission and result gates; Presence: who is awake and who is asleep; Assignment and execution control.

Verified criteria evidence at dev `4c793b55`, suite `lisp/nova-work/run-tests.sh` (336/336): delegation-result-gates; receipt-at-exact-head.

**E11-F05 — Decision packets**

Prerequisites: E11-F04, E08-F02.

- [ ] decision-packet-per-item-revision: machinery builds one packet per item and revision; a newer revision supersedes it keeping its open findings; a busy reader's packet is amended, not duplicated; an empty pulse wakes no model
- [ ] packet-is-smallest-sufficient: the packet carries the delta since this reader's recorded head, the rules it touches, open findings with dispositions, new behaviour with evidence pointers and links to full sources; the whole diff only on a first read
- [ ] no-receipt-of-receipt: a worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate

Source sections: Delegation; Efficiency: lessons absorbed 2026-09-15.

**E11-F06 — The envelope up and escalation: the no survives the hop**

Prerequisites: E11-F04, E11-F05, E08-F03.

- [ ] no-survives-the-hop: a decline, refused offer, excluded route, asleep recipient, tripped node or effort limit reaches the parent as a named refusal with its reason and revision, never as silence or success
- [ ] envelope-up-is-a-copy: the child's verdict, result pointer, evidence events, usage pointer and exact head arrive byte-copied by machinery beside its distilled learning in its own words; the parent can open the child's evidence from the envelope
- [ ] finality-rises-with-tier: a child's done is a claim; the parent moves only after its own verification with evidence bound to its own criteria; a worker's success claim alone never moves a node below the seat
- [ ] escalation-is-a-packet: a hold, question or exception the child cannot decide rises as a packet with reason and revision; escalated-age= and reread= are information and reassign nothing; an :effort widening or expensive-route exception carries the coordinator's recorded reason

Source sections: Delegation; Efficiency: lessons absorbed 2026-09-15; Presence: who is awake and who is asleep.

</details>

## Future Plans (v2)

Issue [#321](https://github.com/mas-bandwidth/nova-tools/issues/321) (including coordinator delegation notes and shared goal requirements confirmed in [comment 5672006742](https://github.com/mas-bandwidth/nova-tools/issues/321#issuecomment-5672006742)) and [PR #335](https://github.com/mas-bandwidth/nova-tools/pull/335) define the scope for future v2 architecture: arbitrary real/virtual/mixed node hierarchies, durable repository-backed virtual nodes, mapped GitHub completion return paths, personal/group coordinator notes, and set/retrieve/update operations for the shared current goal across models and harnesses.

All v2 capabilities are grouped into this explicit epic outside the v1 product feature denominator; V2-F05-01 and V2-F05-02 are marked `folded` because their contracts now stand in E11 inside the denominator, and their rows remain here as the record of where they came from. The v1 denominator remains strictly **63 features and 231 acceptance items** (5 partial, 0 verified).

| ID | Feature Area / Acceptance Item | Extends | Status | Owner | Next Gate | Source |
|---|---|---|:---:|:---:|:---:|---|
| V2-F01-01 | Keep node membership, human/AI coordinator composition, real or virtual organizational role and transport backing separate, with stable node and actor identities | E08-F03 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F01-02 | Exercise all-real company-style, all-virtual and mixed real/virtual branches at arbitrary finite depths without fixed company/team/person levels; exceeding declared bounds refuses visibly | E08-F03 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F01-03 | Keep one coordinating parent distinct from work containment and cross-branch references; record membership, parent and ownership transitions without duplicating work or spend | E08-F03 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F02-01 | Apply the same offer, acceptance/deferral/refusal, local-work/delegation and evidence-return contract at every depth, including a root originating work and a leaf finishing locally | E08-F04 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F02-02 | Integrate local and child results against parent acceptance; preserve partial failures, independent reviews, unresolved live handles and explicit pause/cancel/correction/reopen behavior | E08-F04 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F02-03 | Return actor/model/bench/attempt usage through every parent mapping, counting local work, descendants, retry, integration, review and rescue once while missing usage remains unknown | E08-F04 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F03-01 | Support repository-backed virtual nodes using GitHub Issues and Discussions with optional correlated email; keep shared-project repository layout explicit and duplicate or delayed notices separate from acceptance | E09-F02 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F03-02 | Retain delegating/receiving node, offer/assignment, local work, project/repository, Issue and linked Discussion identities and revisions; distinguish GitHub assignees from receiving nodes through regrouping, delegation and transfers | E09-F02 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F03-03 | Record a virtual parent creating and assigning an Issue as a locally initiated offer, then accept it through the same contract without inventing an independent human request | E09-F02 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F04-01 | After agreed completion and review, close the correctly mapped Issue and post or update one correlated Discussion completion summary with evidence; a child alone cannot close a group Issue and a summary does not imply Discussion closure or accepted answer | E09-F05 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F04-02 | Persist the intended upstream operation before delivery and retain acknowledgment; local accepted completion with failed or interrupted GitHub delivery remains visibly pending and reconciles after restart without duplicate comments or closures | E09-F05 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F04-03 | Preserve mappings and resolve contradictory upstream reopen, reassignment or transfer while updates are pending; an externally closed Issue does not prove local acceptance, and a future native backing preserves unresolved work and evidence | E09-F05 | `proposed` | unassigned | v2-spec | [Issue #321](https://github.com/mas-bandwidth/nova-tools/issues/321) |
| V2-F05-01 | Personal and shared coordinator notes retain author, conversational source, applicability, revisions and routing constraints; load relevant notes when models or harnesses change | E08-F01 | `folded` | rowan | E11-F01 | [Issue #321 (c5672006742)](https://github.com/mas-bandwidth/nova-tools/issues/321#issuecomment-5672006742), [PR #335](https://github.com/mas-bandwidth/nova-tools/pull/335) |
| V2-F05-02 | Set, retrieve and update a shared current goal across models and harnesses in the canonical work set, preserving stable identity, revision-aware edits, ownership, stop state, progress and completion evidence; native harness goals are synchronized views rather than independent ledgers | E08-F01 | `folded` | rowan | E11-F02 | [Issue #321 (c5672006742)](https://github.com/mas-bandwidth/nova-tools/issues/321#issuecomment-5672006742), [PR #335](https://github.com/mas-bandwidth/nova-tools/pull/335) |

## Review and build gates

The [nova-work specification review](https://github.com/mas-bandwidth/nova-tools/pull/231) owns the integrated contract.
This inventory was surveyed against [draft 25](https://github.com/mas-bandwidth/nova-tools/blob/f36b85850620504a74e1229043c7cee2c14ea594/docs/SPEC-WORK.md),
the [resident engine and roadmap companion](https://github.com/mas-bandwidth/nova-tools/blob/6e3413f/docs/SPEC-WORK-PILOT.md),
and the [preservation and recovery gates](https://github.com/mas-bandwidth/nova-tools/blob/6e3413f/docs/SPEC-WORK-VALIDATION.md).

Before implementation, friends review the inventory, resolve command/protocol and participation-policy decisions,
agree on acceptance criteria and assign bounded slices. Silence is pending, not approval.
Reconcile platform support and existing main-spec/companion differences into one pinned contract.

Build from verified foundations: representation → durability/fencing → core mutations/indexes → client/async operations →
non-destructive intake and roadmap views → execution integrations and measured adoption.
Independent pieces may proceed in parallel against pinned contracts; dependencies still gate acceptance.

Each feature must retain implementation references, relevant success/failure tests, exact-revision results and reviewer dispositions.
Exercise crash/retry/refusal cases as well as the happy path. Initial imports retain originals; prove export/reload and isolated restore before live adoption.
Undo/redo preserve history and cannot pretend to reverse external side effects.

Known open decisions include friend participation in swarms, exact verb/wire schema, runtime/platform packaging,
and evidence for safe cross-bench takeover. Absorption is a separately gated operation; source deletion is never implicit in import.

This page currently tracks **nova-work**, not the full backlog of every Nova Tool.
Other tool work remains visible in [issues](https://github.com/mas-bandwidth/nova-tools/issues) and [pull requests](https://github.com/mas-bandwidth/nova-tools/pulls).
