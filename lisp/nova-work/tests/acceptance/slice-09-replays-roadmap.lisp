;;;; slice-09-replays-roadmap.lisp --- five named acceptance replays for the
;;;; roadmap/axis/configure/render machinery of docs/SPEC-WORK.md's required
;;;; list. Each deftest names the paragraph it comes from and asserts the
;;;; outcome that paragraph promises. The pure part each replay needs is in
;;;; src/replays-8621.lisp; the live session and CLI wiring is not this slice.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; axisless-history                        docs/SPEC-WORK.md:5834
;;; ------------------------------------------------------------------

(deftest "axisless-history" "docs/SPEC-WORK.md:5986"
    "expected=ordered-rows;denominator-not-reduced-by-completion;retire-keeps-node;prior-view-at-captured-revision"
  ;; SPEC-WORK.md:5986-5989 -- two ordered rows added by `roadmap row`, one
  ;; finished, the state exported and loaded, the view reopened past the default
  ;; window: both rows and their evidence present, the denominator not reduced by
  ;; completion; a row retired records a scope movement, keeps its node, and the
  ;; prior view reconstructs at its captured revision.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/f1" :type :feature :parent "root" :state :unknown)
                 (:id "root/f1/t" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f2" :type :feature :parent "root" :state :unknown)
                 (:id "root/f2/t" :type :task :parent "root/f2" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :feature
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '() :permitted-roots '() :reason "new"
                        :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    ;; Two ordered rows, added by `roadmap row`.
    (multiple-value-bind (okp line code)
        (roadmap-row k :roadmap "rm" :member "root/f1" :op :add
                     :reason "row" :request "rr-1")
      (ok okp "the first row was refused: ~A" line)
      (check-equal 0 code "first row exit"))
    (multiple-value-bind (okp line code)
        (roadmap-row k :roadmap "rm" :member "root/f2" :op :add
                     :reason "row" :request "rr-2")
      (ok okp "the second row was refused: ~A" line)
      (check-equal 0 code "second row exit"))
    (check-equal '("root/f1" "root/f2") (roadmap-view-members (kernel-state k) "rm")
                 "row order was not preserved")
    ;; One finished: completion is not removal.
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "root/f1/t" :request "done-1"))
      (ok okp "finishing the first row was refused: ~A" line)
      (check-equal 0 code "finish exit"))
    (ok (not (roadmap-settled-p (kernel-state k) "rm"))
        "the view settled while a row was still open")
    (check-equal 2 (length (roadmap-view-members (kernel-state k) "rm"))
                 "completion removed a row from the view")
    (check-equal 1 (roadmap-open-member-count (kernel-state k) "rm")
                 "completion reduced the denominator")
    (ok (roadmap-member-evidence (kernel-state k) "root/f1")
        "the finished row lost its evidence")
    ;; The state exported and loaded, the view reopened past the default window.
    (let* ((captured (export-roadmap-view (node-view (kernel-state k) "rm")))
           (loaded (load-roadmap-view captured))
           (reopened (roadmap-view-open loaded :window 1)))
      (check-equal 2 (length (getf reopened :members))
                   "reopen past the window dropped a row")
      (dolist (id '("root/f1" "root/f2"))
        (ok (member id (getf reopened :members) :test #'string=)
            "row ~A was missing after reopen" id)
        (ok (nova-work::%node-quiet (kernel-state k) id)
            "row ~A lost its node after reopen" id))
      ;; A row retired records a scope movement and keeps its node.
      (let ((rev-before (roadmap-view-revision (kernel-state k) "rm")))
        (multiple-value-bind (okp line code)
            (roadmap-row k :roadmap "rm" :member "root/f2" :op :remove
                         :reason "retire" :request "rr-3")
          (ok okp "retiring a row was refused: ~A" line)
          (check-equal 0 code "retire exit"))
        (ok (> (roadmap-view-revision (kernel-state k) "rm") rev-before)
            "retirement did not record a scope movement"))
      (ok (member "root/f2" (roadmap-view-retired (kernel-state k) "rm") :test #'string=)
          "the retired row was not recorded")
      (ok (nova-work::%node-quiet (kernel-state k) "root/f2")
          "retirement removed the node")
      (check-equal '("root/f1") (roadmap-view-members (kernel-state k) "rm")
                   "the retired row stayed in the view")
      (check-equal 1 (length (roadmap-view-members (kernel-state k) "rm"))
                   "retirement did not drop the denominator")
      ;; The prior view reconstructs at its captured revision.
      (check-equal 2 (length (getf loaded :members))
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

(deftest "completed-view-mutation" "docs/SPEC-WORK.md:5997"
    "expected=reads-revive-nothing;outstanding-member-revives-atomically;no-settled-container-holds-open-required-work;reference-fold-matches"
  ;; SPEC-WORK.md:5997-6000 -- metadata, projection and render on a settled
  ;; roadmap reviving nothing; an outstanding member added applying the atomic
  ;; revival rule so no settled container silently holds open required work;
  ;; counts and indexes checked by the reference fold after each step.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/f1" :type :feature :parent "root" :state :unknown)
                 (:id "root/f1/t" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f2" :type :feature :parent "root" :state :unknown)
                 (:id "root/f2/t" :type :task :parent "root/f2" :state :doing)
                 (:id "root/f3" :type :feature :parent "root" :state :unknown)
                 (:id "root/f3/t" :type :task :parent "root/f3" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)))
         (state nil)
         (fold (lambda ()
                 (let ((members (roadmap-view-members (kernel-state k) "rm")))
                   (count-if-not (lambda (m) (eq :c (node-branch (kernel-state k) m)))
                                 members)))))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "R" :row-kind :feature
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '() :permitted-roots '() :reason "new"
                        :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the roadmap was not created: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    (dolist (m '("root/f1" "root/f2"))
      (multiple-value-bind (okp line code)
          (roadmap-row k :roadmap "rm" :member m :op :add :reason "row"
                       :request (format nil "rr-~A" m))
        (ok okp "adding row ~A was refused: ~A" m line)
        (check-equal 0 code "row add exit")))
    (dolist (tn '("root/f1/t" "root/f2/t"))
      (multiple-value-bind (okp line code)
          (submit k (close-request :node tn :request (format nil "done-~A" tn)))
        (ok okp "finishing ~A was refused: ~A" tn line)
        (check-equal 0 code "finish exit")))
    (setf state (kernel-state k))
    (ok (roadmap-settled-p state "rm") "the roadmap did not settle with every row finished")
    (check-equal 0 (roadmap-open-member-count state "rm")
                 "a settled roadmap held open required work")
    (check-equal (funcall fold) (roadmap-open-member-count state "rm")
                 "the counter and the reference fold disagreed at settle")
    ;; Metadata, projection and render on a settled roadmap revive nothing.
    (multiple-value-bind (okp line code)
        (node-edit k "rm" :changes '(:title "Renamed") :reason "meta" :request "me-1")
      (ok okp "metadata on a settled roadmap was refused: ~A" line)
      (check-equal 0 code "metadata exit"))
    (ok (stringp (roadmap-view-render (kernel-state k) "rm"))
        "render on a settled roadmap produced nothing")
    (multiple-value-bind (okp line code)
        (roadmap-projection k :roadmap "rm" :op :add :id "p1" :root "root"
                            :repo "acme/work" :path "docs/roadmap.md"
                            :start "<!-- ROADMAP:START -->" :end "<!-- ROADMAP:END -->"
                            :policy :markdown-table :reason "proj" :request "pp-1")
      (ok okp "a projection on a settled roadmap was refused: ~A" line)
      (check-equal 0 code "projection add exit"))
    (ok (roadmap-settled-p (kernel-state k) "rm")
        "a read or projection revived the settled roadmap")
    (check-equal '() (roadmap-view-revive-events (kernel-state k) "rm")
                 "a metadata, projection or render step revived the roadmap")
    (check-equal (funcall fold) (roadmap-open-member-count (kernel-state k) "rm")
                 "the counter and the reference fold disagreed after projection")
    ;; An outstanding member added applies the atomic revival rule.
    (multiple-value-bind (okp line code revive)
        (roadmap-row k :roadmap "rm" :member "root/f3" :op :add :reason "new"
                     :request "rr-f3")
      (ok okp "adding an outstanding member was refused: ~A" line)
      (check-equal 0 code "outstanding add exit")
      (ok revive "adding an outstanding member did not revive the container")
      (check-equal 1 (length (roadmap-view-revive-events (kernel-state k) "rm"))
                   "the revival was not atomic")
      (ok (not (roadmap-settled-p (kernel-state k) "rm"))
          "a settled container silently held open required work")
      (check-equal 1 (roadmap-open-member-count (kernel-state k) "rm")
                   "the added outstanding member was not counted")
      (check-equal (funcall fold) (roadmap-open-member-count (kernel-state k) "rm")
                   "the counter and the reference fold disagreed after revival"))
    ;; A matrix selection naming a missing or duplicate axis refuses.
    (multiple-value-bind (okp line code)
        (roadmap-projection k :roadmap "rm" :op :add :id "p2" :root "root"
                            :repo "acme/work" :path "docs/matrix.md"
                            :start "S" :end "E" :policy :markdown-table
                            :row-axis "nope" :column-axis "nope" :fixed '()
                            :reason "bad" :request "pp-2")
      (ok (null okp) "a bad matrix selection was accepted")
      (check-equal 2 code "bad-selection exit")
      (ok (search "bad selection" line) "the bad-selection refusal: ~A" line))))

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

;;; ------------------------------------------------------------------
;;; percent-axis-on-a-matrix                docs/SPEC-WORK.md:5590
;;; ------------------------------------------------------------------

(deftest "percent-axis-on-a-matrix" "docs/SPEC-WORK.md:5590"
    "expected=applicable-is-per-axis-member;rows-and-baseline-rows-roadmap-wide;percentage-over-applicable;zero-applicable-prints-no-percentage;matrix-requires-axis;non-matrix-refuses-axis;partial-cell-k-n-and-unknown"
  ;; SPEC-WORK.md:3118, :1937-1951, :2101, :5590-5593 -- `percent --axis
  ;; <member>` on a matrix: applicable= is the live rows less those with a
  ;; recorded out-of-scope cell for that member, rows= and baseline-rows= are
  ;; the roadmap's own, the percentage is green over applicable, and a percent
  ;; over zero applicable rows prints green=0 applicable=0 with no percentage.
  (let* ((seed (append
                '((:id "root" :type :work-set :parent nil :state :unknown))
                (loop for i from 1 to 10
                      collect (list :id (format nil "root/f~D" i) :type :feature
                                    :parent "root" :state :unknown))
                (loop for i from 1 to 10
                      collect (list :id (format nil "root/f~D/t" i) :type :task
                                    :parent (format nil "root/f~D" i) :state :doing))
                ;; f3 carries a second, still-open required leaf: a partial cell.
                (list (list :id "root/f3/t2" :type :task :parent "root/f3"
                            :state :unknown))))
         (k (make-kernel :state (make-seed-state seed)))
         (rows (loop for i from 1 to 10 collect (format nil "root/f~D" i)))
         (cols (loop for i from 1 to 9 collect (format nil "c~D" i))))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "M" :row-kind :feature
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '("row" "col") :permitted-roots '() :reason "new"
                        :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the matrix roadmap was not created: ~A" line)
      (check-equal 0 code "matrix create exit"))
    (check-equal 2 (length (roadmap-view-axes (kernel-state k) "rm"))
                 "the matrix did not declare two axes")
    ;; Nine rows settle; f3 stays open with one of its two leaves finished.
    (dolist (i '(1 2 4 5 6 7 8 9 10))
      (multiple-value-bind (okp line code)
          (submit k (close-request :node (format nil "root/f~D/t" i)
                                   :request (format nil "d~D" i)))
        (ok okp "closing a leaf of row ~D was refused: ~A" i line)
        (check-equal 0 code "close exit")))
    (multiple-value-bind (okp line code)
        (submit k (close-request :node "root/f3/t" :request "d3a"))
      (ok okp "closing f3's first leaf was refused: ~A" line)
      (check-equal 0 code "f3 close exit"))
    ;; The axis members the `axis --add` verb writes: ten rows, nine columns.
    ;; (The kernel commits a copy per envelope, so the view is written after the
    ;; closes.)
    (let* ((state (kernel-state k))
           (wnode (nova-work::%node-quiet state "rm"))
           (view (nova-work::wnode-view wnode)))
      (setf (getf view :axis-members) (list (cons "row" (copy-list rows))
                                            (cons "col" (copy-list cols))))
      (setf (getf view :members) (copy-list rows))
      ;; One out-of-scope cell for c2 -- the row f3 -- and every row out of
      ;; scope for c9, whose applicable set is therefore empty.
      (setf (getf view :cells)
            (append (list (cons (list "root/f3" "c2") '(:out-of-scope t)))
                    (loop for r in rows
                          collect (cons (list r "c9") '(:out-of-scope t)))))
      ;; A new plist key rebuilds the list, so write it back onto the node.
      (setf (nova-work::wnode-view wnode) view))
    ;; c1: every row applies; nine are green, f3 is a partial cell.
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm" :axis "c1")
      (ok okp "percent --axis c1 was refused: ~A" line)
      (check-equal 0 code "percent c1 exit")
      (ok (search "green=9" line) "c1 green was not 9: ~A" line)
      (ok (search "applicable=10" line) "c1 applicable was not 10: ~A" line)
      (ok (search "rows=10" line) "c1 rows was not 10: ~A" line)
      (ok (search "baseline-rows=10" line) "c1 baseline-rows was not 10: ~A" line)
      (ok (search "percent=90%" line) "c1 percentage was not 90%: ~A" line)
      (ok (search "k/n=1/2" line) "the partial cell's k/n was not 1/2: ~A" line)
      (ok (search "unknown=1" line) "the partial cell's unknown was not 1: ~A" line))
    ;; c2: the open row f3 is out of scope, so nine apply and all nine are
    ;; green -- the percentage is over applicable, not over rows.
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm" :axis "c2")
      (ok okp "percent --axis c2 was refused: ~A" line)
      (check-equal 0 code "percent c2 exit")
      (ok (search "applicable=9" line) "c2 applicable was not 9: ~A" line)
      (ok (search "green=9" line) "c2 green was not 9: ~A" line)
      (ok (search "rows=10" line) "c2 rows was not 10: ~A" line)
      (ok (search "percent=100%" line) "c2 percentage was not 100%: ~A" line))
    ;; c9: zero applicable rows prints green=0 applicable=0 and no percentage.
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm" :axis "c9")
      (ok okp "percent --axis c9 was refused: ~A" line)
      (check-equal 0 code "percent c9 exit")
      (ok (search "green=0 applicable=0" line)
          "the zero-applicable line was not green=0 applicable=0: ~A" line)
      (ok (not (search "percent=" line))
          "a percentage was printed over zero applicable rows: ~A" line))
    ;; A matrix requires --axis, an unknown member refuses, and a zero-axis
    ;; roadmap refuses the flag it does not take (:3118).
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm")
      (ok (null okp) "a matrix percent with no --axis was accepted")
      (check-equal 2 code "missing axis exit")
      (ok (search "--axis" line) "the missing-axis refusal did not name the flag: ~A" line))
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm" :axis "nope")
      (ok (null okp) "an unknown axis member was accepted")
      (check-equal 2 code "unknown member exit"))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm-ax" :parent "root" :title "A" :row-kind :feature
                        :aggregation :required-members
                        :completion-policy :all-required-features
                        :axes '() :permitted-roots '() :reason "new"
                        :request "rm-2" :stamp "2026-09-17T00:00:00Z")
      (ok okp "the axisless roadmap was not created: ~A" line)
      (check-equal 0 code "axisless create exit"))
    (multiple-value-bind (okp line code) (roadmap-percent k :node "rm-ax" :axis "c1")
      (ok (null okp) "an axisless percent with --axis was accepted")
      (check-equal 2 code "non-matrix axis exit")
      (ok (search "--axis" line) "the non-matrix refusal did not name the flag: ~A" line))))
