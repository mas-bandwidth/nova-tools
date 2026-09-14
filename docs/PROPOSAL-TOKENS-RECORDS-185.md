# Finite Records Contract Decision Packet: Nova Tools Issue #185

**Status**: PROPOSED CONTRACT SPECIFICATION (For Records-Owner & Group Review)
**Date**: 2026-09-14
**Author**: Emma Antigravity (gemini-2.5-pro)
**Context**: mas-bandwidth/nova-tools#185 (Issue comment 5669226348) & Stella notes `stella-4c87c906465e`, `stella-44ee260d1b5b`
**Affects**: `internal/records`, `internal/tokens`, `cmd/nova-tokens`, `docs/SPEC-TOKENS.md` (Rule 29), `docs/PROPOSAL-TOKENS-VALIDATORS.md`
**Dependencies**: `nova.tokens.observation/2`, `nova.tokens.mapping/2`, `nova.tokens.coverage/2`

---

## Executive Summary

This decision packet settles the blocking contract gates for native token records collection and whole-package validation identified in Issue #185 and Stella's review on PR #323:

1. **Multi-Binding Source/Result Attribution & Conflict Cardinality**: Supports multiple source streams with explicit ordinals/IDs; introduces `ObservationProvenance` / `SourceRef` (retaining `SourceOrdinal`, `SourceID`) pairing each observation occurrence with its originating source stream without mutating the sealed wire type `CodexObservation`; assigns conflict reasons to all participating sources (same-binding, cross-source, and unknown-source); strictly rejects partial origin bindings; pins multi-source decoder preconditions (source/reader length equality, ordinal agreement, source-ID uniqueness); defines conflict cardinality as the count of *distinct conflicting identity keys*, keeping single-record decoder outcomes separate.
2. **Package Allowance & Strict 3-Way Mapping Closure**: Amends Rule 29 to explicitly admit optional `inventories/<digest>.json`; removes input `coverage/` replica allowance (input package contains strictly `batch.json`, referenced shards, all referenced mappings, and optional inventories; no `coverage/` replica in input packages); enforces strict tripartite closure: $\text{Packaged Mappings} \equiv \text{Coverage } \texttt{mapping\_ids} \equiv \text{Observation } \texttt{mapping\_id}\text{s}$ (no orphans, no missing files, no unused mappings; an existing ledger copy does not exempt the package; writing an already-retained identical mapping into the ledger is a verified no-op).
3. **Coverage Reason Mapping & Separate Retained Observations**: Maps unknown origin/model and ambiguous default-zero counters into the closed reason set `{source_unavailable, unsupported_rows, unknown_fields, partial_interval, conflict}`; clearly distinguishes 0 emitted observations (wire refusals and truncated lines, tracked in refusal and truncated-line counters) from 1 retained observation (unavailable counters, arithmetic mismatches, impossible subsets, which retain sealed observations and are unspendable without incrementing wire-shape/refusal counters); keeps distinct-key conflict cardinality separate (0 conflicts for single-record inconsistencies); pins additive reason composition (conflicts, unsupported rows, and unknown fields compose additively); enforces the structural invariant $\texttt{gaps} = \operatorname{len}(\texttt{reasons})$ over deduplicated $(code, source)$ entries.
4. **Two-Phase Directory Validation, Bottom-Up Durability Trace, and Readiness**: Establishes `ValidateCandidateDirectory` (pre-marker, checking directory contents excluding only the explicitly owned marker temp, verifying its file type/mode/non-symlink boundary; forbidding pre-existing marker temp before staging) and `ValidateInstalledDirectory` (post-marker, strictly forbidding `batch.json.tmp`, any `*.tmp` files, and any unexpected extra files); strictly validates `nova.tokens.observation/2` envelopes and verifies record origin/day matches shard path (`records/<friend>/<bench>/<day>/...` with `_` and `unallocated`); preserves sorted-ID-set inventory comparison; pins explicit bottom-up fsync trace (`mkdir(root) -> fsync(parent-dir)`, intermediate directories created without premature sync, write & sync files, bottom-up sync `day -> bench -> friend -> records (and mappings/, inventories/) -> root` before marker staging, write & sync `batch.json.tmp`, atomic no-replace rename, and `fsync(root)` after marker installation); specifies explicit fail-stop on any sync failure; verifies link/unlink crash witness (marker + temp present) is unpublishable under `ValidateInstalledDirectory`; clarifies that envelope validators for `observation/2`, `mapping/2`, and `coverage/2` exist in `internal/records`, with whole-package composition in `internal/tokens` (or `internal/pkgvalid`) as the remaining dependency.

---

## 1. Multi-Binding Source/Result Attribution & Conflict Cardinality

### 1.1 Multi-Binding Collection Model

A native collection invocation processes an ordered sequence of declared source streams, each associated with its own declared origin provenance binding. Independent per-binding decoding cannot be concatenated naively: deduplication, conflict detection, and spendability must be evaluated globally across all sealed observations in a single reconciliation phase.

To retain exact source stream membership for every observation occurrence after global reconciliation without altering the sealed `CodexObservation` wire type (`internal/tokens/codex.go:152-186`), decoded observations are wrapped in `ObservationProvenance` (retaining `SourceOrdinal` and `SourceID`).

```go
// SourceBinding binds an input stream to its origin provenance.
type SourceBinding struct {
    Ordinal   int     // 0-indexed position in the declared input stream list
    SourceID  string  // Source-scope binding identifier in ns grammar (e.g. "nova.codex.desktop.session")
    Friend    *string // Friend label, or nil if explicitly unknown
    Bench     *string // Bench label, or nil if explicitly unknown
    BindingID *string // Stable binding content ID / ns, or nil if explicitly unknown
    Basis     string  // "owner_binding" | "unknown"
}

// SourceRef pairs a source stream ordinal with its identifier.
type SourceRef struct {
    SourceOrdinal int    `json:"source_ordinal"`
    SourceID      string `json:"source_id"`
}

// ObservationProvenance pairs a decoded CodexObservation with its originating source stream.
// The sealed CodexObservation wire type (internal/tokens/codex.go:152-186) remains unmodified.
type ObservationProvenance struct {
    Observation   CodexObservation `json:"observation"`
    SourceOrdinal int              `json:"source_ordinal"`
    SourceID      string           `json:"source_id"`
}

// SourceRefusal records an observation rejected at the records boundary, retaining source provenance.
// Emits 0 observations (count = 0); no observation envelope or spend key is created.
type SourceRefusal struct {
    SourceOrdinal int    `json:"source_ordinal"`
    SourceID      string `json:"source_id"`
    ResponseID    string `json:"response_id"`
    Rule          string `json:"rule"`
    Field         string `json:"field"`
}

// SourceUnsupported records counts of unsupported wire shapes attributed per source.
// Emits 0 observations (count = 0); no observation envelope or spend key is created.
type SourceUnsupported struct {
    SourceOrdinal int            `json:"source_ordinal"`
    SourceID      string         `json:"source_id"`
    Shapes        map[string]int `json:"shapes"` // Fixed shape vocabulary -> count
}

// MultiSourceDecoding represents the reconciled output of multi-binding decoding.
type MultiSourceDecoding struct {
    Observations   []ObservationProvenance `json:"observations"`   // Sealed observations (retained records, count=1 per record; includes unspendable)
    Refusals       []SourceRefusal         `json:"refusals"`       // Boundary refusals (0 observations emitted)
    Unsupported    []SourceUnsupported     `json:"unsupported"`    // Per-source counts of unsupported wire shapes (0 observations emitted)
    TruncatedLines int                     `json:"truncated_lines"` // Truncated final JSONL lines (0 observations emitted)
    OwedTasks      []string                `json:"owed_tasks"`     // Distinct owed coverage tasks from mappings
}

// DecodeCodexMultiSource decodes multiple source streams under their declared bindings.
func DecodeCodexMultiSource(m *CodexMapping, sources []SourceBinding, readers []io.Reader) (*MultiSourceDecoding, error)
```

**Preconditions on MultiSourceDecoding**:
1. **Source / Reader Length Equality**: `len(sources) == len(readers)`. The number of declared source bindings must strictly equal the number of reader streams. Any mismatch fails immediately upfront with an error (`RuleDecoderLengthMismatch`).
2. **Ordinal Agreement**: For every index $i \in [0, \text{len}(sources))$, `sources[i].Ordinal == i`. Each binding's declared ordinal must match its position in the slice. Out-of-order, skipped, or misaligned ordinals fail upfront (`RuleDecoderOrdinalMismatch`).
3. **Source-ID Uniqueness**: All `sources[i].SourceID` must be non-empty and pairwise distinct across the slice. Duplicate source identifiers fail upfront (`RuleDuplicateSourceID`).
4. **Binding Completeness**: Every source binding must satisfy the origin completeness rule (§1.2: fully specified or explicitly unknown). Partial bindings fail upfront (`RuleBindingPartial`).

### 1.2 Origin Binding Completeness Rule

An origin binding MUST be either **fully specified** or **explicitly unknown**. Partial bindings are strictly rejected upfront before stream processing.

* **Complete Origin Binding**:
  - `Friend` is a valid label (`[a-z0-9][a-z0-9-]{0,31}`), non-nil, non-empty, and $\neq \texttt{"\_"}$.
  - `Bench` is a valid label (`[a-z0-9][a-z0-9-]{0,31}`), non-nil, non-empty, and $\neq \texttt{"\_"}$.
  - `BindingID` is a non-empty `cid` or `ns`.
  - `Basis` is `"owner_binding"`.
* **Explicit Unknown Binding**:
  - `Friend` is `nil`.
  - `Bench` is `nil`.
  - `BindingID` is `nil`.
  - `Basis` is `"unknown"`.
* **Refusal (`RuleBindingPartial`)**:
  Any binding where some but not all of `(Friend, Bench, BindingID)` are provided, or where empty strings or `"\_"` are supplied, is refused immediately with `RuleBindingPartial`. A collector NEVER guesses or defaults an absent friend or bench from the host environment.

### 1.3 Conflict Cardinality Definition & Source Attribution

Let $K$ be the set of unique event/spend keys (`SpendKey` = `[namespace, event_key...]`) across all decoded observations. For each key $k \in K$, let $V(k)$ be the set of distinct observation envelope content IDs:

$$V(k) = \{ \operatorname{id}(obs) \mid obs \in \text{Observations} \land obs.\text{SpendKey} = k \}$$

* **Duplicates**: If $|V(k)| = 1$ and the key appears $m > 1$ times, all occurrences after the first are marked `Duplicate = true`. No conflict exists.
* **Conflicts**: If $|V(k)| > 1$, every observation for that key is marked `Conflict = true`, `Spendable = false`, and excluded from normalized spend.
* **Participating Source Attribution**:
  For every key $k$ where $|V(k)| > 1$, conflict reasons `{code: "conflict", source: <source_id>}` are assigned to **all participating sources** that emitted an observation for key $k$:
  - **Same-Binding Conflicts**: If two or more conflicting observations for key $k$ originate from the same source stream $S$, source $S$ receives `{code: "conflict", source: S}`.
  - **Cross-Source Conflicts**: If Source $S_1$ and Source $S_2$ emit conflicting observations for key $k$, **both** sources receive conflict reasons: `{code: "conflict", source: S_1}` and `{code: "conflict", source: S_2}`.
  - **Unknown-Source Conflicts**: If one of the participating sources has an explicit unknown origin binding (e.g. $S_{\text{unk}}$), that source also receives its corresponding `{code: "conflict", source: S_unk}` reason entry.
* **Cardinality Invariant**:
  $$\texttt{counts.conflicts} = \big| \{ k \in K \mid |V(k)| > 1 \} \big|$$

The `conflicts` count in `coverage/2` is the count of **distinct conflicting identity keys**, NOT the total number of variant records or observations participating in conflicts.

*Example*:
- Key `A` appears 4 times with 3 distinct observation hashes $\implies 1$ conflict key.
- Key `B` appears 2 times with 2 distinct observation hashes $\implies 1$ conflict key.
- Key `C` appears 3 times with identical observation hashes $\implies 0$ conflict keys (2 duplicates).
- Total `counts.conflicts` = $1 + 1 + 0 = 2$.

* **Separation of Retained Observations from Refusals / Truncation**:
  - **Count 1 vs Count 0**:
    * *0 Observations Emitted (Count = 0)*: Wire refusals (envelope schema / field validation failures) and truncated final JSONL lines fail validation before creating an observation envelope. They emit 0 observations (`count = 0`), create no observation envelope ID, and produce no spend key. They are tracked strictly in wire-level `Refusals`, `Unsupported`, or `TruncatedLines` counters.
    * *1 Retained Observation Emitted (Count = 1)*: Records with unavailable supported counters (`Presence == "unavailable"`, completeness gap), arithmetic mismatches (`input + output != total`), or impossible subsets (`cached_input > input`, `reasoning > output`) parse successfully into sealed `nova.tokens.observation/2` envelopes. They emit 1 observation (`count = 1`), retain their original sealed content IDs and mapping diagnoses, and are retained in shard records. They are marked `Spendable = false`.
  - **Strict Counter Separation**:
    Do NOT increment wire-shape (`Unsupported`) or boundary refusal (`Refusals`) counters solely because a coverage reason uses `unsupported_rows`. Those counters strictly track unmapped source shapes and syntax refusals where 0 observations are emitted. Retained observations (count = 1) do not increment wire-shape or refusal counters.
  - **Distinct-Key Conflict Cardinality**:
    When an anomalous record has no competing distinct-content records for its spend key, $|V(k)| = 1$ and it contributes 0 distinct-key conflicts (`counts.conflicts = 0`). A single record's internal invalidity is never counted as a conflict.
  - **Additive Reason Composition**:
    Coverage reason codes are not mutually exclusive; they compose additively across orthogonal anomaly axes:
    * *Arithmetic mismatch + spend-key collision*: Suppose observation $A$ (source $S_1$) has an arithmetic mismatch, and distinct observation $B$ (source $S_2$) shares the same spend key ($|V(k)| > 1$):
      - Spend-key collision yields 1 distinct conflicting key (`counts.conflicts` increments by 1). Both $A$ and $B$ are marked `Conflict = true`, `Spendable = false`.
      - Both participating sources receive conflict reasons: `{code: "conflict", source: S_1}` and `{code: "conflict", source: S_2}`.
      - Source $S_1$ additively receives `{code: "unsupported_rows", source: S_1}` because observation $A$ has an arithmetic mismatch.
      - If either $A$ or $B$ originates from an unattributed origin binding (`Friend == nil`), `{code: "unknown_fields", source: ...}` is additively emitted for that source.
      - Total `reasons` carries all three distinct `(code, source)` tuples; `counts.gaps` is the deduplicated count ($\operatorname{len}(\texttt{reasons}) = 3$).
      - The original sealed observation content IDs and mapping diagnoses remain intact.

---

## 2. Package `inventory_file` Allowance & Strict 3-Way Mapping Closure

### 2.1 Rule 29 Package Allowance Amendment

`SPEC-TOKENS.md` Rule 29 is amended to explicitly admit optional shard inventory files:

A `--batch <dir>` publication package consists strictly of:
1. Exactly one `batch.json` at root (the `nova.tokens.coverage/2` envelope).
2. Referenced observation shards at `records/<friend>/<bench>/<day>/<shard-sha256-hex>.jsonl`.
3. All referenced mapping manifests at `mappings/<mapping-sha256-hex>.json`.
4. **Optional Inventory Files**: Shards using the `inventory_file` tagged branch reside at `inventories/<inventory-sha256-hex>.json`.

**Full Self-Contained Mapping Closure**:
The batch package is strictly self-contained: every mapping envelope referenced in `coverage.mapping_ids` must be packaged in `mappings/`. An existing copy of an identical mapping in the destination ledger repository does NOT exempt the input package from packaging it. When the batch is published into the destination ledger, writing an already-retained identical content-addressed mapping is a verified no-op.

**Strict Exclusion of Input Coverage Replica**:
An input package contains strictly `batch.json` and referenced shard, mapping, and inventory files. There is **no `coverage/` replica** in an input package. The path `coverage/<collector-friend>/<collection-bench>/<UTC-collection-day>/<coverage-sha256-hex>.json` is strictly the destination written into the ledger repository on publication. Any `coverage/` directory or file present in an input batch package is an unexpected extra directory and rejected with `RulePackageStrayFile` (exit 2).

**Inventory File Invariants**:
- File mode must be `100644`.
- Content must be the RFC 8785 canonical JSON array of `cid` strings followed by exactly one `\n`.
- Digest in filename `<inventory-sha256-hex>` must equal the byte SHA-256 of the file (including trailing `\n`).
- Every inventory file in `inventories/` must be referenced by at least one shard in `coverage.shards`. Stray or unreferenced inventory files are rejected (`RulePackageStrayFile`).

### 2.2 Strict 3-Way Mapping Closure

To guarantee that a batch is self-contained and free of unevidenced or orphan artifacts, the validator enforces strict tripartite set equality across mappings:

Let:
- $M_{\text{pkg}} = \{ \operatorname{cid}(\text{file}) \mid \text{file} \in \texttt{mappings/*.json} \}$
- $M_{\text{cov}} = \{ m \mid m \in \texttt{coverage.mapping\_ids} \}$
- $M_{\text{obs}} = \{ obs.\texttt{mapping\_id} \mid obs \in \bigcup \text{shards} \}$

**The Invariant**:
$$M_{\text{pkg}} \equiv M_{\text{cov}} \equiv M_{\text{obs}}$$

Every mapping declared in `coverage.mapping_ids` must exist in `mappings/` within the batch ($M_{\text{cov}} \subseteq M_{\text{pkg}}$). An existing copy of a mapping in the destination ledger repository does not exempt the input package from packaging all referenced mappings.

| Condition | Violation Code | Consequence |
|---|---|---|
| $M_{\text{pkg}} \setminus M_{\text{cov}} \neq \emptyset$ | `RulePackageOrphanMapping` | Refusal: file exists in `mappings/` but is not declared in coverage. |
| $M_{\text{cov}} \setminus M_{\text{pkg}} \neq \emptyset$ | `RulePackageMissingMapping` | Refusal: mapping declared in coverage is missing from `mappings/` (existing ledger copy does not exempt). |
| $M_{\text{obs}} \setminus M_{\text{cov}} \neq \emptyset$ | `RuleObservationUndeclaredMapping` | Refusal: observation record references mapping omitted from coverage. |
| $M_{\text{cov}} \setminus M_{\text{obs}} \neq \emptyset$ | `RuleCoverageUnusedMapping` | Refusal: coverage declares mapping unused by any observation in the batch. |

---

## 3. Unknown Origin/Model/Default-Zero Coverage Reasons

### 3.1 Closed Reason Vocabulary

`nova.tokens.coverage/2` admits ONLY the following closed set of 5 reason codes in `reasons`:
```
source_unavailable | unsupported_rows | unknown_fields | partial_interval | conflict
```

### 3.2 Reason Mapping Decisions

When native collection encounters incomplete or ambiguous metadata or decoder anomalies, it maps them into the closed vocabulary as follows:

| Condition | Emitted Obs Count | Mapped Reason Code | Source Scope (`reasons.source`) | Notes |
|---|---|---|---|---|
| Entire source stream missing, unreadable, or 0 valid bytes | 0 | `source_unavailable` | Specific `source_id` | Stream could not be read or opened. |
| Stream unparseable or malformed container header | 0 | `source_unavailable` | Specific `source_id` | Fatal container-level read failure. |
| Unknown origin (`friend`/`bench`/`basis="unknown"`) | 1 (retained) | `unknown_fields` | Specific `source_id` | Records retained with unattributed origin; spendable if counters valid. |
| Unknown / unconfigured model ID on valid response | 1 (retained) | `unknown_fields` | Specific `source_id` | Model is absent/ambiguous; record retained; spendable if counters valid. |
| Ambiguous zero detail counters (`default_may_mask_absence`) | 1 (retained) | `unknown_fields` | Specific `source_id` | Detail counter emitted as 0 may mask unmeasured absence; record retained. |
| Unmapped source shapes or unsupported wire types | 0 | `unsupported_rows` | Specific `source_id` | Counted in `Unsupported`, excluded from records. 0 observations emitted. |
| Truncated final JSONL line in source stream | 0 | `unsupported_rows` | Specific `source_id` | Counted in `TruncatedLines`, excluded from records. 0 observations emitted (no envelope ID, no spend key). |
| Boundary refusals (envelope schema / field validation failure) | 0 | `unsupported_rows` | Specific `source_id` | Counted in `Refusals`, excluded from records. 0 observations emitted. |
| Supported counter unavailable (`f.Presence == "unavailable"`, completeness gap) | 1 (retained) | `unsupported_rows` | Specific `source_id` | Retained observation (count=1, unspendable). Shard record emitted with sealed ID intact. Does NOT increment wire `Unsupported`/`Refusals`. 0 conflicts (unless colliding). |
| Arithmetic mismatch (`input + output != total` with all present) | 1 (retained) | `unsupported_rows` | Specific `source_id` | Retained observation (count=1, unspendable). Shard record emitted with sealed ID intact. Does NOT increment wire `Unsupported`/`Refusals`. 0 conflicts (unless colliding). |
| Impossible subset (`cached_input > input` or `reasoning > output`) | 1 (retained) | `unsupported_rows` | Specific `source_id` | Retained observation (count=1, unspendable). Shard record emitted with sealed ID intact. Does NOT increment wire `Unsupported`/`Refusals`. 0 conflicts (unless colliding). |
| Collection interval boundary truncated | N/A | `partial_interval` | `source_id` or `null` | Timestamps cut across active window. |
| Conflicting spend keys detected across distinct observation content IDs | 1 per variant (retained) | `conflict` | Participating `source_id`s | Multiple conflicting observation variants for the same spend key ($|V(k)| > 1$). Assigned to all participating sources. Counted in `counts.conflicts`. |

**Emitted Observations vs. Wire Refusals / Truncation and Additive Composition**:
1. **Zero Observations Emitted (Count = 0)**:
   - Wire refusals (envelope schema violations, type mismatches, integer/decimal lexeme errors) and unmapped wire shapes fail validation before creating an observation envelope. They emit 0 observations (`count = 0`), create no observation envelope ID, and produce no spend key. They are tracked strictly in wire-level `Refusals` or unmapped shape `Unsupported` counters.
   - Truncated final JSONL lines at stream EOF cannot form a valid JSON object. They emit 0 observations (`count = 0`, no envelope ID, no spend key). They are tracked in `TruncatedLines` (or source shape `truncated_final_line`), and map to coverage reason `{code: "unsupported_rows", source: S}`.
2. **One Retained Observation Emitted (Count = 1)**:
   - Records with unavailable supported counters, arithmetic mismatches, or impossible subsets parse validly into sealed `nova.tokens.observation/2` envelopes. They are retained in the shard records (emitting 1 observation, contributing to `record_count`), preserving their original sealed envelope IDs and mapping diagnoses.
   - They are marked `Spendable = false` due to incompleteness or internal inconsistency.
   - **Counter Separation**: Do NOT increment wire-shape (`Unsupported`) or boundary refusal (`Refusals`) counters solely because a coverage reason uses `unsupported_rows`. Those counters strictly measure wire-level parser rejections where 0 observations are emitted.
3. **Additive Reason Composition**:
   - Coverage reasons do not map exclusively; they compose additively across independent anomaly dimensions:
     - An observation with an arithmetic mismatch ($A$, source $S_1$) that also shares a spend key with a distinct observation ($B$, source $S_2$):
       * Contributes 1 distinct-key conflict to `counts.conflicts` ($|V(k)| > 1$). Both $A$ and $B$ have `Conflict = true`, `Spendable = false`.
       * Emits conflict reasons for both sources: `{code: "conflict", source: S_1}` and `{code: "conflict", source: S_2}`.
       * Emits an unsupported-rows reason for $A$'s arithmetic failure: `{code: "unsupported_rows", source: S_1}`.
       * If $A$ or $B$ also has unknown origin (`Friend == nil`), an unknown-fields reason is emitted: `{code: "unknown_fields", source: ...}`.
     - All distinct `(code, source)` tuples deduplicate and satisfy $\texttt{gaps} = \operatorname{len}(\texttt{reasons})$.
     - Sealed observation content IDs and mapping diagnoses remain intact and unmodified.

### 3.3 The Invariant: $\texttt{gaps} = \operatorname{len}(\texttt{reasons})$

The coverage envelope requires:
$$\texttt{counts.gaps} == \operatorname{len}(\texttt{reasons})$$

**Deduplication & Sorting Rules**:
1. `reasons` is a set of unique `{code, source}` pairs.
2. Multiple occurrences of the same condition within a source (e.g. 3 ambiguous zero detail counters and an unknown model in source $S$) map to `{code: "unknown_fields", source: S}` and **deduplicate to a single entry**.
3. Duplicate `{code, source}` pairs are refused (`RuleDuplicateElement`).
4. Elements are sorted lexicographically by `(code, source)` with `null` sorting before any string `source`.
5. `counts.gaps` is the integer count of these deduplicated reason tuples. It is **never** the arithmetic sum of missing fields, null counters, or affected records.

---

## 4. Candidate-Marker & Installed-Directory Validator Interfaces

### 4.1 Validator Architecture & Go API

Because `internal/records` strictly excludes path I/O, dealing purely with in-memory data structures, canonical serialization, and envelope validation, whole-package validation (`ValidateCandidateDirectory`, `ValidateInstalledDirectory`) is composed in `internal/tokens` (or `internal/pkgvalid`). The underlying envelope and schema validators (`nova.tokens.observation/2`, `nova.tokens.mapping/2`, and `nova.tokens.coverage/2`) are already implemented and tested in `internal/records`. The composing package manages directory traversal, file I/O, bottom-up fsync verification, and atomic commit semantics, delegating in-memory envelope and schema validation to `internal/records`.

Two distinct functions provide pre-commit validation and post-install validation:

```go
package tokens

// ValidateCandidateDirectory verifies a prepared batch directory BEFORE batch.json is installed.
// dir must exist and must NOT contain batch.json.
// Candidate validation excludes ONLY the explicitly owned marker temp (ownedMarkerTempName, e.g. "batch.json.tmp"),
// checking its file type and non-symlink boundary (mode 100644 regular file).
// All other files in the directory must be referenced package artifacts. Any other temporary
// file (*.tmp) or stray file causes immediate refusal (RuleTemporaryFilePresent / RulePackageStrayFile).
// The staging workflow strictly forbids any pre-existing marker temp prior to staging.
// candidateBatchBytes contains the canonical UTF-8 bytes of the candidate nova.tokens.coverage/2 envelope.
func ValidateCandidateDirectory(dir string, ownedMarkerTempName string, candidateBatchBytes []byte) error

// ValidateInstalledDirectory verifies a completed batch directory containing batch.json.
// dir must exist and MUST contain batch.json.
// It strictly forbids batch.json.tmp, any other *.tmp files, and any unexpected extra files
// (strict package boundary: only records/, mappings/, inventories/, and batch.json permitted;
// no input coverage/ replica allowed).
func ValidateInstalledDirectory(dir string) error
```

### 4.2 Two-Phase Validation Lifecycle

```
[ Collection Phase ]
  │
  ├─ 1. Exclusive Directory Creation (mkdir, O_EXCL, refuse if exists)
  │     └─ mkdir(root)
  │     └─ fsync containing parent directory (filepath.Dir(dir)) to commit root dentry
  │
  ├─ 2. Create Intermediate Directory Tree:
  │     └─ mkdir directory ancestors down to day (no premature sync before children/files):
  │           - dir/records
  │           - dir/records/<friend>
  │           - dir/records/<friend>/<bench>
  │           - dir/records/<friend>/<bench>/<day>
  │           - dir/mappings
  │           - dir/inventories (if inventory_file used)
  │
  ├─ 3. Stream & Write Shards, Mappings, Inventories:
  │     └─ write & fsync each file (file.Sync(), file.Close())
  │
  ├─ 4. Bottom-Up Directory Sync (commit file dentries & directory hierarchy before marker):
  │     └─ fsync newly created directory hierarchy bottom-up:
  │           - records/<friend>/<bench>/<day>
  │           - records/<friend>/<bench>
  │           - records/<friend>
  │           - records/
  │           - mappings/
  │           - inventories/ (if used)
  │           - package root (dir)
  │     └─ Explicit fail-stop on any sync failure
  │
  ├─ 5. Phase 1 Validation (Candidate Validation):
  │     - Verify no pre-existing marker temp or batch.json before staging
  │     - ValidateCandidateDirectory(dir, "batch.json.tmp", candidateCoverageBytes)
  │       (excludes ONLY explicitly owned marker temp, checking its type/mode/non-symlink)
  │       (strictly validates all shard lines under their mappings, origin/day vs shard path,
  │        and sorted-ID-set inventory comparison)
  │     - (Refuse if invalid; dir remains marker-free and unpublishable)
  │
  ├─ 6. Stage Marker: Write batch.json.tmp
  │     └─ fsync batch.json.tmp (file.Sync(), file.Close())
  │
  ├─ 7. Commit Marker: Atomic No-Replace Rename batch.json.tmp -> batch.json
  │     - Pinned atomic no-replace semantics:
  │         * macOS: renameatx_np(AT_FDCWD, tmp, AT_FDCWD, target, RENAME_EXCL)
  │         * Linux: renameat2(AT_FDCWD, tmp, AT_FDCWD, target, RENAME_NOREPLACE)
  │         * Portable POSIX fallback: link(tmp, target) then unlink(tmp)
  │     - If batch.json exists (e.g. concurrent race): fails with EEXIST / RuleMarkerExists
  │       without clobbering existing marker; exits 2
  │     - Crash witness for link/unlink fallback: crash between link and unlink leaves both
  │       batch.json and batch.json.tmp; directory is verified unpublishable under ValidateInstalledDirectory
  │       (RuleTemporaryFilePresent, exit 2)
  │     - fsync package root (dir) immediately after marker installation to commit marker dentry
  │     - Explicit fail-stop on any sync failure
  │
  └─ 8. Phase 2 Validation / Publication Ingestion:
        ValidateInstalledDirectory(dir)
        - batch.json MUST exist and validate
        - Strictly forbids batch.json.tmp and any *.tmp files (RuleTemporaryFilePresent)
        - Strictly forbids unexpected extra files/directories including coverage/ (RulePackageStrayFile)
```

### 4.3 Validation Checklist

Both validators execute the verification matrix against the batch:

1. **File System Invariants**:
   - Every entry under `dir` inspected with `os.Lstat`.
   - **No Symlinks**: Any symlink returns `RuleSymlinkForbidden` (exit 2).
   - **Temporary Files & Exclusion**:
     - *Candidate Validation*: Directory contents are verified excluding ONLY the explicitly owned marker temp (e.g. `batch.json.tmp`), while validating that it is a regular file (not symlink or directory) with mode `100644`. Any other `*.tmp` file is rejected as `RuleTemporaryFilePresent` (exit 2). Pre-existing marker temp before staging is strictly forbidden.
     - *Installed Validation*: Lingering `batch.json.tmp` or any `*.tmp` file is strictly forbidden and returns `RuleTemporaryFilePresent` (exit 2).
   - **Permissions**: Every file must be mode `100644`; every directory mode `0755`.
   - **Strict Package Boundary**:
     - Only `records/`, `mappings/`, `inventories/`, and (for installed directories) `batch.json` are permitted.
     - No `coverage/` directory or replica is permitted in an input package (`RulePackageStrayFile`, exit 2).
     - Any hidden file (e.g. `.DS_Store`), undeclared directory, or unexpected extra file returns `RulePackageStrayFile` (exit 2).
2. **Coverage Envelope Verification**:
   - Candidate bytes / `batch.json` decoded and verified with `records.ValidateEnvelope`.
   - Schema must be `"nova.tokens.coverage/2"`.
   - Canonical CID computed from canonical body bytes.
3. **Shard & Inventory Verification**:
   - Every shard in `coverage.shards` verified at `records/<friend>/<bench>/<day>/<shard-id-hex>.jsonl`.
   - Shard byte SHA-256 matches `shard_id` and filename.
   - Observation line count equals `record_count`.
   - **Strict Observation Validation on Every Shard Line**:
     - Every line in every shard is decoded as a `nova.tokens.observation/2` envelope and verified against its declared `mapping_id` (using the corresponding mapping manifest from `mappings/`).
     - Canonical CID computed from canonical body bytes matches observation envelope CID. Malformed JSON, schema violations, or invalid field lexemes fail with `RuleObservationMalformed` (exit 2).
     - **Record Origin and Shard Path Verification**:
       Each record's own `friend`, `bench`, and UTC `day` are verified against the shard's directory path `records/<friend>/<bench>/<day>/...`:
       - If `friend` is nil / empty, path component must be `_`.
       - If `bench` is nil / empty, path component must be `_`.
       - If `day` is nil / unallocated, path component must be `unallocated`.
       - Any mismatch between the observation's own origin/day and the shard path fails with `RuleRecordPathOriginMismatch` (exit 2).
   - **Sorted-ID-Set Inventory Comparison**:
     - For `inline_ids`: sorted unique observation envelope IDs in the shard must equal sorted `inline_ids`.
     - For `inventory_file`: `inventories/<inventory-id-hex>.json` exists, byte SHA-256 matches `inventory_file`, content is canonical JSON array of CIDs ending in `\n`. Shard observation IDs and inventory file contents are compared as **sorted ID sets** (order-independent), preserving existing sorted-ID-set inventory comparison without imposing an undocumented physical line order constraint.
4. **Mapping Closure**:
   - Every mapping file in `mappings/` validated with `records.ValidateEnvelope`.
   - Filename hex matches mapping envelope canonical CID.
   - 3-way mapping closure holds ($M_{\text{pkg}} \equiv M_{\text{cov}} \equiv M_{\text{obs}}$).
5. **Count Equations**:
   - `gaps == len(reasons)`
   - `records_emitted == sum(shards.record_count)`
   - `observations == len(unique_observation_ids)`
   - `conflicts == len(conflicting_spend_keys)` (distinct conflicting keys only; single-record decoder outcomes produce 0 conflicts).

### 4.4 Durability, Atomic No-Replace, and Crash Recovery Rules

1. **No-Clobber Fresh Destination and Atomic No-Replace Marker Protection**:
   - The destination directory must not exist prior to collection.
   - Attempting to output into an existing directory is refused (`RuleDestinationExists`, exit 2).
   - **Pinned Atomic No-Replace Semantics**:
     Atomic commit cannot rely on check-then-rename, as a concurrency race could insert `batch.json` between the check and `rename(2)`, which would overwrite and clobber the destination.
     Instead, native collection and publication rely on OS-level atomic no-replace primitives:
     - On macOS / Darwin: `renameatx_np(AT_FDCWD, tmpPath, AT_FDCWD, targetPath, RENAME_EXCL)` (flag `0x00000004` / `RENAME_EXCL`).
     - On Linux: `renameat2(AT_FDCWD, tmpPath, AT_FDCWD, targetPath, RENAME_NOREPLACE)` (flag `1`).
     - Portable POSIX fallback idiom: hard link `link(tmpPath, targetPath)` (which atomically fails with `EEXIST` if `targetPath` exists), followed by `unlink(tmpPath)`.
     - If `batch.json` exists at the destination path prior to or during the atomic no-replace commit, the operation fails with `EEXIST` / `RuleMarkerExists` (exit 2). The existing `batch.json` remains byte-identical and un-clobbered, and execution fails stop.
2. **Durability Fsync Ordering (Bottom-Up Directory Sync Trace)**:
   - Fsync trace guarantees that every file and directory entry is safely committed to persistent storage in proper bottom-up dependency order:
     1. Create fresh root directory (`mkdir(dir, 0755)`).
     2. `fsync(filepath.Dir(dir))` (persists the package root directory entry into its containing parent).
     3. Create intermediate directory hierarchy down to day without premature directory sync:
        - `dir/records`
        - `dir/records/<friend>`
        - `dir/records/<friend>/<bench>`
        - `dir/records/<friend>/<bench>/<day>`
        - `dir/mappings`
        - `dir/inventories` (if created)
     4. Write and flush each file payload: shards, mappings, and inventories (`file.Sync()`, `file.Close()`).
     5. Bottom-up directory sync: sync directory entries in reverse hierarchical order from deepest leaf up to package root before marker staging:
        - `fsync(dir + "/records/<friend>/<bench>/<day>")`
        - `fsync(dir + "/records/<friend>/<bench>")`
        - `fsync(dir + "/records/<friend>")`
        - `fsync(dir + "/records")`
        - `fsync(dir + "/mappings")`
        - `fsync(dir + "/inventories")` (if created)
        - `fsync(dir)` (package root)
     6. Stage marker temp: write and flush `batch.json.tmp` (`file.Sync()`, `file.Close()`).
     7. Commit marker: atomic no-replace rename `batch.json.tmp` $\to$ `batch.json`.
     8. Sync package root (`fsync(dir)`) immediately after marker installation to guarantee the marker dentry is committed to durable storage.
   - **Explicit Fail-Stop**: Any error encountered during `mkdir`, file write, file `fsync`, directory `fsync`, or marker rename causes an immediate fail-stop abort (exit 2). No partial or un-synced state is marked valid.
3. **Crash Recovery Semantics & Link/Unlink Fallback Crash Boundary**:
   - Presence of valid `batch.json` without any lingering `.tmp` files is the sole atomic commit point.
   - If collection is killed or crashes before marker rename:
     - `batch.json` is absent (or only `batch.json.tmp` exists).
     - Directory is considered **uncommitted and unpublishable**.
     - `publish --batch` refuses any directory missing `batch.json` or containing `.tmp` files (exit 2).
   - **Link/Unlink Fallback Crash Witness**: If the portable `link`/`unlink` fallback is used and a crash occurs between `link(batch.json.tmp, batch.json)` and `unlink(batch.json.tmp)`, both `batch.json` and `batch.json.tmp` exist on disk simultaneously. Under `ValidateInstalledDirectory`, this state is strictly unpublishable: lingering `*.tmp` files trigger `RuleTemporaryFilePresent` (exit 2).
   - **No In-Place Resumption**: Retries must target a fresh directory. The abandoned directory is left intact for inspection and never cleaned automatically by tool invocations.

---

## 5. Concrete Acceptance Test Cases

### 5.1 Multi-Binding & Conflict Cardinality Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-MB-01` | `multi_binding_two_sources_success` | Positive | Two readers, each with distinct complete `SourceBinding`. Reader 0 has 2 turns; Reader 1 has 3 turns. | Exit 0. Decodes 5 observations wrapped in `ObservationProvenance`. Refusals empty. Global reconciliation confirms 5 spendable records. |
| `TC-MB-02` | `multi_binding_refusal_retains_source` | Positive/Attribution | Reader 0 has valid turn; Reader 1 has invalid number lexeme (`007`). | Exit 0. 1 observation emitted. Refusal list carries `SourceOrdinal: 1`, `SourceID: "src-1"`, `Rule: "integer_lexeme"`, `Field: "input_tokens"`. |
| `TC-MB-03` | `multi_binding_unsupported_retains_source` | Positive/Attribution | Reader 0 has 1 response; Reader 1 has 2 `token_count` snapshot shapes. | Exit 0. `SourceUnsupported` for ordinal 1 records `{"token_count": 2}`. Ordinal 0 has 0 unsupported. |
| `TC-MB-04` | `partial_binding_friend_no_bench` | Negative Control | Source binding has `Friend: "emma"`, `Bench: nil`, `BindingID: "cid:1"`. | Refusal: `RuleBindingPartial`. Execution aborts before reading source lines. |
| `TC-MB-05` | `partial_binding_bench_no_friend` | Negative Control | Source binding has `Friend: nil`, `Bench: "studio"`, `BindingID: "cid:1"`. | Refusal: `RuleBindingPartial`. |
| `TC-MB-06` | `partial_binding_underscore_label` | Negative Control | Source binding has `Friend: "_"`, `Bench: "studio"`, `BindingID: "cid:1"`. | Refusal: `RuleLabelSyntax` / `RuleBindingPartial`. Null must be `nil`, not `"_"`. |
| `TC-MB-07` | `explicit_unknown_binding_accepted` | Positive | Source binding has `Friend: nil`, `Bench: nil`, `BindingID: nil`, `Basis: "unknown"`. | Exit 0. Records emitted with origin `{null, null, "unknown", null}`. |
| `TC-MB-08` | `conflict_cardinality_multi_variant` | Positive/Math | Key `K1` has 3 distinct variants; Key `K2` has 2 distinct variants; Key `K3` has 2 identical copies. | `counts.conflicts = "2"`. Key `K3` is marked duplicate, not conflict. All 5 records for `K1` and `K2` marked `Conflict: true`. |
| `TC-MB-09` | `conflict_same_binding` | Conflict / Attribution | Reader 0 emits 2 conflicting observation envelopes for the same response ID. | Reconciled globally: `counts.conflicts = "1"`. Both records marked `Conflict: true`. Coverage `reasons` receives `{code: "conflict", source: "src-0"}`. |
| `TC-MB-10` | `conflict_cross_source` | Conflict / Attribution | Reader 0 and Reader 1 emit conflicting observations for the same response ID. | Reconciled globally: `counts.conflicts = "1"`. Both records marked `Conflict: true`. Both participating sources assigned reasons: `{code: "conflict", source: "src-0"}` and `{code: "conflict", source: "src-1"}`. |
| `TC-MB-11` | `conflict_unknown_source` | Conflict / Attribution | Reader 0 (known origin) and Reader 1 (unknown binding) emit conflicting observations for the same response ID. | Reconciled globally: `counts.conflicts = "1"`. Both records marked `Conflict: true`. Both sources receive conflict reasons: `{code: "conflict", source: "src-0"}` and `{code: "conflict", source: "src-1"}`. |

### 5.2 Package Inventory Allowance & Mapping Closure Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-PKG-01` | `package_inventory_file_valid` | Positive | Shard uses `inventory_file: "sha256:abc..."`. File `inventories/abc...json` present, valid canonical array + LF. | `ValidateInstalledDirectory` passes (nil error). |
| `TC-PKG-02` | `package_unreferenced_inventory_file` | Negative Control | Package contains `inventories/xyz...json` not referenced by any shard in `coverage.shards`. | Refusal: `RulePackageStrayFile` naming `inventories/xyz...json`. |
| `TC-PKG-03` | `package_missing_referenced_inventory` | Negative Control | Shard references `inventory_file: "sha256:def..."` but `inventories/def...json` missing on disk. | Refusal: `RuleShardReference` / missing inventory file. |
| `TC-PKG-04` | `package_inventory_missing_trailing_lf` | Negative Control | Inventory file valid JSON array of CIDs but lacks trailing `\n`. | Refusal: byte digest mismatch / `RuleInventoryEncoding`. |
| `TC-PKG-05` | `mapping_closure_exact_3way` | Positive | Packaged `mappings/` has `M1`; `coverage.mapping_ids` has `[M1]`; all shard records have `mapping_id: M1`. | Pass: 3-way mapping closure satisfied. |
| `TC-PKG-06` | `mapping_closure_orphan_package_file` | Negative Control | `mappings/` has `M1` and `M2`. `coverage.mapping_ids` has only `[M1]`. | Refusal: `RulePackageOrphanMapping` naming `M2`. |
| `TC-PKG-07` | `mapping_closure_missing_package_file` | Negative Control | `coverage.mapping_ids` has `[M1, M2]`. File `mappings/M2.json` missing on disk. | Refusal: `RulePackageMissingMapping` naming `M2`. |
| `TC-PKG-08` | `mapping_closure_observation_undeclared` | Negative Control | Shard record has `mapping_id: M3`. `coverage.mapping_ids` has only `[M1]`. | Refusal: `RuleObservationUndeclaredMapping` naming `M3`. |
| `TC-PKG-09` | `mapping_closure_coverage_unused` | Negative Control | `coverage.mapping_ids` has `[M1, M2]`. Both files exist. Shard records only reference `M1`. | Refusal: `RuleCoverageUnusedMapping` naming `M2`. |
| `TC-PKG-10` | `package_input_coverage_dir_forbidden` | Negative Control | Package contains an input `coverage/` directory or replica. | Refusal: `RulePackageStrayFile` naming `coverage/` (exit 2). |
| `TC-PKG-11` | `mapping_already_in_destination_ledger_packaged` | Self-Contained Closure | Batch references mapping `M1` which is already retained in destination ledger repository. Batch packages `mappings/M1.json`. | Pass: package satisfies self-contained closure; publisher writes `mappings/M1.json` as verified no-op. |

### 5.3 Coverage Reason & Gap Invariant Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-RSN-01` | `reason_unknown_origin_and_model` | Positive | Source stream has unknown origin and unconfigured model. | `reasons` carries `[{code: "unknown_fields", source: "src-1"}]`. `gaps = "1"`. |
| `TC-RSN-02` | `reason_ambiguous_zero_detail` | Positive | Source has 3 present zero detail fields under `default_may_mask_absence`. | Single reason `{code: "unknown_fields", source: "src-1"}`. `gaps = "1"`. |
| `TC-RSN-03` | `reason_deduplication_same_source` | Positive | Source has unknown model + 3 ambiguous zero details. | Both collapse to `{code: "unknown_fields", source: "src-1"}`. `len(reasons) == 1`, `gaps = "1"`. |
| `TC-RSN-04` | `reason_multi_source_distinct` | Positive | Source 1 and Source 2 both have `unknown_fields`. | `reasons` has 2 entries: `("unknown_fields", "src-1")` and `("unknown_fields", "src-2")`. `gaps = "2"`. |
| `TC-RSN-05` | `reason_unreadable_stream` | Positive | Source file cannot be opened or decoded. | `reasons` carries `{code: "source_unavailable", source: "src-1"}`. `status = "partial"`. |
| `TC-RSN-06` | `reason_gaps_count_mismatch` | Negative Control | `reasons` has 1 entry, but `counts.gaps = "2"`. | Refusal: `RuleCoverageGapsInvariant` (`gaps must equal len(reasons)`). |
| `TC-RSN-07` | `reason_code_outside_closed_set` | Negative Control | `reasons` carries `{code: "missing_token_rate", source: "src-1"}`. | Refusal: `RuleUnknownEnum` / `coverage_reason_code_unknown`. |
| `TC-RSN-08` | `reason_source_not_in_source_ids` | Negative Control | `reasons` carries `{code: "unknown_fields", source: "unregistered-id"}` not declared in `source_ids`. | Refusal: `RuleCoverageReasonSource` (`coverage_reason_source_not_in_scope`). |
| `TC-RSN-09` | `reason_counter_unavailable_unsupported_rows` | Retained Obs / Count=1 | Single record with unavailable supported counter (`f.Presence == "unavailable"`). | Emits 1 retained observation (`count = 1`, `Spendable = false`). Mapped to `{code: "unsupported_rows", source: "src-0"}`. `counts.conflicts = "0"`. Wire `Refusals` and `Unsupported` remain 0. |
| `TC-RSN-10` | `reason_arithmetic_mismatch_unsupported_rows` | Retained Obs / Count=1 | Single record with input 10, output 5, total 99. | Emits 1 retained observation (`count = 1`, `Spendable = false`). Mapped to `{code: "unsupported_rows", source: "src-0"}`. `counts.conflicts = "0"`. Wire `Refusals` and `Unsupported` remain 0. |
| `TC-RSN-11` | `reason_impossible_subset_unsupported_rows` | Retained Obs / Count=1 | Single record with cached input 100, input 50. | Emits 1 retained observation (`count = 1`, `Spendable = false`). Mapped to `{code: "unsupported_rows", source: "src-0"}`. `counts.conflicts = "0"`. Wire `Refusals` and `Unsupported` remain 0. |
| `TC-RSN-12` | `reason_truncated_final_jsonl_unsupported_rows` | Wire Truncation / Count=0 | Source stream ends in unclosed / truncated JSON line. | Emits 0 observations (`count = 0`, no envelope ID or spend key). Truncated line counter incremented. Mapped to `{code: "unsupported_rows", source: "src-0"}`. `counts.conflicts = "0"`. |
| `TC-RSN-13` | `reason_boundary_refusal_unsupported_rows` | Wire Refusal / Count=0 | Single record refused by wire envelope validator (e.g. invalid integer lexeme). | Emits 0 observations (`count = 0`). Counted in `Refusals` (1), mapped to `{code: "unsupported_rows", source: "src-0"}`. `counts.conflicts = "0"`. |
| `TC-RSN-14` | `reason_additive_composition_conflict_and_arithmetic` | Additive Reasons | Source 1 emits observation A with arithmetic mismatch. Source 2 emits distinct observation B with same spend key. | `counts.conflicts = "1"`. Both A and B marked `Conflict: true`, `Spendable: false`. Reasons carry `("conflict", "src-1")`, `("conflict", "src-2")`, and `("unsupported_rows", "src-1")`. `len(reasons) == 3`, `gaps = "3"`. Original sealed IDs and diagnoses intact. Obs A retained in shard (`count = 1`). Wire `Refusals` remain 0. |
| `TC-RSN-15` | `reason_additive_composition_unknown_fields` | Additive Reasons | Source 1 emits observation with arithmetic mismatch under unknown origin binding (`Friend == nil`). | 1 retained observation. Reasons compose additively: `("unsupported_rows", "src-1")` and `("unknown_fields", "src-1")`. `len(reasons) == 2`, `gaps = "2"`. |

### 5.4 Two-Phase Directory, Observation Validation & Durability Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-VAL-01` | `candidate_validation_success` | Positive | Prepared dir with valid shards, mappings, inventories. No `batch.json`. Valid candidate bytes passed. | `ValidateCandidateDirectory` returns `nil`. |
| `TC-VAL-02` | `candidate_validation_refuses_marker` | Negative Control | Prepared dir accidentally already contains `batch.json`. | `ValidateCandidateDirectory` refuses: `batch.json must not exist in candidate directory`. |
| `TC-VAL-03` | `candidate_validation_owned_marker_temp_checked` | Exclusion & Mode Check | Candidate validation given owned marker temp `batch.json.tmp`. File is regular file mode 100644. | `ValidateCandidateDirectory` passes candidate checks, excluding owned marker temp. |
| `TC-VAL-04` | `candidate_validation_stray_temp_rejected` | Negative Control | Candidate dir contains unexpected `stray.tmp` or pre-existing `batch.json.tmp` before staging. | Refusal: `RuleTemporaryFilePresent` (exit 2). |
| `TC-VAL-05` | `installed_validation_success` | Positive | Fully staged dir after atomic rename of `batch.json.tmp` to `batch.json`. | `ValidateInstalledDirectory` returns `nil`. |
| `TC-VAL-06` | `installed_validation_forbids_tmp_and_extra_files` | Negative Control | (a) Installed dir contains lingering `batch.json.tmp` $\implies$ Refusal: `RuleTemporaryFilePresent`. (b) Installed dir contains unexpected extra file (e.g. `extra.txt`) $\implies$ Refusal: `RulePackageStrayFile` (strict package boundary). | Exit 2. Lingering temporary files and unexpected extra files strictly forbidden. |
| `TC-VAL-07` | `no_clobber_destination_exists` | Negative Control | Collector invoked targeting an existing directory `out/batch-01`. | Refusal: `RuleDestinationExists` (exit 2). No files touched or modified. |
| `TC-VAL-08` | `strict_symlink_rejection` | Negative Control | Package contains a symlink `records/link.jsonl -> ../shard.jsonl`. | Refusal: `RuleSymlinkForbidden` (exit 2). |
| `TC-VAL-09` | `crash_recovery_incomplete_batch` | Crash Boundary | Process killed during staging; leaves `batch.json.tmp` on disk without `batch.json`. Next publish run checks dir. | `publish --batch` refuses dir (exit 2, `reason=malformed` / incomplete batch). |
| `TC-VAL-10` | `atomic_rename_competing_marker_race` | Concurrency / No-Clobber | Competing `batch.json` inserted before atomic no-replace commit. | Atomic no-replace rename fails with `EEXIST` / `RuleMarkerExists` (exit 2). Existing marker preserved byte-identical (no clobber). |
| `TC-VAL-11` | `durability_fsync_ancestor_directories_trace` | Durability / Trace | Instrumented filesystem trace checks syscall order during write. | Verifies: `mkdir(root)` $\to$ `fsync(parent-dir)` $\to$ mkdir intermediate tree $\to$ write & fsync files $\to$ bottom-up sync: `fsync(day)` $\to$ `fsync(bench)` $\to$ `fsync(friend)` $\to$ `fsync(records)` (and `fsync(mappings)`, `fsync(inventories)`) $\to$ `fsync(root)` $\to$ `write & fsync(batch.json.tmp)` $\to$ atomic no-replace rename $\to$ `fsync(root)`. Explicit fail-stop on any sync failure. |
| `TC-VAL-12` | `shard_observation_malformed_envelope` | Negative Control | Shard JSONL line contains invalid JSON or violates `observation/2` schema. | Refusal: `RuleObservationMalformed` (exit 2). |
| `TC-VAL-13` | `shard_record_wrong_origin_path` | Negative Control | Shard at `records/alice/bench1/2026-09-14/...` contains observation with origin `friend: "bob"`. | Refusal: `RuleRecordPathOriginMismatch` (exit 2). |
| `TC-VAL-14` | `shard_record_unallocated_and_underscore_paths` | Positive | Shard at `records/_/_/unallocated/...` containing observations with nil friend, bench, and day. | Validation passes (valid origin-path match). |
| `TC-VAL-15` | `inventory_sorted_id_set_order_independent` | Positive | Shard lines ordered differently from inventory array, but identical sorted ID set. | Validation passes (sorted-ID-set equality confirmed). |
| `TC-VAL-16` | `link_unlink_fallback_crash_unpublishable` | Crash Boundary | Simulates crash during link/unlink fallback between link and unlink, leaving both `batch.json` and `batch.json.tmp` on disk. | `ValidateInstalledDirectory` refuses batch with `RuleTemporaryFilePresent` (exit 2). Unfinished staging is strictly unpublishable. |

---

## 6. Recommended Next Implementation Steps

1. **Records Owner Approval**: Records owner reviews and affirms this decision packet.
2. **Implement Whole-Package Validator in `internal/tokens` (or `internal/pkgvalid`)**:
   - Note: Envelope validators for `nova.tokens.observation/2`, `nova.tokens.mapping/2`, and `nova.tokens.coverage/2` are already implemented and tested in `internal/records`. Whole-package validation in `internal/tokens` (or `internal/pkgvalid`) composes these sealed validators across the directory tree, verifying shard paths, inventory comparisons, candidate/installed boundaries, and bottom-up durability sync ordering.
   - Implement the test cases from Section 5.2 and 5.4 in `internal/tokens/package_test.go` (or `internal/pkgvalid`).
3. **Extend Codex Decoder with `DecodeCodexMultiSource`**:
   - Enforce preconditions upfront: source/reader length equality (`len(sources) == len(readers)`), ordinal agreement (`sources[i].Ordinal == i`), source-ID uniqueness (no duplicate `SourceID`s), and origin completeness (§1.2).
   - Output `MultiSourceDecoding` with `ObservationProvenance` pairing each observation with its `SourceOrdinal` and `SourceID`.
   - Separate retained observations (count = 1; unavailable counters, arithmetic mismatches, impossible subsets) from wire refusals / truncated lines (count = 0; tracked in `Refusals`, `Unsupported`, `TruncatedLines` counters). Do not increment wire counters for retained observations.
   - Implement additive reason composition across conflicts, unsupported rows, and unknown fields.
   - Preserve distinct key conflict cardinality ($|V(k)| > 1$).
4. **Wire Native Collector**:
   - Expose native collector CLI in `cmd/nova-tokens` targeting the two-phase validator interface with fresh-dir no-clobber, pinned atomic no-replace commit (`renameatx_np` / `renameat2` / `link-unlink`), and bottom-up durability fsync sequence (`mkdir(root)` $\to$ `fsync(parent-dir)` $\to$ create intermediate tree $\to$ write & sync files $\to$ bottom-up sync day $\to$ bench $\to$ friend $\to$ records (and mappings/, inventories/) $\to$ root $\to$ write & sync `batch.json.tmp` $\to$ atomic no-replace rename $\to$ `fsync(root)`), with explicit fail-stop on any sync failure.
