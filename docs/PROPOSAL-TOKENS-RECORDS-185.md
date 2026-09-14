# Finite Records Contract Decision Packet: Nova Tools Issue #185

**Status**: PROPOSED CONTRACT SPECIFICATION (For Records-Owner & Group Review)
**Date**: 2026-09-14
**Author**: Emma Antigravity (gemini-2.5-pro)
**Context**: mas-bandwidth/nova-tools#185 (Issue comment 5669226348) & Stella notes `stella-4c87c906465e`, `stella-44ee260d1b5b`
**Affects**: `internal/records`, `internal/tokens`, `cmd/nova-tokens`, `docs/SPEC-TOKENS.md` (Rule 29), `docs/PROPOSAL-TOKENS-VALIDATORS.md`
**Dependencies**: `nova.tokens.observation/2`, `nova.tokens.mapping/2`, `nova.tokens.coverage/2`

---

## Executive Summary

This decision packet settles the four blocking contract gates for native token records collection and whole-package validation identified in Issue #185:

1. **Multi-Binding Source/Result Attribution & Conflict Cardinality**: Supports multiple source streams with explicit ordinals/IDs; preserves source attribution on refusals and unsupported records; strictly rejects partial origin bindings; defines conflict cardinality as the count of *distinct conflicting identity keys*, not the count of variant records.
2. **Package `inventory_file` Allowance & Strict 3-Way Mapping Closure**: Amends Rule 29 to explicitly admit optional `inventories/<digest>.json`; enforces strict tripartite closure: $\text{Packaged Mappings} \equiv \text{Coverage } \texttt{mapping\_ids} \equiv \text{Observation } \texttt{mapping\_id}\text{s}$ (no orphans, no missing files, no unused mappings).
3. **Coverage Reason Mapping & Gap Invariants**: Maps unknown origin/model and ambiguous default-zero counters into the closed reason set `{source_unavailable, unsupported_rows, unknown_fields, partial_interval, conflict}`; enforces the structural invariant $\texttt{gaps} = \operatorname{len}(\texttt{reasons})$ over deduplicated $(code, source)$ entries.
4. **Two-Phase Directory Validation & Crash-Resistant Durability**: Establishes `ValidateCandidateDirectory` (pre-marker, checking directory contents excluding `.tmp` and forbidding pre-existing `batch.json.tmp` before staging) and `ValidateInstalledDirectory` (post-marker, strictly forbidding `batch.json.tmp` and any unexpected extra files); pins fresh-destination no-clobber, strict symlink rejection, explicit fsync ordering across nested shard/mapping/inventory directories and the top-level directory before and after atomic `batch.json.tmp` $\to$ `batch.json` rename without overwriting an existing marker (no-clobber), and fail-stop recovery.

---

## 1. Multi-Binding Source/Result Attribution & Conflict Cardinality

### 1.1 Multi-Binding Collection Model

A native collection invocation processes an ordered sequence of declared source streams, each associated with its own declared origin provenance binding. Independent per-binding decoding cannot be concatenated naively: deduplication, conflict detection, and spendability must be evaluated globally across all sealed observations in a single reconciliation phase.

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

// SourceRefusal records an observation rejected at the records boundary, retaining source provenance.
type SourceRefusal struct {
    SourceOrdinal int    `json:"source_ordinal"`
    SourceID      string `json:"source_id"`
    ResponseID    string `json:"response_id"`
    Rule          string `json:"rule"`
    Field         string `json:"field"`
}

// SourceUnsupported records counts of unsupported wire shapes attributed per source.
type SourceUnsupported struct {
    SourceOrdinal int            `json:"source_ordinal"`
    SourceID      string         `json:"source_id"`
    Shapes        map[string]int `json:"shapes"` // Fixed shape vocabulary -> count
}

// MultiSourceDecoding represents the reconciled output of multi-binding decoding.
type MultiSourceDecoding struct {
    Observations []CodexObservation  // All sealed observations, globally reconciled
    Refusals     []SourceRefusal      // Boundary refusals with exact source attribution
    Unsupported  []SourceUnsupported  // Per-source counts of unsupported wire shapes
    OwedTasks    []string             // Distinct owed coverage tasks from mappings
}
```

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

### 1.3 Conflict Cardinality Definition

Let $K$ be the set of unique event/spend keys (`SpendKey` = `[namespace, event_key...]`) across all decoded observations. For each key $k \in K$, let $V(k)$ be the set of distinct observation envelope content IDs:

$$V(k) = \{ \operatorname{id}(obs) \mid obs \in \text{Observations} \land obs.\text{SpendKey} = k \}$$

* **Duplicates**: If $|V(k)| = 1$ and the key appears $m > 1$ times, all occurrences after the first are marked `Duplicate = true`. No conflict exists.
* **Conflicts**: If $|V(k)| > 1$, every observation for that key is marked `Conflict = true`, `Spendable = false`, and excluded from normalized spend.
* **Cardinality Invariant**:
  $$\texttt{counts.conflicts} = \big| \{ k \in K \mid |V(k)| > 1 \} \big|$$

The `conflicts` count in `coverage/2` is the count of **distinct conflicting identity keys**, NOT the total number of variant records or observations participating in conflicts.

*Example*:
- Key `A` appears 4 times with 3 distinct observation hashes $\implies 1$ conflict key.
- Key `B` appears 2 times with 2 distinct observation hashes $\implies 1$ conflict key.
- Key `C` appears 3 times with identical observation hashes $\implies 0$ conflict keys (2 duplicates).
- Total `counts.conflicts` = $1 + 1 + 0 = 2$.

---

## 2. Package `inventory_file` Allowance & Strict 3-Way Mapping Closure

### 2.1 Rule 29 Package Allowance Amendment

`SPEC-TOKENS.md` Rule 29 is amended to explicitly admit optional shard inventory files:

A `--batch <dir>` publication package consists of:
1. Exactly one `batch.json` at root (the `nova.tokens.coverage/2` envelope).
2. Referenced observation shards at `records/<friend>/<bench>/<day>/<shard-sha256-hex>.jsonl`.
3. Referenced mapping manifests at `mappings/<mapping-sha256-hex>.json`.
4. **Optional Inventory Files**: Shards using the `inventory_file` tagged branch reside at `inventories/<inventory-sha256-hex>.json`.
5. **Coverage Replica** (optional/installed): `coverage/<collector-friend>/<collection-bench>/<day>/<coverage-sha256-hex>.json`.

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

| Condition | Violation Code | Consequence |
|---|---|---|
| $M_{\text{pkg}} \setminus M_{\text{cov}} \neq \emptyset$ | `RulePackageOrphanMapping` | Refusal: file exists in `mappings/` but is not declared in coverage. |
| $M_{\text{cov}} \setminus M_{\text{pkg}} \neq \emptyset$ | `RulePackageMissingMapping` | Refusal: mapping declared in coverage is missing from `mappings/`. |
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

When native collection encounters incomplete or ambiguous metadata, it maps them into the closed vocabulary as follows:

| Condition | Mapped Reason Code | Source Scope (`reasons.source`) | Notes |
|---|---|---|---|
| Entire source stream missing, unreadable, or 0 valid bytes | `source_unavailable` | Specific `source_id` | Stream could not be read or opened. |
| Stream unparseable or malformed container header | `source_unavailable` | Specific `source_id` | Fatal container-level read failure. |
| Unknown origin (`friend`/`bench`/`basis="unknown"`) | `unknown_fields` | Specific `source_id` | Records retained with unattributed origin. |
| Unknown / unconfigured model ID on valid response | `unknown_fields` | Specific `source_id` | Model is absent/ambiguous; response is retained. |
| Ambiguous zero detail counters (`default_may_mask_absence`) | `unknown_fields` | Specific `source_id` | Detail counter emitted as 0 may mask unmeasured absence. |
| Unmapped source shapes or unsupported wire types | `unsupported_rows` | Specific `source_id` | Counted in `Unsupported`, excluded from records. |
| Collection interval boundary truncated | `partial_interval` | `source_id` or `null` | Timestamps cut across active window. |
| Conflicting spend keys detected in stream | `conflict` | Specific `source_id` | Key has multiple conflicting observation variants. |

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

### 4.1 Validator Go API

Two distinct functions provide pre-commit validation and post-install validation:

```go
package records

// ValidateCandidateDirectory verifies a prepared batch directory BEFORE batch.json is installed.
// dir must exist and must NOT contain batch.json.
// Candidate validation checks directory contents excluding .tmp files (such as batch.json.tmp),
// permitting staging while verifying all referenced package artifacts.
// The staging workflow strictly forbids any pre-existing batch.json.tmp prior to staging.
// candidateBatchBytes contains the canonical UTF-8 bytes of the candidate nova.tokens.coverage/2 envelope.
func ValidateCandidateDirectory(dir string, candidateBatchBytes []byte) error

// ValidateInstalledDirectory verifies a completed batch directory containing batch.json.
// dir must exist and MUST contain batch.json.
// It strictly forbids batch.json.tmp and any unexpected extra files (strict package boundary).
func ValidateInstalledDirectory(dir string) error
```

### 4.2 Two-Phase Validation Lifecycle

```
[ Collection Phase ]
  │
  ├─ 1. Exclusive Directory Creation (mkdir, O_EXCL, refuse if exists)
  │
  ├─ 2. Stream & Write Shards, Mappings, Inventories
  │     └─ fsync each file
  │     └─ fsync nested directories:
  │           - records/<friend>/<bench>/<day>
  │           - mappings/
  │           - inventories/
  │     └─ fsync top-level dir
  │
  ├─ 3. Phase 1 Validation (Candidate Validation):
  │     - Verify no pre-existing batch.json.tmp or batch.json before staging
  │     - ValidateCandidateDirectory(dir, candidateCoverageBytes)
  │       (checks package structure and files, excluding .tmp files)
  │     - (Refuse if invalid; dir remains marker-free and unpublishable)
  │
  ├─ 4. Stage Marker: Write batch.json.tmp
  │     └─ fsync batch.json.tmp
  │
  ├─ 5. Commit Marker: Atomic Rename batch.json.tmp -> batch.json
  │     - No-clobber check: must NOT overwrite an existing batch.json marker
  │     - fsync top-level dir immediately after rename
  │
  └─ 6. Phase 2 Validation / Publication Ingestion:
        ValidateInstalledDirectory(dir)
        - batch.json MUST exist and validate
        - Strictly forbids batch.json.tmp (RuleTemporaryFilePresent)
        - Strictly forbids any unexpected extra files (RulePackageStrayFile, strict package boundary)
```

### 4.3 Validation Checklist

Both validators execute the verification matrix against the batch:

1. **File System Invariants**:
   - Every entry under `dir` inspected with `os.Lstat`.
   - **No Symlinks**: Any symlink returns `RuleSymlinkForbidden` (exit 2).
   - **Temporary Files & Exclusion**:
     - *Candidate Validation*: Directory contents are verified excluding `.tmp` files (e.g. staged `batch.json.tmp`). However, the staging lifecycle strictly forbids pre-existing `batch.json.tmp` before staging begins.
     - *Installed Validation*: Lingering `batch.json.tmp` or any `*.tmp` file is strictly forbidden and returns `RuleTemporaryFilePresent` (exit 2).
   - **Permissions**: Every file must be mode `100644`; every directory mode `0755`.
   - **Strict Package Boundary**:
     - Only `records/`, `mappings/`, `inventories/`, `coverage/`, and (for installed directories) `batch.json` are permitted.
     - Any hidden file (e.g. `.DS_Store`), undeclared directory, or unexpected extra file returns `RulePackageStrayFile` (exit 2).
2. **Coverage Envelope Verification**:
   - Candidate bytes / `batch.json` decoded and verified with `records.ValidateEnvelope`.
   - Schema must be `"nova.tokens.coverage/2"`.
   - Canonical CID computed from canonical body bytes.
3. **Shard & Inventory Verification**:
   - Every shard in `coverage.shards` verified at `records/<friend>/<bench>/<day>/<shard-id-hex>.jsonl`.
   - Shard byte SHA-256 matches `shard_id` and filename.
   - Observation line count equals `record_count`.
   - For `inline_ids`: observation envelope IDs match `inline_ids` in order.
   - For `inventory_file`: `inventories/<inventory-id-hex>.json` exists, byte SHA-256 matches `inventory_file`, content is canonical JSON array of CIDs ending in `\n`, IDs match shard observation IDs in order.
4. **Mapping Closure**:
   - Every mapping file in `mappings/` validated with `records.ValidateEnvelope`.
   - Filename hex matches mapping envelope canonical CID.
   - 3-way mapping closure holds ($M_{\text{pkg}} \equiv M_{\text{cov}} \equiv M_{\text{obs}}$).
5. **Count Equations**:
   - `gaps == len(reasons)`
   - `records_emitted == sum(shards.record_count)`
   - `observations == len(unique_observation_ids)`
   - `conflicts == len(conflicting_spend_keys)`

### 4.4 Durability, No-Clobber, and Crash Recovery Rules

1. **No-Clobber Fresh Destination and Marker Protection**:
   - The destination directory must not exist prior to collection.
   - Attempting to output into an existing directory is refused (`RuleDestinationExists`, exit 2).
   - **No Clobber on Rename**: Atomic rename must not overwrite an existing marker. If `batch.json` already exists at the destination path prior to renaming `batch.json.tmp`, the renamer refuses to clobber (`RuleMarkerExists`, exit 2), preserving the existing file and failing stop.
2. **Durability Fsync Ordering (Nested & Top-Level Directories)**:
   - Shards, mappings, and inventories are written and explicitly flushed (`file.Sync()`).
   - Durability fsync covers nested directories, not just the top-level directory: each nested directory (`records/<friend>/<bench>/<day>`, `mappings/`, `inventories/`) is opened and synced (`dir.Sync()`) to ensure directory entries are persisted to disk.
   - Top-level directory is synced (`dir.Sync()`).
   - `batch.json.tmp` is written and flushed (`file.Sync()`).
   - Atomic rename `batch.json.tmp` $\to$ `batch.json` is executed without overwriting an existing marker.
   - Top-level directory is flushed (`dir.Sync()`) immediately after rename to guarantee the marker dentry is committed to durable storage.
3. **Crash Recovery Semantics**:
   - Presence of valid `batch.json` without any lingering `.tmp` files is the sole atomic commit point.
   - If collection is killed or crashes before rename:
     - `batch.json` is absent (or `batch.json.tmp` exists).
     - Directory is considered **uncommitted and unpublishable**.
     - `publish --batch` refuses any directory missing `batch.json` or containing `.tmp` files (exit 2).
   - **No In-Place Resumption**: Retries must target a fresh directory. The abandoned directory is left intact for inspection and never cleaned automatically by tool invocations.

---

## 5. Concrete Acceptance Test Cases

### 5.1 Multi-Binding & Conflict Cardinality Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-MB-01` | `multi_binding_two_sources_success` | Positive | Two readers, each with distinct complete `SourceBinding`. Reader 0 has 2 turns; Reader 1 has 3 turns. | Exit 0. Decodes 5 observations. Refusals empty. Global reconciliation confirms 5 spendable records. |
| `TC-MB-02` | `multi_binding_refusal_retains_source` | Positive/Attribution | Reader 0 has valid turn; Reader 1 has invalid number lexeme (`007`). | Exit 0. 1 observation emitted. Refusal list carries `SourceOrdinal: 1`, `SourceID: "src-1"`, `Rule: "integer_lexeme"`, `Field: "input_tokens"`. |
| `TC-MB-03` | `multi_binding_unsupported_retains_source` | Positive/Attribution | Reader 0 has 1 response; Reader 1 has 2 `token_count` snapshot shapes. | Exit 0. `SourceUnsupported` for ordinal 1 records `{"token_count": 2}`. Ordinal 0 has 0 unsupported. |
| `TC-MB-04` | `partial_binding_friend_no_bench` | Negative Control | Source binding has `Friend: "emma"`, `Bench: nil`, `BindingID: "cid:1"`. | Refusal: `RuleBindingPartial`. Execution aborts before reading source lines. |
| `TC-MB-05` | `partial_binding_bench_no_friend` | Negative Control | Source binding has `Friend: nil`, `Bench: "studio"`, `BindingID: "cid:1"`. | Refusal: `RuleBindingPartial`. |
| `TC-MB-06` | `partial_binding_underscore_label` | Negative Control | Source binding has `Friend: "_"`, `Bench: "studio"`, `BindingID: "cid:1"`. | Refusal: `RuleLabelSyntax` / `RuleBindingPartial`. Null must be `nil`, not `"_"`. |
| `TC-MB-07` | `explicit_unknown_binding_accepted` | Positive | Source binding has `Friend: nil`, `Bench: nil`, `BindingID: nil`, `Basis: "unknown"`. | Exit 0. Records emitted with origin `{null, null, "unknown", null}`. |
| `TC-MB-08` | `conflict_cardinality_multi_variant` | Positive/Math | Key `K1` has 3 distinct variants; Key `K2` has 2 distinct variants; Key `K3` has 2 identical copies. | `counts.conflicts = "2"`. Key `K3` is marked duplicate, not conflict. All 5 records for `K1` and `K2` marked `Conflict: true`. |
| `TC-MB-09` | `conflict_across_different_bindings` | Positive/Math | Reader 0 and Reader 1 emit the same response ID with different token counts. | Reconciled globally: `counts.conflicts = "1"`. Both records marked `Conflict: true`. |

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

### 5.4 Two-Phase Directory & Crash Recovery Tests

| Test ID | Name | Category | Input / Setup | Expected Outcome |
|---|---|---|---|---|
| `TC-VAL-01` | `candidate_validation_success` | Positive | Prepared dir with valid shards, mappings, inventories. No `batch.json`. Valid candidate bytes passed. | `ValidateCandidateDirectory` returns `nil`. |
| `TC-VAL-02` | `candidate_validation_refuses_marker` | Negative Control | Prepared dir accidentally already contains `batch.json`. | `ValidateCandidateDirectory` refuses: `batch.json must not exist in candidate directory`. |
| `TC-VAL-03` | `candidate_validation_excludes_tmp_forbids_preexisting` | Negative & Exclusion | (a) Pre-existing `batch.json.tmp` exists before staging $\implies$ refusal (`RuleTemporaryFilePresent`). (b) Candidate validation scans directory contents excluding `.tmp` files (e.g. staged `batch.json.tmp`), validating candidate package structure. | Pass candidate checks when excluding `.tmp`; refuse pre-existing `.tmp` before staging. |
| `TC-VAL-04` | `installed_validation_success` | Positive | Fully staged dir after atomic rename of `batch.json.tmp` to `batch.json`. | `ValidateInstalledDirectory` returns `nil`. |
| `TC-VAL-05` | `installed_validation_forbids_tmp_and_extra_files` | Negative Control | (a) Installed dir contains lingering `batch.json.tmp` $\implies$ Refusal: `RuleTemporaryFilePresent`. (b) Installed dir contains unexpected extra file (e.g. `extra.txt`) $\implies$ Refusal: `RulePackageStrayFile` (strict package boundary). | Exit 2. Lingering temporary files and unexpected extra files strictly forbidden. |
| `TC-VAL-06` | `no_clobber_destination_exists` | Negative Control | Collector invoked targeting an existing directory `out/batch-01`. | Refusal: `RuleDestinationExists` (exit 2). No files touched or modified. |
| `TC-VAL-07` | `strict_symlink_rejection` | Negative Control | Package contains a symlink `records/link.jsonl -> ../shard.jsonl`. | Refusal: `RuleSymlinkForbidden` (exit 2). |
| `TC-VAL-08` | `crash_recovery_incomplete_batch` | Crash Boundary | Process killed during staging; leaves `batch.json.tmp` on disk without `batch.json`. Next publish run checks dir. | `publish --batch` refuses dir (exit 2, `reason=malformed` / incomplete batch). |
| `TC-VAL-09` | `atomic_rename_no_clobber_marker` | Crash / Concurrency Boundary | Staging attempts atomic rename when `batch.json` already exists at destination path. | Renamer refuses to overwrite/clobber existing marker (`RuleMarkerExists`, exit 2). Existing marker remains byte-identical. |
| `TC-VAL-10` | `durability_fsync_nested_directories` | Durability / Trace | Instrumented filesystem trace checks syscall order during write. | Verifies: `write(shard)` $\to$ `fsync(shard)` $\to$ `close(shard)` $\to$ `fsync(records/<friend>/<bench>/<day>)` $\to$ `write(mapping)` $\to$ `fsync(mapping)` $\to$ `fsync(mappings)` $\to$ `write(inventory)` $\to$ `fsync(inventory)` $\to$ `fsync(inventories)` $\to$ `fsync(top-dir)` $\to$ `write(batch.tmp)` $\to$ `fsync(batch.tmp)` $\to$ atomic rename (no-clobber) $\to$ `fsync(top-dir)`. |

---

## 6. Recommended Next Implementation Steps

1. **Records Owner Approval**: Records owner reviews and affirms this decision packet.
2. **Implement Whole-Package Validator**:
   - Add `ValidateCandidateDirectory` and `ValidateInstalledDirectory` to `internal/records`.
   - Implement the test cases from Section 5.2 and 5.4 in `internal/records/package_test.go`.
3. **Extend Codex Decoder**:
   - Update `DecodeCodexReaders` to `DecodeCodexMultiSource(m *CodexMapping, sources []SourceBinding, readers []io.Reader)`.
   - Retain source ordinals/IDs in `SourceRefusal` and `SourceUnsupported`.
   - Implement distinct key conflict cardinality.
4. **Wire Native Collector**:
   - Expose native collector CLI in `cmd/nova-tokens` targeting the two-phase validator interface with fresh-dir no-clobber and durability fsync sequence covering nested shard, mapping, and inventory directories.
