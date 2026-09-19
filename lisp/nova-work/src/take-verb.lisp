;;;; take-verb.lisp --- the node lease as a journaled command (nova-tools #1612
;;;; lane; the spine lifted from #1587's src/take-verb.lisp, without its v2
;;;; dependency gate).
;;;;
;;;; THE DEFECT THIS REPAIRS. `take-lease` and `release-lease` lived in
;;;; src/state.lisp and did this:
;;;;
;;;;     (setf (wnode-holder n) by)
;;;;
;;;; -- a raw mutation of the live kernel state, on the CALLER's thread, with
;;;; no event, no envelope, no journal record, no request id and no revision.
;;;; docs/SPEC-WORK.md:2603-2616 (rule 6) says in as many words: "a mutation
;;;; outside the command loop is a defect". The consequences were not academic:
;;;;
;;;;   * a lease was invisible to the total order the journal's sequence
;;;;     numbers ARE, so no reader of the journal could see who held what;
;;;;   * a lease did not survive `replay-journal`: reconstructing a kernel from
;;;;     its own journal gave every node back UNOWNED, whatever had been taken;
;;;;   * `take` had no two-part dedup (:315): a retried request took again;
;;;;   * and `take` wrote no lease-log row at all, although `settle` wrote the
;;;;     `:release` that ended it -- so the log held releases of takes it had
;;;;     never heard of.
;;;;
;;;; A lock around the old direct mutation would fix none of that: the missing
;;;; thing is the journal record, not the exclusion.
;;;;
;;;; THE REPAIR is structural and is the one this kernel already has for every
;;;; other writer verb. `take` and `release` are `:lease` EVENTS submitted
;;;; through `submit`, dispatched by `%submit`, and installed by
;;;; `%oneshot-submit` -> `%install-envelope` (src/edit-undo.lisp): accept,
;;;; RECORD, then apply, all-or-none, on the one command thread.
;;;;
;;;; THE ORDER INSIDE THE WRITER, which is the part that is easy to get wrong:
;;;;
;;;;   1. the invocation check -- reads the REQUEST only and no state, exit 2;
;;;;   2. the DURABLE REPLAY ANSWER -- reads the JOURNAL only and no state. An
;;;;      identical recorded payload answers its ORIGINAL receipt whatever the
;;;;      set has done since, and grants no second lease, because the lease was
;;;;      already granted and recorded. The same id with a different payload is
;;;;      refused;
;;;;   3. and only THEN the mutable preconditions -- the node, its branch, its
;;;;      holder.
;;;;
;;;; Reading state before the replay answer is what makes a retry of an
;;;; unchanged successful request fail on its own receipt, and it is the defect
;;;; Stella found three times in the v2 stack.
;;;;
;;;; WHAT IS DELIBERATELY NOT HERE (v1 only, Glenn: "finish v1 first, then v2"):
;;;; no needs view, no needs gate, no evidence reading, no `ready` row, no
;;;; `--expect`, no `--dry-run`, no dispatch registry and no schema change.
;;;; docs/SPEC-WORK.md:2117 is explicit that v1 reads a need in C as terminal
;;;; accepted "whatever its disposition and whatever its evidence", so a lease
;;;; over a node whose need is in C is admitted here with nothing else asked.

(in-package #:nova-work)

(defun %lease-request-refusal (request word)
  "An invocation the lease verbs cannot read, or NIL. Exit 2 is the code.

The request id is REQUIRED, as it is for every journaled command: the two-part
dedup of SPEC-WORK.md:315 is the caller's id or it is nothing, and a kernel that
invents one has invented the answer to its own retry."
  (let ((node (getf request :node))
        (by (getf request :by))
        (rid (getf request :request)))
    (declare (ignore word))
    (cond
      ((not (and (stringp node) (plusp (length node)))) "refusing to guess: --node")
      ((not (and (stringp by) (plusp (length by)))) "refusing to guess: --by")
      ((not (and (stringp rid) (plusp (length rid)))) "refusing to guess: --request")
      (t nil))))

(defun %lease-event (kernel request change)
  "One `:lease` event for CHANGE, which is :TAKE or :RELEASE.

The digest is taken over :kind, :node, :by and the kind's own ordered fields and
NEVER over the revision (src/event.lisp `event-digest-form`), so one request
digests the same however far the set has moved since it was recorded. That is
what makes step 2 above answerable from the journal alone."
  (make-work-event
   :kind :lease
   :node (getf request :node)
   :by (getf request :by)
   :fields (list :change change :holder (getf request :by))
   :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
   :clock (or (getf request :clock) :tool)
   :request (getf request :request)
   :generation-owner (or (getf request :generation-owner) (getf request :by))
   :rev (kernel-next-rev kernel)))

(defun %take-node-submit (kernel request)
  "`take`: one live lease per node, as a journaled command on the one writer.
Answers (values OK-P LINE EXIT-CODE ENVELOPE) (SPEC-WORK.md:135, :1354-1357)."
  (let ((refusal (%lease-request-refusal request "LEASE")))
    (when refusal
      (return-from %take-node-submit
        (values nil (format nil "LEASE FAIL node=~A: ~A; run: nova-work help"
                            (or (getf request :node) "-") refusal)
                2 nil))))
  (let* ((node (getf request :node))
         (by (getf request :by))
         (rid (getf request :request))
         (event (%lease-event kernel request :take))
         (digest (payload-digest (list event))))
    ;; ---- the durable replay answer, before any mutable state --------------
    (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
      (when verdict
        (return-from %take-node-submit
          (if (eq verdict :replay)
              (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))
              (values nil (%dedup-refusal "LEASE" rid verdict recorded) 1 nil)))))
    ;; ---- and only now the mutable state -----------------------------------
    (let ((n (%node-quiet (kernel-state kernel) node)))
      (unless n
        (return-from %take-node-submit
          (values nil (format nil "LEASE FAIL node=~A: rule 2: no such node ~A" node node)
                  1 nil)))
      (unless (eq :o (wnode-branch n))
        (return-from %take-node-submit
          (values nil (format nil "LEASE FAIL node=~A: ~A is in C and takes no lease" node node)
                  1 nil)))
      (when (wnode-holder n)
        (return-from %take-node-submit
          (values nil (format nil "LEASE FAIL node=~A holder=~A: held" node (wnode-holder n))
                  1 nil)))
      (let ((line (format nil "LEASE OK id=~A request=~A node=~A holder=~A rev=~D pushed=-"
                          (event-id event) rid node by (work-event-rev event))))
        (%oneshot-submit kernel rid digest line event "LEASE"
                         (list :verb :take-node :node node :holder by :request request))))))

(defun %release-node-submit (kernel request)
  "`release`: a claim is ended by the one who made it, never a third name
reaching in (SPEC-WORK.md:1354-1357). Journaled on the same spine as `take`:
a release that is not recorded is a lease that comes back on reconstruction."
  (let ((refusal (%lease-request-refusal request "LEASE")))
    (when refusal
      (return-from %release-node-submit
        (values nil (format nil "LEASE FAIL node=~A: ~A; run: nova-work help"
                            (or (getf request :node) "-") refusal)
                2 nil))))
  (let* ((node (getf request :node))
         (by (getf request :by))
         (rid (getf request :request))
         (event (%lease-event kernel request :release))
         (digest (payload-digest (list event))))
    (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
      (when verdict
        (return-from %release-node-submit
          (if (eq verdict :replay)
              (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))
              (values nil (%dedup-refusal "LEASE" rid verdict recorded) 1 nil)))))
    (let ((n (%node-quiet (kernel-state kernel) node)))
      (unless n
        (return-from %release-node-submit
          (values nil (format nil "LEASE FAIL node=~A: rule 2: no such node ~A" node node)
                  1 nil)))
      (unless (equal by (wnode-holder n))
        (return-from %release-node-submit
          (values nil (format nil "LEASE FAIL node=~A holder=~A live: held" node
                               (or (wnode-holder n) "unowned"))
                  1 nil)))
      (let ((line (format nil "LEASE RELEASE OK id=~A request=~A node=~A holder=~A rev=~D pushed=-"
                          (event-id event) rid node by (work-event-rev event))))
        (%oneshot-submit kernel rid digest line event "LEASE"
                         (list :verb :release-node :node node :holder by :request request))))))

;;; ------------------------------------------------------------------
;;; The two convenience entries, which are what the suite and the readers
;;; have always called. Their contract is unchanged: they answer BY, and a
;;; refusal signals `unsupported-input` carrying the verb's own FAIL line.
;;; ------------------------------------------------------------------

(defun %derived-lease-request-id (kernel word node by)
  "A request id for a caller that named none.

It is derived from the kernel's own REVISION, which is durable: it comes back
from the journal on reconstruction, so an id minted after a restart cannot
collide with one the journal already holds. A process-local counter would --
it resets to zero with the process while the journal does not.

A caller that has a session and a request id of its own passes it; this is for
the in-process callers that have neither."
  (format nil "~A-~D-~A-~A" word (kernel-next-rev kernel) node by))

(defun take-lease (kernel id by &key request)
  "`take`: one live lease per node; a second `take` is refused and names the
holder (SPEC-WORK.md:135). Goes through `submit`, so the mutation is one
journaled command in the single writer's total order."
  (multiple-value-bind (okp line)
      (submit kernel (list :verb :take-node :node id :by by
                           :request (or request
                                        (%derived-lease-request-id kernel "take" id by))))
    (unless okp (error 'unsupported-input :what line))
    by))

(defun release-lease (kernel id by &key request)
  "`release`: a claim is ended by the one who made it, never a third name
reaching in (SPEC-WORK.md:1354-1357). Journaled, like `take`."
  (multiple-value-bind (okp line)
      (submit kernel (list :verb :release-node :node id :by by
                           :request (or request
                                        (%derived-lease-request-id kernel "release" id by))))
    (unless okp (error 'unsupported-input :what line))
    by))
