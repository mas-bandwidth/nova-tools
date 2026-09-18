;;;; replays-8648.lisp --- the pure model behind the five acceptance replays
;;;; named by SPEC-WORK.md:
;;;;
;;;;   regression-opens-repair-work                 :4943-4951,5782-5785
;;;;   reply-retired-only-under-verified-coverage   :6020-6024,6316-6325
;;;;   restore-is-isolated-and-dispatches-nothing   :6285-6290,5790-5793
;;;;   reuse-only-valid-review                      :4851
;;;;   review-cycles-stay-visible                   :6368-6370
;;;;
;;;; Nothing here starts a session, a restore, a dispatch or a review process:
;;;; the live-session, savepoint and CLI wiring a later slice owns is not in
;;;; this slice. These are the data and the verdicts the replays assert.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; A confirmed regression opens linked repair work
;;; (SPEC-WORK.md:4943-4951,5782-5785)
;;; ------------------------------------------------------------------

(defstruct (verification-summary
             (:constructor make-verification-summary
                 (&key node pinned-revision current-revision (state :verified)
                       criteria dependencies history)))
  "The two records joined by stable id: the historic tick pinned at the
revision it ran against, and the current verification summary, which a changed
source, criterion or dependency invalidates without erasing either."
  node pinned-revision current-revision state criteria dependencies history)

(defun invalidate-verification (summary new-revision &key reason)
  "A changed source, criterion or dependency moves the current summary to
:recheck-needed and the pinned historic tick is retained untouched. REASON is
accepted for the caller's provenance and does not alter the retained record."
  (declare (ignore reason))
  (make-verification-summary
   :node (verification-summary-node summary)
   :pinned-revision (verification-summary-pinned-revision summary)
   :current-revision new-revision
   :state :recheck-needed
   :criteria (verification-summary-criteria summary)
   :dependencies (verification-summary-dependencies summary)
   :history (verification-summary-history summary)))

(defun recheck-needed-p (summary)
  "The answer a changed source or criterion gives: a recheck is needed."
  (eq :recheck-needed (verification-summary-state summary)))

(defstruct (repair-work
             (:constructor make-repair-work (&key id node (state :todo))))
  "A repair item created under the ordinary policy: open (:todo) and linked to
the regressed node. The closed node stays closed; this is new work, not a
silent reopen."
  id node state)

(defun confirm-regression (summary &key id)
  "A confirmed regression creates linked open repair work under the ordinary
policy; the historic tick and the closed node are left as they are."
  (make-repair-work :id (or id (format nil "~A-repair" (verification-summary-node summary)))
                    :node (verification-summary-node summary)
                    :state :todo))

;;; ------------------------------------------------------------------
;;; A reply retires only under verified coverage
;;; (SPEC-WORK.md:6020-6024,6316-6325)
;;; ------------------------------------------------------------------

(defstruct (retained-disposition
             (:constructor make-retained-disposition
                 (&key request payload-digest sequence record-hash reply boundary)))
  "A request's identity, its accepted record's sequence and hash and the
original reply, kept in the image even when its events lie before the cut."
  request payload-digest sequence record-hash reply boundary)

(defstruct (coverage
             (:constructor make-coverage
                 (&key snapshot-reachable retained-events-reachable
                       dedup-root-reachable push-ok)))
  "The three reachabilities a boundary record names, plus the push that a
locally existing commit is not proof of."
  snapshot-reachable retained-events-reachable dedup-root-reachable push-ok)

(defun coverage-verified-p (coverage)
  "A disposition is retired only once the committed snapshot, its retained
events and the dedup root are verified reachable and the push succeeded."
  (and (coverage-snapshot-reachable coverage)
       (coverage-retained-events-reachable coverage)
       (coverage-dedup-root-reachable coverage)
       (coverage-push-ok coverage)))

(defun retire-reply (disposition coverage)
  "Retire a retained disposition to the dedup index's `already applied` only
under verified coverage. Otherwise the original OK still answers from the
retained disposition, and an unreadable dedup root prints the coverage gap. A
reply is never deleted, invented or reconstructed from current state."
  (if (coverage-verified-p coverage)
      (values :already-applied "already applied" nil)
      (values :retained
              (retained-disposition-reply disposition)
              (unless (coverage-dedup-root-reachable coverage)
                (list "recovery-gap kind=coverage-unverified")))))

;;; ------------------------------------------------------------------
;;; A restore is isolated and dispatches nothing
;;; (SPEC-WORK.md:6285-6290,5790-5793)
;;; ------------------------------------------------------------------

(defstruct (restore-session
             (:constructor make-restore-session
                 (&key savepoint (read-only-p t) ownership assignments
                       (dispatch-count 0) replayed-messages external-effects
                       (mode :isolated))))
  "A restore opens a read-only, isolated, non-dispatching recovery session: it
inherits no coordinator ownership, reanimates no assignment, replays no bus
message and duplicates no external side effect."
  savepoint read-only-p ownership assignments dispatch-count replayed-messages
  external-effects mode)

(defun restore-dispatch (session &key node)
  "The isolated restore refuses to dispatch anything."
  (declare (ignore session node))
  (values nil "RESTORE FAIL: read-only isolated session dispatches nothing" 2))

(defun promote-repair (session repair &key fenced validated reconciled)
  "A selected repair is promoted only through a fenced validated reconciliation
with the current state."
  (declare (ignore session repair))
  (if (and fenced validated reconciled)
      (values t "REPAIR OK" 0)
      (values nil "REPAIR FAIL: requires fenced validated reconciliation" 2)))

;;; ------------------------------------------------------------------
;;; Same-scope review is reusable; only validly
;;; (SPEC-WORK.md:4851)
;;; ------------------------------------------------------------------

(defstruct (scope-review
             (:constructor make-scope-review
                 (&key friend scope head acceptance dependencies
                       (independent-gate-p nil) verdict)))
  friend scope head acceptance dependencies independent-gate-p verdict)

(defun scope-review-reusable-p (review &key scope head acceptance dependencies)
  "A same-scope review is reusable while its head, acceptance and dependencies
are unchanged; an independent friend gate cannot be replaced by reuse."
  (and (not (scope-review-independent-gate-p review))
       (equal (scope-review-scope review) scope)
       (equal (scope-review-head review) head)
       (equal (scope-review-acceptance review) acceptance)
       (equal (scope-review-dependencies review) dependencies)))

;;; ------------------------------------------------------------------
;;; Review and repair cycles stay visible as work and as cost
;;; (SPEC-WORK.md:6368-6370)
;;; ------------------------------------------------------------------

(defstruct (review-cycle
             (:constructor make-review-cycle
                 (&key friend revision finding-ids dispositions clearance
                       evidence-reused-p reread-delta (cost 0))))
  "One round: the friend, the exact revision, the finding ids, the author's
dispositions, the clearance, whether valid evidence was reused and the affected
delta reread, and the operational cost."
  friend revision finding-ids dispositions clearance evidence-reused-p
  reread-delta cost)

(defstruct (review-ledger
             (:constructor make-review-ledger (&key (cycles '()))))
  cycles)

(defun record-review-cycle (ledger cycle)
  "Append one review or repair cycle to the ledger, keeping the sequence."
  (make-review-ledger :cycles (append (review-ledger-cycles ledger) (list cycle))))

(defun review-ledger-covers-friends-p (ledger friends)
  "True when every required friend has a recorded cycle carrying its exact
revision, its finding ids and the author's dispositions."
  (every (lambda (friend)
           (let ((cycles (remove-if-not (lambda (c) (equal friend (review-cycle-friend c)))
                                        (review-ledger-cycles ledger))))
             (and cycles
                  (every #'review-cycle-revision cycles)
                  (some #'review-cycle-finding-ids cycles)
                  (some #'review-cycle-dispositions cycles))))
         friends))

(defun review-ledger-cycle-count (ledger)
  "The repeated cycles visible as work."
  (length (review-ledger-cycles ledger)))

(defun review-ledger-total-cost (ledger)
  "The repeated cycles visible as operational cost."
  (reduce #'+ (review-ledger-cycles ledger) :key #'review-cycle-cost :initial-value 0))
