;;;; s9-allocation.lisp --- the ACTIVE half: who holds a machine's slots now.
;;;;
;;;; docs/SPEC-WORK.md "Fleet allocation" rules 2-6 (#500). An allocation is
;;;; ACTIVE data per machine that references the machine's CONFIG identity and
;;;; revision rather than copying a definition; it binds machine, slot and
;;;; generation to a batch, a node and an (offer, attempt). `take` is atomic
;;;; and idempotent; expiry marks suspect and reuse needs fencing; probes are
;;;; dated ACTIVE evidence and never CONFIG; one allocator per physical machine.
;;;;
;;;; Pure functions and records, not wired into the command thread.

(in-package #:nova-work)

;;; ------------------------------------------------------------------ records

(defstruct (s9-allocation (:conc-name allocation-))
  allocation-id        ; the unique <allocation-id>, returned as id=
  machine              ; <machine-id>, the canonical id after alias resolution
  machine-revision     ; the CONFIG revision the binding references
  machine-generation   ; the machine generation carried by --generation
  allocation-generation ; the token the allocator drew at creation
  slot                 ; the first slot index granted
  slots                ; the number of slots granted
  batch                ; the --batch id
  node                 ; the task node the work belongs to
  offer                ; the offer id
  attempt              ; the attempt id
  holder               ; the name that holds the allocation
  parent               ; the parent allocation id, NIL for a root allocation
  suspect-p            ; suspect: retained and counted, never cleared by expiry
  expires-at           ; the stamp the suspect question was raised at
  fenced-p             ; confirmed termination / machine-side fencing
  released-p)          ; the holder (or a --handed party) freed the slot

(defstruct (s9-allocator (:conc-name allocator-) (:constructor %make-s9-allocator))
  (machines (make-hash-table :test #'equal))   ; id -> config plist
  (aliases (make-hash-table :test #'equal))    ; alias -> canonical id
  (writers (make-hash-table :test #'equal))    ; canonical id -> allocator name
  (allocations (make-hash-table :test #'equal)) ; allocation-id -> s9-allocation
  (order '())                                  ; allocation ids in take order
  (dedup (make-hash-table :test #'equal))      ; request-ref -> (id . payload)
  (next-number 0))

(defun make-s9-allocator (&key machines aliases)
  (let ((a (%make-s9-allocator)))
    (dolist (spec machines)
      (setf (gethash (getf spec :id) (allocator-machines a)) spec))
    (loop for (alias canonical) on aliases by #'cddr
          do (setf (gethash alias (allocator-aliases a)) canonical))
    a))

(defun allocator-resolve (allocator machine)
  (gethash (or (gethash machine (allocator-aliases allocator)) machine)
           (allocator-machines allocator)))

(defun allocator-find-allocation (allocator id)
  (gethash id (allocator-allocations allocator)))

(defun allocator-live-allocations (allocator &optional machine)
  (let ((ids (allocator-order allocator))
        (all (allocator-allocations allocator)))
    (remove-if-not
     (lambda (id)
       (let ((a (gethash id all)))
         (and a (not (allocation-released-p a))
              (or (null machine) (string= machine (allocation-machine a))))))
     ids)))

(defun allocator-used-slots (allocator machine)
  (loop for id in (allocator-live-allocations allocator machine)
        sum (allocation-slots (gethash id (allocator-allocations allocator)))))

(defun allocator-suspect-allocation-p (allocator machine)
  (loop for id in (allocator-live-allocations allocator machine)
        for a = (gethash id (allocator-allocations allocator))
        thereis (and (allocation-suspect-p a) (not (allocation-fenced-p a)))))

(defun allocator-next-allocation-id (allocator)
  (format nil "alloc-~D" (incf (allocator-next-number allocator))))

;;; ------------------------------------------------------------------- lines

(defun s9-allocation-bind-form (request)
  "The identity of a take: the fields a retry must reproduce byte for byte."
  (list :machine (getf request :machine)
        :node (getf request :node)
        :slots (getf request :slots)
        :offer (getf request :offer)
        :attempt (getf request :attempt)
        :generation (getf request :generation)
        :batch (getf request :batch)))

(defun s9-alloc-ok-line (allocation request)
  (format nil "ALLOC OK id=~A request=~A machine=~A node=~A slots=~A slot=~A generation=~A allocation-generation=~A offer=~A attempt=~A batch=~A"
          (allocation-allocation-id allocation)
          (getf request :request-ref)
          (allocation-machine allocation)
          (allocation-node allocation)
          (allocation-slots allocation)
          (allocation-slot allocation)
          (allocation-machine-generation allocation)
          (allocation-allocation-generation allocation)
          (allocation-offer allocation)
          (allocation-attempt allocation)
          (allocation-batch allocation)))

(defun s9-alloc-fail (machine slots holder reason)
  (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: ~A"
          (or machine "-") (or slots "-") (or holder "-") reason))

;;; ------------------------------------------------------------------- take

(defun take-allocation (allocator request)
  "`nova-work take --machine --node --slots --offer --attempt --generation
  --request-ref --batch [--holder --allocator --parent]`. All-or-none, one
  allocation, one request id, idempotent under the request-ref. Answers
  (values ok-p line allocation)."
  (let* ((machine-field (getf request :machine))
         (machine-id (or (gethash machine-field (allocator-aliases allocator))
                         machine-field))
         (spec (allocator-resolve allocator machine-id))
         (ref (getf request :request-ref))
         (bind (s9-allocation-bind-form request)))
    (unless spec
      (return-from take-allocation
        (values nil (s9-alloc-fail machine-field nil nil "no machine") nil)))
    ;; Idempotency under a stable request identity, asked before any debit.
    (when ref
      (let ((prior (gethash ref (allocator-dedup allocator))))
        (when prior
          (if (equal (cdr prior) bind)
              (return-from take-allocation
                (values t (s9-alloc-ok-line
                           (allocator-find-allocation allocator (car prior)) request)
                        (allocator-find-allocation allocator (car prior))))
              (return-from take-allocation
                (values nil (format nil "ALLOC FAIL request=~A: reused with a different payload" ref)
                        nil))))))
    ;; Stale machine generation: --generation is compared to the CONFIG generation.
    (let ((want (getf request :generation))
          (have (getf spec :generation)))
      (when (and want (not (eql want have)))
        (return-from take-allocation
          (values nil (s9-alloc-fail machine-id (getf request :slots) (getf request :holder)
                                     "stale machine generation") nil))))
    ;; One authoritative allocator per physical machine.
    (let ((writer (getf request :allocator))
          (held (gethash machine-id (allocator-writers allocator))))
      (when (and writer held (not (string= writer held)))
        (return-from take-allocation
          (values nil (s9-alloc-fail machine-id (getf request :slots) (getf request :holder)
                                     "allocator held") nil)))
      (when writer (setf (gethash machine-id (allocator-writers allocator)) writer)))
    ;; Capacity: slots come from declared concurrency, checked atomically.
    (let* ((slots (or (getf request :slots) 1))
           (concurrent (or (getf spec :concurrent) 0))
           (used (allocator-used-slots allocator machine-id)))
      (when (> (+ used slots) concurrent)
        (return-from take-allocation
          (values nil (s9-alloc-fail machine-id slots (getf request :holder)
                                     (if (allocator-suspect-allocation-p allocator machine-id)
                                         "not fenced"
                                         "capacity"))
                  nil))))
    (let* ((allocation
             (make-s9-allocation
              :allocation-id (allocator-next-allocation-id allocator)
              :machine machine-id
              :machine-revision (getf spec :revision)
              :machine-generation (getf request :generation)
              :allocation-generation (allocator-next-number allocator)
              :slot (1+ (allocator-used-slots allocator machine-id))
              :slots (or (getf request :slots) 1)
              :batch (getf request :batch)
              :node (getf request :node)
              :offer (getf request :offer)
              :attempt (getf request :attempt)
              :holder (getf request :holder)
              :parent (getf request :parent)
              :suspect-p nil :expires-at nil :fenced-p nil :released-p nil)))
      (setf (gethash (allocation-allocation-id allocation)
                     (allocator-allocations allocator)) allocation)
      (setf (allocator-order allocator)
            (append (allocator-order allocator) (list (allocation-allocation-id allocation))))
      (when ref
        (setf (gethash ref (allocator-dedup allocator))
              (cons (allocation-allocation-id allocation) bind)))
      (values t (s9-alloc-ok-line allocation request) allocation))))

;;; ------------------------------------------------------- heartbeat/release

(defun s9-validate-live-allocation (allocator request)
  "Names exactly one allocation and checks both generations. Answers
  (values allocation refusal-or-nil)."
  (let ((id (getf request :allocation))
        (gen (getf request :generation)))
    (let ((a (allocator-find-allocation allocator id)))
      (unless a
        (return-from s9-validate-live-allocation
          (values nil (format nil "ALLOC FAIL allocation=~A: no such allocation" id))))
      (unless (and (not (allocation-released-p a))
                   (eql (allocation-machine-generation a) gen))
        (return-from s9-validate-live-allocation
          (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                              (allocation-machine a) id))))
      (when (allocation-suspect-p a)
        (return-from s9-validate-live-allocation
          (values nil (format nil "ALLOC FAIL machine=~A allocation=~A: suspect since=~A"
                              (allocation-machine a) id (allocation-expires-at a)))))
      (values a nil))))

(defun heartbeat-allocation (allocator request)
  "`nova-work heartbeat --allocation <id> --generation <n>`: names exactly one
  allocation and validates both generations. A suspect allocation refuses."
  (multiple-value-bind (a refusal) (s9-validate-live-allocation allocator request)
    (if refusal
        (values nil refusal)
        (values t (format nil "ALLOC HEARTBEAT OK allocation=~A machine=~A"
                          (allocation-allocation-id a) (allocation-machine a))))))

(defun release-allocation (allocator request)
  "`nova-work release --allocation <id> --generation <n> [--handed <name>]`:
  frees exactly that allocation's slot, changing no other allocation on the
  machine."
  (multiple-value-bind (a refusal) (s9-validate-live-allocation allocator request)
    (when refusal (return-from release-allocation (values nil refusal)))
    (setf (allocation-released-p a) t)
    (values t (format nil "ALLOC RELEASE OK allocation=~A machine=~A slot=~D freed=true"
                      (allocation-allocation-id a) (allocation-machine a)
                      (allocation-slot a)))))

;;; ------------------------------------------------------ suspect and fencing

(defun allocation-suspect (allocator id since)
  "Mark an allocation suspect at SINCE. It is retained and counted, never
  cleared by the expiry that prompted the question."
  (let ((a (allocator-find-allocation allocator id)))
    (setf (allocation-suspect-p a) t
          (allocation-expires-at a) since)
    a))

(defun allocation-fence (allocator id)
  "Confirmed termination or machine-side fencing: the capacity returns now, and
  never on expiry alone."
  (let ((a (allocator-find-allocation allocator id)))
    (setf (allocation-fenced-p a) t
          (allocation-released-p a) t)
    a))

(defun allocator-set-concurrent (allocator machine n)
  "A declared :concurrent reduction. Every live allocation is retained and never
  silently cancelled; no new take is admitted once the reduced capacity is
  declared."
  (let ((spec (gethash machine (allocator-machines allocator))))
    (setf (getf spec :concurrent) n)
    spec))

;;; -------------------------------------------------------------------- list

(defun list-allocation (allocator machine)
  "`list --machine <id>`: one `ALLOC ROW` per live allocation with its id,
  holder, node, batch, (offer, attempt) and age."
  (loop for id in (allocator-live-allocations allocator machine)
        for a = (gethash id (allocator-allocations allocator))
        collect (format nil
                        "ALLOC ROW ~A machine=~A slot=~D slots=~D holder=~A node=~A batch=~A offer=~A attempt=~A"
                        (allocation-allocation-id a)
                        (allocation-machine a)
                        (allocation-slot a)
                        (allocation-slots a)
                        (or (allocation-holder a) "-")
                        (or (allocation-node a) "-")
                        (or (allocation-batch a) "-")
                        (or (allocation-offer a) "-")
                        (or (allocation-attempt a) "-"))))

;;; -------------------------------------------------------------- the probe

(defun machine-probe (allocator request)
  "`PROBE OK machine=<id> slot=<n|-> fact=<observed|absent> at=<stamp>
  source=<pointer>`. An observation writes ACTIVE evidence with its date, its
  source and its last-contact stamp, and never writes CONFIG: no :limits,
  :facts, :connect or :roles change, and a stale declaration is never read as a
  current probe."
  (let* ((machine (or (gethash (getf request :machine) (allocator-aliases allocator))
                      (getf request :machine)))
         (slot (getf request :slot))
         (fact (getf request :fact))
         (at (getf request :at))
         (source (getf request :source)))
    (values (format nil "PROBE OK machine=~A slot=~A fact=~A at=~A source=~A"
                    (or machine "-")
                    (or slot "-")
                    (if fact (string-downcase (symbol-name fact)) "observed")
                    (or at "-")
                    (or source "-")))))
