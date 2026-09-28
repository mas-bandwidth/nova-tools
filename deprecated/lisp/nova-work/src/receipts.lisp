;;;; receipts.lisp --- the receipt ledger and reply retirement of the accepted
;;;; requests whose events lie before the clip boundary (SPEC-WORK.md:6020-6024,
;;;; 6316-6325).
;;;;
;;;; A retry is answered `already applied` only once the committed snapshot,
;;;; its retained events and the dedup root that the boundary record names are
;;;; verified reachable -- including that the root the retry observes is the
;;;; bytes the boundary names -- and the push succeeded. Until then the original
;;;; `OK` still answers from the retained disposition or
;;;; `recovery-gap kind=coverage-unverified` prints, and a reply is never
;;;; deleted, invented or reconstructed from current state.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The receipt ledger
;;; ------------------------------------------------------------------

(defstruct (retained-disposition
             (:constructor make-retained-disposition
                 (&key request payload-digest sequence record-hash reply boundary)))
  "A request's identity, its accepted record's sequence and hash and the
original reply, kept in the image even when its events lie before the cut."
  request payload-digest sequence record-hash reply boundary)

(defstruct (receipt-ledger
             (:constructor make-receipt-ledger (&key (entries '()))))
  "The accepted dispositions the session keeps by request id, in acceptance
order. Nothing here is evicted by a clip: the ledger is what answers a retry
that lands past the journal and into the dedup index."
  entries)

(defun receipt-ledger-record (ledger request disposition)
  "Record one accepted request's disposition, keeping acceptance order. A
record under a request the ledger already holds is left as it was, so a retry
never rewrites the disposition it is answered from."
  (if (assoc request (receipt-ledger-entries ledger) :test #'equal)
      ledger
      (make-receipt-ledger
       :entries (append (receipt-ledger-entries ledger)
                        (list (cons request disposition))))))

(defun receipt-ledger-lookup (ledger request)
  "The disposition the ledger keeps for REQUEST, or NIL."
  (cdr (assoc request (receipt-ledger-entries ledger) :test #'equal)))

(defun receipt-ledger-retains-p (ledger request)
  "True while the ledger still holds REQUEST's disposition. Retirement never
deletes the ledger entry."
  (not (null (receipt-ledger-lookup ledger request))))

;;; ------------------------------------------------------------------
;;; Verified coverage
;;; ------------------------------------------------------------------

(defstruct (coverage
             (:constructor make-coverage
                 (&key snapshot-reachable retained-events-reachable
                       dedup-root-reachable push-ok
                       boundary-root-digest observed-root-digest
                       boundary-sequence observed-sequence)))
  "The three reachabilities a boundary record names, plus the push that a
locally existing commit is not proof of. Where the boundary's dedup root
digest and the retry's observed root digest are both known, they must name the
same bytes; likewise for the retained sequence."
  snapshot-reachable retained-events-reachable dedup-root-reachable push-ok
  boundary-root-digest observed-root-digest boundary-sequence observed-sequence)

(defun coverage-root-matches-p (coverage)
  "The dedup root a retry observes must be the one the boundary record names."
  (or (null (coverage-boundary-root-digest coverage))
      (null (coverage-observed-root-digest coverage))
      (equal (coverage-boundary-root-digest coverage)
             (coverage-observed-root-digest coverage))))

(defun coverage-sequence-matches-p (coverage)
  "The retained-event sequence a retry observes must reach the boundary."
  (or (null (coverage-boundary-sequence coverage))
      (null (coverage-observed-sequence coverage))
      (<= (coverage-boundary-sequence coverage)
          (coverage-observed-sequence coverage))))

(defun coverage-verified-p (coverage)
  "A disposition is retired only once the committed snapshot, its retained
events and the dedup root are verified reachable -- and are the bytes the
boundary names -- and the push succeeded."
  (and (coverage-snapshot-reachable coverage)
       (coverage-retained-events-reachable coverage)
       (coverage-dedup-root-reachable coverage)
       (coverage-push-ok coverage)
       (coverage-root-matches-p coverage)
       (coverage-sequence-matches-p coverage)))

(defun coverage-gap (coverage)
  "The gap line a refused retirement prints, or NIL when coverage is only
unpushed but otherwise readable."
  (cond ((or (not (coverage-dedup-root-reachable coverage))
             (not (coverage-root-matches-p coverage))
             (not (coverage-sequence-matches-p coverage)))
         (list "recovery-gap kind=coverage-unverified"))
        (t nil)))

(defun retire-reply (disposition coverage)
  "Retire a retained disposition to the dedup index's `already applied` only
under verified coverage. Otherwise the original `OK` still answers from the
retained disposition, and an unreadable or mismatched dedup root prints the
coverage gap. A reply is never deleted, invented or reconstructed from current
state."
  (if (coverage-verified-p coverage)
      (values :already-applied "already applied" nil)
      (values :retained
              (retained-disposition-reply disposition)
              (coverage-gap coverage))))
