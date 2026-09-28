;;;; scheduler.lisp --- the operation scheduler: the durable accept record and
;;;; the status/list/wait/cancel surface (SPEC-WORK.md:2721-2760).
;;;;
;;;; The long-operation protocol is one scheduler. A long operation returns an
;;;; operation id and a state at once; the accept record -- the id, the
;;;; operation kind, the request id, the author and the stamp -- is appended to
;;;; the local recovery journal and made durable BEFORE the id is answered, so
;;;; the recovery reconciliation has an id for every operation a caller was
;;;; ever told about (SPEC-WORK.md:2721-2730).
;;;;
;;;; The durable-accept seam below has two implementations: an in-process
;;;; record used by the pure model, and the real bounded filesystem journal of
;;;; src/journal.lisp, whose record method writes and fsyncs the accept frame
;;;; before returning. A later transport slice reads the same journal back.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The durable accept record (seam: the in-process implementation)
;;; ------------------------------------------------------------------

(defclass in-process-accept-journal ()
  ((records :initform '() :accessor accept-journal-records)))

(defun make-accept-journal ()
  "The in-process implementation of the durable-accept seam. The real
implementation is the file journal; this one keeps the same protocol for the
pure model and the acceptance harness (seam: durable-accept-record)."
  (make-instance 'in-process-accept-journal))

(defgeneric durable-accept-record (journal record)
  (:documentation "Append RECORD to JOURNAL and make it durable before returning
the operation id. Answer the id. A refusal signals rather than answering an id
the journal does not hold."))

(defgeneric accept-record-of (journal id)
  (:documentation "The accept record the journal holds for ID, or NIL."))

(defgeneric accept-journal-count (journal)
  (:documentation "The number of accept records the journal holds."))

(defmethod durable-accept-record ((journal in-process-accept-journal) record)
  (push record (accept-journal-records journal))
  (getf record :id))

(defmethod accept-record-of ((journal in-process-accept-journal) id)
  (find id (accept-journal-records journal)
        :key (lambda (record) (getf record :id)) :test #'equal))

(defmethod accept-journal-count ((journal in-process-accept-journal))
  (length (accept-journal-records journal)))

;; No journal at all is the degenerate case; it still refuses nothing and
;; answers the id, but the caller has chosen not to keep a recovery record.
(defmethod durable-accept-record ((journal null) record)
  (getf record :id))

(defmethod accept-record-of ((journal null) id)
  (declare (ignore id))
  nil)

(defmethod accept-journal-count ((journal null))
  0)

;;; ------------------------------------------------------------------
;;; The durable accept record on the real recovery journal
;;; ------------------------------------------------------------------

;;; An accept record rides the journal's accepted-request frame: the operation
;;; id is the request id, the canonical accept record is the recorded line, and
;;; its digest is the payload digest. JOURNAL-RECORD writes the frame and
;;; fsyncs it before answering, which is exactly "durable before it is printed".

(defmethod durable-accept-record ((journal file-journal) record)
  (let* ((id (getf record :id))
         (line (canonical-string record))
         (digest (sha256-hex line))
         (envelope (list :request id :digest digest :events '())))
    (multiple-value-bind (accepted reason) (journal-accept journal envelope)
      (unless accepted
        (error "operation accept refused for ~A: ~A" id reason)))
    (journal-record journal id digest line 0)
    id))

(defmethod accept-record-of ((journal file-journal) id)
  (multiple-value-bind (found digest line) (journal-lookup journal id)
    (declare (ignore digest))
    (when found (read-restricted line))))

(defmethod accept-journal-count ((journal file-journal))
  (journal-seq journal))

(defun recover-operation (registry id)
  "Register an operation the durable journal holds but this process does not,
as the restart reconciliation does before anything is retried. Answer the
accept record, or NIL when no journal holds ID (SPEC-WORK.md:2755-2756)."
  (let ((record (accept-record-of (operation-registry-journal registry) id)))
    (when (and record
               (null (assoc id (operation-registry-operations registry)
                            :test #'string=)))
      (setf (operation-registry-operations registry)
            (append (operation-registry-operations registry)
                    (list (cons id (append record (list :state :queued
                                                        :result nil)))))))
    record))

;;; ------------------------------------------------------------------
;;; operation list: bounded like every other listing (SPEC-WORK.md:2731-2733)
;;; ------------------------------------------------------------------

(defun session-operation-list (session &key max)
  "A bounded listing of the session's operation records, newest first. MAX caps
the listing the way every other listing is capped; the queue limit is the
default (SPEC-WORK.md:2731-2733)."
  (let ((cap (or max (getf (work-session-limits session) :queue))))
    (loop for op in (reverse (work-session-operations session))
          repeat cap
          collect (list :id (operation-id op) :op (operation-op op)
                        :state (operation-state op) :request (operation-request op)))))

(defun registry-operation-list (registry &key max)
  "A bounded listing of the durable registry's operation records, newest first
(SPEC-WORK.md:2731-2733)."
  (let ((cap (or max 4)))
    (loop for (id . record) in (reverse (operation-registry-operations registry))
          repeat cap
          collect (list :id id :op (getf record :kind) :state (getf record :state)))))
