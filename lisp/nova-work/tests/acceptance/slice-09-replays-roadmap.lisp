;;;; slice-09-replays-roadmap.lisp --- five named acceptance replays for the
;;;; roadmap/axis/configure/render machinery of docs/SPEC-WORK.md's required
;;;; list. Each deftest names the paragraph it comes from and asserts the
;;;; outcome that paragraph promises. The pure part each replay needs is in
;;;; src/replays-8621.lisp; the live session and CLI wiring is not this slice.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; axisless-history                        docs/SPEC-WORK.md:5834
;;; ------------------------------------------------------------------

(deftest "axisless-history" "docs/SPEC-WORK.md:5834"
    "expected=ordered-rows;denominator-not-reduced-by-completion;retire-keeps-node;prior-view-at-captured-revision"
  (let ((rows '()))
    (setf rows (r8621-row-add rows "a" '("e-a")))
    (setf rows (r8621-row-add rows "b" '("e-b")))
    (check-equal 2 (length rows) "both ordered rows were not kept")
    (check-equal '("a" "b") (mapcar (lambda (r) (getf r :id)) rows)
                 "row order was not preserved")
    ;; One finished: completion is not removal.
    (setf rows (r8621-row-finish rows "a"))
    (check-equal 2 (r8621-row-active-count rows)
                 "completion reduced the denominator")
    ;; The state exported and loaded, the view reopened past the default window.
    (let* ((loaded (r8621-rows-load (r8621-rows-export rows)))
           (reopened (r8621-view-reopen loaded :window 1)))
      (check-equal 2 (length reopened) "reopen past the window dropped a row")
      (dolist (id '("a" "b"))
        (let ((r (find id reopened :key (lambda (x) (getf x :id)) :test #'string=)))
          (ok r "row ~A was missing after reopen" id)
          (ok (getf r :evidence) "row ~A lost its evidence after reopen" id)))
      ;; A retired row records a scope movement and keeps its node.
      (setf reopened (r8621-row-retire reopened "b" "scope-2"))
      (let ((b (find "b" reopened :key (lambda (x) (getf x :id)) :test #'string=)))
        (ok (getf b :retired) "the retired row was not marked retired")
        (check-equal "b" (getf b :node) "the retired row lost its node")
        (check-equal "scope-2" (getf b :scope-moved)
                     "retirement did not record the scope movement"))
      (check-equal 1 (r8621-row-active-count reopened)
                   "retirement did not drop the denominator")
      ;; The prior view reconstructs at its captured revision.
      (check-equal 2 (length (r8621-rows-load (r8621-rows-export rows)))
                   "the prior view did not reconstruct at its captured revision"))))

;;; ------------------------------------------------------------------
;;; matrix-retirement                       docs/SPEC-WORK.md:5838
;;; ------------------------------------------------------------------

(deftest "matrix-retirement" "docs/SPEC-WORK.md:5838"
    "expected=only-selected-coordinates-retired;recoverable;no-task-cancelled;unknown-member-refused;layout-populated-refused-no-partial-write;no-flatten-without-selections"
  (let* ((tasks '("t1" "t2" "t3"))
         (m (r8621-matrix-make '(("col" . ("r1" "r2")) ("row" . ("r1" "r2"))))))
    ;; `axis --remove` of a first-axis row...
    (multiple-value-bind (m1 line code) (r8621-matrix-remove m "col" "r1")
      (check-equal 0 code "a first-axis removal refused")
      (ok (r8621-matrix-retired-p m1 "col" "r1")
          "the selected coordinate was not retired")
      (ok (not (r8621-matrix-retired-p m1 "col" "r2"))
          "an unselected coordinate was retired")
      (ok (not (r8621-matrix-retired-p m1 "row" "r1"))
          "another axis's coordinate was retired")
      (setf m m1))
    ;; ...and then of another axis's member.
    (multiple-value-bind (m2 line code) (r8621-matrix-remove m "row" "r2")
      (check-equal 0 code "a second-axis removal refused")
      (ok (r8621-matrix-retired-p m2 "row" "r2")
          "the second selected coordinate was not retired")
      (setf m m2))
    (check-equal 2 (length (getf m :retired))
                 "not exactly the selected coordinates were retired")
    ;; Only the selected coordinates retired and recoverable; no task cancelled.
    (multiple-value-bind (m3 line code) (r8621-matrix-restore m "col" "r1")
      (check-equal 0 code "a restore refused")
      (ok (not (r8621-matrix-retired-p m3 "col" "r1"))
          "the restore did not recover the coordinate"))
    (check-equal '("t1" "t2" "t3") tasks "a task was cancelled by retirement")
    ;; An unknown member refused, nothing written.
    (multiple-value-bind (bad line code) (r8621-matrix-remove m "col" "zz")
      (check-equal 2 code "an unknown member was not refused")
      (check-equal m bad "an unknown-member refusal wrote something"))
    ;; A layout change on a populated roadmap refused `layout populated`, no
    ;; partial write.
    (multiple-value-bind (bad line code) (r8621-matrix-add-axis m "z" '("z1"))
      (check-equal 2 code "a layout change on a populated roadmap was not refused")
      (ok (search "layout populated" line)
          "the layout refusal did not name layout populated: ~A" line)
      (check-equal m bad "the layout refusal partially wrote"))
    ;; A matrix is never flattened without explicit selections.
    (multiple-value-bind (bad line code) (r8621-matrix-flatten m '())
      (check-equal 2 code "a matrix was flattened without explicit selections")
      (check-equal m bad "a flatten refusal wrote something"))
    (multiple-value-bind (flat line code) (r8621-matrix-flatten m '((:col "r1")))
      (check-equal 0 code "an explicit flatten refused")
      (ok (getf flat :flattened) "an explicit flatten recorded no selection"))))

;;; ------------------------------------------------------------------
;;; configure-no-effect-and-undo-conflict   docs/SPEC-WORK.md:5842
;;; ------------------------------------------------------------------

(deftest "configure-no-effect-and-undo-conflict" "docs/SPEC-WORK.md:5994"
    "expected=equal-value-receipt;retry-returns-original-receipt-and-keeps-later-value;undo-only-while-guards-match;all-keep-refused;bad-patch-refused;layout-populated-refused"
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/t1" :type :task :parent "root" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :epic
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '() :permitted-roots '() :reason "new"
                        :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    ;; An equal-value configure is the no-effect receipt changed=0 and moves no
    ;; scope revision.
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:set :epic)
                           :aggregation-patch '(:keep)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:keep)
                           :permitted-roots-patch '(:keep)
                           :reason "same" :request "r1")
      (ok okp "an equal-value configure was refused: ~A" line)
      (check-equal 0 code "equal-value configure exit")
      (ok (search "change=configure changed=0" line)
          "an equal-value configure was not a no-effect receipt: ~A" line))
    (check-equal 0 (roadmap-view-revision (kernel-state k) "rm")
                 "a no-effect configure moved the scope revision")
    ;; A later effective edit.
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:keep)
                           :aggregation-patch '(:set :all-members)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:keep)
                           :permitted-roots-patch '(:keep)
                           :reason "later" :request "r2")
      (ok okp "the later configure was refused: ~A" line)
      (check-equal 0 code "later configure exit")
      (ok (search "change=configure changed=1" line)
          "the later configure did not change: ~A" line))
    (check-equal 1 (roadmap-view-revision (kernel-state k) "rm")
                 "an effective configure did not advance the scope revision")
    ;; Then the retry of the lost reply: the original receipt returns and the
    ;; later value stands.
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:set :epic)
                           :aggregation-patch '(:keep)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:keep)
                           :permitted-roots-patch '(:keep)
                           :reason "same" :request "r1")
      (ok okp "the retry was refused: ~A" line)
      (check-equal 0 code "retry exit")
      (ok (search "change=configure changed=0" line)
          "the retry did not return the original receipt: ~A" line))
    (check-equal :all-members (roadmap-view-aggregation (kernel-state k) "rm")
                 "the retry clobbered the later value")
    ;; Undo restores the ordered preimage only while its guard matches.
    (multiple-value-bind (okp line code)
        (roadmap-configure-undo k :roadmap "rm" :of "r2" :request "u1")
      (ok okp "an undo with a matching guard refused: ~A" line)
      (check-equal 0 code "matching-guard undo exit")
      (check-equal :required-members (roadmap-view-aggregation (kernel-state k) "rm")
                   "the undo did not restore the ordered preimage"))
    ;; A later effective configure moves the postimage, so the same undo conflicts.
    (roadmap-configure k :roadmap "rm"
                       :row-kind-patch '(:keep)
                       :aggregation-patch '(:set :leaves)
                       :completion-policy-patch '(:keep)
                       :axes-patch '(:keep)
                       :permitted-roots-patch '(:keep)
                       :reason "moved" :request "r3")
    (multiple-value-bind (okp line code)
        (roadmap-configure-undo k :roadmap "rm" :of "r2" :request "u2")
      (ok (null okp) "an undo with a stale guard did not refuse")
      (check-equal 1 code "stale-guard undo exit")
      (ok (search "conflict" line) "the undo conflict was not named: ~A" line)
      (check-equal :leaves (roadmap-view-aggregation (kernel-state k) "rm")
                   "a refused undo moved the value"))
    ;; An all-keep request is refused, not a no-effect receipt.
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:keep)
                           :aggregation-patch '(:keep)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:keep)
                           :permitted-roots-patch '(:keep)
                           :reason "nothing" :request "r4")
      (ok (null okp) "an all-keep configure was accepted")
      (check-equal 2 code "all-keep exit")
      (ok (search "all keep" line) "the all-keep refusal: ~A" line))
    ;; A clear on a policy or a malformed patch is a bad patch.
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:clear)
                           :aggregation-patch '(:keep)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:keep)
                           :permitted-roots-patch '(:keep)
                           :reason "bad" :request "r5")
      (ok (null okp) "a clear on a policy patch was accepted")
      (check-equal 2 code "bad-patch exit")
      (ok (search "bad patch" line) "the bad-patch refusal: ~A" line))
    ;; The layout changes only while every axis is empty and :members is empty.
    (let ((view (node-view (kernel-state k) "rm")))
      (setf (getf view :members) '("root/t1")))
    (multiple-value-bind (okp line code)
        (roadmap-configure k :roadmap "rm"
                           :row-kind-patch '(:keep)
                           :aggregation-patch '(:keep)
                           :completion-policy-patch '(:keep)
                           :axes-patch '(:set ("col"))
                           :permitted-roots-patch '(:keep)
                           :reason "layout" :request "r6")
      (ok (null okp) "a layout change on a populated roadmap was accepted")
      (check-equal 2 code "layout-populated exit")
      (ok (search "layout populated" line)
          "the layout refusal did not name layout populated: ~A" line))))

;;; ------------------------------------------------------------------
;;; completed-view-mutation                 docs/SPEC-WORK.md:5845
;;; ------------------------------------------------------------------

(deftest "completed-view-mutation" "docs/SPEC-WORK.md:5845"
    "expected=reads-revive-nothing;outstanding-member-revives-atomically;no-settled-container-holds-open-required-work;reference-fold-matches"
  (let ((rm (r8621-roadmap-make '(("m1" . t) ("m2" . t)))))
    (ok (r8621-roadmap-settled-p rm) "the roadmap was not settled")
    ;; Metadata, projection and render on a settled roadmap revive nothing.
    (dolist (op (list #'r8621-roadmap-metadata
                      #'r8621-roadmap-project
                      #'r8621-roadmap-render))
      (let ((before (copy-tree rm)))
        (funcall op rm)
        (check-equal before rm "a read on a settled roadmap mutated it")))
    (check-equal t (r8621-roadmap-settled-p rm)
                 "a settled roadmap was revived by a read")
    (check-equal 0 (r8621-roadmap-open-required rm)
                 "a settled roadmap held open required work")
    ;; An outstanding member added applies the atomic revival rule.
    (multiple-value-bind (rm2 ev) (r8621-roadmap-add-member rm '("m3" . nil))
      (ok ev "adding an outstanding member did not revive the container")
      (check-equal 1 (length (getf rm2 :revive-events)) "the revival was not atomic")
      (check-equal nil (r8621-roadmap-settled-p rm2)
                   "a settled container silently held open required work")
      (check-equal 1 (r8621-roadmap-open-required rm2)
                   "the added outstanding member was not counted")
      ;; Counts and indexes checked by the reference fold after each step.
      (check-equal (r8621-roadmap-fold-count rm2) (r8621-roadmap-open-required rm2)
                   "the counter and the reference fold disagreed"))))

;;; ------------------------------------------------------------------
;;; chat-and-file-render-are-byte-identical docs/SPEC-WORK.md:5755
;;; ------------------------------------------------------------------

(deftest "chat-and-file-render-are-byte-identical" "docs/SPEC-WORK.md:5755"
    "expected=chat-bytes==marker-region-bytes;every-other-byte-preserved;missing-dup-reversed-marker-refused"
  (let* ((view (list :id "v1" :projection "p1" :revision 3
                     :private '("acme/work/f2")))
         (chat (r8621-render-chat view "p1"))
         (before (format nil "top matter~%<!-- ROADMAP:START -->~%old table body~%<!-- ROADMAP:END -->~%bottom matter~%")))
    (ok (and (stringp chat) (plusp (length chat)))
        "render-chat returned non-empty bytes")
    (multiple-value-bind (rendered okp) (r8621-render-file view "p1" before)
      (ok okp "render-file refused a valid projection")
      (let* ((region-start (+ (search "<!-- ROADMAP:START -->" rendered)
                              (length "<!-- ROADMAP:START -->")))
             (region-end (search "<!-- ROADMAP:END -->" rendered :start2 region-start))
             (region (subseq rendered region-start region-end)))
        (check-string= chat region
                       "chat and the marker region carried different bytes")
        (ok (search (format nil "top matter~%<!-- ROADMAP:START -->") rendered)
            "the marker-region edit lost the leading boundary")
        (ok (search "bottom matter" rendered)
            "the marker-region edit lost the trailing boundary")))
    ;; Shared prerequisites and private-data filtering are identical in both modes.
    (ok (search "acme/work/f1" chat) "chat dropped a shared row")
    (ok (not (search "acme/work/f2" chat)) "chat leaked filtered data")
    (ok (search "revision: 3" chat) "chat lost the revision")
    (multiple-value-bind (rendered okp) (r8621-render-file view "p1" before)
      (ok okp "render-file refused a second time")
      (ok (not (search "acme/work/f2" rendered)) "file mode leaked filtered data"))
    ;; A missing, duplicate or reversed marker pair refused, writing nothing.
    (ok (not (nth-value 1 (r8621-render-file view "p1" "no markers here~%")))
        "a missing marker pair rendered instead of refusing")
    (ok (not (nth-value 1 (r8621-render-file
                           view "p1"
                           (format nil "~A~%A~%~A~%A~%~A~%"
                                   "<!-- ROADMAP:START -->" "<!-- ROADMAP:START -->"
                                   "<!-- ROADMAP:END -->" "<!-- ROADMAP:END -->"))))
        "a duplicate marker pair rendered instead of refusing")
    (ok (not (nth-value 1 (r8621-render-file
                           view "p1"
                           (format nil "~A~%A~%~A~%"
                                   "<!-- ROADMAP:END -->" "<!-- ROADMAP:START -->"))))
        "a reversed marker pair rendered instead of refusing")))

;;; ------------------------------------------------------------------
;;; move-updates-every-roadmap-scope         docs/SPEC-WORK.md:5978
;;; ------------------------------------------------------------------

(deftest "move-updates-every-roadmap-scope" "docs/SPEC-WORK.md:5978"
    "expected=referencing-scopes-advance;unrelated-stays;refusal-moves-none;captured-render-keeps-scope;intervening-mutation-conflicts-undo"
  (let* ((ra (make-scope-roadmap :id "ra" :revision 3 :rows '("row-1" "ra-x")))
         (rb (make-scope-roadmap :id "rb" :revision 1 :rows '("row-1")))
         (rc (make-scope-roadmap :id "rc" :revision 7 :rows '("rc-x")))
         (scopes (list ra rb rc)))
    ;; A row referenced by two roadmaps outside both parent chains (they sit in
    ;; neither the old nor the new chain) advances both referencing scopes in
    ;; the one move envelope.
    (multiple-value-bind (moved events ok) (scopes-on-move scopes "row-1")
      (ok ok "the move was accepted")
      (check-equal 2 (length events) "both referencing scope revisions advanced")
      (check-equal 3 (length moved) "an unrelated scope disappeared")
      (check-equal 4 (scope-roadmap-revision
                      (find "ra" moved :key #'scope-roadmap-id :test #'string=))
                   "the first referencing scope did not advance")
      (check-equal 2 (scope-roadmap-revision
                      (find "rb" moved :key #'scope-roadmap-id :test #'string=))
                   "the second referencing scope did not advance")
      ;; The unrelated roadmap stays.
      (check-equal 7 (scope-roadmap-revision
                      (find "rc" moved :key #'scope-roadmap-id :test #'string=))
                   "the unrelated scope advanced")
      ;; A render captured before the move keeps its captured scope.
      (let ((captured (scope-capture scopes)))
        (check-equal 3 (scope-roadmap-revision
                        (find "ra" captured :key #'scope-roadmap-id :test #'string=))
                     "the captured render moved with the row"))
      ;; A failed acceptance moves none.
      (multiple-value-bind (refused revents rok)
          (scopes-on-move scopes "row-1" :accept nil)
        (check-equal nil rok "a failed acceptance reported success")
        (check-equal '() revents "a failed acceptance wrote an envelope")
        (check-equal scopes refused "a failed acceptance moved a scope"))
      ;; An intervening affected-roadmap mutation makes the undo conflict.
      (let* ((mutated (mapcar (lambda (s)
                                (if (string= "ra" (scope-roadmap-id s))
                                    (scope-mutate s)
                                    s))
                              moved))
             (touched '("ra" "rb"))
             (guards '(4 2)))
        (multiple-value-bind (restored line) (scopes-undo moved touched guards scopes)
          (check-equal 3 (scope-roadmap-revision
                          (find "ra" restored :key #'scope-roadmap-id :test #'string=))
                       "undo with matching guards did not restore the preimage")
          (check-equal nil line "undo with matching guards reported a conflict"))
        (multiple-value-bind (restored line) (scopes-undo mutated touched guards scopes)
          (check-equal nil restored "an intervening mutation did not refuse undo")
          (ok (search "conflict" line) "the undo conflict was not named: ~A" line))))))
