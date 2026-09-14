; Proposed recursive roadmap data; not an implemented nova-work interchange schema.
(  :schema "nova-work-roadmap-baseline-1"
  :scope-revision 5
  :inventory-status "proposed baseline; awaiting friend review"
  :sources (    :main-spec "SPEC-WORK.md@f36b85850620504a74e1229043c7cee2c14ea594"
    :companion "SPEC-WORK-PILOT.md@6e3413f"
    :companion-eager-w "SPEC-WORK-PILOT.md@4e800fb"
    :companion-batch-transport "SPEC-WORK-PILOT.md@bc4a4a4"
    :validation "SPEC-WORK-VALIDATION.md@6e3413f"
    :response-correlation "SPEC-WORK.md@7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64"
    :resident-window-bound-proposed "SPEC-WORK.md@a0cfcf580d3140ce6c30d51b3a7f884c9b2afcd6"
    :efficiency-policy-proposed "SPEC-WORK.md@685b7c2"
    :production-inventory "ec01647")
  :baseline-features 52
  :baseline-items 171
  :discovered-features 6
  :current-features 57
  :current-acceptance-items 197
  :events (    (      :kind "baseline"
      :feature-count 52
      :reason "Full initial nova-work source survey; implementation not started")
    (      :kind "discovery"
      :feature-count 6
      :reason "PR 269 review found six source-backed inventory gaps; implementation not started")
    (      :kind "remove"
      :feature-count 1
      :reason "E10-F06 moved to the Now register outside the product feature denominator")
    (      :kind "acceptance-discovery"
      :feature "E08-F02"
      :acceptance "pipeline-replies-are-correlated"
      :item-count 1
      :reason "Draft 28 response correlation requires out-of-order response attribution beyond the existing transport bullets")
    (      :kind "acceptance-discovery"
      :feature "E08-F05"
      :acceptance "policy-round-trip-and-replay"
      :item-count 1
      :reason "Proposed PR 317 efficiency policy requires validated policy/trial/execution replay without partial intake")
    (      :kind "acceptance-discovery"
      :feature "E08-F05"
      :acceptance "packet-and-route-gates"
      :item-count 1
      :reason "Proposed PR 317 adds packet bounds and retained economical-route exceptions")
    (      :kind "acceptance-discovery"
      :feature "E08-F03"
      :acceptance "bounds-are-not-prompts"
      :item-count 1
      :reason "Proposed PR 317 distinguishes adapter-enforced execution limits from prompt and wait deadlines")
    (      :kind "acceptance-discovery"
      :feature "E08-F04"
      :acceptance "quiet-until-actionable"
      :item-count 1
      :reason "Proposed PR 317 requires mechanical unchanged handling and urgent bypass of bounded pulses")
    (      :kind "acceptance-discovery"
      :feature "E05-F03"
      :acceptance "reuse-only-valid-review"
      :item-count 1
      :reason "Proposed PR 317 limits review reuse to unchanged scope, revision, acceptance and dependencies")
    (      :kind "acceptance-discovery"
      :feature "E10-F02"
      :acceptance "complete-cost-lineage"
      :item-count 1
      :reason "Proposed PR 317 adds exact parent/child/retry allocation with unknown gaps and non-overlapping counters")
    (      :kind "acceptance-discovery"
      :feature "E08-F02"
      :acceptance "batch-with-bounds-and-urgency"
      :item-count 1
      :reason "Proposed PR 317 adds coordinator record/byte/delay bounds, urgency and retry attribution")
    (      :kind "acceptance-discovery"
      :feature "E10-F02"
      :acceptance "cache-aware-context-choice"
      :item-count 1
      :reason "Proposed PR 317 prices cache categories, rebuilds and tiers before context choice")
    (      :kind "acceptance-discovery"
      :feature "E10-F05"
      :acceptance "evidence-before-adoption"
      :item-count 1
      :reason "Proposed PR 317 bars retrospective or incomplete evidence from automatic adoption")
    (      :kind "acceptance-discovery"
      :feature "E08-F04"
      :acceptance "regression-and-recovery"
      :item-count 1
      :reason "Proposed PR 317 suspends regressed automatic routing and preserves constrained fallback recovery"))
  :open-questions (    "Common Lisp runtime packaging and supported platforms must be pinned before release"
    "Category taxonomy and roadmap completion policy beyond all-required-features remain open"
    "Exact verb and wire protocol spelling must be finalized in one schema before lock"
    "Friend participation in swarms versus model-only pools requires explicit dispositions, including Freddy's"
    "Absorption remains disabled pending independent reconciliation and authorization gates")
  :now (    "Test operational efficiency improvements while building nova-work; count retries, review and rescue, but exclude implementation cost"
    "https://github.com/mas-bandwidth/nova-tools/pull/300"
    "https://github.com/mas-bandwidth/nova-tools/issues/185"
    "PR 300 merged as d21d65f0e6f16a6b3b797d4620d0f24e0c41c296 at 18:25:58 UTC after root clearance, Emma approval emma-aa67291093bc and Freddy's own Mercury APPROVE relayed in rowan-d5a2c0d5f70d; feature rows remain partial with no whole-criterion green"
    "PR 314 merged as 4b99c1b4"
    "PR 311 merged as 14ef63b1 after scoped wording and link gates cleared; Freddy adoption and any measured efficiency saving remain unproven"
    "PR 312 merged as 20f8581 and was adopted by the root at 0921c80; no measured efficacy claim follows"
    "PR 310 remains open at 5fb0feee with Emma owning integration"
    "PR 316 remains open at f8fc40e with its scoped retained-receipt discovery repair cold-clear"
    "PR 317 remains open at 685b7c2 with Rowan/Fable CLEAR rowan-be028c2589e8; Emma approved older a77f370 in emma-a875c5f1e118 and needs the current delta, while Freddy is pending; its proposed replays are mapped here without implementation credit"
    "Issue 185: native Codex usage decoding exists without an operational ingestion path, so no parent-plus-child token saving is claimed"
    "https://github.com/mas-bandwidth/nova-tools/pull/314"
    "https://github.com/mas-bandwidth/nova-tools/pull/311"
    "https://github.com/mas-bandwidth/nova-tools/pull/312"
    "https://github.com/mas-bandwidth/nova-tools/pull/310"
    "https://github.com/mas-bandwidth/nova-tools/pull/316"
    "https://github.com/mas-bandwidth/nova-tools/pull/317"
    "https://github.com/mas-bandwidth/nova-tools/issues/267"
    "https://github.com/mas-bandwidth/nova-tools/issues/239"
    "https://github.com/mas-bandwidth/nova-tools/issues/236"
    "https://github.com/mas-bandwidth/nova-tools/issues/264"
    "https://github.com/mas-bandwidth/nova-tools/pull/270"
    "https://github.com/mas-bandwidth/nova-tools/pull/271")
  :epics (    (      :id "E01"
      :title "Canonical work data and restricted representation"
      :features (        (          :id "E01-F01"
          :title "Restricted Lisp reader and safe syntax"
          :subfeatures (            "Accept only lists, keywords, strings, integers and comments"
            "Reject reader/evaluation macros with byte-offset diagnostics"
            "Never evaluate imported data")
          :depends-on ()
          :source-sections (            "The data"
            "Hostile data and limits")
          :state "partial"
          :evidence ("PR300@d21d65f0: restricted reader and refusal regressions; full production syntax surface remains incomplete; 26/26 subset tests, no full feature verification"))
        (          :id "E01-F02"
          :title "Uniform read bounds and schema validation"
          :subfeatures (            "Require max-bytes, max-depth and max-nodes on every file read"
            "Apply session bounds to snapshots, archives, journals, caches and replay bundles"
            "Preserve unknown keys and refuse unknown node types")
          :depends-on (            "E01-F01")
          :source-sections (            "The data"
            "Hostile data and limits")
          :state "missing"
          :evidence ())
        (          :id "E01-F03"
          :title "Stable node identity and canonical containment"
          :subfeatures (            "Model one stable ID per node and one owning containment parent"
            "Keep containment as a forest and references as a separate graph"
            "Preserve IDs through rename, close, reopen and reparent")
          :depends-on (            "E01-F02")
          :source-sections (            "The data"
            "Engine and representation")
          :state "partial"
          :evidence ("PR300@d21d65f0: event identity and parent-cycle refusal; full canonical node lifecycle remains incomplete; 26/26 subset tests, no full feature verification"))
        (          :id "E01-F04"
          :title "Typed work kinds and acceptance schema"
          :subfeatures (            "Represent work-set, feature, roadmap, task, lease and event kinds"
            "Represent leaf tasks separately from parent tasks and attempts"
            "Validate acceptance kind, subject, predicate and required flag")
          :depends-on (            "E01-F03")
          :source-sections (            "The data")
          :state "missing"
          :evidence ())
        (          :id "E01-F05"
          :title "Canonical encoding and semantic round trip"
          :subfeatures (            "Produce deterministic bytes with explicit absent/empty and Unicode semantics"
            "Preserve large integers, timestamps, escaping and multiline text"
            "Compare exports with an independent semantic comparator")
          :depends-on (            "E01-F01"
            "E01-F04")
          :source-sections (            "Format determinism"
            "Full data round trip")
          :state "partial"
          :evidence ("PR300@d21d65f0: supported kernel subset encoding/reconstruction; full state round trip remains incomplete; 26/26 subset tests, no full feature verification"))))
    (      :id "E02"
      :title "Resident coordinator session and fencing"
      :features (        (          :id "E02-F01"
          :title "Resident O/COW session lifecycle"
          :subfeatures (            "Load one resident O with structure and event log"
            "Keep C as closed history and W as a predicate inside O"
            "Make session start the launcher and expose status")
          :depends-on (            "E01-F03")
          :source-sections (            "The execution model"
            "The root is COW"
            "Engine and representation")
          :state "missing"
          :evidence ())
        (          :id "E02-F02"
          :title "Single coordinator ownership record"
          :subfeatures (            "Persist owner name, generation, random token, stamp and expiry"
            "Allow take and resume with generation rules; successor handoff belongs to E02-F05"
            "Refuse competing live owners and name holder details"
            "Verify generation fencing under partitions and clock skew; reject unsafe takeover rather than relying on PID locks alone")
          :depends-on (            "E02-F01")
          :source-sections (            "The execution model"
            "One coordinator, one live reader/writer")
          :state "missing"
          :evidence ())
        (          :id "E02-F03"
          :title "Journal and endpoint locking"
          :subfeatures (            "Lock canonical journal path for the full process lifetime"
            "Lock filesystem socket or use Windows first-pipe-instance semantics"
            "Never unlink another live process lock or endpoint")
          :depends-on (            "E02-F02")
          :source-sections (            "The execution model")
          :state "missing"
          :evidence ())
        (          :id "E02-F04"
          :title "Lease expiry, reconfirmation and self-fencing"
          :subfeatures (            "Check base tip and owner token before every write"
            "Admit each request against until and fence on expiry or divergence"
            "Permit only status/export while fenced and preserve journal work")
          :depends-on (            "E02-F02"
            "E02-F03")
          :source-sections (            "The execution model"
            "Single writer")
          :state "missing"
          :evidence ())
        (          :id "E02-F05"
          :title "Handoff, stop and successor recovery"
          :subfeatures (            "Clip and publish successor or released owner record atomically"
            "Let named successor take next generation without expiry wait"
            "Recover fenced accepted events through export and replay")
          :depends-on (            "E02-F04"
            "E06-F03")
          :source-sections (            "The execution model"
            "Checkpoints, undo and redo")
          :state "missing"
          :evidence ())
        (          :id "E02-F06"
          :title "Runtime packaging and first-run installation"
          :subfeatures (            "Pin Common Lisp implementation, Go client and supported OS/runtime combinations"
            "Version client/engine/protocol explicitly and refuse unsupported combinations"
            "Provide reproducible build/install and small startup/status/shutdown smoke tests")
          :depends-on (            "E02-F01"
            "E08-F02")
          :source-sections (            "Engine and representation"
            "The execution model")
          :state "missing"
          :evidence ())
        (          :id "E02-F07"
          :title "Execution leases and worker ownership"
          :subfeatures (            "Keep execution leases distinct from the coordinator ownership lease, with at most one live lease per node"
            "Support take, heartbeat, holder-only release, handed release, one extension and explicit escalation with deadline/default fields"
            "Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now"
            "Preserve lease state through reassignment, correction, pause, stop and recovery without counting an attempt twice")
          :depends-on (            "E02-F04")
          :source-sections (            "The data"
            "The execution model"
            "Operational lessons the pilot must exercise")
          :state "missing"
          :evidence ())))
    (      :id "E03"
      :title "Typed mutation and work lifecycle"
      :features (        (          :id "E03-F01"
          :title "Atomic request envelopes and idempotency"
          :subfeatures (            "Assign stable request IDs and durable event envelopes"
            "Validate multi-node changes all-or-none"
            "Refuse repeated IDs or changed payload digests")
          :depends-on (            "E01-F05"
            "E02-F04")
          :source-sections (            "The data"
            "The verbs"
            "Retry/protocol")
          :state "partial"
          :evidence ("PR300@d21d65f0: internal two-event envelope and fake-journal ordering; durable journal/transport dedup remains incomplete; 26/26 subset tests, no full feature verification"))
        (          :id "E03-F02"
          :title "Structure verbs and decomposition"
          :subfeatures (            "Support add, metadata edit, move/reparent, decompose, link/unlink and retire"
            "Record structural event fields in verb-defined order"
            "Preserve decomposition as scope change rather than completion")
          :depends-on (            "E03-F01")
          :source-sections (            "The data"
            "The verbs"
            "Inventory before implementation")
          :state "missing"
          :evidence ())
        (          :id "E03-F03"
          :title "Scope, baseline and dependency changes"
          :subfeatures (            "Support baseline, discovery, require, dependency add/remove and prioritize"
            "Record author, reason, scope revision and exact member delta"
            "Keep shared prerequisites singly owned and referenced")
          :depends-on (            "E03-F02")
          :source-sections (            "Counting"
            "The data"
            "Operational lessons the pilot must exercise")
          :state "missing"
          :evidence ())
        (          :id "E03-F04"
          :title "State correction and completion transitions"
          :subfeatures (            "Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded"
            "Bump task generation on correction and invalidate older evidence"
            "Keep reopen, pause and stop as durable events")
          :depends-on (            "E03-F03"
            "E05-F01")
          :source-sections (            "The data"
            "The validator"
            "Required coordinator operations")
          :state "missing"
          :evidence ())
        (          :id "E03-F05"
          :title "Reversible undo and redo plans"
          :subfeatures (            "Create typed compensating envelopes from preimages"
            "Check expected revisions, descendants and dependencies before apply"
            "Preserve original history and refuse irreversible or uncertain effects")
          :depends-on (            "E03-F04"
            "E06-F03")
          :source-sections (            "Checkpoints, undo and redo"
            "Undo/redo")
          :state "missing"
          :evidence ())))
    (      :id "E04"
      :title "Counting, indexes and bounded queries"
      :features (        (          :id "E04-F01"
          :title "Incremental open counters and grains"
          :subfeatures (            "Maintain root open-item counters in mutation envelopes"
            "Count canonical IDs once and exclude references, attempts and history"
            "Label features, leaves and member grains with revision")
          :depends-on (            "E03-F01"
            "E01-F03")
          :source-sections (            "Counting"
            "Counts are read directly")
          :state "partial"
          :evidence ("PR300@d21d65f0: write-maintained root/container open counts; full grains and roadmap counters remain incomplete; 26/26 subset tests, no full feature verification"))
        (          :id "E04-F02"
          :title "Roadmap percent and cell progress"
          :subfeatures (            "Compute green feature cells over applicable rows per axis member"
            "Print green, applicable, rows and baseline-rows"
            "Print completed required leaves as k/n and never average percentages")
          :depends-on (            "E04-F01"
            "E07-F01")
          :source-sections (            "Counting"
            "A cell is a reference, not another state store")
          :state "missing"
          :evidence ())
        (          :id "E04-F03"
          :title "Scope movement and branch counts"
          :subfeatures (            "Keep baseline, discovery, deferred, cancelled and superseded distinctions"
            "Report open plus closed totals without double membership"
            "Report closed-in, settles-in and revives-in for windows")
          :depends-on (            "E03-F03"
            "E04-F01")
          :source-sections (            "Counting")
          :state "missing"
          :evidence ())
        (          :id "E04-F04"
          :title "Stable resident indexes and bounded access"
          :subfeatures (            "Build and incrementally update indexes for IDs, containment, dependencies and repositories"
            "Provide bounded focus, subtree, category, ready and blocker queries"
            "Avoid materialized transitive descendant sets and unbounded scans"
            "Maintain eager W membership, its counter and per-friend reverse indexes in the same mutation envelope; normal reads never rebuild W lazily")
          :depends-on (            "E04-F01"
            "E01-F03")
          :source-sections (            "The data"
            "Counting"
            "Queries — the contract"
            "W is an eagerly maintained working index")
          :state "missing"
          :evidence ())
        (          :id "E04-F05"
          :title "Historical indexed queries and coverage honesty"
          :subfeatures (            "Resolve settled dependencies and closed rows through indexed lookup"
            "Distinguish absent days, missing segments and unavailable as-of partitions"
            "Return gap, provenance or refusal rather than fabricate state")
          :depends-on (            "E04-F04"
            "E06-F02")
          :source-sections (            "The execution model — retention"
            "Queries — the contract"
            "Old history")
          :state "missing"
          :evidence ())
        (          :id "E04-F06"
          :title "As-of reconstruction over O"
          :subfeatures (            "Answer --at <revision> over O by replaying retained events in memory"
            "Include the replay in revision-labelled counts and refuse before the retention boundary"
            "Keep historical O answers separate from indexed C as-of queries and report unavailable history honestly")
          :depends-on (            "E04-F04"
            "E03-F01")
          :source-sections (            "Queries — the contract"
            "The execution model — retention"
            "Counting")
          :state "missing"
          :evidence ())))
    (      :id "E05"
      :title "Evidence, verification and acceptance proof"
      :features (        (          :id "E05-F01"
          :title "Evidence events and generation guards"
          :subfeatures (            "Bind pointer, criterion, against revision and generation to evidence"
            "Require matching criterion kind, exact subject and predicate"
            "Make correction generation invalidate prior qualification")
          :depends-on (            "E01-F04"
            "E03-F01")
          :source-sections (            "The data"
            "The validator")
          :state "missing"
          :evidence ())
        (          :id "E05-F02"
          :title "Verification and stale evidence reporting"
          :subfeatures (            "Verify every evidence event, including those not named by done"
            "Report stale, freshest and done-unverified facts"
            "Keep test, job, merged and attested proof distinct")
          :depends-on (            "E05-F01")
          :source-sections (            "The validator"
            "Evidence and the imported starting point")
          :state "missing"
          :evidence ())
        (          :id "E05-F03"
          :title "Reviews, findings and attestations"
          :subfeatures (            "Record exact-revision reviewer findings and author dispositions"
            "Support attested criteria with reviewer identity and result"
            "Preserve disagreement, unknowns and repair cycles"
            "reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers")
          :depends-on (            "E05-F01")
          :source-sections (            "Operational lessons the pilot must exercise"
            "The data"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E05-F04"
          :title "Dependency and release gates"
          :subfeatures (            "Require prerequisite dependencies and acceptance before green"
            "Keep merged fix, verified behavior and published distribution separate"
            "Resolve release version through the release task reference")
          :depends-on (            "E05-F02"
            "E04-F04")
          :source-sections (            "The validator"
            "A cell is a reference, not another state store")
          :state "missing"
          :evidence ())
        (          :id "E05-F05"
          :title "Independent proof and mutation regression checks"
          :subfeatures (            "Use independent oracle/comparator and deliberately broken assertions"
            "Retain fixtures, revisions, fault points and expected/actual reconciliation"
            "Reject green claims unsupported by criterion-level proof")
          :depends-on (            "E05-F02")
          :source-sections (            "Independent oracle and retained evidence"
            "Roadmap proof")
          :state "missing"
          :evidence ())
        (          :id "E05-F06"
          :title "Evidence resolvers and verification cache"
          :subfeatures (            "Resolve configured pointer schemes by direct executable invocation with fact/stamp arguments and no shell"
            "Enforce max-fetch, fetch-timeout and offline behavior; an absent resolver is unreachable"
            "Cache by pointer, subject and resolver identity, pin resolver identity on snapshot reads, and preserve unknown results")
          :depends-on (            "E05-F01")
          :source-sections (            "The validator"
            "The resident session"
            "Required test suites")
          :state "missing"
          :evidence ())))
    (      :id "E06"
      :title "Persistence, closed history and recovery"
      :features (        (          :id "E06-F01"
          :title "Durable journal and local checkpoints"
          :subfeatures (            "Journal every accepted mutation before acknowledgment"
            "Create validated snapshots with schema, revision, boundary and manifest"
            "Expose local/shared revision, age, unshared work and failed backup"
            "Keep prior verified snapshots and protect accepted journal history through retention/compaction")
          :depends-on (            "E01-F05"
            "E03-F01")
          :source-sections (            "Checkpoints, undo and redo"
            "The resident session")
          :state "missing"
          :evidence ())
        (          :id "E06-F02"
          :title "Bounded C partitions and historical indexes"
          :subfeatures (            "Partition closure events by their recorded UTC day in immutable bounded segments"
            "Publish manifests, closed rows and dedup/closed index roots in bounded pages"
            "Maintain rolling 24-hour default window with at most two day partitions; pending SPEC-WORK.md@a0cfcf5 correction adds noon/midnight/over-limit witnesses while explicit historical queries remain bounded")
          :depends-on (            "E06-F01"
            "E03-F04")
          :source-sections (            "The execution model — retention"
            "The data"
            "Old history"
            "Resident-window correction (proposed SPEC-WORK.md@a0cfcf5)")
          :state "missing"
          :evidence ())
        (          :id "E06-F03"
          :title "Clip, commit and recovery replay"
          :subfeatures (            "Publish snapshot, segments, manifests and both index roots in one revision"
            "Verify hashes and never expose a root without referenced files"
            "Reload snapshot, indexes and journal overlays after crash")
          :depends-on (            "E06-F02"
            "E02-F04")
          :source-sections (            "The execution model — retention"
            "Checkpoints, undo and redo")
          :state "missing"
          :evidence ())
        (          :id "E06-F04"
          :title "Isolated restore and compare"
          :subfeatures (            "Restore read-only or isolated without ownership, dispatch or side effects"
            "Compare prior checkpoint to current state and report gaps"
            "Keep originals and prior known-good checkpoints during replacement")
          :depends-on (            "E06-F03")
          :source-sections (            "Checkpoints, undo and redo"
            "Recovery")
          :state "missing"
          :evidence ())
        (          :id "E06-F05"
          :title "Lossless export and schema migration"
          :subfeatures (            "Export selected full history beyond resident window with declared scope"
            "Migrate supported old schemas semantically and refuse unsupported versions"
            "Retain source originals, provenance and unresolved records")
          :depends-on (            "E06-F03"
            "E01-F05")
          :source-sections (            "Lossless migration and round-trip release gates"
            "Schema evolution"
            "Old history")
          :state "missing"
          :evidence ())))
    (      :id "E07"
      :title "Roadmap views and Fixed Tables pilot parity"
      :features (        (          :id "E07-F01"
          :title "Typed roadmap axes and cell references"
          :subfeatures (            "Represent ordered axes and coordinate-to-node references"
            "Refuse unknown axis members and duplicate coordinates"
            "Treat missing cells and out-of-scope cells distinctly"
            "Preserve epic -> feature -> recursive sub-feature hierarchy without mandatory axis or cell wrappers"
            "Retain completed roadmap members and historical code/test evidence beyond the active 24-hour window")
          :depends-on (            "E01-F04"
            "E03-F03")
          :source-sections (            "The data"
            "A cell is a reference, not another state store")
          :state "missing"
          :evidence ())
        (          :id "E07-F02"
          :title "Completion-only projection renderer"
          :subfeatures (            "Render tick only for fully verified cells and cross otherwise"
            "Keep partial, missing and unknown state in S/check counts"
            "Write only the marked roadmap region and preserve unrelated bytes")
          :depends-on (            "E05-F02"
            "E07-F01")
          :source-sections (            "Roadmap as a view; the Schema pilot"
            "What the current prototype proves, and does not")
          :state "missing"
          :evidence ())
        (          :id "E07-F03"
          :title "Generated ROADMAP drift checks"
          :subfeatures (            "Regenerate ROADMAP from primary O data"
            "Make render --check fail on drift or ambiguous markers"
            "Ensure chat/file projections use the same captured revision")
          :depends-on (            "E07-F02"
            "E06-F03")
          :source-sections (            "The failures it closes"
            "Roadmap as a view; the Schema pilot"
            "Roadmap proof")
          :state "missing"
          :evidence ())
        (          :id "E07-F04"
          :title "Fixed Tables imported baseline inventory"
          :subfeatures (            "Preserve source revision, audit IDs, reports and aliases"
            "Include ordinary delivered capabilities beside versioning rows"
            "Keep source support separate from qualified acceptance and incomplete reconciliation visible")
          :depends-on (            "E06-F05"
            "E07-F01")
          :source-sections (            "Evidence and the imported starting point"
            "Inventory before implementation")
          :state "missing"
          :evidence ())
        (          :id "E07-F05"
          :title "Pilot retrospective and scope evolution"
          :subfeatures (            "Exercise completed cell, new discovery, dependency/handoff and changed focus"
            "Record failures, repairs, denominator movement and changed scope"
            "Feed findings into NEXT-TOOLS and production specs before implementation")
          :depends-on (            "E07-F04"
            "E05-F05")
          :source-sections (            "Retrospective required before production implementation"
            "Operational lessons the pilot must exercise")
          :state "missing"
          :evidence ())
        (          :id "E07-F06"
          :title "Private-node projection filtering"
          :subfeatures (            "Omit private nodes and descendants from rendered output files"
            "When a public view reaches private work through a parent, print only the private count"
            "Preserve private state in O and validation while applying filtering only at projection boundaries")
          :depends-on (            "E07-F02")
          :source-sections (            "The data"
            "Output grammar"
            "A cell is a reference, not another state store")
          :state "missing"
          :evidence ())))
    (      :id "E08"
      :title "Coordinator protocol, friends and execution records"
      :features (        (          :id "E08-F01"
          :title "Versioned typed command schema and discovery"
          :subfeatures (            "Use one schema for client validation, protocol, help and examples"
            "Provide bounded family/verb help and machine discovery with schema hash"
            "Refuse stale discovery and invalid suggested actions"
            "List next valid actions and missing prerequisites without granting authority or executing suggestions")
          :depends-on (            "E03-F01")
          :source-sections (            "The verbs"
            "Learn verbs without carrying the manual in context")
          :state "missing"
          :evidence ())
        (          :id "E08-F02"
          :title "Client transport and asynchronous operations"
          :subfeatures (            "Support framed requests, status, wait, cancel and bounded backpressure"
            "Handle disconnect, lost reply, deadlines and uncertain outcomes"
            "Keep control operations responsive during import/export/clip"
            "Use bounded typed JSON over a local Unix socket, exact integer/time encoding and durable asynchronous operation IDs; reconcile cross-platform endpoint requirements before lock"
            "Support bounded read bundles at one captured revision and lease-time watermark, with stable paginated snapshot identity"
            "Support independent ordered batches with per-entry IDs/outcomes and explicit stop/continue semantics, without implying rollback"
            "Support atomic mutation batches with all-or-none validated O/W, counter and reverse-index changes; reject oversized batches without silently splitting"
            "pipeline-replies-are-correlated: correlate out-of-order or fragmented ordinary responses by request ID, retain a distinct durable operation ID, name independent not-attempted and atomic validation entry outcomes, and close on unknown, duplicate, absent or undecodable IDs; same-ID recovery uses E03-F01 durable idempotency"
            "batch-with-bounds-and-urgency: coalesce independent results within byte, record and delay bounds; refuse unreferenced padding, bypass delay for urgent changes and preserve dependency and retry identity")
          :depends-on (            "E08-F01"
            "E02-F04")
          :source-sections (            "The verbs"
            "Async operations"
            "Retry/protocol"
            "Batch-friendly transport and explicit atomicity"
            "Response correlation (SPEC-WORK.md@7db3b95)"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E08-F03"
          :title "Friends, CONFIG and ACTIVE indexes"
          :subfeatures (            "Index every known friend, assignments and working task references"
            "Separate stable capabilities/config from observed active executions"
            "Include coordinator identity and attribute model, bench, attempt and usage"
            "Store agreed specialist roles per friend; keep model strengths/weaknesses in the shared model catalog"
            "Represent children, swarm capabilities, local runs and one-shots separately from friend identity; agreed concurrency limits apply"
            "bounds-are-not-prompts: refuse automatic dispatch when an adapter cannot enforce the configured execution limit; distinguish wait deadline from observed stop or unresolved outcome")
          :depends-on (            "E04-F04"
            "E03-F04")
          :source-sections (            "Friends and assignments are resident indexes too"
            "CONFIG and ACTIVE are different sections"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E08-F04"
          :title "Availability, offers and assignment reconciliation"
          :subfeatures (            "Represent explicit rest, unavailable and unconfirmed contact with observation age"
            "Distinguish offer, acknowledgment, ownership, lease and execution"
            "Reconcile uncertain prior attempts before relaunch or reassignment"
            "After the team-configured silence threshold use one bounded availability probe; nonresponse is unconfirmed capacity, never proof of exhausted credits"
            "quiet-until-actionable: keep unchanged traffic mechanical, batch actionable deltas within bounds and let corrections, stops, lease loss and deadlines bypass delay"
            "regression-and-recovery: suspend new automatic routing on a breached trial, retain uncertain live handles and use only eligible role-preserving fallback")
          :depends-on (            "E08-F03"
            "E02-F04")
          :source-sections (            "Observed availability"
            "Friends and assignments are resident indexes too"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E08-F05"
          :title "Bounded config/pricing exchange and model suitability"
          :subfeatures (            "Exchange UNCHANGED manifests or validated deltas by hash/revision"
            "Store model route, pricing, quota and provenance without secrets"
            "Keep requested versus observed model and measured suitability separate"
            "policy-round-trip-and-replay: preserve per-friend efficiency policy, trial manifests and execution references through validated intake, export/import, restart, undo and replay"
            "packet-and-route-gates: refuse oversized or history-disallowed packets and ineligible/stale routes; retain a scoped reason for economical-route exceptions")
          :depends-on (            "E08-F03")
          :source-sections (            "Efficient friend config exchange and token pricing"
            "Model knowledge informs scheduling"
            "Swarms, models and friend participation: decision required"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())))
    (      :id "E09"
      :title "Issue intake, migration and external boundaries"
      :features (        (          :id "E09-F01"
          :title "Non-destructive issue inventory and capture"
          :subfeatures (            "Capture stable provider/repository/issue identity, revision and URL"
            "Preserve body, comments, labels, relationships, attachments and pagination"
            "Keep inaccessible or unsupported fields explicit")
          :depends-on (            "E06-F05")
          :source-sections (            "Public issue correspondence survives intake"
            "Source inventory"
            "Archive completeness")
          :state "missing"
          :evidence ())
        (          :id "E09-F02"
          :title "Link mode and correspondence reconciliation"
          :subfeatures (            "Map one issue to many nodes and repeated updates without duplicates"
            "Keep remote text as data separate from accepted plan and authority"
            "Track pending, confirmed and failed outbound actions with receipts")
          :depends-on (            "E09-F01"
            "E03-F01")
          :source-sections (            "Public issue correspondence survives intake")
          :state "missing"
          :evidence ())
        (          :id "E09-F03"
          :title "Lossless resumable initial migration"
          :subfeatures (            "Inventory authorized sources with capture manifest and disposition"
            "Import in batches with originals, mappings, deduplication and checkpoints"
            "Reconcile counts/content and exercise interruption and source edits")
          :depends-on (            "E09-F01"
            "E06-F03")
          :source-sections (            "Initial migration: preserve first, reconcile, then choose absorption"
            "Import replay"
            "Moving source")
          :state "missing"
          :evidence ())
        (          :id "E09-F04"
          :title "Explicit absorb operation and deletion gate"
          :subfeatures (            "Separate absorb from default link and require selected scope/authority"
            "Archive source identity, provenance and content before removal; append the actual deletion outcome receipt after the attempt"
            "Leave deletion pending on missing content, source change or uncertain network result")
          :depends-on (            "E09-F03"
            "E05-F05")
          :source-sections (            "Link versus absorb"
            "Archive completeness"
            "Lossless migration and round-trip release gates")
          :state "missing"
          :evidence ())
        (          :id "E09-F05"
          :title "External adapter and side-effect safety"
          :subfeatures (            "Keep GitHub issue closure, publishing and source deletion separate from local completion"
            "Do not claim Git/GitHub atomicity or invent authors"
            "Preserve concurrent human changes and uncertain external outcomes")
          :depends-on (            "E09-F02")
          :source-sections (            "Public issue correspondence survives intake"
            "Link versus absorb"
            "Undo/redo")
          :state "missing"
          :evidence ())))
    (      :id "E10"
      :title "Diagnostics, measurement and release gates"
      :features (        (          :id "E10-F01"
          :title "Structured refusal and operation diagnostics"
          :subfeatures (            "Attach stable error code, stage, verb, request/operation ID and known revisions"
            "Distinguish refused, reply lost, running, cancelled and unknown external outcome"
            "Provide bounded inspect/diagnose drill-down without secrets or private bodies")
          :depends-on (            "E08-F02"
            "E03-F01")
          :source-sections (            "Fast failure diagnosis")
          :state "missing"
          :evidence ())
        (          :id "E10-F02"
          :title "Cost, usage and rate accounting"
          :subfeatures (            "Record attempt usage pointers and unresolved usage as unmeasured"
            "Separate billed cash, estimated cash and virtual token cost"
            "Pin rate/config revisions and include coordinator, review and rework overhead"
            "complete-cost-lineage: join parent, child, retry and failed-attempt receipts once; avoid counting cache/reasoning subsets again, implementation cost separate and gaps unknown"
            "cache-aware-context-choice: price cache read/write categories, service tiers and reset rebuilds; refuse missing decision, adapter or evidence inputs and compare matched accepted work")
          :depends-on (            "E08-F05"
            "E03-F04")
          :source-sections (            "Cost"
            "Model knowledge informs scheduling"
            "Retrospective required before production implementation"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E10-F03"
          :title "Generated, golden, property and fault suites"
          :subfeatures (            "Run parser, round-trip, invariant, index/counter and roadmap proof suites"
            "Inject failures at journal, checkpoint, apply, reply and publication boundaries"
            "Map each suite to an owner, command and CI lane without duplicating its acceptance evidence"
            "Keep per-change CI within two minutes, exhaustive fault/scale suites explicit nightly or pre-release; failures block affected gates")
          :depends-on (            "E01-F05"
            "E05-F05"
            "E06-F04")
          :source-sections (            "Required test suites"
            "Staged verification and release")
          :state "missing"
          :evidence ())
        (          :id "E10-F04"
          :title "Process-level recovery and two-writer verification"
          :subfeatures (            "Orchestrate process-level runs against temporary remotes and fake providers, including crash/restart, partition, stale owner and handoff"
            "Collect release-level results from the owning fencing, recovery, paging and as-of features without re-owning their assertions"
            "Run the authorized read-only real-repository pilot and disposable import, then publish a reconciliation disposition")
          :depends-on (            "E02-F05"
            "E06-F04"
            "E09-F03")
          :source-sections (            "Required test suites"
            "Staged verification and release")
          :state "missing"
          :evidence ())
        (          :id "E10-F05"
          :title "Measured Fixed Tables go/no-go gate"
          :subfeatures (            "Compare update-and-render tokens/wall time with manual editing"
            "Test lease-only answer for who is working on C and unsupported number detection"
            "Pin scenarios, owners and commands before implementation; require exact-revision correctness and measured operational results before adoption"
            "evidence-before-adoption: refuse automatic promotion on missing baseline or coverage, unmatched quality or retrospective correlation; require a qualified prospective result")
          :depends-on (            "E07-F05"
            "E10-F02"
            "E10-F03")
          :source-sections (            "The measurement that decides"
            "Agreement and lock gate"
            "Efficiency policy (proposed SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E10-F07"
          :title "Validator and repair modes"
          :subfeatures (            "Validate duplicate IDs, dangling versus unavailable references, dependency cycles, two parents, invalid cells, conflicting leases and in-two-branches"
            "Run whole-state validation at load/clip and candidate-gate validation at every mutation"
            "Support session start --repair only when findings strictly decrease, preserving the unmodified source and emitting a repair diff")
          :depends-on (            "E01-F04"
            "E03-F01")
          :source-sections (            "The validator"
            "The data"
            "Required test suites")
          :state "missing"
          :evidence ())
        (          :id "E10-F08"
          :title "Output grammar and bounded results"
          :subfeatures (            "Define per-verb first tokens, OK/FAIL/RACED/ROW/NOTE/MORE records, stdout/stderr split and exit codes 0/1/2"
            "Emit emitted= on OK lines and pushed= on scope lines; never exceed configured output caps"
            "Enforce --max default 20, zero meaning all, reject negatives, and print MORE with a usable continuation remedy")
          :depends-on (            "E08-F01"
            "E10-F01")
          :source-sections (            "Output grammar"
            "The verbs"
            "Required test suites")
          :state "missing"
          :evidence ())))))
