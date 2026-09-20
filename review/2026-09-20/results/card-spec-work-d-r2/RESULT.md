RESULT spec-work-d-r2 sha=5298f6be12ea — SPEC-WORK.md has no "Tests this spec demands" section. Read the spec, say which demanded behaviours the tree already tests and which it does not, and draft the issues for the ones it does not.
DONE

<<<SECTION
## Tests this spec demands

All tests run against a **fake kernel** (in-memory Lisp engine, no socket, no I/O) or a **throwaway session directory** with a spawned daemon and `cmd/nova-work` subprocess; nothing reaches a real network, repository, secret store or machine fleet. Every test is proven able to red by a mutation before it is trusted. Scope: lines 2644–3834 of `docs/SPEC-WORK.md`, covering six subsections — the engine and its client, structural verbs (#293 fold), Friends/CONFIG/ACTIVE, the fleet, fleet allocation (#500), and model routes (#500).

### The engine and its client (docs/SPEC-WORK.md:2644–2973)

1. work-d-1 `endpoint-is-local-and-private` — the CLI connects through a local socket or named pipe only; 0700 directory, 0600 socket; no network listener; reaching the socket is not coordinator authority.
2. work-d-2 `wire-integers-are-strings` — every integer on the wire is a JSON decimal-string; a JSON number is refused.
3. work-d-3 `absent-empty-and-null-are-three-spellings` — absent key, JSON null, empty string and empty array produce distinct digest values.
4. work-d-4 `protocol-version-negotiated-or-refused` — hello is the first frame; an unsupported version is refused before any other request.
5. work-d-5 `pipeline-replies-are-correlated` — replies are matched by request id, not arrival order; an unknown/duplicate/missing id is a protocol error.
6. work-d-6 `disconnect-is-not-a-rollback` — a socket disconnect never cancels an accepted mutation; retry with identical payload id and body repeats the recorded disposition.
7. work-d-7 `no-effect-mutation-is-journaled` — an accepted typed event with changed=0 is journaled and advances the event revision.
8. work-d-8 `operation-survives-the-client` — a long operation returns a durable id before it is printed; the CLI may exit while work continues.
9. work-d-9 `cancel-is-a-request-not-an-erasure` — cancel requires write flags, its own request id, is deduplicated; it cannot erase an accepted mutation or undo an external effect.
10. work-d-10 `status-answers-while-io-runs` — status, cancellation and lease renewal are measurable under load; no unbounded scan or network wait holds the mutation loop.
11. work-d-11 `clip-is-one-long-operation` — clip prints OPERATION OK at once; CLIP OK arrives via operation wait; session stop waits by the same path.
12. work-d-12 `batches-and-pipelines` suite (B1–B5) — read bundles are one-revision; independent batch stops at first refusal by default; atomic batch is all-or-none with no I/O inside; ids are stable across retry; entries are typed verbs only.

### Structural verbs, roadmap, priority and state export (#293 fold) (docs/SPEC-WORK.md:2974–3312)

13. work-d-13 `undo-appends-and-preserves` — undo appends a typed compensating envelope; the original event stays in place; redo reapplies intent against current preconditions.
14. work-d-14 `redo-refuses-a-stale-plan` — a stale or conflicting plan refuses atomically, naming what changed.
15. work-d-15 `undo-refuses-an-external-effect` — a sent message, paid execution, publication or deletion that is irreversible or uncertain is refused.
16. work-d-16 `undo-names-its-reversible-set` — the reversible-verb table names every reversible verb; the rest are refused by name with `not reversible here`.
17. work-d-17 `every-field-has-an-owning-verb` — every canonical field maps to an owning typed mutation; no generic set-field escape hatch bypasses an invariant.
18. work-d-18 `add-field-order-is-complete` — node add serializes all twelve fields in order; the shorter pre-fold shape is refused `schema revision unsupported`.
19. work-d-19 `repo-only-at-the-root` — --repo is required with --under-root open and refused anywhere else.
20. work-d-20 `roadmap-has-one-creator` — roadmap create is the sole creator; node add --type roadmap is refused.
21. work-d-21 `metadata-patches-preserve-intent` — node edit uses tagged patches (keep, clear, set); an all-keep request is refused.
22. work-d-22 `edit-is-atomic-and-replayable` — a bad one-of-five patch writes nothing; changed=0 is the no-effect receipt.
23. work-d-23 `edit-undo-preserves-later-work` — undo of node edit restores the preimage only while current metadata matches the postimage; an intervening edit is a conflict.
24. work-d-24 `edit-never-fetches-a-link` — a link is never fetched, executed, normalised or given any access.
25. work-d-25 `move-keeps-every-count` — node move keeps |O|, |C|, W, state, acceptance, generation, source revision, lease and attempt pointers unchanged.
26. work-d-26 `move-same-parent-is-a-receipt` — a request whose --from equals --under and names the actual parent is the no-effect receipt, changed=0.
27. work-d-27 `move-refuses-by-name` — node move refuses with named reasons: parent conflict, cycle, not movable, repository change, etc.
28. work-d-28 `move-keeps-the-lease` — move preserves live leases and unreconciled attempts under the moved subtree.
29. work-d-29 `move-updates-every-roadmap-scope` — move updates the scope revision of every roadmap that has the node as a row.
30. work-d-30 `move-undo-refuses-a-reorder` — undo of move refuses conflict on an intervening reorder, reparent or privacy change.
31. work-d-31 `axisless-history` — zero- or one-axis roadmap works; percent --node on it takes no --axis.
32. work-d-32 `matrix-retirement` — axis --remove retires a member from its axis and from every current cell; on the first axis it also removes the row from the required set.
33. work-d-33 `configure-no-effect-and-undo-conflict` — an equal-value configure is the no-effect receipt; undo of a configure/row/projection change compares current state with stored after-image.
34. work-d-34 `completed-view-mutation` — a settled roadmap keeps its head (axes, members, cells, projections); it opens from the head plus bounded closed reads.
35. work-d-35 `chat-and-file-render-are-byte-identical` — chat and file renders of the same projection produce identical output bytes.
36. work-d-36 `render-refuses-a-target-outside-its-roots` — a path outside the effective root, a symlink escape, or a mapping whose repo identity does not match the projection's stored repo is refused.
37. work-d-37 `render-artifact-is-bounded` — a successful --chat render returns a single bounded artifact; a frame-past-bound or output-past-bound refusal is never truncated.
38. work-d-38 `a-root-id-grants-nothing` — a stored root id grants no filesystem access; file mode needs both the stored permission and a session-start mapping.
39. work-d-39 `priority-orders-only-the-eligible` — query --ask ready --order priority sorts only rows that ready already admits; it grants no capacity and starts no work.
40. work-d-40 `priority-inherits-and-clears` — effective rank is the nearest context (self, then subtree, then default); a clear reveals the next ancestor.
41. work-d-41 `rank-2-precedes-10` — rank 2 sorts before rank 10 (unsigned integer, not lexical).
42. work-d-42 `priority-undo-is-history-not-value` — undo of priority is keyed by the latest-change event id, not by the rank value.
43. work-d-43 `priority-grants-nothing` — priority selects no worker, takes no lease, changes no responsibility, starts no work.
44. work-d-44 `state-export-describes-exactly-r` — session export --state captures and pins the named revision; the manifest's snapshot reference binds base revision B and journal interval reaching R.
45. work-d-45 `state-export-is-one-long-operation` — the resident export is acknowledged at once with OPERATION OK; EXPORT OK arrives via operation wait.
46. work-d-46 `state-export-pin-survives-clip` — the revision is pinned until the export ends; concurrent clip or retention reclaims nothing under it.
47. work-d-47 `state-export-disconnect-and-cancel` — cancellation acknowledges then reconciles; a disconnect cancels nothing; recovery resolves the id.
48. work-d-48 `state-export-refuses-a-gap` — a missing cut, gap, wrong end, split envelope or absent end below R refuses; closure is not selection.
49. work-d-49 `state-load-is-isolated` — state load is a finite process: it verifies the manifest, materialises one directory, prints LOAD OK, and exits; it starts no daemon and creates no writable journal.
50. work-d-50 `fenced-export-can-finish` — a fenced session runs the export and reaches its terminal line through the operation verbs.

### Friends, CONFIG and ACTIVE (docs/SPEC-WORK.md:3313–3469)

51. work-d-51 `no-friend-name-in-the-tool` — no friend name, model name or role is hardcoded into nova-work.
52. work-d-52 `roles-are-configured-not-inferred` — a role is configured, never inferred; a model's capability never cancels a friend's agreed limits.
53. work-d-53 `reserved-role-is-not-spent-on-routine-work` — a role reserved for essential security work only is not spent on routine work.
54. work-d-54 `four-capability-groups-and-three-fields` — each friend may expose child-agent, swarm, local-model and one-shot groups; each entry has declared support, verified runtime and free capacity as three fields.
55. work-d-55 `dispatch-ack-and-ownership-are-three` — dispatch records intent; a pending offer reserves only explicitly declared capacity; a correction retains lineage.
56. work-d-56 `requested-model-is-not-observed-model` — the requested model and the observed model are two fields; a friend's usual model is not proof of what executed.
57. work-d-57 `a-retry-does-not-overwrite-its-attempt` — a retry never overwrites the attempt before it; concurrent attempts keep separate attribution.
58. work-d-58 `silence-is-a-ping-not-a-verdict` — a configured silence threshold triggers one bounded availability ping; a failed probe is unresolved delivery, not a failed friend.
59. work-d-59 `explicit-rest-is-not-pinged` — explicit rest is respected; a resting friend is not pinged.
60. work-d-60 `return-reconciles-before-dispatch` — a return reconciles outstanding assignments and observed capacity before any new dispatch.
61. work-d-61 `unchanged-config-is-one-bounded-answer` — a config request with matching hash returns UNCHANGED; otherwise a bounded delta or full manifest.
62. work-d-62 `an-invalid-delta-leaves-the-old-config` — a changed fragment is validated before application; an invalid delta leaves the old config unchanged.
63. work-d-63 `a-partial-manifest-is-refused` — a partial config is never admitted as a complete replacement.
64. work-d-64 `prompt-profile-expired-shows-on-the-status-line` — profile-state=stale, absent, mismatch or unknown is printed on the status line.

### The fleet (docs/SPEC-WORK.md:3470–3600)

65. work-d-65 `fleet-is-static-config` — a :machine event moves no count, roadmap or required set; a heartbeat, observe or probe changes no member.
66. work-d-66 `no-machine-name-in-the-tool` — no hostname, alias, user, path, architecture or core count is a constant in the product.
67. work-d-67 `one-profile-one-unit` — one connection profile is one unit; a --register whose --connect is a profile already in the fleet is refused `connect held by <id>`.
68. work-d-68 `no-credential-in-a-member` — no key, token, password or secret is ever in a record, manifest or clip; a credential-like field is refused `credential in record`.
69. work-d-69 `unknown-owner-is-refused` — a machine with no owner, no id, an owner not in friends, or a fact without provenance is refused at exit 1.
70. work-d-70 `fleet-for-is-a-recommendation-not-a-lease` — query --ask fleet --for is a recommendation from declared facts; it reserves nothing and dispatches nothing.
71. work-d-71 `an-excluded-choice-is-refused-not-empty` — asking fleet --for with --node on a member that excludes the kind prints QUERY FAIL, never an empty answer.

### Fleet allocation — SPEC-AHEAD: #500 (docs/SPEC-WORK.md:3601–3740)

72. work-d-72 `machine-is-config-and-never-a-work-tree-node` — a machine is equipment; no :acceptance, no :to :done, no settle; equipment does not complete.
73. work-d-73 `allocation-binds-machine-slot-generation` — an ACTIVE allocation binds machine, slot, generation, batch, node and (offer, attempt); admission happens before preparation.
74. work-d-74 `allocation-take-is-atomic-and-idempotent` — take writes all-or-none; a retry answers the original line; a changed payload is refused; heartbeat/release validate both generations.
75. work-d-75 `expiry-marks-suspect-reuse-needs-fencing` — expiry marks the allocation suspect; renewal with the stale token is refused; capacity returns only on confirmed termination.
76. work-d-76 `probe-records-observed-active-and-touches-no-config` — a probe writes ACTIVE evidence with date, source and stamp; it never writes CONFIG.
77. work-d-77 `one-allocator-per-machine-aliases-share-nested-conserve` — one authoritative allocator per physical machine; aliases share identity; nested quotas conserve capacity; capacity reduction preserves active work.

### Model routes — SPEC-AHEAD: #500 (docs/SPEC-WORK.md:3741–3834)

78. work-d-78 `route-config-lists-key-by-path-never-value` — a route is a :kind :route CONFIG member; :key-location is a path or env name, never a key value.
79. work-d-79 `routing-picks-flat-before-metered` — routing order is cheapest first: flat before free before metered, then by cost-per-mtok ascending, then by stable id.
80. work-d-80 `unprobed-route-carries-no-card` — a route with no passing probe or a live :benched-until carries no card for its class.
81. work-d-81 `three-abstains-bench-until-probe` — three consecutive abstains bench a route until the next probe passes and clears it.
SECTION
<<<TABLE
1	work-d-1	endpoint-is-local-and-private	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	197,862	CLI is local-socket only; 0700/0600; no network listener
2	work-d-2	wire-integers-are-strings	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	wire integers are JSON strings; JSON number refused
3	work-d-3	absent-empty-and-null-are-three-spellings	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	absent key, null, empty string, empty array distinct digests
4	work-d-4	protocol-version-negotiated-or-refused	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	hello is first frame; unsupported version refused
5	work-d-5	pipeline-replies-are-correlated	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	replies matched by request id; unknown id is protocol error
6	work-d-6	disconnect-is-not-a-rollback	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	socket disconnect never cancels accepted mutation
7	work-d-7	no-effect-mutation-is-journaled	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	changed=0 is journaled and advances event revision
8	work-d-8	operation-survives-the-client	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	long op returns durable id before printing
9	work-d-9	cancel-is-a-request-not-an-erasure	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	cancel requires write flags; deduplicated
10	work-d-10	status-answers-while-io-runs	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	status measurable under load
11	work-d-11	clip-is-one-long-operation	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	clip prints OPERATION OK; CLIP OK via operation wait
12	work-d-12	batches-and-pipelines	PRESENT	lisp/nova-work/tests/replays-8642.lisp	—	B1-B5: bundles, independent, atomic, ids, typed verbs
13	work-d-13	undo-appends-and-preserves	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	undo appends compensating envelope
14	work-d-14	redo-refuses-a-stale-plan	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	stale plan refuses atomically
15	work-d-15	undo-refuses-an-external-effect	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	irreversible external effect is refused
16	work-d-16	undo-names-its-reversible-set	PRESENT	lisp/nova-work/tests/replays-8651.lisp	—	reversible-verb table; rest refused
17	work-d-17	every-field-has-an-owning-verb	PRESENT	lisp/nova-work/tests/replays-8661.lisp	—	every field maps to owning verb
18	work-d-18	add-field-order-is-complete	PRESENT	lisp/nova-work/tests/replays-8641.lisp	—	node add serialises 12 fields
19	work-d-19	repo-only-at-the-root	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	--repo required at root only
20	work-d-20	roadmap-has-one-creator	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	roadmap create is sole creator
21	work-d-21	metadata-patches-preserve-intent	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	tagged patches keep/clear/set
22	work-d-22	edit-is-atomic-and-replayable	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	bad patch writes nothing
23	work-d-23	edit-undo-preserves-later-work	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	1892	undo restores preimage only under match
24	work-d-24	edit-never-fetches-a-link	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	1926	link never fetched or executed
25	work-d-25	move-keeps-every-count	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	counts, state, lease unchanged by move
26	work-d-26	move-same-parent-is-a-receipt	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	1193	--from equals --under is no-effect
27	work-d-27	move-refuses-by-name	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	1193	move refuses with named reasons
28	work-d-28	move-keeps-the-lease	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	leases preserved under move
29	work-d-29	move-updates-every-roadmap-scope	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	move updates roadmap scope revisions
30	work-d-30	move-undo-refuses-a-reorder	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	1269	undo refuses conflict on intervening change
31	work-d-31	axisless-history	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	zero-/one-axis roadmap works
32	work-d-32	matrix-retirement	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	axis --remove retires member
33	work-d-33	configure-no-effect-and-undo-conflict	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	equal-value configure is no-effect
34	work-d-34	completed-view-mutation	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	settled roadmap keeps head
35	work-d-35	chat-and-file-render-are-byte-identical	PRESENT	lisp/nova-work/tests/acceptance/slice-09-replays-roadmap.lisp	—	chat and file renders identical bytes
36	work-d-36	render-refuses-a-target-outside-its-roots	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	path outside root refused
37	work-d-37	render-artifact-is-bounded	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	chat render returns bounded artifact
38	work-d-38	a-root-id-grants-nothing	PRESENT	lisp/nova-work/tests/acceptance/slice-07-replays-mid.lisp	—	root id grants no filesystem access
39	work-d-39	priority-orders-only-the-eligible	PRESENT	lisp/nova-work/tests/acceptance/slice-06-replays-early.lisp	—	priority sorts only ready rows
40	work-d-40	priority-inherits-and-clears	PRESENT	lisp/nova-work/tests/acceptance/slice-06-replays-early.lisp	—	effective rank is nearest context
41	work-d-41	rank-2-precedes-10	PRESENT	lisp/nova-work/tests/acceptance/slice-06-replays-early.lisp	—	rank 2 sorts before rank 10
42	work-d-42	priority-undo-is-history-not-value	PRESENT	lisp/nova-work/tests/acceptance/slice-06-replays-early.lisp	—	undo keyed by event id, not value
43	work-d-43	priority-grants-nothing	PRESENT	lisp/nova-work/tests/acceptance/slice-06-replays-early.lisp	—	priority selects no worker
44	work-d-44	state-export-describes-exactly-r	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	export pins named revision
45	work-d-45	state-export-is-one-long-operation	PRESENT	lisp/nova-work/tests/acceptance/slice-08-replays-late.lisp	—	export is long operation
46	work-d-46	state-export-pin-survives-clip	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	revision pinned; clip reclaims nothing
47	work-d-47	state-export-disconnect-and-cancel	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	cancel reconciles; disconnect cancels nothing
48	work-d-48	state-export-refuses-a-gap	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	gap, missing cut refuses
49	work-d-49	state-load-is-isolated	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	load verifies manifest; no daemon
50	work-d-50	fenced-export-can-finish	PRESENT	lisp/nova-work/tests/acceptance/slice-09-state-export-replays.lisp	—	fenced session runs export
51	work-d-51	no-friend-name-in-the-tool	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	no friend/model/role hardcoded
52	work-d-52	roles-are-configured-not-inferred	PRESENT	lisp/nova-work/tests/replays-8661.lisp	—	role configured; capability never cancels limits
53	work-d-53	reserved-role-is-not-spent-on-routine-work	PRESENT	lisp/nova-work/tests/replays-8661.lisp	—	reserved role not spent on routine
54	work-d-54	four-capability-groups-and-three-fields	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	four groups; declared/verified/free
55	work-d-55	dispatch-ack-and-ownership-are-three	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	dispatch, ack, ownership are three facts
56	work-d-56	requested-model-is-not-observed-model	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	requested vs observed are two fields
57	work-d-57	a-retry-does-not-overwrite-its-attempt	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	retry does not overwrite prior
58	work-d-58	silence-is-a-ping-not-a-verdict	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	silence pings; failed probe not verdict
59	work-d-59	explicit-rest-is-not-pinged	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	explicit rest respected
60	work-d-60	return-reconciles-before-dispatch	PRESENT	lisp/nova-work/tests/replays-8663.lisp	—	return reconciles before dispatch
61	work-d-61	unchanged-config-is-one-bounded-answer	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	matching hash returns UNCHANGED
62	work-d-62	an-invalid-delta-leaves-the-old-config	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	invalid delta leaves old config
63	work-d-63	a-partial-manifest-is-refused	PRESENT	lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp	—	partial manifest refused
64	work-d-64	prompt-profile-expired-shows-on-the-status-line	PRESENT	lisp/nova-work/tests/replays-8661.lisp	—	profile-state on status line
65	work-d-65	fleet-is-static-config	PRESENT	lisp/nova-work/tests/acceptance/slice-10-fleet.lisp	—	:machine event moves no count
66	work-d-66	no-machine-name-in-the-tool	PRESENT	lisp/nova-work/tests/acceptance/slice-10-fleet.lisp	—	no host constant in product
67	work-d-67	one-profile-one-unit	PRESENT	lisp/nova-work/tests/acceptance/slice-10-fleet.lisp	—	one profile is one unit
68	work-d-68	no-credential-in-a-member	PRESENT	lisp/nova-work/tests/acceptance/slice-10-fleet.lisp	—	no key/token/secret in record
69	work-d-69	unknown-owner-is-refused	PRESENT	lisp/nova-work/tests/acceptance/slice-10-fleet.lisp	—	no owner, no id, unknown owner refused
70	work-d-70	fleet-for-is-a-recommendation-not-a-lease	PRESENT	lisp/nova-work/tests/acceptance/slice-09-fleet-assignment.lisp	—	fleet --for recommends; reserves nothing
71	work-d-71	an-excluded-choice-is-refused-not-empty	PRESENT	lisp/nova-work/tests/acceptance/slice-09-fleet-assignment.lisp	—	--for with excluded member: QUERY FAIL
72	work-d-72	machine-is-config-and-never-a-work-tree-node	PRESENT	lisp/nova-work/tests/replays-8661.lisp	—	machine is equipment; no settle
73	work-d-73	allocation-binds-machine-slot-generation	PRESENT	lisp/nova-work/tests/replays-8662.lisp	—	ACTIVE allocation binds machine/slot/generation
74	work-d-74	allocation-take-is-atomic-and-idempotent	PRESENT	lisp/nova-work/tests/replays-8662.lisp	—	take all-or-none; retry original
75	work-d-75	expiry-marks-suspect-reuse-needs-fencing	PRESENT	lisp/nova-work/tests/replays-8662.lisp	—	expiry marks suspect; stale token refused
76	work-d-76	probe-records-observed-active-and-touches-no-config	PRESENT	lisp/nova-work/tests/replays-8662.lisp	—	probe writes ACTIVE; never CONFIG
77	work-d-77	one-allocator-per-machine-aliases-share-nested-conserve	PRESENT	lisp/nova-work/tests/replays-8662.lisp	—	one allocator per machine; aliases share
78	work-d-78	route-config-lists-key-by-path-never-value	PRESENT	lisp/nova-work/tests/acceptance/slice-10-routes.lisp	—	route is CONFIG; :key-location is path/env
79	work-d-79	routing-picks-flat-before-metered	PRESENT	lisp/nova-work/tests/acceptance/slice-10-routes.lisp	—	cheapest-first; n listings = n shares
80	work-d-80	unprobed-route-carries-no-card	PRESENT	lisp/nova-work/tests/acceptance/slice-10-routes.lisp	—	no-pass route carries no card
81	work-d-81	three-abstains-bench-until-probe	PRESENT	lisp/nova-work/tests/acceptance/slice-10-routes.lisp	—	three abstains bench until pass clears
TABLE
<<<ISSUES
<none> — every demanded behaviour in scope (lines 2644–3834) has at least one active acceptance `deftest` in the repository. No ABSENT behaviours found; no issues to draft.
ISSUES
<<<READINESS
VERDICT: READY
All 81 demanded behaviours enumerated across the six scope subsections are PRESENT in the tree as active `deftest` acceptance replays. Behaviours 72–81 (Fleet allocation and Model routes) carry SPEC-AHEAD: #500 markers — the production code for those sections is a draft, but the acceptance tests already exist in the replay system. No behaviour lacks a test, so no card needs to be cut from this scope. The next card for this spec is outside this scope (lines 3835+ of SPEC-WORK.md).
READINESS
