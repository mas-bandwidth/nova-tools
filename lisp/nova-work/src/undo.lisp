;;;; undo.lisp --- the revision-bound dry run of an accepted request's reversal.
;;;;
;;;; SPEC-WORK.md:2277 names the read verb `nova-work undo-plan --session <path>
;;;; --request <id> [--max <n>]`. SPEC-WORK.md:2827-2845 says a plan "is
;;;; revision-bound and shows the nodes, dependencies, counters, verification
;;;; and assignment effects it would move" and that "a stale or conflicting plan
;;;; refuses atomically, naming what changed, and is never half-applied".
;;;; SPEC-WORK.md:2911-2912 says "a dry run produces a revision-bound plan and
;;;; accepts no mutation". SPEC-WORK.md:5395 fixes the row line a plan prints:
;;;; `UNDO ROW node=<id> effect=<state|scope|assignment|counter|verification>
;;;; before=<text> after=<text>`.
;;;;
;;;; This is a read: it never journals, never applies and never advances the
;;;; revision. It is the same reversible set the `undo` verb applies, read
;;;; through the applied-request index, so a caller can see what the
;;;; compensating envelope would move before accepting it.

(in-package #:nova-work)

(defun %plan-text (value)
  "One deterministic field rendering for a plan row: absent is `-`, a keyword is
its downcased name, a string is itself, anything else is its printed form."
  (cond ((absentp value) "-")
        ((stringp value) value)
        ((keywordp value) (string-downcase (symbol-name value)))
        (t (format nil "~S" value))))

(defun %plan-effect (verb)
  "The effect class a reversible verb's compensation moves."
  (case verb
    (:node-move :scope)
    (t :state)))

(defun %plan-edit-rows (state node before)
  "One row per metadata field the compensating edit would move, from the current
value to the preimage the undo restores."
  (loop for field in *metadata-fields*
        for current = (wnode-field (%node-quiet state node) field)
        for preimage = (cdr (assoc field before))
        unless (equal current preimage)
          collect (list :node node :effect :state :field field
                        :before (%plan-text current)
                        :after (%plan-text preimage))))

(defun %plan-transition-rows (state node entry)
  "The state row and, where the reversal settles or revives, the counter row the
compensating envelope would move."
  (let* ((preimage (getf entry :before-state))
         (current (wnode-state (%node-quiet state node))))
    (append
     (list (list :node node :effect :state :field :state
                 :before (%plan-text current) :after (%plan-text preimage)))
     (cond
       ((and (eq (getf entry :verb) :state-to-done) (eq preimage :done))
        (list (list :node node :effect :counter :field :closed
                    :before (%plan-text (state-closed-count state))
                    :after (%plan-text (1- (state-closed-count state))))))
       ((and (eq (getf entry :verb) :event-reopen) (eq current :done))
        (list (list :node node :effect :counter :field :open
                    :before (%plan-text (state-open-count state))
                    :after (%plan-text (1- (state-open-count state))))))
       (t nil)))))

(defun %plan-rows (kernel entry)
  "Answer (values ROWS REFUSAL): the effects the compensating envelope for ENTRY
would move, or a refusal naming why the request is not reversible here."
  (let ((state (kernel-state kernel))
        (verb (getf entry :verb))
        (node (getf entry :node)))
    (case verb
      (:node-edit
       (values (%plan-edit-rows state node (getf entry :before)) nil))
      ((:state-to-doing :state-to-done :event-reopen)
       (values (%plan-transition-rows state node entry) nil))
      (:node-move
       (values (list (list :node node :effect :scope :field :parent
                           :before (%plan-text (getf entry :under))
                           :after (%plan-text (getf entry :from))))
               nil))
      (t
       (values nil
               (format nil "verb ~A is not reversible here"
                       (string-downcase (symbol-name verb))))))))

(defun undo-plan (kernel of &key (request "undo-plan-1") (by "rowan")
                                  (stamp "2026-09-14T12:00:00Z") (clock :tool)
                                  (generation-owner "gen-4") (max 20))
  "The read verb of SPEC-WORK.md:2277. Answer (values OK-P LINE EXIT-CODE PLAN),
where PLAN is `(:request :of :at-rev :rows ...)` with one row per effect the
compensating envelope would move. Bound to the revision it read, and accepting
no mutation."
  (declare (ignore by stamp clock generation-owner max))
  (let ((entry (gethash of (kernel-applied kernel))))
    (unless entry
      (return-from undo-plan
        (values nil (format nil "UNDO PLAN FAIL request-of=~A: no such request" of)
                1 nil)))
    (when (eq (getf entry :verb) :external-effect)
      (return-from undo-plan
        (values nil (format nil "UNDO PLAN FAIL request-of=~A effect=external kind=~A handle=~A: not reversible here"
                            of (string-downcase (symbol-name (getf entry :effect)))
                            (getf entry :handle))
                1 nil)))
    (when (member (getf entry :verb) '(:node-remove :event-cancel))
      (return-from undo-plan
        (values nil (format nil "UNDO PLAN FAIL request-of=~A: not reversible here (~A is terminal)"
                            of (string-downcase (symbol-name (getf entry :verb))))
                1 nil)))
    (multiple-value-bind (rows refusal) (%plan-rows kernel entry)
      (when refusal
        (return-from undo-plan
          (values nil (format nil "UNDO PLAN FAIL request-of=~A: ~A" of refusal)
                  1 nil)))
      (let ((at-rev (state-revision (kernel-state kernel))))
        (values t
                (format nil "UNDO PLAN request=~A request-of=~A at-rev=~D rows=~D reversible=yes"
                        request of at-rev (length rows))
                0
                (list :request request :of of :at-rev at-rev
                      :effect (%plan-effect (getf entry :verb))
                      :rows rows))))))

(defun %submit-undo-plan (kernel request)
  "Dispatch for the `:undo-plan` verb. The read body is `undo-plan`; this only
checks the request's keys and answers in `submit`'s shape."
  (%check-request-keys :undo-plan request)
  (undo-plan kernel (%eu-required request :of)
             :request (or (getf request :request) "undo-plan-1")
             :by (or (getf request :by) "rowan")
             :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
             :clock (or (getf request :clock) :tool)
             :generation-owner (or (getf request :generation-owner) "gen-4")))
