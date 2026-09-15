; Proposed recursive roadmap data; not an implemented nova-work interchange schema.
(  :schema "nova-work-roadmap-baseline-1"
  :scope-revision 10
  :inventory-status "proposed baseline; awaiting friend review"
  :sources (    :main-spec "SPEC-WORK.md@f36b85850620504a74e1229043c7cee2c14ea594"
    :companion "SPEC-WORK-PILOT.md@6e3413f"
    :companion-eager-w "SPEC-WORK-PILOT.md@4e800fb"
    :companion-batch-transport "SPEC-WORK-PILOT.md@bc4a4a4"
    :validation "SPEC-WORK-VALIDATION.md@6e3413f"
    :response-correlation "SPEC-WORK.md@7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64"
    :resident-window-bound-proposed "SPEC-WORK.md@a0cfcf580d3140ce6c30d51b3a7f884c9b2afcd6"
    :efficiency-policy "SPEC-WORK.md@685b7c2"
    :recursive-grouping "SPEC-WORK.md@9488a19"
    :recursive-grouping-integration "spec/nova-work@9c120a3b0c1b01475e13cb7bd36014ada2dfa729"
    :v2-recursive-node-proposed "https://github.com/mas-bandwidth/nova-tools/issues/321@2026-09-14T20:47:32Z"
    :v2-recursive-node-body-sha256 "138fd865383982f2729484eadb4edeefeb448cd0eb7c56716bd118858a42e0e6"
    :rd-herdr-idea "https://github.com/mas-bandwidth/nova-tools/issues/336"
    :v2-delegation-notes "https://github.com/mas-bandwidth/nova-tools/pull/335"
        :pr-300 "https://github.com/mas-bandwidth/nova-tools/pull/300"
    :pr-310 "https://github.com/mas-bandwidth/nova-tools/pull/310"
    :pr-311 "https://github.com/mas-bandwidth/nova-tools/pull/311"
    :pr-312 "https://github.com/mas-bandwidth/nova-tools/pull/312"
    :pr-314 "https://github.com/mas-bandwidth/nova-tools/pull/314"
    :pr-316 "https://github.com/mas-bandwidth/nova-tools/pull/316"
    :pr-317 "https://github.com/mas-bandwidth/nova-tools/pull/317"
    :pr-319 "https://github.com/mas-bandwidth/nova-tools/pull/319"
    :pr-320 "https://github.com/mas-bandwidth/nova-tools/pull/320"
    :pr-322 "https://github.com/mas-bandwidth/nova-tools/pull/322"
    :pr-323 "https://github.com/mas-bandwidth/nova-tools/pull/323"
    :pr-325 "https://github.com/mas-bandwidth/nova-tools/pull/325"
    :pr-326 "https://github.com/mas-bandwidth/nova-tools/pull/326"
    :pr-330 "https://github.com/mas-bandwidth/nova-tools/pull/330"
    :pr-331 "https://github.com/mas-bandwidth/nova-tools/pull/331"
    :pr-332 "https://github.com/mas-bandwidth/nova-tools/pull/332"
    :pr-333 "https://github.com/mas-bandwidth/nova-tools/pull/333"
    :pr-334 "https://github.com/mas-bandwidth/nova-tools/pull/334"
    :pr-335 "https://github.com/mas-bandwidth/nova-tools/pull/335"
    :prs-293-295 "https://github.com/mas-bandwidth/nova-tools/pull/293"
    :issue-185 "https://github.com/mas-bandwidth/nova-tools/issues/185"
    :issue-321 "https://github.com/mas-bandwidth/nova-tools/issues/321"
    :issue-321-c5672006742 "https://github.com/mas-bandwidth/nova-tools/issues/321#issuecomment-5672006742"
    :issue-336 "https://github.com/mas-bandwidth/nova-tools/issues/336"
    :pr-338 "https://github.com/mas-bandwidth/nova-tools/pull/338"
    :pr-339 "https://github.com/mas-bandwidth/nova-tools/pull/339"
    :fleet "spec/nova-work PR 339, docs/SPEC-WORK.md section The fleet"
    :efficiency-lessons "spec/nova-work, docs/SPEC-WORK.md section Efficiency: lessons absorbed 2026-09-15"
    :pr-337 "https://github.com/mas-bandwidth/nova-tools/pull/337"
    :commit-248d85b "https://github.com/mas-bandwidth/nova-tools/commit/248d85b320923803a82f1f643e303bbf42d2c74a"
    :pr-340 "https://github.com/mas-bandwidth/nova-tools/pull/340"
    :pr-342 "https://github.com/mas-bandwidth/nova-tools/pull/342"
    :pr-231 "https://github.com/mas-bandwidth/nova-tools/pull/231"
    :ideas-780 "https://github.com/mas-bandwidth/ideas/issues/780"
    :production-inventory "ec01647")
  :baseline-features 52
  :baseline-items 171
  :discovered-features 6
  :current-features 57
  :current-acceptance-items 204
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
      :reason "PR 317 efficiency policy requires validated policy/trial/execution replay without partial intake")
    (      :kind "acceptance-discovery"
      :feature "E08-F05"
      :acceptance "packet-and-route-gates"
      :item-count 1
      :reason "PR 317 adds packet bounds and retained economical-route exceptions")
    (      :kind "acceptance-discovery"
      :feature "E08-F03"
      :acceptance "bounds-are-not-prompts"
      :item-count 1
      :reason "PR 317 distinguishes adapter-enforced execution limits from prompt and wait deadlines")
    (      :kind "acceptance-discovery"
      :feature "E08-F04"
      :acceptance "quiet-until-actionable"
      :item-count 1
      :reason "PR 317 requires mechanical unchanged handling and urgent bypass of bounded pulses")
    (      :kind "acceptance-discovery"
      :feature "E05-F03"
      :acceptance "reuse-only-valid-review"
      :item-count 1
      :reason "PR 317 limits review reuse to unchanged scope, revision, acceptance and dependencies")
    (      :kind "acceptance-discovery"
      :feature "E10-F02"
      :acceptance "complete-cost-lineage"
      :item-count 1
      :reason "PR 317 adds exact parent/child/retry allocation with unknown gaps and non-overlapping counters")
    (      :kind "acceptance-discovery"
      :feature "E08-F02"
      :acceptance "batch-with-bounds-and-urgency"
      :item-count 1
      :reason "PR 317 adds coordinator record/byte/delay bounds, urgency and retry attribution")
    (      :kind "acceptance-discovery"
      :feature "E10-F02"
      :acceptance "cache-aware-context-choice"
      :item-count 1
      :reason "PR 317 prices cache categories, rebuilds and tiers before context choice")
    (      :kind "acceptance-discovery"
      :feature "E10-F05"
      :acceptance "evidence-before-adoption"
      :item-count 1
      :reason "PR 317 bars retrospective or incomplete evidence from automatic adoption")
    (      :kind "acceptance-discovery"
      :feature "E08-F04"
      :acceptance "regression-and-recovery"
      :item-count 1
      :reason "PR 317 efficiency policy suspends regressed automatic routing and preserves constrained fallback recovery")
    (      :kind "acceptance-discovery"
      :feature "E01-F03"
      :acceptance "flexible-recursive-grouping-depth"
      :item-count 1
      :reason "Proposed PR 319 permits omitted, repeated and nested work-set grouping layers without prescribing containment depth")
    (      :kind "acceptance-discovery"
      :feature "E01-F04"
      :acceptance "project-stream-work-set-categories"
      :item-count 1
      :reason "Proposed PR 319 represents project and stream groupings as work-set categories rather than inferred kinds")
    (      :kind "acceptance-discovery"
      :feature "E03-F04"
      :acceptance "recursive-container-settle-reopen"
      :item-count 1
      :reason "Proposed PR 319 requires settle and reopen behavior through required-member ancestors at every grouping depth")
    (      :kind "acceptance-discovery"
      :feature "E08-F03"
      :acceptance "fleet-is-static-config"
      :item-count 1
      :reason "Proposed PR 339 adds the fleet to CONFIG as static instance data with one verb and two asks; a recommendation is never a lease")
    (      :kind "acceptance-discovery"
      :feature "E10-F02"
      :acceptance "gas-town-efficiency-accounting"
      :item-count 1
      :reason "Absorb Gas Town token facts: root-only step records, inline checklist steps, and durable next-trigger timing")
    (      :kind "acceptance-discovery"
      :feature "E10-F05"
      :acceptance "efficiency-lessons-gate"
      :item-count 1
      :reason "Absorb Gas Town efficiency contract: prime projection, decompose --pour, tripped node reason, and delegate mode")
    (      :kind "organization"
      :feature-count 0
      :reason "Organize active work outside v1 denominator into explicit R&D register; preserve 57 features and 200 items")
    (      :kind "v2-discovery"
      :feature-count 0
      :reason "Organize Future Plans (v2) epic with 5 mapped feature areas and 14 acceptance items outside v1 denominator"))
  :future-plans-v2 (    :id "V2"
    :title "Future Plans (v2)"
    :state "proposed"
    :outside-v1-denominator "yes"
    :feature-count 5
    :acceptance-item-count 14
    :features (
      (        :id "V2-F01"
        :extends "E08-F03"
        :title "Friends, CONFIG and ACTIVE indexes"
        :state "proposed"
        :items (
          (            :id "V2-F01-01"
            :title "Keep node membership, human/AI coordinator composition, real or virtual organizational role and transport backing separate, with stable node and actor identities"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F01-02"
            :title "Exercise all-real company-style, all-virtual and mixed real/virtual branches at arbitrary finite depths without fixed company/team/person levels; exceeding declared bounds refuses visibly"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F01-03"
            :title "Keep one coordinating parent distinct from work containment and cross-branch references; record membership, parent and ownership transitions without duplicating work or spend"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")))
      (        :id "V2-F02"
        :extends "E08-F04"
        :title "Availability, offers and assignment reconciliation"
        :state "proposed"
        :items (
          (            :id "V2-F02-01"
            :title "Apply the same offer, acceptance/deferral/refusal, local-work/delegation and evidence-return contract at every depth, including a root originating work and a leaf finishing locally"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F02-02"
            :title "Integrate local and child results against parent acceptance; preserve partial failures, independent reviews, unresolved live handles and explicit pause/cancel/correction/reopen behavior"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F02-03"
            :title "Return actor/model/bench/attempt usage through every parent mapping, counting local work, descendants, retry, integration, review and rescue once while missing usage remains unknown"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")))
      (        :id "V2-F03"
        :extends "E09-F02"
        :title "Link mode and correspondence reconciliation"
        :state "proposed"
        :items (
          (            :id "V2-F03-01"
            :title "Support repository-backed virtual nodes using GitHub Issues and Discussions with optional correlated email; keep shared-project repository layout explicit and duplicate or delayed notices separate from acceptance"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F03-02"
            :title "Retain delegating/receiving node, offer/assignment, local work, project/repository, Issue and linked Discussion identities and revisions; distinguish GitHub assignees from receiving nodes through regrouping, delegation and transfers"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F03-03"
            :title "Record a virtual parent creating and assigning an Issue as a locally initiated offer, then accept it through the same contract without inventing an independent human request"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")))
      (        :id "V2-F04"
        :extends "E09-F05"
        :title "External adapter and side-effect safety"
        :state "proposed"
        :items (
          (            :id "V2-F04-01"
            :title "After agreed completion and review, close the correctly mapped Issue and post or update one correlated Discussion completion summary with evidence; a child alone cannot close a group Issue and a summary does not imply Discussion closure or accepted answer"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F04-02"
            :title "Persist the intended upstream operation before delivery and retain acknowledgment; local accepted completion with failed or interrupted GitHub delivery remains visibly pending and reconciles after restart without duplicate comments or closures"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")
          (            :id "V2-F04-03"
            :title "Preserve mappings and resolve contradictory upstream reopen, reassignment or transfer while updates are pending; an externally closed Issue does not prove local acceptance, and a future native backing preserves unresolved work and evidence"
            :status "proposed"
            :owner "unassigned"
            :next-gate "v2-spec"
            :source "issue-321")))
      (        :id "V2-F05"
        :extends "E08-F01"
        :title "Coordinator notes and shared cross-model current goal operations"
        :state "proposed"
        :items (
          (            :id "V2-F05-01"
            :title "Personal and shared coordinator notes retain author, conversational source, applicability, revisions and routing constraints; load relevant notes when models or harnesses change"
            :status "proposed"
            :owner "rowan"
            :next-gate "goal-contract"
            :source "issue-321-c5672006742")
          (            :id "V2-F05-02"
            :title "Set, retrieve and update a shared current goal across models and harnesses in the canonical work set, preserving stable identity, revision-aware edits, ownership, stop state, progress and completion evidence; native harness goals are synchronized views rather than independent ledgers"
            :status "proposed"
            :owner "rowan"
            :next-gate "goal-contract"
            :source "issue-321-c5672006742")))))
  :open-questions (    "Common Lisp runtime packaging and supported platforms must be pinned before release"
    "Category taxonomy and roadmap completion policy beyond all-required-features remain open"
    "Exact verb and wire protocol spelling must be finalized in one schema before lock"
    "Friend participation in swarms versus model-only pools requires explicit dispositions, including Freddy's"
    "Absorption remains disabled pending independent reconciliation and authorization gates")
  :rd-snapshot-at "2026-09-15T01:40:00Z"
  :rd-source-main "16a1d47949f47724284b46b40457e14aae58bd1b"
  :rd-source-spec "efb6dbe78d99603e26180fa5160fff67aa95e181"
  :rd-work (
    (      :id "RD-01"
      :title "PR #300: C/O transition kernel with write-maintained open count"
      :status "merged"
      :owner "rowan"
      :next-gate "integrated"
      :source "pr-300")
    (      :id "RD-02"
      :title "PR #310: PR264 profile migration to eight-member config preimage, execution and control schemas"
      :status "merged"
      :owner "unassigned"
      :next-gate "parent-gate"
      :source "pr-310")
    (      :id "RD-03"
      :title "PR #311: Worker cards: practices and their evidence"
      :status "merged"
      :owner "rowan"
      :next-gate "integrated"
      :source "pr-311")
    (      :id "RD-04"
      :title "PR #312: nova-bus reply transaction: draft a reply without a hand-built header"
      :status "merged"
      :owner "rowan"
      :next-gate "integrated"
      :source "pr-312")
    (      :id "RD-05"
      :title "PR #314: nova-work no-effect events, dry-run and container-count specification"
      :status "merged"
      :owner "rowan"
      :next-gate "integrated"
      :source "pr-314")
    (      :id "RD-06"
      :title "PR #316: Receipt recovery ordering, attribution selection and claim locking"
      :status "merged"
      :owner "unassigned"
      :next-gate "parent-gate"
      :source "pr-316")
    (      :id "RD-07"
      :title "PR #317: Efficiency policy, cache-aware costs and batching"
      :status "merged"
      :owner "unassigned"
      :next-gate "implementation-gate"
      :source "pr-317")
    (      :id "RD-08"
      :title "PR #319: Recursive project and tool-stream grouping"
      :status "merged"
      :owner "unassigned"
      :next-gate "implementation-gate"
      :source "pr-319")
    (      :id "RD-09"
      :title "PR #320: Refuse forbidden Lisp reader tokens before interning"
      :status "merged"
      :owner "unassigned"
      :next-gate "full-verification"
      :source "pr-320")
    (      :id "RD-10"
      :title "PR #322: Static required-member container cascade"
      :status "merged"
      :owner "unassigned"
      :next-gate "resident-cow-gate"
      :source "pr-322")
    (      :id "RD-11"
      :title "PR #323: Finite records decision packet and Rule 29"
      :status "merged"
      :owner "unassigned"
      :next-gate "validator-slice"
      :source "pr-323")
    (      :id "RD-12"
      :title "PR #325: Swarm migration-status and re-reservation corrections"
      :status "merged"
      :owner "unassigned"
      :next-gate "runtime-gate"
      :source "pr-325")
    (      :id "RD-13"
      :title "PR #326: propose optional native swarm batch admission receipts"
      :status "merged"
      :owner "stella"
      :next-gate "encoding-crash-gates"
      :source "pr-326")
    (      :id "RD-14"
      :title "PR #330: Previous roadmap refresh through 21:57Z"
      :status "merged"
      :owner "unassigned"
      :next-gate "superseded"
      :source "pr-330")
    (      :id "RD-15"
      :title "PR #331: start and restart tasks through kernel submit path"
      :status "merged"
      :owner "stella"
      :next-gate "integrated"
      :source "pr-331")
    (      :id "RD-16"
      :title "PR #332: batch admission wire and recovery companion specification"
      :status "merged"
      :owner "emma"
      :next-gate "implementation-gate"
      :source "pr-342")
    (      :id "RD-17"
      :title "PR #333: bounded durable journal persistence and replay adapter"
      :status "merged"
      :owner "emma"
      :next-gate "integrated"
      :source "pr-333")
    (      :id "RD-18"
      :title "PR #334: Previous roadmap refresh through 23:00:18Z"
      :status "merged"
      :owner "unassigned"
      :next-gate "superseded"
      :source "pr-334")
    (      :id "RD-19"
      :title "PR #231: nova-work spec lock (spec/nova-work merged to main)"
      :status "merged"
      :owner "rowan"
      :next-gate "integrated"
      :source "pr-231")
    (      :id "RD-20"
      :title "Issue #185: token ledger operational comparison, triage, and accounting"
      :status "in-progress"
      :owner "unassigned"
      :next-gate "matched-operational-evidence"
      :source "issue-185")
    (      :id "RD-21"
      :title "Issue #336: evaluate Herdr as an agent runtime adapter"
      :status "parked"
      :owner "unassigned"
      :next-gate "closed-not-planned"
      :source "issue-336")
    (      :id "RD-22"
      :title "PR #338: Slice C: whole-package candidate and installed validator"
      :status "merged"
      :owner "emma"
      :next-gate "integrated"
      :source "pr-338")
    (      :id "RD-23"
      :title "E10-F06: Operational adoption and continuous efficiency review"
      :status "open"
      :owner "rowan"
      :next-gate "owner-assignment"
      :source "commit-248d85b")
    (      :id "RD-24"
      :title "PR #339: the fleet, a static description of the machines in the nova-work config"
      :status "merged"
      :owner "unassigned"
      :next-gate "implementation-gate"
      :source "pr-339")
    (      :id "RD-25"
      :title "PR #340: fold the current goal into SPEC-WORK (goal set/show/update, six replays)"
      :status "merged"
      :owner "unassigned"
      :next-gate "implementation-gate"
      :source "pr-340")
    (      :id "RD-26"
      :title "ideas#780: MCP front vs thin client protocol comparison"
      :status "proposed"
      :owner "unassigned"
      :next-gate "proposal-intake"
      :source "ideas-780")
    (      :id "RD-27"
      :title "ideas#780: escalation with a recorded reason"
      :status "proposed"
      :owner "unassigned"
      :next-gate "proposal-intake"
      :source "ideas-780")
    (      :id "RD-28"
      :title "ideas#780: fan-out width before synthesis eats the saving"
      :status "proposed"
      :owner "unassigned"
      :next-gate "proposal-intake"
      :source "ideas-780"))
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
            "Preserve IDs through rename, close, reopen and reparent"
            "Allow omitted, repeated and recursively nested work-set grouping layers without a prescribed depth")
          :depends-on (            "E01-F02")
          :source-sections (            "The data"
            "Engine and representation"
            "Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b)")
          :state "partial"
          :evidence ("PR300@d21d65f0: event identity and parent-cycle refusal; full canonical node lifecycle remains incomplete; 26/26 subset tests, no full feature verification"))
        (          :id "E01-F04"
          :title "Typed work kinds and acceptance schema"
          :subfeatures (            "Represent work-set, feature, roadmap, task, lease and event kinds"
            "Represent leaf tasks separately from parent tasks and attempts"
            "Validate acceptance kind, subject, predicate and required flag"
            "Represent project and stream groupings as work-set categories without inferring kind from title or position")
          :depends-on (            "E01-F03")
          :source-sections (            "The data"
            "Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b)")
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
            "Keep reopen, pause and stop as durable events"
            "Settle and reopen required-member ancestors at every grouping depth while empty required sets remain incomplete")
          :depends-on (            "E03-F03"
            "E05-F01")
          :source-sections (            "The data"
            "The validator"
            "Required coordinator operations"
            "Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b)")
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
            "Efficiency policy (SPEC-WORK.md@685b7c2)")
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
            "Preserve selected scope and completion unit across recursive grouping layouts without mandatory axis or cell wrappers"
            "Retain completed roadmap members and historical code/test evidence beyond the active 24-hour window")
          :depends-on (            "E01-F04"
            "E03-F03")
          :source-sections (            "The data"
            "A cell is a reference, not another state store"
            "Recursive structure within a repository (SPEC-WORK.md@9488a19; merged by PR319@9c120a3b)")
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
            "Efficiency policy (SPEC-WORK.md@685b7c2)")
          :state "missing"
          :evidence ())
        (          :id "E08-F03"
          :title "Friends, CONFIG and ACTIVE indexes"
          :subfeatures (            "Index every known friend, assignments and working task references"
            "Separate stable capabilities/config from observed active executions"
            "Include coordinator identity and attribute model, bench, attempt and usage"
            "Store agreed specialist roles per friend; keep model strengths/weaknesses in the shared model catalog"
            "Represent children, swarm capabilities, local runs and one-shots separately from friend identity; agreed concurrency limits apply"
            "bounds-are-not-prompts: refuse automatic dispatch when an adapter cannot enforce the configured execution limit; distinguish wait deadline from observed stop or unresolved outcome"
            "fleet-is-static-config: hold the fleet as :machine records in CONFIG with stable id, owner, connection-profile reference, roles, limits, permits, exclusions and dated declared facts, all instance data; list it and recommend members for a workload kind from declared facts, never a lease; refuse a record without owner or id, a held connection profile, a credential, an unknown owner or role, and a choice of a member for a workload it excludes")
          :depends-on (            "E04-F04"
            "E03-F04")
          :source-sections (            "Friends and assignments are resident indexes too"
            "CONFIG and ACTIVE are different sections"
            "Efficiency policy (SPEC-WORK.md@685b7c2)"
            "The fleet (spec/nova-work PR 339)")
          :state "missing"
          :evidence ())
        (          :id "E08-F04"
          :title "Availability, offers and assignment reconciliation"
          :subfeatures (            "Represent explicit rest, unavailable and unconfirmed contact with observation age"
            "Distinguish offer, acknowledgment, ownership, lease and execution"
            "Reconcile uncertain prior attempts before relaunch or reassignment"
            "After the team-configured silence threshold use one bounded availability probe; nonresponse is unconfirmed capacity, never proof of exhausted credits"
            "quiet-until-actionable: keep unchanged traffic mechanical, batch actionable deltas within bounds and let corrections, stops, lease loss and deadlines bypass delay"
            "regression-and-recovery: suspend new automatic routing on a breached trial, retain uncertain live handles and use only eligible role-preserving fallback"
            "presence-and-recovery: derive one presence per friend from the newest of the four beat sources (bus cursor, wake probe, harness hook, manual), read asleep at 300 s and unacknowledged at 600 s, refuse assignment to an asleep or unknown friend, and recover only by a coordinator's recorded reassign that cites the reading and fences the prior lease")
          :depends-on (            "E08-F03"
            "E02-F04")
          :source-sections (            "Observed availability"
            "Friends and assignments are resident indexes too"
            "Efficiency policy (SPEC-WORK.md@685b7c2)"
            "Presence: who is awake and who is asleep")
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
            "Efficiency policy (SPEC-WORK.md@685b7c2)")
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
            "cache-aware-context-choice: price cache read/write categories, service tiers and reset rebuilds; refuse missing decision, adapter or evidence inputs and compare matched accepted work"
            "gas-town-efficiency-accounting: enforce root-only step records, inline checklists and durable next-triggers to eliminate empty pulse reruns and token burn")
          :depends-on (            "E08-F05"
            "E03-F04")
          :source-sections (            "Cost"
            "Model knowledge informs scheduling"
            "Retrospective required before production implementation"
            "Efficiency policy (SPEC-WORK.md@685b7c2)"
            "Efficiency: lessons absorbed 2026-09-15")
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
            "evidence-before-adoption: refuse automatic promotion on missing baseline or coverage, unmatched quality or retrospective correlation; require a qualified prospective result"
            "efficiency-lessons-gate: enforce prime read-only projection under --max-bytes, decompose --pour inline checklists, tripped node reason fence, and delegate mode role restrictions")
          :depends-on (            "E07-F05"
            "E10-F02"
            "E10-F03")
          :source-sections (            "The measurement that decides"
            "Agreement and lock gate"
            "Efficiency policy (SPEC-WORK.md@685b7c2)"
            "Efficiency: lessons absorbed 2026-09-15")
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
