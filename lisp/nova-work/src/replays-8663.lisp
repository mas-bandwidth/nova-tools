;;;; replays-8663.lisp --- the pure part of presence and the held lease
;;;; (docs/SPEC-WORK.md:4210-4218) and the fleet allocation rules of #500
;;;; (docs/SPEC-WORK.md:6141-6152).
;;;;
;;;; Nothing here starts a session, sends a presence event, journals a request
;;;; or launches a machine: these are the pure functions the five replays-8663
;;;; acceptance cases drive. The live session, presence bus, journal and CLI
;;;; wiring those paragraphs sit on is out of this slice and named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; A held lease whose holder stops beating           SPEC-WORK.md:4210
;;; ------------------------------------------------------------------

(defparameter *holder-silence-seconds* 300
  "A holder that stops beating reads asleep inside 300 s (SPEC-WORK.md:4210).")

(defstruct (friend-lease (:constructor make-friend-lease
                            (&key holder node (state :held) (generation 1))))
  "One held lease. Reading a stale finding never changes it."
  holder node state generation)

(defun held-lease-p (lease)
  (eq (friend-lease-state lease) :held))

(defun lease-fenced-p (lease)
  (eq (friend-lease-state lease) :fenced))

(defun fence-lease (lease)
  "Fencing a lease retires it as a writer of O without touching its node
(SPEC-WORK.md:4216-4218)."
  (make-friend-lease :holder (friend-lease-holder lease)
                     :node (friend-lease-node lease)
                     :state :fenced
                     :generation (1+ (friend-lease-generation lease))))

(defun holder-asleep-finding (lease last-beat now)
  "The `stale` finding for a held lease whose holder stopped beating: the lease
reads finding=holder-asleep inside 300 s and is otherwise untouched
(SPEC-WORK.md:4210). Reading a finding is a reading, not a mutation."
  (when (and (held-lease-p lease)
             (>= (- now last-beat) *holder-silence-seconds*))
    "finding=holder-asleep"))

;;; ------------------------------------------------------------------
;;; Presence and recovery of a delegation         SPEC-WORK.md:4216
;;; ------------------------------------------------------------------

(defstruct (friend-presence (:constructor make-friend-presence
                               (&key name (state :unknown))))
  name state)

(defstruct (offer-record (:constructor make-offer-record
                              (&key id node friend (state :pending))))
  id node friend state)

(defstruct (recovery-session (:constructor make-recovery-session
                                (&key offers leases reassignments (rev 0))))
  offers leases reassignments rev)

(defun offer-to-sleeper (session friend &key anyway offer-id node)
  "Gate 4 refuses an offer to a friend reading asleep at exit 2. With --anyway
the offer is written and appears in `stale` (SPEC-WORK.md:4216)."
  (if (and (eq :asleep (friend-presence-state friend)) (not anyway))
      (values nil
              (format nil "OFFER FAIL recipient=~A reads asleep: exit 2"
                      (friend-presence-name friend))
              2 session)
      (let ((offer (make-offer-record :id offer-id :node node
                                      :friend (friend-presence-name friend))))
        (values t
                (format nil "OFFER OK id=~A written=true stale=true" offer-id)
                0
                (make-recovery-session
                 :offers (append (recovery-session-offers session) (list offer))
                 :leases (recovery-session-leases session)
                 :reassignments (recovery-session-reassignments session)
                 :rev (recovery-session-rev session))))))

(defun stale-entries (session)
  "The `stale` read lists every written offer whose recipient reads asleep
(SPEC-WORK.md:4216)."
  (mapcar (lambda (o)
            (format nil "STALE offer=~A node=~A friend=~A finding=holder-asleep"
                    (offer-record-id o) (offer-record-node o)
                    (offer-record-friend o)))
          (recovery-session-offers session)))

(defun reassign-node (session &key node from to reading rev)
  "Reassign NODE from FROM to TO, citing the reading it stood on, and fence the
prior lease (SPEC-WORK.md:4180-4184, 4216-4218)."
  (let* ((lease (find from (recovery-session-leases session)
                      :key #'friend-lease-holder :test #'equal))
         (fenced (and lease (fence-lease lease)))
         (leases (if fenced
                     (substitute fenced lease (recovery-session-leases session))
                     (recovery-session-leases session))))
    (values t
            (format nil "QUERY NOTE reassigned node=~A from=~A to=~A at=~D reading=~A"
                    node from to rev reading)
            (make-recovery-session
             :offers (recovery-session-offers session)
             :leases leases
             :reassignments (append (recovery-session-reassignments session)
                                    (list (list :node node :from from :to to
                                                :rev rev :reading reading)))
             :rev rev))))

(defun friend-heartbeat (session holder &key generation)
  "A heartbeat from a fence-retired holder is refused: it holds no lease to
renew (SPEC-WORK.md:4218)."
  (let ((lease (find holder (recovery-session-leases session)
                     :key #'friend-lease-holder :test #'equal)))
    (cond
      ((null lease)
       (values nil (format nil "LEASE FAIL holder=~A: no lease" holder) 1))
      ((lease-fenced-p lease)
       (values nil (format nil "LEASE FAIL holder=~A: lease fenced by reassignment"
                           holder)
               1))
      ((and generation (/= generation (friend-lease-generation lease)))
       (values nil (format nil "LEASE FAIL holder=~A: stale token" holder) 1))
      (t (values t (format nil "LEASE HEARTBEAT OK holder=~A" holder) 0)))))

(defun friend-who (session friend)
  "The first `who` of a woken friend shows the reassignment that fenced it
(SPEC-WORK.md:4218)."
  (let ((r (find-if (lambda (r) (equal (getf r :from) friend))
                    (recovery-session-reassignments session))))
    (when r
      (format nil "QUERY NOTE reassigned node=~A from=~A to=~A at=~D reading=~A"
              (getf r :node) (getf r :from) (getf r :to) (getf r :rev)
              (getf r :reading)))))

;;; ------------------------------------------------------------------
;;; The fleet: machine, allocation, take, release          SPEC-WORK.md:6099
;;; ------------------------------------------------------------------

(defstruct (assign-machine (:conc-name assign-machine-)
                           (:constructor make-assign-machine
                               (&key id (concurrent 1) (generation 1))))
  id concurrent generation)

(defstruct (allocation (:constructor make-allocation
                           (&key id machine slot (slots 1) node
                                 (generation 1) (state :active))))
  id machine slot slots node generation state)

(defun allocation-active-p (allocation)
  (eq (allocation-state allocation) :active))

(defstruct (assign-fleet (:conc-name assign-fleet-)
                         (:constructor make-assign-fleet (&key machine (allocations nil))))
  machine allocations)

(defun fleet-occupied-slots (fleet)
  (loop for a in (assign-fleet-allocations fleet)
        when (allocation-active-p a)
          append (loop for k from 0 below (allocation-slots a)
                       collect (+ (allocation-slot a) k))))

(defun fleet-first-free-slot (fleet)
  (loop for s from 1
        unless (member s (fleet-occupied-slots fleet)) return s))

(defun fleet-active-slots (fleet)
  (reduce #'+ (remove-if-not #'allocation-active-p (assign-fleet-allocations fleet))
          :key #'allocation-slots :initial-value 0))

(defun assign-fleet-take (fleet &key node slots assign-machine-generation holder allocation-id)
  "Admit one allocation whole or refuse it whole; no partial grant
(SPEC-WORK.md:6109-6123). A stale machine generation refuses the take by the
capacity line at exit 1 (SPEC-WORK.md:6150)."
  (let* ((machine (assign-fleet-machine fleet))
         (requested (or slots 1))
         (used (fleet-active-slots fleet)))
    (cond
      ((and assign-machine-generation
            (/= assign-machine-generation (assign-machine-generation machine)))
       (values nil (format nil "ALLOC FAIL machine=~A slots=~D holder=~A: capacity"
                           (assign-machine-id machine) requested holder)
               1 nil))
      ((> (+ used requested) (assign-machine-concurrent machine))
       (values nil (format nil "ALLOC FAIL machine=~A slots=~D holder=~A: capacity"
                           (assign-machine-id machine) requested holder)
               1 nil))
      (t
       (let* ((slot (fleet-first-free-slot fleet))
              (alloc (make-allocation :id allocation-id :machine (assign-machine-id machine)
                                      :slot slot :slots requested :node node
                                      :generation (assign-machine-generation machine)))
              (new (make-assign-fleet :machine machine
                               :allocations (append (assign-fleet-allocations fleet)
                                                    (list alloc)))))
         (values t
                 (format nil "ALLOC OK allocation=~A machine=~A slot=~D node=~A"
                         allocation-id (assign-machine-id machine) slot node)
                 0 new))))))

(defun assign-fleet-release (fleet &key allocation generation)
  "Free exactly that allocation's slot. A released or unknown allocation id, or
a stale allocation generation, is refused by name at exit 1
(SPEC-WORK.md:6119-6123, 6147-6149)."
  (let ((a (find allocation (assign-fleet-allocations fleet)
                 :key #'allocation-id :test #'equal)))
    (cond
      ((or (null a) (not (allocation-active-p a)))
       (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                           (assign-machine-id (assign-fleet-machine fleet)) allocation)
               1 nil))
      ((and generation (/= generation (allocation-generation a)))
       (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                           (assign-machine-id (assign-fleet-machine fleet)) allocation)
               1 nil))
      (t
       (let ((released (copy-allocation a)))
         (setf (allocation-state released) :released)
         (values t
                 (format nil "ALLOC RELEASE OK allocation=~A machine=~A slot=~D freed=true"
                         (allocation-id a) (assign-machine-id (assign-fleet-machine fleet))
                         (allocation-slot a))
                 0
                 (make-assign-fleet :machine (assign-fleet-machine fleet)
                             :allocations (substitute released a
                                                      (assign-fleet-allocations fleet)))))))))

(defun assign-fleet-heartbeat (fleet &key allocation generation)
  "A heartbeat renews one live allocation. A released id or a stale allocation
generation is refused by name at exit 1 (SPEC-WORK.md:6116-6119, 6147-6149)."
  (let ((a (find allocation (assign-fleet-allocations fleet)
                 :key #'allocation-id :test #'equal)))
    (if (and a (allocation-active-p a)
             (or (null generation) (= generation (allocation-generation a))))
        (values t
                (format nil "ALLOC HEARTBEAT OK allocation=~A machine=~A"
                        allocation (assign-machine-id (assign-fleet-machine fleet)))
                0)
        (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                            (assign-machine-id (assign-fleet-machine fleet)) allocation)
                1))))

(defun assign-fleet-list (fleet)
  "One ALLOC ROW per live allocation (SPEC-WORK.md:6121-6123)."
  (loop for a in (assign-fleet-allocations fleet)
        when (allocation-active-p a)
          collect (format nil "ALLOC ROW allocation=~A machine=~A slot=~D node=~A"
                          (allocation-id a) (assign-machine-id (assign-fleet-machine fleet))
                          (allocation-slot a) (allocation-node a))))
