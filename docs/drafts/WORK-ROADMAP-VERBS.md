# Proposed roadmap-view operations — draft against PR231 `7db3b95`

This proposal owns roadmap configuration, targets and `axis --remove`; it needs friend review.

## Model and operation boundary

A roadmap is a node plus a durable view record. `node add`, `axis --add`, and `cell`
retain ownership. Create writes its initial view atomically; configure changes view
metadata, projections own destinations, and axisless rows own ordered membership.

```
nova-work roadmap create --session <path> <write flags> --id <id> --under <parent-id>
  [--title <text>] --row-kind <feature|epic|work-set>
  --aggregation <required-members|all-members|leaves>
  --completion-policy all-required-features
  (--axes-none | --axis-id <id> ...)
  [--permit-root <root-id> ...] --reason <text> [--dry-run]

nova-work roadmap configure --session <path> <write flags> --roadmap <id>
  [--row-kind <...>] [--aggregation <...>]
  [--completion-policy all-required-features]
  [--axes-none | --axis-id <id> ...]
  [--permit-root <root-id> ... | --roots-empty] --reason <text> [--dry-run]

nova-work roadmap projection add --session <path> <write flags> --roadmap <id>
  --projection <id> --root <root-id> --repo <repo-identity> --path <relative-path>
  --start <marker> --end <marker> --policy markdown-table
  [--row-axis <id> --column-axis <id> --fixed <axis-id>=<member-id> ...]
  --reason <text> [--dry-run]
nova-work roadmap projection remove --session <path> <write flags> --roadmap <id>
  --projection <id> --reason <text> [--dry-run]

nova-work roadmap row (add|remove) --session <path> <write flags> --roadmap <id>
  --member <node-id> --reason <text> [--dry-run]   (axisless roadmaps only)

nova-work axis --session <path> <write flags> --roadmap <id> --axis <id>
  --remove <member-id> --reason <text> [--dry-run]
```

`--axis-id` is repeatable in order. `--axes-none` is `:axes ()`; otherwise ids are
distinct. Zero or one axis has rows but no cells; two or more are a matrix. Layout
changes only while every axis has no members, axisless members are empty and no cells
exist; adding dimensions later
remains unresolved.

## Typed wire and canonical records

The wire operations are `roadmap.create`, `roadmap.configure`, `roadmap.row.add/remove`,
`roadmap.projection.add/remove`, and `axis.remove`. Normal write flags and bounded JSON
framing apply. A create JSON args object has direct typed
fields `id`, `under`, `title`, `row_kind`, `aggregation`, `completion_policy`, `axes`,
`members`, `permitted_roots`, and `reason`; missing/null title is absent, `axes:[]` and
`members:[]` are the required axisless initial state, while
`permitted_roots:[]` are empty values. Its structure payload field order is:

```
:verb :node-type :title :under :row-kind :aggregation :completion-policy
:axes :members :permitted-roots :reason
```

with `:verb :roadmap-create` and `:node-type :roadmap`. It creates the normal
node/containment indexes then the view record; every create starts with `members:[]`.
An axisless create additionally has `axes:[]`; a one-axis create has no cells.

Configure uses fixed `keep` or `set` patches for `row_kind`, `aggregation`,
`completion_policy`, `axes`, and `permitted_roots`; the three policies reject clear,
while `[]` is explicit empty axes/roots. Missing/null, unknown/duplicate keys, bad
types and all-keep refuse. Canonically these are `(:keep)` and `(:set V)`, in order:

```
:verb :row-kind-patch :aggregation-patch :completion-policy-patch
:axes-patch :permitted-roots-patch :reason
```

with `:verb :roadmap-configure`; the engine stores a complete ordered `:before` map
outside the requester digest. This borrows the PR293 tagged-patch convention only as a
proposal; its approval is a dependency, not assumed fact.

Axisless row add/remove have requester fields `:verb :roadmap :member :reason`, with
verbs `:roadmap-row-add` and `:roadmap-row-remove`. The member is an existing node of
the roadmap's declared row kind; add appends it to `:members`, while remove retires it
from the current view only. Neither changes the node's containment, state, evidence,
lease or repository. Container settlement/revival is handled by the ordinary cascade,
not suppressed to claim unchanged global counts. Their paired view-scope events preserve prior
ordered membership and make earlier revisions reproducible. A closed row is a valid
member: its id and evidence remain visible unless an explicit view-row removal selects
it. For a zero- or one-axis roadmap, `percent --node R` takes no `--axis`: applicable
rows are its live ordered rows and green rows use its aggregation rule. A matrix still
requires `--axis`; a non-matrix request that supplies one refuses. This explicitly
amends the current mandatory-axis grammar. An empty denominator remains undefined.

The scope registry gains three fixed shapes, serialized after their structure event:
`:view` has `:change :reason` for effective create/configure/projection operations;
`:roadmap-row` has `:change :member :reason`; `:axis-remove` has `:axis :member
:reason`. Each effective view-scope event advances roadmap scope revision. The last two
alter only the roadmap's view-required set, never the member or containment parent set.

A projection is `(:id <id> :root <root-id> :repo <repo-identity> :path <relative-path> :start <marker>
:end <marker> :policy :markdown-table :row-axis <id|absent> :column-axis <id|absent>
:fixed ((<axis-id> <member-id>) ...))`. Add serializes `:verb :projection :reason`;
remove `:verb :projection-id :reason`. Projection ids are unique per roadmap.
`root-id` and path are portable; paths are normalized, nonempty, relative and contained;
markers are distinct nonempty UTF-8 text. Only `:markdown-table` is accepted. For a
matrix, row/column axes are distinct declared axes and `:fixed` supplies exactly one
member for every other axis; unknown/missing/duplicate selections refuse. For zero or one axis, row/column selections are canonically `(:absent)` and fixed
selections are `()`; render an ordered feature/status table with no synthetic cells.
Treating one axis as this row-only view is a proposed clarification requiring agreement.
The stored repository identity must match the selected bench mapping; remapping a root
cannot silently redirect a projection to another repository.

A stored permitted root is an opaque id. Session-start `--render-root
<root-id>=<repo-identity>:<local-directory>` maps it to a bench path. File mode needs
both stored permission and that mapping; mutation alone cannot grant filesystem access.
Its grammar/ownership needs friend review.

## Admission, scope, retention, and undo

Create requires a fresh id, open parent and ordinary validation. Other verbs require
their roadmap; first-axis members use paired `axis --add`, and referenced roots stay.

`axis --remove` is a view-scope operation for any declared axis; it never calls node
remove, cancel or another terminal work verb. It atomically removes the member from the
current ordered axis and every current cell coordinate containing it, and records member
position plus removed cells as engine `:before`. On the first axis it retires
only the current roadmap row; node/evidence/state remain intact. It removes that row
from the view-required set when live (including done: only removed, cancelled and
superseded are not live). Other axes change no view-required set. Underlying tasks remain unchanged; any
roadmap container settlement/revival follows the ordinary cascade. This explicitly amends the current “nothing takes a member
off an axis” rule. An absent member refuses.

Create/configure/row/projection/axis-remove respectively update only node/roadmap,
view-membership, projection-target, axis/cell and affected projection caches. Row and
first-axis removal update only the view-required set for a live row; normal closure
leaves completed rows present. Aggregation changes how a row qualifies as green; it does not change the count of
applicable rows. Row-kind changes require every retained row to match the new kind,
otherwise refuse. No configure operation rewrites task evidence or containment. Effective view-scope actions advance
scope revision and invalidate view caches. Render changes no work/evidence/verdict.

A selected configure patch already equal to state journals its typed event with
`changed=0`, real id and `:before`, but no scope event/revision, cache invalidation or
state/index/counter delta. This proposed PR293 convention returns the original reply on
same-id retry; post-retention dedup refuses reapply. Identical projection add follows;
different same-id projection or missing removal refuses. Dry run writes no event/dedup.

Undo of configure, row/projection add/remove, and axis removal compares the relevant
current postimage, cell/member order and scope revision to its stored `:after`, then
appends a typed compensation or refuses conflict. Create requires no surviving
projection receipt/cell/member outside its preimage.
Metadata and projection changes may address a settled roadmap without reviving work.
Membership changes must apply the existing container settle/revive rules atomically,
including the rule that an empty required set is not done; this does not reopen or
cancel any underlying row. Replay must check the resulting O/C counts and indexes.
Old view events, completed rows, evidence and receipts are never erased. A settled
roadmap retains its current head (axes, members, cells, configuration, projection and
receipt identities); baseline/scope history may be bounded indexed pointers. Opening it
uses that head plus bounded closed/history reads, never all C or only the default window.

## Rendering and output

`render --view <id> --chat [--projection <id> | --row-axis <id>
--column-axis <id> --fixed <axis-id>=<member-id> ...] [--at <revision>]` needs no
filesystem mapping. Without a projection, matrix selectors are explicit as above;
zero/one-axis views need none. With a projection, chat reads only its stored display
selection, not its file. The two selection forms are mutually exclusive.
`render --view <id> --projection <id> (--file|--check) [--at <revision>]` uses stored
target metadata; `--check` is file-only. Chat and file render the same captured
work/evidence/scope revision and byte-identical Markdown. File mode reads the bounded
target `(root-id, repo identity, relative path)`, captures SHA-256 and marker offsets,
then atomically replaces only its region after hash recheck. Mismatch refuses; the
receipt records target identity, old/new hashes and render revision.

**Proposed wire exception for `--chat`:** a successful chat render returns no ordinary
`lines`; it carries exactly one bounded `artifact` object
`{"encoding":"utf8","body":"…","sha256":"<hex>","bytes":"<n>"}` beside the ordinary
request/revision fields. The client verifies byte count and SHA-256, then writes `body`
unchanged to stdout, with no `RENDER OK` prefix; refusal uses normal stderr `lines`.
The artifact must fit response/frame/output bounds; excess refuses, never truncates.
File/check retain status lines. This amends lines-only responses because a prefix cannot
be raw identical Markdown.

This is not a CAS against arbitrary noncooperating editors: an advisory per-target
renderer lock protects cooperating renderers, while an external editor can still race
the final replace. Atomic replacement preserves outside bytes from the captured file snapshot; the
stronger multi-writer guarantee and lock owner need explicit review rather than a false
claim. Missing, duplicate, reversed markers, a path outside the effective root, or a
conflicting captured target refuse. `--check` writes nothing.

Proposed success lines are `ROADMAP OK … change=<create|configure|row-add|row-remove|
projection-add|projection-remove> changed=<n>`, `AXIS OK … change=remove`, and existing `RENDER OK`
with `view=`, `projection=`, captured `scope=`, `target=`, and `emitted=`. Exact common
output fields and the global `--dry-run` spelling require the same protocol-table
amendment as PR293.

Friend review remains needed for the tagged patch convention, root-id/session-mapping
shape, one-axis row-only semantics, membership/cascade interaction, permitted-root
mutation policy, the exact historical view-pointer index,
projection receipt retention detail, and the cooperative renderer-lock boundary.

## Acceptance witnesses required before implementation is verified

These refine existing E07-F01/F02/F03/F05/F06 and shared journal/undo gates; they do
not add roadmap features or claim runtime tests have passed.

- **Axisless history:** add two ordered rows, finish one, export/reload, and reopen the
  view after the default history window. Both rows and their code/test evidence remain;
  completion has not reduced the denominator. Retiring a row records scope movement,
  preserves its underlying node and reconstructs the prior view at its captured revision.
- **Matrix retirement:** remove a first-axis row and then a different-axis member.
  Only selected coordinates retire, prior coordinate mappings remain recoverable, and
  row retirement never cancels a task. Unknown members and populated shape conversion
  refuse without partial writes. Explicit remaining-axis selections forbid flattening.
- **No-effect retry and undo conflict:** apply an equal-value configure, lose its reply,
  make another edit, and retry the first request. Return its original receipt without
  overwriting the later value. Undo restores ordered preimages only when its guards
  still match; a conflict leaves both the later work and journal intact.
- **Completed-view mutation:** metadata/render access does not revive completed work.
  Adding outstanding members applies the defined atomic revival rule; no settled
  container can silently acquire open required work. Verify counts and indexes by the
  shared reference fold after each operation.
- **Rendering parity:** use identical captured view/work/evidence revisions for chat,
  file and check, with Unicode, private rows, shared prerequisites, missing/stale cells
  and an empty denominator. Compare exact table bytes; preserve every byte outside
  markers. Conflicting target hashes and invalid markers leave the target untouched.
- **Root boundaries:** a stored root id alone grants no access. Missing mappings,
  escaping paths, symlink escapes and disallowed target identities refuse; chat can
  still render without filesystem authority. Exercise the agreed cooperative locking
  policy and explicitly retain its external-editor limitation.
- **Wire bounds:** interleave ordinary replies and a render artifact in a correlated
  batch; verify request ids, UTF-8 byte length and hash. A corrupt or oversized artifact
  produces a bounded refusal, never partial successful Markdown. Check mode cannot
  create a target, publication receipt claiming a write, or implicit commit/push.
