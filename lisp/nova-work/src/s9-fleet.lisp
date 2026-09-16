;;;; s9-fleet.lisp --- the fleet: machine records, the one machine verb, and the
;;;; two fleet asks.
;;;;
;;;; docs/SPEC-WORK.md "The fleet" (the `:kind :machine` member of a `fleet`
;;;; section of CONFIG) and "Fleet allocation" rule 1. A machine is a CONFIG
;;;; member of the desired half, never a work-tree node; it has no `:acceptance`,
;;;; no derived state, no `:to :done` and no settle, and every `:machine` event
;;;; writes `:node (:absent)`. The one verb configures it; two asks query it.
;;;;
;;;; This is the records, the parsers, the printed lines and the functions the
;;;; slice names — pure, beside slice 1, not wired into the command thread.

(in-package #:nova-work)

;;; ------------------------------------------------------------------ records

(defstruct (s9-machine (:conc-name machine-))
  (id nil)            ; the stable machine id: never reused, never display text
  (name nil)          ; display only, may change freely
  (owner nil)         ; a friend of `friends`, required
  (connect nil)       ; a `profile:<name>` reference, never a credential
  (roles '())         ; each one of :build, :test, :profile
  (permits '())       ; workload kinds admitted
  (excludes '())      ; workload kinds that must never run there
  (limits '())        ; :concurrent <n> and team-named resource limits
  (facts '())         ; hardware/OS facts with :declared-by and :declared-at
  (retired-p nil))

(defstruct (s9-fleet (:conc-name fleet-))
  (machines (make-hash-table :test #'equal))  ; id -> s9-machine
  (order '())        ; stable ids in configured order
  (friends '())      ; the `friends` an owner must be a member of
  (revision 0))

(defun fleet-live-machines (fleet)
  "Every non-retired member, in configured order. A retired record answers no
  query but the history."
  (remove-if #'machine-retired-p
             (mapcar (lambda (id) (gethash id (fleet-machines fleet)))
                     (fleet-order fleet))))

(defun fleet-find-machine (fleet id)
  (gethash id (fleet-machines fleet)))

(defun fleet-generate-id (fleet)
  (incf (fleet-revision fleet)))

;;; -------------------------------------------------------------- validation

(defparameter s9-valid-roles '(:build :test :profile))

(defun s9-profile-ref-p (connect)
  "True when CONNECT is a `profile:<name>` reference and never a credential."
  (and (stringp connect)
       (> (length connect) (length "profile:"))
       (string= "profile:" connect :end2 (length "profile:"))))

(defun s9-credential-field-p (key)
  (member key '(:key :token :password :secret)))

(defun s9-machine-refusal (fleet request)
  "Answer (values reason machine-id) when the register request must be refused,
  or NIL. Nothing is written on a refusal."
  (let* ((id (getf request :id))
         (owner (getf request :owner))
         (connect (getf request :connect))
         (roles (getf request :roles))
         (facts (getf request :facts '()))
         (declared-by (getf facts :declared-by))
         (connect-holder (and connect
                              (loop for m in (fleet-live-machines fleet)
                                    when (equal connect (machine-connect m))
                                      return (machine-id m)))))
    (cond
      ((or (null id) (and (stringp id) (string= id "")))
       (values "no id" "-"))
      ((or (null owner) (and (stringp owner) (string= owner "")))
       (values "no owner" id))
      ((not (member owner (fleet-friends fleet) :test #'string=))
       (values "unknown owner" id))
      (connect-holder
       (values (format nil "connect held by ~A" connect-holder) id))
      ((and connect (not (s9-profile-ref-p connect)))
       (values "credential in record" id))
      ((loop for (k) on request by #'cddr thereis (s9-credential-field-p k))
       (values "credential in record" id))
      ((some (lambda (r) (not (member r s9-valid-roles))) roles)
       (values "unknown role" id))
      ((and (or (getf request :facts) (getf request :declared-by))
            (or (null declared-by) (and (stringp declared-by) (string= declared-by ""))))
       (values "fact without provenance" id)))))

(defun s9-machine-event (machine change by request reason)
  "The :machine event a mutation writes. Its subject is a machine identity, not
  a node, so `:node` is `(:absent)` — equipment is never a work-tree node."
  (list :kind :machine
        :change change
        :machine (machine-id machine)
        :name (machine-name machine)
        :owner (machine-owner machine)
        :connect (machine-connect machine)
        :roles (machine-roles machine)
        :workload +absent+
        :key +absent+
        :value +absent+
        :declared-by +absent+
        :reason reason
        :node +absent+
        :by by
        :request request))

(defun s9-machine-ok-line (id request change machine-id)
  (format nil "MACHINE OK id=~A request=~A machine=~A change=~A rev=~D pushed=-"
          id request machine-id change id))

;;; ----------------------------------------------------------------- the verb

(defun machine-register (fleet request)
  "`nova-work machine --register <id> --name <text> --owner <name> --connect
  <ref> --role ... --reason <text>`. One `:machine` event; subject `:node
  (:absent)`. Answers (values ok-p line fleet event)."
  (multiple-value-bind (reason id) (s9-machine-refusal fleet request)
    (when reason
      (return-from machine-register
        (values nil
                (format nil "MACHINE FAIL machine=~A: ~A" id reason)
                fleet nil))))
  (let ((id (getf request :id)))
    (when (fleet-find-machine fleet id)
      (return-from machine-register
        (values nil (format nil "MACHINE FAIL machine=~A: duplicate id" id) fleet nil)))
    (let* ((machine (make-s9-machine
                     :id id
                     :name (getf request :name)
                     :owner (getf request :owner)
                     :connect (getf request :connect)
                     :roles (getf request :roles)
                     :permits (getf request :permits)
                     :excludes (getf request :excludes)
                     :limits (getf request :limits)
                     :facts (getf request :facts)
                     :retired-p nil))
           (rev (fleet-generate-id fleet)))
      (setf (gethash id (fleet-machines fleet)) machine)
      (setf (fleet-order fleet) (append (fleet-order fleet) (list id)))
      (values t
              (s9-machine-ok-line rev (getf request :request) "register" id)
              fleet
              (s9-machine-event machine 'register
                                (getf request :by) (getf request :request)
                                (getf request :reason))))))

(defun machine-retire (fleet id request)
  "`--retire <id>`: the record stays in the journal; it answers no query but the
  history. Nothing names a node."
  (let ((machine (fleet-find-machine fleet id)))
    (unless machine
      (return-from machine-retire
        (values nil (format nil "MACHINE FAIL machine=~A: no id" id) fleet nil)))
    (setf (machine-retired-p machine) t)
    (let ((rev (fleet-generate-id fleet)))
      (values t
              (s9-machine-ok-line rev (getf request :request) "retire" id)
              fleet
              (s9-machine-event machine 'retire
                                (getf request :by) (getf request :request)
                                (getf request :reason))))))

(defun s9-edit-machine (fleet id request change fn)
  "The shared shape of `--permit`, `--exclude`, `--limit` and `--fact`: apply FN
  to the member and write one `:machine` event with `:node (:absent)`."
  (let ((machine (fleet-find-machine fleet id)))
    (unless machine
      (return-from s9-edit-machine
        (values nil (format nil "MACHINE FAIL machine=~A: no id" id) fleet nil)))
    (funcall fn machine)
    (let ((rev (fleet-generate-id fleet)))
      (values t
              (s9-machine-ok-line rev (getf request :request) change id)
              fleet
              (s9-machine-event machine (intern change :keyword)
                                (getf request :by) (getf request :request)
                                (getf request :reason))))))

(defun machine-permit (fleet id kind request)
  (s9-edit-machine fleet id request "permit"
                   (lambda (m) (pushnew kind (machine-permits m) :test #'string=))))

(defun machine-exclude (fleet id kind request)
  (s9-edit-machine fleet id request "exclude"
                   (lambda (m) (pushnew kind (machine-excludes m) :test #'string=))))

(defun machine-limit (fleet id key value request)
  (s9-edit-machine fleet id request "limit"
                   (lambda (m) (setf (getf (machine-limits m) key) value))))

(defun machine-fact (fleet id key value declared-by request)
  (s9-edit-machine fleet id request "fact"
                   (lambda (m)
                     (setf (getf (machine-facts m) key) value
                           (getf (machine-facts m) :declared-by) declared-by
                           (getf (machine-facts m) :declared-at)
                           (getf request :declared-at)))))

;;; ------------------------------------------------------------------- asks

(defun s9-machine-admits-p (machine kind)
  "A recommendation from declared facts and never a lease: the kinds whose roles
  and permits admit and whose excludes do not. `:excludes` wins wherever the two
  name one kind — including the empty excludes case."
  (let ((excluded (member kind (machine-excludes machine) :test #'string=)))
    (if excluded
        :excluded
        (if (member kind (machine-permits machine) :test #'string=)
            :admit
            :not-permitted))))

(defun s9-machine-row (machine &optional for)
  "One `QUERY ROW` per machine: owner, roles, limits and dated declared facts."
  (let* ((limits (machine-limits machine))
         (facts (machine-facts machine)))
    (format nil "QUERY ROW ~A kind=machine name=~A owner=~A roles=~A admits=~A concurrent=~A arch=~A os=~A declared-by=~A declared-at=~A"
            (machine-id machine)
            (or (machine-name machine) "-")
            (or (machine-owner machine) "-")
            (s9-join-names (machine-roles machine))
            (if for for "-")
            (or (getf limits :concurrent) "-")
            (or (getf facts :arch) "-")
            (or (getf facts :os) "-")
            (or (getf facts :declared-by) "-")
            (or (getf facts :declared-at) "-"))))

(defun fleet-query (fleet &key for node)
  "`query --ask fleet` lists the fleet; `--for <kind>` answers which machines
  admit the workload; `--node <id>` narrows either ask to one member. An ask
  asked to choose a member for a workload it excludes is refused, never empty.
  Answers (values ok-p line rows)."
  (let ((machines (if node
                      (let ((m (fleet-find-machine fleet node)))
                        (when m (list m)))
                      (fleet-live-machines fleet))))
    (when (null machines)
      (return-from fleet-query
        (values nil (format nil "QUERY FAIL ask=fleet rows=0 shown=0: ~A no such machine" (or node "-")) '())))
    (cond
      (for
       ;; --for narrows to the admitting members.
       (when node
         (let ((verdict (s9-machine-admits-p (first machines) for)))
           (when (eq verdict :excluded)
             (return-from fleet-query
               (values nil
                       (format nil "QUERY FAIL ask=fleet rows=0 shown=0: ~A excludes ~A" node for)
                       '())))))
       (let ((admitting (remove-if-not (lambda (m) (eq (s9-machine-admits-p m for) :admit))
                                       machines)))
         (values t
                 (format nil "QUERY OK ask=fleet rows=~D shown=~D"
                         (length admitting) (length admitting))
                 (mapcar (lambda (m) (s9-machine-row m for)) admitting))))
      (t
       (values t
               (format nil "QUERY OK ask=fleet rows=~D shown=~D"
                       (length machines) (length machines))
               (mapcar #'s9-machine-row machines))))))
