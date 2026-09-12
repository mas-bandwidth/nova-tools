# Antigravity Synthetic Source Fixtures & Identity Join Contract

## Scope & Purpose
These test fixtures provide synthetic, un-metered empirical data representing the Antigravity 2.12.2 token telemetry source. They establish the ground-truth encoding, Proto3 field presence semantics, and 1:1 identity join rules required for `nova-tokens records` collection under `docs/PROPOSAL-TOKENS-FORMAT.md`.

## Source Structure
Antigravity stores step-level generator telemetry in two distinct locations:
1. **SQLite Database** (`~/.gemini/antigravity/conversations/<session-id>.db`):
   - Table: `gen_metadata`
   - Schema: `CREATE TABLE gen_metadata (idx integer, data blob, size integer NOT NULL DEFAULT 0, PRIMARY KEY (idx));`
   - Column `idx` is the step index (integer).
   - Column `data` is a binary Proto3 payload of type `cortex_go_proto.CortexStepGeneratorMetadata`.
2. **Transcript JSONL** (`~/.gemini/antigravity/brain/<session-id>/.system_generated/logs/transcript.jsonl`):
   - Line format: JSON object with field `step_index` (integer), `created_at` (ISO 8601 UTC string), `type`, and `status`.

## Proto3 Wire Encoding (`ModelUsageStats`)
Inside `CortexStepGeneratorMetadata` (Field 1: `ChatModelMetadata`, Field 1.4: `ModelUsageStats`):
- **Tag 1**: `input_tokens` (uint64 varint, prompt/instructions)
- **Tag 2**: `cache_read_tokens` (uint64 varint, context prefix cache)
- **Tag 3**: `output_tokens` (uint64 varint, total generated completion)
- **Tag 5**: `total_tokens` (uint64 varint, uninterpreted producer total; preserved provisionally outside spend without asserting context capacity semantics)
- **Tag 6**: `thinking_output_tokens` (uint64 varint, raw thinking tokens)
- **Field 1.19**: `response_model` (string length-delimited)

## Presence Rules (Preserving Absence vs Present-Zero)
1. **Omitted on Wire**:
   - In Proto3, an omitted tag establishes wire absence only; it does not establish whether the original measurement was unavailable or zero. Normalized measurement semantics must remain unknown unless explicit source evidence resolves them.
   - In `nova.tokens.observation/2`, omitted wire tags MUST be mapped faithfully to `{presence: "absent", reason: "omitted_from_wire"}` without asserting measurement semantics or defaulting to 0.
2. **Explicit Wire Zero**:
   - When a field is explicitly present with varint 0 (e.g. `thinking_output_tokens = 0` in Turn 3), it MUST be preserved as `{presence: "present", value: "0"}`.
3. **Provisional Total Field Outside Spend**:
   - Wire tag 5 (`total_tokens`) is an uninterpreted raw producer total. It is retained as `{field_name: "total_tokens", ...}` in `raw_usage` for provisional evidence, but MUST NOT be added to spend.

## 1:1 Identity Join Invariants
- **Spend Key**: Canonical tuple `[source.namespace, source.event_key]`, where `event_key` is the array `[session_id, idx_str]` under PR #124 (`71ea08b`).
- **Join Rule**: SQLite `gen_metadata.idx` joins 1:1 with transcript `step_index`.
- **Timestamp**: Microsecond-precision ISO 8601 UTC timestamp `created_at` from `transcript.jsonl` provides the canonical event instant (`time.occurred_at`), labeled with `time.basis: "response_observation"`.
- **Disjoint / Unpaired Rows**: If a SQLite row lacks a matching transcript line (Turn 4), the spend key is preserved, but timestamp basis is marked `unknown` without guessing fake instants.
