# Savepoints name a complete journal envelope

Status: proposal against [SPEC-WORK draft 28](https://github.com/mas-bandwidth/nova-tools/blob/7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64/docs/SPEC-WORK.md).
No production codec or implementation approval is claimed. This addresses the
existing savepoint/replay boundary, original-reply and atomic-envelope
requirements; it adds no scheduler or new work-state store.

## The ambiguity this resolves

Source lines 307–350 require durable acceptance and an identical original OK on
a same-payload local retry. Lines 342–344 allow multiple events in one request.
Lines 463–469 and 3381–3401 require a savepoint manifest and journal boundary,
but do not identify the exact replay cut. Lines 372–387 define clip boundary
records and journal rotation. A local revision alone does not identify a byte
record or prove that a multi-event request was captured completely.

Keep three different boundaries distinct:

- The retention boundary belongs to the clip snapshot and archive.
- A clip marker names what was clipped and supplies the existing offline-export
  base and published-dedup boundary.
- A savepoint replay cut names the last complete local journal record reflected
  in that resident-state savepoint. It changes neither of the first two.

## Stable journal identity and record identity

A newly initialized logical local journal gets an immutable random 256-bit identity, written
as 64 lowercase hex characters in its durable header. This is an identity, not
an ownership token. The header also binds the source's bench, lock identity and
coordinator-generation records. The journal ID supplements those records; a
copied journal never grants resume authority on another bench or lock identity.
The source's fencing and fresh-journal rules still decide activation.

Within one journal, each accepted mutation envelope or new clip marker has a
strictly increasing positive integer record sequence. An envelope can contain
several events; it still has one sequence and one original reply. Event
revisions and event-rev:id history keys keep their existing meanings.
Sequences are arbitrary precision under the existing serialized size bounds;
they are not machine integers. A rotation starts a new physical segment of the same logical journal ID.
Its segment header anchors the previous segment and the copied clip marker;
the marker retains its sequence, previous-record hash and record hash. The
following record continues that sequence/hash chain. A new physical file is
not a newly initialized logical journal. Rotation creates no second accepted
mutation and does not reset request identity.

A record's SHA-256 binds the canonical restricted-S-expression serialization
of its ordered fields, including its journal ID, sequence, previous record hash
and complete body. The hash itself is outside that preimage. A root record uses
an explicit absent previous hash. This detects mismatches; it is not an
authentication mechanism. The deterministic printer and complete body schemas
must be integrated with the parent codec before an implementation lock.

A mutation record retains, in order:

1. Journal ID, record sequence and previous-record hash.
2. Request ID and the existing canonical requester payload, plus its digest.
3. Local revisions before and after the envelope, and all assigned events in
   their existing semantic order, including generated events.
4. The original successful response payload: request ID, exit, verbatim ordered
   output lines, resulting local revision and the then-reported pushed revision.

The complete record, including payload, assigned events and original reply,
must pass the source reader byte/depth/node bounds before append or ACK.
An indivisible record that cannot be retained refuses before acceptance; it is
never truncated to make a journal or future savepoint fit.

The original reply is constructed and included before the record is synced.
No ACK precedes durable storage of that complete record. The payload digest
still excludes session-assigned fields exactly as the parent defines; neither
the assigned events nor a new retry timestamp changes requester identity.
Recovery replays the retained assigned events, not the mutation verb anew.
This avoids regenerating IDs, timestamps, settlement or external operations.

A clip marker retains the source's clip commit, base commit, clipped revision
and local event boundary, plus its journal record identity. It also binds the
exact committed snapshot and dedup-root identities used by that boundary;
together with the retained events they must cover every retired request ID.
A locally created commit is not evidence of a successful push. Recovery preserves the recorded
publication status and reconciles the remote under the existing clip protocol.

## Savepoint manifest and replay cut

The proposed manifest is one restricted S-expression with fields in this order:

    (:schema "work-savepoint-v1"
     :journal "<journal-id>"
     :local-revision 812
     :replay-cut (:sequence 420 :sha256 "<record-hash>")
     :clip-marker (:sequence 390 :sha256 "<marker-hash>")
     :state <content-reference>
     :local-replies <content-reference>)

Each content reference identifies immutable, hash-checked content inside the
explicit savepoint root; its eventual exact path/hash/size codec is shared with
the parent persistence schema. Neither reference may follow a path outside
that root. A manifest does not contain its own digest. `manifest=<sha>` hashes
its complete canonical bytes externally.

`:state` is the resident-state savepoint image at `:local-revision`, including
its source-required configuration, observations and index-overlay state. It is
not the clip snapshot or retention archive. `:replay-cut` must resolve to one
complete verified record in the same journal whose resulting local revision
matches the image; it can never point between an envelope's events.
`:clip-marker` identifies the latest source-defined clip marker reflected in
that image, so recovery/export do not guess it from wall time or a filename.
For an initial image before any records or clips, each absent reference uses
`(:absent)` and must agree with the initial journal header and image revision.

`:local-replies` retains the local retry dispositions the existing source says
are still answered with their original OK: request ID, canonical payload digest,
accepted envelope sequence/hash and the original response payload. The image
must not omit these simply because their events lie before the replay cut.
Entries before the source's clip/dedup boundary use the existing already-applied
behavior only after the exact committed snapshot/retained-event/dedup coverage
is verified accessible. A marker alone does not prove that coverage. An
unavailable target keeps the original disposition retained or reports a
recovery gap; it never silently deletes the reply or invents a dedup result.
This changes no normal reply-retention window; failed publication or missing
coverage must not erase its only recoverable evidence. Restore verifies each retained disposition against its accepted
record or an already validated immutable disposition object named by the
savepoint. A missing original reply cannot be reconstructed from current state.

The small replay-cut reference does not license a history-wide validation pass.
A newly published savepoint verifies its newly written content and references;
a retained immutable object can reuse a prior verified identity. A later read
checks the bytes it actually uses. The bounded index/cache and required
dependency lookup rules still apply.

## Write and recover

1. Capture one immutable resident-state image and complete record cut at the
   same revision under the single writer. Whether this requires
   generation pinning, copy-on-write or holding a serialization lock must be
   specified and tested before concurrent mutations are allowed. The source
   C/O/W roots do not imply an existing copy-on-write snapshot mechanism.
2. Write and sync the immutable state and reply objects. Validate the candidate
   manifest and their exact identities, then atomically publish and sync the
   manifest under the declared filesystem durability contract. Retain the prior
   verified savepoint through this operation.
3. On restore, verify the selected manifest, journal identity and exact cut.
   Load that image and its local replies, then replay only complete records
   strictly after the cut, once, in sequence. Process clip markers too; replay
   updates state/index overlays and reply dispositions, never dispatches work.
4. Missing or mismatched cut, missing required tail, broken hash/sequence or
   incomplete envelope reports a recovery gap. A torn tail is not automatically
   proof that it was unacknowledged: retain the bytes and distinguish evidence of
   an interrupted append from unexplained corruption. Never silently truncate
   uncertain data or roll back acknowledged work to make restore pass.
5. Expose the recovered image only after whole-state validation. Restore is
   read-only and non-dispatching; promotion still requires the source's fenced
   reconciliation. A local savepoint never becomes a shared backup by loading it.

Rotating or pruning a journal must preserve a reachable verified replay cut and
all tail/disposition data needed by every retained recoverable savepoint, or
publish an explicitly validated replacement first. The newest filename or the
largest event revision alone is never that proof. A rotation may provide a
bounded locator to the same retained record; exact locator/header forms remain
part of the joint codec integration, not an excuse to scan all old journals.

## Acceptance before locking

| Case | Required observation |
| --- | --- |
| Two-event request, savepoint after acceptance | Both events represented once; retry returns byte-identical original lines |
| Savepoint cut proposed between those events | Manifest rejected; no partial image published |
| Crash after durable append, before reply | Complete envelope replayed once; same request/payload recovers original OK |
| Same request ID, changed payload | Existing conflict refusal, even after restore |
| Savepoint before a later clip marker | Tail replay processes the marker and preserves correct export/dedup behavior |
| Rotation after savepoint | Exact cut and required tail/dispositions remain reachable without global scan |
| Corrupt/missing cut or tail | Visible recovery gap; no silent data deletion or dispatch |
| New image write/rename failure | Previous verified savepoint remains recoverable |
| Copy to another bench | Isolated inspection only; no inherited writer authority |
| Missing raw reply or inconsistent image revision | Refusal; no freshly invented successful reply |

These map to existing atomic-mutation, full-round-trip, recovery,
compaction-keeps-the-last-copy and original-request-retry acceptance. Exact
header/frame/content-reference forms, deterministic string serialization,
physical boundary location, durability platform tests and original-reply
integration with the proposed history index still need one integrated reviewed
revision. This proposal resolves the identity and replay semantics; it does not
mark those remaining codecs or any roadmap feature verified.
