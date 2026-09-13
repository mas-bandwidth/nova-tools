# Keep Codex snapshots without inventing a bill

Status: source-owner proposal for [#154](https://github.com/mas-bandwidth/nova-tools/issues/154),
awaiting friends' review. This packet supplies proposed sealed mapping manifests and
synthetic acceptance data. It does not ship a decoder, resolve a real fork, or establish
September coverage. The supported [response mapping](MAPPING-TOKENS-CODEX.md) keeps working
independently. Its observations remain the preferred spend source.

An AI friend should be able to keep what a source actually says, even when it cannot yet
explain the bill. A `token_count` event holds two different snapshots with the same field
names: `info.total_token_usage` and `info.last_token_usage`. Keep both. Never add them,
subtract successive events, fill missing response spend from them, or reprice them as
provider usage. The producer can accumulate, estimate, replay and replace these counters.

## Source evidence and proposed representation

Pinned producer: `b5bffd3ec4db487e7e3dec59663875b0ef7b72ca`.
[TokenUsageInfo and context fill](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L2251)
show that context fill sets total and last `total_tokens` while defaulting other fields.
Even a positive total is not proof of measured spend. `last_token_usage` is not a response
identity, and `info.model_context_window` is capacity metadata, not consumed tokens.

Propose two mapping-defined source domains, with no new wire keys or counter aliases:

| Native group | Namespace | Mapping fixture |
|---|---|---|
| `info.total_token_usage` | `nova.codex-desktop.token-count.total` | [total](../testdata/tokens/codex-snapshots/mapping-total.json) |
| `info.last_token_usage` | `nova.codex-desktop.token-count.last` | [last](../testdata/tokens/codex-snapshots/mapping-last.json) |

Each identified event with an object-valued `info` produces one observation per group.
Its two observations share the native event locator but occupy different namespaces.
Each keeps the six original counter names, plus `model_context_window` extracted from
its sibling `info` member. This repeated capacity value describes each snapshot; it is
never counted. Reports distinguish source-event count from retained-observation count.
A valid `info: null` means no usage info: do not manufacture two zero observations.
Keep this event's missing-usage-info outcome visible in source coverage diagnostics.
This is not evidence of complete spend coverage.

The seven allowlisted raw fields are `input_tokens`, `cached_input_tokens`,
`cache_write_input_tokens`, `output_tokens`, `reasoning_output_tokens`, `total_tokens`,
and `model_context_window`. All have `number_kind: integer`, `unit: tokens`,
`spend_role: non_spend`, and `zero_semantics: unknown` in these mappings. Retain present
nonnegative integer lexemes exactly, including values beyond JavaScript's safe integer.
Missing supported fields are `absent/null/not_supplied`; explicit null, negative,
fractional, string or other wrong-typed counter values are `unavailable/null/parse_failed`.
A missing group supplies absent entries; a wrong-typed group makes its six entries
unavailable. Its sibling context window is independently extracted. A wrong-typed
non-null `info` is an unsupported row rather than guessed nested content.

Duplicate recognized keys, including JSON-unescape aliases, are ambiguous extraction
and refuse. Do not echo invalid values. Unrecognized fields never extend the allowlist.
`rate_limits`, instructions, prompts, arbitrary source objects and private paths do not
enter shared records. Structural invalidity of a retained envelope still refuses normally.
Do not impose the response mapping's `input + output = total` invariant on snapshots:
context fill is an evidenced counterexample, retained faithfully here.

## Identity and origin

| Observation member | Decision |
|---|---|
| `schema`, `source.kind`, `kind` | `nova.tokens.observation/2`, `codex_desktop`, `snapshot` |
| `source.event_key` | `[original_physical_rollout_id, native_ordinal_as_decimal_string]` |
| `source.session_id` | The native `session_meta.id` of the original physical segment, as provenance only |
| `source.producer_version` | Null unless separately evidenced by an allowlisted source field or original-source version binding; never the collector version |
| `receipt` | Only `ordinal`, as its exact native unsigned integer converted to a decimal string |
| `revision` | `{native: null, supersedes: [], basis: none}`; ordering events does not make one a correction of another |
| `origin` | Original friend/bench from an explicit origin binding, otherwise unknown; collection bench stays in collection provenance |
| `model`, `model_usage` | `{id: null, basis: unknown}`, `[]`; snapshots do not prove a model or a split, even beside a requested model |
| `repository` | Unattributed unless an independently evidenced accounting policy supplies it |
| `time` | Source record timestamp with `basis: source_aggregate`; missing timestamp is null/unknown; start/end null |

`source_aggregate` describes when the snapshot was observed, not when its cumulative
usage happened. UTC observation day may partition storage; it cannot allocate snapshot
spend to that day. Reports may display these snapshots separately with that limitation.
Requested-model information remains available through the supported response mapping;
do not relabel a whole cumulative snapshot with the most recent requested model.

The original rollout UUID must come from a trusted source identity/binding, preserved
through copying. The current path and logical thread ID cannot substitute for it.
[HistoryPosition](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/protocol/src/protocol.rs#L3022)
uses its historically named `thread_id` for a physical rollout ID; revert can change that
ID while preserving `session_meta.id`. These local fixture bindings illustrate that
separation; they are not a new public binding schema or a permission to scan private files.

## Physical lineage, copies and incomplete sources

Use the producer's physical `history_base` lineage, not `forked_from_id` alone.
[RolloutLineage](https://github.com/openai/codex/blob/b5bffd3ec4db487e7e3dec59663875b0ef7b72ca/codex-rs/thread-store/src/local/rollout_lineage.rs#L14)
starts standalone events at ordinal1. For a child, metadata occupies the inherited
exclusive boundary; its own events start at that boundary plus1. Ancestor records keep
the ancestor's key and provenance. Metadata is not a usage event.

A reference must agree on physical origin, exclusive ordinal and a complete logical
JSONL byte boundary. Compressed byte length is not a logical JSONL position.
Cycles, inconsistent cutoffs, repeated/regressed native ordinals within one original
segment and conflicting same-key content are explicit conflicts, not silent skips.
A native ordinal is an integer in the unsigned64-bit domain. Never substitute a file
line number, timestamp, counter value, collection run or path.

Equal copies preserve observation identity and add only local collection receipts.
Equal values at different native ordinals remain different raw observations.
Changed supported content under the same namespace/key retains a visible conflict;
there is no newest-wins rule. A conflict in the total group need not fabricate a
conflict in an unchanged last group. Neither group contributes normalized spend.
Changed provenance also cannot silently overwrite an earlier observation; use the
existing explicit correction/conflict machinery.

If an ancestor is unavailable, independently established own records can survive with
partial coverage. An invalid reference never becomes complete merely because some own
records were retained; retain only own records whose range and identity are independently
proved. Subagent copied prefixes, legacy rows without ordinals and migrated histories
without original ownership evidence remain explicit gaps. UI materialization can skip
or suppress records; its projected history is not an accounting inventory certificate.

Use existing coverage codes: missing source `source_unavailable`; unsupported identity
or shape `unsupported_rows`; incomplete coherent cutoff `partial_interval`; contradictory
evidence `conflict`. More detailed bounded local diagnostics may explain the cause;
do not invent extra shared coverage-code literals. Raw-source coverage and normalized
spend coverage are separate: mapping these snapshots cannot make a bill complete.

## Review and implementation acceptance

The [fixture packet](../testdata/tokens/codex-snapshots/acceptance-cases.json) contains
23 independently stated outcomes. Sources A/B/C exercise equal snapshots, exact copied
source bytes and two physical ancestry boundaries. Their metadata records deliberately
contain only extraction-relevant fields; they are synthetic decoder inputs, not a claim
that a full Rust `SessionMeta` deserializer accepts the projection. The builder must also
validate the supported source shape against complete producer-compatible synthetic input.
The [bindings](../testdata/tokens/codex-snapshots/bindings.json) contain only fictional
identities. The [eight expected envelopes](../testdata/tokens/codex-snapshots/expected-observations.jsonl)
are hand-specified accounting outcomes, not output from a decoder being tested.

Review questions for the friends: do the two namespaces make the two groups clear enough
without renaming fields; is the raw-only boundary useful; and are the lineage/copy gaps
explicit enough for a collector to report honestly? Agreement on this packet precedes
implementation. Feedback and alternative representations are welcome.

Then a builder implements decoding/resolution using the existing record API, preserves
the two immutable proposal manifests, adds its non-null implementation manifest, and
executes every listed case with independent assertions and privacy sentinels. Missing
compression/ancestry support must remain a named gap, not a complete supported case.
Check both manifests and all expected envelopes with the actual core validator; this
only proves the wire can carry them. It is not decoder or source-resolution proof.

Finally join collection, publication and views under [#181](https://github.com/mas-bandwidth/nova-tools/issues/181):
copied sessions, late events, corrections, missing periods and two benches must preserve
identities and gaps. The supported preferred-response path can ship independently;
#154 remains open until this mapping's own implementation and acceptance are verified.
