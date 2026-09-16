;;;; replays-packets.lisp --- the pure packets, refusals, envelopes and claims
;;;; that answer the eight packet replays of docs/SPEC-WORK.md:4489-4496.
;;;;
;;;; Nothing here touches a node, a journal or a socket. It is the pure core of
;;;; packet delivery and result bookkeeping: the smallest sufficient decision
;;;; packet for one reader about one item revision, the named refusal that
;;;; survives the hop as a refusal rather than silence, the envelope that copies
;;;; a child's evidence byte-for-byte up, and the claim the parent verifies with
;;;; its own criteria before it moves.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; decision packets (SPEC-WORK.md:4489-4490)
;;; ------------------------------------------------------------------

(defstruct (decision-packet (:constructor make-decision-packet
                     (&key item revision delta rules findings behaviour
                           source-links whole-p)))
  item        ; string id
  revision    ; the item's revision this packet speaks for
  delta       ; the delta since this reader's recorded head
  rules       ; the rules the delta touches
  findings    ; open findings, each (:id ... :disposition ...)
  behaviour   ; the new behaviour, with (:evidence ...) pointers
  source-links ; links to the full sources
  whole-p)    ; T when the reader has never read the entry: the whole diff goes

(defstruct (packet-store (:constructor make-packet-store))
  (table (make-hash-table :test #'equal)))

(defun record-packet (store packet)
  "One packet per item and revision. A repeat of the same item and revision
  replaces the existing slot rather than adding a second packet."
  (setf (gethash (cons (decision-packet-item packet) (decision-packet-revision packet))
                 (packet-store-table store))
        packet)
  store)

(defun packet-count (store)
  (hash-table-count (packet-store-table store)))

(defun find-packets (store item)
  (loop for p being the hash-values of (packet-store-table store)
        when (equal item (decision-packet-item p)) collect p))

(defun supersede-packet (store new-packet)
  "A newer revision supersedes the item's older packets, keeping their open
  findings. The older packets are removed from the store; their open findings
  (disposition :open) are carried forward onto the new packet, before its own."
  (let ((carried '()))
    (dolist (p (find-packets store (decision-packet-item new-packet)))
      (when (< (decision-packet-revision p) (decision-packet-revision new-packet))
        (dolist (f (decision-packet-findings p))
          (when (eq :open (getf f :disposition))
            (push f carried)))
        (remhash (cons (decision-packet-item p) (decision-packet-revision p))
                 (packet-store-table store))))
    (when carried
      (setf (decision-packet-findings new-packet)
            (append (nreverse carried) (decision-packet-findings new-packet))))
    (record-packet store new-packet))
  new-packet)

(defun amend-packet (store packet &key delta findings behaviour)
  "While the reader is busy the packet is amended in place: the same single
  packet, never a duplicate. DELTA and FINDINGS append; BEHAVIOUR replaces."
  (declare (ignore store))
  (when delta (setf (decision-packet-delta packet) (append (decision-packet-delta packet) delta)))
  (when findings (setf (decision-packet-findings packet) (append (decision-packet-findings packet) findings)))
  (when behaviour (setf (decision-packet-behaviour packet) behaviour))
  packet)

(defun pulse (store packets)
  "An empty pulse wakes no model and re-executes nothing."
  (declare (ignore store))
  (if (null packets)
      (values :no-op 0 0)
      (values :pulsed (length packets) 0)))

;;; ------------------------------------------------------------------
;;; the reader and the smallest sufficient packet (SPEC-WORK.md:4490)
;;; ------------------------------------------------------------------

(defstruct (reader (:constructor make-reader (&key id recorded-head busy-p)))
  id recorded-head busy-p)

(defun build-packet (reader &key item revision delta rules findings behaviour source-links)
  "The packet is the delta, the rules it touches, the open findings with
  dispositions, the new behaviour with evidence pointers, and links to the full
  sources; the whole diff only when this reader has never read the entry."
  (make-decision-packet :item item :revision revision
               :delta delta :rules rules :findings findings
               :behaviour behaviour :source-links source-links
               :whole-p (null (reader-recorded-head reader))))

;;; ------------------------------------------------------------------
;;; results, verdicts and their durable home (SPEC-WORK.md:4491)
;;; ------------------------------------------------------------------

(defstruct (worker-result (:constructor make-worker-result
                             (&key reader sha gate outcome)))
  reader sha gate outcome)

(defstruct (verdict (:constructor make-verdict
                      (&key reader sha base head integration)))
  reader sha base head integration)

(defstruct (verdict-home (:constructor make-verdict-home))
  (table (make-hash-table :test #'equal)))

(defun book-verdict (home verdict &key (via :coordinator))
  "A verdict is keyed (reader, sha) with a gate (base, head, integration) in one
  durable home. An independent review (:via :independent) is booked as
  independent, never re-routed through the coordinator. A receipt of a receipt
  -- the same (reader, sha) again -- is refused as a duplicate."
  (let* ((key (list (verdict-reader verdict) (verdict-sha verdict)))
         (table (verdict-home-table home)))
    (if (gethash key table)
        (values nil :duplicate)
        (progn (setf (gethash key table) verdict)
               (values t via)))))

(defun verdict-lookup (home reader sha)
  (gethash (list reader sha) (verdict-home-table home)))

;;; ------------------------------------------------------------------
;;; the named refusal (SPEC-WORK.md:4492)
;;; ------------------------------------------------------------------

(defstruct (refusal (:constructor make-refusal (&key kind reason revision node)))
  kind reason revision node)

(defun hop (kind reason revision &optional node)
  "A decline, refused offer, excluded route, asleep recipient, tripped node or
  effort limit reaches the parent as a named refusal with its reason and
  revision -- never as silence, never as success."
  (make-refusal :kind kind :reason reason :revision revision :node node))

;;; ------------------------------------------------------------------
;;; the envelope (SPEC-WORK.md:4493)
;;; ------------------------------------------------------------------

(defstruct (envelope (:constructor make-envelope
                       (&key child-id verdict result-pointer evidence-events
                             usage-pointer head learning)))
  child-id verdict result-pointer evidence-events usage-pointer head learning)

(defun send-up (child)
  "The child's verdict, result pointer, evidence events, usage pointer and exact
  head arrive byte-copied by machinery, beside the child's distilled learning in
  its own words."
  (make-envelope :child-id (envelope-child-id child)
                 :verdict (envelope-verdict child)
                 :result-pointer (envelope-result-pointer child)
                 :evidence-events (copy-list (envelope-evidence-events child))
                 :usage-pointer (envelope-usage-pointer child)
                 :head (envelope-head child)
                 :learning (envelope-learning child)))

(defun open-child-evidence (up)
  "Open the child's evidence from the envelope: the parent can find what the
  summary dropped."
  (envelope-evidence-events up))

;;; ------------------------------------------------------------------
;;; claims and tier finality (SPEC-WORK.md:4494)
;;; ------------------------------------------------------------------

(defstruct (claim (:constructor make-claim (&key node outcome by evidence)))
  node outcome by evidence)

(defun verify-claim (claim criteria)
  "The parent moves only after its own verification with evidence bound to its
  own criteria. A child's done is a claim, not a fact: it verifies only when the
  criteria are satisfied, and a criteria that requires evidence never passes on
  a claim with no evidence."
  (and (eq :done (claim-outcome claim))
       (if (getf criteria :require-evidence)
           (and (listp (claim-evidence claim)) (plusp (length (claim-evidence claim))))
           t)))

(defun tier-move (&key tier seat success-only)
  "A worker's bare success claim alone never moves a node on any tier below the
  seat; the seat itself moves only on its own verified (non-success-only)
  decision."
  (and (eq tier seat) (not success-only)))

;;; ------------------------------------------------------------------
;;; escalation (SPEC-WORK.md:4495)
;;; ------------------------------------------------------------------

(defstruct (escalation (:constructor make-escalation
                          (&key kind reason revision coordinator-reason)))
  kind reason revision coordinator-reason)

(defun escalate (kind reason revision &key coordinator-reason)
  "A hold, question or exception the child cannot decide rises as a packet with
  its reason and revision; an :effort widening or expensive-route exception
  carries the coordinator's recorded reason."
  (make-escalation :kind kind :reason reason :revision revision
                   :coordinator-reason coordinator-reason))

(defun stale-pass-line (&key age reread escalation)
  "The stale pass prints escalated-age= and reread= as information and
  reassigns nothing."
  (declare (ignore escalation))
  (values (format nil "escalated-age=~A reread=~A" age reread) nil))

;;; ------------------------------------------------------------------
;;; the parent and its outstanding children (SPEC-WORK.md:4496)
;;; ------------------------------------------------------------------

(defstruct (parent (:constructor make-parent (&key id children issue-mapping)))
  id children issue-mapping (status (make-hash-table :test #'equal)))

(defun observe-child (parent child-id outcome)
  (setf (gethash child-id (parent-status parent)) outcome)
  parent)

(defun parent-closed-p (parent)
  (and (parent-children parent)
       (every (lambda (id) (eq :done (gethash id (parent-status parent))))
              (parent-children parent))))

(defun parent-outstanding (parent)
  (count-if (lambda (id) (not (eq :done (gethash id (parent-status parent)))))
            (parent-children parent)))

(defun outstanding-line (parent)
  (format nil "outstanding=~D" (parent-outstanding parent)))

(defun issue-open-p (parent issue-url)
  (loop for (child . url) in (parent-issue-mapping parent)
        thereis (and (equal url issue-url)
                     (not (eq :done (gethash child (parent-status parent)))))))
