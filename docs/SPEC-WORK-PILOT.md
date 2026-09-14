# nova-work: resident engine and roadmap parity

Proposed integration requirements for [SPEC-WORK.md](SPEC-WORK.md), following
Glenn's questions on 2026-09-14. This records the requested production destination;
a first pilot increment does not redefine completion as a smaller feature set.

## Engine and representation

Use Common Lisp for the supervised resident work engine: typed data, stable-ID
indexes, recursive policies and incremental updates operate in one process.
A Go CLI may remain a thin transport/integration client consistent with the other
nova tools. The engine/runtime packaging and supported platforms must be pinned
and tested before release. Lisp is not itself the complexity guarantee; maintained
indexes and bounded access supply that guarantee. Imported S-expressions remain
restricted data and never enter eval or reader evaluation.

The canonical containment forest spans open and closed members. A node has one
owning parent and a stable ID; references and roadmap coordinates do not create
additional ownership, tasks or cost. O is the current open membership view, C
preserves closed events and indexed historical facts, and W is the live-lease
view within O. Closing/reopening changes membership and event history, not the
node's identity or its canonical owning parent. Daily storage partitions are not
new containment parents. Repository work sets contain features/work sets and
leaf tasks; category is indexed metadata, not mandatory extra wrapper nodes.

## Counts are read directly

Maintain a root open-item counter as part of each accepted mutation envelope.
The resident current-revision |O| query reads that counter in constant time;
it must not trigger a lazy full rollup, scan, parse or replay after mutation.
Counts use canonical item IDs once, excluding references, attempts and history
records. Report the counting unit and revision. Keep distinct counters for open
linked GitHub issues and open task leaves; neither is silently labelled |O|.
A closure/reopen cascade updates all affected counters before the next read.

Mutation cost may depend on affected nodes and ancestors. Startup/recovery may
reconstruct counters from the canonical state; ordinary queries cannot. A test
must mutate, query repeatedly and show zero node visits/parses/replays for |O|,
then compare against an independent full count after close/reopen/import replay.
Arbitrary new filters are not promised constant time.

## Port the whole fixed-table roadmap workflow

A :roadmap node stores ordered named axes, coordinate-to-:ref mappings, scope
revision and completion policy. Its references select canonical feature/work
nodes; each target can contain nested subtasks. The same target's ownership,
acceptance evidence and history feed every view. Arbitrary dimensions are allowed;
languages are one example. Rendering to a table requires an explicit two-axis
projection or fixed selections for extra axes, never a silently flattened matrix.

Retain all current prototype capabilities: bounded restricted-data validation,
duplicate/dangling/cycle checks, required-work rollups, exact roadmap selection,
Unicode-safe marker replacement, deterministic rerendering, drift checks,
empty/whitespace-only evidence refusal and no sentinel collision at literal EOF.
Fix the prototype's known quadratic descendant-set and pre-parse depth gaps as
part of production work; copying the prototype verbatim is not completion.

Provide a read-only Markdown output mode for the selected roadmap so a coordinator
can paste a table here without creating a file or calling a model to recompute
cells. Use the same renderer for a marked region in ROADMAP.md. Render from one
captured work/evidence/scope revision; include that provenance in the receipt.
The file-update mode changes only the selected marker region, refuses missing,
duplicate or reversed markers, writes atomically and preserves all other bytes.
A check mode reports drift without writing. Respect existing working-tree edits;
rendering a target repository is not implicit permission to commit or push it.

Store each configured projection's target repository/path, marker pair and display
policy as non-executable view metadata associated with the roadmap, so the
coordinator does not reconstruct command flags from memory. Resolve only within
explicitly configured permitted target roots. Persist target state and publishing
receipts separately from the authoritative progress data. Missing mappings or
conflicting changes are explicit refusals, never guessed destinations.

Use Glenn's locked display: rows are features, columns are selected axis members;
center the status cells; tick only for fully verified, X for every other state;
percentage summaries show z% only. Preserve partial and unknown work internally.
Each column percentage is fully verified applicable feature cells divided by
applicable feature rows, not averaged subtask percentages. Empty denominators
remain explicitly undefined. Discoveries append visibly and preserve baseline
membership; scope removal cannot masquerade as completion.

Cache derived cell and column summaries, invalidate the affected references after
mutation, and render in time proportional to selected output size. The full table
cannot be constant-time if it contains many cells. Require byte-identical Markdown
between chat-output and file-render modes for the same projection/revision,
including required shared prerequisites and private-data filtering. Measure real
operational token use before and after adoption at equivalent report quality;
implementation tokens are sunk and excluded from that comparison.
