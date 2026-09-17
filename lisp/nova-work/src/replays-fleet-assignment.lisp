;;;; replays-fleet-assignment.lisp --- the pure part of the fleet recommendation
;;;; and the four assignment facts, with their verified/staged admission
;;;; (docs/SPEC-WORK.md:3557-3590, 3829-3860, 5680-5686).
;;;;
;;;; Nothing here starts a session, a launcher, a dispatch or a child, and there
;;;; is no CLI, no socket and no provider: these are the pure records and verbs
;;;; the five acceptance replays of slice 9 call. The fleet ask is a
;;;; recommendation from declared facts and never a lease; the offer,
;;;; acknowledge and decline verbs keep dispatch, delivery, accepted ownership
;;;; and refusal apart; the verifier and the staged inputs are the session's own
;;;; half and refuse an unverified provenance.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The fleet: declared facts, a recommendation, never a lease
;;; (SPEC-WORK.md:3557-3590)
;;; ------------------------------------------------------------------

(defstruct (machine (:constructor make-machine
                                  (&key id name owner connect roles permits excludes limits facts)))
  "One CONFIG `:machine` member: equipment, never a work-tree node. Its roles,
permits and excludes are declared facts; a probe or a heartbeat never writes
them."
  id name owner connect roles permits excludes limits facts)

(defstruct (fleet-session (:constructor make-fleet-session (&key who leases)))
  "The caller's view, held only so a replay can prove the ask writes nothing to
it: WHO is the caller, LEASES is the lease index."
  who leases)

(defstruct (fleet-ask (:constructor make-fleet-ask
                                   (&key kind rows fail (lease nil) (exit-code 0))))
  "The fleet ask's answer. ROWS is one row per admitting member; FAIL is the
refusal line when an excluded member was named. LEASE is always NIL: the answer
is a recommendation and never a lease (SPEC-WORK.md:3562)."
  kind rows fail lease exit-code)

(defun machine-excludes-p (machine kind)
  "A member excludes KIND when the declared :excludes names it."
  (member kind (machine-excludes machine) :test #'equal))

(defun machine-admits-p (machine kind)
  "The declared :roles and :permits admit KIND and the declared :excludes does
not (SPEC-WORK.md:3573-3576)."
  (and (not (machine-excludes-p machine kind))
       (or (member kind (machine-roles machine) :test #'equal)
           (member kind (machine-permits machine) :test #'equal))))

(defun fleet-row (machine kind)
  "One QUERY ROW: the machine id, owner, roles, limits and the declared facts
with their dates so the asker sees how old the declaration is."
  (list :id (machine-id machine)
        :name (machine-name machine)
        :owner (machine-owner machine)
        :roles (machine-roles machine)
        :limits (machine-limits machine)
        :admits kind
        :facts (machine-facts machine)
        :declared-at (mapcar (lambda (fact) (getf fact :declared-at))
                             (machine-facts machine))))

(defun fleet-for (session machines kind &key node)
  "Answer which machines admit KIND, in their configured order: their declared
:roles and :permits admit it and their :excludes do not. With NODE, narrow to
that one member; a member that excludes KIND is a refusal, never an empty answer
a caller could read as no machine and fall through. The answer is a
recommendation from declared facts and never a lease: it reserves nothing,
dispatches nothing and writes nothing into SESSION (SPEC-WORK.md:3562-3590)."
  (declare (ignore session))
  (if node
      (let ((machine (find node machines :key #'machine-id :test #'equal)))
        (cond
          ((null machine)
           (make-fleet-ask :kind kind :rows '()
                           :fail (format nil "QUERY FAIL ask=fleet rows=0 shown=0: no such member ~A"
                                         node)
                           :exit-code 1))
          ((machine-excludes-p machine kind)
           (make-fleet-ask :kind kind :rows '()
                           :fail (format nil "QUERY FAIL ask=fleet rows=0 shown=0: ~A excludes ~A"
                                         node (string-downcase (princ-to-string kind)))
                           :exit-code 1))
          (t
           (make-fleet-ask :kind kind :rows (list (fleet-row machine kind))))))
      (make-fleet-ask
       :kind kind
       :rows (mapcar (lambda (machine) (fleet-row machine kind))
                     (remove-if-not (lambda (machine) (machine-admits-p machine kind))
                                    machines)))))

;;; ------------------------------------------------------------------
;;; The four assignment facts and their verbs (SPEC-WORK.md:3829-3860, 5680)
;;; ------------------------------------------------------------------

(defstruct (assignment-state
            (:constructor make-assignment-state
                (&key who (offers '()) (deliveries '()) (acceptances '())
                      (declines '()) (reservations '()) leases (attempts '())
                      (evidence '()) (completed '()) node-state (w '())
                      responsible (free-slots 0) (launches 0))))
  "The coordinator's canonical records, one slot per fact: OFFERS is dispatch
intent, DELIVERIES is a verified received report, ACCEPTANCES is accepted
ownership, DECLINES is a verified refusal. LEASES, ATTEMPTS, EVIDENCE,
COMPLETED, NODE-STATE, W and RESPONSIBLE are held only so a verb can be proved
not to infer one fact from another. LAUNCHES counts launches; no verb here
launches anything."
  who offers deliveries acceptances declines reservations leases attempts
  evidence completed node-state w responsible free-slots launches)

(defparameter *session-written-fields* '(:sender :receipt-digest :effect)
  "The three fields the coordinator derives from a verified receipt; a plain
request carrying one is refused, because they are the session's own half of the
envelope (SPEC-WORK.md:3856).")

(defstruct (staged-input
            (:constructor make-staged-input
                (&key bytes result valid-p rev reason)))
  "Immutable staged bytes and the validation result produced outside the
mutation loop. RESULT is the verifier's (values); VALID-P is false when the
verifier could not vouch for the provenance."
  bytes result valid-p rev reason)

(defun stage-provenance (bytes verifier &key (rev 0))
  "Run the operator-configured VERIFIER over the staged provenance BYTES outside
the mutation loop. A verifier result carries the configured recipient identity,
a stable receipt id and the digest of the received bytes; a verifier outage or a
failing result is an invalid stage that admits nothing (SPEC-WORK.md:3852-3858)."
  (handler-case
      (let ((result (and verifier (funcall verifier bytes))))
        (if result
            (make-staged-input :bytes bytes :result result :valid-p t :rev rev)
            (make-staged-input :bytes bytes :result nil :valid-p nil :rev rev
                               :reason "provenance unverified")))
    (error ()
      (make-staged-input :bytes bytes :result nil :valid-p nil :rev rev
                         :reason "provenance unverified"))))

(defun session-written-field (request)
  "The first key of REQUEST that belongs to the session's own half, or NIL."
  (loop for (key value) on request by #'cddr
        when (member key *session-written-fields* :test #'equal)
          return key))

(defun assignment-offer (state &key offer-id node attempt to profile payload-sha256
                                   (reserve 0) until)
  "Write dispatch — the coordinator's intent to send a named offer — and nothing
else: no node state, no task evidence, no :attempt, no lease and no W. It writes
:effect :dispatched, one pending-offer entry and one reservation, leaving node
state, W, the lease index, attempts, evidence and completion unchanged
(SPEC-WORK.md:3833-3843, 5680)."
  (unless (and offer-id node attempt to)
    (return-from assignment-offer
      (values nil "OFFER FAIL: incomplete offer" 2)))
  (unless (plusp reserve)
    (return-from assignment-offer
      (values nil "OFFER FAIL: the reservation is not positive" 2)))
  (when (> reserve (assignment-state-free-slots state))
    (return-from assignment-offer
      (values nil (format nil "OFFER FAIL: declared free capacity does not cover ~D"
                          reserve)
              2)))
  (when (find offer-id (assignment-state-offers state)
              :key (lambda (offer) (getf offer :offer-id)) :test #'equal)
    (return-from assignment-offer
      (values nil (format nil "OFFER FAIL offer=~A: reused offer id" offer-id) 2)))
  (push (list :offer-id offer-id :node node :attempt attempt :to to :profile profile
              :payload-sha256 payload-sha256 :reserve reserve :until until
              :effect :dispatched)
        (assignment-state-offers state))
  (push (cons offer-id attempt) (assignment-state-reservations state))
  (decf (assignment-state-free-slots state) reserve)
  (values t (format nil "OFFER OK id=~A effect=dispatched" offer-id) 0))

(defun %admission-refusal (state request staged current-rev what)
  "The shared admission boundary for acknowledge and decline: a plain request
carrying the session's own half, an unverified provenance and a stage from a
conflicting revision each refuse with no canonical write."
  (declare (ignore state))
  (let ((field (and request (session-written-field request))))
    (when field
      (return-from %admission-refusal
        (values nil (format nil "~A FAIL: ~A is the session's own half of the envelope"
                            what (string-downcase (symbol-name field)))
                2))))
  (unless (and staged (staged-input-valid-p staged))
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: provenance unverified" what) 2)))
  (unless (= (staged-input-rev staged) current-rev)
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: stale stage (rev ~D, now ~D)"
                          what (staged-input-rev staged) current-rev)
              2)))
  (values t nil 0))

(defun %verified-receipt (offer-id staged effect)
  "The session's own half of the envelope, derived from the verified result:
:sender, :receipt-digest and :effect."
  (let ((result (staged-input-result staged)))
    (list :offer-id offer-id
          :sender (or (getf result :sender) (getf result :recipient))
          :receipt-id (getf result :receipt-id)
          :receipt-digest (getf result :digest)
          :effect effect)))

(defun assignment-acknowledge (state stage &key staged request offer-id
                                              (expect 0) (current-rev 0))
  "`acknowledge --stage received` writes delivery, a verified report that the
named recipient received that exact offer, and consents to nothing;
`--stage accepted` writes accepted ownership, the admission of a verified
acceptance as an assignment, changing no :responsible and proving nothing about
whether remote work began. Both admit only behind the verifier; an unverified
provenance writes nothing (SPEC-WORK.md:3835-3858)."
  (let ((what (ecase stage (:received "ACKNOWLEDGE") (:accepted "ACKNOWLEDGE"))))
    (multiple-value-bind (admitted refusal code)
        (%admission-refusal state request staged current-rev what)
      (unless admitted
        (return-from assignment-acknowledge (values nil refusal code))))
    (ecase stage
      (:received
       (push (%verified-receipt offer-id staged :delivered)
             (assignment-state-deliveries state))
       (values t (format nil "ACKNOWLEDGE OK offer=~A stage=received effect=delivered"
                         offer-id)
               0))
      (:accepted
       (push (%verified-receipt offer-id staged :accepted)
             (assignment-state-acceptances state))
       (values t (format nil "ACKNOWLEDGE OK offer=~A stage=accepted effect=accepted"
                         offer-id)
               0)))))

(defun assignment-decline (state &key staged request offer-id (expect 0) (current-rev 0))
  "Write a verified refusal, and nothing else. It is inferred from nothing: a
delivery or an acceptance already recorded is neither erased nor duplicated
(SPEC-WORK.md:3839-3843)."
  (multiple-value-bind (admitted refusal code)
      (%admission-refusal state request staged current-rev "DECLINE")
    (unless admitted
      (return-from assignment-decline (values nil refusal code))))
  (push (%verified-receipt offer-id staged :declined)
        (assignment-state-declines state))
  (values t (format nil "DECLINE OK offer=~A effect=declined" offer-id) 0))

(defun session-status (state)
  "`session status` answers while a stage runs: it reads the counters and
touches none of the staged input (SPEC-WORK.md:3856)."
  (let ((events (+ (length (assignment-state-offers state))
                   (length (assignment-state-deliveries state))
                   (length (assignment-state-acceptances state))
                   (length (assignment-state-declines state)))))
    (values t (format nil "SESSION OK who=~A events=~D pending=~D"
                      (assignment-state-who state) events
                      (length (assignment-state-offers state))))))
