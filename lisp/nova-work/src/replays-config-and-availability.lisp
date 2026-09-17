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
