;;;; replays-8651.lisp --- pure functions and records for the three named
;;;; acceptance replays of docs/SPEC-WORK.md at origin/dev:
;;;;
;;;;   undo-redo                        SPEC-WORK.md:6245
;;;;   unknown-price-is-not-zero        SPEC-WORK.md:4636-4651
;;;;   unrelated-receipts-stay-reusable SPEC-WORK.md:4943-4951
;;;;
;;;; This file holds the pure functions and records the acceptance replays call.
;;;; It owns no state and no I/O. Where a replay names behaviour that belongs in
;;;; the resident kernel, session or state slices, the pure shape is proven here
;;;; and the wiring owed to those slices is listed in RESULT.md, one line each.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; unknown-price-is-not-zero                        SPEC-WORK.md:4636-4651
;;; ------------------------------------------------------------------

(defun resolved-price (value)
  "A missing or unsupported price dimension resolves to :unknown, and is never
read as zero (SPEC-WORK.md:4640)."
  (cond ((null value) :unknown)
        ((eq value :unsupported) :unknown)
        (t value)))

;;; ------------------------------------------------------------------
;;; undo-redo                                        SPEC-WORK.md:6245
;;; ------------------------------------------------------------------

(defparameter *reversible-verb-kinds*
  '(:node-add :node-require :decompose :accept :dep :axis :node-edit :node-move
    :roadmap-create :roadmap-configure :roadmap-row :roadmap-projection :prioritise
    :cell :responsible :source :take :release :offer :execution-pause :execution-stop
    :execution-resume :execution-correct :state-transition :event-defer :event-reopen
    :friend :model-register :model-rate :config-intake)
  "Which verbs are reversible, verb by verb. Terminal dispositions and recorded
receipts are refused by name and never reach an undo (SPEC-WORK.md:2853-2898).")

(defun reversible-verb-p (verb)
  (member verb *reversible-verb-kinds*))

(defstruct edit-entry
  "A reversible edit: its reverse keeps the preimage and postimage, so an undo
appends a compensation and a redo reapplies against the preimage."
  id verb preimage postimage)

(defun history-with-undo (history id)
  "Append a compensation for ID, preserving every earlier entry exactly where it
was (appending, never erasing)."
  (let ((entry (find id history :key #'edit-entry-id :test #'equal)))
    (if entry
        (append history
                (list (make-edit-entry
                       :id (format nil "~A-undo" id)
                       :verb :undo
                       :preimage (edit-entry-postimage entry)
                       :postimage (edit-entry-preimage entry))))
        history)))

(defun redo-applies-p (entry current)
  "Redo reapplies the intent against current preconditions rather than deleting
the undo; it is admitted only while CURRENT still equals the entry's preimage."
  (equal (edit-entry-preimage entry) current))

(defun conflict-is-explicit (history id current)
  "A conflicting undo or redo names what moved and mutates nothing: answer a
refusal naming the expected and current values, never a half-applied history."
  (let ((entry (find id history :key #'edit-entry-id :test #'equal)))
    (when (and entry (not (redo-applies-p entry current)))
      (list :conflict id :expected (edit-entry-preimage entry) :current current))))

;;; ------------------------------------------------------------------
;;; unrelated-receipts-stay-reusable                 SPEC-WORK.md:4943-4951
;;; ------------------------------------------------------------------

(defstruct proof-scope
  "A feature's declared proof scope: the paths and criteria a result receipts
against. A change outside it invalidates nothing (SPEC-WORK.md:4948-4949)."
  paths criteria)

(defstruct receipt
  "A retained result receipt, reusable while its declared proof scope is
untouched by any change."
  id scope)

(defun change-within-scope-p (change scope)
  (let ((path (getf change :path))
        (criterion (getf change :criterion)))
    (or (and path (member path (proof-scope-paths scope) :test #'equal))
        (and criterion (member criterion (proof-scope-criteria scope) :test #'equal)))))

(defun unrelated-receipts-stay-reusable (receipts change)
  "Answer the receipts whose declared proof scope a change does not touch; a
change outside a feature's scope invalidates no unrelated receipt without a
dependency reason (SPEC-WORK.md:4948-4951)."
  (remove-if (lambda (receipt) (change-within-scope-p change (receipt-scope receipt)))
             receipts))
