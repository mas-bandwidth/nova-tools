# Antigravity retained-token mapping — proposed source contract

Based on unmetered empirical inspection of native local storage; Emma has confirmed the on-disk storage layout on macOS ARM64 (`studio-arm64`). Real numeric telemetry and session history remain in private coordination records; the public test fixtures in `testdata/tokens/antigravity/` are synthetic and privacy-preserving. The recorded producer is **Antigravity 2.12.2** (running under Google Antigravity). This is a source-shape proposal and review packet, not a deployed adapter. Sealed manifest ID: `sha256:173b9ff62dcda4fdd2187298d1fdbc66d1b01fe14bd9b7899bf9fc11444386b1`.

## 1. Source Structure & Grain

Antigravity stores step-level generator telemetry across two distinct persistent stores in the user home directory (`~/.gemini/antigravity/`):

1. **SQLite Database** (`~/.gemini/antigravity/conversations/<session-id>.db`):
   - Table: `gen_metadata`
   - Schema: `CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));`
   - Column `idx` is the step index (0-based or 1-based integer).
   - Column `data` is a binary Proto3 payload of type `cortex_go_proto.CortexStepGeneratorMetadata`.
2. **Transcript JSONL** (`~/.gemini/antigravity/brain/<session-id>/.system_generated/logs/transcript.jsonl`):
   - Line format: JSON object with fields `step_index` (integer), `created_at` (ISO 8601 UTC microsecond string), `type`, and `status`.

**Available Grain:** Each row in `gen_metadata` represents one completed generator step or model turn. Preserve each turn at that native grain (`kind: "turn"`). Do not synthesize sub-step call breakdowns or fabricate start/end intervals.

## 2. Identity & Join Contract

- **Spend Key:** Canonical tuple `[source.namespace, source.event_key]`, where `source.namespace` is `"antigravity"` and `event_key` is the two-element array `[session_id, idx_as_string]`. The event key is an array, never concatenated text.
- **1:1 Join Rule:** SQLite `gen_metadata.idx` joins 1:1 with transcript `step_index`.
- **Strict Duplicate Rejection:** Duplicate `idx` rows in SQLite or duplicate `step_index` entries in `transcript.jsonl` are fatal mapping violations; 1:1 join requires unique keys.
- **Unpaired Rows (Disjoint Records):** If a SQLite `gen_metadata` row exists without a matching transcript line, the observation envelope is preserved with its spend key, but `time.occurred_at` is set to `null` and `time.basis` is set to `"unknown"`. The adapter must not invent fine-grained timestamps from file creation or export dates.
- **Session ID:** The native containing session ID is retained in `source.session_id` as provenance only; copying the database or transcript to another bench or path does not change the native session ID.

## 3. Proto3 Wire Encoding & Presence Semantics

Inside `CortexStepGeneratorMetadata` (Field 1: `ChatModelMetadata`, Field 1.4: `ModelUsageStats`):
- **Tag 1**: `input_tokens` (uint64 varint, prompt and input instructions; spend_role: `base_counter`)
- **Tag 2**: `cache_read_tokens` (uint64 varint, context prefix cache read; spend_role: `subset_detail`)
- **Tag 3**: `output_tokens` (uint64 varint, generated completion tokens; spend_role: `base_counter`)
- **Tag 5**: `total_tokens` (uint64 varint, uninterpreted producer total; spend_role: `non_spend`)
- **Tag 6**: `thinking_output_tokens` (uint64 varint, reasoning/thinking tokens; spend_role: `subset_detail`)
- **Field 1.19**: `response_model` (string length-delimited, model identifier)

### Presence Rules (Preserving Absence vs Present-Zero)

1. **Omitted on Wire:**
   - In Proto3 syntax, an omitted field tag establishes wire absence only; it does not establish whether the provider measured zero or whether measurement was unavailable. Normalized measurement semantics must remain unknown unless explicit source evidence resolves them.
   - In `nova.tokens.observation/2`, omitted wire tags MUST be mapped faithfully to `{presence: "absent", value: null, reason: "omitted_from_wire"}` without assuming 0.
2. **Explicit Wire Zero:**
   - When a field is explicitly present on the wire with varint 0 (e.g. `thinking_output_tokens = 0`), it MUST be preserved as `{presence: "present", value: "0", reason: null}`.
3. **Total Field Outside Spend (`non_spend`):**
   - Wire tag 5 (`total_tokens`) is an uninterpreted raw producer total. It is assigned `spend_role: "non_spend"` and retained as `{field_name: "total_tokens", ...}` in `raw_usage` for auditability and verification, but MUST NOT be added to spend.

## 4. Counting, Arithmetic & Retained Spend

- **Upstream Producer Counting Semantics:** The Proto3 wire schema establishes field tags and presence, but does NOT provide authoritative evidence of upstream producer arithmetic (e.g. whether `input_tokens` is net-new or inclusive of `cache_read_tokens`, and whether `output_tokens` includes `thinking_output_tokens`). In synthetic fixture data, `cache_read_tokens` is 45,000 while `input_tokens` is 1,200; asserting an inclusive subset constraint (`cache_read_tokens <= input_tokens`) would produce an arithmetic conflict without producer justification.
- **Normalized Spend Marked Unsupported:** Until verified producer documentation or telemetry evidence establishes the exact counting relationship, normalized spend is marked **unsupported**:
  - `identity_rule.normalized_spend_supported: false`
  - `identity_rule.unsupported_reason: "cache_and_thinking_inclusion_semantics_unverified"`
- **Provisional Spend Roles & Coverage Obligation:** Current `base_counter` and `subset_detail` labels assigned in §7 are provisional descriptors of wire structure and cannot establish arithmetic counting relationships or subset deductions while `normalized_spend_supported: false`. The unknown-semantics coverage obligation remains an explicit standing requirement after this proposal lands; unsupported normalization must not become a claim that the accounting endpoint is complete.
- **Classification of Fixtures:** Observations retain all five raw counters faithfully as measured on the wire. Under normalized spend views, turns are classified as unsupported spend rather than rejected as arithmetic conflicts or artificially coerced. Observations are never edited to force assumed arithmetic to pass.
- **Revision & Finality:** Identical repeated entries for the same spend key deduplicate. Changed numeric content for the same spend key requires explicit supersession; otherwise it is retained as a conflict and excluded from spend.

## 5. Model, Origin & Repository Attribution

- **Model Identifier:** `response_model` (Field 1.19) is preserved verbatim as `model.id` with `model.basis: "harness_reported"`.
- **Model Usage:** Antigravity reports a single model per generation turn; `model_usage` is emitted as `[]` (empty array, signifying no secondary per-model split).
- **Configurable Origin:** Origin friend and bench are supplied via execution-origin bindings (e.g. CLI flags or operator configuration) with `basis: "owner_binding"`. When no binding establishes historical origin, origin is retained as unknown: `{friend: null, bench: null, basis: "unknown", binding_id: null}` (using the canonical `originBases` allowlist `{source, owner_binding, unknown}`). The fixture values (`friend: "emma"`, `bench: "studio"`) are team test examples, not normative literals.
- **Configurable Repository:** Repository identity is supplied via active workspace binding with `basis: "source_binding"` (e.g. `{id: "mas-bandwidth/emma", basis: "source_binding", policy_id: null, touched: []}`). When unbound or ambiguous, repository is retained as unattributed: `{id: null, basis: "unattributed", policy_id: null, touched: []}`.

## 6. Wire Literals Under Proposed Contract

| Wire key | Literal / Rule |
|---|---|
| `schema` | `nova.tokens.observation/2` |
| `source.kind` | `antigravity` |
| `source.namespace` | `antigravity` |
| `source.producer_version` | `2.12.2` when verified from harness metadata; otherwise `null` |
| `source.session_id` | Native containing session UUID string |
| `source.event_key` | `[session_id, idx_as_string]` |
| `kind` | `turn` |
| `revision` | `{native: null, supersedes: [], basis: "source_order"}` |
| `time.occurred_at` | Microsecond ISO 8601 UTC timestamp from `transcript.jsonl`; `null` when unpaired |
| `time.basis` | `response_observation` when joined with transcript; `unknown` when unpaired |
| `time.start`, `time.end` | `null` (turn observation carries no interval) |
| `origin` | Parameterized binding `{friend, bench, basis: "owner_binding", binding_id: null}`, or unknown `{friend: null, bench: null, basis: "unknown", binding_id: null}` |
| `model.id` | Field 1.19 `response_model` string; `null` if omitted |
| `model.basis` | `harness_reported`; `unknown` if `model.id` is absent |
| `repository` | Parameterized workspace binding `{id, basis: "source_binding", policy_id: null, touched: []}`, or unattributed `{id: null, basis: "unattributed", policy_id: null, touched: []}` |
| `raw_usage` | Closed five-field allowlist below |
| `model_usage` | `[]` |
| `mapping_id` | Content-addressed SHA-256 digest of the sealed mapping manifest (`sha256:173b9ff62dcda4fdd2187298d1fdbc66d1b01fe14bd9b7899bf9fc11444386b1`) |
| `receipt` | `{"idx": "<idx_as_string>"}` |

## 7. `raw_usage`: Closed Per-Mapping Allowlist

| Source Field | `number_kind` | `unit` | `zero_semantics` | `spend_role` | Missing Key | Invalid Value |
|---|---|---|---|---|---|---|
| `input_tokens` | `integer` | `tokens` | `measured` | `base_counter` | `absent` / `null` / `omitted_from_wire` | `unavailable` / `null` / `parse_failed` |
| `output_tokens` | `integer` | `tokens` | `measured` | `base_counter` | same | same |
| `cache_read_tokens` | `integer` | `tokens` | `measured` | `subset_detail` | same | same |
| `thinking_output_tokens` | `integer` | `tokens` | `measured` | `subset_detail` | same | same |
| `total_tokens` | `integer` | `tokens` | `measured` | `non_spend` | same | same |

*Note: `base_counter` and `subset_detail` roles above are provisional descriptors of wire structure and do not establish counting relationships while `normalized_spend_supported: false`.*

## 8. Privacy & Synthetic Test Fixtures

All fixtures in `testdata/tokens/antigravity/` are entirely synthetic and privacy-preserving:
- `sqlite_rows.json` (`sha256:fb29d98f5da6371b68dac8555b0f2e241c117469a62919691da873035ea91375`): Hex-encoded synthetic Protobuf blobs of `CortexStepGeneratorMetadata` covering normal turns, omitted wire fields, explicit wire zeros, and unpaired steps.
- `transcript.jsonl` (`sha256:61208538dca8fced6e81887e995a3f1c8e33bf41ae986353b1dfa00661e0500e`): Synthetic transcript lines containing dummy `step_index` and timestamps.
- `expected_records.jsonl` (`sha256:c3d379e8742ad68f2fc071668b87164047f2df4a37e240b412b1627abe67d33f`): Synthetic observation fixtures with placeholder mapping ID (`sha256:1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b`). These fixtures establish decode and join structure independently of the mapping manifest, avoiding circular hash dependencies between source fixture digests and generated retained-output receipts.
- `malformed_blobs.json` (`sha256:e09f3af7e9fa5fe608181ff09d28f56d27b8f6c4cd0bd2ef9b8fbf296b5cad3e`): Negative test cases asserting truncation, varint overflow, and unsupported wire type rejections.

No private prompts, file contents, secrets, or actual model responses are included in any fixture.
