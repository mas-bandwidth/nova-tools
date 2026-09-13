# nova-tokens retained records: decision draft 1

Status: proposed replacement accounting contract for #117; not a claim about the shipped binary. Read with the [shared adoption proposal](https://github.com/mas-bandwidth/nova-tools/issues/117#issuecomment-5646457908). New adapters wait for the shared contract and their source mappings to be reviewed. Existing v1 day files remain readable and unchanged.

[Smart scheduling and virtual token cost](PROPOSAL-SCHEDULING-COST.md) adds a derived cost view over these retained observations: configurable weights per model and token category, complete task cost including review/rework, and scheduling within quality, availability and shared-budget constraints. It does not replace raw usage with priced totals.

## Result and compatibility boundary

Keep usage evidence before aggregation. A daily or monthly report can group by UTC day, friend, execution bench, repository and model with its evidence basis without losing any of those dimensions. Different reports use the same retained records. Collection and reporting run mechanically; interpretation and anomalous mappings deserve thought.

[SPEC-TOKENS v1](SPEC-TOKENS.md) deliberately discards friend, bench and event identity in its day rows; its repo rule selects the first matching tool path and carries the previous repo forward; it provides no guarantee of excluding overlapping sources and disallows Git publication. The implementation warns about some shared IDs but still sums both declarations. Those are explicit contracts, not incidental implementation details. This proposal changes them through a separately versioned record/report interface. Do not silently reinterpret old TSVs as detailed records or put overlapping counters into v1's five-type Total().

V1 imports, if needed, are labelled aggregate observations with their original dimensions and declared source coverage. They cannot recover missing event identity, friend, bench or per-message provenance. Where original evidence exists, backfill that evidence instead. An aggregate and its underlying events never both enter a total.

## Three retained objects

1. **Observation:** allowlisted numeric usage fields and the metadata needed to interpret them, at the source's finest available grain. It is immutable. It can describe a per-request result, a streamed revision, a cumulative snapshot or an aggregate; the kind is explicit. An observation is not automatically an additional spend event.
2. **Mapping:** a versioned, reviewed description of event identity, counter meanings, model and time semantics, duplicate/revision handling and safe source extraction. It references sanitized fixtures and their expected results. A changed mapping produces new derived results, without rewriting the observations.
3. **Coverage contribution:** the explicit source scope and interval inspected on one bench, with immutable identity, collection time/build, observation identities or a shard digest, and complete-within-scope / partial / unavailable status with reasons. It proves what was inspected, not that every possible session on a machine exists in that scope.

Reporting selects a consistent set of observations under a named mapping version and computes spend events. It records its input contribution set and mapping revisions so the view can be reproduced later. Derived views may be regenerated; original observations and coverage revisions are retained.

## Required observation fields and unknowns

| Field | Meaning |
|---|---|
| `schema` | Explicit record-format version; unsupported versions are reported and excluded, never guessed. |
| `observation_id` | Deterministic identity of this exact usage observation; stable across retries and byte-equivalent copies. |
| `source_kind`, `source_version` | Harness/export shape and known producer/schema version; unavailable producer version is explicit. |
| `source_id`, `event_key` | `event_key` is a nonempty array of native identity strings establishing spend identity within its namespace; `source_id` is provenance. A fork's containing session creates no fresh spend. Collection paths and benches do not define identity. |
| `kind`, `revision` | Per-request, cumulative, streamed revision or aggregate; source ordering/revision evidence as the mapping defines it. |
| `occurred_at`, `interval`, `day_basis` | Original event time or measured interval; a zoned daily aggregate keeps its actual basis. Unknown time is not collection time. |
| `friend`, `execution_bench` | Whose work and where it ran, with provenance. `studio` and `air` are labels, not inferred from whichever host now holds a copy. Unknown is explicit. |
| `model` | Source model ID with provider-reported, harness-reported, requested, mixed or unknown basis; provider namespace if needed. Available requested/harness model views are useful without claiming independent server verification. Mixed-model aggregate stays mixed unless the source gives a split. |
| `repository`, `attribution` | Repository identity and the evidence/rule that assigned it, or `unattributed`. Touched repositories can be a separate list with no numeric allocation. |
| `raw_usage` | Allowlisted original numeric field names, values and units, including unknown/absent distinction. No serialized transcript object. |
| `mapping_id`, `source_receipt` | Mapping revision and portable metadata-only receipt needed to trace this observation locally. Private paths stay in a local side index. |

The [format and command decision packet](PROPOSAL-TOKENS-FORMAT.md) proposes exact encoding, identifiers and a separate records command namespace for review. Arbitrary extension objects are not a route for private prompt content. A source with no stable native event identifier needs an explicitly reviewed deterministic identity method; ambiguity is a conflict, not permission to count twice.

## Identity, revisions and overlap

Use the source's stable session/request identity, not a filename, Git author, current host or collector invocation. Distinguish an observation identity from the spend-event identity it updates. The same event can have several streamed observations; the accepted final revision counts once while all observations survive. A repeated identical observation is an idempotent no-op.

A deterministic digest can name allowlisted canonical metadata and numeric content; it is not an authenticity or authorship claim. The mapping defines the canonical bytes and which native fields establish event identity and order. If a source rewrites or renumbers its records, that behavior must be covered before its positional keys are accepted.

Conflicting values for one event without an evidenced revision order produce a visible conflict. No last-Git-commit-wins, file-order-wins or maximum-counter-wins rule. An explicit correction names every conflicting predecessor it resolves and retains each predecessor. Cyclic or incomplete supersession chains fail validation.

Copying Studio sessions to Air preserves source identity and Studio origin. A later evidenced bench correction supersedes the earlier provenance rather than duplicating spend. Source and wrapper overlaps, such as an OpenCode session and its nova-swarm job total, need a declared relationship and one selected counting source. If no relationship can be proved, incompatible scopes are refused for a combined report; the report names the unresolved overlap.

## Counter and time semantics

Raw numbers are kept unchanged. Each mapping states whether input includes cache read/write and whether output includes reasoning. A missing type is unknown, never zero. Integer overflow, negative counts and impossible subset relationships are explicit failures.

For comparable reports, propose **total input including cache** and **total output including reasoning**, with cache-read, cache-write and reasoning shown separately as subsets where measured. These totals are derived only when the mapping proves them: a source with exclusive input may need addition of its cache counts; a source with inclusive input must not add them again. Unknown component relationships leave the affected normalized total unknown. Never add the five displayed columns to produce a grand total. A spend total is input-total plus output-total only where both are known; otherwise display known components and coverage, not a falsely complete scalar.

Example with an independently specified inclusive mapping: raw input 1000, cache-read 800, output 100, reasoning 40 means input-total 1000 and output-total 100, hence 1100 total, with 800 and 40 displayed as subsets. It does not mean 1940. This is a synthetic arithmetic fixture, not proof that a particular harness uses that mapping.

Per-request usage counts once. Cumulative snapshots are retained as snapshots; derive differences only within an evidenced monotonic scope/model/counter epoch. Repeated totals are not additional spend. A decrease, reset, model switch or missing predecessor cannot silently create a new zero baseline. A trustworthy source-provided last-request delta can be used when its identity and scope are verified; it is never added to the cumulative difference for the same event.

A cumulative difference spanning midnight belongs to the measured interval unless the source provides finer timestamps. Do not put the entire difference on its final day and call that an accurate UTC daily allocation. The monthly view can report an interval total within its month while the daily allocation remains unknown; intervals crossing the month boundary stay unallocated unless evidence supplies the split. Original event time and collection time are separate.

Antigravity field 1.4.5 is owner-interpreted as context-window size; producer symbols name it GetTotalTokens but do not establish that interpretation. Preserve it provisionally and do not add it to spend. Codex Desktop's supported top-level token_usage_record supplies per-response usage; turn/thread totals and token_count snapshots are not additional spend. Grok costUsdTicks remains a raw value with unverified unit, not dollars. Source-specific mappings document these distinctions; uncertain semantics need not block retaining supported raw evidence.

## Repository attribution

Use an explicit source task/repository binding or another reviewed event attribution rule. A cwd, first tool path, inherited previous repo or touched list can be retained as labelled attribution evidence; it is not a measured division of a request that worked across repositories. The safe default for ambiguous work is `unattributed`. No proportional split by paths, elapsed time or model judgment.

An operator may select an explicit accounting policy such as charging a whole bound task to one repo, but the report must name that policy and distinguish it from measured attribution. The first shared report should use verified bindings and unattributed counts; no quiet migration of v1's heuristic into a claim of measured usage. Private repository paths need not leave the bench; canonical repository labels suffice.

## Shared private Git publication

The agreed private ledger is provisioned; the record contract is still under review. Use existing authorized access. The `friend` field identifies usage, independently of Git commit authorship. This design neither creates accounts nor changes credentials or access controls.

Observations and coverage contributions use the immutable shard-digest and coverage-digest names specified in the format packet. Record paths partition original execution friend/bench and honest day allocation; coverage paths describe collection provenance, with the inspected source interval explicit in the body. Undated/interval evidence has an explicit unallocated location, never a guessed day. A fixed coverage filename can only be a generated index, not the sole retained history. Publication validates the entire contribution before adding only its named files.

A bounded publisher stages an immutable batch and pushes one atomic commit; on a race it fetches and retries while preserving both writers. Identical identities/content are already-published; conflicting content is refused. Dirty unrelated work is preserved, there is no force push, reset, clean, removal or broad staging. Ambiguous push results are resolved by querying exact contribution identity before retrying. No blind replay with a fresh identity.

The accounting reader remains read-only. The format packet selects the separate `nova-tokens records publish` verb, which preserves that boundary and exposes its exact writes. No hidden network access in collect/report/check.

The ledger and total reports remain private. Any future public report requires an explicit reviewed allowlist of open-source repositories, removes friend/bench/source identifiers unless deliberately included, excludes unattributed/private work and is published only by a separate deliberate step. A repository label alone is not a publication grant. No autonomous public export is part of daily collection.

## September coverage and daily operation

Each friend chooses their compatible local extraction/scheduling method. Inspect their own authorized source scope once for September 1 through collection start, including the previous week; collect on other benches when that bench is available and authorized. Preserve originals. Record coverage per friend/bench/day even when partial or unavailable. A missing source/day is not a zero day. Live sessions are partial until the declared cut-off and completeness conditions are met.

After backfill, a collector may reuse validated receipts/indexes to avoid repeated whole-history scans; correctness on append, truncation, rewrite and schema change must be explicit before claiming incremental completeness. No model turn is required for unchanged collection, checking or publication. Errors can be deduplicated locally and surfaced through the friend's chosen notification path.

Daily and monthly views carry mapping version, selected contributions, covered interval, unknown fields, conflicts, unresolved overlap and unattributed counts. They can group or filter without deleting the underlying dimensions. September accuracy means honest coverage with recomputable totals, not filling every calendar cell with a number.

## Acceptance evidence before adoption

- Independent numeric fixtures for every source mapping: inclusive/exclusive cache, reasoning overlap, absent/zero fields, model changes, timestamp basis and cumulative resets. No paid model runs just to generate usage.
- The same source copied to another bench, ingested by backfill and live collection, counts once; correcting its origin also counts once. Different real events with equal values both survive.
- Stream revisions, reordered files, duplicate IDs with conflicting values, unresolved wrapper/source overlap and supersession forks produce the specified selection or visible refusal.
- A midnight cumulative interval and a cross-month interval retain truthful allocation; no timestamp invention. A mixed-repo task remains unattributed unless an explicit policy is selected and shown.
- Two Git writers racing preserve both contributions. Lost push acknowledgment followed by retry is idempotent. Conflicting identities fail without overwriting history. Dirty unrelated files and original sources remain byte-identical.
- Secret/prompt/private-path sentinels in non-usage source fields never enter published records or diagnostics. Public subset generation excludes non-allowlisted and unattributed records; it does not publish itself.
- Recompute two different reports from the same retained observations and reproduce them from their input/mapping manifests. Corrupt or missing contributions make the corresponding coverage incomplete.
- Each friend can describe and demonstrate their chosen collection path with its actual build/mapping IDs, source scope and first retained contribution. Installed-only and manually typed counts do not satisfy this endpoint.
