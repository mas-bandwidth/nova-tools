# nova-work next: the spec's promises the kernel does not yet keep

`scratch/next.tsv` audits every verb, view and subsystem `docs/SPEC-WORK.md`
promises against what `lisp/nova-work/` actually exports and does, read at
commit `cf9c9986` (`origin/dev`, branch `rowan/nova-work-next`); a row is
`exported and real`, `exported but a stub that only satisfies its replay`, or
`absent`, with the spec lines and the replays that touch it. Every replay the
spec names already exists as a real `deftest` (#362 closed), so a stub is a
pure model driven directly by its own replay while the resident session, CLI,
socket, provider or store the verb actually needs is still out; the `real`
rows are the slice-1 kernel plus the pure books that carry their replays whole.

The epics below regroup the `stub` and `absent` rows only (`real` rows stay in
the table as the base the epics build on), one subsystem per epic, ordered by
how many later rows ride on the epic: the transport and the operation scheduler
first, because every session verb, the batch modes, `operation wait`/`cancel`
and the clip/export transports ride on them, and every verb below reaches a
client only through them. Each card is 30 minutes — one verb or view of the
epic — so an epic's card count is its row count.

## E01 — The socket transport and the resident session (11 cards)

Spec: `:2256-2267`, `:2642-2708`, `:2710-2760`, `:5762-5775`, `:5879-5882`.
Rows: socket endpoint and the local listener; session server daemon and
`session start`; `session status`; `session stop` and `session handoff`;
`session replay` bundle intake; `session export` request bundle; the framed
length-prefixed JSON wire codec; `hello` protocol-version negotiation;
pipelined request correlation; the read bundle / independent / atomic batch
modes; the CLI thin client.
Replays: `endpoint-is-local-and-private`, `wire-integers-are-strings`,
`protocol-version-negotiated-or-refused`, `pipeline-replies-are-correlated`,
`absent-empty-and-null-are-three-spellings`, `disconnect-is-not-a-rollback`,
`operation-survives-the-client`, `status-answers-while-io-runs`,
`cancel-is-a-request-not-an-erasure`, `batches-and-pipelines`, `single-writer`,
`no-friend-name-in-the-tool`.

## E02 — The operation scheduler (capture / export / clip) (6 cards)

Spec: `:2262-2271`, `:2721-2760`, `:3197-3223`, `:3283-3299`, `:5930-5934`,
`:6026-6057`, `:6360`.
Rows: `operation status|list|wait|cancel`; the durable operation id and its
accept record; source capture and import staging; `session export --state
--at`; `state load`; the `clip` transport as one long operation.
Replays: `operation-survives-the-client`, `status-answers-while-io-runs`,
`cancel-is-a-request-not-an-erasure`, `clip-is-one-long-operation`,
`state-export-describes-exactly-r`, `state-export-refuses-a-gap`,
`state-export-pin-survives-clip`, `state-export-disconnect-and-cancel`,
`fenced-export-can-finish`, `state-load-is-isolated`, `async-operations`.

## E03 — The verifier and staged admission (3 cards)

Spec: `:2302`, `:2748-2750`, `:3858-3859`, `:5537-5543`, `:5837-5840`.
Rows: the verifier result a receipt needs; staged admission of slow-I/O
results; the `verify` verb and the verification cache.
Replays: `a-receipt-needs-a-verifier`, `staged-admission-refuses`,
`settle-keeps-id-and-evidence`, `local-tokens-cost-zero-api`.

## E04 — The roadmap verbs (8 cards)

Spec: `:2313`, `:2323-2329`, `:3072-3129`, `:5834-5845`, `:5986-6000`.
Rows: `roadmap create`; `roadmap configure`; `roadmap row`; `roadmap
projection`; `axis --add/--remove`; `cell`; `render --view` over a stored
selection; `percent --axis` on a matrix.
Replays: `roadmap-has-one-creator`, `completed-view-mutation`,
`configure-no-effect-and-undo-conflict`, `axisless-history`,
`matrix-retirement`, `roadmap-proof`, `move-updates-every-roadmap-scope`,
`roadmap-outlives-its-work`, `roadmap-opened-after-the-window`.

## E05 — The savepoint restore session (6 cards)

Spec: `:2272-2276`, `:6119-6123`, `:6391-6472`.
Rows: `savepoint create`; `savepoint list`; `savepoint verify`; `savepoint
restore` as the isolated read-only session; `savepoint compare`; journal
rotation and compaction.
Replays: `savepoint-cut-never-splits-an-envelope`,
`savepoint-write-failure-keeps-the-previous`, `a-savepoint-is-not-a-shared-backup`,
`restore-is-isolated-and-dispatches-nothing`, `copied-journal-grants-nothing`,
`rotation-keeps-one-journal`, `compaction-keeps-the-last-copy`,
`torn-tail-is-diagnosed-not-truncated`,
`reply-retired-only-under-verified-coverage`.

## E06 — Undo and redo over external effects (5 cards)

Spec: `:2277-2280`, `:2827-2900`, `:5595-5606`, `:6366`.
Rows: `undo-plan`; `undo`; `redo-plan`; `redo`; the external-effect refusal
that names its handle and never rewinds local state.
Replays: `undo-appends-and-preserves`, `redo-refuses-a-stale-plan`,
`undo-refuses-an-external-effect`, `undo-names-its-reversible-set`,
`undo-redo`, `no-effect-mutation-is-journaled`, `dry-run-writes-nothing`.

## E07 — The reconcile and index surface over receipts (5 cards)

Spec: `:2309`, `:3980-4019`, `:526`, `:5538-5568`, `:5685-5688`,
`:6141-6145`, `:6363`.
Rows: closed-history index paging (`query --branch closed|root`); the
`reconcile` surface over operation receipts; `execution reconcile` over
observations; the receipt ledger and reply retirement; the
indexes-and-counters reconstruction.
Replays: `closed-paged-without-full-load`, `closed-row-with-archive-absent`,
`busy-day-many-segments`, `history-grows-startup-does-not`,
`reconcile-preserves-contradiction`, `reply-retired-only-under-verified-coverage`,
`late-and-duplicate-receipts-are-retained`, `indexes-and-counters`,
`index-replayed-after-crash`.

## E08 — The render targets (4 cards)

Spec: `:2313`, `:3080-3085`, `:3131-3156`, `:5304`, `:5398`, `:5853`.
Rows: the `render --chat` artifact; the `render --file` marker region;
`render --check`; the stored projections and their permitted-root mappings.
Replays: `chat-and-file-render-are-byte-identical`,
`render-artifact-is-bounded`, `render-refuses-a-target-outside-its-roots`,
`a-root-id-grants-nothing`.

## E09 — The fleet views (7 cards)

Spec: `:2288-2289`, `:2311-2312`, `:2334-2335`, `:3459-3588`,
`:3592-3731`, `:3585-3588`, `:6208-6314`.
Rows: `query --ask fleet`; `query --ask routes`; the `machine` verb; the
`route` verb; the `probe` verb; `take`/`heartbeat`/`release` allocation; the
`fleet` recommendation's `--for`.
Replays: `fleet-is-static-config`, `fleet-for-is-a-recommendation-not-a-lease`,
`an-excluded-choice-is-refused-not-empty`,
`machine-is-config-and-never-a-work-tree-node`, `no-credential-in-a-member`,
`route-config-lists-key-by-path-never-value`, `routing-picks-flat-before-metered`,
`unprobed-route-carries-no-card`, `three-abstains-bench-until-probe`,
`allocation-binds-machine-slot-generation`,
`allocation-take-is-atomic-and-idempotent`,
`expiry-marks-suspect-reuse-needs-fencing`,
`release-one-allocation-spares-the-other`, `stale-allocation-id-refused-by-name`,
`preparation-interrupted-before-launch`, `concurrent-slots-refuse-third-job`,
`one-allocator-per-machine-aliases-share-nested-conserve`,
`probe-records-observed-active-and-touches-no-config`.

totals: epics=9 rows=89 cards=55
