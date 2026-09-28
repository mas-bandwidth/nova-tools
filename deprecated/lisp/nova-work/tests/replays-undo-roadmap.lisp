;;;; replays-undo-roadmap.lisp --- `undo`, `undo-plan`, `redo` and `redo-plan`
;;;; over an accepted `roadmap configure` (docs/SPEC-WORK.md:2872, the
;;;; reversible-verb table's roadmap row; :2827-2845 for the plan, the atomic
;;;; refusal and the redo). The subject is src/undo-roadmap.lisp and the
;;;; *undo-handlers* hook in src/edit-undo.lisp.
;;;;
;;;; Before this, all four surfaces answered a `:roadmap-configure` applied
;;;; entry `verb roadmap-configure is not reversible here` -- the sentence the
;;;; table reserves for the rows it refuses by name -- while
;;;; src/roadmap.lisp:696 `roadmap-configure-undo` sat there, kernel-real and
;;;; unreachable.

(in-package #:nova-work/tests)

(defparameter *roadmap-undo-seed*
  '((:id "root" :type :work-set :parent nil :state :unknown)
    (:id "root/f1" :type :feature :parent "root" :state :unknown)
    (:id "root/f2" :type :feature :parent "root" :state :unknown)))

(defun roadmap-undo-kernel ()
  "A kernel holding one configured roadmap and one accepted `roadmap configure`
under the request id `cfg-1`."
  (let ((k (make-kernel :state (make-seed-state *roadmap-undo-seed*))))
    (multiple-value-bind (okp line)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :feature
                          :aggregation :required-members
                          :completion-policy :all-required-features
                          :axes '() :permitted-roots '() :reason "new"
                          :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line))
    (multiple-value-bind (okp line)
        (roadmap-configure k :roadmap "rm"
                             :row-kind-patch '(:set :epic)
                             :aggregation-patch '(:keep)
                             :completion-policy-patch '(:keep)
                             :axes-patch '(:keep)
                             :permitted-roots-patch '(:set ("root/f1"))
                             :reason "configure" :request "cfg-1")
      (ok okp "the configure was refused: ~A" line))
    k))

(defun rm-view-field (k field)
  (getf (node-view (kernel-state k) "rm") field))

;;; ------------------------------------------------------------------
;;; The table row: a configure is reversible, in its preimage form.
;;; SPEC-WORK.md:2872.
;;; ------------------------------------------------------------------

(deftest "roadmap-configure-is-reversible-through-undo" "docs/SPEC-WORK.md:2872"
    "expected=undo-plan-names-the-scope-rows;undo-restores-the-preimage;the-original-is-not-erased"
  (let ((k (roadmap-undo-kernel)))
    (check-equal :epic (rm-view-field k :row-kind) "the configure did not take")
    (check-equal '("root/f1") (rm-view-field k :permitted-roots) "the configure did not take")
    ;; The dry run names the rows it would move and accepts no mutation.
    (let ((revision (rm-view-field k :revision)))
      (multiple-value-bind (okp line code plan)
          (submit k (list :verb :undo-plan :of "cfg-1" :by "rowan"
                          :request "up-1" :stamp "2026-09-17T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok okp "undo-plan over a configure was refused: ~A" line)
        (check-equal 0 code "undo-plan exit code")
        (ok (search "reversible=yes" line) "the plan does not call it reversible: ~A" line)
        (ok (search "at-rev=" line) "the plan is not revision-bound: ~A" line)
        (check-equal 2 (length (getf plan :rows)) "the plan's rows")
        (let ((row (find :row-kind (getf plan :rows) :key (lambda (r) (getf r :field)))))
          (ok row "the plan does not name the row-kind it would move")
          (check-equal :scope (getf row :effect) "a configure's effect class")
          (check-string= "epic" (getf row :before) "the plan's current value")
          (check-string= "feature" (getf row :after) "the plan's preimage")))
      (check-equal revision (rm-view-field k :revision) "the dry run moved the view"))
    ;; The undo restores the ordered preimage.
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "cfg-1" :by "rowan"
                        :request "undo-1" :stamp "2026-09-17T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of a configure was refused: ~A" line)
      (check-equal 0 code "undo exit code")
      (ok (search "UNDO OK" line) "the undo line: ~A" line))
    (check-equal :feature (rm-view-field k :row-kind) "the undo did not restore the preimage")
    (check-equal '() (rm-view-field k :permitted-roots)
                 "the undo did not restore the preimage roots")
    ;; Mistakes are reversible by appending, never by erasing: the configure's
    ;; own log entry is still there, with the undo appended beside it.
    (let ((log (roadmap-view-log (kernel-state k) "rm")))
      (ok (find "cfg-1" log :key (lambda (e) (getf e :request)) :test #'equal)
          "the undo erased the original configure's record")
      (ok (find :undo log :key (lambda (e) (getf e :op)))
          "the undo appended no record of itself"))))

;;; ------------------------------------------------------------------
;;; A stale plan refuses atomically, naming what changed.
;;; SPEC-WORK.md:2831-2833.
;;; ------------------------------------------------------------------

(deftest "a-stale-roadmap-undo-refuses-atomically" "docs/SPEC-WORK.md:2831"
    "expected=the-view-moved-named;nothing-half-applied"
  (let ((k (roadmap-undo-kernel)))
    ;; A second configure moves the view past the first one's postimage.
    (multiple-value-bind (okp line)
        (roadmap-configure k :roadmap "rm"
                             :row-kind-patch '(:set :work-set)
                             :aggregation-patch '(:keep)
                             :completion-policy-patch '(:keep)
                             :axes-patch '(:keep)
                             :permitted-roots-patch '(:keep)
                             :reason "again" :request "cfg-2")
      (ok okp "the second configure was refused: ~A" line))
    (let ((row-kind (rm-view-field k :row-kind))
          (revision (rm-view-field k :revision)))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo-plan :of "cfg-1" :by "rowan"
                          :request "up-2" :stamp "2026-09-17T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale plan was accepted: ~A" line)
        (check-equal 1 code "stale plan exit code")
        (ok (search "the view moved" line) "the refusal does not name what changed: ~A" line)
        (ok (search "row-kind" line) "the refusal does not name the field: ~A" line))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo :of "cfg-1" :by "rowan"
                          :request "undo-2" :stamp "2026-09-17T01:01:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale undo was applied: ~A" line)
        (check-equal 1 code "stale undo exit code")
        (ok (search "conflict" line) "the refusal does not name the conflict: ~A" line))
      ;; Never half-applied: the view is exactly as it was.
      (check-equal row-kind (rm-view-field k :row-kind) "the refused undo moved the view")
      (check-equal revision (rm-view-field k :revision) "the refused undo moved the revision"))))

;;; ------------------------------------------------------------------
;;; Redo reapplies the intent and never deletes the undo.
;;; SPEC-WORK.md:2829-2830.
;;; ------------------------------------------------------------------

(deftest "redo-reapplies-a-roadmap-configure" "docs/SPEC-WORK.md:2829"
    "expected=redo-plan-names-the-rows;redo-restores-the-postimage;the-undo-is-not-deleted"
  (let ((k (roadmap-undo-kernel)))
    (submit k (list :verb :undo :of "cfg-1" :by "rowan"
                    :request "undo-3" :stamp "2026-09-17T01:01:00Z"
                    :clock :tool :generation-owner "gen-4"))
    (check-equal :feature (rm-view-field k :row-kind) "the undo did not take")
    (multiple-value-bind (okp line code plan)
        (submit k (list :verb :redo-plan :of "undo-3" :by "rowan"
                        :request "rp-1" :stamp "2026-09-17T01:02:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the redo-plan was refused: ~A" line)
      (check-equal 0 code "redo-plan exit code")
      (let ((row (find :row-kind (getf plan :rows) :key (lambda (r) (getf r :field)))))
        (ok row "the redo plan does not name the row-kind")
        (check-string= "feature" (getf row :before) "the redo plan's current value")
        (check-string= "epic" (getf row :after) "the redo plan's intent")))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :redo :of "undo-3" :by "rowan"
                        :request "redo-3" :stamp "2026-09-17T01:03:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the redo was refused: ~A" line)
      (check-equal 0 code "redo exit code")
      (ok (search "REDO OK" line) "the redo line: ~A" line))
    (check-equal :epic (rm-view-field k :row-kind) "the redo did not reapply the intent")
    (check-equal '("root/f1") (rm-view-field k :permitted-roots)
                 "the redo did not reapply the whole intent")
    ;; The undo is reapplied against current preconditions, never deleted.
    (ok (find :undo (roadmap-view-log (kernel-state k) "rm") :key (lambda (e) (getf e :op)))
        "the redo deleted the undo")))

(deftest "redo-refuses-a-stale-roadmap-plan" "docs/SPEC-WORK.md:2832"
    "expected=stale-plan-named;nothing-applied;the-undo-still-there"
  (let ((k (roadmap-undo-kernel)))
    (submit k (list :verb :undo :of "cfg-1" :by "rowan"
                    :request "undo-4" :stamp "2026-09-17T01:01:00Z"
                    :clock :tool :generation-owner "gen-4"))
    ;; The view moves after the undo: the redo's preconditions are gone.
    (roadmap-configure k :roadmap "rm"
                         :row-kind-patch '(:set :work-set)
                         :aggregation-patch '(:keep)
                         :completion-policy-patch '(:keep)
                         :axes-patch '(:keep)
                         :permitted-roots-patch '(:keep)
                         :reason "moved" :request "cfg-3")
    (let ((row-kind (rm-view-field k :row-kind))
          (revision (rm-view-field k :revision)))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo-plan :of "undo-4" :by "rowan"
                          :request "rp-2" :stamp "2026-09-17T01:02:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale redo-plan was accepted: ~A" line)
        (check-equal 1 code "stale redo-plan exit code")
        (ok (search "stale plan changed" line) "the refusal does not name what changed: ~A" line)
        (ok (search "row-kind" line) "the refusal does not name the field: ~A" line))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo :of "undo-4" :by "rowan"
                          :request "redo-4" :stamp "2026-09-17T01:03:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale redo was applied: ~A" line)
        (check-equal 1 code "stale redo exit code")
        (ok (search "not applied" line) "the refusal does not say nothing was applied: ~A" line))
      (check-equal row-kind (rm-view-field k :row-kind) "the refused redo moved the view")
      (check-equal revision (rm-view-field k :revision)
                   "the refused redo moved the revision"))))
