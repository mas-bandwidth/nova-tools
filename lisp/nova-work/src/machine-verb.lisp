;;;; machine-verb.lisp --- `machine --register/--retire` as journaled commands.
;;;;
;;;; THE DEFECT THIS REPAIRS. `machine --register` (and `--retire`, `--permit`,
;;;; `--exclude`, `--limit`, `--fact`) wrote the fleet member record and opened
;;;; its ACTIVE allocator from the CALLER's thread, with no event, no envelope,
;;;; no journal record, no request id and no revision, and only then did
;;;; `%submit` reach `journal-lookup`/`journal-accept`/`journal-record`. That is
;;;; three rules at once:
;;;;
;;;;   * SPEC-WORK.md:1056-1058 -- the `fleet` section is an index "under the
;;;;     one writer and the one journal";
;;;;   * SPEC-WORK.md:327 -- a mutation is appended to the journal and made
;;;;     durable BEFORE it is applied;
;;;;   * SPEC-WORK.md:2603-2616 (rule 6) -- "a mutation outside the command
;;;;     loop is a defect".
;;;;
;;;; The consequences were not academic: a register was invisible to the total
;;;; order the journal's sequence numbers ARE, it did not survive
;;;; `replay-journal` (a reconstructed kernel had an empty fleet), and it had no
;;;; two-part dedup (:315), so a retried register registered again.
;;;;
;;;; THE REPAIR is the one this kernel already has for every other writer verb
;;;; (`take` in src/take-verb.lisp is the model): `:machine` is a `work-event`
;;;; submitted through `submit`, dispatched by `%submit` to
;;;; `%machine-verb-submit`, and installed by `%oneshot-submit` ->
;;;; `%install-envelope` -> `apply-envelope` -> `apply-event`: accept, RECORD,
;;;; then apply, all-or-none, on the one command thread. This file mirrors
;;;; `%take-node-submit` step for step, including the order that is easy to get
;;;; wrong -- the DURABLE REPLAY ANSWER is read from the journal before any
;;;; mutable precondition is read from the state.
;;;;
;;;; The event is a real `work-event`: its subject is a machine identity, so
;;;; `:node` is `(:absent)` and no count, roadmap or required set moves when it
;;;; is written (SPEC-WORK.md:1046-1049, :3484-3486, :3617-3630). It is
;;;; applied by `apply-event`'s `:machine` branch, which writes the CONFIG and
;;;; advances the revision and nothing else.

(in-package #:nova-work)

(defparameter *machine-changes* '(:register :retire :permit :exclude :limit :fact)
  "The six static-configuration changes of SPEC-WORK.md:3541. A heartbeat, an
`observe` and a probe are no part of CONFIG and refuse rather than guess.")

(defun %machine-author (request)
  "The event's author, read from the request and never a constant. The spec's
machine event order has no `:by`; the owner whose word admits the record is the
author, with the declaring name and then a fixed fallback."
  (or (getf request :by) (getf request :owner) (getf request :declared-by) "rowan"))

(defun %machine-request-refusal (request)
  "An invocation the machine verb cannot read, or NIL. Exit 2 is the code.

The request id is REQUIRED, as it is for every journaled command: the two-part
dedup of SPEC-WORK.md:315 is the caller's id or it is nothing, and a kernel that
invents one has invented the answer to its own retry."
  (let ((change (getf request :change))
        (rid (getf request :request)))
    (cond
      ((not (member change *machine-changes*))
       (format nil "unsupported change ~A"
               (string-downcase (princ-to-string (or change "?")))))
      ((not (and (stringp rid) (plusp (length rid)))) "refusing to guess: --request")
      (t nil))))

(defun %machine-canonical-event (kernel request change)
  "The one `:machine` CONFIG event for CHANGE, as a real `work-event`.

The digest is taken over :kind, :node, :by and the kind's own ordered fields and
NEVER over the revision (src/event.lisp `event-digest-form`), so one request
digests the same however far the fleet has moved since it was recorded. That is
what makes the durable replay answer answerable from the journal alone.

The spec's ordered field list for `:machine` (SPEC-WORK.md:1046-1049) carries no
slot for the record's declared `:permits`, `:excludes`, `:limits` and `:facts`.
A `:register` carries them whole in `:value` so a replay rebuilds the member
exactly, rather than a member that lost its declared limits."
  (let ((id (getf request :machine)))
    (make-work-event
     :kind :machine
     :node (machine-node-field nil)
     :by (%machine-author request)
     :fields (case change
               (:register
                (list :change :register
                      :machine id
                      :name (getf request :name)
                      :owner (getf request :owner)
                      :connect (getf request :connect)
                      :roles (copy-list (getf request :roles))
                      :value (list :permits (copy-list (getf request :permits))
                                   :excludes (copy-list (getf request :excludes))
                                   :limits (copy-list (getf request :limits))
                                   :facts (copy-tree (getf request :facts))
                                   :aliases (copy-list (getf request :aliases))
                                   :generation (getf request :generation))))
               (:retire (list :change :retire :machine id))
               ((:permit :exclude) (list :change change :machine id
                                         :workload (getf request :workload)))
               (:limit (list :change :limit :machine id
                             :key (getf request :key) :value (getf request :value)))
               (:fact (list :change :fact :machine id
                            :key (getf request :key) :value (getf request :value)
                            :declared-by (getf request :declared-by))))
     :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
     :clock (or (getf request :clock) :tool)
     :request (getf request :request)
     :generation-owner (or (getf request :generation-owner) (%machine-author request))
     :rev (kernel-next-rev kernel)
     :session-written-p nil)))

(defun %derived-machine-request-id (kernel id)
  "A request id for an in-process caller that named none. It is derived from the
kernel's own REVISION, which is durable: it comes back from the journal on
reconstruction, so an id minted after a restart cannot collide with one the
journal already holds. A process-local counter would reset with the process
while the journal does not. The `machine` verb itself REQUIRES `--request`."
  (format nil "machine-~D-~A" (kernel-next-rev kernel) id))

;;; ------------------------------------------------------------------
;;; The mutable preconditions, read only after the durable replay answer
;;; ------------------------------------------------------------------

(defun %machine-register-refusal (fleet request)
  "The register refusals of SPEC-WORK.md:3544-3546, in the order that paragraph
lists them, verbatim so the slice-10 cases keep passing."
  (let ((id (getf request :machine))
        (owner (getf request :owner))
        (connect (getf request :connect))
        (roles (getf request :roles))
        (facts (getf request :facts))
        (declared-by (getf request :declared-by)))
    (unless (and owner (stringp owner) (plusp (length owner)))
      (return-from %machine-register-refusal (%machine-fail id "no owner")))
    (unless (and id (stringp id) (plusp (length id)))
      (return-from %machine-register-refusal (%machine-fail nil "no id")))
    (unless (member owner (fleet-friends fleet) :test #'string=)
      (return-from %machine-register-refusal (%machine-fail id "unknown owner")))
    (dolist (role roles)
      (unless (member role *machine-roles*)
        (return-from %machine-register-refusal (%machine-fail id "unknown role"))))
    (when (or (not (%profile-reference-p connect))
              (some (lambda (key) (getf request key)) *machine-credential-keys*))
      (return-from %machine-register-refusal (%machine-fail id "credential in record")))
    (let ((held (find connect (fleet-members fleet)
                      :key #'machine-connect :test #'string=)))
      (when held
        (return-from %machine-register-refusal
          (%machine-fail id (format nil "connect held by ~A" (machine-id held))))))
    (when (and facts
               (not (and declared-by (stringp declared-by)
                         (plusp (length declared-by)))))
      (return-from %machine-register-refusal (%machine-fail id "fact without provenance")))
    (when (fleet-member fleet id)
      (return-from %machine-register-refusal
        (%machine-fail id (format nil "id held by ~A" id))))
    nil))

(defun %machine-precondition (kernel request change)
  "The mutable preconditions for CHANGE, or NIL when the request may be applied.
Returns the same (values OK-P LINE EXIT-CODE ENVELOPE) shape as `%submit`."
  (let* ((fleet (kernel-fleet kernel))
         (id (getf request :machine)))
    (case change
      (:register (%machine-register-refusal fleet request))
      ((:retire :permit :exclude :limit :fact)
       (let ((member (and id (fleet-member fleet id))))
         (unless member
           (return-from %machine-precondition (%machine-fail id "no such member")))
         (when (and (eq change :fact)
                    (not (let ((declared-by (getf request :declared-by)))
                           (and declared-by (stringp declared-by)
                                (plusp (length declared-by))))))
           (return-from %machine-precondition
             (%machine-fail id "fact without provenance")))
         nil))
      (otherwise
       (values nil
               (format nil "MACHINE FAIL machine=~A: unsupported change ~A"
                       (or id "-")
                       (string-downcase (princ-to-string (or change "?"))))
               2 nil)))))

;;; ------------------------------------------------------------------
;;; The apply, run by `apply-event` on the candidate state
;;; ------------------------------------------------------------------

(defun %machine-apply-event (config event)
  "Apply one `:machine` event to CONFIG and return it. Called from
`apply-event` on the candidate state, so the mutation is one journaled command
in the single writer's total order and comes back with `replay-journal`."
  (let* ((fields (work-event-fields event))
         (change (getf fields :change))
         (id (getf fields :machine))
         (fleet (work-config-fleet config))
         (allocations (work-config-allocations config)))
    (ecase change
      (:register
       (let* ((extra (getf fields :value))
              (member (%make-machine
                       :id id
                       :name (getf fields :name)
                       :owner (getf fields :owner)
                       :connect (getf fields :connect)
                       :roles (copy-list (getf fields :roles))
                       :permits (copy-list (getf extra :permits))
                       :excludes (copy-list (getf extra :excludes))
                       :limits (copy-list (getf extra :limits))
                       :facts (copy-tree (getf extra :facts)))))
         (setf (gethash id (fleet-machines fleet)) member)
         (push id (fleet-order fleet))
         ;; The machine is CONFIG; opening its one ACTIVE allocator is how a
         ;; slot later becomes takeable (SPEC-WORK.md:3707-3715).
         (%register-machine-allocator
          allocations
          (list :name (getf fields :name) :owner (getf fields :owner)
                :aliases (getf extra :aliases) :generation (getf extra :generation))
          id (getf extra :limits) (getf extra :facts)
          (getf fields :connect) (getf fields :roles))))
      (:retire
       (remhash id (fleet-machines fleet))
       (setf (fleet-order fleet) (remove id (fleet-order fleet) :test #'equal))
       ;; Today's behaviour exactly: retiring a machine drops its live
       ;; allocation with it (SPEC-WORK.md:3646-3675 says capacity "is retained
       ;; through verified release"; this card journals what is, not what should
       ;; be, and names the gap rather than fixing it silently).
       (remhash id (fleet-registry-allocators allocations)))
      ((:permit :exclude :limit :fact)
       (let ((member (fleet-member fleet id)))
         (when member
           (case change
             (:permit (pushnew (getf fields :workload) (machine-permits member)
                               :test #'equal))
             (:exclude (pushnew (getf fields :workload) (machine-excludes member)
                                :test #'equal))
             (:limit (setf (machine-limits member)
                           (let ((l (copy-list (machine-limits member))))
                             (setf (getf l (getf fields :key)) (getf fields :value))
                             l)))
             (:fact (setf (machine-facts member)
                          (list* (list :key (getf fields :key)
                                       :value (getf fields :value)
                                       :declared-by (getf fields :declared-by)
                                       :declared-at (work-event-stamp event))
                                 (remove (getf fields :key) (machine-facts member)
                                         :key (lambda (f) (getf f :key)) :test #'equal)))))
           (%sync-machine-allocator allocations member change))))))
  config)

;;; ------------------------------------------------------------------
;;; The receipt line and the verb
;;; ------------------------------------------------------------------

(defun %machine-receipt-line (event)
  "The `MACHINE OK` line, unchanged in shape from the CONFIG verb's: a register
names id=, request=, machine= and rev=; retire and the four changes keep their
short form (fleet.lisp:217, :240)."
  (let ((fields (work-event-fields event)))
    (if (eq :register (getf fields :change))
        (format nil "MACHINE OK id=~A request=~A machine=~A rev=~D pushed=- changed=1 emitted=0"
                (event-id event) (work-event-request event)
                (getf fields :machine) (work-event-rev event))
        (format nil "MACHINE OK machine=~A" (getf fields :machine)))))

(defun %machine-verb-submit (kernel request)
  "`machine --register/--retire/...`: one `:machine` CONFIG event, as a journaled
command on the one writer. Answers (values OK-P LINE EXIT-CODE ENVELOPE)
(SPEC-WORK.md:327, :3541). The four steps mirror `%take-node-submit` exactly."
  (let ((refusal (%machine-request-refusal request)))
    (when refusal
      (return-from %machine-verb-submit
        (values nil (format nil "MACHINE FAIL machine=~A: ~A; run: nova-work help"
                            (or (getf request :machine) "-") refusal)
                2 nil))))
  (let* ((change (getf request :change))
         (id (getf request :machine))
         (rid (getf request :request))
         (event (%machine-canonical-event kernel request change))
         (digest (payload-digest (list event))))
    ;; ---- the durable replay answer, before any mutable state --------------
    (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
      (when verdict
        (return-from %machine-verb-submit
          (if (eq verdict :replay)
              (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))
              (values nil (%dedup-refusal "MACHINE" rid verdict recorded) 1 nil)))))
    ;; ---- and only now the mutable preconditions ---------------------------
    (multiple-value-bind (pok pline pcode) (%machine-precondition kernel request change)
      (declare (ignore pok))
      (when pline
        (return-from %machine-verb-submit (values nil pline pcode nil))))
    ;; ---- the OK line from the event, then record-then-apply ----------------
    (let ((line (%machine-receipt-line event)))
      (%oneshot-submit kernel rid digest line event "MACHINE"
                       (list :verb :machine :change change :machine id
                             :request request)))))
