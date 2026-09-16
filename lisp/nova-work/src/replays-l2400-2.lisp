;;;; replays-l2400-2.lisp --- the pure half of the SPEC-WORK.md:2400-3600
;;;; replays, batch 2 (lines 3409-3566). None of this edits kernel.lisp,
;;;; session.lisp or state.lisp; it is the bounded config exchange and the fleet
;;;; member rules each replay asserts, to be wired into those files when the
;;;; config and fleet slices land.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The fleet member record and its readers. Every field is the team's
;;; own configuration, never a product constant (SPEC-WORK.md:3451-3462).
;;; ------------------------------------------------------------------

(defun make-fleet-member (&key id name owner connect roles permits excludes limits facts)
  (list :kind :machine :id id :name name :owner owner :connect connect
        :roles roles :permits permits :excludes excludes
        :limits limits :facts facts))

(defun fleet-member-id (m) (getf m :id))
(defun fleet-member-name (m) (getf m :name))
(defun fleet-member-owner (m) (getf m :owner))
(defun fleet-member-connect (m) (getf m :connect))
(defun fleet-member-roles (m) (getf m :roles))
(defun fleet-member-permits (m) (getf m :permits))
(defun fleet-member-excludes (m) (getf m :excludes))
(defun fleet-member-limits (m) (getf m :limits))
(defun fleet-member-facts (m) (getf m :facts))

(defparameter *fleet-roles* '(:build :test :profile))

(defun fleet-role-p (role) (member role *fleet-roles*))

;;; ------------------------------------------------------------------
;;; no-machine-name-in-the-tool (SPEC-WORK.md:3457-3460)
;;; ------------------------------------------------------------------

(defparameter *product-machine-names* '()
  "The machine names this tool itself hardcodes. None: a tool that names a host
in source has already failed the test. The three members of the spec are
example data, not product constants.")

;;; ------------------------------------------------------------------
;;; no-credential-in-a-member (SPEC-WORK.md:3468-3470, 3527-3528)
;;; ------------------------------------------------------------------

(defun connection-profile-reference-p (connect)
  (and (stringp connect)
       (>= (length connect) (length "profile:"))
       (string= "profile:" (subseq connect 0 (length "profile:")))))

(defparameter *credential-field-names* '(:key :token :password :secret))

(defun fleet-member-has-credential-p (member)
  "A :connect that is not a profile: reference, or any field named key, token,
password or secret, is a credential and the record is refused whole."
  (loop for (k v) on member by #'cddr
        thereis (if (member k *credential-field-names*)
                    t
                    (and (eq k :connect)
                         (and v (not (connection-profile-reference-p v)))))))

;;; ------------------------------------------------------------------
;;; one-profile-one-unit and unknown-owner-is-refused
;;; (SPEC-WORK.md:3521-3531)
;;; ------------------------------------------------------------------

(defun friend-name-p (name friends) (member name friends :test #'string=))

(defun fact-without-provenance-p (member)
  (let ((facts (fleet-member-facts member)))
    (and facts
         (not (and (getf facts :declared-by) (getf facts :declared-at))))))

(defun connection-holder (fleet connect)
  (find-if (lambda (m) (string= connect (fleet-member-connect m))) fleet))

(defun register-fleet-member (fleet member friends)
  "Pure admission of MEMBER into FLEET. Returns (VALUES OK-P RESULT REASON).
RESULT is the new fleet on success and the old fleet on refusal. The refusals
of SPEC-WORK.md:3521-3531, each in the spec's words."
  (let ((id (fleet-member-id member))
        (owner (fleet-member-owner member)))
    (cond
      ((null id)
       (values nil fleet "no id"))
      ((null owner)
       (values nil fleet "no owner"))
      ((not (friend-name-p owner friends))
       (values nil fleet "unknown owner"))
      ((fleet-member-has-credential-p member)
       (values nil fleet "credential in record"))
      ((loop for role in (fleet-member-roles member)
             thereis (not (fleet-role-p role)))
       (values nil fleet "unknown role"))
      ((fact-without-provenance-p member)
       (values nil fleet "fact without provenance"))
      ((and (fleet-member-connect member)
            (connection-holder fleet (fleet-member-connect member)))
       (values nil fleet
               (format nil "connect held by ~A"
                       (fleet-member-id
                        (connection-holder fleet (fleet-member-connect member))))))
      (t
       (values t (append fleet (list member)) nil)))))

;;; ------------------------------------------------------------------
;;; fleet-is-static-config (SPEC-WORK.md:3563)
;;; ------------------------------------------------------------------

(defun fleet-observe (fleet observation)
  "A heartbeat, an observe or a probe is not a configuration change; the member
set is untouched (SPEC-WORK.md:3455-3457)."
  (declare (ignore observation))
  fleet)

(defun fleet-transition (fleet event work-open roadmap)
  "A :machine event is config and not work: it may change the member set but it
moves no work count and no roadmap; a heartbeat, an observe or a probe changes
no member at all."
  (cond
    ((member (getf event :kind) '(:heartbeat :observe :probe))
     (values fleet work-open roadmap))
    ((eq (getf event :kind) :machine)
     (values (append fleet (list (getf event :member))) work-open roadmap))
    (t (values fleet work-open roadmap))))

;;; ------------------------------------------------------------------
;;; Config exchange, bounded (SPEC-WORK.md:3409-3422)
;;; ------------------------------------------------------------------

(defun make-config-manifest (&key schema friend revision routes content-hash)
  (list :schema schema :friend friend :revision revision
        :routes routes :content-hash content-hash))

(defun manifest-schema (m) (getf m :schema))
(defun manifest-friend (m) (getf m :friend))
(defun manifest-revision (m) (getf m :revision))
(defun manifest-routes (m) (getf m :routes))
(defun manifest-content-hash (m) (getf m :content-hash))

(defun config-content-hash (manifest)
  "The content hash covers the routes alone, so a dropped or changed route moves
the hash and any part count."
  (sha256-hex (canonical-string (manifest-routes manifest))))

(defun config-identity (manifest)
  (list :friend (manifest-friend manifest)
        :revision (manifest-revision manifest)
        :content-hash (manifest-content-hash manifest)))

(defun config-unchanged-p (request manifest)
  (and (string= (getf request :friend) (manifest-friend manifest))
       (eql (getf request :last-revision) (manifest-revision manifest))
       (string= (getf request :last-content-hash) (manifest-content-hash manifest))))

(defun config-exchange (request manifest)
  "One bounded answer: :unchanged with the identity when equal, else the
manifest (SPEC-WORK.md:3409-3411)."
  (if (config-unchanged-p request manifest)
      (list :unchanged (config-identity manifest))
      (list :manifest manifest)))

(defun config-delta-valid-p (delta manifest)
  (and (eql (getf delta :base-revision) (manifest-revision manifest))
       (string= (getf delta :base-content-hash) (manifest-content-hash manifest))))

(defun apply-config-delta (delta manifest)
  "A bounded delta against the exact named base. An invalid base leaves the old
config untouched (SPEC-WORK.md:3411-3413)."
  (if (config-delta-valid-p delta manifest)
      (let ((routes (getf delta :routes)))
        (make-config-manifest
         :schema (manifest-schema manifest)
         :friend (manifest-friend manifest)
         :revision (1+ (manifest-revision manifest))
         :routes routes
         :content-hash (sha256-hex (canonical-string routes))))
      manifest))

(defun manifest-partial-p (manifest)
  "A manifest whose completeness hash no longer covers its routes is partial and
is never admitted as a complete replacement (SPEC-WORK.md:3415)."
  (let ((hash (getf manifest :completeness-hash)))
    (and hash (not (string= hash (config-content-hash manifest))))))

(defun replace-config (current manifest)
  "Admit MANIFEST only as a complete replacement; a partial one returns CURRENT
unchanged."
  (if (manifest-partial-p manifest) current manifest))
