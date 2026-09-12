# Retained token records: format and command decision packet

Proposed companion to [the record contract](PROPOSAL-TOKENS-RECORDS.md), not shipped behavior. This packet makes the next implementation decisions reviewable. Existing v1 verbs and files keep their meanings. Different harnesses may produce this format with their own extraction tools.

## Encoding and identities

Use UTF-8 JSONL for observation shards and JSON for manifests. Reject duplicate keys, invalid Unicode, unknown schema versions and fields outside the schema's allowlist; report the field name and location without echoing its value. Never serialize an arbitrary source object.

An object envelope is exactly `{"id":"sha256:<64 lowercase hex>","body":{...}}`. Its ID is SHA-256 of the body's [RFC8785 canonical JSON](https://www.rfc-editor.org/rfc/rfc8785), with no trailing newline. The envelope ID is excluded from its own digest. Canonicalization makes key order and whitespace irrelevant; it supplies neither authenticity nor permission. Each body's schema string separates object types and versions.

Usage values are JSON **strings**, preserving exact numeric lexemes without a float64 round trip. Each value has a declared `number_kind` (`integer` or `decimal`) and `unit`. Integer counters accept only `0` or `[1-9][0-9]*`, use exact arithmetic, and reject negatives. A decimal source value retains its original valid JSON-number lexeme; normalization needs a mapping that defines its arithmetic and unit. No guessed conversion of cost ticks. Presence is `present`, `absent` or `unavailable`; the latter two require `value:null` and a bounded reason code. Raw present zero survives even when measurement semantics are unknown. No raw JSON numbers occur in these bodies.

Native identifiers remain strings, not numbers coerced through floating point. Portable friend/bench labels use `[a-z0-9][a-z0-9-]{0,31}`. Model and repository labels are data strings, never paths to open. Local paths appear only in the caller's source manifest and local receipt index, never in a shared body. Storage uses only validated friend/bench labels, validated dates, reserved null/unallocated names and checked content digests, as defined below; none is an arbitrary path.

## Observation body

The required keys are:

| Key | Exact shape / decision |
|---|---|
| `schema` | `nova.tokens.observation/2` |
| `source` | `{kind, producer_version, namespace, session_id, event_key}`; producer_version may be null. Namespace is a mapping-defined stable source domain, independent of collector, path, friend and bench. `event_key` is a nonempty array of native identity strings; `session_id` is provenance, not implicitly part of the spend key. |
| `kind` | `request`, `turn`, `snapshot`, or `aggregate`; never infer additivity from the name alone. |
| `revision` | `{native:null-or-string, supersedes:[observation IDs], basis}`. Sorted unique predecessors; basis is `source_order`, `operator_correction`, or `none`. A native value is ordered only as the mapping specifies. |
| `time` | `{occurred_at:null-or-offset-timestamp, start:null-or-offset-timestamp, end:null-or-offset-timestamp, basis}`. Basis is `response_observation`, `turn_completion`, `measured_interval`, `source_aggregate`, or `unknown`. Preserve source offset/precision; reporting converts known instants to UTC. |
| `origin` | `{friend:null-or-label, bench:null-or-label, basis, binding_id:null-or-string}`. Basis is `source`, `owner_binding`, or `unknown`; the collector host never supplies execution origin implicitly. |
| `model` | `{id:null-or-string, basis}`; basis is `provider_reported`, `harness_reported`, `requested`, `mixed`, or `unknown`. Reports retain and group by basis alongside ID. |
| `repository` | `{id:null-or-string, basis, policy_id:null-or-string, touched:[labels]}`; basis is `source_binding`, `explicit_policy`, or `unattributed`. Sorted unique touched labels do not allocate tokens. |
| `raw_usage` | Map from mapping-allowlisted source field names to `{presence, value, number_kind, unit, reason:null-or-code}`. Every supported field has an entry, including absent fields. |
| `model_usage` | Array of `{model_id, raw_usage}` retaining source-provided model detail; sorted by model_id, duplicates refused. Empty means no split supplied, not no usage. Aggregate and model detail are never both counted. |
| `mapping_id` | Content ID of the extraction/mapping manifest that interpreted this source shape. Later normalization can select another explicitly compatible mapping without editing this observation. |
| `receipt` | Mapping-allowlisted native locator fields only, such as turn_id or idx. No prompt, arbitrary string extension, private filename or source blob. |

The spend key is the canonical tuple `[source.namespace, source.event_key]`. A globally unique native response/message ID uses `[native_id]`; a session-scoped turn counter uses `[original_session_id, turn_number_as_string]`. The mapping proves which scope applies. A fork may carry an earlier response in a different containing session, so the containing session cannot create fresh spend. Ambiguous positional identities remain explicitly unsupported/conflicting. Collector runs and changed metadata can create different observations of one spend key, never extra spend. Mapping changes do not change that key unless an explicit migration relates the identities. A same-key conflict with no evidenced revision order is excluded from spend totals and reported. Operator corrections preserve predecessors and require an explicit correction input; collection does not invent them. A correction must resolve every competing tip. Forks, cycles and missing predecessors remain conflicts.

For model reporting, requested and harness-reported IDs are useful evidence and remain visible. They are not relabelled provider-confirmed. Requiring an independent server receipt before displaying any model would discard available information. Mixed aggregates keep their source model detail; unsupported model splits remain unallocated.

## Mapping and coverage manifests

A mapping body has `schema:"nova.tokens.mapping/2"`, `name`, `revision`, `source_shapes`, `field_rules`, `identity_rule`, `revision_rule`, `time_rule`, `model_rule`, `overlap_rule`, `fixture_digests`, and `implementation_id`. All nested shapes are fixed by the supported adapter; arbitrary executable expressions or plugin loading from a ledger are forbidden. The manifest names reviewed built-in behavior and evidence. An unknown mapping is preserved but cannot normalize spend. Approvals are provenance, not commands embedded in a manifest.

`field_rules` must state `zero_semantics` for each numeric field: `measured`, `default_may_mask_absence`, or `unknown`. Raw wire/key presence stays `present` even when the producer synthesized 0. For a present zero under either latter rule, normalized measurement is unknown and subset completeness is false; a positive value is normalized only when its other counter semantics are supported. This uses the existing mapping object rather than changing source presence into a claim about measurement. Missing/unavailable raw fields are never normalized to measured 0.

A coverage body has `schema:"nova.tokens.coverage/2"`, `scope_id`, `source_ids`, `interval`, `status`, `reasons`, `collected_at`, `collector_build`, `collector_friend`, `collection_bench`, `mapping_ids`, `shards`, `predecessors`, and `counts`. IDs and predecessors are sorted unique. Interval is explicit start/end instants, with an exclusive end. Status is `complete_within_scope`, `partial` or `unavailable`. Counts use decimal strings. Scope IDs resolve to metadata-only declared scopes, not private paths; original source location stays local. `collection_bench` is deliberately distinct from observation origin.

Each shard reference includes its byte SHA-256, record count and sorted observation-ID inventory or an inventory digest with its referenced file. Missing/corrupt references invalidate coverage. A later coverage contribution preserves its predecessors and prior observations; it does not make an unavailable source disappear from a requested report. Unknown fields or unsupported rows contribute named coverage gaps. A live source is partial unless the adapter can prove completeness at a coherent cutoff.

## Batch and ledger layout

A batch directory contains `batch.json` (exactly one coverage envelope, whose ID is the contribution ID), the new observation shards it references at their final relative paths, and any new mapping envelopes it references. It contains no other files, symlinks or executable content. References may resolve to byte-identical objects already in the explicitly named local ledger; the batch need not duplicate those. Core combines the adapters' coverage fragments into one contribution for the declared collection scope, retaining every source and gap; it does not choose the most complete-looking fragment.

Shard placement is `records/<friend>/<bench>/<day>/<shard-sha256-hex>.jsonl`. Each shard has exactly one origin partition. Null friend or bench uses reserved `_`; a valid point timestamp with its stated day basis supplies the UTC day; interval-only or unknown allocation uses `unallocated`. Never put a multi-day interval under its starting day and imply day allocation. Batches may reference several partitions. A changed origin is a new observation/correction, never a file move.

Mappings live at `mappings/<mapping-sha256-hex>.json`. Coverage is published at `coverage/<collector-friend>/<collection-bench>/<UTC-collection-day>/<coverage-sha256-hex>.json`, with the original inspected interval retained in its body; the directory date is collection provenance, not the spend day. Unknown collector labels use `_`. Content digests exclude filenames. Coverage references shard digests, so no shard filename depends on the coverage hash that references it. This closes a possible circular hash dependency. The ledger README must be reconciled to these exact paths before the first publication.

Publisher validation is structural and referential: known envelope schemas, canonical hashes, allowed paths/fields, exact shard inventories and count/digest checks. It does not need to normalize a newly retained mapping to preserve it. A view requires an installed reviewed implementation for its selected mapping; unknown mapping code is never loaded or executed from the ledger.

## Commands and publication boundary

Keep v1 untouched by adding one explicit namespace:

```text
nova-tokens records collect --sources <local-manifest> --ledger <dir> --out <new-batch-dir> --from <UTC-instant> --until <UTC-instant>
nova-tokens records check --batch <dir> --ledger <dir>
nova-tokens records view --ledger <dir> --selection <manifest> --group-by day,friend,bench,repo,model --format json
nova-tokens records publish --batch <dir> --ledger <git-checkout> --remote <name> --branch <name>
```

`collect` reads only explicit authorized source paths, reads the local ledger to avoid emitting already retained observations, and writes only the new named batch directory and an explicitly configured local receipt index. It has no network, Git or bus action. New observations are placed in immutable JSONL shards capped at 4 MiB, sorted by ID; oversize individual records fail with a named coverage gap. Existing ledger objects are referenced, not recopied on every daily run. A repeated identical collection emits no duplicate observations. Changed coverage cutoff is useful evidence, not new spend.

`check` and `view` write nothing; stdout is their result. A selection manifest pins coverage IDs, compatible mapping IDs, date basis and repository policy. Unknown values, conflicts, unavailable scopes and interval-only allocation appear beside known totals. Exit 0 means valid and complete for that explicit selection, exit 1 means a usable partial result with named gaps/conflicts, exit 2 means the invocation/schema cannot be evaluated. None may present partial spend as a complete scalar. Empty selections are refused. Mechanical reporting requires no model turn.

`publish` alone performs Git/network writes. It validates the batch, uses an isolated temporary index and an atomic commit containing only its named immutable files, and updates the chosen remote branch without force. At most 3 race retries within 60s; preserve both writers. Dirty unrelated work and the caller's index remain unchanged. After an ambiguous push response, fetch/query the exact contribution ID before retrying; do not generate a fresh contribution. No delete/reset/clean, remote configuration change or credential acquisition. Failure leaves the batch available for explicit retry. This publisher targets the agreed private ledger; it neither generates nor publishes an OSS subset.

The flags above are the required contract, not permission to scan an unspecified home, install a schedule or run another friend's collector. Scheduling and notification remain each friend's chosen shell/harness integration. A wrapper can send one actionable failure through the existing bus; unchanged success requires no model wake.

## Tests that decide this packet

Before implementation is called adopted, demonstrate exact digest fixtures with reordered keys, non-ASCII labels, invalid Unicode and duplicate keys; integer values above 2^53 surviving unchanged; missing versus present-zero fields; two equal-valued distinct events; copied sources and an origin correction counting once; a forked or resumed session replaying an earlier response ID counting once, with a session-scoped turn counter keyed by the original session ID rather than the containing session; mapping revisions and conflicts; two report groupings from one retained input set; defaulted-zero and mixed-model cases; refusal of a mapping missing `zero_semantics` for any numeric field; coverage gaps and midnight/month intervals; unchanged collection emitting no duplicate shard; and two Git writers plus a lost acknowledgment preserving both contributions, the caller's index and unrelated dirty files. Prompt/private-path sentinel values in unsupported source fields must never appear in shared files or diagnostics.

Open for the group's implementation read: this concrete encoding and command boundary. The agreed need for retained detail, voluntary diverse integrations, execution bench and private publication is not being reopened. No adapter should wait for proof of every other harness's counters: retain supported raw evidence, normalize only supported semantics, and report the remainder explicitly.
