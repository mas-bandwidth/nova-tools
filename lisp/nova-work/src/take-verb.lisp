;;;; take-verb.lisp --- `take --node` as a writer verb (nova-tools #785).
;;;;
;;;; Rule 3 of *The dependency gate and the hand report*, docs/SPEC-WORK.md:4869,
;;;; and its applied-revision requirement at :4892:
;;;;
;;;;   "Each verb evaluates needs-met for the node it names inside the single
;;;;    writer, at the revision the request is applied at, so there is no window
;;;;    between the check and the write."
;;;;
;;;; STELLA'S [P1] ON 7333349f. The first cut of this slice read the gate on the
;;;; CALLER's thread and then called `%take-lease-unchecked` directly. With a
;;;; barrier immediately after the predicate read, a normal `submit` reopened the
;;;; need while take was paused; the need read `(NIL :NEED-REVERTED "-")` before
;;;; the barrier was released, and take still granted the lease afterwards. The
;;;; code comment claimed the applied-revision property and the code did not have
;;;; it.
;;;;
;;;; The repair is structural, not a lock: `take --node` is now a `:lease` EVENT
;;;; submitted through `submit`, so the gate and the write happen on the one
;;;; command thread, in the one total order, inside `%take-node-submit`. A reopen
;;;; is another command in that order and can no longer interleave. It also buys
;;;; what every other writer verb has: a request id, the two-part dedup answer, a
;;;; journal record before the apply, and a revision the lease log carries.

(in-package #:nova-work)

(defun %take-node-request-refusal (request)
  "An invocation `take --node` cannot read, or NIL. Exit 2 is the code."
  (let ((node (getf request :node))
        (by (getf request :by))
        (rid (getf request :request)))
    (cond
      ((not (and (stringp node) (plusp (length node)))) "refusing to guess: --node")
      ((not (and (stringp by) (plusp (length by)))) "refusing to guess: --by")
      ((not (and (stringp rid) (plusp (length rid)))) "refusing to guess: --request")
      (t nil))))

(defun %request-replay-verdict (kernel rid digest)
  "The two-part request dedup, asked of the journal (SPEC-WORK.md:315).

Answers NIL when this request is new, or (values KIND LINE) where KIND is
:REPLAY -- an identical recorded payload, so the ORIGINAL receipt is the answer
-- :CONFLICT, the same id with a different payload, or :UNAVAILABLE.

STELLA'S [P2] ON 1a11652d: this must be asked BEFORE every mutable-state
precondition. Her witness submitted `same-take` successfully at revision 3,
reopened its need through a normal submit, and retried the IDENTICAL request:
it came back `LEASE FAIL ... need-reverted` at exit 1 although the journal held
the original `LEASE OK`. `--expect` was checked before the lookup too, so an
unchanged successful request carrying its original expectation could fail on
its own retry.

The durable replay contract is about the RECORDED payload and not about the
state now: an identical replay answers the original receipt whatever has
happened since, and grants no new work, because the work was already granted
and recorded. A different payload under the same id is still refused."
  (multiple-value-bind (found recorded-digest recorded-line)
      (journal-lookup (kernel-journal kernel) rid)
    (cond
      ((eq found :unavailable) (values :unavailable recorded-digest))
      ((not found) nil)
      ((string= digest recorded-digest) (values :replay recorded-line))
      (t (values :conflict recorded-line)))))

(defun %take-node-submit (kernel request)
  "`take --node`, run on the kernel's one command thread.

Answers (values OK-P LINE EXIT-CODE ENVELOPE).

THE ORDER. An invocation it cannot read, exit 2 -- that reads the request only
and no state. Then the DURABLE REPLAY ANSWER, which reads the journal and no
state: an identical recorded payload answers its original receipt, and the same
id with a different payload is refused. Only then rule 3's refusal order over
mutable state (SPEC-WORK.md:4894): a stale `--expect`; a node the session does
not hold; NEEDS-MET; and only then this verb's other preconditions -- a node in
C, and a lease already held.

`--dry-run` is how a caller asks without writing (:4934): it answers the same
refusal or the projected receipt, journals nothing and writes nothing."
  (let ((refusal (%take-node-request-refusal request)))
    (when refusal
      (return-from %take-node-submit
        (values nil (format nil "LEASE FAIL node=~A: ~A; run: nova-work help"
                            (or (getf request :node) "-") refusal)
                2 nil))))
  (let* ((state (kernel-state kernel))
         (node (getf request :node))
         (by (getf request :by))
         (rid (getf request :request))
         (dry-run (getf request :dry-run))
         (expect (getf request :expect))
         ;; The authoritative view is the session's, read HERE, on the writer,
         ;; at the revision this request is applied at.
         (view (or (getf request :view) (kernel-needs-view kernel)))
         (event (make-work-event
                 :kind :lease :node node :by by
                 :fields (list :holder by
                               :default (getf request :default +absent+))
                 :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
                 :clock (or (getf request :clock) :tool)
                 :request rid
                 :generation-owner (or (getf request :generation-owner) by)
                 :rev (kernel-next-rev kernel)))
         ;; The digest is stable: it covers :kind, :node, :by and the kind's own
         ;; fields, and never the revision, so one request digests the same
         ;; however much the set has moved since (src/event.lisp).
         (digest (payload-digest (list event))))
    ;; ---- the durable replay answer, before any mutable state -------------
    (multiple-value-bind (verdict recorded) (%request-replay-verdict kernel rid digest)
      (case verdict
        (:unavailable
         (return-from %take-node-submit
           (values nil (format nil "LEASE FAIL node=~A page=~A: dedup unavailable"
                               node recorded)
                   1 nil)))
        (:replay
         (return-from %take-node-submit
           (values t recorded 0
                   (list :request rid :digest digest :events (list) :replayed t))))
        (:conflict
         (return-from %take-node-submit
           (values nil (format nil "LEASE FAIL node=~A request=~A: reused with a different payload"
                               node rid)
                   1 nil)))))
    ;; ---- and only now the mutable state ----------------------------------
    (when (and expect (/= expect (state-revision state)))
      (return-from %take-node-submit
        (values nil (format nil "LEASE FAIL node=~A expect=~D current=~D: stale"
                            node expect (state-revision state))
                1 nil)))
    (let ((n (%node-quiet state node)))
      ;; a node the session does not hold, before any need is read (:4929)
      (unless n
        (return-from %take-node-submit
          (values nil (format nil "LEASE FAIL node=~A: rule 2: no such node ~A" node node)
                  1 nil)))
      ;; then needs-met (:4894)
      (multiple-value-bind (tail unmet) (needs-gate-refusal state node :view view)
        (when tail
          (return-from %take-node-submit
            (values nil (format nil "LEASE FAIL node=~A unmet=~D: ~A" node unmet tail)
                    1 nil))))
      ;; and only then this verb's other preconditions
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
        ;; `--dry-run` journals nothing and writes nothing (:4934).
        (when dry-run
          (return-from %take-node-submit
            (values t (format nil "~A dry-run=true" line) 0 nil)))
        ;; ONE durable append and apply, with the final receipt.
        (%oneshot-submit kernel rid digest line event "LEASE"
                         (list :verb :take-node :node node :holder by :request rid))))))
