# A frozen native model for each job — proposed contract

Status: discussion draft for [per-job profiles](../SPEC-SWARM-PROFILES.md)
and the [native OpenCode adapter](SWARM-OPENCODE-NATIVE.md). This supplies a
proposed data shape and encoding witnesses, not a live route table, an approved
adapter, or friend consensus. Existing profile readers and earlier parent
fixtures do not acquire these fields implicitly.

Each job may select a different supported model, SDK and endpoint. We preserve
that choice instead of forcing a swarm into one homogeneous model configuration.
The coordinator and launcher must recover the same effective inputs from the
protected record without rereading a mutable model catalog.

## Why three route strings are insufficient

Pinned OpenCode v1.18.29, commit
`16747470f976aca3d362ad730bcd3fe82ecc2c9a`, selects
[API ID, SDK and URL per model](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/provider/provider.ts#L1489).
It also merges capabilities, options, headers, limits and variants.
[SDK construction](https://github.com/anomalyco/opencode/blob/16747470f976aca3d362ad730bcd3fe82ecc2c9a/packages/opencode/src/provider/provider.ts#L1730)
applies provider options and URL overrides. A provider name or the permitted
tool list does not identify all those inputs.

`worker.execution` remains the single adapter/artifact/PATH owner. This
amendment adds model data, not another executable selector or callback.

## Catalog selection and the protected projection

For the proposed `opencode-native/1` adapter, `route.native` is required and
has exactly `models`, a nonempty array. Each member has exactly `requested`
(string) and `resolved` (the object below). The requested strings equal
`allowed_models` in the same order, one entry each; duplicate, missing, extra,
or reordered entries refuse. Each route must be admitted by the exact pinned
adapter's reviewed route table. A synthetic example is not such a table.

The existing `route.provider` remains the requested provider identity. Each
model's `resolved.provider` is its effective native provider ID; the reviewed
route table must admit that mapping. An explicit `route.endpoint` must equal
every admitted member's final endpoint. It does not silently overwrite one.
Omit that common override when models have distinct endpoints. Each member
then carries its own explicit endpoint, obtained at admission from the pinned
route table, never a launch-time provider default.

The selected `resolved` object has exactly:

| Member | Value |
| --- | --- |
| `provider` | Nonempty native provider ID, with no `/` |
| `model` | Nonempty native model key; `/` may occur inside it |
| `endpoint` | Final literal API endpoint admitted by the route table |
| `native` | The closed object below |

`requested.provider/model` keep the caller/default selection. Copy the selected
`resolved` object into the protected attempt exactly. Do not keep another SDK,
API ID or endpoint under `worker`, or recalculate it after admission.

`native` has exactly `schema`, `api_id`, `sdk`, `model_url`, and `runtime`:

- `schema` is `nova.swarm.native-model/1`.
- `api_id` is the nonempty, case-sensitive effective API model ID. Native
  tool selection uses this value; a display name is not a replacement.
- `sdk` names the exact SDK entry admitted by the pinned adapter. A package
  name alone does not prove its dependency bytes; artifact/compatibility checks
  must bind those separately. File URLs and runtime package installation are
  not implied permissions.
- `model_url` retains the explicit model URL before a provider override;
  `resolved.endpoint` is the final URL. Their distinct roles are checked by
  the route table. Neither contains environment-substitution syntax.
- `runtime` is the complete supported effective model projection below.

URLs are nonempty absolute HTTP(S) URLs with a host, without userinfo or
fragments. Actual schemes, hosts, ports, paths and queries require route-table
approval. A fixture's `.invalid` host authorizes no network operation.

## Runtime fields

Every listed field is required; unknown and duplicate members refuse at every
depth. Strings are valid Unicode. No implicit catalog/default lookup is allowed.

| Member | Exact shape and meaning |
| --- | --- |
| `name`, `family`, `release_date` | Strings retained explicitly, including empty strings; no assumption that every consumer treats them as presentation only |
| `status` | `alpha`, `beta`, `deprecated`, or `active`; admission policy decides which are supported |
| `capabilities` | Exactly boolean `temperature`, `reasoning`, `attachment`, `toolcall`; `input`, `output`; and `interleaved` |
| `capabilities.input`, `capabilities.output` | Each has exactly boolean `text`, `audio`, `image`, `video`, `pdf` |
| `capabilities.interleaved` | Boolean `false` or `true`, or an object with exactly nonempty string `field`; the selected SDK schema must admit the field name |
| `limits` | Exactly `context`, `input`, `output`; context/output are positive canonical decimal integer strings at most `9007199254740991`; input is the same or `null` for explicitly absent |
| `headers` | Array of exactly `{name,value}` string pairs, distinct names under ASCII case folding and sorted by raw UTF-8 name bytes |
| `sdk_options` | Typed object node below, containing final non-secret SDK factory options |
| `model_options` | Typed object node below, containing final non-secret model-call options, with all admitted variant/default merges already applied |
| `variant` | `null` for explicitly no selected variant, or its nonempty admitted name; provenance for the final options, not an instruction to merge another variant at launch |

Booleans remain JSON booleans: the existing number-free encoder supports them.
Do not convert them to strings or collapse `interleaved=true` into `false`.
The integer limit restriction is this adapter's explicit constraint, not a
claim that OpenCode's finite-number schema itself requires integers.

Header names use HTTP token syntax; values contain neither controls nor CR/LF.
The pinned SDK schema enumerates permitted non-secret headers. Authentication,
cookies and API-key headers are not alternative credential channels. There is
no general secret detector: supported input fields simply provide no place for
a credential value. The separately bound secret-variable name remains its owner.

`prompt.tools` still lists permitted registry tools. It is distinct from model
capabilities: requesting tools when `toolcall=false` refuses. The pinned adapter
must admit every requested tool for the selected model, then enforce the
separate registry/permission mapping. Neither field grants OS access.

## Typed SDK options without raw numbers in the snapshot

Use these closed recursive nodes for SDK-specific JSON. Each row lists all
allowed members; a value of the wrong type refuses.

| `kind` | Other members | Native JSON value |
| --- | --- | --- |
| `null` | None | `null` |
| `bool` | `value`: boolean | That boolean |
| `string` | `value`: string | That string |
| `number` | `decimal`: string | That exact decimal token, unquoted |
| `array` | `items`: node array | Array in retained order |
| `object` | `members`: array of exactly `{name,value}`; string name and node value | Object with distinct names, ordered by UTF-16 code units |

Decimal grammar is `-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?`. Negative zero is
forbidden. Thus `1`, `0.5`, and `-0.25` are numbers, while `1.0`, `01`, `1e3`,
`NaN` and `-0` refuse; use `1000` for the corresponding integer. The selected
runtime must reject overflow and nonzero underflow instead of silently turning
them into infinity or zero. The exact retained token, not a float formatting
round trip, is emitted into generated JSON. SDK-specific ranges and precision
requirements remain part of its closed option schema.

The proposed adapter bounds a selected canonical `resolved` object to 256 KiB,
each option tree to 4096 nodes and 32 node levels (root is level one), and each
decimal token to 4096 UTF-8 bytes. Count before allocation beyond a bound and
refuse the entire selection; no truncation. An object member name is unique
exactly, and must already be UTF-16 ordered. This intentionally differs from
header-array ordering. No sorting, default filling or duplicate-key repair
occurs in a reader.

This wire grammar is necessary but **not sufficient** to admit options.
For every SDK and adapter revision the route table names a closed semantic
schema enumerating every permitted key, nested type, range and non-secret
literal role. Unknown schema/key, unsupported nested value, or callback refuses;
even an empty object is not an exemption from that schema check. No generic
`Record<string, Any>` or arbitrary JSON passthrough is an admitted schema.

Endpoint overrides (`baseURL`), credential values (`apiKey` and equivalent),
custom fetch functions and dynamic package selectors are forbidden in options:
they already have separate owners or are unsupported. Config substitutions
such as `{env:`, `{file:` and `${` must not survive in generated configuration.
The adapter injects the final endpoint and the separately gated credential
binding through their named owners; it may not recover either from this tree.

Non-default variants and heterogeneous SDKs are supported by this representation.
The adapter must materialize their final effective options at admission and
reproduce them without a second merge. If it cannot, that route is still
unimplemented; do not relabel default-only or empty-options operation as the
completed feature.

## Hashes and generated configuration

Canonicalize the expanded `resolved` with the existing `internal/records`
encoder. Its objects, strings, booleans and nulls need no new number serializer.
`hashes.config` includes this entire object in the execution-binding preimage
`{requested,resolved,worker,credentials,env_var,limits,prompt,execution}`.
The catalog hash includes every admitted model entry, not only the default.
Neither digest contains itself or a credential value.

Option lowering uses the canonical encoder's string escaping and UTF-16 object
ordering, preserving arrays and emitting only validated decimal tokens for
number nodes. This is a deterministic config encoding, not a claim to implement
RFC 8785 numeric normalization. For example a typed number `0.5` lowers to
`0.5`, while a typed string `"0.5"` lowers to `"0.5"`; their hashes differ.

The exact final `HARNESS.json` grammar remains a separate native adapter gate.
Its generator must take the frozen projection, and `generated_config` hashes
its exact output bytes outside the input preimage, avoiding a cycle. Do not
mistake an option-tree encoding witness for a native launch/config witness.
Earlier body/catalog/launch fixtures preserve their earlier draft contract;
the new companion vectors do not silently rewrite those readers. Incorporating
the execution/model amendments requires updating all dependent parent fixtures
and strict readers together before enabling profiled native admission.

## Evidence and remaining acceptance

[Synthetic model vectors](fixtures/swarm-native-model-vectors.json) exercise
different SDKs, API IDs, endpoints, limits, capabilities and number-bearing
options. Run `go run ./docs/drafts/fixtures/native-model-check` from the repo.
The checker uses the real records encoder for projection identity and checks
option lowering; it does not certify any actual Go/Zen model, SDK semantic
schema, artifact, credentials, generated native config or launch behavior.

Production acceptance additionally requires:

1. Closed SDK option schemas and reviewed Go/Zen route rows, with accurate
   per-model data and all effective defaults captured.
2. Negative production fixtures for wrong/unknown fields, every bound, bad URL,
   option ownership collision, secret header, unsupported variant or tool,
   malformed decimal and selected-model mismatch. Fail before provider calls.
3. Full catalog, attempt, config and launch fixture migration, including one
   changed non-default model and one changed numeric option changing the proper
   digests; secret values never enter these preimages.
4. Exact generated native configuration, dependency binding, registry filtering
   and [issue 296](https://github.com/mas-bandwidth/nova-tools/issues/296)
   isolation. Observe actual config/default/catalog reads and any auxiliary model
   calls; a hash alone cannot enforce frozen execution.
5. Independent friend dispositions on the incorporated revision and equivalent
   real-work usage measurements after adoption. Encoding success is neither.

Catalog/harness cost remains separately attributed accounting evidence. Native
cost defaults can be zero without a provider billing observation; retain the
parent spec's unknown-cost and raw-evidence rules.
