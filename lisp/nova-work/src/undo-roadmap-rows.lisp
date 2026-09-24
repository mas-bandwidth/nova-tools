;;;; undo-roadmap-rows.lisp --- `undo` and `redo` over an accepted `roadmap
;;;; row` and `roadmap projection`, through the one undo surface.
;;;;
;;;; docs/SPEC-WORK.md:2873 is one row of the reversible-verb table: `roadmap
;;;; configure`, `roadmap row` and `roadmap projection` | undo appends "the same
;;;; verb in its preimage form" | refused when "the current view record is not
;;;; this event's postimage". `roadmap configure` reached undo in
;;;; src/undo-roadmap.lisp; this file carries the other two verbs of the same
;;;; row, so the table is honoured verb by verb and not half-built.
;;;;
;;;; The two verbs share the view record, the postimage guard and the refusal,
;;;; so they live in one file. Their compensations sit beside
;;;; `roadmap-configure-undo` in src/roadmap.lisp, and both verbs record an
;;;; applied entry there; the five handlers per verb register themselves in
;;;; `*undo-handlers*` (src/edit-undo.lisp) at load time, so no earlier file
;;;; names a function defined here and the plain `asdf` load stays quiet.

(in-package #:nova-work)

(defparameter *roadmap-row-fields* '(:members :retired :revision :revive-events :branch)
  "The view fields a `roadmap row` moves, in the order a plan prints them. Only
the fields the event actually wrote are present in its `:before`/`:after`
(src/roadmap.lisp:831-862): a remove moves the ordered members, the retired
list and the revision; an add also moves the revive events and the branch it
reopened.")

(defparameter *roadmap-projection-fields* '(:projections :revision)
  "The view fields a `roadmap projection` moves, in the order a plan prints
them. A removed projection's own plist survives only in the applied entry
(src/roadmap.lisp:996-1040).")

(defun %roadmap-field-names (fields)
  "The moved fields as the lowercase names a refusal prints."
  (mapcar (lambda (f) (string-downcase (symbol-name f))) fields))

(defun %roadmap-row-live (state roadmap)
  "The live view's row fields as the `:after`/`:before` shapes hold them, or NIL
when the roadmap has no view."
  (let* ((node (%node-quiet state roadmap))
         (view (and node (wnode-view node))))
    (and view
         (list :members (copy-list (getf view :members))
               :retired (copy-list (getf view :retired))
               :revision (getf view :revision)
               :revive-events (copy-list (getf view :revive-events))
               :branch (wnode-branch node)))))

(defun %roadmap-projection-live (state roadmap)
  "The live view's projection fields as the applied entry shapes them, or NIL
when the roadmap has no view."
  (let* ((node (%node-quiet state roadmap))
         (view (and node (wnode-view node))))
    (and view
         (list :projections (copy-tree (getf view :projections))
               :revision (getf view :revision)))))

(defun %roadmap-fields-moved (live image fields)
  "The FIELDS, in FIELDS order, that IMAGE carries a different value for than
LIVE. A field absent from IMAGE did not move and is never named."
  (loop for field in fields
        when (member field image)
          unless (equal (getf live field) (getf image field))
            collect field))

(defun %roadmap-plan-rows (roadmap live image fields)
  "One `scope` plan row per field the compensation would move, from LIVE to
IMAGE. A scope write is what these verbs make (src/roadmap.lisp:826, :942), and
`scope` is one of the five the plan row of SPEC-WORK.md:5395 admits."
  (loop for field in fields
        when (member field image)
          unless (equal (getf live field) (getf image field))
            collect (list :node roadmap :effect :scope :field field
                          :before (%plan-text (getf live field))
                          :after (%plan-text (getf image field)))))

;;; ------------------------------------------------------------------
;;; `roadmap row` (docs/SPEC-WORK.md:2873, :3104-3106, :3130-3132).
;;; ------------------------------------------------------------------

(defun %roadmap-row-undo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `undo-plan` over an accepted row. A plan
whose view has moved past this event's postimage refuses atomically, naming
what changed (SPEC-WORK.md:2831-2833)."
  (let ((live (%roadmap-row-live (kernel-state kernel) (getf entry :roadmap))))
    (unless live
      (return-from %roadmap-row-undo-plan (values nil "no roadmap view")))
    (let ((moved (%roadmap-fields-moved live (getf entry :after) *roadmap-row-fields*)))
      (when moved
        (return-from %roadmap-row-undo-plan
          (values nil (format nil "conflict; the view moved ~{~A~^,~}"
                              (%roadmap-field-names moved)))))
      (values (%roadmap-plan-rows (getf entry :roadmap) live (getf entry :before)
                                  *roadmap-row-fields*)
              nil))))

(defun %roadmap-row-undo (kernel entry rid request)
  "Apply the undo: src/roadmap.lisp `roadmap-row-undo` splices the ordered
preimage back only while the live view is still this event's `:after`."
  (multiple-value-bind (okp line code)
      (roadmap-row-undo kernel :roadmap (getf entry :roadmap)
                               :of (getf request :of)
                               :request rid)
    (values okp line code nil)))

(defun %roadmap-row-redo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `redo-plan`: the view's current preimage to
the postimage the original row wrote."
  (let ((live (%roadmap-row-live (kernel-state kernel) (getf entry :roadmap))))
    (unless live
      (return-from %roadmap-row-redo-plan (values nil "no roadmap view")))
    (values (%roadmap-plan-rows (getf entry :roadmap) live (getf entry :after)
                                *roadmap-row-fields*)
            nil)))

(defun %roadmap-row-redo-changed (state entry)
  "What moved since the undo: a redo reapplies the intent against current
preconditions (SPEC-WORK.md:2829-2830), so the view must still hold the preimage
the undo restored. Answers the names that moved, empty when it does."
  (let ((live (%roadmap-row-live state (getf entry :roadmap))))
    (if (null live)
        (list "roadmap view")
        (%roadmap-field-names
         (%roadmap-fields-moved live (getf entry :before) *roadmap-row-fields*)))))

(defun %roadmap-row-redo (kernel entry rid request)
  "Reapply the original row through the ordinary `roadmap-row`, under the redo's
own request id, so validation and the view's own log are the real ones."
  (let ((op (getf entry :op)))
    (multiple-value-bind (okp line code)
        (roadmap-row kernel :roadmap (getf entry :roadmap)
                            :member (getf entry :member)
                            :op op :reason (getf entry :reason) :request rid)
      (if okp
          (values t
                  (format nil "REDO OK request=~A request-of=~A roadmap=~A change=~A"
                          rid (getf request :of) (getf entry :roadmap)
                          (if (eq op :add) "row-add" "row-remove"))
                  0 nil)
          (values nil line code nil)))))

;;; ------------------------------------------------------------------
;;; `roadmap projection` (docs/SPEC-WORK.md:2873, :3098-3103, :3130-3132).
;;; ------------------------------------------------------------------

(defun %roadmap-projection-undo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `undo-plan` over an accepted projection."
  (let ((live (%roadmap-projection-live (kernel-state kernel) (getf entry :roadmap))))
    (unless live
      (return-from %roadmap-projection-undo-plan (values nil "no roadmap view")))
    (let ((moved (%roadmap-fields-moved live (getf entry :after)
                                        *roadmap-projection-fields*)))
      (when moved
        (return-from %roadmap-projection-undo-plan
          (values nil (format nil "conflict; the view moved ~{~A~^,~}"
                              (%roadmap-field-names moved)))))
      (values (%roadmap-plan-rows (getf entry :roadmap) live (getf entry :before)
                                  *roadmap-projection-fields*)
              nil))))

(defun %roadmap-projection-undo (kernel entry rid request)
  "Apply the undo: src/roadmap.lisp `roadmap-projection-undo` splices the
ordered projections back whole only while the live view is this event's
`:after`."
  (multiple-value-bind (okp line code)
      (roadmap-projection-undo kernel :roadmap (getf entry :roadmap)
                                      :of (getf request :of)
                                      :request rid)
    (values okp line code nil)))

(defun %roadmap-projection-redo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `redo-plan`: the current preimage to the
postimage the original projection change wrote."
  (let ((live (%roadmap-projection-live (kernel-state kernel) (getf entry :roadmap))))
    (unless live
      (return-from %roadmap-projection-redo-plan (values nil "no roadmap view")))
    (values (%roadmap-plan-rows (getf entry :roadmap) live (getf entry :after)
                                *roadmap-projection-fields*)
            nil)))

(defun %roadmap-projection-redo-changed (state entry)
  "What moved since the undo, as `%roadmap-row-redo-changed` reads it."
  (let ((live (%roadmap-projection-live state (getf entry :roadmap))))
    (if (null live)
        (list "roadmap view")
        (%roadmap-field-names
         (%roadmap-fields-moved live (getf entry :before) *roadmap-projection-fields*)))))

(defun %roadmap-projection-redo (kernel entry rid request)
  "Reapply the original projection change through the ordinary
`roadmap-projection`, under the redo's own request id. The stored payload's
`:absent` row and column axes are the public verb's NIL again."
  (let* ((payload (getf (getf entry :before) :projection))
         (op (getf entry :op)))
    (multiple-value-bind (okp line code)
        (roadmap-projection kernel
                            :roadmap (getf entry :roadmap)
                            :op op
                            :id (getf payload :id)
                            :root (getf payload :root)
                            :repo (getf payload :repo)
                            :path (getf payload :path)
                            :start (getf payload :start)
                            :end (getf payload :end)
                            :policy (getf payload :policy)
                            :row-axis (let ((v (getf payload :row-axis)))
                                        (if (absentp v) nil v))
                            :column-axis (let ((v (getf payload :column-axis)))
                                           (if (absentp v) nil v))
                            :fixed (getf payload :fixed)
                            :reason (getf entry :reason)
                            :request rid)
      (if okp
          (values t
                  (format nil "REDO OK request=~A request-of=~A roadmap=~A change=~A"
                          rid (getf request :of) (getf entry :roadmap)
                          (if (eq op :add) "projection-add" "projection-remove"))
                  0 nil)
          (values nil line code nil)))))

(setf (gethash :roadmap-row *undo-handlers*)
      (list :plan #'%roadmap-row-undo-plan
            :undo #'%roadmap-row-undo
            :redo-plan #'%roadmap-row-redo-plan
            :redo-changed #'%roadmap-row-redo-changed
            :redo #'%roadmap-row-redo))

(setf (gethash :roadmap-projection *undo-handlers*)
      (list :plan #'%roadmap-projection-undo-plan
            :undo #'%roadmap-projection-undo
            :redo-plan #'%roadmap-projection-redo-plan
            :redo-changed #'%roadmap-projection-redo-changed
            :redo #'%roadmap-projection-redo))
