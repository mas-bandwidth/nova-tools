;;;; assignment.lisp --- the assignment and execution-control book: offer,
;;;; acknowledge (received / accepted), decline, the offer deadline and the
;;;; receipt ledger (docs/SPEC-WORK.md:3835-3919).
;;;;
;;;; Slice 1's C/O kernel has the three transition verbs only. This file is the
;;;; pure booking layer the five assignment acceptance replays drive: an
;;;; admitted offer writes :effect :dispatched, a pending-offer entry under the
;;;; node and the friend, and a reservation keyed by (offer, attempt); an
;;;; acceptance after a verified received creates exactly one lease or binds to
;;;; the holder's own; --until leaves an offer overdue and unreconciled; a late
;;;; or duplicate receipt is retained. The live session, verifier, transport and
;;;; CLI wiring a later slice owns stays out; the tests exercise the pure book
;;;; directly, exactly as the applicable/delegation slice does.
;;;;
;;;; The book is a value: every function copies it and returns the new one, so
;;;; "the offer leaves node state, W, the lease index, attempts, evidence and
;;;; completion unchanged" is a comparison and not a convention.

(in-package #:nova-work)

(defstruct (leasebook
            (:constructor make-leasebook
                (&key (nodes '()) (w '()) (leases '()) (holders '()) (pending '())
                      (reservations '()) (evidence '()) (completion '())
                      (receipts '()) (requests '()) (generation '())
                      (observed-models '()) (effects '()) (bindings '())
                      (index-node '()) (index-friend '())))
            (:conc-name leasebook-))
  nodes            ; alist node -> its C/O state, read-only here
  w                ; alist node -> holder; one entry is one W membership
  leases           ; alist node -> (:holder :deadline :default)
  holders          ; alist node -> the current or proposed holder
  pending          ; alist offer -> the pinned offer tuple and its state
  reservations     ; alist (offer . attempt) -> (:reserve :state)
  evidence         ; the evidence index, read-only here
  completion       ; the completion index, read-only here
  receipts         ; alist receipt-id -> (:digest :effect :request)
  requests         ; alist request -> its recorded disposition
  generation       ; alist node -> current generation
  observed-models  ; alist node -> the observed model, or :unknown
  effects          ; alist (offer . attempt) -> newest effect
  bindings         ; alist (offer . attempt) -> the lease it bound to
  index-node       ; alist node -> offers, the pending-offer index under the node
  index-friend)    ; alist friend -> offers, the pending-offer index under the friend

(defun %lget (key alist)
  (cdr (assoc key alist :test #'equal)))

(defun %lput (key value alist)
  (acons key value (remove key alist :key #'car :test #'equal)))

(defun %pending-update (book offer update)
  (let ((nbk (copy-leasebook book))
        (entry (%lget offer (leasebook-pending book))))
    (setf (leasebook-pending nbk) (%lput offer (funcall update entry) (leasebook-pending book)))
    nbk))

(defmacro %refuse (word control &rest args)
  "A refusal writes nothing: the untouched BOOK is returned beside the nil."
  `(values nil (format nil "~A FAIL: ~A" ,word (format nil ,control ,@args)) nil book))

;;; ------------------------------------------------------------------
;;; offer (SPEC-WORK.md:3861-3888)
;;; ------------------------------------------------------------------

(defun leasebook-offer (book &key offer node generation attempt to profile reserve until
                                     free-slots request request-ref payload-sha256
                                     staged-digest predecessor-offer predecessor-attempt
                                     predecessor-node requested-model held profile-ok
                                     current-generation)
  "Admit one offer. An admitted offer writes :effect :dispatched, a pending-offer
entry under the node and the friend, and a reservation keyed by (offer, attempt).
It writes no node state, no W entry, no lease, no observed attempt, no evidence
and no completion. A refusal writes nothing."
  (declare (ignore request-ref requested-model))
  (cond
    ((or (null offer) (and (stringp offer) (string= offer ""))) (%refuse "OFFER" "malformed offer id"))
    ((or (null node) (and (stringp node) (string= node ""))) (%refuse "OFFER" "malformed node"))
    ((or (null to) (and (stringp to) (string= to ""))) (%refuse "OFFER" "malformed recipient"))
    ((or (null attempt) (and (stringp attempt) (string= attempt ""))) (%refuse "OFFER" "malformed attempt"))
    ((or (null profile) (and (stringp profile) (string= profile ""))) (%refuse "OFFER" "malformed profile"))
    ((not (and (integerp reserve) (plusp reserve))) (%refuse "OFFER" "reservation is not positive"))
    ((and free-slots (> reserve free-slots))
     (%refuse "OFFER" "reservation ~D exceeds declared free capacity ~D" reserve free-slots))
    (held (%refuse "OFFER" "effective hold on the scope"))
    ((eq profile-ok nil) (%refuse "OFFER" "profile is not that friend's at that revision"))
    ((and payload-sha256 staged-digest (not (equal payload-sha256 staged-digest)))
     (%refuse "OFFER" "staged payload digest is not --payload-sha256"))
    ((and current-generation (not (equal current-generation generation)))
     (%refuse "OFFER" "stale generation"))
    ((and predecessor-offer predecessor-node (not (equal predecessor-node node)))
     (%refuse "OFFER" "predecessor is not this node's"))
    ((%lget offer (leasebook-pending book)) (%refuse "OFFER" "reused offer id ~A" offer))
    ((find attempt (leasebook-pending book)
           :key (lambda (p) (getf (cdr p) :attempt)))
     (%refuse "OFFER" "reused attempt id ~A" attempt))
    ((find-if (lambda (p) (and (equal (getf (cdr p) :node) node)
                              (eq (getf (cdr p) :state) :overdue)))
              (leasebook-pending book))
     (%refuse "OFFER" "an overdue unreconciled offer admits no new offer"))
    (t
     (let* ((holder (%lget node (leasebook-holders book)))
            (entry (list :offer offer :node node :to to :attempt attempt
                         :generation generation :profile profile :reserve reserve
                         :until until :state :pending :received nil)))
       (when (and holder (not (equal holder to)))
         (return-from leasebook-offer
           (%refuse "OFFER" "cross-holder: ~A holds ~A; an offer cannot create a shadow lease"
                    holder node)))
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-holders nbk)
               (if holder (leasebook-holders book) (%lput node to (leasebook-holders book))))
         (setf (leasebook-pending nbk) (%lput offer entry (leasebook-pending book)))
         (setf (leasebook-reservations nbk)
               (%lput (cons offer attempt) (list :reserve reserve :state :held)
                      (leasebook-reservations book)))
         (setf (leasebook-effects nbk)
               (%lput (cons offer attempt) :dispatched (leasebook-effects book)))
         (setf (leasebook-index-node nbk)
               (%lput node (append (%lget node (leasebook-index-node book)) (list offer))
                      (leasebook-index-node book)))
         (setf (leasebook-index-friend nbk)
               (%lput to (append (%lget to (leasebook-index-friend book)) (list offer))
                      (leasebook-index-friend book)))
         (values t (format nil "OFFER OK offer=~A node=~A" offer node) :dispatched nbk))))))

;;; ------------------------------------------------------------------
;;; acknowledge --stage received (SPEC-WORK.md:3874-3875)
;;; ------------------------------------------------------------------

(defun leasebook-received (book &key offer node generation attempt receipt-digest
                                      receipt-id request verifier profile payload-sha256)
  "Admit delivery only: a verified report that the named recipient received that
exact offer. Consents to nothing: no lease, no W, no conversion, no release."
  (declare (ignore receipt-digest profile payload-sha256))
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "ACKNOWLEDGE" "no such offer ~A" offer))
      ((null verifier) (%refuse "ACKNOWLEDGE" "provenance unverified"))
      ((not (equal (getf entry :node) node)) (%refuse "ACKNOWLEDGE" "offer is for another node"))
      ((not (equal (getf entry :generation) generation))
       (%refuse "ACKNOWLEDGE" "offer is for another generation"))
      ((not (equal (getf entry :attempt) attempt)) (%refuse "ACKNOWLEDGE" "offer is for another attempt"))
      (t
       (let ((nbk (%pending-update book offer
                                   (lambda (e) (list* :received t e)))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :received (leasebook-effects book)))
         (when receipt-id
           (setf (leasebook-receipts nbk)
                 (%lput receipt-id (list :digest receipt-digest :effect :received :request request)
                        (leasebook-receipts book))))
         (values t (format nil "ACKNOWLEDGE OK stage=received offer=~A" offer) :received nbk))))))

;;; ------------------------------------------------------------------
;;; acknowledge --stage accepted (SPEC-WORK.md:3890-3903)
;;; ------------------------------------------------------------------

(defun leasebook-accepted (book &key offer node generation attempt by default deadline
                                        observed-model receipt-digest receipt-id request)
  "Accept ownership. With no live lease the one accepted envelope creates one
canonical :lease and one W entry and converts the reservation to committed; with
the holder's own live lease it binds the assignment and changes neither deadline
nor default. A cross-holder acceptance or a late one creates no lease."
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "ACKNOWLEDGE" "no such offer ~A" offer))
      ((not (getf entry :received)) (%refuse "ACKNOWLEDGE" "no earlier verified :received on the offer"))
      ((not (equal (getf entry :generation) generation))
       (%refuse "ACKNOWLEDGE" "generation changed"))
      ((member (getf entry :state) '(:declined :accepted :overdue :late :replaced))
       (%late-accept book offer attempt receipt-digest receipt-id request))
      (t
       (let* ((lease (%lget node (leasebook-leases book)))
              (holder (and lease (getf lease :holder))))
         (cond
           ((and lease (not (equal holder by)))
            (%late-accept book offer attempt receipt-digest receipt-id request))
           (t
            (let ((nbk (%pending-update book offer
                                        (lambda (e) (list* :state :accepted e)))))
              (setf (leasebook-reservations nbk)
                    (let ((key (cons offer attempt)))
                      (%lput key (list* :state :committed (%lget key (leasebook-reservations book)))
                             (leasebook-reservations book))))
              (if lease
                  ;; Bind to the holder's own lease; deadline and default are not fields of the binding.
                  (setf (leasebook-bindings nbk)
                        (%lput (cons offer attempt) (getf lease :holder) (leasebook-bindings book)))
                  (progn
                    (setf (leasebook-leases nbk)
                          (%lput node (list :holder by :deadline deadline :default default)
                                 (leasebook-leases book)))
                    (setf (leasebook-w nbk) (%lput node by (leasebook-w book)))))
              (setf (leasebook-observed-models nbk)
                    (%lput node (if observed-model observed-model :unknown)
                           (leasebook-observed-models book)))
              (setf (leasebook-effects nbk) (%lput (cons offer attempt) :accepted (leasebook-effects book)))
              (when receipt-id
                (setf (leasebook-receipts nbk)
                      (%lput receipt-id (list :digest receipt-digest :effect :accepted :request request)
                             (leasebook-receipts book))))
              (values t (format nil "ACKNOWLEDGE OK stage=accepted offer=~A lease=~A"
                                offer (if lease holder by))
                      :accepted nbk)))))))))

(defun %late-accept (book offer attempt receipt-digest receipt-id request)
  "A late acceptance is retained :late: it revives no lease, converts nothing,
releases nothing and overwrites no successor."
  (let ((nbk (%pending-update book offer (lambda (e) (list* :state :late e)))))
    (setf (leasebook-effects nbk) (%lput (cons offer attempt) :late (leasebook-effects book)))
    (when receipt-id
      (setf (leasebook-receipts nbk)
            (%lput receipt-id (list :digest receipt-digest :effect :late :request request)
                   (leasebook-receipts book))))
    (values t (format nil "ACKNOWLEDGE OK stage=accepted late offer=~A" offer) :late nbk)))

;;; ------------------------------------------------------------------
;;; decline (SPEC-WORK.md:3910-3912)
;;; ------------------------------------------------------------------

(defun leasebook-decline (book &key offer node generation attempt receipt-id receipt-digest request)
  (declare (ignore node generation))
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "DECLINE" "no such offer ~A" offer))
      ((eq :pending (getf entry :state))
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :declined e)))))
         (setf (leasebook-reservations nbk)
               (let ((key (cons offer attempt)))
                 (%lput key (list* :state :released (%lget key (leasebook-reservations book)))
                        (leasebook-reservations book))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :declined (leasebook-effects book)))
         (values t (format nil "DECLINE OK offer=~A" offer) :declined nbk)))
      (t
       ;; A decline after acceptance or over an uncertain execution is :late and
       ;; releases neither committed nor uncertain capacity.
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :late e)))))
         (setf (leasebook-effects nbk) (%lput (cons offer attempt) :late (leasebook-effects book)))
         (values t (format nil "DECLINE OK late offer=~A" offer) :late nbk))))))

;;; ------------------------------------------------------------------
;;; --until and lease expiry (SPEC-WORK.md:3905-3910)
;;; ------------------------------------------------------------------

(defun lease-until (book &key offer now)
  "At --until an unanswered offer is overdue and unreconciled: no new offer and no
automatic launch is admitted for it and the reservation stands."
  (let ((entry (%lget offer (leasebook-pending book))))
    (cond
      ((null entry) (%refuse "LEASE" "no such offer ~A" offer))
      ((and (getf entry :until) now (string< (getf entry :until) now))
       (let ((nbk (%pending-update book offer (lambda (e) (list* :state :overdue e)))))
         (values t (format nil "LEASE OK offer=~A overdue unreconciled" offer) :overdue nbk)))
      (t (values t (format nil "LEASE OK offer=~A until not reached" offer) :pending book)))))

(defun leasebook-expire (book &key node now)
  "Lease expiry takes the task out of W and leaves the friend's ACTIVE capacity
and any uncertain execution retained; it claims no stop and no completion."
  (declare (ignore now))
  (let ((lease (%lget node (leasebook-leases book))))
    (cond
      ((null lease) (%refuse "LEASE" "no live lease on ~A" node))
      (t
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-w nbk) (remove node (leasebook-w book) :key #'car :test #'equal))
         (setf (leasebook-leases nbk)
               (%lput node (list* :expired t (leasebook-leases book)) (leasebook-leases book)))
         (values t (format nil "LEASE OK node=~A expired" node) :expired nbk))))))

;;; ------------------------------------------------------------------
;;; receipts: late, duplicate, conflicting (SPEC-WORK.md:3912-3919)
;;; ------------------------------------------------------------------

(defun leasebook-receipt (book &key receipt-id receipt-digest request stage
                                     offer node generation attempt by default deadline
                                     observed-model verifier)
  "The receipt ledger. The same request replays its recorded disposition; the
same verified receipt under a fresh request journals one no-effect :duplicate and
consumes no capacity twice; conflicting bytes for one receipt id are refused."
  (let ((seen (%lget receipt-id (leasebook-receipts book))))
    (cond
      ((and seen (not (equal (getf seen :digest) receipt-digest)))
       (%refuse "ACKNOWLEDGE" "conflicting bytes for receipt id ~A" receipt-id))
      ((and seen request (equal request (getf seen :request)))
       (values t (format nil "ACKNOWLEDGE OK replay receipt=~A" receipt-id)
               (getf seen :effect) book))
      (seen
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-requests nbk)
               (%lput request (list :effect :duplicate) (leasebook-requests book)))
         (when (null (%lget request (leasebook-requests book)))
           (setf (leasebook-receipts nbk)
                 (%lput receipt-id (list :digest receipt-digest :effect :duplicate :request request)
                        (leasebook-receipts book))))
         (values t (format nil "ACKNOWLEDGE OK duplicate receipt=~A" receipt-id) :duplicate nbk)))
      (t
       (let ((nbk (copy-leasebook book)))
         (setf (leasebook-receipts nbk)
               (%lput receipt-id (list :digest receipt-digest :effect :pending :request request)
                      (leasebook-receipts book)))
         (ecase stage
           (:received
            (multiple-value-bind (okp line effect rbk)
                (leasebook-received nbk :offer offer :node node :generation generation
                                    :attempt attempt :receipt-digest receipt-digest
                                    :receipt-id receipt-id :request request :verifier verifier)
              (values okp line effect rbk)))
           (:accepted
            (multiple-value-bind (okp line effect rbk)
                (leasebook-accepted nbk :offer offer :node node :generation generation
                                    :attempt attempt :by by :default default
                                    :deadline deadline :observed-model observed-model
                                    :receipt-digest receipt-digest :receipt-id receipt-id
                                    :request request)
              (values okp line effect rbk)))))))))
