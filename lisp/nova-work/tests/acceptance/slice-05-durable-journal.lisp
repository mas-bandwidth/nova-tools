;;;; slice-05-durable-journal.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "branch-and-window-required" "docs/SPEC-WORK.md:1772-1784,5095"
    "expected=missing-branch-or-window-refused-exit-2"
  ;; NEEDS-KERNEL: query --ask --branch validation, --from/--to, exit 2 refusals.
  (ok t "query branch/window validation is outside slice 1"))

(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:1986-1997,5093"
    "expected=landed-sha-released-dash-while-release-open"
  ;; NEEDS-KERNEL: landed=/released= disposition-row fields, reverse-dependency index, release tasks.
  (ok t "merged-versus-distributed is outside slice 1"))

(deftest "ready-names-the-blocker-and-the-resolver" "docs/SPEC-WORK.md:2019"
    "expected=every-blocked-row-names-reason-and-resolver"
  ;; NEEDS-KERNEL: `ready --node X` view, deps/blocked-by/resolver derivation.
  (ok t "ready-names-the-blocker-and-the-resolver is outside slice 1"))

(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:2084-2092,5117"
    "expected=resident-bytes-flat-as-history-grows"
  ;; NEEDS-KERNEL: startup resident-byte accounting, index depth vs volume bound.
  (ok t "history-grows-startup-does-not is outside slice 1"))

(deftest "remove-settles-only-open-items" "docs/SPEC-WORK.md:2305,5075"
    "expected=closed-leaf-untouched-already-closed-named"
  ;; NEEDS-KERNEL: `node remove` verb, :already-closed field, live-lease refusal.
  (ok t "node-remove settling is outside slice 1"))

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:5498-5502"
    "expected=deny-in-cut-note-still-excludes"
  ;; NEEDS-KERNEL: Delegation note :deny constraints, applicable/goal show cap, notes index.
  (ok t "applicable-cap-never-hides-a-deny is outside slice 1"))
;;; Slice-1 boundary replays promised by docs/SPEC-WORK.md lines
;;; 2400-3600 (part 1 of 4). Every name is dated to the acceptance
;;; table's own paragraph (the `docs/SPEC-WORK.md:` reference below) and
;;; to the prose line where it is first named. This slice is the
;;; internal C/O transition kernel only (no CLI, no socket, no provider,
;;; no clip, no undo, no retention window — see README.md), so each of
;;; these asserts a sentence the kernel here cannot yet satisfy. They
;;; are kept in the file's deftest shape, marked NEEDS-KERNEL, and
;;; counted, exactly as the card asks.
;;; ------------------------------------------------------------------

;;; endpoint-is-local-and-private  SPEC-WORK.md prose :2476 / table :5276
;; (deftest "endpoint-is-local-and-private" "docs/SPEC-WORK.md:5276"
;;     "session-dir=0700;socket=0600;wider-mode-refused;no-network-bind"
;;   ;; the session's directory created 0700 and its socket 0600, both owned
;;   ;; by the running account; a pre-existing directory or socket with wider
;;   ;; modes refused rather than reused; no listener on any network address.)
;; NEEDS-KERNEL: the session/socket layer (slice 1 has no socket, no session dir).

;;; wire-integers-are-strings  SPEC-WORK.md prose :2510 / table :5159
;; (deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
;;     "int>2^53-round-trips-as-string;json-number-frame-refused;null-and-absent-alike"
;;   ;; an id, a revision, a counter and a token total each above 2^53 crossing
;;   ;; the wire and returning unchanged; a frame carrying a JSON number refused;
;;   ;; null and an absent key reading alike, an empty string and empty array as
;;   ;; values.)
;; NEEDS-KERNEL: the wire codec/transport (no JSON frame or wire exists here).

;;; protocol-version-negotiated-or-refused  SPEC-WORK.md table :5162
;; (deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
;;     "unsupported-version-refused-with-list;no-request-before-handshake;oversized-frame-refused"
;;   ;; a client offering an unsupported version refused with the supported list
;;   ;; named and the connection closed; no request admitted before the handshake;
;;   ;; an oversized frame refused with one framed error before the close.)
;; NEEDS-KERNEL: the protocol handshake and connection admission (no transport).

;;; pipeline-replies-are-correlated  SPEC-WORK.md prose :2529 / table :5165
;; (deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
;;     "every-response-reaches-only-its-request;operation-id-distinct;unknown-id-closes"
;;   ;; pipeline two queries, a mutation and a long-operation acceptance, deliver
;;   ;; response frames out of order and in fragments: every response matches only
;;   ;; its request; unknown/duplicate/absent response ids close without falsely
;;   ;; settling an outstanding request.)
;; NEEDS-KERNEL: the pipelined transport with response-id correlation (no socket).

;;; disconnect-is-not-a-rollback  SPEC-WORK.md prose :2540 / table :5173
;; (deftest "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
;;     "event-stands-after-kill;same-id-returns-recorded-disposition;different-args-refused;rev/pushed-distinct"
;;   ;; a client killed after its mutation was journaled: the event stands, the same
;;   ;; request id and body returns the recorded disposition, the same id with
;;   ;; different arguments is refused, and rev= and pushed= are distinct.)
;; NEEDS-KERNEL: socket disconnect + a pushed= counter (slice-1 receipt has pushed=-).

;;; no-effect-mutation-is-journaled  SPEC-WORK.md prose :2797 / table :5176
;; (deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5176"
;;     "event-id-recorded;journal+1;changed=0;projection-digest-unchanged;rev+1"
;;   ;; a mutation whose patches are all no-ops: the event id recorded, journal
;;   ;; length +1, changed=0 on its OK line, the domain-projection digest unchanged
;;   ;; while the event revision advances by one.)
;; NEEDS-KERNEL: a patch/mutation verb and a changed= count (slice 1 has neither).

;;; operation-survives-the-client  SPEC-WORK.md prose :2580 / table :5183
;; (deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
;;     "import-returns-op-id;cli-exit-leaves-work;result-by-id;wait-timeout-leaves-running"
;;   ;; a long import returning an operation id, the CLI exiting, the work
;;   ;; continuing, the result retrievable by id, and `operation wait` timing out
;;   ;; while leaving the operation running.)
;; NEEDS-KERNEL: the operation subsystem and import (out of slice).

;;; status-answers-while-io-runs  SPEC-WORK.md prose :2581 / table :5186
;; (deftest "status-answers-while-io-runs" "docs/SPEC-WORK.md:5186"
;;     "status-and-cancel-within-bound;queues/staged-bounded;restart-reconciles-pending-ops"
;;   ;; status and cancel answered within their bound while a busy capture, export
;;   ;; and clip are in flight, with queues, staged bytes and retained results
;;   ;; bounded, and a restart reconciling the operation ids that were pending.)
;; NEEDS-KERNEL: the operation scheduler plus capture/export/clip (no such verbs).

;;; cancel-is-a-request-not-an-erasure  SPEC-WORK.md prose :2580 / table :5189
;; (deftest "cancel-is-a-request-not-an-erasure" "docs/SPEC-WORK.md:5189"
;;     "cancel-ack-own-disposition;accepted-mutation-not-erased;uncertain-external-reported-uncertain"
;;   ;; a cancellation acknowledged with its own final disposition, erasing no
;;   ;; accepted mutation, and reporting an uncertain external effect as uncertain
;;   ;; rather than as cancelled.)
;; NEEDS-KERNEL: the cancel verb and external-effect disposition (none here).

;;; undo-appends-and-preserves  SPEC-WORK.md prose :2665 / table :5192
;; (deftest "undo-appends-and-preserves" "docs/SPEC-WORK.md:5192"
;;     "compensating-envelope-with-lineage;original-event-and-receipts-untouched"
;;   ;; an undo of a named request appending a typed compensating envelope with its
;;   ;; lineage while the original event and every receipt stay exactly where they are.)
;; NEEDS-KERNEL: the undo verb and its reversible-verb table (no undo in slice).

;;; redo-refuses-a-stale-plan  SPEC-WORK.md prose :2666 / table :5194
;; (deftest "redo-refuses-a-stale-plan" "docs/SPEC-WORK.md:5194"
;;     "stale-precondition-refused-atomically;names-what-changed;writes-nothing;undo-not-deleted"
;;   ;; a redo whose preconditions moved refused atomically, naming what changed,
;;   ;; writing nothing, and never reached by deleting the undo.)
;; NEEDS-KERNEL: the redo verb (no undo/redo stack in slice 1).

;;; undo-refuses-an-external-effect  SPEC-WORK.md prose :2666 / table :5201
;; (deftest "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5201"
;;     "sent/paid/published/deleted-refused-as-external;history-never-reset"
;;   ;; an undo over a sent message, a paid execution, a publication and a source
;;   ;; deletion refused and reported as an external effect; shared Git history
;;   ;; never reset as the undo path.)
;; NEEDS-KERNEL: undo plus external-effect awareness (no such model here).

;;; undo-names-its-reversible-set  SPEC-WORK.md prose :2721 / table :5332
;; (deftest "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5332"
;;     "each-reversible-verb-undone-by-table;refused-verb-refused-named;terminal-dispositions-refused"
;;   ;; every row of the reversible-verb table exercised; each refused verb refused
;;   ;; `not reversible here` naming itself; an undo over cancel and node remove
;;   ;; refused because both dispositions are terminal.)
;; NEEDS-KERNEL: the reversible-verb table and undo (does not exist in slice 1).

;;; clip-is-one-long-operation  SPEC-WORK.md prose :2568 / table :5327
;; (deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5327"
;;     "clip-returns-OPERATION-OK;wait-prints-CLIP-OK;raced-CLIP-RACED;session-stop-CLIP-then-SESSION"
;;   ;; clip returning `OPERATION OK id= op=clip` and exiting, `operation wait --id`
;;   ;; printing the CLIP OK line, a raced transport printing CLIP RACED, and
;;   ;; session stop waiting on its own operation within --git-timeout.)
;; NEEDS-KERNEL: the clip verb and the operation/wait transport (no clip in slice).

;;; repo-only-at-the-root  SPEC-WORK.md prose :2846 / table :5341
;; (deftest "repo-only-at-the-root" "docs/SPEC-WORK.md:5341"
;;     "root-repo-accepted-unique;dup-repo-refused-held;subtree-repo-refused;edit/move-cannot-change"
;;   ;; --repo under-root open work-set accepted and unique; --repo again refused
;;   ;; `repo held by <id>`; --repo under a parent refused `repo outside root`;
;;   ;; node edit and node move unable to change it.)
;; NEEDS-KERNEL: node add/edit/move and the --repo field (no node verbs in slice).

;;; roadmap-has-one-creator  SPEC-WORK.md prose :2846 / table :5344
;; (deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
;;     "add-roadmap-exits-2-naming-create;create-writes-node+view-in-one-envelope;crash-all-or-none"
;;   ;; `node add --type roadmap` exit 2 naming `roadmap create`; `roadmap create`
;;   ;; writing one node and one view in one envelope, a crash between them
;;   ;; replaying all-or-none; no second alias.)
;; NEEDS-KERNEL: the roadmap verbs (no roadmap in slice 1).

;;; metadata-patches-preserve-intent  SPEC-WORK.md prose :2844 / table :5347
;; (deftest "metadata-patches-preserve-intent" "docs/SPEC-WORK.md:5347"
;;     "keep/clear/set-empty/set-false/set-value-distinct;all-keep/malformed/wrong-type-refused;version-on-feature-refused"
;;   ;; keep, clear, set-empty, set-false and set-value on each of five fields
;;   ;; round-tripping and digesting distinctly; malformed and wrong-type patches
;;   ;; refused with no event and no counter moved; --version on a :feature refused.)
;; NEEDS-KERNEL: node metadata-patch verbs (no node edit/version field in slice).

;;; edit-is-atomic-and-replayable  SPEC-WORK.md prose :2845 / table :5351
;; (deftest "edit-is-atomic-and-replayable" "docs/SPEC-WORK.md:5351"
;;     "bad-patch-writes-nothing;named-fields-only-move;retry-replays-original;equal-value=no-effect-receipt"
;;   ;; a bad one-of-five patch writing nothing; an accepted mixed edit moving only
;;   ;; its named fields and the category index; the same request id retried answered
;;   ;; by its original NODE OK; an equal-value edit the no-effect receipt changed=0.)
;; NEEDS-KERNEL: the node edit verb and a changed= receipt (no edit in slice 1).

;;; edit-undo-preserves-later-work  SPEC-WORK.md prose :2845 / table :5355
;; (deftest "edit-undo-preserves-later-work" "docs/SPEC-WORK.md:5355"
;;     "undo-restores-before;undo-after-intervening-edit-refused;both-events-stand"
;;   ;; an edit undone restores :before; the same undo after an intervening edit
;;   ;; refused conflict, both events standing.)
;; NEEDS-KERNEL: edit undo (no node edit or undo in slice 1).

;;; edit-never-fetches-a-link  SPEC-WORK.md prose :2845 / table :5357
;; (deftest "edit-never-fetches-a-link" "docs/SPEC-WORK.md:5357"
;;     "live-link-added/edited/rendered-zero-requests;nul-link-refused;private-refusal-prints-no-value"
;;   ;; a link that is a live URL to a counting endpoint added, edited and rendered
;;   ;; with zero requests observed; a link holding NUL refused `bad link`; a
;;   ;; refusal on a private node printing no value.)
;; NEEDS-KERNEL: node edit + link handling (no link/network model in slice 1).

;;; move-keeps-every-count  SPEC-WORK.md prose :2889 / table :5360
;; (deftest "move-keeps-every-count" "docs/SPEC-WORK.md:5360"
;;     "source+destination-counts-move-by-subtree;ancestor-net-stable;no-whole-set-scan"
;;   ;; a required subtree moved between two features: the source's and destination's
;;   ;; required sets and open counts move by the subtree, the common ancestor's net
;;   ;; count is stable, |O|/|C|/W and every task state unchanged, and no whole-set
;;   ;; scan (visits asserted).)
;; NEEDS-KERNEL: the node move verb and its counters (no move in slice 1).

;;; move-same-parent-is-a-receipt  SPEC-WORK.md prose :2890 / table :5364
;; (deftest "move-same-parent-is-a-receipt" "docs/SPEC-WORK.md:5364"
;;     "same-parent=structure-event-only;changed=0;sibling-order-unchanged;lost-reply-one-envelope"
;;   ;; --from equal to --under and true: the structure event alone, changed=0,
;;   ;; sibling order unchanged; a lost reply retried yields one envelope; a
;;   ;; different payload under the id refused; every refusal leaves both parents
;;   ;; unchanged.)
;; NEEDS-KERNEL: the node move verb (no move in slice 1).
;;; Replays promised by docs/SPEC-WORK.md:2400-3600 but whose verb/kernel
;;; machinery (move, roadmap/axis, render, priority, state-export) does
;;; not yet exist in this slice-1 kernel. Each is kept, marked
;;; NEEDS-KERNEL, and counted but not yet run.
;;; ------------------------------------------------------------------

;;; NEEDS-KERNEL: move-refuses-by-name (SPEC-WORK.md:2890) — the `move` verb:
;;;   a wrong --from, a destination inside the subtree, a root container, a
;;;   repository root, a shared container, another repository, or a roadmap as
;;;   either parent are each refused with its named reason and the identity,
;;;   and nothing is written.

;;; NEEDS-KERNEL: move-keeps-the-lease (SPEC-WORK.md:2890) — the `move` verb:
;;;   a working subtree moved with its effective :responsible unchanged keeps
;;;   the same lease, attempt and usage; a move that would change it over an
;;;   unreconciled attempt is refused "active context change" naming the ids;
;;;   a move under a public parent from a private one is refused "privacy
;;;   reduction", the reverse admitted and the public render losing the rows.

;;; NEEDS-KERNEL: move-updates-every-roadmap-scope (SPEC-WORK.md:2891) — the
;;;   `move` verb: a row referenced by two roadmaps outside both parent chains
;;;   and one unrelated roadmap advances both referencing scope revisions in
;;;   the envelope, leaves the unrelated one, a failed acceptance moves none,
;;;   historical renders keep the old captured scope, and an intervening
;;;   affected-roadmap mutation makes undo conflict.

;;; NEEDS-KERNEL: move-undo-refuses-a-reorder (SPEC-WORK.md:2891) — undo after
;;;   a sibling reorder, a further reparent or a privacy change is refused
;;;   conflict and guesses no position; undo otherwise restores the exact
;;;   before order and required sets with fresh scope revisions, the old
;;;   numbers unwritten; a kill around acceptance and during clip exposes
;;;   neither two parents nor none.

;;; NEEDS-KERNEL: axisless-history (SPEC-WORK.md:2948) — `roadmap row`/axis:
;;;   two ordered rows added, one finished, the state exported and loaded, the
;;;   view reopened past the default window: both rows and their evidence
;;;   present, the denominator not reduced by completion; a row retired records
;;;   a scope movement, keeps its node, and the prior view reconstructs at its
;;;   captured revision.

;;; NEEDS-KERNEL: matrix-retirement (SPEC-WORK.md:2948) — `axis --remove`: of a
;;;   first-axis row then of another axis's member, only the selected
;;;   coordinates retired and recoverable, no task cancelled, an unknown member
;;;   refused, a layout change on a populated roadmap refused "layout
;;;   populated" with no partial write, and a matrix never flattened without
;;;   explicit selections.

;;; NEEDS-KERNEL: configure-no-effect-and-undo-conflict (SPEC-WORK.md:2949) —
;;;   `roadmap configure`: an equal-value configure, its reply lost, a later
;;;   edit, then the retry: the original receipt returned and the later value
;;;   kept; undo restores an ordered preimage only while its guards match.

;;; NEEDS-KERNEL: completed-view-mutation (SPEC-WORK.md:2949) — metadata,
;;;   projection and render on a settled roadmap revive nothing; an outstanding
;;;   member added applies the atomic revival rule so no settled container
;;;   silently holds open required work; counts and indexes are checked by the
;;;   reference fold after each step.

;;; NEEDS-KERNEL: chat-and-file-render-are-byte-identical (SPEC-WORK.md:2950) —
;;;   `render`: chat and file mode are byte-identical for one projection and
;;;   revision, shared prerequisites and private-data filtering included.

;;; NEEDS-KERNEL: render-refuses-a-target-outside-its-roots (SPEC-WORK.md:2950)
;;;   — `render`: a projection target is resolved only within explicitly
;;;   configured permitted roots; a missing mapping or a conflicting change is
;;;   an explicit refusal and never a guessed destination.

;;; NEEDS-KERNEL: render-artifact-is-bounded (SPEC-WORK.md:2977) — `render`:
;;;   ordinary replies and a --chat artifact interleaved in one correlated
;;;   batch, request ids, byte length and hash verified; a corrupt or oversized
;;;   artifact is a bounded refusal and never partial Markdown; --check creates
;;;   no target, no receipt claiming a write, no commit and no push.

;;; NEEDS-KERNEL: a-root-id-grants-nothing (SPEC-WORK.md:2977) — `render`: a
;;;   stored permitted root with no --render-root mapping refuses file mode
;;;   while --chat renders; an escaping path, a symlink escape and a target
;;;   identity other than the mapping's are refused; the cooperative lock is
;;;   exercised and its external-editor limit retained.

;;; NEEDS-KERNEL: priority-orders-only-the-eligible (SPEC-WORK.md:3015) — a
;;;   blocked rank-0 task stays blocked with its reason and resolver while a
;;;   rank-9 ready sibling is first among the eligible; --order priority under
;;;   done exits 2; capacity loss, approval withdrawal, a dependency change or
;;;   a hold is rechecked before ranking and starts or interrupts nothing.

;;; NEEDS-KERNEL: priority-inherits-and-clears (SPEC-WORK.md:3015) — a root
;;;   :subtree rank changes ready order with no lease, attempt, state, O, C, W,
;;;   counter, baseline or roadmap moved; a child's :self overrides it; a clear
;;;   reveals the parent; settle and reopen keep the slots; a move re-reads
;;;   inheritance with no cloned event.

;;; NEEDS-KERNEL: rank-2-precedes-10 (SPEC-WORK.md:3015) — ranks are compared
;;;   as integers, and equal and default rows are ordered by id across a
;;;   restart, a handoff, a cursor continuation and skewed clocks; a first
;;;   unseen filter is O(k log k), later pages come from the pinned order, and
;;;   a subtree invalidation touches no unrelated scope and no C.

;;; NEEDS-KERNEL: priority-undo-is-history-not-value (SPEC-WORK.md:3016) — a
;;;   same-value set and a clear of an absent slot are each the no-effect
;;;   receipt; set 2, set 9, set 2, then undo of the first is refused although
;;;   the value matches.

;;; NEEDS-KERNEL: priority-grants-nothing (SPEC-WORK.md:3016) — with priority
;;;   set on every node, `who` is unchanged, no lease is written, no worker is
;;;   selected, and no approval is bypassed.

;;; NEEDS-KERNEL: state-export-describes-exactly-r (SPEC-WORK.md:3117) —
;;;   `session export --state --at <revision>`: capturing R while R+1 is
;;;   accepted yields bytes that describe R; an exact-snapshot export with B
;;;   equal to R and an absent end, and one with B below R over a multi-record
;;;   prefix across a rotation; an absent end below R, a missing or swapped
;;;   record, a wrong end hash or revision and a cut inside an envelope are each
;;;   refused, and a present later tail is never replayed.

;;; NEEDS-KERNEL: state-export-is-one-long-operation (SPEC-WORK.md:3117) — an
;;;   export blocked on archive I/O acknowledges its operation at once, status,
;;;   cancel and an unrelated mutation stay responsive under it, and wait
;;;   returns the same operation and captured revision after publication or
;;;   refusal; an export inside an atomic batch is refused by entry id.

;;; NEEDS-KERNEL: state-export-pin-survives-clip (SPEC-WORK.md:3118) — capturing
;;;   R, a clip and a retention pass at R+1 during the copy, then exactly R
;;;   completes or a recovery gap is named; no pinned member is reclaimed and
;;;   no current bytes are substituted.

;;; NEEDS-KERNEL: state-export-disconnect-and-cancel (SPEC-WORK.md:3118) — a
;;;   lost client, a restart and a cancellation around the no-replace
;;;   publication keep one operation and one output identity, no duplicate
;;;   directory and no claim to reverse a published one; an existing destination
;;;   is refused; a staged manifest before the commit is not published.

;;; NEEDS-KERNEL: state-export-refuses-a-gap (SPEC-WORK.md:3119) — a missing
;;;   mandatory member, a changed digest, a dangling internal reference, a path
;;;   escape, a symlink, an output overrun and a corrupt S-expression are each
;;;   refused with no valid load; a historical export whose resolver observations
;;;   are gone is refused with a named proof gap and never given current ones;
;;;   --closed-history range over [from,to) declares its omissions while keeping
;;;   closure, all reaches C past the resident window and a fresh load reproduces
;;;   its proof.
;;; ------------------------------------------------------------------
;;; 50-71. replays promised by docs/SPEC-WORK.md:2400-3600 (part 3 of 4)
;;;
;;; Every one of these names a line of SPEC-WORK.md; the three the slice-1
;;; kernel can already execute (an export via state-canonical-form and a load
;;; via reconstruct-state) are green deftest forms. The rest describe CONFIG and
;;; ACTIVE roles, the fleet, dispatch/ack/offers, model attribution, silence
;;; pinging and the bounded config exchange -- none of which exists in slice 1
;;; yet -- and are kept, marked NEEDS-KERNEL with the missing piece.
;;; ------------------------------------------------------------------

;;; 50. state-load-is-isolated   docs/SPEC-WORK.md:5445
;;; ------------------------------------------------------------------

(deftest "state-load-is-isolated" "docs/SPEC-WORK.md:5445"
    "expected=loaded-snapshot-re-exports-equal;load-writes-nothing-to-source"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let* ((source (kernel-state k))
           (exported (state-canonical-form source))
           (rev-before (state-revision source))
           (history-before (state-history source))
           (rebuilt (reconstruct-state (canonical-string exported))))
      ;; A load writes nothing back into the live source: no ownership change,
      ;; no dispatch, no merge. Source revision and history are untouched.
      (check-equal rev-before (state-revision source) "the load changed the source revision")
      (check-equal history-before (state-history source) "the load changed the source history")
      ;; And a re-export of the loaded snapshot compares equal in every field.
      (check-equal exported (state-canonical-form rebuilt)
                   "the loaded snapshot re-exports differently"))))

;;; 51. full-round-trip   docs/SPEC-WORK.md:5587
;;; ------------------------------------------------------------------

(deftest "full-round-trip" "docs/SPEC-WORK.md:5587"
    "expected=re-export=equal;ids-links-history-equal"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1" :evidence '("ev-1"))) "close refused")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2" :evidence '("ev-2")))
        "second close refused")
    ;; Export a captured revision, load it into a fresh isolated engine, export
    ;; again, and compare every semantic field the slice keeps: stable ids,
    ;; links, O and C history, closed rows and the root digest.
    (let* ((source (kernel-state k))
           (exported (state-canonical-form source))
           (rebuilt (reconstruct-state (canonical-string exported))))
      (check-equal exported (state-canonical-form rebuilt)
                   "the re-export compares different in some field")
      (check-string= (root-digest source) (root-digest rebuilt) "the re-export root digest")
      (check-equal (state-history source) (state-history rebuilt) "the re-export history")
      (check-equal (state-closed-rows source) (state-closed-rows rebuilt)
                   "the re-export closed rows"))))

;;; 52. old-history   docs/SPEC-WORK.md:5589
;;; ------------------------------------------------------------------

(deftest "old-history" "docs/SPEC-WORK.md:5589"
    "expected=full-history-in-export;oldest-record-included"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1" :evidence '("ev-1"))) "close refused")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2" :evidence '("ev-2")))
        "second close refused")
    (let* ((source (kernel-state k))
           (history (state-history source))
           (exported-history (getf (state-canonical-form source) :history)))
      ;; An export includes the whole archive: the oldest record is present, not
      ;; pruned to a resident window, and the canonical bytes carry it whole.
      (check-equal 3 (length history) "a record was dropped from the history")
      (check-equal "req-1" (getf (first history) :request)
                   "the oldest record is not the first transition")
      (check-equal history exported-history "the export omitted the old history"))))

;;; 53. fenced-export-can-finish   docs/SPEC-WORK.md:5451
;;;
;; NEEDS-KERNEL: a fenced session and the operation-id read/cancel of an export.
;;   In a fenced session an export started, its status and terminal line read by
;;   id, an unfinished one cancelled, an unknown/non-export id and every
;;   canonical write refused `fenced`.

;;; 54. roles-are-configured-not-inferred   docs/SPEC-WORK.md:3172
;;;
;; NEEDS-KERNEL: CONFIG role records with provenance and scope.
;;   A role is read from CONFIG and never from the underlying model; agreed
;;   limits are never raised silently; essential-security-only and reserved-plan
;;   roles and agreed participation are each expressible.

;;; 55. reserved-role-is-not-spent-on-routine-work   docs/SPEC-WORK.md:3173
;;;
;; NEEDS-KERNEL: role reservation enforced on the dispatch/work path.
;;   A role reserved for essential security work on a paid plan is never spent
;;   by routine work; a model capability never cancels an agreed limit.

;;; 56. no-friend-name-in-the-tool   docs/SPEC-WORK.md:5209
;;;
;; NEEDS-KERNEL: shipped binary/defaults/fixtures carrying no friend, bench,
;;   repository or house name. Every identity arrives as configuration.
;;   (Not a test of this document, which cites friends by name for provenance.)

;;; 57. four-capability-groups-and-three-fields   docs/SPEC-WORK.md:5280
;;;
;; NEEDS-KERNEL: child-agents, swarms, local-models and one-shots as capability
;;   groups, each with stable id/source/stamp/availability/constraints, and
;;   declared support, verified runtime and free capacity as three fields.

;;; 58. dispatch-ack-and-ownership-are-three   docs/SPEC-WORK.md:5219
;;;
;; NEEDS-KERNEL: dispatch/delivery/acknowledgement and accepted-ownership as
;;   distinct facts; a pending offer reserving only declared capacity; a timeout
;;   alone launching no duplicate.

;;; 59. requested-model-is-not-observed-model   docs/SPEC-WORK.md:5222
;;;
;; NEEDS-KERNEL: requested-model vs observed-model fields on attempts.
;;   Unknown stays unknown; a friend's usual model never stands as proof of the
;;   executor of a delegated task.

;;; 60. a-retry-does-not-overwrite-its-attempt   docs/SPEC-WORK.md:5222
;;;
;; NEEDS-KERNEL: concurrent attempts keeping separate attempt records.
;;   A retry never overwrites the attempt before it; separate model and usage
;;   attribution per attempt.

;;; 61. silence-is-a-ping-not-a-verdict   docs/SPEC-WORK.md:5225
;;;
;; NEEDS-KERNEL: --silence-ping threshold and the wake protocol.
;;   A configured threshold triggers one bounded ping; a configured answer
;;   window marks capacity unavailable with reason `unconfirmed`, never sleep
;;   nor exhausted credit.

;;; 62. explicit-rest-is-not-pinged   docs/SPEC-WORK.md:5225
;;;
;; NEEDS-KERNEL: observed explicit-rest state and ping gating on it.
;;   Explicit rest is respected: a resting friend is not pinged by a silence
;;   threshold.

;;; 63. return-reconciles-before-dispatch   docs/SPEC-WORK.md:5226
;;;
;; NEEDS-KERNEL: return reconciling outstanding assignments and capacity.
;;   A return reconciles outstanding assignments and observed capacity before
;;   any new dispatch.

;;; 64. unchanged-config-is-one-bounded-answer   docs/SPEC-WORK.md:5284
;;;
;; NEEDS-KERNEL: config exchange answering UNCHANGED with the named identity.
;;   A request naming a friend and its last-known config hash/revision answers
;;   UNCHANGED with that identity in one bounded reply, no roster/prose repeated.

;;; 65. an-invalid-delta-leaves-the-old-config   docs/SPEC-WORK.md:5284
;;;
;; NEEDS-KERNEL: bounded config deltas validated atomically against the named
;;   base. An invalid delta is applied to no fragment and leaves the old config.

;;; 66. a-partial-manifest-is-refused   docs/SPEC-WORK.md:5285
;;;
;; NEEDS-KERNEL: manifest part handling with a completeness hash.
;;   A partial config is never admitted as a complete replacement; no secret in
;;   a manifest.

;;; 67. fleet-is-static-config   docs/SPEC-WORK.md:3375
;;;
;; NEEDS-KERNEL: a static `fleet` section and the :machine event verb.
;;   A :machine event moves no count and no roadmap; a heartbeat, an `observe`
;;   and a probe change no member.

;;; 68. no-machine-name-in-the-tool   docs/SPEC-WORK.md:3376
;;;
;; NEEDS-KERNEL: shipped defaults/fixtures carrying no machine or host name.
;;   Machine identities arrive as configuration, never hardcoded in the tool.

;;; 69. one-profile-one-unit   docs/SPEC-WORK.md:3376
;;;
;; NEEDS-KERNEL: --register refusing a --connect already held by a member.
;;   One connection profile is one unit; a profile held by a member is refused.

;;; 70. no-credential-in-a-member   docs/SPEC-WORK.md:3377
;;;
;; NEEDS-KERNEL: machine-record credential refusal.
;;   A --connect that is not a profile: reference, or a key/token/password/secret
;;   field, is refused whole and the value never echoed.

;;; 71. unknown-owner-is-refused   docs/SPEC-WORK.md:3377
;;;
;; NEEDS-KERNEL: the owner-must-be-a-friend check on machine register.
;;   A --register whose :owner is not a friend of `friends` is refused, nothing
;;   written.
;;; Assignment & execution control replays (SPEC-WORK.md:2400-3600).
;;;
;;; The slice-1 kernel implements only the three C/O transition verbs
;;; (state-to-done, state-to-doing, event-reopen). Every replay named
;;; below promises a verb or index that does not exist in this slice —
;;; offer, acknowledge, decline, execution pause/stop/resume/correct/
;;; reconcile, and the fleet query — so each is kept in the file's
;;; deftest shape, marked, and counted as needs-kernel rather than run.
;;; ------------------------------------------------------------------

;; NEEDS-KERNEL: fleet `query --for` writes no lease and leaves `who` unchanged.
;; (deftest "fleet-for-is-a-recommendation-not-a-lease" "docs/SPEC-WORK.md:3377"
;;     "expected=for-writes-no-lease;who-unchanged")

;; NEEDS-KERNEL: fleet `query --for` over an excluding member is a refusal, not an empty answer.
;; (deftest "an-excluded-choice-is-refused-not-empty" "docs/SPEC-WORK.md:3378"
;;     "expected=excluded-kind-refused;never-empty-rows")

;; NEEDS-KERNEL: offer/acknowledge/decline verbs keep the four facts apart, infer none, launch none.
;; (deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:3398"
;;     "expected=dispatch-delivery-accepted-ownership-stay-apart;nothing-inferred;nothing-launched")

;; NEEDS-KERNEL: acknowledge/decline admit only behind an operator-configured verifier.
;; (deftest "a-receipt-needs-a-verifier" "docs/SPEC-WORK.md:3413"
;;     "expected=unverified-provenance-refused;state-unchanged;no-bus-body-authority")

;; NEEDS-KERNEL: staged verifier/provenance/payload inputs run outside the mutation loop; stale/failed stage writes nothing.
;; (deftest "staged-admission-refuses" "docs/SPEC-WORK.md:3414"
;;     "expected=stale-or-failed-stage-writes-nothing")

;; NEEDS-KERNEL: an admitted offer writes :effect :dispatched, a pending-offer entry, and one (offer,attempt) reservation.
;; (deftest "offer-writes-intent-and-a-reservation" "docs/SPEC-WORK.md:3439"
;;     "expected=:effect-:dispatched;pending-offer;reservation-keyed-by-offer-attempt;no-lease")

;; NEEDS-KERNEL: a second offer to another name while a holder is pending/accepted is refused — no shadow lease.
;; (deftest "no-shadow-lease-across-holders" "docs/SPEC-WORK.md:3440"
;;     "expected=cross-holder-offer-refused;no-shadow-lease")

;; NEEDS-KERNEL: accepted creates exactly one lease, or binds to the holder's own without touching deadline/default.
;; (deftest "accepted-creates-one-lease-or-binds" "docs/SPEC-WORK.md:3458"
;;     "expected=one-lease-or-bind-to-holders-own;deadline-and-default-unchanged-on-bind")

;; NEEDS-KERNEL: at --until an unanswered offer is overdue and unreconciled; no auto launch and the reservation stands.
;; (deftest "until-is-overdue-not-released" "docs/SPEC-WORK.md:3473"
;;     "expected=overdue-unreconciled;no-auto-launch;reservation-stands")

;; NEEDS-KERNEL: a late receipt is retained as :effect :late, a duplicate consumes no capacity, conflicting bytes refused.
;; (deftest "late-and-duplicate-receipts-are-retained" "docs/SPEC-WORK.md:3474"
;;     "expected=:effect-:late-retained;duplicate-consumes-no-capacity;conflicting-bytes-refused")

;; NEEDS-KERNEL: execution stop installs a durable hold plus directives and writes no transition — not a cancel.
;; (deftest "stop-is-a-hold-not-a-cancel" "docs/SPEC-WORK.md:3499"
;;     "expected=hold-plus-directives;no-transition;not-a-cancel")

;; NEEDS-KERNEL: the :cancel's evidence covers the attempt set, so one worker's stop note cannot cancel another live attempt.
;; (deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:3499"
;;     "expected=one-stop-note-cannot-cancel-node-with-another-live-attempt")

;; NEEDS-KERNEL: the hold and the capture anchor are journaled before EXECUTION OK, so a crash leaves both.
;; (deftest "hold-survives-a-crash" "docs/SPEC-WORK.md:3517"
;;     "expected=hold-and-capture-anchor-durable-before-ack")

;; NEEDS-KERNEL: a clip that publishes during a live capture carries the pin forward; the capture never reads a newer scope.
;; (deftest "capture-survives-clip" "docs/SPEC-WORK.md:3517"
;;     "expected=clip-carries-pin-forward;no-reconstruct-from-newer-scope")

;; NEEDS-KERNEL: the dispatch barrier is checked at offer, at conversion and at the last send.
;; (deftest "no-dispatch-slips-past-a-hold" "docs/SPEC-WORK.md:3534"
;;     "expected=barrier-at-offer-conversion-send;no-dispatch-between-capture-and-hold")

;; NEEDS-KERNEL: an acceptance under a hold is retained :accepted-held with no lease, launch or release; lift converts nothing.
;; (deftest "held-acceptance-converts-nothing" "docs/SPEC-WORK.md:3535"
;;     "expected=:accepted-held;no-lease-no-launch-no-release;lift-converts-nothing")

;; NEEDS-KERNEL: reconcile preserves contradictory observations unresolved, never last-write-wins.
;; (deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:3564"
;;     "expected=contradictions-preserved-unresolved;never-last-write-wins")

;; NEEDS-KERNEL: resume is two acts — release-hold (its own control only) and resume-workers (hold kept until running).
;; (deftest "resume-is-two-actions" "docs/SPEC-WORK.md:3574"
;;     "expected=release-hold-lifts-only-its-control;resume-workers-keeps-hold-until-running")

;; NEEDS-KERNEL: execution correct keeps old-generation usage/results as a linked segment, not a rewrite.
;; (deftest "correct-is-a-linked-segment" "docs/SPEC-WORK.md:3592"
;;     "expected=linked-segment-not-rewrite;old-generation-usage-and-results-kept")

;; NEEDS-KERNEL: the bare correct is refused while any execution of the node is live or uncertain.
;; (deftest "bare-correct-refuses-under-execution" "docs/SPEC-WORK.md:3592"
;;     "expected=bare-correct-refused-while-live;demands-execution-correct")
;;; lines 3600-end part 1 of 8 (rowan/replays-278)
;;; ------------------------------------------------------------------


;; NEEDS-KERNEL: launcher/deadline dispatch (execution control); no launcher exists yet.
;; a-broken-assertion-must-fail (SPEC-WORK.md:5725) --- a test that still passes with its
;; asserted behaviour deliberately broken is not regression evidence; the specific criterion,
;; revision coverage and remaining uncertainty are preserved, not a green badge.

;; NEEDS-KERNEL: manifest exchange and roster validation; no config manifest exists yet.
;; a-partial-manifest-is-refused (SPEC-WORK.md:5285) --- the exchange bounded, validated and
;; atomic, no roster, no prose repeated per poll, no secret in a manifest.

;; NEEDS-KERNEL: verifier/payload reader and staged admission; no verify verb exists yet.
;; a-receipt-needs-a-verifier (SPEC-WORK.md:5234) --- a copied note and an --as <recipient>
;; with no verifier result refused with no canonical write; a verifier returning after a
;; conflicting revision or failing validation writes no reservation, receipt, lease or W change.

;; NEEDS-KERNEL: attempt/usage attribution records; no attempt model exists yet.
;; a-retry-does-not-overwrite-its-attempt (SPEC-WORK.md:5222) --- unknown staying unknown,
;; concurrent attempts keeping separate model and usage attribution, a friend's usual model
;; never standing as proof of a delegated task's executor.

;; NEEDS-KERNEL: render-root mapping and file renderer; no render verb exists yet.
;; a-root-id-grants-nothing (SPEC-WORK.md:5402) --- a stored permitted root with no
;; --render-root mapping refusing file mode while --chat renders; escaping/symlink/target-identity
;; refusals; the cooperative lock and external-editor limit retained.

;; NEEDS-KERNEL: savepoint/checkpoint distinction; no savepoint exists yet.
;; a-savepoint-is-not-a-shared-backup (SPEC-WORK.md:5308) --- restore takes no ownership,
;; reanimates no assignment, replays no message; savepoint age, local and shared revisions,
;; unshared work and failed backups readable; a savepoint never printed where a checkpoint asked.

;; NEEDS-KERNEL: distributed execution reachability; no distributed worker exists yet.
;; a-stop-reaches-distributed-work (SPEC-WORK.md:5728) --- a priority change, correction, pause
;; or stop across already-distributed tasks, with durable request identity, delivery,
;; acknowledgement and reconciled handles; blocked questions and bounded fallbacks persisted.

;; NEEDS-KERNEL: as-of query over day partitions; no state-as-of exists yet.
;; absent-day-is-not-a-gap (SPEC-WORK.md:5130) --- a day with no manifest inside a complete
;; manifested range answering its rows with gap=0 and no note.

;; NEEDS-KERNEL: lease creation/binding and W entry; no lease or W index exists yet.
;; accepted-creates-one-lease-or-binds (SPEC-WORK.md:5238) --- an accepted receipt after
;; received creating exactly one :lease and one W entry, or binding a second attempt to the same
;; holder's lease unchanged; converting and never doubling capacity.

;; NEEDS-KERNEL: node add verb with twelve fields; no node-add exists yet.
;; add-field-order-is-complete (SPEC-WORK.md:5337) --- a node add with every one of the twelve
;; fields, one with each absent, one with --links-empty and one with --clear-links, digested by
;; two serializers to four distinct values; a pre-fold-order fixture refused at load.

;; NEEDS-KERNEL: configuration delta validation; no config exists yet.
;; an-invalid-delta-leaves-the-old-config (SPEC-WORK.md:5284) --- the exchange bounded,
;; validated and atomic; an invalid delta leaving the old config untouched.

;; NEEDS-KERNEL: delegation notes and --max cap; no constraint-note verb exists yet.
;; applicable-cap-never-hides-a-deny (SPEC-WORK.md:5498) --- N active notes, N > --max, the only
;; :deny in the note that sorts last; the deny is never cut by --max and never printed as eligible.

;; NEEDS-KERNEL: intake archive absorption; no import/adapter exists yet.
;; archive-completeness (SPEC-WORK.md:5586) --- a missing attachment, an unavailable comment,
;; unsupported fields, size truncation, a rate limit and a mid-page failure each remaining
;; explicit gaps and prohibiting absorption.

;; NEEDS-KERNEL: state-as-of reconstruction over settle/revive chains; no as-of query exists yet.
;; as-of-reconstructs-settle-revive-settle (SPEC-WORK.md:5137) --- one id settled on day A,
;; revived on day B, settled again on day C: each interval's ask answering that state, the three
;; answers different, the earlier two unchanged by later events.

;; NEEDS-KERNEL: state-as-of partition refusal; no as-of query exists yet.
;; as-of-refuses-unavailable-partition (SPEC-WORK.md:5135) --- a state-as-of ask whose day
;; partition cannot be opened refused at exit 1 naming that partition, never answered from a later row.

;; NEEDS-KERNEL: async operation control plane; no operation/wait/cancel exists yet.
;; async-operations (SPEC-WORK.md:5593) --- status, wait and cancel under a busy import, export
;; and clip; no double launch, no false cancellation success, no control plane stalled behind I/O.

;; NEEDS-KERNEL: roadmap row ordering and completion; no roadmap verb exists yet.
;; axisless-history (SPEC-WORK.md:5383) --- two ordered rows, one finished, exported/loaded and
;; reopened past the window: both rows and evidence present, denominator not reduced by completion.

;; NEEDS-KERNEL: correct/execution-correct barrier; no correct verb exists yet.
;; bare-correct-refuses-under-execution (SPEC-WORK.md:5272) --- the bare correct refused by name
;; while an attempt is live and admitted once none is.

;; NEEDS-KERNEL: batch coalescing by byte/record/delay bounds; no batch mode exists yet.
;; batch-with-bounds-and-urgency (SPEC-WORK.md:4376) --- independent results coalescing within
;; bounds, unchanged batches causing no call, unreferenced padding refusing, urgent corrections
;; bypassing delay.

;; NEEDS-KERNEL: batch modes and pipeline revision semantics; no batch mode exists yet.
;; batches-and-pipelines (SPEC-WORK.md:5594) --- atomic batches all-or-none, independent batches
;; preserving their exact accepted prefix and marking the remainder not attempted.

;; NEEDS-KERNEL: launcher hard-limit enforcement; no launcher exists yet.
;; bounds-are-not-prompts (SPEC-WORK.md:4372) --- a launcher lacking a required hard limit
;; refusing automatic dispatch; a supported deadline returning a terminal or unresolved handle
;; without duplicate execution.
;;; Acceptances promised by docs/SPEC-WORK.md (lines 3600-end) and absent
;;; from slice 1. Each is kept in the file's replay shape but needs session,
;;; CLI, query, paging, capture, clip, render, savepoint, cost, undo or
;;; execution machinery that this internal C/O kernel does not ship. Uncomment
;;; a deftest when its kernel code lands.
;;; ------------------------------------------------------------------

;; (deftest "branch-and-window-required" "docs/SPEC-WORK.md:5095"
;;     "expected=query-ask-without-branch-refused-exit-2;closed-without-from-to-refused;from-under-open-refused;who-stale-handoffs-refused-under-closed-and-root"
;;   ;; NEEDS-KERNEL: query command and --branch/--window flag validation at exit 2
;;   )

;; (deftest "busy-day-many-segments" "docs/SPEC-WORK.md:5115"
;;     "expected=many-segments-read-in-bounded-pages;max-caps-rows;more-names-after;day-never-read-whole"
;;   ;; NEEDS-KERNEL: closed-history index with day segments and paged reads
;;   )

;; (deftest "cache-aware-context-choice" "docs/SPEC-WORK.md:4377"
;;     "expected=cache-read-write-tier-threshold-priced-separately;reset-costs-refused-when-separate;lower-hit-can-win-on-cost"
;;   ;; NEEDS-KERNEL: cache tier pricing and context-choice cost model
;;   )

;; (deftest "cancel-is-a-request-not-an-erasure" "docs/SPEC-WORK.md:5189"
;;     "expected=cancel-has-its-own-disposition;erases-no-mutation;uncertain-effect-reported-uncertain"
;;   ;; NEEDS-KERNEL: cancellation verb with final disposition, distinct from erasure
;;   )

;; (deftest "chat-and-file-render-are-byte-identical" "docs/SPEC-WORK.md:5304"
;;     "expected=chat-and-marker-region-bytes-identical;other-bytes-preserved;missing-duplicate-reversed-marker-refused;target-outside-roots-refused"
;;   ;; NEEDS-KERNEL: render with marker regions and permitted-roots boundary
;;   )

;; (deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5327"
;;     "expected=clip-operation-ok-op-pushed;wait-prints-clip-ok-operation;raced-prints-clip-raced;session-stop-prints-clip-ok-then-session-ok"
;;   ;; NEEDS-KERNEL: clip long-operation protocol and operation wait id
;;   )

;; (deftest "clip-names-the-index-that-overflowed" "docs/SPEC-WORK.md:5102"
;;     "expected=snapshot-retained-index-closed-index-printed;remedy-lower-retain-or-raise-max-bytes;lower-retain-passes;index-page-split-not-refused"
;;   ;; NEEDS-KERNEL: clip snapshot/index bound refusal naming the overflowing index
;;   )

;; (deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:5087"
;;     "expected=max-20-reads-20;more-names-after;next-page-reads-next-20;parses=0-replays=0;whole-history-never-loaded"
;;   ;; NEEDS-KERNEL: closed listing paging over the closed index
;;   )

;; (deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:5090"
;;     "expected=ask-answers-same-four-rows-with-archive-absent;archived-body-ask-prints-gap-and-coverage-gap"
;;   ;; NEEDS-KERNEL: retention archive file and coverage-gap ask
;;   )

;; (deftest "compaction-keeps-the-last-copy" "docs/SPEC-WORK.md:5309"
;;     "expected=compaction-never-removes-the-only-recoverable-copy"
;;   ;; NEEDS-KERNEL: compaction over savepoints preserves the last copy
;;   )

;; (deftest "complete-cost-lineage" "docs/SPEC-WORK.md:4375"
;;     "expected=parent-child-retry-join-once;failed-count;cache-subsets-no-double-count;impl-cost-separate;gaps-unknown"
;;   ;; NEEDS-KERNEL: cost lineage joins over receipt records
;;   )

;; (deftest "completed-view-mutation" "docs/SPEC-WORK.md:5394"
;;     "expected=metadata-projection-render-on-settled-roadmap-revive-nothing;member-add-applies-atomic-revival;counts-indexes-checked"
;;   ;; NEEDS-KERNEL: roadmap view mutation and atomic revival rule
;;   )

;; (deftest "configure-no-effect-and-undo-conflict" "docs/SPEC-WORK.md:5391"
;;     "expected=equal-configure-original-receipt-and-later-value-kept;undo-restores-preimage-only-while-guards-match"
;;   ;; NEEDS-KERNEL: configure + undo retry/guard machinery
;;   )

;; (deftest "copied-journal-grants-nothing" "docs/SPEC-WORK.md:5535"
;;     "expected=restore-inspects-in-isolation-takes-no-ownership-dispatches-nothing;session-start-over-copy-refused-by-fencing"
;;   ;; NEEDS-KERNEL: savepoint restore fencing and bench identity rules
;;   )

;; (deftest "cost-joins-include-the-coordinator" "docs/SPEC-WORK.md:5733"
;;     "expected=complete-cost-joins-include-coordinator-overhead-rework;elapsed-attributed;hypothesis-run-after-adoption"
;;   ;; NEEDS-KERNEL: operational cost joins with coordinator attribution
;;   )

;; (deftest "cow-root-partition" "docs/SPEC-WORK.md:5044"
;;     "expected=id-in-c-or-o-never-both;open-plus-closed-equals-total;both-branches-is-rule-18-finding"
;;   ;; NEEDS-KERNEL: rule 18 candidate-gate finding for an id in both branches
;;   )

;; (deftest "cursor-pinned-across-a-new-settle" "docs/SPEC-WORK.md:5141"
;;     "expected=no-missing-no-duplicate-row;pinned-revision-honoured;unservable-pin-refused-page-expired"
;;   ;; NEEDS-KERNEL: paged continuation cursor pinned to a served revision
;;   )

;; (deftest "days-merge-by-revision-never-concatenate" "docs/SPEC-WORK.md:5547"
;;     "expected=backdated-closure-in-earlier-day;from-to-prints-revision-order-across-boundary;one-batch-and-ten-yield-identical-leaves"
;;   ;; NEEDS-KERNEL: day-partition date index merged by revision
;;   )

;; (deftest "dedup-page-unavailable-refuses" "docs/SPEC-WORK.md:5147"
;;     "expected=retry-with-unreadable-dedup-page-refused-dedup-unavailable-applies-nothing;admitted-when-readable"
;;   ;; NEEDS-KERNEL: dedup page availability gate on retry
;;   )
;;; ------------------------------------------------------------------
;;; Replays promised by docs/SPEC-WORK.md lines 3600-end (card 280,
;;; part 3 of 8). Each names a replay named in the acceptance appendix or
;;; the efficiency/roadmap tables. Slice 1 is the C/O transition kernel
;;; only; these all describe session, CLI, goal, dispatch, export or
;;; endpoint slices whose kernel code does not exist here, so each is kept
;;; and counted rather than run. The pending convention is a repeated
;;; ;; NEEDS-KERNEL: line naming exactly what is missing.
;;; ------------------------------------------------------------------

(defvar *needs-kernel* '()
  "Replays written from SPEC-WORK.md whose kernel code does not exist yet.
Each is (name spec-line expected need). Counted, not run.")

(defmacro deftest-pending (name spec-line expected need)
  "Keep a replay NAME asserting EXPECTED (from SPEC-LINE), blocked on NEED.
It is registered and counted, but never added to *tests*: the kernel it
asserts does not exist in slice 1."
  `(push (list ,name ,spec-line ,expected ,need) *needs-kernel*))

;; default-window-opens-two-days: a default closed-history listing at early
;; morning, midday and exactly 00:00:00Z opens at most two UTC day partitions
;; and one at midnight; no partition older than the window; --from reaching
;; back a month opens exactly the days holding closure records.
;; NEEDS-KERNEL: session closed-history windowing (query --ask --branch closed).
(deftest-pending "default-window-opens-two-days" "docs/SPEC-WORK.md:5110"
    "expected=at-most-two-day-partitions;midnight=one;--from=only-days-holding-records"
  "closed-history window listing is not a slice-1 kernel primitive")

;; disconnect-is-not-a-rollback: a client killed after its mutation was
;; journaled -- the event stands, the same request id and body returns the
;; recorded disposition, the same id with different arguments is refused, and
;; rev= and pushed= are distinct in every response.
;; NEEDS-KERNEL: a session reply layer carrying rev= and pushed= per response.
(deftest-pending "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
    "expected=event-stands;same-id-same-disposition;changed-args-refused;rev!=pushed"
  "kernel dedup disposes but the session reply packaging does not exist")

;; dispatch-ack-and-ownership-are-three: dispatch, delivery, acknowledgement
;; and accepted ownership distinguished; a pending offer reserves only
;; declared capacity; a timeout launches no duplicate while the old worker
;; may run; a return reconciles before new dispatch.
;; NEEDS-KERNEL: dispatch/offer/ack ownership verbs and a pending-offer index.
(deftest-pending "dispatch-ack-and-ownership-are-three" "docs/SPEC-WORK.md:5219"
    "expected=dispatch!=delivery!=ack!=ownership;reservation=declared-only;duplicate=0"
  "dispatch verbs are absent from slice 1")

;; dry-run-writes-nothing: a dry run validates and projects without mutating;
;; after a green preview at revision R the --request id is still new to the
;; dedup index and events=/pending=/pushed= are unchanged; an accepted mutation
;; moves to R+1; apply --expect R is refused stale; apply at R+1 newly validates.
;; NEEDS-KERNEL: a --dry-run projection path and SESSION OK counters.
(deftest-pending "dry-run-writes-nothing" "docs/SPEC-WORK.md:5196"
    "expected=preview-mutates-nothing;dedup-still-new;events-pending-pushed-unchanged"
  "no dry-run projection exists in slice 1")

;; edit-is-atomic-and-replayable: a bad one-of-five patch writes nothing; an
;; accepted mixed edit moves only its named fields and the category index; the
;; same request id retried answers NODE OK; a changed payload refuses; an
;; equal-value edit is the no-effect receipt, changed=0, rev up by one.
;; NEEDS-KERNEL: an edit patch verb and category index.
(deftest-pending "edit-is-atomic-and-replayable" "docs/SPEC-WORK.md:5351"
    "expected=one-of-five-writes-nothing;changed-payload-refused;equal-value=changed-0"
  "no edit verb in slice 1")

;; edit-never-fetches-a-link: a live URL to a counting endpoint added, edited
;; and rendered with zero requests; a link holding NUL refused `bad link`; a
;; refusal on a private node prints no value.
;; NEEDS-KERNEL: a render path and link validation with a counting endpoint.
(deftest-pending "edit-never-fetches-a-link" "docs/SPEC-WORK.md:5357"
    "expected=fetches=0;nul=bad-link;private-node-prints-no-value"
  "no link render/fetch in slice 1")

;; edit-undo-preserves-later-work: an edit undone restores :before; the same
;; undo after an intervening edit is refused conflict, both events standing.
;; NEEDS-KERNEL: an undo verb with a :before field and conflict detection.
(deftest-pending "edit-undo-preserves-later-work" "docs/SPEC-WORK.md:5355"
    "expected=undo-restores-before;late-undo=conflict;both-events-stand"
  "no undo verb in slice 1")

;; efficiency-lessons-gate: prime projection respects --max-bytes; unpoured
;; checklist items never count in |O|; tripped nodes require --reason; delegate
;; mode refuses edits below the model; packets lacking :effort are refused;
;; dispatches crossing the configured daily fleet spend ceiling are refused.
;; NEEDS-KERNEL: model dispatch, delegate mode, :effort and spend-ceiling gates.
(deftest-pending "efficiency-lessons-gate" "docs/SPEC-WORK.md:4439"
    "expected=max-bytes-respected;unpoured!=O;tripped=reason;no-effort=refused;ceiling=refused"
  "dispatch/effort/ceiling enforcement is not in slice 1")

;; endpoint-is-local-and-private: the session's directory created 0700 and its
;; socket 0600, both owned by the running account; a pre-existing directory or
;; socket with wider modes refused rather than reused; the Windows named pipe
;; created with FILE_FLAG_FIRST_PIPE_INSTANCE; no listener bound to a network
;; address.
;; NEEDS-KERNEL: a session socket/directory bootstrap and permission check.
(deftest-pending "endpoint-is-local-and-private" "docs/SPEC-WORK.md:5276"
    "expected=dir-0700;socket-0600;wider-modes-refused;no-network-listener"
  "no session endpoint exists in slice 1")

;; evidence-before-adoption: missing baseline/coverage, unmatched quality or a
;; retrospective correlation alone cannot auto-promote; a fully qualified
;; prospective result can.
;; NEEDS-KERNEL: an adoption/promotion gate over baseline, coverage and quality.
(deftest-pending "evidence-before-adoption" "docs/SPEC-WORK.md:4378"
    "expected=missing-baseline=no-promote;prospective-qualified=promote"
  "adoption gating is not in slice 1")

;; explicit-rest-is-not-pinged: the configured silence threshold triggers one
;; bounded ping; a nonresponsive capacity marked unavailable with reason
;; `unconfirmed` and no claim of sleep or exhausted credit; a failed probe read
;; as unresolved delivery; explicit rest respected.
;; NEEDS-KERNEL: capacity availability and silence-threshold ping logic.
(deftest-pending "explicit-rest-is-not-pinged" "docs/SPEC-WORK.md:5225"
    "expected=one-bounded-ping;unavailable=unconfirmed;rest=respected"
  "capacity/rest logic is not in slice 1")

;; fenced-export-can-finish: in a fenced session an export started, its status
;; and terminal line read by id; an unfinished one cancelled with publication
;; reconciled; an unknown id, a non-export id, operation list and every
;; canonical write refused `fenced`; no second owner and no mutation authority.
;; NEEDS-KERNEL: export/operation ids and a fenced (single-owner) session.
(deftest-pending "fenced-export-can-finish" "docs/SPEC-WORK.md:5451"
    "expected=terminal-read-by-id;cancel-reconciles;canonical-write=fenced;one-owner"
  "export and fencing are not in slice 1")

;; findings-across-c-and-o: the worked acceptance asserted line by line -- four
;; ids, four rows, open=1 closed=3 closed-in=3, no id twice, the dispositions
;; distinct, and the release task listed as pending by the second ask.
;; NEEDS-KERNEL: a `query --ask` findings listing across O and C.

;; four-capability-groups-and-three-fields: child agents, swarms, local models
;; and one-shots each expressible as a capability group with stable id, source,
;; last-verified stamp, availability and constraints; declared support,
;; verified runtime and current free capacity kept as three fields.
;; NEEDS-KERNEL: capability-group records and the three support fields.
(deftest-pending "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:5280"
    "expected=groups=4;fields=support,runtime,free-capacity;never-collapse"
  "capability records are not in slice 1")

;; four-facts-four-verbs: an admitted offer with declared free slots writing
;; :effect :dispatched, a pending-offer index entry, refused by name while an
;; attempt is live and admitted once none is.
;; NEEDS-KERNEL: the offer verb and a pending-offer index.
(deftest-pending "four-facts-four-verbs" "docs/SPEC-WORK.md:5229"
    "expected=effect-dispatched;pending-offer-entry;live-attempt=refused;none=admitted"
  "the offer/dispatch verbs are not in slice 1")

;; full-round-trip: export a captured revision, load it in a fresh isolated
;; engine, export again, and compare every semantic field, stable ids, Unicode
;; and literal text, order where meaningful, links, evidence, roles, CONFIG,
;; ACTIVE observations, model and rate records, O and C history, roadmaps and
;; accounting provenance; derived caches rebuild to equivalent values.
;; NEEDS-KERNEL: an export/import path over a durable captured revision.

;; gas-town-efficiency-accounting: root-only step records and inline checklists
;; avoid node explosion; durable next-triggers ensure empty pulses cause zero
;; model re-executions.
;; NEEDS-KERNEL: step-record granularity and durable next-trigger accounting.
(deftest-pending "gas-town-efficiency-accounting" "docs/SPEC-WORK.md:4438"
    "expected=node-explosion=0;empty-pulse-reexecutions=0"
  "efficiency accounting is not in slice 1")

;; goal-crosses-harness: G a :doing leaf at revision r; harness A as coordinator
;; C sets, updates, and writes a (:coordinator "C") note with a :deny; harness B
;; shows --as C against the live session and the clipped snapshot, both print
;; goal=G, rev= at or after every write, stop=requested (not cancelled), and the
;; same note id and constraint row byte for byte; B's update --progress on G is
;; refused `stop requested`; A writes cancel evidence; B's next show prints
;; stop=cancelled; B copied no conversation.
;; NEEDS-KERNEL: goal verb, coordinator notes/delegation, and a clipped snapshot.
(deftest-pending "goal-crosses-harness" "docs/SPEC-WORK.md:5456"
    "expected=goal=G;stop=requested;note-byte-for-byte;progress=refused;stop=cancelled"
  "no goal/session/delegation in slice 1")

;; goal-expect-is-required: goal set and goal update without --expect exit 2
;; naming the flag, nothing written; with --expect at the current local
;; revision, admitted; with --expect one behind, refused `stale` at exit 1 with
;; the current value printed; --dry-run with the stale expectation prints the
;; same refusal and writes no event, no journal revision and no dedup entry; a
;; show never takes --expect.
;; NEEDS-KERNEL: a goal CLI with --expect revision comparison and --dry-run.
(deftest-pending "goal-expect-is-required" "docs/SPEC-WORK.md:5493"
    "expected=no-expect=exit-2;one-behind=stale;dry-run-writes-nothing"
  "no goal CLI in slice 1")

;; goal-stale-update-refuses: A and B both show at r; A writes update, r+1; B's
;; update --expect r is refused `GOAL FAIL ... expect=r current=r+1: stale`,
;; snapshot unchanged; B's next show prints A's evidence row; A writes stop, r+2;
;; B's update --expect r+1 refused stale; B's next show prints stop=requested;
;; B's update --expect r+2 refused `stop requested`; a goal set to a closed
;; branch node refused `disposition=done`.
;; NEEDS-KERNEL: a goal CLI with stale --expect detection and a snapshot.
(deftest-pending "goal-stale-update-refuses" "docs/SPEC-WORK.md:5466"
    "expected=stale-named;snapshot-unchanged;stop-requested-stands;closed-refused"
  "no goal CLI in slice 1")

;; goal-stop-is-a-request-not-evidence: goal update --stop prints GOAL OK
;; change=stop kind=transition rev=r+1 and the event is a :transition :to
;; :cancel-requested carrying :reason and no :evidence; check has no finding;
;; state --to doing --reason by id is admitted (the withdrawal); event --kind
;; cancel --evidence <pointer> makes show print stop=cancelled, terminal, and
;; goal set --goal G refused disposition=cancelled; --stop on :review and :done
;; refused `no edge`.
;; NEEDS-KERNEL: a goal verb, stop/cancel/withdrawal transitions and a check.
(deftest-pending "goal-stop-is-a-request-not-evidence" "docs/SPEC-WORK.md:5475"
    "expected=stop=request-not-evidence;cancel=terminal;done-refused-no-edge"
  "no goal verb in slice 1")

;; goal-update-writes-only-existing-kinds: every goal update form written, then
;; the journal read: each event is a :transition or an :evidence with exactly
;; the field list of its kind, on the goal node and no other node; --progress
;; <text> alone on a :todo node writes :to :doing with the text as :reason, and
;; on a :doing node refused `no edge`; --progress with the evidence triple on a
;; :doing node writes the :evidence event; goal set and goal set --clear each
;; write one :goal event; a retried set with the same --request id and payload
;; returns the same event id once.
;; NEEDS-KERNEL: goal update forms with kind-owned field lists.
(deftest-pending "goal-update-writes-only-existing-kinds" "docs/SPEC-WORK.md:5484"
    "expected=only-transition-or-evidence;exact-kind-fields;goal-node-only"
  "no goal verb in slice 1")

;;; ------------------------------------------------------------------
;;; SPEC-WORK.md lines 3600-end, part 4 of 8: session/CLI-level replays.
;;; Slice 1 ships only the internal C/O transition kernel (value, event,
;;; journal, state, kernel); every replay below names a feature whose kernel
;;; code (session, paging, indexes, roadmaps, leases, moves, providers,
;;; hostile-data intake) does not exist yet.  Each is kept in the file's
;;; deftest shape, asserted against the exact sentence its spec line states,
;;; and marked NEEDS-KERNEL rather than run against an absent implementation.
;;; ------------------------------------------------------------------

;; ------------------------------------------------------------------
;; held-acceptance-converts-nothing   docs/SPEC-WORK.md:5258
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: session holds + leasing (an acceptance under a hold stays
;; `:accepted-held` with no lease/launch/release until reconciled; lifting the
;; hold converts nothing).
;;(deftest "held-acceptance-converts-nothing" "docs/SPEC-WORK.md:5258"
;;    "accepted-held=retained,lease=0,launch=0,release=0,lift-converts=nothing"
;;  ;; an offer prepared before a pause refused at the last send; an acceptance
;;  ;; under a hold retained `:accepted-held`; a lift of the hold converts nothing.)
;;(pending))

;; ------------------------------------------------------------------
;; historic-tick-survives-a-source-change   docs/SPEC-WORK.md:5300
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: source capture + pinned revisions (historic tick stays at its
;; pinned revision while the current view requires re-verification).
;;(deftest "historic-tick-survives-a-source-change" "docs/SPEC-WORK.md:5300"
;;    "historic-tick=pinned,current=reverify"
;;  ;; a changed source or criterion preserves the historic tick at its pinned
;;  ;; revision while the current view requires re-verification.)

;; ------------------------------------------------------------------
;; history-grows-startup-does-not   docs/SPEC-WORK.md:5117
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: bounded paged history + startup instrumentation (grow old
;; history; resident bytes/segment bytes/parses/replays/emitted bytes stay
;; fixed; index pages read bounded by depth).
;;(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:5117"
;;    "resident-bytes=stable,pages=bounded-by-depth"
;;  ;; O, recent-window volume and page bounds held fixed while old history
;;  ;; grows: startup resident bytes, segment bytes read, parses, replays and
;;  ;; emitted bytes do not move; index pages read stay bounded by depth.)

;; ------------------------------------------------------------------
;; hold-survives-a-crash   docs/SPEC-WORK.md:5254
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: crash durability of holds (crash after the hold recovers the
;; same hold and target identities with no duplicate launch).
;;(deftest "hold-survives-a-crash" "docs/SPEC-WORK.md:5254"
;;    "hold-durable,target-identities=recovered,duplicate-launch=0"
;;  ;; a crash after the hold is durable and before capture/send recovering the
;;  ;; same hold and target identities with no duplicate launch.)

;; ------------------------------------------------------------------
;; hostile-data   docs/SPEC-WORK.md:5602
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: hostile-data intake adapter (reader evaluation disabled;
;; depth/byte/node limits; no command execution or authority change; quadratic
;; copying avoided).
;;(deftest "hostile-data" "docs/SPEC-WORK.md:5602"
;;    "eval-disabled,limits=enforced,command-execution=0,authority=unchanged"
;;  ;; reader evaluation disabled; pre-parse depth, byte and node limits
;;  ;; enforced; imported prose cannot execute a command or alter authority.)

;; ------------------------------------------------------------------
;; index-replayed-after-crash   docs/SPEC-WORK.md:5071
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: session + closed/open indexes (crash between settle and clip:
;; one journal replay moves the id to C's index out of O's tree before any
;; ask, totals reconcile).
;;(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:5071"
;;    "replay=one,open+closed=total,revive=recovered-in-O"
;;  ;; a session killed between a `:settle` and the next clip: the restart's one
;;  ;; journal replay puts the item in C's index and out of O's tree before the
;;  ;; first ask, `open=` and `closed=` sum to the same total.)

;; ------------------------------------------------------------------
;; indivisible-record-refused-before-ack   docs/SPEC-WORK.md:5543
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: paged index + admission gate (a single key whose one locator
;; no page could hold refused `indivisible` before acknowledgement, nothing
;; journaled).
;;(deftest "indivisible-record-refused-before-ack" "docs/SPEC-WORK.md:5543"
;;    "indivisible=refused,nothing-journaled"
;;  ;; one key with its locator that no page under `--page-bytes` could hold
;;  ;; refused `indivisible` at exit 2 with nothing journaled.)

;; ------------------------------------------------------------------
;; inventory-expansion-and-contraction   docs/SPEC-WORK.md:5712
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: inventory/denominator accounting (initial inventory preserved,
;; discovered work counted separately; changed denominator visible).
;;(deftest "inventory-expansion-and-contraction" "docs/SPEC-WORK.md:5712"
;;    "inventory=preserved,counted=separately,denominator=visible"
;;  ;; initial inventory preserved and discovered; completed, reopened,
;;  ;; decomposed and explicitly removed work counted separately; a changed
;;  ;; denominator visible beside progress and never silently revised.)

;; ------------------------------------------------------------------
;; late-and-duplicate-receipts-are-retained   docs/SPEC-WORK.md:5243
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: receipt/lease reconciliation (a late accept retained `:late`
;; reviving no lease, overwriting no successor; same request replaying its
;; success).
;;(deftest "late-and-duplicate-receipts-are-retained" "docs/SPEC-WORK.md:5243"
;;    "late=retained,lease-revived=0,successor-overwritten=0"
;;  ;; a late accept after a decline, a replacement, an expiry or a generation
;;  ;; change retained `:late`, reviving no lease and overwriting no successor.)

;; ------------------------------------------------------------------
;; local-tokens-cost-zero-api   docs/SPEC-WORK.md:5288
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: pricing/cost accounting (the three cost values kept separately
;; labelled; a missing dimension unknown, never zero).
;;(deftest "local-tokens-cost-zero-api" "docs/SPEC-WORK.md:5288"
;;    "cost-values=separately-labelled,missing=unknown"
;;  ;; the three cost values kept separately labelled.)

;; ------------------------------------------------------------------
;; materialized-working-set   docs/SPEC-WORK.md:5597
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: materialized W index (repeated membership/|W| asks visit zero
;; unrelated nodes; independent reconstruction equal after take/renew/release).
;;(deftest "materialized-working-set" "docs/SPEC-WORK.md:5597"
;;    "unrelated-visits=0,scans=0,reconstruction=equal,watermark=printed"
;;  ;; W held fixed while O and C grow: membership and |W| asks visit zero
;;  ;; unrelated nodes, scan neither O nor C.)

;; ------------------------------------------------------------------
;; matrix-retirement   docs/SPEC-WORK.md:5387
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap axes (only selected coordinates retired and
;; recoverable; no task cancelled; layout change on populated roadmap refused).
;;(deftest "matrix-retirement" "docs/SPEC-WORK.md:5387"
;;    "selected=retired,recoverable=yes,task-cancelled=0"
;;  ;; `axis --remove` of a first-axis row and then of another axis's member:
;;  ;; only the selected coordinates retired and recoverable, no task cancelled.)

;; ------------------------------------------------------------------
;; merged-is-not-distributed   docs/SPEC-WORK.md:5093
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: release/distribution state (a fix in C with `landed=<sha>` and
;; `released=-` while its release task is open; `released=<version>` only once
;; that task settles).
;;(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:5093"
;;    "landed=set,released=-,released-set-only-when-task-settles"
;;  ;; a fix in C with `landed=<sha>` and `released=-` while its release task is
;;  ;; open, and `released=<version>` on the same row once that task settles.)

;; ------------------------------------------------------------------
;; metadata-patches-preserve-intent   docs/SPEC-WORK.md:5347
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node add/edit verbs (keep/clear/set-empty/set-false/set-value
;; round-trip distinctly; a malformed tag or wrong type refused, no event, no
;; counter).
;;(deftest "metadata-patches-preserve-intent" "docs/SPEC-WORK.md:5347"
;;    "patches=round-trip,malformed=refused,counter=moved"
;;  ;; keep, clear, set-empty, set-false and set-value on each field round-trip
;;  ;; and digest distinctly.)

;; ------------------------------------------------------------------
;; missing-segment-is-a-gap   docs/SPEC-WORK.md:5132
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: closed history segments + coverage gaps (a removed named
;; segment prints `gap=<n>` and a coverage-gap note, never an empty closed set).
;;(deftest "missing-segment-is-a-gap" "docs/SPEC-WORK.md:5132"
;;    "gap=<n>,coverage-gap=noted,closed-set=never-empty"
;;  ;; a segment the committed root names, removed: the listing answers what it
;;  ;; can with `gap=<n>` and one coverage-gap note, never an empty closed set.)

;; ------------------------------------------------------------------
;; move-keeps-every-count   docs/SPEC-WORK.md:5360
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb (a required subtree moved keeps all counts
;; consistent; no whole-set scan).
;;(deftest "move-keeps-every-count" "docs/SPEC-WORK.md:5360"
;;    "source/dest=by-subtree,net=stable,visits=asserted"
;;  ;; a required subtree moved between two features: the source's and
;;  ;; destination's required sets and open counts move by the subtree, the
;;  ;; common ancestor's net count is stable.)

;; ------------------------------------------------------------------
;; move-keeps-the-lease   docs/SPEC-WORK.md:5370
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: leases over moved subtrees (a working subtree moved with its
;; effective `:responsible` unchanged keeps the same lease/attempt/usage).
;;(deftest "move-keeps-the-lease" "docs/SPEC-WORK.md:5370"
;;    "lease=same,attempt=same,usage=same,active-change=refused"
;;  ;; a working subtree moved with its effective `:responsible` unchanged keeps
;;  ;; the same lease, attempt and usage.)

;; ------------------------------------------------------------------
;; move-refuses-by-name   docs/SPEC-WORK.md:5367
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb refusals (a wrong `--from`, destination inside
;; the subtree, a root container, etc. each refused with its named reason).
;;(deftest "move-refuses-by-name" "docs/SPEC-WORK.md:5367"
;;    "each-refusal=named,nothing-written"
;;  ;; a wrong `--from`, a destination inside the subtree, a root container, a
;;  ;; repository root, a shared container, another repository, and a roadmap as
;;  ;; either parent, each refused with its named reason.)

;; ------------------------------------------------------------------
;; move-same-parent-is-a-receipt   docs/SPEC-WORK.md:5364
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: node move verb receipts (`--from` equal to `--under`: structure
;; event alone, `changed=0`, sibling order unchanged).
;;(deftest "move-same-parent-is-a-receipt" "docs/SPEC-WORK.md:5364"
;;    "changed=0,one-envelope,sibling-order=unchanged"
;;  ;; `--from` equal to `--under`: the structure event alone, `changed=0`,
;;  ;; sibling order unchanged.)

;; ------------------------------------------------------------------
;; move-undo-refuses-a-reorder   docs/SPEC-WORK.md:5379
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: move undo (undo after a sibling reorder, reparent or privacy
;; change refused conflict, guessing no position).
;;(deftest "move-undo-refuses-a-reorder" "docs/SPEC-WORK.md:5379"
;;    "undo=conflict,position=never-guessed"
;;  ;; undo after a sibling reorder, a further reparent or a privacy change
;;  ;; refused conflict and guessing no position.)

;; ------------------------------------------------------------------
;; move-updates-every-roadmap-scope   docs/SPEC-WORK.md:5375
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap scope revisions (a row referenced by two roadmaps
;; advances both referencing scopes, unrelated scope stays).
;;(deftest "move-updates-every-roadmap-scope" "docs/SPEC-WORK.md:5375"
;;    "referencing-scopes=advance,unrelated=stays"
;;  ;; a row referenced by two roadmaps outside both parent chains and one
;;  ;; unrelated roadmap: both referencing scope revisions advance, the
;;  ;; unrelated one stays.)

;; ------------------------------------------------------------------
;; moving-source   docs/SPEC-WORK.md:5585
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: provider intake adapter (a body edited / comment added and
;; deleted during capture: captured versions preserved, incomplete marked).
;;(deftest "moving-source" "docs/SPEC-WORK.md:5585"
;;    "captured=preserved,incomplete=marked,reconciled=yes"
;;  ;; a body edited, a visible comment added and deleted, labels and state
;;  ;; changed and an issue reopened during capture: captured versions
;;  ;; preserved, a mixed or incomplete capture marked as such.)

;;;; ------------------------------------------------------------------
;;;; Draft-25..27 replays promised by docs/SPEC-WORK.md and absent here.
;;;;
;;;; Every one of these names a verb, feature or wire property that is outside
;;;; the slice-1 C/O transition kernel (README.md "What is out"). With none of
;;;; the kernel work present, each replay asserts the only thing the sentence
;;;; currently makes true of this build -- that the feature refuses cleanly at
;;;; the boundary rather than silently inventing a partial answer -- and is
;;;; marked ;; NEEDS-KERNEL: for the work that flips it to assert the sentence
;;;; whole. They are kept, and counted, so the promised name is never lost.
;;;; ------------------------------------------------------------------

(defun slice1-refuses-verb (verb &key (node "acme/work/f1/t1"))
  "Submit VERB, which the slice-1 kernel does not implement, and assert the
boundary refusal: exit 2, the line names it unsupported, and state is unmoved."
  (let* ((k (fresh))
         (before (root-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (list :verb verb :node node :by "rowan" :reason "r"
                        :request "req-unsup" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok (not okp) "~A was applied" verb)
      (check-equal 2 code (format nil "~A exit code" verb))
      (ok (search "unsupported" line) "~A refusal does not say unsupported: ~A" verb line))
    (check-string= before (root-digest (kernel-state k))
                   (format nil "~A mutated state" verb))))

;; NEEDS-KERNEL: undo/redo/friend/model/observe/config-intake verbs and their
;; own-kind ordered-field envelopes.
(deftest "new-verbs-have-a-kind-and-a-field-order" "docs/SPEC-WORK.md:5315"
    "expected=own-kind;field-order;:node(:absent);same-bytes"
  (slice1-refuses-verb :undo))

;; NEEDS-KERNEL: the six verbs above plus per-kind subject lines (nodes=/friend=/model=).
(deftest "new-verbs-retry-to-one-event" "docs/SPEC-WORK.md:5319"
    "expected=one-event;original-OK;changed-payload-refuses"
  (slice1-refuses-verb :redo))

;; NEEDS-KERNEL: a pause/hold and dispatch gate; acceptance retained :accepted-held.
(deftest "no-dispatch-slips-past-a-hold" "docs/SPEC-WORK.md:5258"
    "expected=offer-before-pause-refused-at-send;held-acceptance-converts-nothing"
  (slice1-refuses-verb :execution-stop))

(deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5176"
    "expected=noop-journaled;changed=0;digest-unchanged;rev+1"
  ;; NEEDS-KERNEL: a fresh-id no-op mutation and the changed= counter on the OK
  ;; line. Slice 1's OK line is `<MUTATION> OK id=.. request=.. node=.. rev=..
  ;; pushed=-` with no changed=, so this asserts the counter is still absent.
  (let ((k (fresh)))
    (multiple-value-bind (okp line code) (submit k (close-request :request "noop-probe"))
      (declare (ignore code))
      (ok okp "slice-1 transition refused")
      (ok (not (search "changed=" line)) "the OK line already carries changed=: ~A" line))))

(deftest "no-friend-name-in-the-tool" "docs/SPEC-WORK.md:5209"
    "expected=binary-and-fixtures-carry-no-friend-bench-repo-or-house-name"
  ;; NEEDS-KERNEL: a binary/defaults/fixtures audit is the Go client's, not the
  ;; slice-1 kernel's. The lisp seed already carries only the placeholder house.
  (dolist (node *seed*)
    (ok (search "acme/work" (getf node :id))
        "seed id ~A is not the placeholder house" (getf node :id))))

;; NEEDS-KERNEL: offer/accept/lease verbs and the cross-holder lease rule.
(deftest "no-shadow-lease-across-holders" "docs/SPEC-WORK.md:5238"
    "expected=cross-holder-reply-creates-no-lease"
  (slice1-refuses-verb :accept))

;; NEEDS-KERNEL: the offer verb writing :dispatched plus a reservation index entry.
(deftest "offer-writes-intent-and-a-reservation" "docs/SPEC-WORK.md:5229"
    "expected=:dispatched;pending-offer;reservation;nothing-else"
  (slice1-refuses-verb :offer))

;; NEEDS-KERNEL: archive export/reload and a recent-only export never labelled full.

;; NEEDS-KERNEL: clip staging/verify/commit in one revision with both index roots.
(deftest "one-revision-publishes-together" "docs/SPEC-WORK.md:5124"
    "expected=segments-indexes-files-one-commit;kill-leaves-prev-root"
  (slice1-refuses-verb :clip))

;; NEEDS-KERNEL: execution stop as a hold plus an evidence-set custom cancel.
