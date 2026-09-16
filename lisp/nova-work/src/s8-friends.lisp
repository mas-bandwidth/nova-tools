;;;; s8-friends.lisp --- friends, capability groups, and the four facts with
;;;; their four verbs (S8, #500).
;;;;
;;;; docs/SPEC-WORK.md:3280-3435 (*Friends, CONFIG and ACTIVE*) and :3363-3391
;;;; and :3719-3727 (the four facts). This file holds the friend record and its
;;;; six change forms, the four capability groups in three never-collapsing
;;;; fields, and the four facts (dispatch, delivery, accepted ownership,
;;;; refusal) each written by its own verb and by nothing else.
;;;;
;;;; Replays: `four-capability-groups-and-three-fields` (3373),
;;;; `four-facts-four-verbs` (3727).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The friend record and its six change forms
;;; (SPEC-WORK.md:1026-1030, 2272). The subject is a friend identity, never a
;;; node; `:node` is `(:absent)` and FRIEND OK prints `friend=<name>`.
;;; ------------------------------------------------------------------

(defstruct (friend-record (:conc-name friend-))
  change friend role scope participation capability group limit reason)

(defparameter +friend-changes+
  '(:register :retire :role :participation :capability :limit))

(defun parse-friend-args (args)
  "Normalize the `friend` verb's argument plist to exactly one change form.
  Returns a friend-record; a plist with none or more than one change refuses."
  (let ((changes '()))
    (dolist (k +friend-changes+)
      (when (member k args)
        (push k changes)))
    (when (null changes)
      (error 'unsupported-input
             :what "friend needs one of --register|--retire|--role|--participation|--capability|--limit"))
    (when (cdr changes)
      (error 'unsupported-input
             :what (format nil "friend takes exactly one change, got ~{~A~^, ~}" (reverse changes))))
    (make-friend-record
     :change (car changes)
     :friend (getf args :friend)
     :role (getf args :role)
     :scope (getf args :scope)
     :participation (getf args :participation)
     :capability (getf args :capability)
     :group (getf args :group)
     :limit (getf args :limit)
     :reason (getf args :reason))))

;;; ------------------------------------------------------------------
;;; The four capability groups in three fields (SPEC-WORK.md:3363-3376).
;;; CONFIG holds what a friend *may* do; three fields stay three: declared
;;; support is not verified runtime and neither is free capacity.
;;; ------------------------------------------------------------------

(defstruct (capability-entry (:conc-name capability-))
  group id source last-verified availability constraints
  declared-support verified-runtime free-capacity)

(defun make-capability (group id &key source last-verified availability constraints
                                     declared-support verified-runtime free-capacity)
  (unless (member group '(:child :swarm :local :one-shot))
    (error 'unsupported-input
           :what (format nil "capability group ~A is not child|swarm|local|one-shot" group)))
  (make-capability-entry
   :group group :id id :source source :last-verified last-verified
   :availability availability :constraints constraints
   :declared-support declared-support
   :verified-runtime verified-runtime
   :free-capacity free-capacity))

;;; ------------------------------------------------------------------
;;; The four facts, four verbs (SPEC-WORK.md:3719-3727): dispatch, delivery,
;;; accepted ownership, refusal. None is inferred from another, and none
;;; launches anything. `offer` writes dispatch — intent and a reservation from
;;; declared free slots — and nothing else; a received acknowledgement writes
;;; delivery only; an accepted acknowledgement writes accepted ownership.
;;; ------------------------------------------------------------------

(defstruct offer-intent id to profile reserve until requested-model)

(defun admit-offer (intent &key free-slots)
  (if (and free-slots (<= (offer-intent-reserve intent) free-slots))
      (make-offer-result :effect :dispatched
                         :offer-id (offer-intent-id intent)
                         :reserve (offer-intent-reserve intent))
      (make-offer-result :effect :refused
                         :offer-id (offer-intent-id intent)
                         :reserve 0)))

(defstruct offer-result effect offer-id reserve)

(defun acknowledge-received (offer-id reply)
  (make-receipt :offer-id offer-id :reply reply :stage :received))

(defun acknowledge-accepted (offer-id reply)
  (make-receipt :offer-id offer-id :reply reply :stage :accepted))

(defun decline-offer (offer-id reply)
  (make-receipt :offer-id offer-id :reply reply :stage :declined))

(defstruct receipt offer-id reply stage)
