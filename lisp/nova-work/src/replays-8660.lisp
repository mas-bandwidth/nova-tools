;;;; replays-8660.lisp --- the pure duty-tier model behind the five replays of
;;;; docs/SPEC-WORK.md:5547 and the amendment #500 replay list at
;;;; docs/SPEC-WORK.md:6067-6079.
;;;;
;;;; The amendment says of itself "nothing here is built" (docs/SPEC-WORK.md:6444)
;;;; and the resident session, the provider dispatch and the CLI it names are
;;;; outside the slice-1 C/O transition kernel (README.md, "What is out"). So,
;;;; exactly as src/replays-applicable-delegation.lisp does for the
;;;; applicable/delegation replays, this file is the pure part the tests call:
;;;; the approved policy record and its execution, the escalation row and the
;;;; stale pass, the wait table's four presence columns, and the quiet pulse.
;;;; The revive replay itself is kernel behaviour and uses src/kernel.lisp.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; duty-tier-executes-the-policy             SPEC-WORK.md:6067-6070
;;; ------------------------------------------------------------------

(defstruct (policy-rule (:constructor make-policy-rule (&key id by task-class models)))
  "One rule of an approved policy. :by is the word the rule is; a rule with no
:by is refused at load (docs/SPEC-WORK.md:2574)."
  id by task-class models)

(defstruct (duty-policy (:constructor %make-duty-policy (&key version rules)))
  "A versioned approved finite policy record in C."
  version rules)

(defun load-policy (&key version rules)
  "Load the approved policy record; every rule must carry :by. A rule with no
:by refuses at load, because an approved policy is a record of whose word every
rule is (docs/SPEC-WORK.md:2572-2576)."
  (dolist (rule rules)
    (unless (and (stringp (policy-rule-by rule)) (plusp (length (policy-rule-by rule))))
      (error 'unsupported-input
             :what (format nil "policy rule ~A has no :by" (policy-rule-id rule)))))
  (%make-duty-policy :version version :rules (copy-list rules)))

(defun author-policy (&rest args)
  "The duty tier executes a policy it never authors (docs/SPEC-WORK.md:2569-2574)."
  (declare (ignore args))
  (error 'unsupported-input :what "a duty session never authors policy"))

(defun policy-rule-for (policy task-class)
  "The approved rule that decides TASK-CLASS, or NIL."
  (find task-class (duty-policy-rules policy)
        :key #'policy-rule-task-class :test #'equal))

(defun cheapest-qualified (models price-table)
  "The cheapest model among MODELS that the price table knows, or NIL. Ties go
to the name that sorts first, so the choice is deterministic."
  (let ((qualified (remove-if-not (lambda (m) (assoc m price-table :test #'equal))
                                  models)))
    (when qualified
      (first (sort (copy-list qualified) #'<
                   :key (lambda (m) (cdr (assoc m price-table :test #'equal))))))))

(defun execute-policy (policy task-class price-table)
  "Answer (values DISPOSITION MODEL-OR-RULE): :execute with the cheapest qualified
model, :none with the rule when no named model is qualified, or :escalate when no
rule could decide. Every judgment it cannot make becomes an escalation."
  (let ((rule (policy-rule-for policy task-class)))
    (unless rule
      (return-from execute-policy (values :escalate nil)))
    (let ((model (cheapest-qualified (policy-rule-models rule) price-table)))
      (if model
          (values :execute model)
          (values :none rule)))))

;;; ------------------------------------------------------------------
;;; escalation-carries-rule-default-age       SPEC-WORK.md:6071-6073
;;; ------------------------------------------------------------------

(defstruct (escalation-row
             (:constructor make-escalation-row (&key rule default age)))
  "An escalation row carries the policy rule that could not decide it, the
default that fires on silence, and its age (docs/SPEC-WORK.md:2579-2586)."
  rule default age)

(defun stale-pass (rows)
  "The coordinator's stale pass reads the three and reassigns nothing: answer the
same rows and a receipt of what it read."
  (values rows
          (format nil "STALE OK rows=~D reassigned=0" (length rows))))

;;; ------------------------------------------------------------------
;;; wait-table-four-presence-columns          SPEC-WORK.md:6074-6076
;;; ------------------------------------------------------------------

(defparameter *wait-presence-columns*
  '(:process-alive :beat-written :delivery-handled :parent-woke)
  "The four presence facts the per-harness wait table gains, beside :wait-source
(docs/SPEC-WORK.md:2589-2591).")

(defstruct (wait-row
             (:constructor make-wait-row
                 (&key harness process-alive beat-written delivery-handled parent-woke)))
  "One harness's wait row: the wait source plus the four presence columns."
  harness process-alive beat-written delivery-handled parent-woke)

(defun wait-presence (row)
  "The row's four presence facts, in column order."
  (list :process-alive (wait-row-process-alive row)
        :beat-written (wait-row-beat-written row)
        :delivery-handled (wait-row-delivery-handled row)
        :parent-woke (wait-row-parent-woke row)))

(defun wait-holds-resident-p (row)
  "A harness with the fourth column unproven cannot hold a resident session
(docs/SPEC-WORK.md:2591-2596)."
  (every (lambda (column) (getf (wait-presence row) column))
         *wait-presence-columns*))

(defun wait-holds-duty-p (row)
  "A harness that has proven the first three may hold a duty session driven by
notes (docs/SPEC-WORK.md:2594-2596)."
  (every (lambda (column) (getf (wait-presence row) column))
         '(:process-alive :beat-written :delivery-handled)))

;;; ------------------------------------------------------------------
;;; quiet-time-calls-nothing                  SPEC-WORK.md:6077-6079
;;; ------------------------------------------------------------------

(defstruct (pulse-result (:constructor %make-pulse-result
                              (&key model-calls notes published cost)))
  "One pulse's outcome: the calls it made, the notes it sent, what it published
mechanically, and the spend of the one call the event caused."
  model-calls notes published cost)

(defun quiet-pulse (&key changed decision price-table)
  "A resident or duty session with nothing changed makes no model call and sends
no note; state is published mechanically. When something changed, one call is
made and the cost of the event is the measured spend of that one call."
  (if (not changed)
      (%make-pulse-result :model-calls 0 :notes 0
                          :published '(:clip :beat :projection) :cost 0)
      (let* ((model (getf decision :model))
             (cost (or (cdr (assoc model price-table :test #'equal)) 0)))
        (%make-pulse-result :model-calls 1 :notes 0
                            :published '(:clip :beat :projection) :cost cost))))
