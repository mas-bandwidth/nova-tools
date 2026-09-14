# Nova Tools roadmap

## nova-work

Help AI friends coordinate work without repeatedly rebuilding the plan in their context.

**Planning baseline: 10 epics, 52 features, 171 inventoried items (166 acceptance items and 5 open questions).**
**Verified implementation: 0%.** This is proposed scope for review, not a delivery-date or effort estimate.
The current main branch has no production `nova-work` engine or CLI; existing prototypes and written specs do not count as verified production features.
Current scope after the recorded moves and discoveries: 57 product features and 200 tickable acceptance items; the historical 171-item baseline count remains unchanged. The original inventory is retained at revision `248d85b`.

The current work register is below. The first internal C/O kernel has merged; a merged partial implementation and passing subset tests do not mark a full production feature verified. The planning source records partial support for E01-F01, E01-F03, E01-F05, E03-F01 and E04-F01 with the merged subset evidence; their cells remain ❌ until all criteria pass.

✅ = implemented and verified against the agreed criteria at a recorded source revision. ❌ = any other state.
Completion is verified features divided by applicable features; it is not averaged subtask percentages or an estimate of time remaining.

This roadmap uses **epic → feature → sub-feature/acceptance item**, with no language axis.
[Recursive work data](docs/roadmaps/nova-work.sexp) retains stable IDs, dependencies and source references.
This baseline format is planning data, not a claim that nova-work import/export already exists.
Source citation key: `SPEC-WORK.md@f36b8585` names main-spec headings; `SPEC-WORK-PILOT.md@6e3413f` names resident-engine/roadmap headings; later companion refinements are pinned as `SPEC-WORK-PILOT.md@4e800fb` (eager W) and `SPEC-WORK-PILOT.md@bc4a4a4` (batch transport); `SPEC-WORK-VALIDATION.md@6e3413f` names acceptance suites. Additive deltas are `SPEC-WORK.md@7db3b95` (response correlation), proposed `SPEC-WORK.md@a0cfcf5` (resident 24-hour bound), `SPEC-WORK.md@685b7c2` (efficiency policy, merged to `spec/nova-work` by PR #317 as `cf5dd8f5`), and PR #319 `SPEC-WORK.md@9488a19` (recursive project/stream grouping, merged to `spec/nova-work` as `9c120a3b`). A source-section label below is resolved through its named file/revision key.

| Epic | Features | Verified |
|---|:---:|:---:|
| [Canonical work data and restricted representation](#e01) | 5 | 0% |
| [Resident coordinator session and fencing](#e02) | 7 | 0% |
| [Typed mutation and work lifecycle](#e03) | 5 | 0% |
| [Counting, indexes and bounded queries](#e04) | 6 | 0% |
| [Evidence, verification and acceptance proof](#e05) | 6 | 0% |
| [Persistence, closed history and recovery](#e06) | 5 | 0% |
| [Roadmap views and Fixed Tables pilot parity](#e07) | 6 | 0% |
| [Coordinator protocol, friends and execution records](#e08) | 5 | 0% |
| [Issue intake, migration and external boundaries](#e09) | 5 | 0% |
| [Diagnostics, measurement and release gates](#e10) | 7 | 0% |

## Scope movement

| Baseline features | Discovered after baseline | Verified | Reopened | Removed | Current features |
|:---:|:---:|:---:|:---:|:---:|:---:|
| 52 | 6 | 0 | 0 | 1 | 57 |

Preserve feature IDs and the baseline. The six discovered features below close source-backed gaps found in the PR #269 review; the response-correlation, ten efficiency-policy and three recursive-grouping acceptance discoveries map to existing features without creating features. E10-F06 moved to the Now register outside this product denominator. Append discoveries with reasons; record decomposition and removal separately.
Do not count removal as completion. These counts track inventory movement, not estimated engineering effort.

## Open questions

- Common Lisp runtime packaging and supported platforms must be pinned before release.
- Category taxonomy and roadmap completion policy beyond `all-required-features` remain open.
- Exact verb and wire protocol spelling must be finalized in one schema before lock.
- Friend participation in swarms versus model-only pools requires explicit dispositions, including Freddy's.
- Absorption remains disabled pending independent reconciliation and authorization gates.

## Research and Development (R&D)

All active engineering and research items outside the v1 product feature denominator are organized in this explicit R&D register. These items track active prototypes, compiler and runtime hardening, protocol companions, decision packets, and adapter explorations across the team. Items in this register do not grant completion credit toward the v1 product denominator (57 features and 200 acceptance items).

| ID | Work Item | Status | Owner | Next Gate | Source / Evidence |
|---|---|:---:|:---:|:---:|---|
| RD-01 | PR #300: Common Lisp C/O kernel and maintained open counter | `in-review` | rowan | merge-gate | pr-300 |
| RD-02 | PR #310: Token ledger preimage migration integration into PR #264 | `in-review` | rowan | parent-gate | pr-310 |
| RD-03 | PR #311: Common Lisp test suite parity with SBCL | `in-review` | freddy | merge-gate | pr-311 |
| RD-04 | PR #312: SBCL memory compaction and foreign memory hardening | `in-review` | freddy | merge-gate | pr-312 |
| RD-05 | PR #314: Common Lisp compiler warnings pass | `in-review` | freddy | merge-gate | pr-314 |
| RD-06 | PR #316: Common Lisp reader/writer roundtrip in PR #240 | `in-review` | freddy | parent-gate | pr-316 |
| RD-07 | PR #317: Efficiency policy acceptance replays in spec/nova-work | `in-review` | freddy | merge-gate | pr-317 |
| RD-08 | PR #319: Recursive project and stream grouping in spec/nova-work | `in-review` | stella | merge-gate | pr-319 |
| RD-09 | PR #320: Restricted reader repairs merged into main | `in-review` | freddy | verification | pr-320 |
| RD-10 | PR #322: Work-set static container cascades and atomic settle/revive | `in-review` | rowan | verification | pr-322 |
| RD-11 | PR #323: Finite records contract decision packet and Rule 29 amendment | `in-review` | emma | whole-package-slice | pr-323 |
| RD-12 | PR #325: Swarm job profile re-reservation and migration status in PR #264 | `in-review` | stella | runtime-gate | pr-325 |
| RD-13 | PR #326: Native optional batch receipt proposal | `in-review` | rowan | encoding-gate | pr-326 |
| RD-14 | PR #330: Roadmap refresh with verified evidence through 21:57Z | `in-review` | freddy | verification | pr-330 |
| RD-15 | PR #331: Runtime task start and restart through kernel submit path | `in-review` | rowan | freddy-review | pr-331 |
| RD-16 | PR #332: Batch receipt admission companion specification | `in-review` | stella | darwin-witness | pr-332 |
| RD-17 | PR #333: Bounded durable filesystem journal persistence and replay adapter | `in-review` | emma | darwin-sync-witness | pr-333 |
| RD-18 | PR #334: Swarm lease heartbeat and fencing token protocol | `in-review` | stella | review | pr-334 |
| RD-19 | PRs #293-#295: Token ledger schema v2 foundation with Fable approvals | `in-review` | rowan | friend-gates | prs-293-295 |
| RD-20 | Issue #185: Finite records contract and whole-package validation | `proposed` | emma | slice-c-validator | issue-185 |
| RD-21 | Issue #336: Herdr multi-agent coordinator adapter | `proposed` | johnny | proposal | issue-336 |

## Feature inventory

<a id="e01"></a>

### Canonical work data and restricted representation

| Feature | Verified |
|---|:---:|
| E01-F01 — Restricted Lisp reader and safe syntax | ❌ |
| E01-F02 — Uniform read bounds and schema validation | ❌ |
| E01-F03 — Stable node identity and canonical containment | ❌ |
| E01-F04 — Typed work kinds and acceptance schema | ❌ |
| E01-F05 — Canonical encoding and semantic round trip | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E01-F01 — Restricted Lisp reader and safe syntax**

Prerequisites: none.

- [ ] Accept only lists, keywords, strings, integers and comments
- [ ] Reject reader/evaluation macros with byte-offset diagnostics
- [ ] Never evaluate imported data

Source sections: The data; Hostile data and limits.

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
- [ ] Preserve IDs through rename, close, reopen and reparent
- [ ] Allow omitted, repeated and recursively nested work-set grouping layers without a prescribed depth

Source sections: The data; Engine and representation; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

**E01-F04 — Typed work kinds and acceptance schema**

Prerequisites: E01-F03.

- [ ] Represent work-set, feature, roadmap, task, lease and event kinds
- [ ] Represent leaf tasks separately from parent tasks and attempts
- [ ] Validate acceptance kind, subject, predicate and required flag
- [ ] Represent project and stream groupings as work-set categories without inferring kind from title or position

Source sections: The data; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

**E01-F05 — Canonical encoding and semantic round trip**

Prerequisites: E01-F01, E01-F04.

- [ ] Produce deterministic bytes with explicit absent/empty and Unicode semantics
- [ ] Preserve large integers, timestamps, escaping and multiline text
- [ ] Compare exports with an independent semantic comparator

Source sections: Format determinism; Full data round trip.

</details>

<a id="e02"></a>

### Resident coordinator session and fencing

| Feature | Verified |
|---|:---:|
| E02-F01 — Resident O/COW session lifecycle | ❌ |
| E02-F02 — Single coordinator ownership record | ❌ |
| E02-F03 — Journal and endpoint locking | ❌ |
| E02-F04 — Lease expiry, reconfirmation and self-fencing | ❌ |
| E02-F05 — Handoff, stop and successor recovery | ❌ |
| E02-F06 — Runtime packaging and first-run installation | ❌ |
| E02-F07 — Execution leases and worker ownership | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E02-F01 — Resident O/COW session lifecycle**

Prerequisites: E01-F03.

- [ ] Load one resident O with structure and event log
- [ ] Keep C as closed history and W as a predicate inside O
- [ ] Make session start the launcher and expose status

Source sections: The execution model; The root is COW; Engine and representation.

**E02-F02 — Single coordinator ownership record**

Prerequisites: E02-F01.

- [ ] Persist owner name, generation, random token, stamp and expiry
- [ ] Allow take and resume with generation rules; successor handoff belongs to E02-F05
- [ ] Refuse competing live owners and name holder details
- [ ] Verify generation fencing under partitions and clock skew; reject unsafe takeover rather than relying on PID locks alone

Source sections: The execution model; One coordinator, one live reader/writer.

**E02-F03 — Journal and endpoint locking**

Prerequisites: E02-F02.

- [ ] Lock canonical journal path for the full process lifetime
- [ ] Lock filesystem socket or use Windows first-pipe-instance semantics
- [ ] Never unlink another live process lock or endpoint

Source sections: The execution model.

**E02-F04 — Lease expiry, reconfirmation and self-fencing**

Prerequisites: E02-F02, E02-F03.

- [ ] Check base tip and owner token before every write
- [ ] Admit each request against until and fence on expiry or divergence
- [ ] Permit only status/export while fenced and preserve journal work

Source sections: The execution model; Single writer.

**E02-F05 — Handoff, stop and successor recovery**

Prerequisites: E02-F04, E06-F03.

- [ ] Clip and publish successor or released owner record atomically
- [ ] Let named successor take next generation without expiry wait
- [ ] Recover fenced accepted events through export and replay

Source sections: The execution model; Checkpoints, undo and redo.

**E02-F06 — Runtime packaging and first-run installation**

Prerequisites: E02-F01, E08-F02.

- [ ] Pin Common Lisp implementation, Go client and supported OS/runtime combinations
- [ ] Version client/engine/protocol explicitly and refuse unsupported combinations
- [ ] Provide reproducible build/install and small startup/status/shutdown smoke tests

Source sections: Engine and representation; The execution model.

**E02-F07 — Execution leases and worker ownership**

Prerequisites: E02-F04.

- [ ] Keep execution leases distinct from the coordinator ownership lease, with at most one live lease per node
- [ ] Support take, heartbeat, holder-only release, handed release, one extension and explicit escalation with deadline/default fields
- [ ] Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now
- [ ] Preserve lease state through reassignment, correction, pause, stop and recovery without counting an attempt twice

Source sections: The data; The execution model; Operational lessons the pilot must exercise.

</details>

<a id="e03"></a>

### Typed mutation and work lifecycle

| Feature | Verified |
|---|:---:|
| E03-F01 — Atomic request envelopes and idempotency | ❌ |
| E03-F02 — Structure verbs and decomposition | ❌ |
| E03-F03 — Scope, baseline and dependency changes | ❌ |
| E03-F04 — State correction and completion transitions | ❌ |
| E03-F05 — Reversible undo and redo plans | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E03-F01 — Atomic request envelopes and idempotency**

Prerequisites: E01-F05, E02-F04.

- [ ] Assign stable request IDs and durable event envelopes
- [ ] Validate multi-node changes all-or-none
- [ ] Refuse repeated IDs or changed payload digests

Source sections: The data; The verbs; Retry/protocol.

**E03-F02 — Structure verbs and decomposition**

Prerequisites: E03-F01.

- [ ] Support add, metadata edit, move/reparent, decompose, link/unlink and retire
- [ ] Record structural event fields in verb-defined order
- [ ] Preserve decomposition as scope change rather than completion

Source sections: The data; The verbs; Inventory before implementation.

**E03-F03 — Scope, baseline and dependency changes**

Prerequisites: E03-F02.

- [ ] Support baseline, discovery, require, dependency add/remove and prioritize
- [ ] Record author, reason, scope revision and exact member delta
- [ ] Keep shared prerequisites singly owned and referenced

Source sections: Counting; The data; Operational lessons the pilot must exercise.

**E03-F04 — State correction and completion transitions**

Prerequisites: E03-F03, E05-F01.

- [ ] Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded
- [ ] Bump task generation on correction and invalidate older evidence
- [ ] Keep reopen, pause and stop as durable events
- [ ] Settle and reopen required-member ancestors at every grouping depth while empty required sets remain incomplete

Source sections: The data; The validator; Required coordinator operations; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

**E03-F05 — Reversible undo and redo plans**

Prerequisites: E03-F04, E06-F03.

- [ ] Create typed compensating envelopes from preimages
- [ ] Check expected revisions, descendants and dependencies before apply
- [ ] Preserve original history and refuse irreversible or uncertain effects

Source sections: Checkpoints, undo and redo; Undo/redo.

</details>

<a id="e04"></a>

### Counting, indexes and bounded queries

| Feature | Verified |
|---|:---:|
| E04-F01 — Incremental open counters and grains | ❌ |
| E04-F02 — Roadmap percent and cell progress | ❌ |
| E04-F03 — Scope movement and branch counts | ❌ |
| E04-F04 — Stable resident indexes and bounded access | ❌ |
| E04-F05 — Historical indexed queries and coverage honesty | ❌ |
| E04-F06 — As-of reconstruction over O | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E04-F01 — Incremental open counters and grains**

Prerequisites: E03-F01, E01-F03.

- [ ] Maintain root open-item counters in mutation envelopes
- [ ] Count canonical IDs once and exclude references, attempts and history
- [ ] Label features, leaves and member grains with revision

Source sections: Counting; Counts are read directly.

**E04-F02 — Roadmap percent and cell progress**

Prerequisites: E04-F01, E07-F01.

- [ ] Compute green feature cells over applicable rows per axis member
- [ ] Print green, applicable, rows and baseline-rows
- [ ] Print completed required leaves as k/n and never average percentages

Source sections: Counting; A cell is a reference, not another state store.

**E04-F03 — Scope movement and branch counts**

Prerequisites: E03-F03, E04-F01.

- [ ] Keep baseline, discovery, deferred, cancelled and superseded distinctions
- [ ] Report open plus closed totals without double membership
- [ ] Report closed-in, settles-in and revives-in for windows

Source sections: Counting.

**E04-F04 — Stable resident indexes and bounded access**

Prerequisites: E04-F01, E01-F03.

- [ ] Build and incrementally update indexes for IDs, containment, dependencies and repositories
- [ ] Provide bounded focus, subtree, category, ready and blocker queries
- [ ] Avoid materialized transitive descendant sets and unbounded scans
- [ ] Maintain eager W membership, its counter and per-friend reverse indexes in the same mutation envelope; normal reads never rebuild W lazily

Source sections: The data; Counting; Queries — the contract; W is an eagerly maintained working index.

**E04-F05 — Historical indexed queries and coverage honesty**

Prerequisites: E04-F04, E06-F02.

- [ ] Resolve settled dependencies and closed rows through indexed lookup
- [ ] Distinguish absent days, missing segments and unavailable as-of partitions
- [ ] Return gap, provenance or refusal rather than fabricate state

Source sections: The execution model — retention; Queries — the contract; Old history.

**E04-F06 — As-of reconstruction over O**

Prerequisites: E04-F04, E03-F01.

- [ ] Answer --at <revision> over O by replaying retained events in memory
- [ ] Include the replay in revision-labelled counts and refuse before the retention boundary
- [ ] Keep historical O answers separate from indexed C as-of queries and report unavailable history honestly

Source sections: Queries — the contract; The execution model — retention; Counting.

</details>

<a id="e05"></a>

### Evidence, verification and acceptance proof

| Feature | Verified |
|---|:---:|
| E05-F01 — Evidence events and generation guards | ❌ |
| E05-F02 — Verification and stale evidence reporting | ❌ |
| E05-F03 — Reviews, findings and attestations | ❌ |
| E05-F04 — Dependency and release gates | ❌ |
| E05-F05 — Independent proof and mutation regression checks | ❌ |
| E05-F06 — Evidence resolvers and verification cache | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E05-F01 — Evidence events and generation guards**

Prerequisites: E01-F04, E03-F01.

- [ ] Bind pointer, criterion, against revision and generation to evidence
- [ ] Require matching criterion kind, exact subject and predicate
- [ ] Make correction generation invalidate prior qualification

Source sections: The data; The validator.

**E05-F02 — Verification and stale evidence reporting**

Prerequisites: E05-F01.

- [ ] Verify every evidence event, including those not named by done
- [ ] Report stale, freshest and done-unverified facts
- [ ] Keep test, job, merged and attested proof distinct

Source sections: The validator; Evidence and the imported starting point.

**E05-F03 — Reviews, findings and attestations**

Prerequisites: E05-F01.

- [ ] Record exact-revision reviewer findings and author dispositions
- [ ] Support attested criteria with reviewer identity and result
- [ ] Preserve disagreement, unknowns and repair cycles
- [ ] reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers

Source sections: Operational lessons the pilot must exercise; The data; Efficiency policy (SPEC-WORK.md@685b7c2).

**E05-F04 — Dependency and release gates**

Prerequisites: E05-F02, E04-F04.

- [ ] Require prerequisite dependencies and acceptance before green
- [ ] Keep merged fix, verified behavior and published distribution separate
- [ ] Resolve release version through the release task reference

Source sections: The validator; A cell is a reference, not another state store.

**E05-F05 — Independent proof and mutation regression checks**

Prerequisites: E05-F02.

- [ ] Use independent oracle/comparator and deliberately broken assertions
- [ ] Retain fixtures, revisions, fault points and expected/actual reconciliation
- [ ] Reject green claims unsupported by criterion-level proof

Source sections: Independent oracle and retained evidence; Roadmap proof.

**E05-F06 — Evidence resolvers and verification cache**

Prerequisites: E05-F01.

- [ ] Resolve configured pointer schemes by direct executable invocation with fact/stamp arguments and no shell
- [ ] Enforce max-fetch, fetch-timeout and offline behavior; an absent resolver is unreachable
- [ ] Cache by pointer, subject and resolver identity, pin resolver identity on snapshot reads, and preserve unknown results

Source sections: The validator; The resident session; Required test suites.

</details>

<a id="e06"></a>

### Persistence, closed history and recovery

| Feature | Verified |
|---|:---:|
| E06-F01 — Durable journal and local checkpoints | ❌ |
| E06-F02 — Bounded C partitions and historical indexes | ❌ |
| E06-F03 — Clip, commit and recovery replay | ❌ |
| E06-F04 — Isolated restore and compare | ❌ |
| E06-F05 — Lossless export and schema migration | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E06-F01 — Durable journal and local checkpoints**

Prerequisites: E01-F05, E03-F01.

- [ ] Journal every accepted mutation before acknowledgment
- [ ] Create validated snapshots with schema, revision, boundary and manifest
- [ ] Expose local/shared revision, age, unshared work and failed backup
- [ ] Keep prior verified snapshots and protect accepted journal history through retention/compaction

Source sections: Checkpoints, undo and redo; The resident session.

**E06-F02 — Bounded C partitions and historical indexes**

Prerequisites: E06-F01, E03-F04.

- [ ] Partition closure events by their recorded UTC day in immutable bounded segments
- [ ] Publish manifests, closed rows and dedup/closed index roots in bounded pages
- [ ] Maintain rolling 24-hour default window with at most two day partitions; pending SPEC-WORK.md@a0cfcf5 correction adds noon/midnight/over-limit witnesses while explicit historical queries remain bounded

Source sections: The execution model — retention; The data; Old history; Resident-window correction (proposed SPEC-WORK.md@a0cfcf5).

**E06-F03 — Clip, commit and recovery replay**

Prerequisites: E06-F02, E02-F04.

- [ ] Publish snapshot, segments, manifests and both index roots in one revision
- [ ] Verify hashes and never expose a root without referenced files
- [ ] Reload snapshot, indexes and journal overlays after crash

Source sections: The execution model — retention; Checkpoints, undo and redo.

**E06-F04 — Isolated restore and compare**

Prerequisites: E06-F03.

- [ ] Restore read-only or isolated without ownership, dispatch or side effects
- [ ] Compare prior checkpoint to current state and report gaps
- [ ] Keep originals and prior known-good checkpoints during replacement

Source sections: Checkpoints, undo and redo; Recovery.

**E06-F05 — Lossless export and schema migration**

Prerequisites: E06-F03, E01-F05.

- [ ] Export selected full history beyond resident window with declared scope
- [ ] Migrate supported old schemas semantically and refuse unsupported versions
- [ ] Retain source originals, provenance and unresolved records

Source sections: Lossless migration and round-trip release gates; Schema evolution; Old history.

</details>

<a id="e07"></a>

### Roadmap views and Fixed Tables pilot parity

| Feature | Verified |
|---|:---:|
| E07-F01 — Typed roadmap axes and cell references | ❌ |
| E07-F02 — Completion-only projection renderer | ❌ |
| E07-F03 — Generated ROADMAP drift checks | ❌ |
| E07-F04 — Fixed Tables imported baseline inventory | ❌ |
| E07-F05 — Pilot retrospective and scope evolution | ❌ |
| E07-F06 — Private-node projection filtering | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E07-F01 — Typed roadmap axes and cell references**

Prerequisites: E01-F04, E03-F03.

- [ ] Represent ordered axes and coordinate-to-node references
- [ ] Refuse unknown axis members and duplicate coordinates
- [ ] Treat missing cells and out-of-scope cells distinctly
- [ ] Preserve selected scope and completion unit across recursive grouping layouts without mandatory axis or cell wrappers
- [ ] Retain completed roadmap members and historical code/test evidence beyond the active 24-hour window

Source sections: The data; A cell is a reference, not another state store; Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b).

**E07-F02 — Completion-only projection renderer**

Prerequisites: E05-F02, E07-F01.

- [ ] Render tick only for fully verified cells and cross otherwise
- [ ] Keep partial, missing and unknown state in S/check counts
- [ ] Write only the marked roadmap region and preserve unrelated bytes

Source sections: Roadmap as a view; the Schema pilot; What the current prototype proves, and does not.

**E07-F03 — Generated ROADMAP drift checks**

Prerequisites: E07-F02, E06-F03.

- [ ] Regenerate ROADMAP from primary O data
- [ ] Make render --check fail on drift or ambiguous markers
- [ ] Ensure chat/file projections use the same captured revision

Source sections: The failures it closes; Roadmap as a view; the Schema pilot; Roadmap proof.

**E07-F04 — Fixed Tables imported baseline inventory**

Prerequisites: E06-F05, E07-F01.

- [ ] Preserve source revision, audit IDs, reports and aliases
- [ ] Include ordinary delivered capabilities beside versioning rows
- [ ] Keep source support separate from qualified acceptance and incomplete reconciliation visible

Source sections: Evidence and the imported starting point; Inventory before implementation.

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

| Feature | Verified |
|---|:---:|
| E08-F01 — Versioned typed command schema and discovery | ❌ |
| E08-F02 — Client transport and asynchronous operations | ❌ |
| E08-F03 — Friends, CONFIG and ACTIVE indexes | ❌ |
| E08-F04 — Availability, offers and assignment reconciliation | ❌ |
| E08-F05 — Bounded config/pricing exchange and model suitability | ❌ |

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

- [ ] Support framed requests, status, wait, cancel and bounded backpressure
- [ ] Handle disconnect, lost reply, deadlines and uncertain outcomes
- [ ] Keep control operations responsive during import/export/clip
- [ ] Use bounded typed JSON over a local Unix socket, exact integer/time encoding and durable asynchronous operation IDs; reconcile cross-platform endpoint requirements before lock
- [ ] Support bounded read bundles at one captured revision and lease-time watermark, with stable paginated snapshot identity
- [ ] Support independent ordered batches with per-entry IDs/outcomes and explicit stop/continue semantics, without implying rollback
- [ ] Support atomic mutation batches with all-or-none validated O/W, counter and reverse-index changes; reject oversized batches without silently splitting
- [ ] pipeline-replies-are-correlated: correlate out-of-order or fragmented ordinary responses by request ID, retain a distinct durable operation ID, name independent not-attempted and atomic validation entry outcomes, and close on unknown, duplicate, absent or undecodable IDs; same-ID recovery uses E03-F01 durable idempotency
- [ ] batch-with-bounds-and-urgency: coalesce independent results within byte, record and delay bounds; refuse unreferenced padding, bypass delay for urgent changes and preserve dependency and retry identity

Source sections: The verbs; Async operations; Retry/protocol; Batch-friendly transport and explicit atomicity; Response correlation (SPEC-WORK.md@7db3b95); Efficiency policy (SPEC-WORK.md@685b7c2).

**E08-F03 — Friends, CONFIG and ACTIVE indexes**

Prerequisites: E04-F04, E03-F04.

- [ ] Index every known friend, assignments and working task references
- [ ] Separate stable capabilities/config from observed active executions
- [ ] Include coordinator identity and attribute model, bench, attempt and usage
- [ ] Store agreed specialist roles per friend; keep model strengths/weaknesses in the shared model catalog
- [ ] Represent children, swarm capabilities, local runs and one-shots separately from friend identity; agreed concurrency limits apply
- [ ] bounds-are-not-prompts: refuse automatic dispatch when an adapter cannot enforce the configured execution limit; distinguish wait deadline from observed stop or unresolved outcome

Source sections: Friends and assignments are resident indexes too; CONFIG and ACTIVE are different sections; Efficiency policy (SPEC-WORK.md@685b7c2).

**E08-F04 — Availability, offers and assignment reconciliation**

Prerequisites: E08-F03, E02-F04.

- [ ] Represent explicit rest, unavailable and unconfirmed contact with observation age
- [ ] Distinguish offer, acknowledgment, ownership, lease and execution
- [ ] Reconcile uncertain prior attempts before relaunch or reassignment
- [ ] After the team-configured silence threshold use one bounded availability probe; nonresponse is unconfirmed capacity, never proof of exhausted credits
- [ ] quiet-until-actionable: keep unchanged traffic mechanical, batch actionable deltas within bounds and let corrections, stops, lease loss and deadlines bypass delay
- [ ] regression-and-recovery: suspend new automatic routing on a breached trial, retain uncertain live handles and use only eligible role-preserving fallback

Source sections: Observed availability; Friends and assignments are resident indexes too; Efficiency policy (SPEC-WORK.md@685b7c2).

**E08-F05 — Bounded config/pricing exchange and model suitability**

Prerequisites: E08-F03.

- [ ] Exchange UNCHANGED manifests or validated deltas by hash/revision
- [ ] Store model route, pricing, quota and provenance without secrets
- [ ] Keep requested versus observed model and measured suitability separate
- [ ] policy-round-trip-and-replay: preserve per-friend efficiency policy, trial manifests and execution references through validated intake, export/import, restart, undo and replay
- [ ] packet-and-route-gates: refuse oversized or history-disallowed packets and ineligible/stale routes; retain a scoped reason for economical-route exceptions

Source sections: Efficient friend config exchange and token pricing; Model knowledge informs scheduling; Swarms, models and friend participation: decision required; Efficiency policy (SPEC-WORK.md@685b7c2).

</details>

<a id="e09"></a>

### Issue intake, migration and external boundaries

| Feature | Verified |
|---|:---:|
| E09-F01 — Non-destructive issue inventory and capture | ❌ |
| E09-F02 — Link mode and correspondence reconciliation | ❌ |
| E09-F03 — Lossless resumable initial migration | ❌ |
| E09-F04 — Explicit absorb operation and deletion gate | ❌ |
| E09-F05 — External adapter and side-effect safety | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E09-F01 — Non-destructive issue inventory and capture**

Prerequisites: E06-F05.

- [ ] Capture stable provider/repository/issue identity, revision and URL
- [ ] Preserve body, comments, labels, relationships, attachments and pagination
- [ ] Keep inaccessible or unsupported fields explicit

Source sections: Public issue correspondence survives intake; Source inventory; Archive completeness.

**E09-F02 — Link mode and correspondence reconciliation**

Prerequisites: E09-F01, E03-F01.

- [ ] Map one issue to many nodes and repeated updates without duplicates
- [ ] Keep remote text as data separate from accepted plan and authority
- [ ] Track pending, confirmed and failed outbound actions with receipts

Source sections: Public issue correspondence survives intake.

**E09-F03 — Lossless resumable initial migration**

Prerequisites: E09-F01, E06-F03.

- [ ] Inventory authorized sources with capture manifest and disposition
- [ ] Import in batches with originals, mappings, deduplication and checkpoints
- [ ] Reconcile counts/content and exercise interruption and source edits

Source sections: Initial migration: preserve first, reconcile, then choose absorption; Import replay; Moving source.

**E09-F04 — Explicit absorb operation and deletion gate**

Prerequisites: E09-F03, E05-F05.

- [ ] Separate absorb from default link and require selected scope/authority
- [ ] Archive source identity, provenance and content before removal; append the actual deletion outcome receipt after the attempt
- [ ] Leave deletion pending on missing content, source change or uncertain network result

Source sections: Link versus absorb; Archive completeness; Lossless migration and round-trip release gates.

**E09-F05 — External adapter and side-effect safety**

Prerequisites: E09-F02.

- [ ] Keep GitHub issue closure, publishing and source deletion separate from local completion
- [ ] Do not claim Git/GitHub atomicity or invent authors
- [ ] Preserve concurrent human changes and uncertain external outcomes

Source sections: Public issue correspondence survives intake; Link versus absorb; Undo/redo.

</details>

<a id="e10"></a>

### Diagnostics, measurement and release gates

| Feature | Verified |
|---|:---:|
| E10-F01 — Structured refusal and operation diagnostics | ❌ |
| E10-F02 — Cost, usage and rate accounting | ❌ |
| E10-F03 — Generated, golden, property and fault suites | ❌ |
| E10-F04 — Process-level recovery and two-writer verification | ❌ |
| E10-F05 — Measured Fixed Tables go/no-go gate | ❌ |
| E10-F07 — Validator and repair modes | ❌ |
| E10-F08 — Output grammar and bounded results | ❌ |

<details>
<summary>Sub-features, prerequisites and acceptance scope</summary>

**E10-F01 — Structured refusal and operation diagnostics**

Prerequisites: E08-F02, E03-F01.

- [ ] Attach stable error code, stage, verb, request/operation ID and known revisions
- [ ] Distinguish refused, reply lost, running, cancelled and unknown external outcome
- [ ] Provide bounded inspect/diagnose drill-down without secrets or private bodies

Source sections: Fast failure diagnosis.

**E10-F02 — Cost, usage and rate accounting**

Prerequisites: E08-F05, E03-F04.

- [ ] Record attempt usage pointers and unresolved usage as unmeasured
- [ ] Separate billed cash, estimated cash and virtual token cost
- [ ] Pin rate/config revisions and include coordinator, review and rework overhead
- [ ] complete-cost-lineage: join parent, child, retry and failed-attempt receipts once; avoid counting cache/reasoning subsets again, implementation cost separate and gaps unknown
- [ ] cache-aware-context-choice: price cache read/write categories, service tiers and reset rebuilds; refuse missing decision, adapter or evidence inputs and compare matched accepted work

Source sections: Cost; Model knowledge informs scheduling; Retrospective required before production implementation; Efficiency policy (SPEC-WORK.md@685b7c2).

**E10-F03 — Generated, golden, property and fault suites**

Prerequisites: E01-F05, E05-F05, E06-F04.

- [ ] Run parser, round-trip, invariant, index/counter and roadmap proof suites
- [ ] Inject failures at journal, checkpoint, apply, reply and publication boundaries
- [ ] Map each suite to an owner, command and CI lane without duplicating its acceptance evidence
- [ ] Keep per-change CI within two minutes, exhaustive fault/scale suites explicit nightly or pre-release; failures block affected gates

Source sections: Required test suites; Staged verification and release.

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
- [ ] evidence-before-adoption: refuse automatic promotion on missing baseline or coverage, unmatched quality or retrospective correlation; require a qualified prospective result

Source sections: The measurement that decides; Agreement and lock gate; Efficiency policy (SPEC-WORK.md@685b7c2).

**E10-F07 — Validator and repair modes**

Prerequisites: E01-F04, E03-F01.

- [ ] Validate duplicate IDs, dangling versus unavailable references, dependency cycles, two parents, invalid cells, conflicting leases and in-two-branches
- [ ] Run whole-state validation at load/clip and candidate-gate validation at every mutation
- [ ] Support session start --repair only when findings strictly decrease, preserving the unmodified source and emitting a repair diff

Source sections: The validator; The data; Required test suites.

**E10-F08 — Output grammar and bounded results**

Prerequisites: E08-F01, E10-F01.

- [ ] Define per-verb first tokens, OK/FAIL/RACED/ROW/NOTE/MORE records, stdout/stderr split and exit codes 0/1/2
- [ ] Emit emitted= on OK lines and pushed= on scope lines; never exceed configured output caps
- [ ] Enforce --max default 20, zero meaning all, reject negatives, and print MORE with a usable continuation remedy

Source sections: Output grammar; The verbs; Required test suites.

</details>

## Future Plans (v2)

Issue [#321](https://github.com/mas-bandwidth/nova-tools/issues/321) and [PR #335](https://github.com/mas-bandwidth/nova-tools/pull/335) define the scope for future v2 architecture: arbitrary real/virtual/mixed node hierarchies, durable repository-backed virtual nodes, mapped GitHub completion return paths, and shared coordinator notes.

All v2 capabilities are grouped into this explicit epic outside the v1 product feature denominator. The v1 denominator remains strictly **57 features and 200 acceptance items** (5 partial, 0 verified).

| ID | Feature Area / Acceptance Item | Extends | Status | Owner | Next Gate | Source |
|---|---|---|:---:|:---:|:---:|---|
| V2-F01-01 | Keep node membership, human/AI coordinator composition, real or virtual organizational role and transport backing separate, with stable node and actor identities | E08-F03 | `proposed` | stella | v2-spec | issue-321 |
| V2-F01-02 | Exercise all-real company-style, all-virtual and mixed real/virtual branches at arbitrary finite depths without fixed company/team/person levels; exceeding declared bounds refuses visibly | E08-F03 | `proposed` | stella | v2-spec | issue-321 |
| V2-F01-03 | Keep one coordinating parent distinct from work containment and cross-branch references; record membership, parent and ownership transitions without duplicating work or spend | E08-F03 | `proposed` | stella | v2-spec | issue-321 |
| V2-F02-01 | Apply the same offer, acceptance/deferral/refusal, local-work/delegation and evidence-return contract at every depth, including a root originating work and a leaf finishing locally | E08-F04 | `proposed` | stella | v2-spec | issue-321 |
| V2-F02-02 | Integrate local and child results against parent acceptance; preserve partial failures, independent reviews, unresolved live handles and explicit pause/cancel/correction/reopen behavior | E08-F04 | `proposed` | stella | v2-spec | issue-321 |
| V2-F02-03 | Return actor/model/bench/attempt usage through every parent mapping, counting local work, descendants, retry, integration, review and rescue once while missing usage remains unknown | E08-F04 | `proposed` | stella | v2-spec | issue-321 |
| V2-F03-01 | Support repository-backed virtual nodes using GitHub Issues and Discussions with optional correlated email; keep shared-project repository layout explicit and duplicate or delayed notices separate from acceptance | E09-F02 | `proposed` | stella | v2-spec | issue-321 |
| V2-F03-02 | Retain delegating/receiving node, offer/assignment, local work, project/repository, Issue and linked Discussion identities and revisions; distinguish GitHub assignees from receiving nodes through regrouping, delegation and transfers | E09-F02 | `proposed` | stella | v2-spec | issue-321 |
| V2-F03-03 | Record a virtual parent creating and assigning an Issue as a locally initiated offer, then accept it through the same contract without inventing an independent human request | E09-F02 | `proposed` | stella | v2-spec | issue-321 |
| V2-F04-01 | After agreed completion and review, close the correctly mapped Issue and post or update one correlated Discussion completion summary with evidence; a child alone cannot close a group Issue and a summary does not imply Discussion closure or accepted answer | E09-F05 | `proposed` | stella | v2-spec | issue-321 |
| V2-F04-02 | Persist the intended upstream operation before delivery and retain acknowledgment; local accepted completion with failed or interrupted GitHub delivery remains visibly pending and reconciles after restart without duplicate comments or closures | E09-F05 | `proposed` | stella | v2-spec | issue-321 |
| V2-F04-03 | Preserve mappings and resolve contradictory upstream reopen, reassignment or transfer while updates are pending; an externally closed Issue does not prove local acceptance, and a future native backing preserves unresolved work and evidence | E09-F05 | `proposed` | stella | v2-spec | issue-321 |
| V2-F05-01 | Durable cross-model coordinator notes stream with sender attribution, monotonic sequence numbers, and bounded retention | E08-F01 | `proposed` | stella | v2-spec | pr-335 |
| V2-F05-02 | Atomic shared current-goal register across heterogeneous agents with optimistic concurrency and conflict detection | E08-F01 | `proposed` | stella | v2-spec | pr-335 |

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
