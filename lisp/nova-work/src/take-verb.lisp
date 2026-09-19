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

(defun %take-node-submit (kernel request)
  "`take --node`, run on the kernel's one command thread.

Answers (values OK-P LINE EXIT-CODE ENVELOPE).

The refusal order is rule 3's, fixed so every expected line can be written
(SPEC-WORK.md:4894): an invocation that cannot be read, exit 2; then a stale
`--expect`; then a node the session does not hold, by this verb's existing line;
then NEEDS-MET; and only then the verb's other preconditions -- a node in C, and
a lease already held.

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
         ;; at the revision this request is applied at. A request may carry one
         ;; for a caller that built it; the kernel's is the default.
         (view (or (getf request :view) (kernel-needs-view kernel))))
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
      ;; The two-part dedup test is asked of the journal FIRST, as `%submit`
      ;; asks it, so a retry of one request id answers the original line and
      ;; never this verb's `held` precondition.
      (let* ((event (make-work-event
                     :kind :lease :node node :by by
                     :fields (list :holder by
                                   :default (getf request :default +absent+))
                     :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
                     :clock (or (getf request :clock) :tool)
                     :request rid
                     :generation-owner (or (getf request :generation-owner) by)
                     :rev (kernel-next-rev kernel)))
             (digest (payload-digest (list event)))
             (line (format nil "LEASE OK id=~A request=~A node=~A holder=~A rev=~D pushed=-"
                           (event-id event) rid node by (work-event-rev event))))
        ;; `--dry-run` journals nothing and writes nothing (:4934).
        ;; dedup first (SPEC-WORK.md:315), then this verb's last precondition
        (multiple-value-bind (found recorded-digest recorded-line)
            (journal-lookup (kernel-journal kernel) rid)
          (cond
            ((eq found :unavailable)
             (return-from %take-node-submit
               (values nil (format nil "LEASE FAIL node=~A page=~A: dedup unavailable"
                                   node recorded-digest)
                       1 nil)))
            (found
             (return-from %take-node-submit
               (if (string= digest recorded-digest)
                   (values t recorded-line 0
                           (list :request rid :digest digest :events (list) :replayed t))
                   (values nil (format nil "LEASE FAIL node=~A request=~A: reused with a different payload"
                                       node rid)
                           1 nil))))))
        (when (wnode-holder n)
          (return-from %take-node-submit
            (values nil (format nil "LEASE FAIL node=~A holder=~A: held" node (wnode-holder n))
                    1 nil)))
        (when dry-run
          (return-from %take-node-submit
            (values t (format nil "~A dry-run=true" line) 0 nil)))
        (%oneshot-submit kernel rid digest line event "LEASE"
                         (list :verb :take-node :node node :holder by :request rid))))))
