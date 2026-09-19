;;;; replays-undo-roadmap-rows.lisp --- `undo`, `undo-plan`, `redo` and
;;;; `redo-plan` over an accepted `roadmap row` and `roadmap projection`
;;;; (docs/SPEC-WORK.md:2873, the reversible-verb table's shared roadmap row;
;;;; :2827-2845 for the plan, the atomic refusal and the redo). The subject is
;;;; src/undo-roadmap-rows.lisp, the two compensations in src/roadmap.lisp and
;;;; the *undo-handlers* hook in src/edit-undo.lisp.
;;;;
;;;; Before this, both verbs answered `verb roadmap-row is not reversible here`
;;;; and `verb roadmap-projection is not reversible here` even though :2873
;;;; makes all three verbs of the row reversible and both now record the
;;;; preimage in `kernel-applied`.

(in-package #:nova-work/tests)

(defparameter *roadmap-rows-seed*
  '((:id "root" :type :work-set :parent nil :state :unknown)
    (:id "root/f1" :type :feature :parent "root" :state :unknown)
    (:id "root/f1/t" :type :task :parent "root/f1" :state :doing)
    (:id "root/f2" :type :feature :parent "root" :state :unknown)
    (:id "root/f2/t" :type :task :parent "root/f2" :state :doing)
    (:id "root/f3" :type :feature :parent "root" :state :unknown))
  "Three ordered members of the one row-kind, with a task under the first two so
they can settle and revive a roadmap.")

(defun roadmap-rows-kernel ()
  "A kernel holding one axisless roadmap created under the request id `rm-1`."
  (let ((k (make-kernel :state (make-seed-state *roadmap-rows-seed*))))
    (multiple-value-bind (okp line)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :feature
                          :aggregation :required-members
                          :completion-policy :all-required-features
                          :axes '() :permitted-roots '() :reason "new"
                          :request "rm-1" :stamp "2026-09-19T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line))
    k))

(defun rm-members (k) (roadmap-view-members (kernel-state k) "rm"))
(defun rm-retired (k) (roadmap-view-retired (kernel-state k) "rm"))
(defun rm-revive-events (k) (roadmap-view-revive-events (kernel-state k) "rm"))

(defun rm-projection-ids (k)
  (mapcar (lambda (p) (getf p :id)) (roadmap-view-projections (kernel-state k) "rm")))

(defun rr-add-row (k member request)
  (multiple-value-bind (okp line code)
      (roadmap-row k :roadmap "rm" :member member :op :add :reason "row" :request request)
    (ok okp "adding row ~A was refused: ~A" member line)
    (check-equal 0 code "row add exit")))

(defun rr-remove-row (k member request)
  (multiple-value-bind (okp line code)
      (roadmap-row k :roadmap "rm" :member member :op :remove :reason "retire" :request request)
    (ok okp "removing row ~A was refused: ~A" member line)
    (check-equal 0 code "row remove exit")))

(defun rr-settle (k node request)
  (multiple-value-bind (okp line code)
      (submit k (close-request :node node :request request))
    (ok okp "finishing ~A was refused: ~A" node line)
    (check-equal 0 code "finish exit")))

(defun rr-add-projection (k id request)
  (multiple-value-bind (okp line code)
      (roadmap-projection k :roadmap "rm" :op :add :id id :root "root"
                            :repo "acme/work" :path (format nil "docs/~A.md" id)
                            :start "<!-- S -->" :end "<!-- E -->"
                            :policy :markdown-table :reason "proj" :request request)
    (ok okp "adding projection ~A was refused: ~A" id line)
    (check-equal 0 code "projection add exit")))

(defun rr-remove-projection (k id request)
  (multiple-value-bind (okp line code)
      (roadmap-projection k :roadmap "rm" :op :remove :id id :reason "drop" :request request)
    (ok okp "removing projection ~A was refused: ~A" id line)
    (check-equal 0 code "projection remove exit")))

(defun rr-plan-rows (plan) (getf plan :rows))

(defun rr-row-for (plan field)
  (find field (rr-plan-rows plan) :key (lambda (r) (getf r :field))))

;;; ------------------------------------------------------------------
;;; The table row: `roadmap row` is reversible, in its preimage form.
;;; SPEC-WORK.md:2873.
;;; ------------------------------------------------------------------

(deftest "roadmap-row-is-reversible-through-undo" "docs/SPEC-WORK.md:2873"
    "expected=a-removed-row-returns-at-its-original-index;an-add-that-revived-a-settled-roadmap-undone-restores-C-and-pops-the-revive"
  ;; A `--remove` of a member NOT last in `:members` returns at its original
  ;; index, off `:retired`, whole.
  (let ((k (roadmap-rows-kernel)))
    (rr-add-row k "root/f1" "r1")
    (rr-add-row k "root/f2" "r2")
    (check-equal '("root/f1" "root/f2") (rm-members k) "row order was not preserved")
    (rr-remove-row k "root/f1" "r-rm")
    (check-equal '("root/f2") (rm-members k) "the removal did not drop the row")
    (check-equal '("root/f1") (rm-retired k) "the removal did not record the retirement")
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "r-rm" :by "rowan"
                        :request "u-rm" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of a retirement was refused: ~A" line)
      (check-equal 0 code "undo exit code"))
    (check-equal '("root/f1" "root/f2") (rm-members k)
                 "the removed row did not come back at its original index")
    (check-equal '() (rm-retired k) "the undo did not take the row off :retired"))
  ;; An `--add` that revived a settled roadmap: the undo restores the pre-roll
  ;; branch and pops the revive event.
  (let ((k (roadmap-rows-kernel)))
    (rr-add-row k "root/f1" "rr-1")
    (rr-settle k "root/f1/t" "done-1")
    (check-equal t (roadmap-settled-p (kernel-state k) "rm")
                 "the roadmap did not settle with its only row finished")
    (setf (nova-work::wnode-branch (nova-work::%node-quiet (kernel-state k) "rm")) :c)
    (rr-add-row k "root/f3" "rr-revive")
    (check-equal :o (node-branch (kernel-state k) "rm")
                 "adding an outstanding member did not reopen the roadmap")
    (check-equal 1 (length (rm-revive-events k))
                 "the revival was not recorded")
    ;; The dry run names the scope rows and accepts no mutation.
    (let ((members (rm-members k)) (branch (node-branch (kernel-state k) "rm"))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code plan)
          (submit k (list :verb :undo-plan :of "rr-revive" :by "rowan"
                          :request "up-rr" :stamp "2026-09-19T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok okp "undo-plan over a roadmap row was refused: ~A" line)
        (check-equal 0 code "undo-plan exit code")
        (ok (search "reversible=yes" line) "the plan does not call it reversible: ~A" line)
        (ok (plusp (length (rr-plan-rows plan))) "the plan named no rows")
        (dolist (row (rr-plan-rows plan))
          (check-equal :scope (getf row :effect) "a row plan's effect class"))
        (ok (rr-row-for plan :members) "the plan does not name the membership it would move"))
      (check-equal members (rm-members k) "the dry run moved the view")
      (check-equal branch (node-branch (kernel-state k) "rm") "the dry run moved the branch")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the dry run moved the revision"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "rr-revive" :by "rowan"
                        :request "undo-rr" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of a roadmap row was refused: ~A" line)
      (check-equal 0 code "undo exit code")
      (ok (search "UNDO OK" line) "the undo line: ~A" line))
    (check-equal '("root/f1") (rm-members k) "the undo did not restore the preimage")
    (check-equal :c (node-branch (kernel-state k) "rm")
                 "the undo did not put the roadmap back into C")
    (check-equal '() (rm-revive-events k) "the undo did not pop the revive event")))

;;; ------------------------------------------------------------------
;;; A stale row plan refuses atomically, naming what changed.
;;; SPEC-WORK.md:2831-2833.
;;; ------------------------------------------------------------------

(deftest "a-stale-roadmap-row-undo-refuses-atomically" "docs/SPEC-WORK.md:2873"
    "expected=the-view-moved-named;nothing-half-applied"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-row k "root/f1" "r1")
    ;; A second row moves the view past the first one's postimage.
    (rr-add-row k "root/f2" "r2")
    (let ((members (rm-members k))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo-plan :of "r1" :by "rowan"
                          :request "up-stale" :stamp "2026-09-19T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale row plan was accepted: ~A" line)
        (ok (search "the view moved" line) "the refusal does not name what changed: ~A" line)
        (ok (search "members" line) "the refusal does not name the moved field: ~A" line)
        (check-equal 1 code "stale row plan exit code"))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo :of "r1" :by "rowan"
                          :request "u-stale" :stamp "2026-09-19T01:01:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale row undo was applied: ~A" line)
        (ok (search "conflict" line) "the refusal does not name the conflict: ~A" line)
        (check-equal 1 code "stale row undo exit code"))
      (check-equal members (rm-members k) "the refused undo moved the view")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the refused undo moved the revision"))))

;;; ------------------------------------------------------------------
;;; Redo reapplies the row intent and never deletes the undo.
;;; SPEC-WORK.md:2829-2830.
;;; ------------------------------------------------------------------

(deftest "redo-reapplies-a-roadmap-row" "docs/SPEC-WORK.md:2873"
    "expected=redo-plan-names-the-rows;redo-restores-the-postimage;the-undo-is-not-deleted"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-row k "root/f1" "r1")
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "r1" :by "rowan"
                        :request "u1" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of the row was refused: ~A" line)
      (check-equal 0 code "undo exit code"))
    (check-equal '() (rm-members k) "the undo did not take")
    (multiple-value-bind (okp line code plan)
        (submit k (list :verb :redo-plan :of "u1" :by "rowan"
                        :request "rp1" :stamp "2026-09-19T01:02:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the row redo-plan was refused: ~A" line)
      (check-equal 0 code "redo-plan exit code")
      (ok (rr-row-for plan :members) "the redo plan does not name the membership"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :redo :of "u1" :by "rowan"
                        :request "redo1" :stamp "2026-09-19T01:03:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the row redo was refused: ~A" line)
      (check-equal 0 code "redo exit code")
      (ok (search "REDO OK" line) "the redo line: ~A" line))
    (check-equal '("root/f1") (rm-members k) "the redo did not reapply the intent")
    ;; The undo is reapplied against current preconditions, never deleted.
    (ok (find :undo (roadmap-view-log (kernel-state k) "rm") :key (lambda (e) (getf e :op)))
        "the redo deleted the undo")))

(deftest "redo-refuses-a-stale-roadmap-row-plan" "docs/SPEC-WORK.md:2873"
    "expected=stale-plan-named;nothing-applied;the-undo-still-there"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-row k "root/f1" "r1")
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "r1" :by "rowan"
                        :request "u2" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of the row was refused: ~A" line)
      (check-equal 0 code "undo exit code"))
    ;; The view moves after the undo: the redo's preconditions are gone.
    (rr-add-row k "root/f2" "r2")
    (let ((members (rm-members k))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo-plan :of "u2" :by "rowan"
                          :request "rp2" :stamp "2026-09-19T01:02:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale row redo-plan was accepted: ~A" line)
        (check-equal 1 code "stale row redo-plan exit code")
        (ok (search "stale plan changed" line) "the refusal does not name what changed: ~A" line)
        (ok (search "members" line) "the refusal does not name the field: ~A" line))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo :of "u2" :by "rowan"
                          :request "redo2" :stamp "2026-09-19T01:03:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale row redo was applied: ~A" line)
        (check-equal 1 code "stale row redo exit code")
        (ok (search "not applied" line) "the refusal does not say nothing was applied: ~A" line))
      (check-equal members (rm-members k) "the refused redo moved the view")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the refused redo moved the revision"))))

;;; ------------------------------------------------------------------
;;; The table row: `roadmap projection` is reversible, in its preimage form.
;;; SPEC-WORK.md:2873.
;;; ------------------------------------------------------------------

(deftest "roadmap-projection-is-reversible-through-undo" "docs/SPEC-WORK.md:2873"
    "expected=the-removed-plist-comes-back-whole-and-at-its-index;the-dry-run-moves-nothing"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (rr-add-projection k "p2" "q2")
    (check-equal '("p1" "p2") (rm-projection-ids k) "projection order was not preserved")
    ;; A `--remove` of a projection NOT last in `:projections`.
    (rr-remove-projection k "p1" "q-rm")
    (check-equal '("p2") (rm-projection-ids k) "the removal did not drop the projection")
    ;; The dry run names the scope rows and accepts no mutation.
    (let ((ids (rm-projection-ids k))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code plan)
          (submit k (list :verb :undo-plan :of "q-rm" :by "rowan"
                          :request "up-pp" :stamp "2026-09-19T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok okp "undo-plan over a roadmap projection was refused: ~A" line)
        (check-equal 0 code "undo-plan exit code")
        (ok (search "reversible=yes" line) "the plan does not call it reversible: ~A" line)
        (ok (plusp (length (rr-plan-rows plan))) "the plan named no rows")
        (dolist (row (rr-plan-rows plan))
          (check-equal :scope (getf row :effect) "a projection plan's effect class")))
      (check-equal ids (rm-projection-ids k) "the dry run moved the view")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the dry run moved the revision"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "q-rm" :by "rowan"
                        :request "u-pp" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of a projection removal was refused: ~A" line)
      (check-equal 0 code "undo exit code")
      (ok (search "UNDO OK" line) "the undo line: ~A" line))
    (check-equal '("p1" "p2") (rm-projection-ids k)
                 "the removed projection did not come back at its original index")
    ;; The removed plist is the only copy; the undo brings all of it back.
    (let ((p1 (find "p1" (roadmap-view-projections (kernel-state k) "rm")
                    :key (lambda (p) (getf p :id)) :test #'string=)))
      (ok p1 "the removed projection came back without its record")
      (check-equal "acme/work" (getf p1 :repo) "the restored projection lost its repo")
      (check-equal "docs/p1.md" (getf p1 :path) "the restored projection lost its path")
      (check-equal "<!-- S -->" (getf p1 :start) "the restored projection lost its start marker"))))

;;; ------------------------------------------------------------------
;;; A stale projection plan refuses atomically, naming what changed.
;;; SPEC-WORK.md:2831-2833.
;;; ------------------------------------------------------------------

(deftest "a-stale-roadmap-projection-undo-refuses-atomically" "docs/SPEC-WORK.md:2873"
    "expected=the-view-moved-named;nothing-half-applied"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    ;; A second projection moves the view past the first one's postimage.
    (rr-add-projection k "p2" "q2")
    (let ((ids (rm-projection-ids k))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo-plan :of "q1" :by "rowan"
                          :request "up-pstale" :stamp "2026-09-19T01:00:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale projection plan was accepted: ~A" line)
        (ok (search "the view moved" line) "the refusal does not name what changed: ~A" line)
        (ok (search "projections" line) "the refusal does not name the moved field: ~A" line)
        (check-equal 1 code "stale projection plan exit code"))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :undo :of "q1" :by "rowan"
                          :request "u-pstale" :stamp "2026-09-19T01:01:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale projection undo was applied: ~A" line)
        (ok (search "conflict" line) "the refusal does not name the conflict: ~A" line)
        (check-equal 1 code "stale projection undo exit code"))
      (check-equal ids (rm-projection-ids k) "the refused undo moved the view")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the refused undo moved the revision"))))

;;; ------------------------------------------------------------------
;;; Redo reapplies the projection intent and never deletes the undo.
;;; SPEC-WORK.md:2829-2830.
;;; ------------------------------------------------------------------

(deftest "redo-reapplies-a-roadmap-projection" "docs/SPEC-WORK.md:2873"
    "expected=redo-plan-names-the-rows;redo-restores-the-postimage;the-undo-is-not-deleted"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "q1" :by "rowan"
                        :request "u3" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of the projection was refused: ~A" line)
      (check-equal 0 code "undo exit code"))
    (check-equal '() (rm-projection-ids k) "the undo did not take")
    (multiple-value-bind (okp line code plan)
        (submit k (list :verb :redo-plan :of "u3" :by "rowan"
                        :request "rp3" :stamp "2026-09-19T01:02:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the projection redo-plan was refused: ~A" line)
      (check-equal 0 code "redo-plan exit code")
      (ok (rr-row-for plan :projections) "the redo plan does not name the projections"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :redo :of "u3" :by "rowan"
                        :request "redo3" :stamp "2026-09-19T01:03:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the projection redo was refused: ~A" line)
      (check-equal 0 code "redo exit code")
      (ok (search "REDO OK" line) "the redo line: ~A" line))
    (check-equal '("p1") (rm-projection-ids k) "the redo did not reapply the intent")
    (ok (find :undo (roadmap-view-log (kernel-state k) "rm") :key (lambda (e) (getf e :op)))
        "the redo deleted the undo")))

(deftest "redo-refuses-a-stale-roadmap-projection-plan" "docs/SPEC-WORK.md:2873"
    "expected=stale-plan-named;nothing-applied;the-undo-still-there"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "q1" :by "rowan"
                        :request "u4" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of the projection was refused: ~A" line)
      (check-equal 0 code "undo exit code"))
    ;; The view moves after the undo: the redo's preconditions are gone.
    (rr-add-projection k "p2" "q2")
    (let ((ids (rm-projection-ids k))
          (revision (roadmap-view-revision (kernel-state k) "rm")))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo-plan :of "u4" :by "rowan"
                          :request "rp4" :stamp "2026-09-19T01:02:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale projection redo-plan was accepted: ~A" line)
        (check-equal 1 code "stale projection redo-plan exit code")
        (ok (search "stale plan changed" line) "the refusal does not name what changed: ~A" line)
        (ok (search "projections" line) "the refusal does not name the field: ~A" line))
      (multiple-value-bind (okp line code)
          (submit k (list :verb :redo :of "u4" :by "rowan"
                          :request "redo4" :stamp "2026-09-19T01:03:00Z"
                          :clock :tool :generation-owner "gen-4"))
        (ok (not okp) "a stale projection redo was applied: ~A" line)
        (check-equal 1 code "stale projection redo exit code")
        (ok (search "not applied" line) "the refusal does not say nothing was applied: ~A" line))
      (check-equal ids (rm-projection-ids k) "the refused redo moved the view")
      (check-equal revision (roadmap-view-revision (kernel-state k) "rm")
                   "the refused redo moved the revision"))))

;;; ------------------------------------------------------------------
;;; An identical `roadmap projection` re-add is the accepted no-effect
;;; receipt: it records a real event id's worth of receipt with changed=0,
;;; a retry replays the stored receipt, and undo reaches the typed
;;; compensation instead of refusing `no such request`. SPEC-WORK.md:370-377
;;; and :3125-3132.
;;; ------------------------------------------------------------------

(deftest "an-identical-projection-re-add-records-a-real-no-effect-receipt"
    "docs/SPEC-WORK.md:370-377"
    "expected=an-accepted-no-op-records-a-durable-receipt-changed=0;nothing-moved"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (multiple-value-bind (okp line code)
        (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                              :repo "acme/work" :path "docs/p1.md"
                              :start "<!-- S -->" :end "<!-- E -->"
                              :policy :markdown-table :reason "proj" :request "q-noop")
      (ok okp "the identical re-add was refused: ~A" line)
      (ok (search "changed=0" line) "the no-op line does not say changed=0: ~A" line)
      (check-equal 0 code "the no-op re-add exit code"))
    (let ((entry (gethash "q-noop" (nova-work::kernel-applied k))))
      (ok entry "the identical re-add recorded no receipt")
      (check-equal :roadmap-projection (getf entry :verb) "the receipt's verb")
      (check-equal 0 (getf entry :changed) "the receipt's changed"))
    (check-equal '("p1") (rm-projection-ids k) "the no-op moved the projections")))

(deftest "an-identical-projection-re-add-retried-returns-its-original-receipt"
    "docs/SPEC-WORK.md:370-377"
    "expected=a-retry-replays-the-stored-receipt;a-different-payload-refuses"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (multiple-value-bind (okp line code)
        (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                              :repo "acme/work" :path "docs/p1.md"
                              :start "<!-- S -->" :end "<!-- E -->"
                              :policy :markdown-table :reason "proj" :request "q-noop")
      (ok okp "the no-op re-add was refused: ~A" line)
      (check-equal 0 code "the no-op re-add exit code"))
    (let ((stored (gethash "q-noop" (nova-work::kernel-applied k))))
      (ok stored "the no-op re-add recorded no receipt")
      (multiple-value-bind (okp line code)
          (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                                :repo "acme/work" :path "docs/p1.md"
                                :start "<!-- S -->" :end "<!-- E -->"
                                :policy :markdown-table :reason "proj" :request "q-noop")
        (ok okp "the retry was refused: ~A" line)
        (check-equal (getf stored :line) line "the retry did not return the stored receipt")
        (check-equal 0 code "the retry exit code"))
      (multiple-value-bind (okp line code)
          (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                                :repo "acme/work" :path "docs/other.md"
                                :start "<!-- S -->" :end "<!-- E -->"
                                :policy :markdown-table :reason "proj" :request "q-noop")
        (ok (not okp) "a different payload under the same request id was accepted: ~A" line)
        (ok (search "reused with a different payload" line)
            "the refusal does not name the conflict: ~A" line)
        (check-equal 1 code "the conflict exit code")))
    (check-equal '("p1") (rm-projection-ids k) "the retries moved the projections")))

(deftest "undo-of-an-accepted-no-op-projection-reaches-typed-compensation"
    "docs/SPEC-WORK.md:3125-3132"
    "expected=the-no-op-undo-plans-nothing-and-applies-UNDO-OK;never-no-such-request"
  (let ((k (roadmap-rows-kernel)))
    (rr-add-projection k "p1" "q1")
    (multiple-value-bind (okp line code)
        (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                              :repo "acme/work" :path "docs/p1.md"
                              :start "<!-- S -->" :end "<!-- E -->"
                              :policy :markdown-table :reason "proj" :request "q-noop")
      (ok okp "the no-op re-add was refused: ~A" line)
      (check-equal 0 code "the no-op re-add exit code"))
    (multiple-value-bind (okp line code plan)
        (submit k (list :verb :undo-plan :of "q-noop" :by "rowan"
                        :request "up-noop" :stamp "2026-09-19T01:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo-plan over the accepted no-op was refused: ~A" line)
      (check-equal 0 code "the undo-plan exit code")
      (check-equal '() (rr-plan-rows plan) "the no-op plan should move nothing"))
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "q-noop" :by "rowan"
                        :request "u-noop" :stamp "2026-09-19T01:01:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok okp "the undo of the accepted no-op was refused: ~A" line)
      (check-equal 0 code "the undo exit code")
      (ok (search "UNDO OK" line) "the undo line is not UNDO OK: ~A" line))
    (check-equal '("p1") (rm-projection-ids k) "the undo moved the projections")))
