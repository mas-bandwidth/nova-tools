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

;;; ------------------------------------------------------------------
;;; Decision packet per item and revision (SPEC-WORK.md:4648, :4649)
;;; ------------------------------------------------------------------

(defstruct (decision-packet
             (:constructor make-decision-packet
                 (&key item revision reader head recorded-head
                       delta rules-touched open-findings evidence-pointers
                       links (whole-diff-p nil) (amended-p nil))))
  "One decision packet per item and revision.
Carries the delta since this reader's recorded head, the rules it touches,
the open findings with dispositions, the new behaviour with evidence pointers
and links to the full sources; the whole diff only when this reader has never
read the entry."
  item
  revision
  reader
  head
  recorded-head
  delta
  rules-touched
  open-findings
  evidence-pointers
  links
  whole-diff-p
  amended-p)

(defun build-decision-packet (&key item revision reader head recorded-head
                                   delta diff rules-touched open-findings
                                   evidence-pointers links busy)
  "Build a smallest-sufficient decision packet.
When RECORDED-HEAD is nil (the reader has never read the entry), carries the
whole diff (WHOLE-DIFF-P is true). Otherwise carries the delta since the reader's
recorded head."
  (let* ((first-read (null recorded-head))
         (computed-delta (if first-read
                             (or diff delta :whole-diff)
                             (or delta (when (and recorded-head head)
                                         (format nil "delta:~A..~A" recorded-head head))))))
    (make-decision-packet
     :item item
     :revision revision
     :reader reader
     :head head
     :recorded-head recorded-head
     :delta computed-delta
     :rules-touched (or rules-touched '())
     :open-findings (or open-findings '())
     :evidence-pointers (or evidence-pointers '())
     :links (or links '())
     :whole-diff-p first-read
     :amended-p (not (null busy)))))

(defun decision-packet (&rest args)
  "Flexible entry point: accepts positional (item revision &key ...)
or pure keyword arguments, dispatching to BUILD-DECISION-PACKET."
  (if (and args (not (keywordp (first args))))
      (let ((item (first args))
            (rev (second args))
            (kwargs (cddr args)))
        (apply #'build-decision-packet :item item :revision rev kwargs))
      (apply #'build-decision-packet args)))

(defun supersede-decision-packet (packet new-revision &key head delta rules-touched
                                                         evidence-pointers links
                                                         (additional-findings '()))
  "A newer revision supersedes the packet, keeping its open findings."
  (make-decision-packet
   :item (decision-packet-item packet)
   :revision new-revision
   :reader (decision-packet-reader packet)
   :head (or head (decision-packet-head packet))
   :recorded-head (or (decision-packet-head packet) (decision-packet-recorded-head packet))
   :delta delta
   :rules-touched (or rules-touched (decision-packet-rules-touched packet))
   :open-findings (append (decision-packet-open-findings packet) additional-findings)
   :evidence-pointers (or evidence-pointers (decision-packet-evidence-pointers packet))
   :links (or links (decision-packet-links packet))
   :whole-diff-p nil
   :amended-p nil))

(defun amend-decision-packet (packet &key delta rules-touched open-findings
                                         evidence-pointers links)
  "While the reader is busy, the packet is amended in place, not duplicated."
  (when delta (setf (decision-packet-delta packet) delta))
  (when rules-touched (setf (decision-packet-rules-touched packet) rules-touched))
  (when open-findings (setf (decision-packet-open-findings packet) open-findings))
  (when evidence-pointers (setf (decision-packet-evidence-pointers packet) evidence-pointers))
  (when links (setf (decision-packet-links packet) links))
  (setf (decision-packet-amended-p packet) t)
  packet)
