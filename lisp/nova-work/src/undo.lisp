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
        (values nil (format nil "UNDO PLAN FAIL request-of=~A effect=external kind=~A owner=~A state=~A handle=~A: not reversible here"
                            of (string-downcase (symbol-name (getf entry :effect)))
                            (string-downcase (symbol-name (or (getf entry :owner) :operation)))
                            (string-downcase (symbol-name (or (getf entry :state) :known)))
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


;;; ------------------------------------------------------------------
;;; redo-plan and redo: the revision-bound reapplication of an undone intent.
;;;
;;; SPEC-WORK.md:2279-2280 names the verbs `redo-plan` and `redo`; :2827-2845
;;; says "redo reapplies the intent against current preconditions rather than
;;; deleting the undo", and that a stale or conflicting plan "refuses
;;; atomically, naming what changed, and is never half-applied". Both name the
;;; accepted request id of the *undo* they redo. The intent reapplied is the
;;; original accepted request, re-issued under the redo's own request id through
;;; the ordinary `%submit` path, so validation, journaling, dedup and cascade are
;;; the real ones and nothing is special-cased.
;;; ------------------------------------------------------------------

(defun %undo-entry (kernel of)
  "The applied undo recorded under request id OF, or NIL when OF is not an
accepted undo."
  (let ((entry (gethash of (kernel-applied kernel))))
    (and entry (eq (getf entry :verb) :undo) entry)))

(defun %redo-post-state (entry)
  "The state the original verb ENTRY moved its node to, for the three supported
transition verbs."
  (case (getf entry :verb)
    (:state-to-doing :doing)
    (:state-to-done :done)
    (:event-reopen :done)
    (t nil)))

(defun %redo-changed (state entry)
  "The fields or state that moved since ENTRY's undo: what a redo must find
unchanged for its intent to be reapplied against current preconditions.
Answers a list of names, empty when the preconditions still hold."
  (let ((node (getf entry :node)))
    (case (getf entry :verb)
      (:node-edit
       (loop for field in *metadata-fields*
             for current = (wnode-field (%node-quiet state node) field)
             for preimage = (cdr (assoc field (getf entry :before)))
             unless (equal current preimage)
               collect (string-downcase (symbol-name field))))
      ((:state-to-doing :state-to-done :event-reopen)
       (let ((current (wnode-state (%node-quiet state node)))
             (preimage (getf entry :before-state)))
         (unless (eq current preimage)
           (list (format nil "state=~A (expected ~A)"
                         (string-downcase (symbol-name current))
                         (string-downcase (symbol-name preimage)))))))
      (:node-move
       (let ((f (%node-quiet state (getf entry :from)))
             (u (%node-quiet state (getf entry :under)))
             (before (getf entry :before)))
         (unless (and f u
                      (equal (wnode-children f) (getf before :from-children))
                      (equal (wnode-children u) (getf before :under-children)))
           (list "parent children"))))
      (t (list "unreversible verb")))))

(defun %redo-edit-rows (state node after)
  "One row per metadata field the redo would move, from the current value to the
postimage the original intent restores."
  (loop for field in *metadata-fields*
        for current = (wnode-field (%node-quiet state node) field)
        for target = (cdr (assoc field after))
        unless (equal current target)
          collect (list :node node :effect :state :field field
                        :before (%plan-text current) :after (%plan-text target))))

(defun %redo-transition-rows (state node entry)
  "The state row and, where the reapplication settles or revives, the counter row
the intent would move."
  (let* ((current (wnode-state (%node-quiet state node)))
         (target (%redo-post-state entry)))
    (append
     (list (list :node node :effect :state :field :state
                 :before (%plan-text current) :after (%plan-text target)))
     (cond
       ((and (eq (getf entry :verb) :state-to-done) (eq target :done))
        (list (list :node node :effect :counter :field :closed
                    :before (%plan-text (state-closed-count state))
                    :after (%plan-text (1+ (state-closed-count state))))))
       ((and (eq (getf entry :verb) :event-reopen) (eq target :done))
        (list (list :node node :effect :counter :field :open
                    :before (%plan-text (state-open-count state))
                    :after (%plan-text (1+ (state-open-count state))))))
       (t nil)))))

(defun %redo-plan-rows (kernel entry)
  "Answer (values ROWS REFUSAL): the effects the redo of ENTRY would move."
  (let ((state (kernel-state kernel))
        (verb (getf entry :verb))
        (node (getf entry :node)))
    (case verb
      (:node-edit
       (values (%redo-edit-rows state node (getf entry :after)) nil))
      ((:state-to-doing :state-to-done :event-reopen)
       (values (%redo-transition-rows state node entry) nil))
      (:node-move
       (values (list (list :node node :effect :scope :field :parent
                           :before (%plan-text (getf entry :from))
                           :after (%plan-text (getf entry :under))))
               nil))
      (t
       (values nil
               (format nil "verb ~A is not reversible here"
                       (string-downcase (symbol-name verb))))))))

(defun redo-plan (kernel of &key (request "redo-plan-1") (by "rowan")
                                (stamp "2026-09-14T12:00:00Z") (clock :tool)
                                (generation-owner "gen-4") (max 20))
  "The read verb of SPEC-WORK.md:2279. Answer (values OK-P LINE EXIT-CODE PLAN),
where PLAN is `(:request :of :at-rev :rows ...)`, revision-bound and accepting no
mutation. A conflicting plan refuses atomically, naming what changed."
  (declare (ignore by stamp clock generation-owner max))
  (let ((uentry (%undo-entry kernel of)))
    (unless uentry
      (return-from redo-plan
        (values nil (format nil "REDO PLAN FAIL request-of=~A: no such undo" of) 1 nil)))
    (let ((oentry (getf uentry :original)))
      (let ((changed (%redo-changed (kernel-state kernel) oentry)))
        (when changed
          (return-from redo-plan
            (values nil (format nil "REDO PLAN FAIL request-of=~A: stale plan changed ~{~A~^,~}"
                                of changed)
                    1 nil)))
        (multiple-value-bind (rows refusal) (%redo-plan-rows kernel oentry)
          (when refusal
            (return-from redo-plan
              (values nil (format nil "REDO PLAN FAIL request-of=~A: ~A" of refusal) 1 nil)))
          (let ((at-rev (state-revision (kernel-state kernel))))
            (values t
                    (format nil "REDO PLAN request=~A request-of=~A at-rev=~D rows=~D reversible=yes"
                            request of at-rev (length rows))
                    0
                    (list :request request :of of :at-rev at-rev
                          :effect (%plan-effect (getf oentry :verb))
                          :rows rows))))))))

(defun %redo-target-request (oentry redo-request)
  "The original accepted request, re-issued under the redo's own id and write
flags. NIL when ENTRY carries no recorded request to reapply."
  (let ((original (getf oentry :request)))
    (unless (and (consp original) (getf original :verb))
      (return-from %redo-target-request nil))
    (let ((copy (copy-list original)))
      (setf (getf copy :request) (getf redo-request :request))
      (setf (getf copy :stamp) (getf redo-request :stamp))
      (setf (getf copy :clock) (getf redo-request :clock))
      (setf (getf copy :generation-owner) (getf redo-request :generation-owner))
      (setf (getf copy :by) (getf redo-request :by))
      copy)))

(defun %redo-line (rid of envelope)
  "The `REDO OK` line of SPEC-WORK.md:5394, from the reapplied envelope's own
last event."
  (let ((events (getf envelope :events)))
    (if events
        (let ((last (car (last events))))
          (format nil "REDO OK id=~A request=~A request-of=~A node=~A rev=~D pushed=-"
                  (event-id last) rid of (work-event-node last) (work-event-rev last)))
        (format nil "REDO OK request=~A request-of=~A" rid of))))

(defun %redo-move (kernel entry rid of request)
  "A `node move` redo: the node moved back to ENTRY's `:under`. Answers in
`%submit`'s shape."
  (multiple-value-bind (okp line code)
      (node-move kernel :id (getf entry :node) :from (getf entry :from)
                 :under (getf entry :under)
                 :reason (or (getf request :reason) "redo")
                 :request rid
                 :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
                 :by (or (getf request :by) "rowan")
                 :clock (or (getf request :clock) :tool)
                 :generation-owner (or (getf request :generation-owner) "gen-4"))
    (if okp
        (progn
          (setf (gethash rid (kernel-applied kernel))
                (list :verb :redo :of of :original entry :request request))
          (values t (format nil "REDO OK request=~A request-of=~A node=~A change=move"
                            rid of (getf entry :node))
                  0 nil))
        (values nil line code nil))))

(defun %submit-redo (kernel request)
  "%submit's dispatch for the `:redo` verb."
  (%check-request-keys :redo request)
  (let* ((of (%eu-required request :of))
         (rid (%eu-required request :request))
         (uentry (%undo-entry kernel of)))
    (unless uentry
      (return-from %submit-redo
        (values nil (format nil "REDO FAIL request-of=~A: no such undo" of) 1 nil)))
    ;; A revision-bound plan the caller applied must revalidate, exactly as an
    ;; applied undo plan does (SPEC-WORK.md:2831-2833).
    (when (getf request :at-rev)
      (let ((at (getf request :at-rev))
            (rev (state-revision (kernel-state kernel))))
        (unless (eql at rev)
          (return-from %submit-redo
            (values nil (format nil "REDO FAIL request-of=~A: stale plan at-rev=~A rev=~D: not applied"
                                of at rev)
                    1 nil)))))
    (let ((oentry (getf uentry :original)))
      (let ((changed (%redo-changed (kernel-state kernel) oentry)))
        (when changed
          (return-from %submit-redo
            (values nil (format nil "REDO FAIL request-of=~A: stale plan changed ~{~A~^,~}: not applied"
                                of changed)
                    1 nil))))
      (if (eq (getf oentry :verb) :node-move)
          (%redo-move kernel oentry rid of request)
          (let ((target (%redo-target-request oentry request)))
            (unless target
              (return-from %submit-redo
                (values nil (format nil "REDO FAIL request-of=~A: no recorded intent to reapply" of)
                        1 nil)))
            (multiple-value-bind (okp line code envelope) (%submit kernel target)
              (unless okp
                (return-from %submit-redo (values nil line code nil)))
              (let ((redo-line (%redo-line rid of envelope)))
                (setf (gethash rid (kernel-applied kernel))
                      (list :verb :redo :of of :original oentry :request target))
                (values t redo-line 0 envelope))))))))

(defun %submit-redo-plan (kernel request)
  "Dispatch for the `:redo-plan` verb. The read body is `redo-plan`."
  (%check-request-keys :redo-plan request)
  (redo-plan kernel (%eu-required request :of)
             :request (or (getf request :request) "redo-plan-1")
             :by (or (getf request :by) "rowan")
             :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
             :clock (or (getf request :clock) :tool)
             :generation-owner (or (getf request :generation-owner) "gen-4")))
