;;;; event.lisp --- typed events, their ordered field lists, and the digest.
;;;;
;;;; docs/SPEC-WORK.md:330 fixes the written form: "Each event is written as one
;;;; parenthesized list: `:kind` and its value, then `:node` and its value, then
;;;; `:by` and its value, then the kind's own fields in the order the `:event`
;;;; kinds below list them for that kind — every field written whether or not
;;;; the caller gave one, a field the caller did not give written as
;;;; `(:absent)`".
;;;;
;;;; SPEC-WORK.md:339 fixes what the digest covers: ":kind, :node, :by and the
;;;; kind's own fields are in it; :stamp, :clock, :request and
;;;; :generation-owner are not". SPEC-WORK.md:894 puts the session's own
;;;; :settle and :revive outside it too (replay settle-outside-the-digest).

(in-package #:nova-work)

(defstruct (work-event (:constructor make-work-event
                           (&key kind node by fields stamp clock request
                                 generation-owner rev session-written-p)))
  kind node by fields stamp clock request generation-owner rev session-written-p)

(defparameter *kind-field-order*
  '((:transition :to :reason :blocked-by :evidence)   ; SPEC-WORK.md:823
    (:reopen     :reason)                             ; SPEC-WORK.md:887
    (:settle     :disposition :reason :already-closed) ; SPEC-WORK.md:889-892
    (:revive     :reason)                             ; SPEC-WORK.md:892
    (:edit       :title-patch :category-patch :links-patch :private-patch
                 :version-patch :reason)              ; SPEC-WORK.md:5798
    (:external   :effect :handle)                     ; SPEC-WORK.md:5652
    (:terminal   :disposition :reason)                ; cancel/remove, SPEC-WORK.md:5783
    (:goal       :change :scope :goal :reason)        ; SPEC-WORK.md:1060
    (:evidence   :pointer :criterion :against :generation :attempt) ; :996
    ;; The six new verbs draft 26 added (SPEC-WORK.md:1014-1043, replay
    ;; new-verbs-have-a-kind-and-a-field-order): each writes an event of its own
    ;; kind, with its ordered field list, and addresses no containment node.
    (:undo       :request-of :reason)
    (:redo       :request-of :reason)
    (:friend     :change :friend :role :scope :participation :capability
                 :group :limit :reason)
    (:model      :change :model :provider :route :billing :pricing :effective
                 :source :task-class :result :samples :reason)
    (:observe    :change :friend :state :source :attempt :observed-model
                 :bench :usage :reason)
    (:config     :friend :base :revision :hash :parts :reason))
  "The ordered field list per kind. Slice 1 supports the four transition kinds;
the goal and evidence rows are the goal verb's two event kinds, added by
nova-tools #362 so a `goal update` writes a kind of its own field list.")
    (:terminal   :disposition :reason))               ; cancel/remove, SPEC-WORK.md:5783
  "The ordered field list per kind, for the four kinds slice 1 supports. Any
other kind is refused rather than serialized on a guessed order.")
    (:goal       :change :scope :goal :reason)        ; SPEC-WORK.md:1060
    (:evidence   :pointer :criterion :against :generation :attempt)) ; :996
  "The ordered field list per kind. Slice 1 supports the four transition kinds;
the goal and evidence rows are the goal verb's two event kinds, added by
nova-tools #362 so a `goal update` writes a kind of its own field list.")

(defun kind-fields (kind)
  (let ((row (assoc kind *kind-field-order*)))
    (unless row
      (error 'unsupported-input :what (format nil "event kind ~A outside slice 1"
                                              (string-downcase (symbol-name kind)))))
    (rest row)))

(defun %ordered-fields (event)
  (loop for field in (kind-fields (work-event-kind event))
        append (list field (getf (work-event-fields event) field +absent+))))

(defun event-digest-form (event)
  "The serialization the payload digest is taken over."
  (append (list :kind (work-event-kind event)
                :node (work-event-node event)
                :by (work-event-by event))
          (%ordered-fields event)))

(defun event-record-form (event)
  "The durable form: every field SPEC-WORK.md:802 says an event carries, plus
:rev, which is this event's identity and must survive a replay."
  (append (list :kind (work-event-kind event)
                :node (work-event-node event)
                :by (work-event-by event)
                :stamp (work-event-stamp event)
                :clock (work-event-clock event)
                :request (work-event-request event)
                :generation-owner (work-event-generation-owner event)
                :rev (work-event-rev event))
          (%ordered-fields event)))

(defun record-form->event (form &key session-written-p)
  (let ((kind (getf form :kind)))
    (make-work-event
     :kind kind
     :node (getf form :node)
     :by (getf form :by)
     :fields (loop for field in (kind-fields kind)
                   append (list field (getf form field +absent+)))
     :stamp (getf form :stamp)
     :clock (getf form :clock)
     :request (getf form :request)
     :generation-owner (getf form :generation-owner)
     :rev (getf form :rev)
     :session-written-p session-written-p)))

(defun event-id (event)
  "The event's identity: its own revision, which no two events share."
  (format nil "~D" (work-event-rev event)))

(defun closed-row-key (event)
  "SPEC-WORK.md:1218 — a closed-index row is keyed <event-rev>:<id>."
  (format nil "~D:~A" (work-event-rev event) (work-event-node event)))

(defun payload-digest (events)
  "SPEC-WORK.md:343 — a request whose envelope holds two events digests both,
one space between the two lists, as one serialization. Session-written events
are not the requester's and are never in it."
  (sha256-hex
   (with-output-to-string (s)
     (loop for tail on events
           do (canonical-print (event-digest-form (car tail)) s)
              (when (cdr tail) (write-char #\Space s))))))
