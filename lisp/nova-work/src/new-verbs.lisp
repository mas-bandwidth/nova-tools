;;;; new-verbs.lisp --- the six new verbs draft 26 added, their own event kinds
;;;; and ordered field lists, and their one-event retry (SPEC-WORK.md:1014-1058,
;;;; replays new-verbs-have-a-kind-and-a-field-order and
;;;; new-verbs-retry-to-one-event).
;;;;
;;;; `undo`, `redo`, `friend`, `model`, `observe` and `config --intake` are
;;;; mutations by *Output grammar* below, so each has a kind, an ordered field
;;;; list and a named subject. None addresses a node of the containment forest:
;;;; `:node` is written `(:absent)` on all six, and each OK line carries the
;;;; subject its kind names -- `nodes=`, `friend=` or `model=` -- never an empty
;;;; `node=`. Their events write CONFIG indexes, not work, so no count, roadmap
;;;; or required set moves when one is written.
;;;;
;;;; This slice carries the event shape and the durable-request-id path that
;;;; makes the retry answer with the original OK line. The resident `friends`,
;;;; `models` and `undo` indexes the events write are configuration, not work,
;;;; and the CLI, session and socket the verbs arrive over are out of slice 1;
;;;; the pure shape is proven here (see README.md).

(in-package #:nova-work)

(defparameter *new-verb-grammar*
  '((:undo    :kind :undo    :subject :nodes
     :fields (:request-of :reason))
    (:redo    :kind :redo    :subject :nodes
     :fields (:request-of :reason))
    (:friend  :kind :friend  :subject :friend
     :fields (:change :friend :role :scope :participation :capability
              :group :limit :reason))
    (:model   :kind :model   :subject :model
     :fields (:change :model :provider :route :billing :pricing :effective
              :source :task-class :result :samples :reason))
    (:observe :kind :observe :subject :friend
     :fields (:change :friend :state :source :attempt :observed-model
              :bench :usage :reason))
    (:config  :kind :config  :subject :friend
     :fields (:friend :base :revision :hash :parts :reason)))
  "Each new verb, its event kind, its ordered field list and its subject
(SPEC-WORK.md:1014-1043). Order is the order of the sentence that lists them.")

(defparameter *new-verb-words*
  '((:undo "UNDO") (:redo "REDO") (:friend "FRIEND") (:model "MODEL")
    (:observe "OBSERVE") (:config "CONFIG")))

(defun new-verb-p (verb)
  (and (assoc verb *new-verb-grammar*) t))

(defun new-verb-kind (verb)
  (getf (cdr (assoc verb *new-verb-grammar*)) :kind))

(defun new-verb-fields (verb)
  (getf (cdr (assoc verb *new-verb-grammar*)) :fields))

(defun new-verb-subject (verb)
  (getf (cdr (assoc verb *new-verb-grammar*)) :subject))

(defun new-verb-word (verb)
  (second (assoc verb *new-verb-words*)))

(defun %new-verb-event (kernel request)
  "One event of its own kind, every field of its list written in order, an absent
field written (:absent), and :node written (:absent) (SPEC-WORK.md:1014-1058)."
  (let* ((verb (getf request :verb))
         (fields (new-verb-fields verb)))
    (unless (new-verb-p verb)
      (error 'unsupported-input
             :what (format nil "unsupported: verb ~A is not one of the six new verbs"
+                           (if verb (string-downcase (princ-to-string verb)) "-"))))
    (make-work-event
     :kind (new-verb-kind verb)
     :node +absent+
     :by (or (getf request :by) "rowan")
     :fields (loop for f in fields append (list f (getf request f +absent+)))
     :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
     :clock (or (getf request :clock) :tool)
     :request (getf request :request)
     :generation-owner (or (getf request :generation-owner) "gen-1")
     :rev (kernel-next-rev kernel))))

(defun %new-verb-subject-token (verb)
  (ecase (new-verb-subject verb)
    (:nodes "nodes")
    (:friend "friend")
    (:model "model")))

(defun %new-verb-subject-value (request verb)
  (ecase (new-verb-subject verb)
    (:nodes (or (getf request :request-of) "1"))
    (:friend (getf request :friend))
    (:model (getf request :model))))

(defun %new-verb-ok-line (request verb event)
  (format nil "~A OK id=~A request=~A ~A=~A rev=~D pushed=-"
          (new-verb-word verb) (event-id event) (work-event-request event)
          (%new-verb-subject-token verb) (%new-verb-subject-value request verb)
          (work-event-rev event)))

(defun submit-new-verb (kernel request)
  "Answer (values OK-P LINE EXIT-CODE ENVELOPE) for one of the six new verbs.
The journal is asked first, so a retry of one request id is answered with the
original OK line and applies nothing, while the same id with a changed payload
is refused `reused with a different payload` (SPEC-WORK.md:5319, replay
new-verbs-retry-to-one-event)."
  (handler-case (%submit-new-verb kernel request)
    (unsupported-input (c)
      (values nil (format nil "~A FAIL: ~A"
                          (or (new-verb-word (getf request :verb)) "MUTATION")
                          (unsupported-input-what c))
              2 nil))))

(defun %submit-new-verb (kernel request)
  (let* ((verb (getf request :verb))
         (word (new-verb-word verb))
         (event (%new-verb-event kernel request))
         (rid (work-event-request event))
         (digest (payload-digest (list event))))
    (unless (and (stringp rid) (plusp (length rid)))
      (error 'unsupported-input :what "no request id"))
    ;; The two-part dedup test (SPEC-WORK.md:315), asked of the journal. The
    ;; kernel keeps no resident map of request ids.
    (multiple-value-bind (found recorded-digest recorded-line)
        (journal-lookup (kernel-journal kernel) rid)
      (cond
        ((eq found :unavailable)
         (return-from %submit-new-verb
           (values nil (format nil "~A FAIL request=~A page=~A: dedup unavailable"
                               word rid recorded-digest)
                   1 nil)))
        (found
         (if (string= digest recorded-digest)
             (return-from %submit-new-verb
               (values t recorded-line 0
                       (list :request rid :digest digest :events '() :replayed t)))
             (return-from %submit-new-verb
               (values nil (format nil "~A FAIL request=~A: reused with a different payload"
                                   word rid)
                       1 nil))))))
    (let* ((envelope (list :request rid :digest digest :events (list event)))
           (line (%new-verb-ok-line request verb event)))
      (multiple-value-bind (accepted refusal) (journal-accept (kernel-journal kernel) envelope)
        (unless accepted
          (return-from %submit-new-verb
            (values nil (format nil "~A FAIL request=~A: journal refused acceptance: ~A"
                                word rid refusal)
                    1 nil))))
      (journal-record (kernel-journal kernel) rid digest line (work-event-rev event))
      (let ((candidate (apply-envelope (kernel-state kernel) envelope)))
        (setf (kernel-state kernel) candidate)
        (setf (kernel-next-rev kernel) (1+ (work-event-rev event)))
        (values t line 0 envelope)))))
