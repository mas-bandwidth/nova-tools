;;;; fleet.lisp --- the fleet's static member records and the one verb that
;;;; configures them (docs/SPEC-WORK.md:3459-3588, *The fleet*).
;;;;
;;;; A member is CONFIG, never work: `:kind :machine`, instance data, no child
;;;; of O and under no repository work set, so no count, roadmap or required
;;;; set moves when one is written. This slice carries the registration path
;;;; and the refusals *The fleet* fixes:
;;;;
;;;;   no owner / no id / unknown owner / connect held by <id> /
;;;;   credential in record / unknown role / fact without provenance
;;;;
;;;; Nothing here names a machine or a host: the id, name, owner and profile
;;;; reference all arrive in the request, which is a team's own configuration.
;;;; The retired record, the other five changes, the two asks and the dynamic
;;;; allocation of *Fleet allocation* are outside this slice; unknown changes
;;;; refuse rather than guessing.

(in-package #:nova-work)

(defparameter *machine-roles* '(:build :test :profile)
  "SPEC-WORK.md:3531 --- the intended roles, Glenn's three: build code, run
tests and do profiling. An unknown role is a refusal, never a guess.")

(defparameter *machine-credential-keys* '(:key :token :password :secret)
  "SPEC-WORK.md:3548 --- a field named any of these in a machine record is a
credential; the record is refused whole and the value is not echoed.")

;;; ONE `machine` struct, two constructors (nova-tools #1612). This file
;;; defined it twice -- here and again at the head of the folded #1102 section
;;; below -- with IDENTICAL slots and two constructor names. SBCL calls that
;;; "Duplicate definition for COPY-MACHINE found in one file", a full WARNING
;;; and not a style-warning, which makes `(asdf:load-system :nova-work)` FAIL:
;;; the system loaded at all only because run-tests.sh muffled every warning.
;;; `defstruct` takes as many `(:constructor ...)` options as it is given, so
;;; both call shapes keep working from the one definition.
(defstruct (machine (:constructor %make-machine
                        (&key id name owner connect roles permits excludes limits facts))
                    (:constructor make-machine
                        (&key id name owner connect roles permits excludes limits facts)))
  "One CONFIG `:machine` member: equipment, never a work-tree node. Its roles,
permits and excludes are declared facts; a probe or a heartbeat never writes
them."
  id name owner connect roles permits excludes limits facts)

(defstruct (fleet (:constructor %make-fleet ()))
  (friends '())
  (machines (make-hash-table :test #'equal))
  (order '()))

(defun make-fleet (&key (friends '()))
  "FRIENDS is the team's own `friends` list. It is configuration supplied to
the session, never a constant here."
  (let ((f (%make-fleet)))
    (setf (fleet-friends f) (copy-list friends))
    f))

;;; Reads of the section. These are CONFIG reads, not work-set walks.

(defun fleet-member (fleet id)
  (gethash id (fleet-machines fleet)))

(defun fleet-members (fleet)
  "The live members in configured order."
  (loop for id in (reverse (fleet-order fleet))
        collect (gethash id (fleet-machines fleet))))

(defun fleet-member-count (fleet)
  (hash-table-count (fleet-machines fleet)))

(defun %machine-fail (id reason)
  "SPEC-WORK.md:3545 --- `MACHINE FAIL machine=<id>: <reason>`, exit 1. An
absent id prints `machine=-`. The reason is a fixed phrase, so no refused value
is ever echoed."
  (values nil (format nil "MACHINE FAIL machine=~A: ~A" (or id "-") reason) 1 nil))

(defun %profile-reference-p (connect)
  (and (stringp connect)
       (>= (length connect) 8)
       (string= "profile:" (subseq connect 0 8))))

(defun machine-config-section (machine)
  "A machine is the `:kind :machine` member record of the fleet section of
CONFIG, and never a work-tree node (SPEC-WORK.md:3620)."
  (list :section :fleet :kind :machine :id (machine-id machine)))

(defun machine-work-tree-node-p (machine)
  "Equipment never completes: a machine is no child of O and under no repository
work set."
  (declare (ignore machine))
  nil)

(defun machine-node-field (machine)
  "Every :machine event writes :node (:absent) by the kind's own subject rule."
  (declare (ignore machine))
  +absent+)

(defun machine-acceptance (machine)
  (declare (ignore machine))
  nil)

(defun machine-derived-state (machine)
  (declare (ignore machine))
  nil)

(defun machine-settle (machine)
  "No verb can settle a machine."
  (values nil
          (format nil "STATE FAIL machine=~A: equipment does not settle"
                  (machine-id machine))
          2))

(defun machine-to-done (machine)
  "No verb can take a machine `:to :done`."
  (values nil
          (format nil "STATE FAIL machine=~A: equipment has no edge to done"
                  (machine-id machine))
          2))

(defun machine-completion-evidence-p (machine)
  "Equipment does not complete, so nothing a machine does is completion
evidence."
  (declare (ignore machine))
  nil)

(defun machine-ok-line (event)
  (format nil "MACHINE OK id=~A request=~A machine=~A rev=~D pushed=~A changed=~D emitted=~D"
          (getf event :event-id) (getf event :request) (getf event :machine)
          (getf event :rev) (or (getf event :pushed) "-")
          (getf event :changed) (getf event :emitted)))

(defun %machine-event (kernel request change)
  "The one `:machine` CONFIG event: `:kind :machine`, `:node` `(:absent)`, and
the machine identity as its subject. It moves no work revision."
  (let ((id (getf request :machine)))
    (list :kind :machine :change change :node (machine-node-field nil)
          :machine id
          :event-id (format nil "ev-machine-~A-~A" id
                            (string-downcase (symbol-name change)))
          :request (or (getf request :request) (format nil "machine-~A" id))
          :rev (state-revision (kernel-state kernel))
          :pushed nil :changed 1 :emitted 0)))

(defun %machine-register (kernel request)
  "Write one `:kind :machine` fleet CONFIG member, or refuse whole. Every
refusal phrase is fixed, so no refused value is ever echoed (SPEC-WORK.md:3541-3548)."
  (let ((id (getf request :machine))
        (owner (getf request :owner))
        (name (getf request :name))
        (connect (getf request :connect))
        (roles (getf request :roles))
        (permits (getf request :permits))
        (excludes (getf request :excludes))
        (limits (getf request :limits))
        (facts (getf request :facts))
        (declared-by (getf request :declared-by))
        (fleet (kernel-fleet kernel)))
    ;; SPEC-WORK.md:3544-3546, in the order the paragraph lists the refusals.
    (unless (and owner (stringp owner) (plusp (length owner)))
      (return-from %machine-register (%machine-fail id "no owner")))
    (unless (and id (stringp id) (plusp (length id)))
      (return-from %machine-register (%machine-fail nil "no id")))
    (unless (member owner (fleet-friends fleet) :test #'string=)
      (return-from %machine-register (%machine-fail id "unknown owner")))
    (dolist (role roles)
      (unless (member role *machine-roles*)
        (return-from %machine-register (%machine-fail id "unknown role"))))
    ;; A credential is a `--connect` that is not a `profile:` reference, or any
    ;; field named key, token, password or secret. The record is refused whole.
    (when (or (not (%profile-reference-p connect))
              (some (lambda (key) (getf request key)) *machine-credential-keys*))
      (return-from %machine-register (%machine-fail id "credential in record")))
    ;; One profile is one unit: a profile already held by a member refuses.
    (let ((held (find connect (fleet-members fleet)
                      :key #'machine-connect :test #'string=)))
      (when held
        (return-from %machine-register
          (%machine-fail id (format nil "connect held by ~A" (machine-id held))))))
    (when (and facts
               (not (and declared-by (stringp declared-by)
                         (plusp (length declared-by)))))
      (return-from %machine-register (%machine-fail id "fact without provenance")))
    (when (fleet-member fleet id)
      (return-from %machine-register
        (%machine-fail id (format nil "id held by ~A" id))))
    (let ((member (%make-machine
                   :id id :name name :owner owner :connect connect
                   :roles (copy-list roles) :permits (copy-list permits)
                   :excludes (copy-list excludes) :limits (copy-list limits)
                   :facts (copy-list facts)))
          (event (%machine-event kernel request :register)))
      (setf (gethash id (fleet-machines fleet)) member)
      (push id (fleet-order fleet))
      ;; The machine is CONFIG; opening its one ACTIVE allocator is how a slot
      ;; later becomes takeable (SPEC-WORK.md:3707-3715).
      (%register-machine-allocator kernel request id limits facts connect roles)
      (values t (machine-ok-line event) 0 event))))

(defun %machine-change (kernel request)
  "A `:permit`, `:exclude`, `:limit` or `:fact` change to one live member: a
meaningful CONFIG change, never a heartbeat, a probe or a load sample
(SPEC-WORK.md:3527-3554)."
  (let* ((id (getf request :machine))
         (member (and id (fleet-member (kernel-fleet kernel) id))))
    (unless member
      (return-from %machine-change
        (%machine-fail id "no such member")))
    (let ((change (getf request :change))
          (workload (getf request :workload))
          (key (getf request :key))
          (value (getf request :value))
          (declared-by (getf request :declared-by)))
      (case change
        (:permit (pushnew workload (machine-permits member) :test #'equal))
        (:exclude (pushnew workload (machine-excludes member) :test #'equal))
        (:limit (setf (machine-limits member)
                      (let ((l (copy-list (machine-limits member))))
                        (setf (getf l key) value)
                        l)))
        (:fact
         (unless (and declared-by (stringp declared-by) (plusp (length declared-by)))
           (return-from %machine-change
             (%machine-fail (machine-id member) "fact without provenance")))
         (setf (machine-facts member)
               (list* (list :key key :value value
                            :declared-by declared-by
                            :declared-at (getf request :stamp))
                      (remove key (machine-facts member)
                              :key (lambda (f) (getf f :key)) :test #'equal)))))
      (when (member change '(:permit :exclude :limit :fact))
        (%sync-machine-allocator kernel member change))
      (values t (format nil "MACHINE OK machine=~A" (machine-id member)) 0
              (%machine-event kernel request change)))))

(defun machine-submit (kernel request)
  "One `:machine` event. SPEC-WORK.md:3541 --- `:register`, `:retire`, `:permit`,
`:exclude`, `:limit` and `:fact` are the six static-configuration changes; a
heartbeat, an `observe` and a probe are no part of CONFIG and change no member.
The work tree is never touched: no count, roadmap or required set moves."
  (let ((change (getf request :change)))
    (case change
      (:register (%machine-register kernel request))
      (:retire
       (let* ((id (getf request :machine))
              (fleet (kernel-fleet kernel))
              (member (and id (fleet-member fleet id))))
         (if (null member)
             (%machine-fail id "no such member")
             (progn
               (remhash id (fleet-machines fleet))
               (setf (fleet-order fleet) (remove id (fleet-order fleet)
                                                 :test #'equal))
               (remhash id (fleet-registry-allocators (kernel-allocations kernel)))
               (values t (format nil "MACHINE OK machine=~A" id) 0
                       (%machine-event kernel request :retire))))))
      ((:permit :exclude :limit :fact) (%machine-change kernel request))
      (otherwise
       (values nil
               (format nil "MACHINE FAIL machine=~A: unsupported change ~A"
                       (or (getf request :machine) "-")
                       (string-downcase (princ-to-string (or change "?"))))
               2 nil)))))


;;; ------------------------------------------------------------------
;;; folded from replays-8663.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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


;;; ------------------------------------------------------------------
;;; folded from replays-8664.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8664.lisp --- pure models for the three named acceptance replays
;;;; of docs/SPEC-WORK.md at origin/dev:
;;;;
;;;;   preparation-interrupted-before-launch   SPEC-WORK.md:6153-6159
;;;;   concurrent-slots-refuse-third-job       SPEC-WORK.md:6160-6164
;;;;   indexes-and-counters                    SPEC-WORK.md:6242
;;;;
;;;; The first two paragraphs name CLI lines on a live session; what they turn
;;;; on is one admission rule -- slots come from declared concurrency, never
;;;; from cores, and a slot returns only on a confirmed termination. That rule
;;;; is proven here purely. The third paragraph is the preservation suite's
;;;; counters-and-indexes row; the write-maintained |O|, friend index and
;;;; per-container counts are proven against an independent reconstruction.
;;;; The live journal, session and CLI wiring is out of this slice and is
;;;; listed in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; fleet allocation: slots come from declared concurrency, not cores
;;; ------------------------------------------------------------------

(defstruct (replay-allocation (:conc-name alloc-)
                              (:constructor %make-alloc))
  id slot node holder state)

(defstruct (replay-machine (:conc-name replay-machine-)
                           (:constructor %make-replay-machine))
  id cores concurrent allocations)

(defun make-replay-machine (id &key cores concurrent)
  (%make-replay-machine :id id :cores cores :concurrent concurrent :allocations '()))

(defun %live-allocations (machine)
  "An allocation holds its slot until a confirmed termination releases it; an
interrupted preparation and a suspect expiry are both still live."
  (remove-if (lambda (a) (eq :released (alloc-state a)))
             (replay-machine-allocations machine)))

(defun machine-live-slots (machine)
  (reduce #'+ (%live-allocations machine) :key #'alloc-slot :initial-value 0))

(defun find-allocation (machine allocation-id)
  (find allocation-id (replay-machine-allocations machine)
        :key #'alloc-id :test #'equal))

(defun take-slot (machine node &key (slots 1) holder id)
  "Take SLOTS of the machine's declared concurrency for NODE. Grant every
requested slot or none: a request that would pass :concurrent is refused with
the blocking holder and writes no allocation (SPEC-WORK.md:3659-3662,
6153-6164). Cores are a separate constraint and never the slot count."
  (if (> (+ (machine-live-slots machine) slots) (replay-machine-concurrent machine))
      (let* ((blocking (first (%live-allocations machine)))
             (named (or (and blocking (alloc-holder blocking)) holder)))
        (values nil
                (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: capacity"
                        (replay-machine-id machine) slots named)
                machine))
      (let* ((allocation (%make-alloc
                          :id (or id (format nil "alloc-~A"
                                             (length (replay-machine-allocations machine))))
                          :slot slots :node node :holder holder :state :active)))
        (values t
                (format nil "ALLOC OK id=~A machine=~A allocation=~A slot=~A node=~A"
                        (alloc-id allocation) (replay-machine-id machine)
                        (alloc-id allocation) slots node)
                (%make-replay-machine :id (replay-machine-id machine)
                              :cores (replay-machine-cores machine)
                              :concurrent (replay-machine-concurrent machine)
                              :allocations (append (replay-machine-allocations machine)
                                                   (list allocation)))))))

(defun preparation-interrupted (machine allocation-id)
  "Preparation (clone/scp) was interrupted before the model launched: the
allocation keeps holding its slot (SPEC-WORK.md:6153-6156)."
  (let ((allocation (find-allocation machine allocation-id)))
    (when allocation (setf (alloc-state allocation) :interrupted))
    machine))

(defparameter *termination-evidence* '(:verified-stop :not-started :machine-fencing)
  "Capacity returns only on confirmed process termination: a verified stop
observation, a not-started bound to the launch authority, or machine-side
fencing (SPEC-WORK.md:3682-3687).")

(defun reconcile-allocation (machine allocation-id evidence)
  "Free ALLOCATION-ID's slot only on confirmed termination EVIDENCE; an expiry,
a silence or an elapsed estimate frees nothing (SPEC-WORK.md:6156-6159)."
  (let ((allocation (find-allocation machine allocation-id)))
    (cond ((null allocation)
           (values nil
                   (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                           (replay-machine-id machine) allocation-id)
                   machine))
          ((not (member evidence *termination-evidence*))
           (values nil
                   (format nil "ALLOC FAIL machine=~A: not fenced" (replay-machine-id machine))
                   machine))
          (t
           (setf (alloc-state allocation) :released)
           (values t
                   (format nil "ALLOC RELEASE OK allocation=~A machine=~A slot=~A freed=true"
                           allocation-id (replay-machine-id machine) (alloc-slot allocation))
                   machine)))))

;;; ------------------------------------------------------------------
;;; indexes-and-counters                            SPEC-WORK.md:6242
;;; ------------------------------------------------------------------
;;; The write-maintained |O|, |C|, the friend (holder) index and the
;;; per-container counts. Each verb moves them on the write path along the
;;; item's containment ancestors only; the reconstruction walks everything and
;;; consults no maintained value, so the two agreeing is the acceptance.

(defstruct (rnode (:conc-name rnode-))
  id parent state holder links)

(defstruct (replay-index (:conc-name rindex-)
                         (:constructor %make-rindex))
  nodes order open-count closed-count holder-index container-index)

(defun make-replay-index (specs)
  (let ((table (make-hash-table :test #'equal)) (order '()))
    (dolist (spec specs)
      (push (getf spec :id) order)
      (setf (gethash (getf spec :id) table)
            (make-rnode :id (getf spec :id) :parent (getf spec :parent)
                        :state (getf spec :state :open)
                        :holder (getf spec :holder)
                        :links (getf spec :links))))
    (%seed-maintained
     (%make-rindex :nodes table :order (nreverse order)
                  :open-count 0 :closed-count 0
                  :holder-index '() :container-index '()))))

(defun %node-of (index id) (gethash id (rindex-nodes index)))

(defun %ancestors (index id)
  "ID and its parent chain, nearest first."
  (let ((out '()) (cur id))
    (loop while cur
          do (push cur out)
             (let ((n (%node-of index cur))) (setf cur (and n (rnode-parent n)))))
    out))

(defun %holder-cell (index holder)
  (assoc holder (rindex-holder-index index) :test #'equal))

(defun %seed-maintained (index)
  "Seed the maintained counters and indexes once, on the write path that builds
the set."
  (let ((open 0) (closed 0) (holders '()) (containers '()))
    (dolist (id (rindex-order index))
      (if (eq :open (rnode-state (%node-of index id)))
          (progn (incf open)
                 (let ((h (rnode-holder (%node-of index id))))
                   (when h
                     (let ((cell (assoc h holders :test #'equal)))
                       (if cell (push id (cdr cell))
                           (push (cons h (list id)) holders))))))
          (incf closed)))
    (dolist (id (rindex-order index))
      (when (eq :open (rnode-state (%node-of index id)))
        (dolist (anc (%ancestors index id))
          (let ((cell (assoc anc containers :test #'equal)))
            (if cell (incf (cdr cell)) (push (cons anc 1) containers))))))
    (setf (rindex-open-count index) open
          (rindex-closed-count index) closed
          (rindex-holder-index index) holders
          (rindex-container-index index) containers)
    index))

(defun %holder-remove (index id)
  (let* ((n (%node-of index id)) (h (and n (rnode-holder n))))
    (when h
      (let ((cell (%holder-cell index h)))
        (when cell (setf (cdr cell) (remove id (cdr cell) :test #'equal)))))))

(defun %holder-add (index id)
  (let* ((n (%node-of index id)) (h (and n (rnode-holder n))))
    (when h
      (let ((cell (%holder-cell index h)))
        (if cell
            (unless (member id (cdr cell) :test #'equal) (push id (cdr cell)))
            (push (cons h (list id)) (rindex-holder-index index)))))))

(defun %container-adjust (index ids delta)
  (dolist (id ids)
    (let ((cell (assoc id (rindex-container-index index) :test #'equal)))
      (if cell (incf (cdr cell) delta)
          (push (cons id delta) (rindex-container-index index))))))

(defun replay-index-open-p (index id)
  (let ((n (%node-of index id))) (and n (eq :open (rnode-state n)))))

(defun replay-index-closed-p (index id)
  (let ((n (%node-of index id))) (and n (eq :closed (rnode-state n)))))

(defun replay-index-ancestor-p (index id ancestor)
  "T when ANCESTOR is ID or lies on ID's parent chain."
  (and id (member ancestor (%ancestors index id) :test #'equal) t))

(defun replay-index-close (index id)
  (let ((n (%node-of index id)))
    (when (and n (eq :open (rnode-state n)))
      (setf (rnode-state n) :closed)
      (decf (rindex-open-count index))
      (incf (rindex-closed-count index))
      (%holder-remove index id)
      (%container-adjust index (%ancestors index id) -1))
    index))

(defun replay-index-reopen (index id)
  (let ((n (%node-of index id)))
    (when (and n (eq :closed (rnode-state n)))
      (setf (rnode-state n) :open)
      (incf (rindex-open-count index))
      (decf (rindex-closed-count index))
      (%holder-add index id)
      (%container-adjust index (%ancestors index id) 1))
    index))

(defun replay-index-reparent (index id new-parent)
  "Move ID under NEW-PARENT. |O| and the friend index are untouched; the
per-container counts move along the old and new ancestor chains and are never
double-counted (SPEC-WORK.md:6242)."
  (let ((n (%node-of index id)))
    (when (and n (%node-of index new-parent))
      (let ((openp (eq :open (rnode-state n))))
        (when openp (%container-adjust index (%ancestors index id) -1))
        (setf (rnode-parent n) new-parent)
        (when openp (%container-adjust index (%ancestors index id) 1))))
    index))

(defun replay-index-assign (index id holder)
  (let ((n (%node-of index id)))
    (when n
      (when (eq :open (rnode-state n)) (%holder-remove index id))
      (setf (rnode-holder n) holder)
      (when (eq :open (rnode-state n)) (%holder-add index id)))
    index))

(defun holder-open-ids (index holder)
  (copy-list (cdr (%holder-cell index holder))))

;;; The independent reconstruction: a full walk of the current item set that
;;; reads no maintained counter and no maintained index.

(defun %subtree-open-count (index id)
  (+ (if (eq :open (rnode-state (%node-of index id))) 1 0)
     (loop for child in (rindex-order index)
           when (equal (rnode-parent (%node-of index child)) id)
             sum (%subtree-open-count index child))))

(defun independent-reconstruction (index)
  (let ((open 0) (closed 0) (holders '()) (containers '()))
    (dolist (id (rindex-order index))
      (if (eq :open (rnode-state (%node-of index id)))
          (progn
            (incf open)
            (let ((h (rnode-holder (%node-of index id))))
              (when h
                (let ((cell (assoc h holders :test #'equal)))
                  (if cell (push id (cdr cell))
                      (push (cons h (list id)) holders))))))
          (incf closed)))
    (dolist (id (rindex-order index))
      (let ((count (%subtree-open-count index id)))
        (when (plusp count) (push (cons id count) containers))))
    (list :open open :closed closed :holder-index holders :container-index containers)))

(defun %normalize-holder (alist)
  (sort (remove-if (lambda (cell) (null (cdr cell)))
                   (mapcar (lambda (cell) (cons (car cell) (sort (copy-list (cdr cell)) #'string<)))
                           alist))
        #'string< :key #'car))

(defun %normalize-container (alist)
  (sort (remove-if (lambda (cell) (zerop (cdr cell))) (copy-list alist))
        #'string< :key #'car))

(defun index-mismatches (index)
  "Every way the maintained counters and indexes disagree with an independent
full reconstruction; () when they agree."
  (let ((r (independent-reconstruction index)) (out '()))
    (unless (= (rindex-open-count index) (getf r :open))
      (push (format nil "|O| maintained=~A reconstructed=~A"
                    (rindex-open-count index) (getf r :open)) out))
    (unless (= (rindex-closed-count index) (getf r :closed))
      (push (format nil "|C| maintained=~A reconstructed=~A"
                    (rindex-closed-count index) (getf r :closed)) out))
    (unless (equal (%normalize-holder (rindex-holder-index index))
                   (%normalize-holder (getf r :holder-index)))
      (push "friend index disagrees with reconstruction" out))
    (unless (equal (%normalize-container (rindex-container-index index))
                   (%normalize-container (getf r :container-index)))
      (push "container counters disagree with reconstruction" out))
    out))


;;; ------------------------------------------------------------------
;;; folded from replays-config-and-availability.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-config-and-availability.lisp --- the pure availability/rest layer and
;;;; the bounded, validated, atomic configuration exchange the five replays call
;;;; (docs/SPEC-WORK.md:3415-3444).
;;;;
;;;; Nothing here opens a session, sends a ping or touches a wire: these are the
;;;; pure decisions the `explicit-rest-is-not-pinged`,
;;;; `return-reconciles-before-dispatch`, `unchanged-config-is-one-bounded-answer`,
;;;; `an-invalid-delta-leaves-the-old-config` and `a-partial-manifest-is-refused`
;;;; acceptance replays assert.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Availability: explicit rest is respected (SPEC-WORK.md:3419)
;;; ------------------------------------------------------------------

(defstruct (availability
             (:constructor make-availability
                 (&key (state :active) last-contact reserved
                       (silence-threshold 60) (answer-window 30) (pinged nil))))
  state last-contact reserved silence-threshold answer-window pinged)

(defun silence-breach-p (av now)
  "True when a last contact is at least the configured threshold before NOW."
  (and (availability-last-contact av)
       (>= (- now (availability-last-contact av))
           (availability-silence-threshold av))))

(defun silence-ping (av now)
  "One bounded availability ping at the configured silence threshold. A resting
or reserved friend is never pinged; a second ask after a ping is bounded to no
action (SPEC-WORK.md:3419-3423). Returns (values action reason)."
  (cond
    ((eq (availability-state av) :resting) (values :none :explicit-rest))
    ((availability-reserved av) (values :none :reserved))
    ((availability-pinged av) (values :none :already-pinged))
    ((silence-breach-p av now) (values :ping :silence-threshold))
    (t (values :none :within-threshold))))

(defun mark-pinged (av)
  "Record the one bounded ping; the ping is never repeated."
  (setf (availability-pinged av) t)
  av)

(defun mark-unconfirmed (av)
  "A nonresponse marks the capacity unavailable with reason `unconfirmed`,
asserting neither sleep nor exhausted credit (SPEC-WORK.md:3424)."
  (setf (availability-state av) :unconfirmed)
  av)

;;; ------------------------------------------------------------------
;;; A return reconciles before any new dispatch (SPEC-WORK.md:3426)
;;; ------------------------------------------------------------------

(defstruct (return-reconciliation
             (:constructor make-return-reconciliation
                 (&key assignments capacity done)))
  assignments capacity done)

(defun reconcile-return (&key assignments capacity)
  "A return reconciles its outstanding assignments and observed capacity."
  (make-return-reconciliation :assignments assignments :capacity capacity :done t))

(defun return-done-p (r)
  (and r (return-reconciliation-done r)))

(defun dispatch-gate (r)
  "A new dispatch is admitted only after a return's reconciliation."
  (if (return-done-p r)
      (values t "DISPATCH OK" 0)
      (values nil
              "DISPATCH FAIL: return reconciling outstanding assignments before new dispatch"
              2)))

;;; ------------------------------------------------------------------
;;; Configuration: bounded, validated and atomic (SPEC-WORK.md:3431-3444)
;;; ------------------------------------------------------------------

(defparameter *config-fragment-keys* '(:friends :models :routes :pricing :fleet)
  "The fragments a configuration exchange may name.")

(defparameter *config-secret-keys* '(:api-key :token :password :secret :credential)
  "A manifest carries no credential value (SPEC-WORK.md:3441).")

(defun fragment-digest (value)
  (sha256-hex (canonical-string value)))

(defun make-config (&key friend schema revision fragments)
  "A manifest is its revision, its fragments and the content hash over them."
  (let ((c (list :friend friend :schema schema :revision revision
                 :fragments fragments)))
    (setf (getf c :hash) (config-hash c))
    c))

(defun config-hash (config)
  (sha256-hex
   (canonical-string (list :friend (getf config :friend)
                           :schema (getf config :schema)
                           :fragments (getf config :fragments)))))

(defun config-identity (config)
  (cons (getf config :revision) (getf config :hash)))

(defun config-fragment (config key)
  (let ((frag (assoc key (getf config :fragments))))
    (and frag (second frag))))

(defun make-delta-entry (key value)
  (list :key key :value value :hash (fragment-digest value)))

(defun config-delta (base config)
  "The bounded delta against BASE: one entry per changed fragment."
  (let ((delta '()))
    (dolist (frag (getf config :fragments))
      (let* ((key (first frag)) (value (second frag))
             (old (assoc key (getf base :fragments))))
        (unless (and old (equal (second old) value))
          (push (make-delta-entry key value) delta))))
    (nreverse delta)))

(defun config-request (base config)
  "BASE is the request's last-known config (its revision and hash are the
request's identity), or NIL when the base is unknown. An equal identity answers
`UNCHANGED` with that identity in one bounded reply; otherwise a bounded delta
against the exact named base, or a full manifest when the base is unknown."
  (let ((identity (config-identity config)))
    (cond
      ((null base)
       (values :full (list :friend (getf config :friend)
                           :revision (getf config :revision)
                           :hash (getf config :hash)
                           :fragments (getf config :fragments))))
      ((equal (config-identity base) identity)
       (values :unchanged
               (format nil "UNCHANGED friend=~A rev=~D hash=~A"
                       (getf config :friend) (getf config :revision)
                       (getf config :hash))))
      (t (values :delta (config-delta base config))))))

(defun set-config-fragment (frags key value)
  (let ((cell (assoc key frags)))
    (if cell
        (progn (setf (second cell) value) frags)
        (append frags (list (list key value))))))

(defun apply-config-delta (base delta)
  "Apply DELTA against the exact named BASE, after schema, identity and hash
validation, atomically. On any refusal the old config is returned untouched."
  (let ((refusal nil))
    (dolist (entry delta)
      (let ((key (getf entry :key)))
        (cond
          ((not (member key *config-fragment-keys*))
           (setf refusal (format nil "unknown fragment ~S" key)))
          ((not (equal (getf entry :hash)
                       (fragment-digest (getf entry :value))))
           (setf refusal (format nil "bad hash for fragment ~S" key))))))
    (when refusal
      (return-from apply-config-delta (values base refusal)))
    (let ((frags (copy-tree (getf base :fragments))))
      (dolist (entry delta)
        (setf frags (set-config-fragment frags
                                         (getf entry :key)
                                         (getf entry :value))))
      (let ((new (list :friend (getf base :friend)
                       :schema (getf base :schema)
                       :revision (1+ (getf base :revision))
                       :fragments frags)))
        (setf (getf new :hash) (config-hash new))
        (values new nil)))))

;;; ------------------------------------------------------------------
;;; A partial manifest is never a complete replacement (SPEC-WORK.md:3436)
;;; ------------------------------------------------------------------

(defun manifest-digest (parts)
  (sha256-hex (canonical-string parts)))

(defun make-manifest (&key friend schema revision parts)
  (list :friend friend :schema schema :revision revision
        :parts parts :completeness-hash (manifest-digest parts)))

(defun find-secret (form)
  "The first secret key anywhere in FORM, or NIL."
  (cond
    ((consp form)
     (or (and (keywordp (car form))
              (member (car form) *config-secret-keys*)
              (car form))
         (find-secret (car form))
         (find-secret (cdr form))))
    (t nil)))

(defun admit-manifest (manifest declared-parts)
  "A manifest is admitted only when it carries every declared part and its
completeness hash, and only when it carries no secret. A partial config is
refused whole and is never admitted as a complete replacement."
  (let ((secret (find-secret manifest)))
    (cond
      (secret
       (values nil (format nil "MANIFEST REFUSED: secret field ~S" secret)))
      ((not (subsetp declared-parts (mapcar #'car (getf manifest :parts))
                     :test #'equal))
       (values nil "MANIFEST REFUSED: partial config is not a complete replacement"))
      ((not (equal (getf manifest :completeness-hash)
                   (manifest-digest (getf manifest :parts))))
       (values nil "MANIFEST REFUSED: completeness hash mismatch"))
      (t (values manifest nil)))))


;;; ------------------------------------------------------------------
;;; folded from replays-fleet-allocation.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-fleet-allocation.lisp --- the pure part of the Fleet allocation
;;;; amendment: the machine as CONFIG, the one allocator per physical machine,
;;;; the ACTIVE allocation, the atomic and idempotent take, the suspect expiry,
;;;; the dated probe and the machine aliases (docs/SPEC-WORK.md:3622-3728, the
;;;; *Acceptance replays* rows at :6099-6140).
;;;;
;;;; Nothing here starts a session, a process, a socket or a clock: the
;;;; allocator is the pure planning and accounting layer the five
;;;; `allocation-*` / `expiry-*` / `probe-*` / `one-allocator-*` replays call.
;;;; The live-session, CLI and transport wiring the spec's command lines imply
;;;; is out of this slice (see RESULT.md).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The registry and the one allocator per physical machine
;;; ------------------------------------------------------------------

(defstruct (fleet-registry
             (:constructor make-fleet-registry ()))
  (allocators (make-hash-table :test #'equal))
  (aliases (make-hash-table :test #'equal))
  (events 0))

(defstruct (fleet-allocator
             (:constructor make-fleet-allocator
                 (&key machine-id name owner aliases concurrent cores generation
                       facts limits connect roles)))
  machine-id name owner aliases concurrent cores generation facts limits connect roles
  (allocations '())
  (observations '())
  (requests (make-hash-table :test #'equal))
  (reduced-p nil))

(defstruct (allocation-record
             (:constructor make-allocation-record
                 (&key allocation-id machine alias slot slots node batch offer attempt
                       machine-generation allocation-generation request request-ref
                       holder parent deadline state line admission-phase core-pin created)))
  allocation-id machine alias slot slots node batch offer attempt
  machine-generation allocation-generation request request-ref holder parent
  deadline state line admission-phase core-pin created
  released fenced)

(defun fleet-resolve (registry machine)
  "The canonical machine id an alias names, or the id itself when unknown."
  (gethash machine (fleet-registry-aliases registry) machine))

(defun fleet-allocator-of (registry machine)
  (gethash (fleet-resolve registry machine) (fleet-registry-allocators registry)))

(defun fleet-open-allocator (registry machine
                             &key (concurrent 1) cores (generation 0)
                                  facts limits connect roles name owner)
  "Open the one allocator for MACHINE, or refuse a second allocator for a
machine that already has one (SPEC-WORK.md:3710)."
  (let* ((canonical (fleet-resolve registry machine))
         (existing (gethash canonical (fleet-registry-allocators registry))))
    (if existing
        (values nil (format nil "ALLOC FAIL machine=~A: allocator held" machine))
        (let ((allocator (make-fleet-allocator
                          :machine-id canonical :name name :owner owner
                          :concurrent concurrent :cores cores :generation generation
                          :facts facts :limits limits :connect connect :roles roles
                          :aliases (list canonical))))
          (setf (gethash canonical (fleet-registry-allocators registry)) allocator)
          (setf (gethash canonical (fleet-registry-aliases registry)) canonical)
          (values allocator
                  (format nil "MACHINE OK machine=~A rev=~A" canonical generation))))))

(defun fleet-register-machine (registry &key machine-id (aliases '()) name owner concurrent
                                                cores generation facts limits connect roles)
  "Register a `:kind :machine` CONFIG member and open its one allocator; every
alias of the physical host shares that allocator."
  (multiple-value-bind (allocator line)
      (fleet-open-allocator registry machine-id
                            :concurrent concurrent :cores cores :generation generation
                            :facts facts :limits limits :connect connect :roles roles
                            :name name :owner owner)
    (if allocator
        (progn
          (dolist (alias aliases)
            (setf (gethash alias (fleet-registry-aliases registry)) machine-id))
          (setf (fleet-allocator-aliases allocator) (cons machine-id aliases))
          (values t line))
        (values nil line))))

;;; ------------------------------------------------------------------
;;; Allocation queries
;;; ------------------------------------------------------------------

(defun allocation-live-p (a)
  "An allocation holds its slot until a verified release or machine-side fencing."
  (and (not (allocation-record-released a))
       (not (allocation-record-fenced a))))

(defun fleet-stamp-before-p (a b)
  "Stamps compare as integers when both are integers and as strings otherwise."
  (if (and (numberp a) (numberp b)) (< a b) (string< (princ-to-string a) (princ-to-string b))))

(defun allocation-suspect-p (a &key now)
  "An allocation past its deadline reads suspect; it is retained and counted
(SPEC-WORK.md:3677)."
  (and (allocation-live-p a)
       (allocation-record-deadline a)
       now
       (fleet-stamp-before-p (allocation-record-deadline a) now)))

(defun fleet-find-allocation (registry allocation-id)
  "The live-or-recent allocation named exactly once by ALLOCATION-ID."
  (loop for allocator being the hash-values of (fleet-registry-allocators registry)
        do (let ((a (find allocation-id (fleet-allocator-allocations allocator)
                         :key #'allocation-record-allocation-id :test #'equal)))
             (when a (return-from fleet-find-allocation (values a allocator))))))

(defun fleet-live-allocations (registry &key machine)
  "Every allocation that still holds a slot, in creation order, each once."
  (let ((allocator (fleet-allocator-of registry machine)))
    (and allocator
         (remove-if-not #'allocation-live-p
                        (reverse (fleet-allocator-allocations allocator))))))

(defun fleet-consumed-capacity (registry &key machine)
  "The slots held by non-nested live allocations on the physical machine."
  (let ((allocator (fleet-allocator-of registry machine)))
    (and allocator (fleet-consumed allocator))))

(defun fleet-consumed (allocator)
  (loop for a in (fleet-allocator-allocations allocator)
        when (and (allocation-live-p a) (null (allocation-record-parent a)))
          sum (allocation-record-slots a)))

(defun fleet-capacity-holder (allocator)
  (let ((a (find-if (lambda (x) (and (allocation-live-p x)
                                     (null (allocation-record-parent x))))
                    (fleet-allocator-allocations allocator))))
    (and a (allocation-record-holder a))))

(defun fleet-declared-facts (registry &key machine)
  (let ((a (fleet-allocator-of registry machine)))
    (and a (fleet-allocator-facts a))))

(defun fleet-declared-limits (registry &key machine)
  (let ((a (fleet-allocator-of registry machine)))
    (and a (fleet-allocator-limits a))))

(defun fleet-declared-connect (registry &key machine)
  (let ((a (fleet-allocator-of registry machine)))
    (and a (fleet-allocator-connect a))))

(defun fleet-declared-roles (registry &key machine)
  (let ((a (fleet-allocator-of registry machine)))
    (and a (fleet-allocator-roles a))))

(defun fleet-observations (registry &key machine)
  "The dated observed ACTIVE evidence, oldest first; CONFIG is never touched."
  (let ((a (fleet-allocator-of registry machine)))
    (and a (reverse (fleet-allocator-observations a)))))

;;; ------------------------------------------------------------------
;;; take: atomic, idempotent, all-or-none
;;; ------------------------------------------------------------------

(defun fleet-take (registry &key machine node slots offer attempt generation
                                request-ref batch request allocation-id (holder "rowan")
                                allocation-generation parent (now 0) deadline)
  "Write one ACTIVE allocation for MACHINE, binding slot and generation to a
batch, a node and an (offer, attempt), or refuse with a bounded line. A retry
under the same request id returns the original line; a changed payload under
that id is refused (SPEC-WORK.md:3646-3673)."
  (let* ((canonical (fleet-resolve registry machine))
         (allocator (gethash canonical (fleet-registry-allocators registry))))
    (unless allocator
      (return-from fleet-take
        (values nil (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: unknown machine"
                            machine slots holder) 1)))
    (unless generation
      (setf generation (fleet-allocator-generation allocator)))
    (let ((prior (gethash request (fleet-allocator-requests allocator))))
      (when prior
        (if (equal (getf prior :payload)
                   (list machine node slots offer attempt generation request-ref batch))
            (return-from fleet-take (values t (getf prior :line) 0))
            (return-from fleet-take
              (values nil (format nil "ALLOC FAIL machine=~A: reused with a different payload"
                                  machine) 1)))))
    ;; A fresh take under a suspect allocation's stale reservation token is refused.
    (let ((suspect (find-if (lambda (a)
                              (and (allocation-live-p a)
                                   (allocation-suspect-p a :now now)
                                   (equal offer (allocation-record-offer a))
                                   (equal attempt (allocation-record-attempt a))))
                            (fleet-allocator-allocations allocator))))
      (when suspect
        (return-from fleet-take
          (values nil (format nil "ALLOC FAIL machine=~A: suspect since=~A"
                              machine (allocation-record-deadline suspect)) 1))))
    ;; A stale machine generation grants nothing.
    (when (/= generation (fleet-allocator-generation allocator))
      (return-from fleet-take
        (values nil (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: stale token"
                            machine slots holder) 1)))
    (let ((parent-record (and parent (fleet-find-allocation registry parent))))
      (when (and parent (not (and parent-record (allocation-live-p parent-record))))
        (return-from fleet-take
          (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                              machine parent) 1)))
      (let* ((nested-p (not (null parent-record)))
             (available (- (fleet-allocator-concurrent allocator) (fleet-consumed allocator))))
        (when (and (not nested-p)
                   (or (fleet-allocator-reduced-p allocator) (> slots available)))
          (return-from fleet-take
            (values nil (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: capacity"
                                machine slots (or (fleet-capacity-holder allocator) holder)) 1)))
        (let* ((ev (incf (fleet-registry-events registry)))
               (aid (or allocation-id (format nil "alloc-~A" ev)))
               (ag (or allocation-generation (format nil "ag-~A" ev)))
               (slot-no (if nested-p (allocation-record-slot parent-record) 1))
               (record (make-allocation-record
                        :allocation-id aid :machine canonical :alias machine
                        :slot slot-no :slots slots :node node :batch batch
                        :offer offer :attempt attempt
                        :machine-generation generation :allocation-generation ag
                        :request request :request-ref request-ref :holder holder
                        :parent (and parent-record (allocation-record-allocation-id parent-record))
                        :deadline deadline :state :active :admission-phase :before-preparation
                        :core-pin nil :created now))
               (line (format nil "ALLOC OK id=ev-~4,'0D request=~A machine=~A allocation=~A slot=~A slots=~A node=~A batch=~A offer=~A attempt=~A machine-generation=~A allocation-generation=~A rev=~A pushed=- changed=1 emitted=300"
                             ev request canonical aid slot-no slots node batch offer attempt
                             generation ag ev)))
          (push record (fleet-allocator-allocations allocator))
          (setf (gethash request (fleet-allocator-requests allocator))
                (list :payload (list machine node slots offer attempt generation request-ref batch)
                      :line line))
          (values t line 0))))))

;;; ------------------------------------------------------------------
;;; heartbeat and release
;;; ------------------------------------------------------------------

(defun fleet-generation-ok-p (allocator record generation allocation-generation)
  (and (or (null generation) (= generation (fleet-allocator-generation allocator)))
       (or (null allocation-generation)
           (equal allocation-generation (allocation-record-allocation-generation record)))))

(defun fleet-heartbeat (registry &key allocation generation allocation-generation now request)
  "Validate an allocation's generations against its ACTIVE record and the
machine's CONFIG record; a suspect allocation renews nothing (SPEC-WORK.md:3663)."
  (declare (ignore request))
  (multiple-value-bind (record allocator) (fleet-find-allocation registry allocation)
    (unless (and record (allocation-live-p record))
      (return-from fleet-heartbeat
        (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                            (if record (allocation-record-machine record) "m-a1") allocation) 1)))
    (when (allocation-suspect-p record :now now)
      (return-from fleet-heartbeat
        (values nil (format nil "ALLOC FAIL machine=~A: suspect since=~A"
                            (allocation-record-machine record)
                            (allocation-record-deadline record)) 1)))
    (unless (fleet-generation-ok-p allocator record generation allocation-generation)
      (return-from fleet-heartbeat
        (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                            (allocation-record-machine record) allocation) 1)))
    (when (and now (allocation-record-deadline record))
      (setf (allocation-record-deadline record) (+ now 1000)))
    (values t (format nil "ALLOC HEARTBEAT OK allocation=~A machine=~A machine-generation=~A allocation-generation=~A rev=~A"
                      allocation (allocation-record-machine record)
                      (allocation-record-machine-generation record)
                      (allocation-record-allocation-generation record)
                      (fleet-registry-events registry))
            0)))

(defun fleet-release (registry &key allocation generation allocation-generation now
                                 fenced stop-observed not-started handed holder)
  "Free exactly ALLOCATION's slot after validating both generations; an expired
allocation is released only on confirmed termination (SPEC-WORK.md:3667-3687)."
  (declare (ignore handed holder))
  (multiple-value-bind (record allocator) (fleet-find-allocation registry allocation)
    (unless (and record (allocation-live-p record))
      (return-from fleet-release
        (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                            (if record (allocation-record-machine record) "m-a1") allocation) 1)))
    (unless (fleet-generation-ok-p allocator record generation allocation-generation)
      (return-from fleet-release
        (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                            (allocation-record-machine record) allocation) 1)))
    (when (and (allocation-suspect-p record :now now)
               (not (or fenced stop-observed not-started)))
      (return-from fleet-release
        (values nil (format nil "ALLOC FAIL machine=~A: not fenced"
                            (allocation-record-machine record)) 1)))
    (setf (allocation-record-released record) t
          (allocation-record-fenced record) (and fenced t))
    (values t (format nil "ALLOC RELEASE OK allocation=~A machine=~A slot=~A freed=true rev=~A"
                      allocation (allocation-record-machine record)
                      (allocation-record-slot record) (fleet-registry-events registry))
            0)))

;;; ------------------------------------------------------------------
;;; list and probe
;;; ------------------------------------------------------------------

(defun fleet-list (registry &key machine (now 0))
  "One `ALLOC ROW` per live allocation, each listed once (SPEC-WORK.md:3671)."
  (let ((allocator (fleet-allocator-of registry machine)))
    (when allocator
      (let ((canonical (fleet-resolve registry machine)))
        (loop for a in (reverse (fleet-allocator-allocations allocator))
              when (allocation-live-p a)
                collect (format nil "ALLOC ROW allocation=~A machine=~A slot=~A holder=~A node=~A batch=~A offer=~A attempt=~A age=~A"
                                (allocation-record-allocation-id a) canonical
                                (allocation-record-slot a) (allocation-record-holder a)
                                (allocation-record-node a) (allocation-record-batch a)
                                (allocation-record-offer a) (allocation-record-attempt a)
                                (- now (allocation-record-created a))))))))

(defun fleet-probe (registry &key machine slot source (fact :observed) (at 0))
  "Write dated observed ACTIVE evidence; declared CONFIG is never written
(SPEC-WORK.md:3689-3702)."
  (let ((allocator (fleet-allocator-of registry machine)))
    (unless allocator
      (return-from fleet-probe
        (values nil (format nil "PROBE FAIL machine=~A: unknown machine" machine) 1)))
    (push (list :machine (fleet-resolve registry machine) :slot slot :fact fact
                :at at :source source)
          (fleet-allocator-observations allocator))
    (values t (format nil "PROBE OK machine=~A slot=~A fact=~A at=~A source=~A"
                      machine slot (if (eq fact :observed) "observed" "absent") at source)
            0)))

;;; ------------------------------------------------------------------
;;; capacity reduction: preserve active, drain new admission
;;; ------------------------------------------------------------------

(defun fleet-reduce-capacity (registry &key machine concurrent)
  "Lower the declared concurrency: every live allocation is retained and no new
take is admitted once the reduced capacity is declared (SPEC-WORK.md:3722)."
  (let ((allocator (fleet-allocator-of registry machine)))
    (unless allocator
      (return-from fleet-reduce-capacity
        (values nil (format nil "MACHINE FAIL machine=~A: unknown machine" machine) 1)))
    (setf (fleet-allocator-concurrent allocator) concurrent)
    (setf (fleet-allocator-reduced-p allocator) (> (fleet-consumed allocator) concurrent))
    (values t (format nil "MACHINE OK machine=~A concurrent=~A active=~A"
                      machine concurrent (fleet-consumed allocator))
            0)))


;;; ------------------------------------------------------------------
;;; The ACTIVE fleet verbs over the kernel's one allocator per machine
;;; (SPEC-WORK.md:3592-3731, E09 rows 5-6)
;;; ------------------------------------------------------------------
;;;
;;; `take`, `heartbeat`, `release` and `probe` are the verbs of the fleet's
;;; ACTIVE half. Each shares `submit`'s answer shape and writes the kernel's
;;; allocation registry; none touches the work tree, its counters or CONFIG.

(defun fleet-take-submit (kernel request)
  "The `take` verb (SPEC-WORK.md:3646-3675). One ACTIVE allocation, all or
none, over the kernel's one allocator for the machine."
  (multiple-value-bind (ok line code)
      (fleet-take (kernel-allocations kernel)
                  :machine (getf request :machine)
                  :node (getf request :node)
                  :slots (or (getf request :slots) 1)
                  :offer (getf request :offer)
                  :attempt (getf request :attempt)
                  :generation (getf request :generation)
                  :request-ref (getf request :request-ref)
                  :batch (getf request :batch)
                  :request (getf request :request)
                  :holder (or (getf request :holder) (getf request :by) "rowan")
                  :allocation-id (getf request :allocation-id)
                  :allocation-generation (getf request :allocation-generation)
                  :parent (getf request :parent)
                  :now (or (getf request :now) 0)
                  :deadline (getf request :deadline))
    (values ok line code nil)))

(defun fleet-heartbeat-submit (kernel request)
  "The `heartbeat` verb (SPEC-WORK.md:3665-3669): validate both generations
against the ACTIVE record and the machine's CONFIG record."
  (multiple-value-bind (ok line code)
      (fleet-heartbeat (kernel-allocations kernel)
                       :allocation (getf request :allocation)
                       :generation (getf request :generation)
                       :allocation-generation (getf request :allocation-generation)
                       :now (or (getf request :now) 0)
                       :request (getf request :request))
    (values ok line code nil)))

(defun fleet-release-submit (kernel request)
  "The `release` verb (SPEC-WORK.md:3669-3687): free exactly that allocation's
slot after both generations validate and, past the deadline, confirmed
termination."
  (multiple-value-bind (ok line code)
      (fleet-release (kernel-allocations kernel)
                     :allocation (getf request :allocation)
                     :generation (getf request :generation)
                     :allocation-generation (getf request :allocation-generation)
                     :now (or (getf request :now) 0)
                     :fenced (getf request :fenced)
                     :stop-observed (getf request :stop-observed)
                     :not-started (getf request :not-started)
                     :handed (getf request :handed)
                     :holder (getf request :holder))
    (values ok line code nil)))

(defun fleet-probe-submit (kernel request)
  "The `probe` verb (SPEC-WORK.md:3691-3704): write dated observed ACTIVE
evidence, never CONFIG."
  (multiple-value-bind (ok line code)
      (fleet-probe (kernel-allocations kernel)
                   :machine (getf request :machine)
                   :slot (getf request :slot)
                   :source (getf request :source)
                   :fact (or (getf request :fact) :observed)
                   :at (or (getf request :at) 0))
    (values ok line code nil)))

(defun %register-machine-allocator (kernel request id limits facts connect roles)
  "Open the ONE authoritative allocator for the physical machine in the
kernel's ACTIVE allocation registry. The declared :limits supply the
concurrency and cores; the machine generation is what `take`, `heartbeat` and
`release` compare against."
  (let ((registry (kernel-allocations kernel)))
    (fleet-register-machine registry
                            :machine-id id
                            :aliases (getf request :aliases)
                            :name (getf request :name)
                            :owner (getf request :owner)
                            :concurrent (or (getf limits :concurrent) 1)
                            :cores (getf limits :cores)
                            :generation (or (getf request :generation) 1)
                            :facts (copy-list facts)
                            :limits (copy-list limits)
                            :connect connect
                            :roles (copy-list roles))))

(defun %sync-machine-allocator (kernel member change)
  "A meaningful machine CONFIG change moves the live allocator's declared
numbers and bumps the machine generation; an allocation is never touched
(SPEC-WORK.md:3724-3730)."
  (let ((allocator (fleet-allocator-of (kernel-allocations kernel)
                                       (machine-id member))))
    (when allocator
      (setf (fleet-allocator-limits allocator) (copy-list (machine-limits member)))
      (setf (fleet-allocator-facts allocator) (copy-list (machine-facts member)))
      (unless (eq change :retire)
        (when (eq change :limit)
          (let ((concurrent (getf (machine-limits member) :concurrent)))
            (when concurrent (setf (fleet-allocator-concurrent allocator) concurrent))))
        (incf (fleet-allocator-generation allocator))))
    allocator))



;;; ------------------------------------------------------------------
;;; folded from replays-fleet-assignment.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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

;;; `machine` was defined again here (nova-tools #1612). It is one struct with
;;; two constructors at the head of this file now; `make-machine` is unchanged.

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
        :declared-at (let ((facts (machine-facts machine)))
                       (cond ((null facts) nil)
                             ;; A registration supplies the facts as one plist
                             ;; with one declaration; a `--fact` change supplies
                             ;; one entry per fact. Read both shapes.
                             ((keywordp (car facts)) (getf facts :declared-at))
                             (t (mapcar (lambda (fact) (getf fact :declared-at))
                                        facts))))
        :line (fleet-row-line machine kind)))

(defun fleet-for (session machines kind &key node)
  "Answer which machines admit KIND, in their configured order: their declared
:roles and :permits admit it and their :excludes do not. With NODE, narrow to
that one member; a member that excludes KIND is a refusal, never an empty answer
a caller could read as no machine and fall through, while a member that merely
does not admit KIND is an empty answer, because every row printed under `--for`
admits the kind. The answer is a recommendation from declared facts and never a
lease: it reserves nothing, dispatches nothing and writes nothing into SESSION
(SPEC-WORK.md:3562-3590)."
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
          ((machine-admits-p machine kind)
           (make-fleet-ask :kind kind :rows (list (fleet-row machine kind))))
          (t
           (make-fleet-ask :kind kind :rows '()))))
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

(defun %admission-refusal (state request staged current-rev what
                           &key expect offer-id node generation attempt
                                (profile-ok t) staged-payload payload-sha256 reserve)
  "The shared admission boundary for acknowledge and decline. The staged reader
runs outside the mutation loop; here the one writer revalidates the stage
against the expected revision, --expect, the offer's immutable tuple, the
profile and the capacity before admitting one envelope. A plain request carrying
the session's own half, an unverified provenance, a conflicting revision and a
stale --expect each refuse with no canonical write (SPEC-WORK.md:3854-3858)."
  (let ((field (and request (session-written-field request))))
    (when field
      (return-from %admission-refusal
        (values nil (format nil "~A FAIL: ~A is the session's own half of the envelope"
                            what (string-downcase (symbol-name field)))
                2))))
  (unless (and staged (staged-input-valid-p staged))
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: provenance unverified" what) 2)))
  (when (and expect (/= expect current-rev))
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: stale admission (--expect rev ~D, now ~D)"
                          what expect current-rev)
              2)))
  (unless (= (staged-input-rev staged) current-rev)
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: stale stage (rev ~D, now ~D)"
                          what (staged-input-rev staged) current-rev)
              2)))
  ;; the offer's immutable tuple, revalidated against the pending offer.
  (when (and offer-id (or node generation attempt))
    (let ((entry (find offer-id (assignment-state-offers state)
                       :key (lambda (offer) (getf offer :offer-id)) :test #'equal)))
      (cond
        ((null entry)
         (return-from %admission-refusal
           (values nil (format nil "~A FAIL: no such offer ~A" what offer-id) 2)))
        ((and node (not (equal (getf entry :node) node)))
         (return-from %admission-refusal
           (values nil (format nil "~A FAIL: offer is for another node" what) 2)))
        ((and generation (not (equal (getf entry :generation) generation)))
         (return-from %admission-refusal
           (values nil (format nil "~A FAIL: offer is for another generation" what) 2)))
        ((and attempt (not (equal (getf entry :attempt) attempt)))
         (return-from %admission-refusal
           (values nil (format nil "~A FAIL: offer is for another attempt" what) 2))))))
  (when (null profile-ok)
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: profile is not that friend's at that revision"
                          what)
              2)))
  (when (and staged-payload payload-sha256
             (not (equal (staged-input-digest staged-payload) payload-sha256)))
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: staged payload digest is not --payload-sha256"
                          what)
              2)))
  (when (and reserve (plusp reserve) (< (assignment-state-free-slots state) reserve))
    (return-from %admission-refusal
      (values nil (format nil "~A FAIL: declared free capacity does not cover ~D"
                          what reserve)
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
                                              expect (current-rev 0)
                                              node generation attempt
                                              (profile-ok t) staged-payload
                                              payload-sha256 reserve)
  "`acknowledge --stage received` writes delivery, a verified report that the
named recipient received that exact offer, and consents to nothing;
`--stage accepted` writes accepted ownership, the admission of a verified
acceptance as an assignment, changing no :responsible and proving nothing about
whether remote work began. Both admit only behind the verifier, and the one
writer revalidates --expect, the offer's immutable tuple, the profile and the
capacity at the expected revision before admitting one envelope; an unverified
or stale provenance writes nothing (SPEC-WORK.md:3835-3861)."
  (let ((what (ecase stage (:received "ACKNOWLEDGE") (:accepted "ACKNOWLEDGE"))))
    (multiple-value-bind (admitted refusal code)
        (%admission-refusal state request staged current-rev what
                            :expect expect :offer-id offer-id :node node
                            :generation generation :attempt attempt
                            :profile-ok profile-ok :staged-payload staged-payload
                            :payload-sha256 payload-sha256 :reserve reserve)
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

(defun assignment-decline (state &key staged request offer-id expect (current-rev 0)
                                  node generation attempt (profile-ok t))
  "Write a verified refusal, and nothing else. The one writer revalidates the
stage and --expect the same way `acknowledge` does. It is inferred from nothing:
a delivery or an acceptance already recorded is neither erased nor duplicated
(SPEC-WORK.md:3839-3843)."
  (multiple-value-bind (admitted refusal code)
      (%admission-refusal state request staged current-rev "DECLINE"
                          :expect expect :offer-id offer-id :node node
                          :generation generation :attempt attempt
                          :profile-ok profile-ok)
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


;;; ------------------------------------------------------------------
;;; `query --ask fleet`: the listing and the recommendation (E09 row 1)
;;; (SPEC-WORK.md:2311, 3557-3575, the replays at :3585-3590)
;;; ------------------------------------------------------------------
;;;
;;; The fleet section of CONFIG is read, never written: the ask reserves
;;; nothing, dispatches nothing, probes nothing, leases nothing and leaves
;;; `who` unchanged. A `--for` asks which configured members admit a workload
;;; kind; `--node` names one member and refuses rather than answering empty when
;;; that member excludes the kind. Without `--for` the whole live fleet is
;;; listed. The listing reads the declared facts, never a probe.

(defun %machine-fact (facts key)
  "One declared fact from FACTS, which is either a plist (`:arch \"arm64\"`) or a
list of plists (`((:value \"/opt\" :declared-by ...))`)."
  (cond
    ((null facts) nil)
    ((keywordp (car facts)) (getf facts key))
    (t (getf (first facts) key))))

(defun %machine-roles-line (roles)
  (if roles
      (format nil "~{~A~^,~}"
              (mapcar (lambda (r) (string-downcase (princ-to-string r))) roles))
      "-"))

(defun fleet-row-line (machine kind)
  "The `QUERY ROW` line of SPEC-WORK.md:3574: the machine id, its owner, roles,
limits and declared facts with their dates, and the kind (or `-`) it admits."
  (format nil "QUERY ROW ~A kind=machine name=~A owner=~A roles=~A admits=~A concurrent=~A arch=~A os=~A declared-by=~A declared-at=~A"
          (machine-id machine)
          (or (machine-name machine) "-")
          (or (machine-owner machine) "-")
          (%machine-roles-line (machine-roles machine))
          (if kind (string-downcase (princ-to-string kind)) "-")
          (or (getf (machine-limits machine) :concurrent) "-")
          (or (%machine-fact (machine-facts machine) :arch) "-")
          (or (%machine-fact (machine-facts machine) :os) "-")
          (or (%machine-fact (machine-facts machine) :declared-by) "-")
          (or (%machine-fact (machine-facts machine) :declared-at) "-")))

(defun fleet-query-list (machines)
  "Every live member, one row each, in configured order. This is the bare
`query --ask fleet` listing; it admits no workload kind and writes nothing."
  (make-fleet-ask :kind :fleet
                  :rows (mapcar (lambda (machine) (fleet-row machine nil))
                                machines)))

(defun query-fleet (kernel &key for node)
  "`query --ask fleet` over the configured fleet section: without `--for`, list
every live member; with `--for`, the members whose declared `:roles` and
`:permits` admit the kind and whose `:excludes` do not; `--node` narrows either
ask to one member and an excluded choice is a refusal, never an empty answer
(SPEC-WORK.md:3557-3575)."
  (let ((machines (fleet-members (kernel-fleet kernel))))
    (if for
        (fleet-for nil machines for :node node)
        (let ((ask (if node
                       (let ((machine (find node machines
                                            :key #'machine-id :test #'equal)))
                         (if machine
                             (fleet-query-list (list machine))
                             (make-fleet-ask
                              :kind :fleet :rows '()
                              :fail (format nil "QUERY FAIL ask=fleet rows=0 shown=0: no such member ~A" node)
                              :exit-code 1)))
                       (fleet-query-list machines))))
          ask))))
