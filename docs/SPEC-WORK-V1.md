# nova-work v1: the working tree

Draft for review. This branch does not describe a shipped command. This page lands
with the first implemented verb; subsequent sections land with their verbs. The
complete v1 design remains reviewable here until then. It is not linked from the
current CLI, README or command catalogue.

## 1. Workflows the tree must carry

The inventory was read on 2026-09-28. These are workflows to preserve, not a claim
that every source package is selected by today's build. No historical command is
revived merely because its source supplied a requirement.

| Current workflow and evidence | Required v1 behavior |
| --- | --- |
| `internal/gh/api.go`: create an issue, read title/body, comment, read comment body, close with a reason, enumerate issues | Create, show, edit, comment, close and reopen entries; retain an explicit reason and source identity. |
| `internal/nsprint/file/file.go`, `Issuer.Post`: body-file input followed by exact body readback | Preserve multiline prose literally; a failed or mismatched readback records an uncertain operation and its remote identity. |
| `internal/merge/queuegh.go`, `IssueFor`: open issue search by title; `internal/merge/host.go`: paginated comments | Search by text and state; enumerate complete comment threads; a read never creates work. |
| `.github/scripts/revert-on-red.sh`: comment on a PR through the issue-comments endpoint | Distinguish issue and PR identities. Retain a PR reference without importing a PR as an issue. PR mutation is outside this v1. |
| rowan-tools `prompts/nova-sweep.md` and `prompts/estate-check.md` | Enumerate every open entry across selected repositories, triage each, identify aging entries, close superseded work with evidence. A default display limit must not masquerade as a complete inventory. |
| rowan-tools `prompts/ideas.md` | Create an internal idea, attach author labels and receipt links, search existing ideas before filing another. |
| rowan-tools `cmd/rowan-github/{voice,pair,loud}.go` | Create/edit/comment/close/reopen with deliberate prose input; pair an explanatory comment with closure and report either partial result. |
| rowan-tools `cmd/rowan-pulse/issues.go` | Export an open count with measurement time, repository scope and source query. Incomplete or stale evidence is unknown, never zero. |
| The `needs-glenn` labeled queue in the work repository, and the ideas repository | Label filters, owner/assignee filters, state, title/body text, chronological ordering and references. Preserve labels exactly; importing work does not grant authority to perform its requested action. |
| Rowan's **fleet-runbook.md** (read-only inventory input) | Keep machine-operating instructions separate from work records. Preserve links from work to runbooks, execution receipts and fixes; importing an entry executes none of them. |
| Existing coordination messages: stable message IDs, replies, paths and dispositions | Preserve issue-to-issue, issue-to-PR and message references as typed data. A reference is not a send operation. |

The rowan-tools inventory used `cmd/`, `prompts/`, `fleet/` and `doctrine/` at
`42980489fd1a31dfe740607d9e5df0dfe2355e57`. The two queue reads confirmed the
filtering workflows; private issue titles and bodies are not copied into this
spec. Label/unlabel, assign/unassign and milestone set/clear are also explicit v1
requirements even where a wrapper currently forwards them rather than owning a
separate implementation.

## 2. Source of truth and scope

One restricted Lisp data tree, one file in `mas-bandwidth/work`, is the working
store. Each invocation supplies `--tree <file>`; no home-directory or repository
path is guessed. The tree is authoritative for internal work. New internal work
is created in the tree and is never implicitly filed on GitHub.

GitHub retains externally filed issues for the filer's visibility. Import runs
repeatedly to discover new issues and changes. An external issue stays open after
import. When its entry is fixed, an explicit mirror operation posts a comment
naming the fix and closes that same issue. A migration closure of an internal
issue is distinct from completion of its tree entry.

The repository scope is explicit, with a seeded fixture repository first and all
selected repositories supported afterward. Selecting a repository is not
permission to publish its contents into a store with a wider audience. A store
must retain the source's access constraints. No command creates a repository,
changes its access, or copies credentials into the tree.

V1 handles issues and their related data. The recursive representation is also
usable for future PRs and reviews, but neither PR execution nor the future card
integration ships as part of this contract.

## 3. Tree shape and identities

The root has `schema`, `repos`, `receipts` and `policy` objects. Under `repos`, an
owner contains repositories, each repository contains `issues`, and each issue
is a child keyed by its identity. A reader can traverse root to issue and issue
to repository to owner to root. Parents are derived from the structural path,
not duplicated as mutable pointers.

A GitHub issue's stable path is `repos/<owner>/<repo>/issues/<number>`. Store its
forge origin, repository ID, issue ID, node ID, number and returned HTML URL as
attributes, not inferred substitutes for each other. The URL resolves to the
path and the path resolves to the same source identity. Repository renames and
issue transfers retain the original stable path plus explicit aliases to current
locations; a collision refuses instead of silently moving or merging entries.

An internal entry uses `repos/<owner>/<repo>/issues/local-<uuid>` so allocating it
cannot collide with a GitHub issue number. It has no invented GitHub URL or issue
number. IDs are allocated once under the tree writer and remain stable on edit.
Paths are structural addresses inside the data, never paths to execute or write
outside the supplied tree file.

Each entry has:

- `origin`: internal or external, classification evidence and policy revision;
- `identity`: stable path, source identity if any, and aliases;
- `working`: title, body, labels, assignees, milestone, comments, state, reason,
  fix reference and typed issue/PR/message references;
- `captures`: immutable, versioned source captures and their completeness manifests;
- `mirror`: last verified source baseline, pending operations, observed differences
  and status (`none`, `clean`, `pending`, `conflict` or `uncertain`);
- `card_ref`: null in v1, reserved for a later card state reference.

A later card uses the stable entry path for identity and dependency references;
a GitHub number is an attribute. V1 validates and preserves this reserved field
but neither launches a card nor interprets its lifecycle.

A minimal empty store uses the same generic form as every nested value:

```lisp
(object
  ("schema" (string "nova-work/1"))
  ("repos" (object))
  ("receipts" (array))
  ("policy" (object)))
```

For a populated tree, `repos` contains an owner object, then a repository object,
then its `issues` object. The number or `local-<uuid>` is a string key. The entry
envelope above is an object whose values follow the same rule below. Source JSON
keys live inside `captures`, so an upstream `mirror` key cannot collide with the
entry's own mirror state.

### The recursive JSON rule

Use `internal/worklang.Read`, the data-only reader specified in
[SPEC-WORKLANG.md](SPEC-WORKLANG.md), with a new tree schema. Do not feed this tree
through the existing work-set schema and pretend the schemas are interchangeable.
Every JSON value has exactly one reversible form:

| JSON value | Lisp form |
| --- | --- |
| object | `(object ("key" <value>) ...)` |
| array | `(array <value> ...)` |
| string | `(string "<reader-escaped contents>")` |
| number | `(number "<original JSON numeric lexeme>")` |
| boolean | `(boolean true)` or `(boolean false)` |
| null | `(null)` |

Object keys are strings, including unknown keys. Arrays retain order. Numbers
retain arbitrary precision, exponent spelling and negative zero through their
validated lexemes; they never pass through a float. Missing, null, empty string,
empty array and empty object stay distinct. Object key order is preserved;
duplicate keys are refused because field lookup otherwise becomes ambiguous.
Malformed JSON and invalid Unicode refuse with the source endpoint and offset.

The Lisp string writer follows the existing reader's byte-escape rule: a
backslash quotes the next byte. It does not substitute JSON's backslash-n for a
newline. Quotes, backslashes, literal newlines and non-ASCII text have round-trip
fixtures. No eval, reader macros, includes, code loading or shell interpolation.

The full tree has explicit byte, depth and node bounds supplied to the reader;
the work-set defaults are too small for a repository import. Required limits are
`--max-bytes`, `--max-depth` and `--max-nodes`, positive and recorded in import
receipts. Exceeding a bound refuses atomically; it never drops a subtree. The
seeded fixture measures suitable adopter limits before any are standardized.

## 4. Capture completeness and origin

Capture the raw JSON-shaped issue, title/body/state/reason, labels, assignees,
milestone, timestamps, comment bodies and authors, issue/comment reactions,
timeline/events and edit records exposed by the selected API. Preserve every
returned field recursively, including unknown fields and nested user records.
A projection containing only today's recognized issue fields is insufficient.

The importer pins and records its API version and media type. It follows every
page in each selected collection and records endpoint, page chain, item IDs,
counts, capture start/end, response hash and completeness. A collection manifest
names every required collection as complete, unavailable with reason, or failed;
not exposed is distinct from empty. Inaccessible historical edits cannot be
invented or described as captured. Attachment URLs and metadata are retained;
external attachment bytes are not fetched by following arbitrary body links.

Each response also retains its exact raw body, base64-encoded inside the tree,
with a SHA-256 hash. The generic parsed form and raw bytes must decode to the
same JSON value. This allows both field-level inspection and byte-exact source
export, including original whitespace and escape spelling. Authorization headers
and credentials are never captured. No auxiliary file is required to reconstruct
the authoritative tree, although explicit export files may be produced.

The issues endpoint also returns PRs; its `pull_request` discriminator excludes
those from the issue collection. Raw Markdown bodies are selected explicitly.
[GitHub issues API](https://docs.github.com/en/rest/issues/issues).

Origin uses an explicit, versioned mapping of source author IDs to internal
actors, with an audited per-entry override. A matching organization name or an
assignee is not evidence of origin. An unknown author defaults to external, so
it cannot accidentally qualify for migration closure. Later imports retain the
recorded classification unless an explicit override changes it.

## 5. Verb contract

This is the proposed v1 grammar, not a help page for the released binary. Every
verb takes `--tree <file>` and the reader bounds. Every mutation also takes
`--op <unique-id>` and `--expect <tree-sha256>`. Network operations take an explicit
repository/source scope and bounded timeout. Help reads no tree or network.

| Verb | Effect |
| --- | --- |
| `create --repo <owner/name> --title <text> --body-file <file>` | Create one internal entry and return its stable path. |
| `list --repo <owner/name> [--state open\|closed\|all] [--label <name>] [--assignee <id>] [--milestone <id>] [--max <n>]` | Read working entries; print total/matched/shown/truncated. `--max 0` means all. Multiple labels are an intersection. |
| `find --text <text> [same filters]` | Literal Unicode text search across title/body/comments; no implicit regex or forge search language. |
| `show <path-or-url> [--parent\|--children]` | Read the entry or traverse the tree; a URL resolves through the stored identity map. |
| `edit <path> [--title <text>] [--body-file <file>]` | Change working content; preserve original captures. |
| `comment <path> --body-file <file>` | Append a local authored comment with one stable comment ID; source comments remain attributed to their original authors. |
| `label <path> --add <name>\|--remove <name>` | Change the working label set. |
| `assign <path> --add <id>\|--remove <id>` | Change the working assignee set. |
| `milestone <path> --set <id>\|--clear` | Set or clear the working milestone. |
| `link <path> --kind issue\|pr\|message --target <path-or-url>` | Add a typed reference, retaining the source spelling. |
| `close <path> --reason fixed\|superseded\|abandoned [--fix <reference>] [--comment-file <file>]` | Close working state; fixed requires a fix reference. For an external issue, enqueue the explanatory comment and closure for mirror push. |
| `reopen <path> [--comment-file <file>]` | Reopen working state, with an explicit mirror intent for a source-linked entry. |
| `import --repo <owner/name> --dry-run\|--non-destructive\|--destructive [--origin-policy <file>]` | Plan, import only, or import and request the guarded internal migration closure described below. Exactly one mode is required. |
| `close --after-import <receipt-id>` | Execute only the proven internal migration closures named by that receipt; never marks their tree entries fixed. |
| `export <path-or-scope> --capture <id> --out <dir>` | Write the retained capture's original JSON response bodies and manifest; no GitHub write. |
| `export <path-or-scope> --working --out <file>` | Write the current working JSON view and a field capability receipt. |
| `push <path-or-scope> [--restore-import <receipt-id>]` | Apply explicit mirror intents to existing issues; restore-import reopens only issues closed by that import, in place. Never creates internal GitHub issues. |
| `verify <path-or-scope> --capture <id>\|--mirror` | Check exact capture round-trip, or compare working mirrorable fields with a fresh GitHub capture; print all differences by path. |

Read verbs are offline, except explicit `verify --mirror`. Sorting is stable by
requested time and then stable path. Empty matches are successful with count zero;
an incomplete input is a refusal, not an empty match. A machine-readable query
result includes tree hash, scope, query and measurement time, enough to feed an
open-count ledger without a new network reader.

Body files preserve bytes after valid UTF-8 decoding, without trimming whitespace.
A comment paired with closure is two remote operations; the receipt records each
result. No reply or public comment is sent merely by reading/importing a thread.

## 6. One writer, durable mutations and receipts

All mutating verbs use one writer implementation. It acquires an OS-backed lock
on the supplied tree's stable lock file, validates the expected content hash,
checks the full candidate tree, writes and syncs an owned sibling temporary file,
atomically renames it, and syncs the containing directory. Lock inode identity
is stable; a process does not unlink another writer's lock. Readers see the old
or new complete tree. No network call is performed while holding the file lock.

The transaction contains both changed data and a receipt. Receipt fields include
operation ID, actor, verb, scope/entry IDs, request hash, before/after data hash,
time, source revisions, effect counts, result and actionable next step. Receipt
hashing excludes its own hash slot by a versioned rule. An identical operation ID
and request returns the recorded result; reuse with different arguments refuses.

Git publication is separate from GitHub issue mirroring. The tree file is committed
and pushed through the store's existing Git workflow; a destructive import proof
must name the commit and confirm that the intended remote branch contains it.
V1's `push` means issue mirroring, not an implicit Git commit, merge or force push.
A local snapshot alone is not sufficient evidence to close source issues.

Remote operations have a durable intent before the request, an observed response
and readback afterward. A transport failure after a request was sent is uncertain,
not failed-with-no-effect. Retry first reads the remote state. For append-only
comments, use an explicit operation marker and reconcile the complete comment
collection; if identity remains ambiguous, refuse another POST and name the
uncertain operation. Never claim exactly-once HTTP delivery.

Exit 0 means the requested operation completed and its required verification
passed; 1 means a comparison failed or a recorded conflict/partial effect needs
attention; 2 means the invocation could not run. A receipt always distinguishes
no effect, applied, pending and uncertain. A successful local edit may leave a
clearly reported pending mirror; a `push` never calls pending success.

## 7. Import, second import and migration closure

`--dry-run` may read GitHub and write its explicitly requested report, but changes
neither tree nor source. It prints additions, changes, conflicts, classification,
coverage and proposed closures. It never emits a closure-authorizing receipt.

`--non-destructive` captures and validates all selected issue data, then commits
one complete local tree transaction. Partial capture stays in owned scratch and
cannot replace a complete entry or authorize a closure. An absent object on a
failed page is not a deletion. An explicit source deletion is retained as a
source tombstone and history, never erased from the working tree.

A second import matches stable source IDs, preserving internal IDs and comments.
Use the previous verified baseline B, current local L and new remote R: unchanged
L permits adopting R; unchanged R preserves L; identical L and R converge;
independent fields may merge; conflicting changes to the same scalar/set member
or comment create a conflict containing B/L/R and leave the working value intact.
Collections match by stable item IDs, not positional indexes. Repeated unchanged
imports create no duplicate entries or comments.

`--destructive` means capture plus the same guarded close-after-import pipeline.
It never deletes an issue. It may stop after preparing the import proof because
store publication is a separate step; the receipt reports `awaiting-publication`
and `close --after-import <id>` as the continuation. It cannot report completion
while a requested closure remains pending.

Close-after-import requires all of the following for each selected issue:

1. Internal origin, explicit selection and no unresolved local/remote conflict.
2. Complete capture with exact JSON round-trip proof and a matching source identity.
3. A verified store commit reachable from the intended remote branch, containing
   that capture and proof. Subsequent unrelated tree edits do not invalidate an
   unchanged entry capture, but relevant edits do.
4. A fresh recapture matching the expected source content immediately before the
   state-only closure. If any relevant source field changed, stop and import again.
5. Durable remote intent, PATCH of only the required state/reason, and post-write
   recapture. Preserve and report concurrent changes, including late comments.

An already closed issue is recorded as pre-existing closure; restore-import must
not reopen it. External issues are skipped with an explicit reason and remain
open unless a later fixed-entry operation intentionally closes them.

## 8. Export, mirror capabilities and concurrency

There are two different proofs:

- **Storage proof:** each captured response round-trips through the Lisp tree to
  identical original JSON bytes. Decoded values also match the generic tree;
  fresh pre-close capture has zero relevant content differences. Unknown fields,
  original authors, IDs, timestamps and exposed history remain in the tree.
- **Mirror proof:** compare only the fields and operations the selected API can
  write. The receipt lists mirrorable fields, preserved-only fields and every
  mismatch. Raw source metadata is never silently discarded from the export.

A push can change supported issue fields and add the authenticated actor's new
comments. It cannot recreate another person's authorship, server IDs or original
creation timestamps. Original comments and reactions are retained on the same
issue. Restore-import reopens that original issue; it does not reconstruct a new
issue/thread or replay old reactions as someone else.
[GitHub issue update](https://docs.github.com/en/rest/issues/issues#update-an-issue),
[comment creation](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment),
[reactions](https://docs.github.com/en/rest/reactions/reactions).

Server-maintained update times and timeline entries can change when closing or
reopening. The mirror receipt names these expected metadata changes separately
from user-content differences. Unknown writable fields are preserved but not
pushed until their API mapping is explicit. Readback verifies labels, assignees
and milestones as well as bodies; a successful HTTP response alone is insufficient.

GitHub documents conditional reads but does not generally support conditional
unsafe writes. Its issue update contract does not provide a compare-and-swap
revision. Therefore a preflight read is **not** an atomic concurrency guard.
[GitHub conditional-request contract](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api#use-conditional-requests).

The implementation must show the read/write race in its model and receipts.
It writes only explicitly selected fields, reads back the affected issue and
child collections, and records conflict/uncertain on divergence. If another
actor changes the same writable field inside that interval, the API cannot prove
that no intermediate update was overwritten. This limitation is an explicit
review decision before destructive migration or general edit mirroring is enabled;
it is not solved by an invented If-Match header or a local writer lock. State-only
closure and in-place reopen minimize the affected fields but have the same race.
An external issue remains discoverable on GitHub even if capture fails.

For an external fixed entry, push its fix comment first and record its returned
ID/readback before closure. If closure fails, the entry remains fixed locally with
mirror pending/conflict and the issue still visible. Resume reconciles that comment
before posting again. Nothing silently rolls the local completion back.

## 9. State machines and proof obligations

Two TLA+ models accompany implementation: import/capture and mirror/sync. Their
bounded instances include two issues (one internal, one external), two pages,
two local writers, repeated requests, interrupted writes, and a remote actor
that can change issue or child content between any two API operations.

| Action | State change |
| --- | --- |
| CapturePage / CaptureFail | Accumulate complete pages or mark a non-publishable partial capture. |
| VerifyCapture / PublishTree | Prove raw/decoded round-trip; atomically install data and receipt. |
| EditLocal / ImportAgain | Update one expected tree revision; three-way merge or record conflict. |
| PublishStore / PrepareClose | Verify durable remote store proof; create an eligible internal intent. |
| RemoteEdit / RemoteComment | Change source state during capture, preflight or write/readback. |
| CloseInternal / FixExternal / ReopenImported | Perform the intended state transition on the same source identity. |
| SendComment / LoseResponse / Reconcile | Model observable response loss and duplicate prevention/refusal. |
| ReadBack / RecordConflict | Establish verified mirror state or preserve an explicit divergence. |

Safety: one entry per stable source identity; no lost unknown fields; no closure
from partial/unverified/unpublished capture; no external migration closure; no
source delete; no source recreation during restore; no mutation without a durable
intent; no successful mirror receipt after a known failed readback; no two writers
commit from the same prior revision; conflicts retain both observed values.

The model must not assert an impossible no-concurrent-overwrite property for
GitHub. Include a negative witness for that race and state its assumption at the
API boundary. Include negative witnesses for closing external issues, accepting
partial pages, stale tree overwrite and duplicate comment after lost response.
Under fair retries, stable source and available storage/API, eligible work reaches
verified or an explicit conflict; without those assumptions there is no promised
completion deadline. Generated/distinct states, config bounds and measured run
results are retained. Each CI or nightly job remains within the two-minute cap;
a timeout is failed evidence, never an accepted proof.

## 10. Seeded proof, rollout and documentation gate

The first implementation PR adds the generic codec and owned fixture tree; the
second adds import/export/verify against a seeded repository. Fixtures include
more than one issue/comment page, Unicode and multiline bodies, quotes and
backslashes, missing/null/empty, large numeric lexemes, unknown nested fields,
labels/assignees/milestone, reaction authors, available/unavailable edit history,
PR rows, internal/external classification and overrides.

A strict fake API proves every refusal and interleaving without network access:
partial pagination, permission/rate failure, source changes between pages,
concurrent same-field changes, wrong readback, second import after local edits,
identity collision, process death before/after rename, uncertain comment POST,
restore of a pre-closed issue, and stale/missing store publication proof.

On the seeded repository, capture -> tree -> exact JSON export has zero byte
and semantic differences. Then show repeated import idempotence, internal-only
migration closure, restore by reopening the same issue ID, an external issue left
open on import, and external fix comment plus closure. Compare mirrorable content
and separately record expected server metadata deltas. No production issue closes
until this evidence and the concurrency limitation have an explicit disposition.

Subsequent PRs add the working verbs and mirroring incrementally, with receipts,
functional tests and per-verb documentation in the same change. All existing
inventory workflows must have an exercised replacement before GitHub ceases to be
their working store. Audit counts use complete enumerations and capture freshness.

This draft is discussed with Rowan before a summary goes to Glenn. Implementation
runs in parallel with the sprint work; it does not claim that the future card
integration exists. No active help, CLI page or catalogue advertises a verb before
that verb is implemented and tested.
