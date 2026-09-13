# Antigravity retained-token mapping — proposed source contract

Based on unmetered empirical inspection of native local storage; Emma has confirmed the on-disk storage layout on macOS ARM64 (`studio-arm64`). Real numeric telemetry and session history remain in private coordination records; the public test fixtures in `testdata/tokens/antigravity/` are synthetic and privacy-preserving. The recorded producer is **Antigravity 2.12.2** (running under Google Antigravity). This is a source-shape proposal and review packet, not a deployed adapter.

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
- **Tag 1**: `input_tokens` (uint64 varint, prompt and input instructions)
- **Tag 2**: `cache_read_tokens` (uint64 varint, context prefix cache read)
- **Tag 3**: `output_tokens` (uint64 varint, generated completion tokens)
- **Tag 5**: `total_tokens` (uint64 varint, uninterpreted producer total; preserved provisionally outside spend)
- **Tag 6**: `thinking_output_tokens` (uint64 varint, reasoning/thinking tokens)
- **Field 1.19**: `response_model` (string length-delimited, model identifier)

### Presence Rules (Preserving Absence vs Present-Zero)

1. **Omitted on Wire:**
   - In Proto3 syntax, an omitted field tag establishes wire absence only; it does not establish whether the provider measured zero or whether measurement was unavailable. Normalized measurement semantics must remain unknown unless explicit source evidence resolves them.
   - In `nova.tokens.observation/2`, omitted wire tags MUST be mapped faithfully to `{presence: "absent", value: null, reason: "omitted_from_wire"}` without assuming 0.
2. **Explicit Wire Zero:**
   - When a field is explicitly present on the wire with varint 0 (e.g. `thinking_output_tokens = 0`), it MUST be preserved as `{presence: "present", value: "0", reason: null}`.
3. **Provisional Total Field Outside Spend:**
   - Wire tag 5 (`total_tokens`) is an uninterpreted raw producer total. It is retained as `{field_name: "total_tokens", ...}` in `raw_usage` for provisional evidence and auditability, but MUST NOT be added to spend.

## 4. Counting, Arithmetic & Conflict Handling

- **Spend Components:** Spend is the sum of base counters: `input_tokens + output_tokens`.
- **Inclusive Input:** `cache_read_tokens` is an inclusive subset of `input_tokens` (`cache_read_tokens <= input_tokens`).
- **Inclusive Output:** `thinking_output_tokens` is an inclusive subset of `output_tokens` (`thinking_output_tokens <= output_tokens`).
- **No Double Counting:** Neither `cache_read_tokens` nor `thinking_output_tokens` may be added on top of base counters.
- **Arithmetic Invariant Check:** Any record where `cache_read_tokens > input_tokens` or `thinking_output_tokens > output_tokens` produces an arithmetic mapping conflict and is excluded from spend, rather than silently adjusted.
- **Revision & Finality:** Identical repeated entries for the same spend key deduplicate. Changed numeric content for the same spend key requires explicit supersession; otherwise it is retained as a conflict and excluded from spend.

## 5. Model, Origin & Repository Attribution

- **Model Identifier:** `response_model` (Field 1.19) is preserved verbatim as `model.id` with `model.basis: "harness_reported"`.
- **Model Usage:** Antigravity reports a single model per generation turn; `model_usage` is emitted as `[]` (empty array, signifying no secondary per-model split).
- **Origin:** Origin friend and bench are supplied via explicit owner binding (`origin.basis: "owner_binding"`, e.g. `{friend: "emma", bench: "studio"}`). The collection host does not establish historical execution origin.
- **Repository:** Retained as `{id: "mas-bandwidth/emma", basis: "source_binding", policy_id: null, touched: []}` when bound to an active workspace; otherwise `{id: null, basis: "unattributed", policy_id: null, touched: []}`.

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
| `origin` | `{friend: "emma", bench: "studio", basis: "owner_binding", binding_id: null}` |
| `model.id` | Field 1.19 `response_model` string; `null` if omitted |
| `model.basis` | `harness_reported`; `unknown` if `model.id` is absent |
| `repository` | `{id: "mas-bandwidth/emma", basis: "source_binding", policy_id: null, touched: []}` |
| `raw_usage` | Closed five-field allowlist below |
| `model_usage` | `[]` |
| `mapping_id` | Content-addressed SHA-256 digest of the sealed mapping manifest |
| `receipt` | `{"idx": "<idx_as_string>"}` |

## 7. `raw_usage`: Closed Per-Mapping Allowlist

| Source Field | `number_kind` | `unit` | `zero_semantics` | Role | Missing Key | Invalid Value |
|---|---|---|---|---|---|---|
| `input_tokens` | `integer` | `tokens` | `measured` | base counter | `absent` / `null` / `omitted_from_wire` | `unavailable` / `null` / `parse_failed` |
| `output_tokens` | `integer` | `tokens` | `measured` | base counter | same | same |
| `cache_read_tokens` | `integer` | `tokens` | `measured` | subset of input | same | same |
| `thinking_output_tokens` | `integer` | `tokens` | `measured` | subset of output | same | same |
| `total_tokens` | `integer` | `tokens` | `measured` | provisional evidence (outside spend) | same | same |

## 8. Privacy & Synthetic Test Fixtures

All fixtures in `testdata/tokens/antigravity/` are entirely synthetic:
- `sqlite_rows.json`: Contains hex-encoded synthetic Protobuf blobs of `CortexStepGeneratorMetadata` covering normal turns, omitted wire fields, explicit wire zeros, and unpaired steps.
- `transcript.jsonl`: Synthetic transcript lines containing dummy `step_index` and timestamps.
- `expected_records.jsonl`: Golden sealed `nova.tokens.observation/2` records matching the exact Proto3 decoding and 1:1 join rules.
- `malformed_blobs.json`: Negative test cases asserting truncation, varint overflow, and unsupported wire type rejections.

No private prompts, file contents, secrets, or actual model responses are included in any fixture.
