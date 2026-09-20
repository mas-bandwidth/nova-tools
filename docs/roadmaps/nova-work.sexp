; Proposed recursive roadmap data; not an implemented nova-work interchange schema.
(  :schema "nova-work-roadmap-baseline-1"
  :scope-revision 11
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
    :production-inventory "ec01647"
    :delegation-fold "spec: docs/SPEC-WORK.md section Delegation, folded from docs/SPEC-DELEGATION.md draft 3 on Glenn's word of 2026-09-15")
  :baseline-features 52
  :baseline-items 171
  :discovered-features 12
  :current-features 63
  :current-acceptance-items 231
  :verification (    :measured-at "2026-09-19"
    :revision "4c793b55a30160e5fe1ed45e25928f2c85dcfe5c"
    :branch "dev"
    :suite "cd lisp/nova-work && ./run-tests.sh"
    :suite-result "NOVA-WORK SLICE1 total=336 pass=336 fail=0"
    :criteria-rule "each :criteria row is THE record of one acceptance criterion: :id is stable (feature id + ordinal), :state is verified | unverified | unmet (a test exists and is RED: the behaviour is missing), :tests names the proving tests when known; ROADMAP.md checkboxes are a view of these rows and tools/roadmap-parity.sh fails when they differ"
    :rule "a criterion is verified only when a named test in this repository proves it and that test passed at :revision; a feature is verified only when every one of its criteria is"
    :verified-features 23
    :verified-acceptance-items 155
    :by-feature (
      (:feature "E01-F01" :verified 3 :total 3 :tests "tests/acceptance/slice-01-reader.lisp: forbidden-token-boundary-before-interning, line-comments-accepted, comment-inside-form, comment-text-is-text, quote-in-comment-is-text, semicolon-in-string-is-literal, reader-eof-sentinel-is-not-payload, malformed-trailing-unclosed-form, malformed-trailing-unclosed-list, dispatch-byte-offset-utf8, eof-byte-offset-utf8, trailing-byte-offset-utf8, unterminated-string-start-byte, forbidden-after-unicode-comment, utf8-byte-offsets; hostile-data"
       :criteria ((:id "E01-F01-01" :state "verified" :text "Accept only lists, keywords, strings, integers and comments")
                  (:id "E01-F01-02" :state "verified" :text "Reject reader/evaluation macros with byte-offset diagnostics")
                  (:id "E01-F01-03" :state "verified" :text "Never evaluate imported data")))
      (:feature "E01-F02" :verified 0 :total 3 :tests ""
       :criteria ((:id "E01-F02-01" :state "unverified" :text "Require max-bytes, max-depth and max-nodes on every file read")
                  (:id "E01-F02-02" :state "unverified" :text "Apply session bounds to snapshots, archives, journals, caches and replay bundles")
                  (:id "E01-F02-03" :state "unverified" :text "Preserve unknown keys and refuse unknown node types")))
      (:feature "E01-F03" :verified 1 :total 4 :tests "indexes-and-counters; reconstruction-after-close-and-revive; settle-keeps-id-and-evidence"
       :criteria ((:id "E01-F03-01" :state "unverified" :text "Model one stable ID per node and one owning containment parent")
                  (:id "E01-F03-02" :state "unverified" :text "Keep containment as a forest and references as a separate graph")
                  (:id "E01-F03-03" :state "verified" :text "Preserve IDs through rename, close, reopen and reparent")
                  (:id "E01-F03-04" :state "unverified" :text "Allow omitted, repeated and recursively nested work-set grouping layers without a prescribed depth")))
      (:feature "E01-F04" :verified 0 :total 4 :tests ""
       :criteria ((:id "E01-F04-01" :state "unverified" :text "Represent work-set, feature, roadmap, task, lease and event kinds")
                  (:id "E01-F04-02" :state "unverified" :text "Represent leaf tasks separately from parent tasks and attempts")
                  (:id "E01-F04-03" :state "unverified" :text "Validate acceptance kind, subject, predicate and required flag")
                  (:id "E01-F04-04" :state "unverified" :text "Represent project and stream groupings as work-set categories without inferring kind from title or position")))
      (:feature "E01-F05" :verified 2 :total 3 :tests "supported-subset-format-determinism; absent-empty-and-null-are-three-spellings; wire-integers-are-strings"
       :criteria ((:id "E01-F05-01" :state "verified" :text "Produce deterministic bytes with explicit absent/empty and Unicode semantics")
                  (:id "E01-F05-02" :state "verified" :text "Preserve large integers, timestamps, escaping and multiline text")
                  (:id "E01-F05-03" :state "unverified" :text "Compare exports with an independent semantic comparator")))
      (:feature "E02-F01" :verified 2 :total 3 :tests "working-is-a-view; cow-root-partition; session-server-daemon-and-session-start; session-status"
       :criteria ((:id "E02-F01-01" :state "unverified" :text "Load one resident O with structure and event log")
                  (:id "E02-F01-02" :state "verified" :text "Keep C as closed history and W as a predicate inside O")
                  (:id "E02-F01-03" :state "verified" :text "Make session start the launcher and expose status")))
      (:feature "E02-F02" :verified 3 :total 4 :tests "ownership-record-round-trips; resume-needs-the-journal-lock; resume-refuses-a-copied-journal"
       :criteria ((:id "E02-F02-01" :state "verified" :text "Persist owner name, generation, random token, stamp and expiry")
                  (:id "E02-F02-02" :state "verified" :text "Allow take and resume with generation rules; successor handoff belongs to E02-F05")
                  (:id "E02-F02-03" :state "verified" :text "Refuse competing live owners and name holder details")
                  (:id "E02-F02-04" :state "unverified" :text "Verify generation fencing under partitions and clock skew; reject unsafe takeover rather than relying on PID locks alone")))
      (:feature "E02-F03" :verified 1 :total 3 :tests "resume-needs-the-journal-lock"
       :criteria ((:id "E02-F03-01" :state "verified" :text "Lock canonical journal path for the full process lifetime")
                  (:id "E02-F03-02" :state "unverified" :text "Lock filesystem socket or use Windows first-pipe-instance semantics")
                  (:id "E02-F03-03" :state "unverified" :text "Never unlink another live process lock or endpoint")))
      (:feature "E02-F04" :verified 1 :total 3 :tests "fenced-export-can-finish; session-status"
       :criteria ((:id "E02-F04-01" :state "unverified" :text "Check base tip and owner token before every write")
                  (:id "E02-F04-02" :state "unverified" :text "Admit each request against until and fence on expiry or divergence")
                  (:id "E02-F04-03" :state "verified" :text "Permit only status/export while fenced and preserve journal work")))
      (:feature "E02-F05" :verified 3 :total 3 :tests "session-stop; handoff-successor-takes-next-generation; session-export-writes-the-request-bundle; session-replay-bundle-intake"
       :criteria ((:id "E02-F05-01" :state "verified" :text "Clip and publish successor or released owner record atomically")
                  (:id "E02-F05-02" :state "verified" :text "Let named successor take next generation without expiry wait")
                  (:id "E02-F05-03" :state "verified" :text "Recover fenced accepted events through export and replay")))
      (:feature "E02-F06" :verified 1 :total 3 :tests "protocol-version-negotiated-or-refused"
       :criteria ((:id "E02-F06-01" :state "unverified" :text "Pin Common Lisp implementation, Go client and supported OS/runtime combinations")
                  (:id "E02-F06-02" :state "verified" :text "Version client/engine/protocol explicitly and refuse unsupported combinations")
                  (:id "E02-F06-03" :state "unverified" :text "Provide reproducible build/install and small startup/status/shutdown smoke tests")))
      (:feature "E02-F07" :verified 3 :total 4 :tests "no-shadow-lease-across-holders; accepted-creates-one-lease-or-binds; branch-and-window-required; delegated-to-a-sleeper-then-recovered; move-keeps-the-lease; a-retry-does-not-overwrite-its-attempt; correct-is-a-linked-segment"
       :criteria ((:id "E02-F07-01" :state "verified" :text "Keep execution leases distinct from the coordinator ownership lease, with at most one live lease per node")
                  (:id "E02-F07-02" :state "unverified" :text "Support take, heartbeat, holder-only release, handed release, one extension and explicit escalation with deadline/default fields")
                  (:id "E02-F07-03" :state "verified" :text "Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now")
                  (:id "E02-F07-04" :state "verified" :text "Preserve lease state through reassignment, correction, pause, stop and recovery without counting an attempt twice")))
      (:feature "E03-F01" :verified 3 :total 3 :tests "journal-records-before-it-applies; two-event-candidate-is-all-or-none; durable-journal-multi-event-envelope-never-partly-publishes; durable-journal-changed-payload-refuses; dedup-refuses-past-its-bound"
       :criteria ((:id "E03-F01-01" :state "verified" :text "Assign stable request IDs and durable event envelopes")
                  (:id "E03-F01-02" :state "verified" :text "Validate multi-node changes all-or-none")
                  (:id "E03-F01-03" :state "verified" :text "Refuse repeated IDs or changed payload digests")))
      (:feature "E03-F02" :verified 2 :total 3 :tests "new-verbs-have-a-kind-and-a-field-order; add-field-order-is-complete; every-field-has-an-owning-verb; inventory-expansion-and-contraction"
       :criteria ((:id "E03-F02-01" :state "unverified" :text "Support add, metadata edit, move/reparent, decompose, link/unlink and retire")
                  (:id "E03-F02-02" :state "verified" :text "Record structural event fields in verb-defined order")
                  (:id "E03-F02-03" :state "verified" :text "Preserve decomposition as scope change rather than completion")))
      (:feature "E03-F03" :verified 1 :total 3 :tests "shared-prerequisite-owned-once"
       :criteria ((:id "E03-F03-01" :state "unverified" :text "Support baseline, discovery, require, dependency add/remove and prioritize")
                  (:id "E03-F03-02" :state "unverified" :text "Record author, reason, scope revision and exact member delta")
                  (:id "E03-F03-03" :state "verified" :text "Keep shared prerequisites singly owned and referenced")))
      (:feature "E03-F04" :verified 3 :total 4 :tests "correct-is-a-linked-segment; regression-opens-repair-work; reopen-revives; stop-is-a-hold-not-a-cancel; containers-settle-with-their-members; completed-view-mutation"
       :criteria ((:id "E03-F04-01" :state "unverified" :text "Support evidence-guarded state transitions including blocked, done, deferred, cancelled and superseded")
                  (:id "E03-F04-02" :state "verified" :text "Bump task generation on correction and invalidate older evidence")
                  (:id "E03-F04-03" :state "verified" :text "Keep reopen, pause and stop as durable events")
                  (:id "E03-F04-04" :state "verified" :text "Settle and reopen required-member ancestors at every grouping depth while empty required sets remain incomplete")))
      (:feature "E03-F05" :verified 3 :total 3 :tests "undo-appends-and-preserves; redo-refuses-a-stale-plan; edit-undo-preserves-later-work; undo-refuses-an-external-effect; undo-names-its-reversible-set"
       :criteria ((:id "E03-F05-01" :state "verified" :text "Create typed compensating envelopes from preimages")
                  (:id "E03-F05-02" :state "verified" :text "Check expected revisions, descendants and dependencies before apply")
                  (:id "E03-F05-03" :state "verified" :text "Preserve original history and refuse irreversible or uncertain effects")))
      (:feature "E04-F01" :verified 2 :total 3 :tests "open-count-is-read-not-computed; cow-root-partition; indexes-and-counters; findings-across-c-and-o"
       :criteria ((:id "E04-F01-01" :state "verified" :text "Maintain root open-item counters in mutation envelopes")
                  (:id "E04-F01-02" :state "verified" :text "Count canonical IDs once and exclude references, attempts and history")
                  (:id "E04-F01-03" :state "unverified" :text "Label features, leaves and member grains with revision")))
      (:feature "E04-F02" :verified 3 :total 3 :tests "percent-axis-on-a-matrix"
       :criteria ((:id "E04-F02-01" :state "verified" :text "Compute green feature cells over applicable rows per axis member")
                  (:id "E04-F02-02" :state "verified" :text "Print green, applicable, rows and baseline-rows")
                  (:id "E04-F02-03" :state "verified" :text "Print completed required leaves as k/n and never average percentages")))
      (:feature "E04-F03" :verified 2 :total 3 :tests "cow-root-partition; revive-appends-and-counts-latest; closed-rows-carry-revived-and-settles; activity-and-state-are-two-counts"
       :criteria ((:id "E04-F03-01" :state "unverified" :text "Keep baseline, discovery, deferred, cancelled and superseded distinctions")
                  (:id "E04-F03-02" :state "verified" :text "Report open plus closed totals without double membership")
                  (:id "E04-F03-03" :state "verified" :text "Report closed-in, settles-in and revives-in for windows")))
      (:feature "E04-F04" :verified 4 :total 4 :tests "indexes-and-counters; reverse-dependency-index-is-bounded; ready-names-the-blocker-and-the-resolver; ready-needs-every-dependency-settled; materialized-working-set; working-is-a-view; history-grows-startup-does-not"
       :criteria ((:id "E04-F04-01" :state "verified" :text "Build and incrementally update indexes for IDs, containment, dependencies and repositories")
                  (:id "E04-F04-02" :state "verified" :text "Provide bounded focus, subtree, category, ready and blocker queries")
                  (:id "E04-F04-03" :state "verified" :text "Avoid materialized transitive descendant sets and unbounded scans")
                  (:id "E04-F04-04" :state "verified" :text "Maintain eager W membership, its counter and per-friend reverse indexes in the same mutation envelope; normal reads never rebuild W lazily")))
      (:feature "E04-F05" :verified 3 :total 3 :tests "closed-paged-without-full-load; absent-day-is-not-a-gap; missing-segment-is-a-gap; as-of-refuses-unavailable-partition; closed-row-with-archive-absent; rule-2-unavailable-is-not-green"
       :criteria ((:id "E04-F05-01" :state "verified" :text "Resolve settled dependencies and closed rows through indexed lookup")
                  (:id "E04-F05-02" :state "verified" :text "Distinguish absent days, missing segments and unavailable as-of partitions")
                  (:id "E04-F05-03" :state "verified" :text "Return gap, provenance or refusal rather than fabricate state")))
      (:feature "E04-F06" :verified 3 :total 3 :tests "as-of-reconstructs-settle-revive-settle; cursor-pinned-across-a-new-settle; default-window-opens-two-days; rule-2-unavailable-is-not-green"
       :criteria ((:id "E04-F06-01" :state "verified" :text "Answer --at <revision> over O by replaying retained events in memory")
                  (:id "E04-F06-02" :state "verified" :text "Include the replay in revision-labelled counts and refuse before the retention boundary")
                  (:id "E04-F06-03" :state "verified" :text "Keep historical O answers separate from indexed C as-of queries and report unavailable history honestly")))
      (:feature "E05-F01" :verified 2 :total 3 :tests "verify-job-criterion-reads-the-revision; verify-resolver-identity-is-the-command; a-removed-or-corrected-need-is-unmet; regression-opens-repair-work"
       :criteria ((:id "E05-F01-01" :state "verified" :text "Bind pointer, criterion, against revision and generation to evidence")
                  (:id "E05-F01-02" :state "unverified" :text "Require matching criterion kind, exact subject and predicate")
                  (:id "E05-F01-03" :state "verified" :text "Make correction generation invalidate prior qualification")))
      (:feature "E05-F02" :verified 3 :total 3 :tests "a-done-need-is-unverified-without-complete-proof; verify-stale-evidence-needs-no-fetch; stale-evidence-does-not-unmeet-a-need; a-done-need-on-unverified-evidence-admits-nothing; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; merged-is-not-distributed"
       :criteria ((:id "E05-F02-01" :state "verified" :text "Verify every evidence event, including those not named by done")
                  (:id "E05-F02-02" :state "verified" :text "Report stale, freshest and done-unverified facts")
                  (:id "E05-F02-03" :state "verified" :text "Keep test, job, merged and attested proof distinct")))
      (:feature "E05-F03" :verified 4 :total 4 :tests "review-cycles-stay-visible; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less; reconcile-preserves-contradiction; regression-opens-repair-work; reuse-only-valid-review"
       :criteria ((:id "E05-F03-01" :state "verified" :text "Record exact-revision reviewer findings and author dispositions")
                  (:id "E05-F03-02" :state "verified" :text "Support attested criteria with reviewer identity and result")
                  (:id "E05-F03-03" :state "verified" :text "Preserve disagreement, unknowns and repair cycles")
                  (:id "E05-F03-04" :state "verified" :text "reuse-only-valid-review: reuse a review only for unchanged reviewed content, acceptance and dependencies; retain independent friend gates, reviewer-selected integrating depth and repeat triggers")))
      (:feature "E05-F04" :verified 2 :total 3 :tests "parent-green-needs-dependencies; merged-is-not-distributed"
       :criteria ((:id "E05-F04-01" :state "verified" :text "Require prerequisite dependencies and acceptance before green")
                  (:id "E05-F04-02" :state "verified" :text "Keep merged fix, verified behavior and published distribution separate")
                  (:id "E05-F04-03" :state "unverified" :text "Resolve release version through the release task reference")))
      (:feature "E05-F05" :verified 2 :total 3 :tests "a-broken-assertion-must-fail; roadmap-proof"
       :criteria ((:id "E05-F05-01" :state "verified" :text "Use independent oracle/comparator and deliberately broken assertions")
                  (:id "E05-F05-02" :state "unverified" :text "Retain fixtures, revisions, fault points and expected/actual reconciliation")
                  (:id "E05-F05-03" :state "verified" :text "Reject green claims unsupported by criterion-level proof")))
      (:feature "E05-F06" :verified 3 :total 3 :tests "verify-resolver-identity-is-the-command; verify-offline-refuses-a-fetch-budget; verify-negatives-and-unreachable; verify-cache-holds-raw-resolutions; verify-keeps-cached-evidence-rows"
       :criteria ((:id "E05-F06-01" :state "verified" :text "Resolve configured pointer schemes by direct executable invocation with fact/stamp arguments and no shell")
                  (:id "E05-F06-02" :state "verified" :text "Enforce max-fetch, fetch-timeout and offline behavior; an absent resolver is unreachable")
                  (:id "E05-F06-03" :state "verified" :text "Cache by pointer, subject and resolver identity, pin resolver identity on snapshot reads, and preserve unknown results")))
      (:feature "E06-F01" :verified 4 :total 4 :tests "journal-records-before-it-applies; crash-after-append-recovers-the-reply-once; savepoint-cut-never-splits-an-envelope; a-savepoint-is-not-a-shared-backup; savepoint-write-failure-keeps-the-previous; compaction-keeps-the-last-copy"
       :criteria ((:id "E06-F01-01" :state "verified" :text "Journal every accepted mutation before acknowledgment")
                  (:id "E06-F01-02" :state "verified" :text "Create validated snapshots with schema, revision, boundary and manifest")
                  (:id "E06-F01-03" :state "verified" :text "Expose local/shared revision, age, unshared work and failed backup")
                  (:id "E06-F01-04" :state "verified" :text "Keep prior verified snapshots and protect accepted journal history through retention/compaction")))
      (:feature "E06-F02" :verified 3 :total 3 :tests "days-merge-by-revision-never-concatenate; busy-day-many-segments; one-revision-publishes-together; closed-paged-without-full-load; page-budget-is-not-max; default-window-opens-two-days"
       :criteria ((:id "E06-F02-01" :state "verified" :text "Partition closure events by their recorded UTC day in immutable bounded segments")
                  (:id "E06-F02-02" :state "verified" :text "Publish manifests, closed rows and dedup/closed index roots in bounded pages")
                  (:id "E06-F02-03" :state "verified" :text "Maintain rolling 24-hour default window with at most two day partitions; pending SPEC-WORK.md@a0cfcf5 correction adds noon/midnight/over-limit witnesses while explicit historical queries remain bounded")))
      (:feature "E06-F03" :verified 3 :total 3 :tests "one-revision-publishes-together; index-replayed-after-crash; overlay-is-bounded-and-rebuilt"
       :criteria ((:id "E06-F03-01" :state "verified" :text "Publish snapshot, segments, manifests and both index roots in one revision")
                  (:id "E06-F03-02" :state "verified" :text "Verify hashes and never expose a root without referenced files")
                  (:id "E06-F03-03" :state "verified" :text "Reload snapshot, indexes and journal overlays after crash")))
      (:feature "E06-F04" :verified 2 :total 3 :tests "restore-is-isolated-and-dispatches-nothing; copied-journal-grants-nothing; state-load-is-isolated; savepoint-write-failure-keeps-the-previous"
       :criteria ((:id "E06-F04-01" :state "verified" :text "Restore read-only or isolated without ownership, dispatch or side effects")
                  (:id "E06-F04-02" :state "unverified" :text "Compare prior checkpoint to current state and report gaps")
                  (:id "E06-F04-03" :state "verified" :text "Keep originals and prior known-good checkpoints during replacement")))
      (:feature "E06-F05" :verified 3 :total 3 :tests "old-history; schema-evolution; source-inventory; moving-source"
       :criteria ((:id "E06-F05-01" :state "verified" :text "Export selected full history beyond resident window with declared scope")
                  (:id "E06-F05-02" :state "verified" :text "Migrate supported old schemas semantically and refuse unsupported versions")
                  (:id "E06-F05-03" :state "verified" :text "Retain source originals, provenance and unresolved records")))
      (:feature "E07-F01" :verified 5 :total 5 :tests "roadmap-has-one-creator; render-view-over-a-stored-selection; cell-moves-no-required-set; move-updates-every-roadmap-scope; roadmap-outlives-its-work; roadmap-opened-after-the-window; axisless-history"
       :criteria ((:id "E07-F01-01" :state "verified" :text "Represent ordered axes and coordinate-to-node references")
                  (:id "E07-F01-02" :state "verified" :text "Refuse unknown axis members and duplicate coordinates")
                  (:id "E07-F01-03" :state "verified" :text "Treat missing cells and out-of-scope cells distinctly")
                  (:id "E07-F01-04" :state "verified" :text "Preserve selected scope and completion unit across recursive grouping layouts without mandatory axis or cell wrappers")
                  (:id "E07-F01-05" :state "verified" :text "Retain completed roadmap members and historical code/test evidence beyond the active 24-hour window")))
      (:feature "E07-F02" :verified 2 :total 3 :tests "roadmap-proof; a-broken-assertion-must-fail; chat-and-file-render-are-byte-identical; render-refuses-a-target-outside-its-roots"
       :criteria ((:id "E07-F02-01" :state "verified" :text "Render tick only for fully verified cells and cross otherwise")
                  (:id "E07-F02-02" :state "unverified" :text "Keep partial, missing and unknown state in S/check counts")
                  (:id "E07-F02-03" :state "verified" :text "Write only the marked roadmap region and preserve unrelated bytes")))
      (:feature "E07-F03" :verified 2 :total 3 :tests "render-artifact-is-bounded; roadmap-proof; chat-and-file-render-are-byte-identical; move-updates-every-roadmap-scope"
       :criteria ((:id "E07-F03-01" :state "unverified" :text "Regenerate ROADMAP from primary O data")
                  (:id "E07-F03-02" :state "verified" :text "Make render --check fail on drift or ambiguous markers")
                  (:id "E07-F03-03" :state "verified" :text "Ensure chat/file projections use the same captured revision")))
      (:feature "E07-F04" :verified 1 :total 3 :tests "moving-source"
       :criteria ((:id "E07-F04-01" :state "unverified" :text "Preserve source revision, audit IDs, reports and aliases")
                  (:id "E07-F04-02" :state "unverified" :text "Include ordinary delivered capabilities beside versioning rows")
                  (:id "E07-F04-03" :state "verified" :text "Keep source support separate from qualified acceptance and incomplete reconciliation visible")))
      (:feature "E07-F05" :verified 0 :total 3 :tests ""
       :criteria ((:id "E07-F05-01" :state "unverified" :text "Exercise completed cell, new discovery, dependency/handoff and changed focus")
                  (:id "E07-F05-02" :state "unverified" :text "Record failures, repairs, denominator movement and changed scope")
                  (:id "E07-F05-03" :state "unverified" :text "Feed findings into NEXT-TOOLS and production specs before implementation")))
      (:feature "E07-F06" :verified 0 :total 3 :tests ""
       :criteria ((:id "E07-F06-01" :state "unverified" :text "Omit private nodes and descendants from rendered output files")
                  (:id "E07-F06-02" :state "unverified" :text "When a public view reaches private work through a parent, print only the private count")
                  (:id "E07-F06-03" :state "unverified" :text "Preserve private state in O and validation while applying filtering only at projection boundaries")))
      (:feature "E08-F01" :verified 0 :total 4 :tests ""
       :criteria ((:id "E08-F01-01" :state "unverified" :text "Use one schema for client validation, protocol, help and examples")
                  (:id "E08-F01-02" :state "unverified" :text "Provide bounded family/verb help and machine discovery with schema hash")
                  (:id "E08-F01-03" :state "unverified" :text "Refuse stale discovery and invalid suggested actions")
                  (:id "E08-F01-04" :state "unverified" :text "List next valid actions and missing prerequisites without granting authority or executing suggestions")))
      (:feature "E08-F02" :verified 8 :total 9 :tests "wire-is-length-prefixed-utf8-json; status-answers-while-io-runs; cancel-is-a-request-not-an-erasure; disconnect-is-not-a-rollback; lost-reply-then-reopen; durable-journal-append-plus-lost-reply-recovers-once; state-export-is-one-long-operation; clip-is-one-long-operation; batches-and-pipelines; materialized-working-set; pipeline-replies-are-correlated; batch-with-bounds-and-urgency"
       :criteria ((:id "E08-F02-01" :state "verified" :text "Support framed requests, status, wait, cancel and bounded backpressure")
                  (:id "E08-F02-02" :state "verified" :text "Handle disconnect, lost reply, deadlines and uncertain outcomes")
                  (:id "E08-F02-03" :state "verified" :text "Keep control operations responsive during import/export/clip")
                  (:id "E08-F02-04" :state "unverified" :text "Use bounded typed JSON over a local Unix socket, exact integer/time encoding and durable asynchronous operation IDs; reconcile cross-platform endpoint requirements before lock")
                  (:id "E08-F02-05" :state "verified" :text "Support bounded read bundles at one captured revision and lease-time watermark, with stable paginated snapshot identity")
                  (:id "E08-F02-06" :state "verified" :text "Support independent ordered batches with per-entry IDs/outcomes and explicit stop/continue semantics, without implying rollback")
                  (:id "E08-F02-07" :state "verified" :text "Support atomic mutation batches with all-or-none validated O/W, counter and reverse-index changes; reject oversized batches without silently splitting")
                  (:id "E08-F02-08" :state "verified" :text "pipeline-replies-are-correlated: correlate out-of-order or fragmented ordinary responses by request ID, retain a distinct durable operation ID, name independent not-attempted and atomic validation entry outcomes, and close on unknown, duplicate, absent or undecodable IDs; same-ID recovery uses E03-F01 durable idempotency")
                  (:id "E08-F02-09" :state "verified" :text "batch-with-bounds-and-urgency: coalesce independent results within byte, record and delay bounds; refuse unreferenced padding, bypass delay for urgent changes and preserve dependency and retry identity")))
      (:feature "E08-F03" :verified 4 :total 7 :tests "fleet-is-static-config; probe-records-observed-active-and-touches-no-config; four-capability-groups-and-three-fields; roles-are-configured-not-inferred; reserved-role-is-not-spent-on-routine-work; bounds-are-not-prompts"
       :criteria ((:id "E08-F03-01" :state "unverified" :text "Index every known friend, assignments and working task references")
                  (:id "E08-F03-02" :state "verified" :text "Separate stable capabilities/config from observed active executions")
                  (:id "E08-F03-03" :state "unverified" :text "Include coordinator identity and attribute model, bench, attempt and usage")
                  (:id "E08-F03-04" :state "verified" :text "Store agreed specialist roles per friend; keep model strengths/weaknesses in the shared model catalog")
                  (:id "E08-F03-05" :state "unverified" :text "Represent children, swarm capabilities, local runs and one-shots separately from friend identity; agreed concurrency limits apply")
                  (:id "E08-F03-06" :state "verified" :text "bounds-are-not-prompts: refuse automatic dispatch when an adapter cannot enforce the configured execution limit; distinguish wait deadline from observed stop or unresolved outcome")
                  (:id "E08-F03-07" :state "verified" :text "fleet-is-static-config: hold the fleet as `:machine` records in CONFIG with stable id, owner, connection-profile reference, roles, limits, permits, exclusions and dated declared facts, all instance data; list it and recommend members for a workload kind from declared facts, never a lease; refuse a record without owner or id, a held connection profile, a credential, an unknown owner or role, and a choice of a member for a workload it excludes")))
      (:feature "E08-F04" :verified 6 :total 7 :tests "explicit-rest-is-not-pinged; silence-is-a-ping-not-a-verdict; four-facts-four-verbs; dispatch-ack-and-ownership-are-three; return-reconciles-before-dispatch; hold-survives-a-crash; quiet-until-actionable; regression-and-recovery"
       :criteria ((:id "E08-F04-01" :state "verified" :text "Represent explicit rest, unavailable and unconfirmed contact with observation age")
                  (:id "E08-F04-02" :state "verified" :text "Distinguish offer, acknowledgment, ownership, lease and execution")
                  (:id "E08-F04-03" :state "verified" :text "Reconcile uncertain prior attempts before relaunch or reassignment")
                  (:id "E08-F04-04" :state "verified" :text "After the team-configured silence threshold use one bounded availability probe; nonresponse is unconfirmed capacity, never proof of exhausted credits")
                  (:id "E08-F04-05" :state "verified" :text "quiet-until-actionable: keep unchanged traffic mechanical, batch actionable deltas within bounds and let corrections, stops, lease loss and deadlines bypass delay")
                  (:id "E08-F04-06" :state "verified" :text "regression-and-recovery: suspend new automatic routing on a breached trial, retain uncertain live handles and use only eligible role-preserving fallback")
                  (:id "E08-F04-07" :state "unverified" :text "presence-and-recovery: derive one presence per friend from the newest of the four beat sources (bus cursor, wake probe, harness hook, manual), read asleep at 300 s and unacknowledged at 600 s, refuse assignment to an asleep or unknown friend, and recover only by a coordinator's recorded reassign that cites the reading and fences the prior lease")))
      (:feature "E08-F05" :verified 5 :total 5 :tests "unchanged-config-is-one-bounded-answer; an-invalid-delta-leaves-the-old-config; a-partial-manifest-is-refused; route-config-lists-key-by-path-never-value; no-credential-in-a-member; pricing-is-pinned-by-revision; requested-model-is-not-observed-model; policy-round-trip-and-replay; packet-and-route-gates"
       :criteria ((:id "E08-F05-01" :state "verified" :text "Exchange UNCHANGED manifests or validated deltas by hash/revision")
                  (:id "E08-F05-02" :state "verified" :text "Store model route, pricing, quota and provenance without secrets")
                  (:id "E08-F05-03" :state "verified" :text "Keep requested versus observed model and measured suitability separate")
                  (:id "E08-F05-04" :state "verified" :text "policy-round-trip-and-replay: preserve per-friend efficiency policy, trial manifests and execution references through validated intake, export/import, restart, undo and replay")
                  (:id "E08-F05-05" :state "verified" :text "packet-and-route-gates: refuse oversized or history-disallowed packets and ineligible/stale routes; retain a scoped reason for economical-route exceptions")))
      (:feature "E09-F01" :verified 1 :total 3 :tests "archive-completeness"
       :criteria ((:id "E09-F01-01" :state "unverified" :text "Capture stable provider/repository/issue identity, revision and URL")
                  (:id "E09-F01-02" :state "unverified" :text "Preserve body, comments, labels, relationships, attachments and pagination")
                  (:id "E09-F01-03" :state "verified" :text "Keep inaccessible or unsupported fields explicit")))
      (:feature "E09-F02" :verified 1 :total 3 :tests "read-only-intake; hostile-data"
       :criteria ((:id "E09-F02-01" :state "unverified" :text "Map one issue to many nodes and repeated updates without duplicates")
                  (:id "E09-F02-02" :state "verified" :text "Keep remote text as data separate from accepted plan and authority")
                  (:id "E09-F02-03" :state "unverified" :text "Track pending, confirmed and failed outbound actions with receipts")))
      (:feature "E09-F03" :verified 2 :total 3 :tests "source-inventory; moving-source"
       :criteria ((:id "E09-F03-01" :state "verified" :text "Inventory authorized sources with capture manifest and disposition")
                  (:id "E09-F03-02" :state "unverified" :text "Import in batches with originals, mappings, deduplication and checkpoints")
                  (:id "E09-F03-03" :state "verified" :text "Reconcile counts/content and exercise interruption and source edits")))
      (:feature "E09-F04" :verified 0 :total 3 :tests ""
       :criteria ((:id "E09-F04-01" :state "unverified" :text "Separate absorb from default link and require selected scope/authority")
                  (:id "E09-F04-02" :state "unverified" :text "Archive source identity, provenance and content before removal; append the actual deletion outcome receipt after the attempt")
                  (:id "E09-F04-03" :state "unverified" :text "Leave deletion pending on missing content, source change or uncertain network result")))
      (:feature "E09-F05" :verified 3 :total 3 :tests "merged-is-not-distributed; disconnect-is-not-a-rollback; reply-retired-only-under-verified-coverage; cancel-is-a-request-not-an-erasure"
       :criteria ((:id "E09-F05-01" :state "verified" :text "Keep GitHub issue closure, publishing and source deletion separate from local completion")
                  (:id "E09-F05-02" :state "verified" :text "Do not claim Git/GitHub atomicity or invent authors")
                  (:id "E09-F05-03" :state "verified" :text "Preserve concurrent human changes and uncertain external outcomes")))
      (:feature "E10-F01" :verified 1 :total 3 :tests "cancel-is-a-request-not-an-erasure; disconnect-is-not-a-rollback; lost-reply-then-reopen; torn-tail-is-diagnosed-not-truncated"
       :criteria ((:id "E10-F01-01" :state "unverified" :text "Attach stable error code, stage, verb, request/operation ID and known revisions")
                  (:id "E10-F01-02" :state "verified" :text "Distinguish refused, reply lost, running, cancelled and unknown external outcome")
                  (:id "E10-F01-03" :state "unverified" :text "Provide bounded inspect/diagnose drill-down without secrets or private bodies")))
      (:feature "E10-F02" :verified 6 :total 6 :tests "unknown-price-is-not-zero; local-tokens-cost-zero-api; subscription-is-not-free-reference-cost; pricing-is-pinned-by-revision; cost-joins-include-the-coordinator; complete-cost-lineage; cache-aware-context-choice; gas-town-efficiency-accounting"
       :criteria ((:id "E10-F02-01" :state "verified" :text "Record attempt usage pointers and unresolved usage as unmeasured")
                  (:id "E10-F02-02" :state "verified" :text "Separate billed cash, estimated cash and virtual token cost")
                  (:id "E10-F02-03" :state "verified" :text "Pin rate/config revisions and include coordinator, review and rework overhead")
                  (:id "E10-F02-04" :state "verified" :text "complete-cost-lineage: join parent, child, retry and failed-attempt receipts once; avoid counting cache/reasoning subsets again, implementation cost separate and gaps unknown")
                  (:id "E10-F02-05" :state "verified" :text "cache-aware-context-choice: price cache read/write categories, service tiers and reset rebuilds; refuse missing decision, adapter or evidence inputs and compare matched accepted work")
                  (:id "E10-F02-06" :state "verified" :text "gas-town-efficiency-accounting: enforce root-only step records, inline checklists and durable next-triggers to eliminate empty pulse reruns and token burn")))
      (:feature "E10-F03" :verified 2 :total 4 :tests "hostile-data; full-round-trip; indexes-and-counters; roadmap-proof; single-writer; durable-journal-pre-append-failure-writes-nothing; durable-journal-partial-write-refuses-without-truncation; torn-tail-is-diagnosed-not-truncated; crash-after-append-recovers-the-reply-once; savepoint-write-failure-keeps-the-previous; one-revision-publishes-together"
       :criteria ((:id "E10-F03-01" :state "verified" :text "Run parser, round-trip, invariant, index/counter and roadmap proof suites")
                  (:id "E10-F03-02" :state "verified" :text "Inject failures at journal, checkpoint, apply, reply and publication boundaries")
                  (:id "E10-F03-03" :state "unverified" :text "Map each suite to an owner, command and CI lane without duplicating its acceptance evidence")
                  (:id "E10-F03-04" :state "unverified" :text "Keep per-change CI within two minutes, exhaustive fault/scale suites explicit nightly or pre-release; failures block affected gates")))
      (:feature "E10-F04" :verified 0 :total 3 :tests ""
       :criteria ((:id "E10-F04-01" :state "unverified" :text "Orchestrate process-level runs against temporary remotes and fake providers, including crash/restart, partition, stale owner and handoff")
                  (:id "E10-F04-02" :state "unverified" :text "Collect release-level results from the owning fencing, recovery, paging and as-of features without re-owning their assertions")
                  (:id "E10-F04-03" :state "unverified" :text "Run the authorized read-only real-repository pilot and disposable import, then publish a reconciliation disposition")))
      (:feature "E10-F05" :verified 2 :total 5 :tests "evidence-before-adoption; efficiency-lessons-gate"
       :criteria ((:id "E10-F05-01" :state "unverified" :text "Compare update-and-render tokens/wall time with manual editing")
                  (:id "E10-F05-02" :state "unverified" :text "Test lease-only answer for who is working on C and unsupported number detection")
                  (:id "E10-F05-03" :state "unverified" :text "Pin scenarios, owners and commands before implementation; require exact-revision correctness and measured operational results before adoption")
                  (:id "E10-F05-04" :state "verified" :text "evidence-before-adoption: refuse automatic promotion on missing baseline or coverage, unmatched quality or retrospective correlation; require a qualified prospective result")
                  (:id "E10-F05-05" :state "verified" :text "efficiency-lessons-gate: enforce prime read-only projection under --max-bytes, decompose --pour inline checklists, tripped node reason fence, and delegate mode role restrictions")))
      (:feature "E10-F07" :verified 2 :total 3 :tests "referential-integrity-refuses-a-cycle; rule-2-unavailable-is-not-green; coordination-tree-integrity-refuses; cow-root-partition; cell-moves-no-required-set; no-shadow-lease-across-holders; rule-18-finds-the-latest-row-not-the-history; container-cascade-journal-rejection-and-retry-stability"
       :criteria ((:id "E10-F07-01" :state "verified" :text "Validate duplicate IDs, dangling versus unavailable references, dependency cycles, two parents, invalid cells, conflicting leases and in-two-branches")
                  (:id "E10-F07-02" :state "verified" :text "Run whole-state validation at load/clip and candidate-gate validation at every mutation")
                  (:id "E10-F07-03" :state "unverified" :text "Support session start --repair only when findings strictly decrease, preserving the unmodified source and emitting a repair diff")))
      (:feature "E10-F08" :verified 3 :total 3 :tests "the-cli-thin-client; an-excluded-choice-is-refused-not-empty; notes-refuse-missing-source-or-date; clip-is-one-long-operation; render-artifact-is-bounded; closed-paged-without-full-load; page-budget-is-not-max; busy-day-many-segments"
       :criteria ((:id "E10-F08-01" :state "verified" :text "Define per-verb first tokens, OK/FAIL/RACED/ROW/NOTE/MORE records, stdout/stderr split and exit codes 0/1/2")
                  (:id "E10-F08-02" :state "verified" :text "Emit emitted= on OK lines and pushed= on scope lines; never exceed configured output caps")
                  (:id "E10-F08-03" :state "verified" :text "Enforce --max default 20, zero meaning all, reject negatives, and print MORE with a usable continuation remedy")))
      (:feature "E11-F01" :verified 6 :total 6 :tests "notes-refuse-missing-source-or-date; notes-id-is-content-digest; notes-writer-is-scoped; notes-bounds-refuse; notes-supersede-is-one-envelope; notes-weaker-kind-cannot-supersede"
       :criteria ((:id "E11-F01-01" :state "verified" :text "notes-refuse-missing-source-or-date: refuse a write without --source or --date, or with a :kind outside :instruction, :observation and :heuristic, at exit 2 naming the field, nothing written")
                  (:id "E11-F01-02" :state "verified" :text "notes-id-is-content-digest: assign :id as note:<sha256> over the canonical eight fields so two benches yield one id; refuse a second write of the same preimage as already written; never reuse an id")
                  (:id "E11-F01-03" :state "verified" :text "notes-writer-is-scoped: refuse a coordinator-scope note by another --as, a group-scope note by a non-member and an unregistered --as; a note grants no access")
                  (:id "E11-F01-04" :state "verified" :text "notes-bounds-refuse: refuse a write past :max-active, :max-text-bytes or :max-constraint-nodes naming the field and both numbers; refuse to guess a missing bound; a supersede never changes the active count")
                  (:id "E11-F01-05" :state "verified" :text "notes-supersede-is-one-envelope: write the replacement and the supersede as one validated envelope or neither; refuse the second of two competing supersedes as not active; never print a superseded note as active")
                  (:id "E11-F01-06" :state "verified" :text "notes-weaker-kind-cannot-supersede: refuse an :observation or :heuristic replacement for an :instruction; inherit :kind and :scope from the old note and require a new :source")))
      (:feature "E11-F02" :verified 5 :total 5 :tests "goal-crosses-harness; goal-stale-update-refuses; goal-stop-is-a-request-not-evidence; goal-update-writes-only-existing-kinds; goal-expect-is-required"
       :criteria ((:id "E11-F02-01" :state "verified" :text "goal-crosses-harness: a second build and harness reads the same goal, rev, stop=requested and the same note id and constraint row byte for byte, live and from the snapshot; its progress update on the stopped goal is refused")
                  (:id "E11-F02-02" :state "verified" :text "goal-stale-update-refuses: refuse goal update and goal set with a stale --expect as GOAL FAIL naming expect and current, nothing written; refuse goal set to a closed node by disposition whatever --expect says")
                  (:id "E11-F02-03" :state "verified" :text "goal-stop-is-a-request-not-evidence: goal update --stop writes only a :transition to :cancel-requested with :reason and no :evidence; show prints stop=requested; stop=cancelled only after event --kind cancel with evidence")
                  (:id "E11-F02-04" :state "verified" :text "goal-update-writes-only-existing-kinds: every goal update form writes a :transition or :evidence event with its kind's field list on the goal node and no other; goal set writes one :goal event; objective edits never go through update")
                  (:id "E11-F02-05" :state "verified" :text "goal-expect-is-required: refuse goal set and goal update without --expect at exit 2 naming the flag; --dry-run with a stale expectation prints the refusal and writes nothing")))
      (:feature "E11-F03" :verified 6 :total 6 :tests "applicable-before-route; applicable-cap-never-hides-a-deny; applicable-unknown-is-not-eligible; applicable-snapshot-is-planning-only; narrative-does-not-filter; delegation-admission-gates"
       :criteria ((:id "E11-F03-01" :state "verified" :text "applicable-before-route: route selection calls applicable first and prices only routes printed eligible; a card builder handed an excluded or unknown route refuses naming the note id or the reason")
                  (:id "E11-F03-02" :state "verified" :text "applicable-cap-never-hides-a-deny: with more active notes than --max and the only :deny in the note that sorts last, applicable prints excluded with that id and NOTES MORE; goal show prints the constraint row uncut")
                  (:id "E11-F03-03" :state "verified" :text "applicable-unknown-is-not-eligible: with no live session and no --snapshot, a snapshot past a bound, a missing notes index or an unregistered model, print NOTES FAIL and no eligible row")
                  (:id "E11-F03-04" :state "verified" :text "applicable-snapshot-is-planning-only: an answer from=snapshot admits no route; the admitting write carries --expect the live rev and a stop or deny written between check and admission refuses it stale")
                  (:id "E11-F03-05" :state "verified" :text "narrative-does-not-filter: a note with prose and no :constraint is printed and excludes nothing; a :deny excludes only a candidate matching every named axis; two disagreeing constraints print both and exclude")
                  (:id "E11-F03-06" :state "verified" :text "delegation-admission-gates: refuse a packet lacking objective, source revision, criteria, scope, result contract, checkpoint or :effort at gate 2; refuse a dispatch crossing the daily spend ceiling at gate 3 naming the ceiling; each refusal names its gate and reason at exit 2")))
      (:feature "E11-F04" :verified 2 :total 3 :tests "delegation-result-gates; receipt-at-exact-head"
       :criteria ((:id "E11-F04-01" :state "verified" :text "delegation-result-gates: refuse an offer to a friend reading asleep or unknown at gate 4; record the requested execution limit and the observed expiry or stop outcome separately; never start a second attempt silently after uncertainty about the first")
                  (:id "E11-F04-02" :state "verified" :text "receipt-at-exact-head: book a child's result as a machinery receipt at the exact head it ran against; bind a review verdict to --head <sha> and never reuse it across a changed head or an unchecked rebase")
                  (:id "E11-F04-03" :state "unverified" :text "partial-child-never-closes-parent: one child done beside one refused, blocked or asleep leaves the parent open with outstanding=<n> and the mapped external issue open; the outstanding count and the issue mapping survive the child's refusal unchanged")))
      (:feature "E11-F05" :verified 0 :total 3 :tests ""
       :criteria ((:id "E11-F05-01" :state "unverified" :text "decision-packet-per-item-revision: machinery builds one packet per item and revision; a newer revision supersedes it keeping its open findings; a busy reader's packet is amended, not duplicated; an empty pulse wakes no model")
                  (:id "E11-F05-02" :state "unverified" :text "packet-is-smallest-sufficient: the packet carries the delta since this reader's recorded head, the rules it touches, open findings with dispositions, new behaviour with evidence pointers and links to full sources; the whole diff only on a first read")
                  (:id "E11-F05-03" :state "unverified" :text "no-receipt-of-receipt: a worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate")))
      (:feature "E11-F06" :verified 0 :total 4 :tests ""
       :criteria ((:id "E11-F06-01" :state "unverified" :text "no-survives-the-hop: a decline, refused offer, excluded route, asleep recipient, tripped node or effort limit reaches the parent as a named refusal with its reason and revision, never as silence or success")
                  (:id "E11-F06-02" :state "unverified" :text "envelope-up-is-a-copy: the child's verdict, result pointer, evidence events, usage pointer and exact head arrive byte-copied by machinery beside its distilled learning in its own words; the parent can open the child's evidence from the envelope")
                  (:id "E11-F06-03" :state "unverified" :text "finality-rises-with-tier: a child's done is a claim; the parent moves only after its own verification with evidence bound to its own criteria; a worker's success claim alone never moves a node below the seat")
                  (:id "E11-F06-04" :state "unverified" :text "escalation-is-a-packet: a hold, question or exception the child cannot decide rises as a packet with reason and revision; escalated-age= and reread= are information and reassign nothing; an :effort widening or expensive-route exception carries the coordinator's recorded reason")))
      ))
  :events (    (      :kind "baseline"
      :feature-count 52
      :reason "Full initial nova-work source survey; implementation not started")
    (      :kind "discovery"
      :feature-count 6
      :reason "PR 269 review found six source-backed inventory gaps; implementation not started")
    (      :kind "remove"
      :feature-count 1
      :reason "E10-F06 moved to the Now register outside the product feature denominator")
    (      :kind "discovery"
      :feature-count 6
      :reason "Glenn 2026-09-15: DELEGATION folded into SPEC-WORK.md as section Delegation; epic E11 with six features and 27 acceptance items enters the product denominator; implementation not started")
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
      :status "measured"
      :owner "unassigned"
      :next-gate "integrated"
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
      :source "ideas-780")
    (      :id "RD-29"
      :title "ideas#780: a bounded sideways channel between sibling cards in flight"
      :status "proposed"
      :owner "unassigned"
      :next-gate "experiment"
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
          :evidence ())))
    (      :id "E11"
      :title "Delegation"
      :features (        (          :id "E11-F01"
          :title "Coordinator notes: record, identity and one writer"
          :subfeatures (            "notes-refuse-missing-source-or-date: refuse a write without --source or --date, or with a :kind outside :instruction, :observation and :heuristic, at exit 2 naming the field, nothing written"
            "notes-id-is-content-digest: assign :id as note:<sha256> over the canonical eight fields so two benches yield one id; refuse a second write of the same preimage as already written; never reuse an id"
            "notes-writer-is-scoped: refuse a coordinator-scope note by another --as, a group-scope note by a non-member and an unregistered --as; a note grants no access"
            "notes-bounds-refuse: refuse a write past :max-active, :max-text-bytes or :max-constraint-nodes naming the field and both numbers; refuse to guess a missing bound; a supersede never changes the active count"
            "notes-supersede-is-one-envelope: write the replacement and the supersede as one validated envelope or neither; refuse the second of two competing supersedes as not active; never print a superseded note as active"
            "notes-weaker-kind-cannot-supersede: refuse an :observation or :heuristic replacement for an :instruction; inherit :kind and :scope from the old note and require a new :source")
          :depends-on (            "E01-F01"
            "E03-F01"
            "E08-F01")
          :source-sections (            "Delegation"
            "The data"
            "Output grammar")
          :state "missing"
          :evidence ())
        (          :id "E11-F02"
          :title "The current goal across models and harnesses"
          :subfeatures (            "goal-crosses-harness: a second build and harness reads the same goal, rev, stop=requested and the same note id and constraint row byte for byte, live and from the snapshot; its progress update on the stopped goal is refused"
            "goal-stale-update-refuses: refuse goal update and goal set with a stale --expect as GOAL FAIL naming expect and current, nothing written; refuse goal set to a closed node by disposition whatever --expect says"
            "goal-stop-is-a-request-not-evidence: goal update --stop writes only a :transition to :cancel-requested with :reason and no :evidence; show prints stop=requested; stop=cancelled only after event --kind cancel with evidence"
            "goal-update-writes-only-existing-kinds: every goal update form writes a :transition or :evidence event with its kind's field list on the goal node and no other; goal set writes one :goal event; objective edits never go through update"
            "goal-expect-is-required: refuse goal set and goal update without --expect at exit 2 naming the flag; --dry-run with a stale expectation prints the refusal and writes nothing")
          :depends-on (            "E11-F01"
            "E03-F02"
            "E08-F01")
          :source-sections (            "The current goal"
            "Delegation"
            "Acceptance replays")
          :state "missing"
          :evidence ())
        (          :id "E11-F03"
          :title "Admission gates 1 to 3: notes read, packet bounded, route eligible"
          :subfeatures (            "applicable-before-route: route selection calls applicable first and prices only routes printed eligible; a card builder handed an excluded or unknown route refuses naming the note id or the reason"
            "applicable-cap-never-hides-a-deny: with more active notes than --max and the only :deny in the note that sorts last, applicable prints excluded with that id and NOTES MORE; goal show prints the constraint row uncut"
            "applicable-unknown-is-not-eligible: with no live session and no --snapshot, a snapshot past a bound, a missing notes index or an unregistered model, print NOTES FAIL and no eligible row"
            "applicable-snapshot-is-planning-only: an answer from=snapshot admits no route; the admitting write carries --expect the live rev and a stop or deny written between check and admission refuses it stale"
            "narrative-does-not-filter: a note with prose and no :constraint is printed and excludes nothing; a :deny excludes only a candidate matching every named axis; two disagreeing constraints print both and exclude"
            "delegation-admission-gates: refuse a packet lacking objective, source revision, criteria, scope, result contract, checkpoint or :effort at gate 2; refuse a dispatch crossing the daily spend ceiling at gate 3 naming the ceiling; each refusal names its gate and reason at exit 2")
          :depends-on (            "E11-F01"
            "E08-F04"
            "E10-F02")
          :source-sections (            "Delegation"
            "Admission and result gates"
            "Efficiency: lessons absorbed 2026-09-15")
          :state "missing"
          :evidence ())
        (          :id "E11-F04"
          :title "Execution and result gates 4 to 6: real bounds, receipts by machinery, integration"
          :subfeatures (            "delegation-result-gates: refuse an offer to a friend reading asleep or unknown at gate 4; record the requested execution limit and the observed expiry or stop outcome separately; never start a second attempt silently after uncertainty about the first"
            "receipt-at-exact-head: book a child's result as a machinery receipt at the exact head it ran against; bind a review verdict to --head <sha> and never reuse it across a changed head or an unchecked rebase"
            "partial-child-never-closes-parent: one child done beside one refused, blocked or asleep leaves the parent open with outstanding=<n> and the mapped external issue open; the outstanding count and the issue mapping survive the child's refusal unchanged")
          :depends-on (            "E11-F03"
            "E08-F05"
            "E05-F02")
          :source-sections (            "Delegation"
            "Admission and result gates"
            "Presence: who is awake and who is asleep"
            "Assignment and execution control")
          :state "missing"
          :evidence ())
        (          :id "E11-F05"
          :title "Decision packets"
          :subfeatures (            "decision-packet-per-item-revision: machinery builds one packet per item and revision; a newer revision supersedes it keeping its open findings; a busy reader's packet is amended, not duplicated; an empty pulse wakes no model"
            "packet-is-smallest-sufficient: the packet carries the delta since this reader's recorded head, the rules it touches, open findings with dispositions, new behaviour with evidence pointers and links to full sources; the whole diff only on a first read"
            "no-receipt-of-receipt: a worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate")
          :depends-on (            "E11-F04"
            "E08-F02")
          :source-sections (            "Delegation"
            "Efficiency: lessons absorbed 2026-09-15")
          :state "missing"
          :evidence ())
        (          :id "E11-F06"
          :title "The envelope up and escalation: the no survives the hop"
          :subfeatures (            "no-survives-the-hop: a decline, refused offer, excluded route, asleep recipient, tripped node or effort limit reaches the parent as a named refusal with its reason and revision, never as silence or success"
            "envelope-up-is-a-copy: the child's verdict, result pointer, evidence events, usage pointer and exact head arrive byte-copied by machinery beside its distilled learning in its own words; the parent can open the child's evidence from the envelope"
            "finality-rises-with-tier: a child's done is a claim; the parent moves only after its own verification with evidence bound to its own criteria; a worker's success claim alone never moves a node below the seat"
            "escalation-is-a-packet: a hold, question or exception the child cannot decide rises as a packet with reason and revision; escalated-age= and reread= are information and reassign nothing; an :effort widening or expensive-route exception carries the coordinator's recorded reason")
          :depends-on (            "E11-F04"
            "E11-F05"
            "E08-F03")
          :source-sections (            "Delegation"
            "Efficiency: lessons absorbed 2026-09-15"
            "Presence: who is awake and who is asleep")
          :state "missing"
          :evidence ())))))
