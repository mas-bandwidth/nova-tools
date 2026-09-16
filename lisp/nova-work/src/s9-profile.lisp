;;;; s9-profile.lisp --- the prompt profile: a manager's prompt as CONFIG data,
;;;; never memory.
;;;;
;;;; docs/SPEC-WORK.md (PR687, #500). A prompt profile is a CONFIG member keyed
;;;; by name, carrying its manager model, harness and work type as identity, a
;;;; pointer to the prompt text (never inline) pinned by the SHA-256 digest of
;;;; the content, a policy version, dated evidence, an expiry and an owner. A
;;;; stale, absent or mismatched profile is printed on the status line as such.
;;;; Every edit is a versioned record with :by, never an in-place rewrite.
;;;;
;;;; Pure functions and records, not wired into the command thread.

(in-package #:nova-work)

(defstruct (s9-profile (:conc-name profile-))
  (name nil)
  (model nil)
  (harness nil)
  (work-type nil)
  (pointer nil)       ; a file path in the repository, never inline
  (digest nil)        ; SHA-256 of the prompt content the pointer resolved to
  (policy nil)
  (evidence :unknown) ; a dated measurement, or :unknown where none was taken
  (expiry nil)
  (owner nil)
  (revision 0)
  (revisions '()))    ; versioned edits, each (:by :field :revision), newest first

(defstruct (s9-profiles (:conc-name profiles-) (:constructor %make-s9-profiles))
  (profiles (make-hash-table :test #'equal)) ; name -> s9-profile
  (order '())
  (friends '())
  (revision 0))

(defun make-s9-profiles (&key friends)
  (%make-s9-profiles :friends (or friends '())))

(defun profiles-find (registry name)
  (gethash name (profiles-profiles registry)))

(defun profiles-generate-id (registry)
  (incf (profiles-revision registry)))

(defun profile-pin-digest (profile content)
  (setf (profile-digest profile) (and content (sha256-hex content))))

(defun profile-record-edit (profile field by)
  (let ((rev (incf (profile-revision profile))))
    (push (list :by by :field field :revision rev) (profile-revisions profile))
    rev))

(defun profile-digest-mismatch-p (profile content)
  "True when the prompt content at the pointer hashes to a digest other than the
  pinned one. A mismatch refuses the invocation rather than running on unknown
  bytes or silently re-pinning."
  (and (profile-digest profile)
       content
       (not (string= (profile-digest profile) (sha256-hex content)))))

;;; -------------------------------------------------------------------- write

(defun profile-write (registry request)
  "`nova-work profile --write <name> --model --harness --work-type --pointer
  --policy [--evidence] [--expiry] [--owner] --reason`. No two profiles hold one
  model, harness and work-type triple. Answers (values ok-p line registry
  profile)."
  (let* ((name (getf request :name))
         (owner (getf request :owner))
         (model (getf request :model))
         (harness (getf request :harness))
         (work-type (getf request :work-type)))
    (cond
      ((or (null name) (and (stringp name) (string= name "")))
       (return-from profile-write (values nil "PROFILE FAIL profile=-: no name" registry nil)))
      ((or (null owner) (and (stringp owner) (string= owner "")))
       (return-from profile-write
         (values nil (format nil "PROFILE FAIL profile=~A: no owner" name) registry nil)))
      ((not (member owner (profiles-friends registry) :test #'string=))
       (return-from profile-write
         (values nil (format nil "PROFILE FAIL profile=~A: unknown owner" name) registry nil)))
      ((profiles-find registry name)
       (return-from profile-write
         (values nil (format nil "PROFILE FAIL profile=~A: duplicate name" name) registry nil)))
      ((loop for id in (profiles-order registry)
             for p = (gethash id (profiles-profiles registry))
             thereis (and (equal model (profile-model p))
                          (equal harness (profile-harness p))
                          (equal work-type (profile-work-type p))))
       (return-from profile-write
         (values nil (format nil "PROFILE FAIL profile=~A: no two profiles hold one model, harness and work-type triple" name)
                 registry nil))))
    (let* ((profile (make-s9-profile
                     :name name
                     :model model
                     :harness harness
                     :work-type work-type
                     :pointer (getf request :pointer)
                     :digest nil
                     :policy (getf request :policy)
                     :evidence (getf request :evidence :unknown)
                     :expiry (getf request :expiry)
                     :owner owner
                     :revision 0
                     :revisions '())))
      (profile-pin-digest profile (getf request :prompt-content))
      (profile-record-edit profile "write" (getf request :by))
      (setf (gethash name (profiles-profiles registry)) profile)
      (setf (profiles-order registry) (append (profiles-order registry) (list name)))
      (let ((rev (profiles-generate-id registry)))
        (values t
                (format nil "PROFILE OK id=~A request=~A profile=~A change=write rev=~D"
                        rev (getf request :request) name rev)
                 registry profile)))))

;;; --------------------------------------------------------------------- edit

(defun profile-edit (registry request)
  "`nova-work profile --edit <name> (--pointer <path> | --policy <rev> |
  --evidence <pointer> | --expiry <stamp> | --owner <name>) --reason`. One
  versioned record with :by naming the profile and stating the fields it
  changes; never an in-place rewrite (which would hide who edited what). An edit
  re-pins the digest when it changes --pointer."
  (let* ((name (getf request :name))
         (profile (profiles-find registry name)))
    (unless profile
      (return-from profile-edit
        (values nil (format nil "PROFILE FAIL profile=~A: no such profile" name) registry nil)))
    (let ((pointer (getf request :pointer :keep))
          (policy (getf request :policy :keep))
          (evidence (getf request :evidence :keep))
          (expiry (getf request :expiry :keep))
          (owner (getf request :owner :keep))
          (content (getf request :prompt-content))
          (changed '()))
      (unless (eq pointer :keep)
        (setf (profile-pointer profile) pointer)
        (profile-pin-digest profile content)
        (push "pointer" changed))
      (unless (eq policy :keep) (setf (profile-policy profile) policy) (push "policy" changed))
      (unless (eq evidence :keep) (setf (profile-evidence profile) evidence) (push "evidence" changed))
      (unless (eq expiry :keep) (setf (profile-expiry profile) expiry) (push "expiry" changed))
      (unless (eq owner :keep) (setf (profile-owner profile) owner) (push "owner" changed))
      (let ((rev (profile-record-edit profile (or changed '("no-op")) (getf request :by))))
        (values t
                (format nil "PROFILE OK id=~A request=~A profile=~A change=edit rev=~D by=~A"
                        rev (getf request :request) name rev (getf request :by))
                registry profile)))))

;;; --------------------------------------------------------------------- status

(defun profile-status-line (registry name &key now digest)
  "The profile segment a manager session prints on its status line. A stale,
  absent, mismatched or unknown profile is printed as such, never silently
  re-measured and never swapped for another model's prompt."
  (let ((profile (profiles-find registry name)))
    (unless profile
      (return-from profile-status-line
        (format nil "profile=~A profile-state=absent" name)))
    (cond
      ((and (profile-expiry profile) (s9-stamp< (profile-expiry profile) now))
       (format nil "profile=~A profile-state=stale profile-expired=~A profile-owner=~A"
               name (profile-expiry profile) (profile-owner profile)))
      ((and digest (profile-digest profile)
            (not (string= (profile-digest profile) digest)))
       (format nil "profile=~A profile-state=mismatch" name))
      ((eq :unknown (profile-evidence profile))
       (format nil "profile=~A profile-state=unknown" name))
      (t
       (format nil "profile=~A profile-state=current profile-expires=~A profile-owner=~A"
               name (or (profile-expiry profile) "-") (profile-owner profile))))))
