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

(defstruct (machine (:constructor %make-machine
                        (&key id name owner connect roles permits excludes limits facts)))
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

(defun %machine-register (kernel request)
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
                   :facts (copy-list facts))))
      (setf (gethash id (fleet-machines fleet)) member)
      (push id (fleet-order fleet))
      (values t (format nil "MACHINE OK machine=~A" id) 0 nil))))

(defun machine-submit (kernel request)
  "One `:machine` event. SPEC-WORK.md:3541 --- `:register` is the only change
this slice carries; a heartbeat, an `observe`, a probe and the other five
changes are no part of the static configuration and change no member. The
work tree is never touched: no count, roadmap or required set moves."
  (let ((change (getf request :change)))
    (case change
      (:register (%machine-register kernel request))
      (otherwise
       (values nil
               (format nil "MACHINE FAIL machine=~A: unsupported change ~A"
                       (or (getf request :machine) "-")
                       (string-downcase (princ-to-string (or change "?"))))
               2 nil)))))
