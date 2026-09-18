;;;; replays-verdict-state.lisp --- the pure part of nova-tools#854 item 1:
;;;; verdicts are state, not events (pit stop 3, #828 D).
;;;;
;;;; A read's APPROVE/HOLD is a row on the PR node, not a one-shot enqueue
;;;; decision. The row records the node, the reader, the exact head the read was
;;;; made at and the decision; every tick re-evaluates it until the PR merges or
;;;; closes. A head move does not discard the decision: it marks the row stale
;;;; and owes a fresh read at the new head. Terminal rows are final and owe
;;;; nothing. This is the pure layer only; the resident session, the PR node
;;;; store and the enqueue wiring a later slice owns are not here.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The verdict row (SPEC-WORK.md:4535)
;;; ------------------------------------------------------------------

(defstruct (verdict-row
             (:constructor make-verdict-row
                 (&key node reader head decision merged closed
                       (state :current))))
  "One read's APPROVE/HOLD as a state row on a PR node. HEAD is the exact head
the read was made at, DECISION is :approve or :hold, and MERGED/CLOSED make the
row terminal. STATE is maintained by TICK-VERDICT-ROW, never by the caller."
  node reader head decision merged closed state)

(defun verdict-approved-p (row)
  (eq (verdict-row-decision row) :approve))

(defun verdict-hold-p (row)
  (eq (verdict-row-decision row) :hold))

(defun verdict-terminal-p (row)
  "A merged or closed PR ends the re-evaluation; the row is final."
  (or (verdict-row-merged row) (verdict-row-closed row)))

(defun tick-verdict-row (row current-head)
  "Re-evaluate ROW at CURRENT-HEAD and return it. A terminal row is :final and
owes no read; a row whose head has not moved is :current; a row whose head has
moved is :stale, and a read is owed — the decision is reconsidered, never lost
because it was enqueued once."
  (setf (verdict-row-state row)
        (cond ((verdict-terminal-p row) :final)
              ((equal (verdict-row-head row) current-head) :current)
              (t :stale)))
  row)

(defun read-owed-p (row)
  "True when ROW's last tick left it stale: a read is owed at the new head."
  (eq (verdict-row-state row) :stale))
