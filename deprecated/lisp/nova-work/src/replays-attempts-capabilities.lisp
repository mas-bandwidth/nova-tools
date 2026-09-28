;;;; replays-attempts-capabilities.lisp --- the pure part of the five
;;;; execution-attribution replays of docs/SPEC-WORK.md:3385-3429 and the
;;;; Required replays table at :5655-5680.
;;;;
;;;; Each function is the smallest record the named paragraph promises:
;;;;
;;;;   four-capability-groups-and-three-fields  (:3390-3399, :5731)
;;;;   dispatch-ack-and-ownership-are-three     (:3401-3413, :5670)
;;;;   requested-model-is-not-observed-model    (:3407-3409, :5673)
;;;;   a-retry-does-not-overwrite-its-attempt   (:3409-3410, :5673)
;;;;   silence-is-a-ping-not-a-verdict          (:3415-3429, :5676)
;;;;
;;;; Nothing here starts a session, sends a ping or launches a worker: the
;;;; records are the facts the acceptance replays assert, and the *dispatch*,
;;;; *offer* and wake-protocol transports that would carry them live remain
;;;; outside this slice.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Capability groups and the three fields (SPEC-WORK.md:3390-3399)
;;; ------------------------------------------------------------------

(defparameter *capability-group-kinds*
  '(:child-agents :swarms :local-models :one-shots)
  "The four execution capability groups each friend may expose (:3390).")

(defstruct (capability-group
             (:constructor make-capability-group
                 (&key id kind source last-verified availability constraints
                       declared-support runtime-verified free-capacity)))
  "One catalog entry: the stable id, source, last-verified stamp, availability
and budget/permission constraints, plus declared support, successful runtime
verification and current free capacity kept as three separate fields."
  id kind source last-verified availability constraints
  declared-support runtime-verified free-capacity)

(defun capability-declared-support-p (g)
  "CONFIG says the friend may do this. It is not a runtime observation."
  (and (capability-group-declared-support g) t))

(defun capability-runtime-verified-p (g)
  "A successful runtime verification, never inferred from declared support."
  (and (capability-group-runtime-verified g) t))

(defun capability-free-capacity (g)
  "Current free capacity where observed, otherwise :unknown. A catalog entry is
not evidence of free credits (:3393)."
  (let ((c (capability-group-free-capacity g)))
    (if (null c) :unknown c)))

(defun capability-fields-collapse-p (g)
  "True when the three fields have been answered by one reading: support and
runtime agree, and free capacity is a bare boolean rather than a reading."
  (let ((support (capability-declared-support-p g))
        (runtime (capability-runtime-verified-p g))
        (free (capability-free-capacity g)))
    (and (eq support runtime) (member free '(t nil)))))

;;; ------------------------------------------------------------------
;;; Dispatch, delivery, acknowledgement and ownership (SPEC-WORK.md:3401-3413)
;;; ------------------------------------------------------------------

(defstruct (dispatch-fact
             (:constructor %make-dispatch-fact (kind &key request node to state
                                                             reserved declared)))
  "One dispatch fact. KIND keeps the four facts apart; RESERVED is the pending
offer's reservation and DECLARED the capacity the offer named."
  kind request node to state reserved declared)

(defun dispatch-offer (request node to declared-slots)
  "A dispatch records intent and a stable request identity, and its pending
offer reserves only the explicitly declared capacity (:3402)."
  (%make-dispatch-fact :offer :request request :node node :to to
                       :state :dispatched :reserved declared-slots
                       :declared declared-slots))

(defun delivery-receipt (offer)
  "Delivery is its own fact, distinct from the dispatch and the acceptance."
  (%make-dispatch-fact :delivery :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to (dispatch-fact-to offer)
                       :state :received :reserved 0 :declared 0))

(defun acknowledgement (offer &key stage)
  "Acknowledgement is its own fact; STAGE is :received or :accepted."
  (check-type stage (member :received :accepted))
  (%make-dispatch-fact :acknowledge :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to (dispatch-fact-to offer)
                       :state stage :reserved 0 :declared 0))

(defun accepted-ownership (offer holder)
  "Accepted ownership is its own fact, naming the holder it is bound to."
  (%make-dispatch-fact :ownership :request (dispatch-fact-request offer)
                       :node (dispatch-fact-node offer)
                       :to holder :state :accepted :reserved 0 :declared 0))

(defun facts-collapsed-p (&rest facts)
  "True when any two of the dispatch facts have been read as one."
  (let ((kinds (mapcar #'dispatch-fact-kind facts)))
    (or (some #'null kinds)
        (/= (length kinds) (length (remove-duplicates kinds))))))

(defun pending-offer-p (f)
  "A dispatched offer is pending until it is reconciled."
  (and (eq (dispatch-fact-kind f) :offer)
       (eq (dispatch-fact-state f) :dispatched)))

(defun pending-offer-reserved (f) (dispatch-fact-reserved f))
(defun pending-offer-declared (f) (dispatch-fact-declared f))

(defun offer-after-timeout (offer)
  "A timeout alone never blindly launches a duplicate while the old worker may
still be running (:3403): it marks the offer overdue and launches nothing, so
the reservation stands until reconciliation."
  (let ((o (copy-dispatch-fact offer)))
    (setf (dispatch-fact-state o) :overdue)
    o))

(defun dispatch-launched-p (f)
  "True only once a launched worker is recorded; a timeout records none."
  (eq (dispatch-fact-state f) :launched))

(defun second-offer-admitted-p (pending other)
  "One holder has one lease: while PENDING is live, a second offer to a
different name is refused and creates no shadow lease."
  (not (and (pending-offer-p pending)
            (not (equal (dispatch-fact-to pending) (dispatch-fact-to other))))))

;;; ------------------------------------------------------------------
;;; Requested vs observed model, and the retry (SPEC-WORK.md:3407-3410)
;;; ------------------------------------------------------------------

(defstruct (attempt
             (:constructor make-attempt (&key id node requested-model observed usage)))
  "One attempt: its stable id, the model that was requested and the model that
was observed (NIL when none was), and its own usage reading."
  id node requested-model observed usage)

(defun attempt-observed-model (a)
  "The observed model, or :unknown when no observation was recorded. A friend's
usual model is never proof of the model that executed a delegated task (:3408)."
  (let ((m (attempt-observed a)))
    (if (null m) :unknown m)))

(defun append-attempt (attempts new)
  "A retry appends a second attempt record; it never overwrites the attempt
before it (:3410)."
  (append attempts (list new)))

(defun attempts-collapsed-p (attempts)
  "True when two attempt records share one identity, the collapse a retry must
never cause."
  (/= (length attempts)
      (length (remove-duplicates attempts :key #'attempt-id :test #'equal))))

;;; ------------------------------------------------------------------
;;; Availability, silence and the one bounded ping (SPEC-WORK.md:3415-3429)
;;; ------------------------------------------------------------------

(defun silence-action (elapsed threshold &key resting reserved pinged-p)
  "The action the configured silence threshold asks for: :ping once, then
:already-pinged; :rest and :reserved suppress the ping; :quiet below the
threshold (:3419-3424)."
  (cond
    ((and resting (>= elapsed threshold)) :rest)
    ((and reserved (>= elapsed threshold)) :reserved)
    ((< elapsed threshold) :quiet)
    (pinged-p :already-pinged)
    (t :ping)))

(defun availability-after-window (&key answered-p resting)
  "The availability state after the configured answer window: an answer is
available; an unresolved nonresponse is :unconfirmed; explicit rest keeps its
own reason."
  (cond
    (answered-p (list :available t :reason nil))
    (resting (list :available nil :reason :explicit-rest))
    (t (list :available nil :reason :unconfirmed))))

(defun silence-verdict (availability)
  "The scheduling verdict for an availability reading. An unconfirmed capacity
asserts neither sleep nor exhausted credit without evidence (:3424)."
  (list :state (if (getf availability :available) :available :unavailable)
        :reason (getf availability :reason)
        :sleep nil
        :exhausted nil))

(defun probe-outcome (reply)
  "A failed probe is unresolved delivery, not a failed friend (:3425)."
  (if (eq reply :failed) :unresolved-delivery reply))
