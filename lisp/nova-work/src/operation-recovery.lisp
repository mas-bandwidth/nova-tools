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

(defun reconcile-operation-registry (registry)
  "Register every operation id the durable journal holds that this process does
not know about, as the restart does before anything is retried. Answer the ids
recovered, oldest first; answer NIL when there was nothing to recover, so a
second pass over an already reconciled registry recovers nothing and registers
no duplicate (SPEC-WORK.md:2759-2760)."
  (let ((journal (operation-registry-journal registry)))
    (loop for id in (operation-journal-ids journal)
          unless (assoc id (operation-registry-operations registry) :test #'equal)
            collect (progn (recover-operation registry id) id))))

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
