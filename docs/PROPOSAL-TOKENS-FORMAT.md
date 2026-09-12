# Retained token records: format and command decision packet

Proposed companion to [the record contract](PROPOSAL-TOKENS-RECORDS.md), not shipped behavior. This packet makes the next implementation decisions reviewable. Existing v1 verbs and files keep their meanings. Different harnesses may produce this format with their own extraction tools.

## Encoding and identities

Use UTF-8 JSONL for observation shards and JSON for manifests. Reject duplicate keys, invalid Unicode, unknown schema versions and fields outside the schema's allowlist; report the field name and location without echoing its value. Never serialize an arbitrary source object.

An object envelope is exactly `{"id":"sha256:<64 lowercase hex>","body":{...}}`. Its ID is SHA-256 of the body's [RFC8785 canonical JSON](https://www.rfc-editor.org/rfc/rfc8785), with no trailing newline. The envelope ID is excluded from its own digest. Canonicalization makes key order and whitespace irrelevant; it supplies neither authenticity nor permission. Each body's schema string separates object types and versions.

Usage values are JSON **strings**, preserving exact numeric lexemes without a float64 round trip. Each value has a declared `number_kind` (`integer` or `decimal`) and `unit`. Integer counters accept only `0` or `[1-9][0-9]*`, use exact arithmetic, and reject negatives. A decimal source value retains its original valid JSON-number lexeme; normalization needs a mapping that defines its arithmetic and unit. No guessed conversion of cost ticks. Presence is `present`, `absent` or `unavailable`; the latter two require `value:null` and a bounded reason code. Raw present zero survives even when measurement semantics are unknown. No raw JSON numbers occur in these bodies.

Native identifiers remain strings, not numbers coerced through floating point. Portable friend/bench labels use `[a-z0-9][a-z0-9-]{0,31}`. Model and repository labels are data strings, never paths to open. Local paths appear only in the caller's source manifest and local receipt index, never in a shared body. Storage paths are derived from checked digest identifiers, not arbitrary labels.

## Observation body

The required keys are:

| Key | Exact shape / decision |
|---|---|
| `schema` | `nova.tokens.observation/2` |
| `source` | `{kind, producer_version, namespace, session_id, event_key}`; producer_version may be null. Namespace is a mapping-defined stable source domain, independent of collector, path, friend and bench. |
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

The spend key is the canonical tuple `[source.namespace, source.session_id, source.event_key]`. Collector runs and changed metadata can create different observations of one spend key, never extra spend. Mapping changes do not change that key unless an explicit migration relates the identities. A same-key conflict with no evidenced revision order is excluded from spend totals and reported. Operator corrections preserve predecessors and require an explicit correction input; collection does not invent them. A correction must resolve every competing tip. Forks, cycles and missing predecessors remain conflicts.

For model reporting, requested and harness-reported IDs are useful evidence and remain visible. They are not relabelled provider-confirmed. Requiring an independent server receipt before displaying any model would discard available information. Mixed aggregates keep their source model detail; unsupported model splits remain unallocated.

## Mapping and coverage manifests

A mapping body has `schema:"nova.tokens.mapping/2"`, `name`, `revision`, `source_shapes`, `field_rules`, `identity_rule`, `revision_rule`, `time_rule`, `model_rule`, `overlap_rule`, `fixture_digests`, and `implementation_id`. All nested shapes are fixed by the supported adapter; arbitrary executable expressions or plugin loading from a ledger are forbidden. The manifest names reviewed built-in behavior and evidence. An unknown mapping is preserved but cannot normalize spend. Approvals are provenance, not commands embedded in a manifest.

A coverage body has `schema:"nova.tokens.coverage/2"`, `scope_id`, `source_ids`, `interval`, `status`, `reasons`, `collected_at`, `collector_build`, `collection_bench`, `mapping_ids`, `shards`, `predecessors`, and `counts`. IDs and predecessors are sorted unique. Interval is explicit start/end instants, with an exclusive end. Status is `complete_within_scope`, `partial` or `unavailable`. Counts use decimal strings. Scope IDs resolve to metadata-only declared scopes, not private paths; original source location stays local. `collection_bench` is deliberately distinct from observation origin.

Each shard reference includes its byte SHA-256, record count and sorted observation-ID inventory or an inventory digest with its referenced file. Missing/corrupt references invalidate coverage. A later coverage contribution preserves its predecessors and prior observations; it does not make an unavailable source disappear from a requested report. Unknown fields or unsupported rows contribute named coverage gaps. A live source is partial unless the adapter can prove completeness at a coherent cutoff.

## Commands and publication boundary

Keep v1 untouched by adding one explicit namespace:

```text
nova-tokens records collect --sources <local-manifest> --ledger <dir> --out <new-batch-dir> --from <UTC-instant> --until <UTC-instant>
nova-tokens records check --batch <dir> --ledger <dir>
nova-tokens records view --ledger <dir> --selection <manifest> --group-by day,friend,bench,repo,model --format json
nova-tokens records publish --batch <dir> --ledger <git-checkout> --remote <name> --branch <name>
```

`collect` reads only explicit authorized source paths, reads the local ledger to avoid emitting already retained observations, and writes only the new named batch directory and an explicitly configured local receipt index. It has no network, Git or bus action. New observations are placed in immutable JSONL shards capped at4MiB, sorted by ID; oversize individual records fail with a named coverage gap. Existing ledger objects are referenced, not recopied on every daily run. A repeated identical collection emits no duplicate observations. Changed coverage cutoff is useful evidence, not new spend.

`check` and `view` write nothing; stdout is their result. A selection manifest pins coverage IDs, compatible mapping IDs, date basis and repository policy. Unknown values, conflicts, unavailable scopes and interval-only allocation appear beside known totals. Exit0 means valid and complete for that explicit selection, exit1 means a usable partial result with named gaps/conflicts, exit2 means the invocation/schema cannot be evaluated. None may present partial spend as a complete scalar. Empty selections are refused. Mechanical reporting requires no model turn.

`publish` alone performs Git/network writes. It validates the batch, uses an isolated temporary index and an atomic commit containing only its named immutable files, and updates the chosen remote branch without force. At most3 race retries within60s; preserve both writers. Dirty unrelated work and the caller's index remain unchanged. After an ambiguous push response, fetch/query the exact contribution ID before retrying; do not generate a fresh contribution. No delete/reset/clean, remote configuration change or credential acquisition. Failure leaves the batch available for explicit retry. This publisher targets the agreed private ledger; it neither generates nor publishes an OSS subset.

The flags above are the required contract, not permission to scan an unspecified home, install a schedule or run another friend's collector. Scheduling and notification remain each friend's chosen shell/harness integration. A wrapper can send one actionable failure through the existing bus; unchanged success requires no model wake.

## Tests that decide this packet

Before implementation is called adopted, demonstrate exact digest fixtures with reordered keys, non-ASCII labels, invalid Unicode and duplicate keys; integer values above2^53 surviving unchanged; missing versus present-zero fields; two equal-valued distinct events; copied sources and an origin correction counting once; mapping revisions and conflicts; two report groupings from one retained input set; defaulted-zero and mixed-model cases; coverage gaps and midnight/month intervals; unchanged collection emitting no duplicate shard; and two Git writers plus a lost acknowledgment preserving both contributions, the caller's index and unrelated dirty files. Prompt/private-path sentinel values in unsupported source fields must never appear in shared files or diagnostics.

Open for the group's implementation read: this concrete encoding and command boundary. The agreed need for retained detail, voluntary diverse integrations, execution bench and private publication is not being reopened. No adapter should wait for proof of every other harness's counters: retain supported raw evidence, normalize only supported semantics, and report the remainder explicitly.
