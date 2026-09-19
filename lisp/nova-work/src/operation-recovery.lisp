;;;; operation-recovery.lisp --- the operation scheduler's local recovery
;;;; journal and its restart reconciliation (SPEC-WORK.md:2728-2733, :2759-2760).
;;;;
;;;; src/scheduler.lisp carries the durable-accept seam and both its
;;;; implementations. What was missing is the thing that makes the guarantee a
;;;; guarantee rather than a protocol: a registry that is opened over a real
;;;; file on disk, and a reconciliation that reads every accept record back
;;;; when a second process opens that file. "A crash between accepting the work
;;;; and acknowledging it can never leave a caller holding an id the restart
;;;; never heard of" (SPEC-WORK.md:2731-2733) is a claim about the restart, so
;;;; the restart has to be able to ask.
;;;;
;;;; The recovery journal is a file of its own, not the kernel's journal: an
;;;; accept record is not a canonical event and must never enter the kernel's
;;;; replay. It uses the same bounded, fsynced, header-validated file journal
;;;; of src/journal.lisp, whose JOURNAL-RECORD publishes and syncs the frame
;;;; before it returns -- which is exactly "waits for that journal to be
;;;; durable, and only then replies".

(in-package #:nova-work)

(defparameter *operation-journal-initial-state* "nova-work/operation-recovery/v1"
  "The recovery journal's header identity. It pins no kernel revision because
an accept record is not a kernel event; it is here so a kernel journal and a
recovery journal can never be opened as one another.")

(defgeneric operation-journal-ids (journal)
  (:documentation "Every operation id the durable accept journal holds, oldest
accepted first. This is the set the restart reconciliation walks
(SPEC-WORK.md:2759-2760)."))

(defmethod operation-journal-ids ((journal null))
  '())

(defmethod operation-journal-ids ((journal in-process-accept-journal))
  (reverse (mapcar (lambda (record) (getf record :id))
                   (accept-journal-records journal))))

(defmethod operation-journal-ids ((journal file-journal))
  (journal-order journal))

(defun cancel-record-p (record)
  "True when RECORD is a cancellation and not an operation accept. One journal
carries both, so the reconciliation has to tell them apart."
  (and record (eq :cancel (getf record :record))))

(defun registry-apply-cancel-record (registry record)
  "Install the disposition a durable cancel record carries onto the operation it
names. Answer the operation cell, or NIL when the journal holds a cancel for an
operation it does not hold."
  (let ((cell (assoc (getf record :operation)
                     (operation-registry-operations registry) :test #'equal)))
    (when cell
      (setf (getf (cdr cell) :state) (getf record :disposition)))
    cell))

(defun reconcile-operation-registry (registry)
  "Register every operation id the durable journal holds that this process does
not know about, and replay onto it every cancellation the journal holds, as the
restart does before anything is retried. Answer the operation ids recovered,
oldest first; answer NIL when there was nothing to recover, so a second pass
over an already reconciled registry recovers nothing and registers no duplicate
(SPEC-WORK.md:2759-2760, :2740-2743)."
  (let ((journal (operation-registry-journal registry))
        (recovered (list)))
    (dolist (id (operation-journal-ids journal) (nreverse recovered))
      (let ((record (accept-record-of journal id)))
        (cond
          ((null record))
          ((cancel-record-p record) (registry-apply-cancel-record registry record))
          ;; Any other tagged record on this journal belongs to another
          ;; subsystem of the same operation (a staging record, for one) and is
          ;; not an operation id to recover.
          ((getf record :record))
          ((assoc id (operation-registry-operations registry) :test #'equal))
          (t (recover-operation registry id)
             (push id recovered)))))))

(defun open-durable-operation-registry (path &key (capacity 64))
  "Open the local recovery journal at PATH and answer an operation registry
over it, reconciled against every accept record the file already holds. CAPACITY
is the journal's explicit bound: past it an accept is refused rather than
answered with an id the journal cannot hold (SPEC-WORK.md:2728-2733, :2759)."
  (let* ((journal (open-file-journal path
                                     :capacity capacity
                                     :initial-state-hash *operation-journal-initial-state*))
         (registry (make-operation-registry :journal journal)))
    (reconcile-operation-registry registry)
    registry))

(defun close-durable-operation-registry (registry)
  "Close the registry's recovery journal and release its lock. Closing is not a
cancellation: the accept records stay on disk and the next open reconciles them
(SPEC-WORK.md:2759-2760)."
  (let ((journal (operation-registry-journal registry)))
    (when (typep journal 'file-journal)
      (close-file-journal journal))
    t))

;;; ------------------------------------------------------------------
;;; The cancellation: a request of its own, deduplicated on the same
;;; durable journal (SPEC-WORK.md:2717-2720, :2740-2744)
;;; ------------------------------------------------------------------
;;;
;;; "A cancellation is a request with its own acknowledgement and its own final
;;; disposition ... deduplicated by the same predicate, and a cancel replayed
;;; twice cancels once" (:2740-2743). The predicate is the journal's, and the
;;; paragraph above it says what it may not be: "never by an unbounded resident
;;; map of request ids" (:2719-2720). src/operations.lisp's pure model keeps the
;;; cancellations in exactly such a map, which is also why a cancellation there
;;; does not survive the process. Here the cancel rides the recovery journal
;;; under its own request id, is durable before it is acknowledged, and is
;;; replayed out of the journal by the restart.
;;;
;;; A cancel record is told from an accept record by its :record tag, so the
;;; reconciliation can walk one journal and tell the two apart.

(defun make-cancel-record (&key request operation author stamp disposition)
  "The durable cancel record. Its :id is the cancel's own request id, because
that is what the dedup predicate is keyed by; :operation names the operation it
acknowledges (SPEC-WORK.md:2740-2743)."
  (list :id request :record :cancel :operation operation
        :author author :stamp stamp :disposition disposition))

(defun registry-cancellation-of (registry request)
  "The durable cancel record recorded under REQUEST, or NIL. The journal is the
only place asked (SPEC-WORK.md:2719-2720)."
  (let ((record (accept-record-of (operation-registry-journal registry) request)))
    (when (cancel-record-p record) record)))

(defun registry-operation-cancel (registry id &key request author stamp
                                                (external-effect :none))
  "Cancel the operation ID under the cancel's own REQUEST id.

Answers (values DISPOSITION LINE CODE REPLAYED-P): a plist disposition and code
0, or NIL, a refusal line and code 2. The record is appended to the recovery
journal and made durable before the acknowledgement, exactly as the accept
record is; a replay of the same request id answers the recorded disposition and
changes nothing, and a different request id is a fresh acknowledgement of the
operation's final disposition. A cancellation erases no accepted mutation: the
operation's accept record stays on the journal. An uncertain external effect is
never claimed cancelled (SPEC-WORK.md:2740-2744)."
  (let ((recorded (registry-cancellation-of registry request)))
    (when recorded
      (return-from registry-operation-cancel
        (if (equal id (getf recorded :operation))
            (values (list :id id :state (getf recorded :disposition) :request request)
                    nil 0 t)
            ;; The same request id with different arguments. The grammar's
            ;; general refusal line carries the reason; the reason token here is
            ;; this file's and is listed in the handoff for review.
            (values nil
                    (format nil "OPERATION FAIL id=~A op=- state=-: a request id reused"
                            id)
                    2 nil)))))
  ;; An id no journal holds has a line of its own, and nothing is written for it
  ;; (SPEC-WORK.md:2733-2735, :5982).
  (let ((cell (or (assoc id (operation-registry-operations registry) :test #'equal)
                  (progn (recover-operation registry id)
                         (assoc id (operation-registry-operations registry)
                                :test #'equal)))))
    (if (null cell)
        (values nil
                (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id)
                2 nil)
        (let* ((state (getf (cdr cell) :state))
               (disposition
                 (cond
                   ;; An external effect that may already have happened is
                   ;; reported uncertain, never claimed cancelled (:2743-2744).
                   ((eq external-effect :uncertain) :uncertain)
                   ;; Already settled: a different request id is a fresh
                   ;; acknowledgement of the final disposition, not a second
                   ;; cancellation.
                   ((member state '(:cancelled :uncertain :done)) state)
                   (t :cancelled))))
          (durable-accept-record (operation-registry-journal registry)
                                 (make-cancel-record :request request
                                                     :operation id
                                                     :author author
                                                     :stamp stamp
                                                     :disposition disposition))
          (setf (getf (cdr cell) :state) disposition)
          ;; A waiter must not go on blocking behind a disposition that is
          ;; already durable (SPEC-WORK.md:2740-2743).
          (operation-notify registry id)
          (values (list :id id :state disposition :request request) nil 0 nil)))))

(defun open-session-operation-registry (session &key (capacity 64))
  "The resident session's own operation registry, over the local recovery
journal beside its path. A restart of that session finds every operation id a
caller was ever told about (SPEC-WORK.md:2729-2733)."
  (open-durable-operation-registry (session-recovery-journal-path session)
                                   :capacity capacity))
