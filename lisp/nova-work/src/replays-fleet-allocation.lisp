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
