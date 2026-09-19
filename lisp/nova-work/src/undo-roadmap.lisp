;;;; undo-roadmap.lisp --- `undo` and `redo` over an accepted `roadmap
;;;; configure`, through the one undo surface.
;;;;
;;;; docs/SPEC-WORK.md:2872 is a row of the reversible-verb table: `roadmap
;;;; configure`, `roadmap row`, `roadmap projection` | undo appends "the same
;;;; verb in its preimage form" | refused when "the current view record is not
;;;; this event's postimage". The table is the spec's answer to "Which verbs are
;;;; reversible is named here, verb by verb, and the rest are refused by name"
;;;; (:2847).
;;;;
;;;; The compensation itself was already written and kernel-real:
;;;; src/roadmap.lisp:696 `roadmap-configure-undo` restores the ordered preimage
;;;; of an accepted configure and refuses when the view has moved. What was
;;;; missing is that no caller could reach it through `undo`: `undo-plan`,
;;;; `undo`, `redo-plan` and `redo` all answered a `:roadmap-configure` applied
;;;; entry with `verb roadmap-configure is not reversible here` -- the sentence
;;;; the table reserves for the rows it refuses by name. That is the defect this
;;;; file closes, and it closes it without a forward reference: the four
;;;; functions register themselves in `*undo-handlers*` (src/edit-undo.lisp) at
;;;; load time, so `undo.lisp` and `edit-undo.lisp` keep no knowledge of a file
;;;; loaded after them and the plain `asdf` load stays as quiet as it was.
;;;;
;;;; `roadmap row` and `roadmap projection` share the table row and are not
;;;; here: they are membership and projection writes with their own preimages,
;;;; and neither records an applied entry the undo path can read yet. They are
;;;; named in HANDOFF rather than half-built.

(in-package #:nova-work)

(defparameter *roadmap-config-fields*
  '(:row-kind :aggregation :completion-policy :axes :permitted-roots)
  "The five configurable fields of a roadmap view (src/roadmap.lisp:539
`%roadmap-config-postimage`), in the order a plan prints them.")

(defun %roadmap-entry-view (kernel entry)
  "The live view record of ENTRY's roadmap, or NIL."
  (let* ((node (%node-quiet (kernel-state kernel) (getf entry :roadmap))))
    (and node (wnode-view node))))

(defun %roadmap-moved-fields (view image)
  "The configurable fields where VIEW's current value differs from IMAGE."
  (loop for field in *roadmap-config-fields*
        unless (equal (getf view field) (getf image field))
          collect field))

(defun %roadmap-rows (kernel entry image)
  "One plan row per field the compensation would move, from the view's current
value to IMAGE's. The effect class is `scope`: a configure writes the roadmap's
scope event (src/roadmap.lisp:753 `roadmap-view-log`), and `scope` is one of the
five the plan row of SPEC-WORK.md:5395 admits."
  (let ((view (%roadmap-entry-view kernel entry))
        (roadmap (getf entry :roadmap)))
    (loop for field in (%roadmap-moved-fields view image)
          collect (list :node roadmap :effect :scope :field field
                        :before (%plan-text (getf view field))
                        :after (%plan-text (getf image field))))))

(defun %roadmap-undo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `undo-plan` over an accepted configure. A
plan whose view has moved past this event's postimage refuses atomically,
naming what changed, and is never half-applied (SPEC-WORK.md:2831-2833)."
  (let ((view (%roadmap-entry-view kernel entry)))
    (unless view
      (return-from %roadmap-undo-plan (values nil "no roadmap view")))
    (let ((moved (%roadmap-moved-fields view (getf entry :after))))
      (when moved
        (return-from %roadmap-undo-plan
          (values nil (format nil "conflict; the view moved ~{~A~^,~}"
                              (mapcar (lambda (f) (string-downcase (symbol-name f))) moved)))))
      (values (%roadmap-rows kernel entry (getf entry :before)) nil))))

(defun %roadmap-undo (kernel entry rid request)
  "Apply the undo: the compensation is src/roadmap.lisp:696
`roadmap-configure-undo`, which restores the ordered preimage only while the
current postimage is still this event's `:after`."
  (multiple-value-bind (okp line code)
      (roadmap-configure-undo kernel :roadmap (getf entry :roadmap)
                                     :of (getf request :of)
                                     :request rid)
    (values okp line code nil)))

(defun %roadmap-redo-plan (kernel entry)
  "Answer (values ROWS REFUSAL) for `redo-plan`: the view moved back to the
postimage the original intent wrote."
  (let ((view (%roadmap-entry-view kernel entry)))
    (unless view
      (return-from %roadmap-redo-plan (values nil "no roadmap view")))
    (values (%roadmap-rows kernel entry (getf entry :after)) nil)))

(defun %roadmap-redo-changed (state entry)
  "What moved since the undo: a redo reapplies the intent against current
preconditions (SPEC-WORK.md:2829-2830), so the view must still hold the
preimage the undo restored. Answers the names that moved, empty when it does."
  (let* ((node (%node-quiet state (getf entry :roadmap)))
         (view (and node (wnode-view node))))
    (if (null view)
        (list "roadmap view")
        (mapcar (lambda (f) (string-downcase (symbol-name f)))
                (%roadmap-moved-fields view (getf entry :before))))))

(defun %roadmap-redo (kernel entry rid request)
  "Reapply the original intent: the recorded patches are re-issued through the
ordinary `roadmap-configure`, under the redo's own request id, so validation and
the view's own log are the real ones and nothing is special-cased."
  (let ((payload (getf entry :payload)))
    (unless payload
      (return-from %roadmap-redo
        (values nil (format nil "REDO FAIL request-of=~A: no recorded intent to reapply"
                            (getf request :of))
                1 nil)))
    (multiple-value-bind (okp line code)
        (roadmap-configure kernel
                           :roadmap (getf entry :roadmap)
                           :row-kind-patch (getf payload :row-kind-patch)
                           :aggregation-patch (getf payload :aggregation-patch)
                           :completion-policy-patch (getf payload :completion-policy-patch)
                           :axes-patch (getf payload :axes-patch)
                           :permitted-roots-patch (getf payload :permitted-roots-patch)
                           :reason (getf payload :reason)
                           :request rid)
      (if okp
          (values t (format nil "REDO OK request=~A request-of=~A roadmap=~A change=configure"
                            rid (getf request :of) (getf entry :roadmap))
                  0 nil)
          (values nil line code nil)))))

(setf (gethash :roadmap-configure *undo-handlers*)
      (list :plan #'%roadmap-undo-plan
            :undo #'%roadmap-undo
            :redo-plan #'%roadmap-redo-plan
            :redo-changed #'%roadmap-redo-changed
            :redo #'%roadmap-redo))
