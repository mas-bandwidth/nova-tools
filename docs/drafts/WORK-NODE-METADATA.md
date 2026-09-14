# Proposed node metadata contracts — draft against PR231 `7db3b95`

This proposes only `node edit` and `node add --repo`. It neither specifies
`node move` nor creates a generic field setter. It is not approved or implemented.

## Repair the current add mismatch without inventing legacy support

At `7db3b95`, `node add` has the fixed structure/digest order `:verb`, `:node-type`,
`:title`, `:under`, `:category`, `:required`, `:acceptance`, `:reason`
(SPEC-WORK 806–21), but the missing-verb section says add may set `:links`, `:private`,
and `:version`, while its CLI grammar omits those and `--repo` (1814, 2334–39).

**Proposal for the next draft revision:** define one `node add` operation with this new,
complete requester-field order:

```
:verb :node-type :title :under :category :required :acceptance
:repo :links :private :version :reason
```

Every field is serialized, using the existing canonical distinctions: omitted or JSON
`null` is `(:absent)`; `()` is an explicit empty list; `""` is an empty text value;
`false` is a boolean value. It is the operation’s payload digest in the revised draft.

There is **no production nova-work stream** to preserve. The proposal must therefore not
claim a permanent legacy `node-add-v2` compatibility path. A loader meeting a
pre-revision draft fixture/event with the old add shape refuses `schema revision
unsupported` unless a separately approved migration names its input revision and exact
re-encoding. It never silently rereads an old shape as the new one. If no persisted
artifacts exist, fixtures are regenerated from the revised schema. This keeps any real
old digest meaningful rather than changing it by interpretation.

## CLI and JSON wire mapping

```
nova-work node add --session <path> <write flags> --id <id> --type <kind>
  (--under <parent-id> | --under-root open) [--repo <owner/name>] [--title <text>]
  [--category <label>] [--required <true|false>]
  [--acceptance <id:kind:subject:predicate> ...]
  [--link <text> ... | --links-empty | --clear-links]
  [--private <true|false>] [--version <text>] --reason <text> [--dry-run]

nova-work node edit --session <path> <write flags> --node <id>
  (--title <text>|--clear-title|--category <label>|--clear-category|
   --link <text> ...|--links-empty|--clear-links|
   --private <true|false>|--clear-private|
   --version <text>|--clear-version) ... --reason <text> [--dry-run]
```

`--under-root open` is proposed as the explicit top-level selector, mutually
exclusive with `--under <id>`. It reserves no opaque node ID. On the wire, `under`
is exactly `{"kind":"node","id":"<id>"}` or `{"kind":"open-root"}`; unknown keys
or kinds refuse. Canonical payload forms are `(:node "<id>")` and `(:open-root)`.
The current grammar cannot name O despite its top-level rule (1193–99); this adds
that missing case without interpreting an ID as a root by its spelling.

For add, a missing scalar is absent, `--links-empty` is `()`, `--clear-links` is
absent, and repeatable `--link` sets the entire ordered list. The flags in each field
family are mutually exclusive. An empty shell text is a set-empty text, not clear.

For edit, each of the five metadata fields has a mandatory tagged patch in the JSON
args, even when the CLI omitted it:

```json
{"op":"node.edit","args":{"node":"n",
 "title":{"op":"keep"}, "category":{"op":"clear"},
 "links":{"op":"set","value":[]},
 "private":{"op":"set","value":false},
 "version":{"op":"set","value":""}, "reason":"r"}}
```

The only patch objects are `{"op":"keep"}`, `{"op":"clear"}`, and
`{"op":"set","value":V}`. `clear` stores absent; `set` preserves an empty text or
empty link list. `keep` is unchanged. Missing, `null`, unknown keys, duplicate keys,
an `op`/value mismatch, or a set value of the wrong type refuse. At least one patch
must be clear or set; an all-keep request has no CLI selector and refuses. `node.add` has direct
args `id`, `node_type`, `under`, `title`, `category`, `required`, `acceptance`, `repo`,
`links`, `private`, `version`, `reason`; missing/null has the ordinary absent meaning.
Its JSON booleans are booleans, and its lists preserve order. For edit, JSON tags serialize as `(:keep)`, `(:clear)` and `(:set V)` respectively,
with V printed by the existing canonical value printer. Structure verb values are
`:node-add` and `:node-edit`, corresponding to wire `node.add` and `node.edit`.
The edit payload serializes the tagged selectors, not `(:absent)` alone:

```
:verb :title-patch :category-patch :links-patch :private-patch :version-patch :reason
```

so `keep`, `clear`, set-empty, and set-value have different digests. `:before` follows
those fields in the stored structure event as a complete five-field mapping; it is
engine-derived and excluded from the request digest, but retained for undo. Its fixed
field order is `:title :category :links :private :version`; each stored value uses
`(:absent)` for absent and preserves explicit empty/false values.

`--dry-run` needs to be added to the otherwise global write-flag grammar that already
promises revision-bound plans (2298–2304). It validates but writes neither event nor
dedup disposition; an eventual apply revalidates.

## Admission and state effects

The proposed sole creation owner for a roadmap is `roadmap create`, which admits
the node and mandatory view data atomically. `node add` therefore admits only
`:work-set`, `:epic`, `:feature`, and `:task`; `--type roadmap` refuses at exit 2
naming `roadmap create`. This is an explicit parent grammar/type-admission revision
for friend review, not a second alias or a compatibility path. `:event` and
`:lease` remain non-addable. Metadata edits may still target existing roadmaps.
Existing unique-id, hierarchy, cycle, acceptance and candidate validation rules stand.
A direct add under a roadmap remains exit 2 naming `axis --add` (923–36).

`--repo` is required only with `--under-root open`, where the kind must be
`:work-set`; it is forbidden elsewhere. It is a canonical nonempty `<owner>/<name>`
identity, unique in the repository index. Repository identity is immutable: neither
edit nor these add forms permit changing it. This preserves stable ownership, query
selection and inherited source context. `node edit` can change title, category, links
and privacy on any containment kind; version set/clear is task-only (791–98).
The mandatory version keep patch is legal on other kinds because it changes nothing. It cannot change
id, type, containment, dependencies, acceptance, required, responsibility, repo,
source, state or roadmap data.

Both operations apply the ordinary fence/owner and `--expect` checks before durable
admission. Add writes its structure event and the existing paired `:discovery` scope
event where membership changes. Edit writes its structure event only. Add updates id,
containment, repository and category indexes; its existing scope event changes the
parent/roadmap revision and required counters. Edit updates the category index only
when needed; it invalidates affected node/ancestor and privacy-filtered render
projections, but moves no containment, required set, scope revision, or open-item
counter. The current privacy floor remains: a private node and descendants are omitted
from render, with only `private=<n>` exposed (791–93).

Link values are proposed as nonempty UTF-8 reference text, bounded by the session's
existing string/frame limits, with NUL and ASCII control characters refused. They
need not be HTTP URLs; preserve order and bytes without fetching, executing,
normalizing or granting access from a link. Links are not dependency edges. This
text-validation rule requires friend disposition with the other field contracts.

All validation is all-or-none: bad parent/type/repository/criterion, a required leaf
without required acceptance, stale expectation, type-invalid version, malformed patch,
selector collision, or candidate finding writes nothing. The proposed link-text rule is checked at the same candidate gate, and failures
identify the field/index without printing private values.

## Durable idempotency, output and undo

An edit whose selected patches already equal the stored values is still an **accepted
structure event**. It has a real event id, request id, payload digest and `:before`; it
increments the ordinary journal/event revision but changes no node value, scope event,
counter or index. Therefore a lost reply retries through normal request-id lookup before
current-state validation, returns the original `NODE OK`, and after journal retention
the retained dedup entry refuses reapplication as `already applied`. No `id=-` outcome
or unrecorded no-op exists. `changed=0` names this case; otherwise `changed` is the
number of selected fields whose values differ. Add cannot be a no-op because its id must
be new.

Success uses the existing generic fields plus `change=add|edit changed=<n>`:
`NODE OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|->
change=<add|edit> changed=<n> emitted=<bytes>`. Refusals retain the established
NODE/stale/request forms and never print old private values.

Add `node edit` to the undo table. Its compensating typed edit restores `:before` only
if the current metadata still equals the original postimage and the node remains open;
otherwise undo-plan/apply refuses conflict. A no-effect edit is likewise historical and
its undo may append a no-effect compensator under that check. It changes no scope or
counter. Add retains the current node-add removal undo rules.

## Required acceptance witnesses

- `metadata-patches-preserve-intent`: keep, clear, set-empty, set-false and set-value
  round-trip and digest distinctly where the parent distinguishes them; omitted
  and null use the parent's absent rule. All-keep, malformed tags and wrong types
  refuse without an event or counter change.
- `metadata-create-has-one-owner`: generic add of roadmap refuses with the owning
  command; roadmap create produces exactly one node and one coherent view in one
  accepted envelope, never a half-created node or duplicate alias.
- `metadata-repository-and-version`: root ownership must be explicit and unique;
  non-root repo selectors and non-task version edits refuse; task version keep
  preserves the source value without changing identity or ownership.
- `metadata-edit-is-atomic-and-replayable`: bad one-of-five patch writes nothing;
  accepted mixed edits update only declared fields/indexes; identical-request
  retry returns its disposition without another event, changed payload refuses.
- `metadata-undo-preserves-later-work`: compensate only against matching postimage;
  intervening edit refuses rather than overwriting; original events remain.
- `metadata-privacy-and-output`: privacy invalidates affected public projections;
  errors name field/index without revealing private values; links are never fetched.

The no-effect-event and global `--dry-run` rules above remain joint parent
integration decisions. Their witnesses must be adopted with one shared convention,
not implemented independently in each verb draft.

## Friend-review decisions

Approve or revise: explicit `--under-root open` and its typed parent representation; the complete extended add
field order and revised-schema refusal/migration boundary; tagged edit patches and their
JSON object spelling; link grammar; the global `--dry-run` spelling; and the stated
journaled no-effect event/output rule. These are contract proposals, not implementation
claims.
