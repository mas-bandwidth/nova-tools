;;;; replays-8641.lisp --- five acceptance replays of the required-replays and
;;;; draft-27 sections of docs/SPEC-WORK.md. Each deftest is named exactly as the
;;;; spec names it and asserts the paragraph's reading.
;;;;
;;;;   a-broken-assertion-must-fail         (docs/SPEC-WORK.md:6371-6373, :6384-6387)
;;;;   a-savepoint-is-not-a-shared-backup   (:5790-5793, :6270-6303)
;;;;   a-stop-reaches-distributed-work      (:6374-6378)
;;;;   add-field-order-is-complete          (:5819-5822, :962-967, :2977-2999)
;;;;   archive-completeness                 (:6232)

(in-package #:nova-work/tests)

(deftest "a-broken-assertion-must-fail" "docs/SPEC-WORK.md:6371"
    "expected=mutation-makes-the-assertion-fail;green-badge-only-on-real-evidence;criterion-revision-uncertainty-preserved"
  (let* ((preserved (lambda (x) (concatenate 'string "kept:" x)))
         (assertion (lambda (b) (string= "kept:value" (funcall b "value"))))
         (break (lambda (b)
                  (declare (ignore b))
                  (lambda (x)
                    (declare (ignore x))
                    "broken")))
         (receipt (make-regression-receipt
                   :criterion "acme/work/f1/t1 acceptance-1"
                   :revision-coverage "rev 42..44"
                   :uncertainty "no receipt above rev 44"
                   :assertion assertion :mutation break)))
    ;; the assertion holds on the preserved behaviour ...
    (check-equal t (assertion-holds-p receipt preserved)
                 "the assertion holds on the preserved behaviour")
    ;; ... and the deliberately broken behaviour makes it fail, so it is real
    ;; regression evidence rather than a green badge.
    (check-equal t (regression-evidence-p receipt preserved)
                 "the mutation makes the assertion fail")
    (check-equal :evidence (green-badge-p receipt preserved)
                 "a real mutation is evidence")
    ;; the specific criterion, revision coverage and remaining uncertainty are
    ;; preserved, not a green badge.
    (check-string= "acme/work/f1/t1 acceptance-1" (regression-receipt-criterion receipt)
                   "the specific criterion is preserved")
    (check-string= "rev 42..44" (regression-receipt-revision-coverage receipt)
                   "the revision coverage is preserved")
    (check-string= "no receipt above rev 44" (regression-receipt-uncertainty receipt)
                   "the remaining uncertainty is preserved")
    ;; a test that still passes with its asserted behaviour broken is not
    ;; regression evidence and earns no badge.
    (let ((vacuous (make-regression-receipt
                    :criterion "acme/work/f1/t1 acceptance-1"
                    :revision-coverage "rev 42..44"
                    :uncertainty "none"
                    :assertion (lambda (b)
                                 (declare (ignore b))
                                 t)
                    :mutation break)))
      (check-equal nil (regression-evidence-p vacuous preserved)
                   "a test that still passes under the mutation is not evidence")
      (check-equal nil (green-badge-p vacuous preserved)
                   "no green badge for a vacuous test"))))

(deftest "a-savepoint-is-not-a-shared-backup" "docs/SPEC-WORK.md:6270"
    "expected=savepoint!=checkpoint;restore-owns-nothing;local-shared-unshared-failed-readable"
  (let* ((sp (make-savepoint
              :id "sp-1" :schema "work-savepoint-v1" :local-revision 812
              :journal-id "abc" :replay-cut '(:sequence 420 :sha256 "h1")
              :boundary '(:sequence 390 :sha256 "h2") :manifest "sha-1"
              :image "state" :local-replies "replies" :age 3600
              :failed-backup nil))
         (cp (make-checkpoint :id "cp-1" :shared-revision 800 :source "clip")))
    ;; the savepoint is local and is not reported as the shared backup.
    (check-equal t (savepoint-is-not-shared-backup-p sp)
                 "a savepoint is not a shared backup")
    ;; a restore opens read-only and isolated: it inherits no coordinator
    ;; ownership, reanimates no assignment, replays no message.
    (let ((r (restore-open :savepoint sp)))
      (check-equal t (getf r :read-only) "the restore is read-only")
      (check-equal t (getf r :isolated) "the restore is isolated")
      (check-equal nil (getf r :dispatch) "the restore dispatches nothing")
      (check-equal nil (getf r :ownership) "the restore takes no ownership")
      (check-equal '() (getf r :assignments) "the restore reanimates no assignment")
      (check-equal '() (getf r :messages) "the restore replays no message"))
    ;; savepoint age, the local and the shared revisions, the unshared work and
    ;; the failed backups are all readable.
    (let ((report (savepoint-report sp :shared-revision 800
                                       :unshared '("acme/work/f1/t1")
                                       :failed-backups 1)))
      (check-equal 3600 (getf report :age) "the savepoint age is readable")
      (check-equal 812 (getf report :local-revision) "the local revision is readable")
      (check-equal 800 (getf report :shared-revision) "the shared revision is readable")
      (check-equal '("acme/work/f1/t1") (getf report :unshared) "the unshared work is readable")
      (check-equal 1 (getf report :failed-backups) "the failed backups are readable"))
    ;; the same fields are what `savepoint list` exposes: the age, the local and
    ;; the shared revisions side by side, the unshared work and the failed
    ;; backup attempts.
    (let ((listed (savepoint-list (make-savepoint-store :verified sp)
                                  :shared-revision 800
                                  :unshared '("acme/work/f1/t1")
                                  :failed-backups 1)))
      (ok (search "age=3600" listed) "savepoint list shows the age: ~A" listed)
      (ok (search "local-revision=812" listed) "savepoint list shows the local revision")
      (ok (search "shared-revision=800" listed)
          "savepoint list shows the shared revision beside it")
      (ok (search "unshared" listed) "savepoint list shows the unshared work")
      (ok (search "failed-backups=1" listed) "savepoint list shows the failed backups"))
    ;; `savepoint compare --savepoint <path> --against <session|snapshot>` puts
    ;; the local and the against revisions side by side, names the work unshared
    ;; each way and the shared checkpoint separately, and never reports the
    ;; savepoint as that shared backup.
    (let* ((records (list (list :seq 1 :revision 700 :events '(1))
                          (list :seq 2 :revision 812 :events '(2))
                          (list :seq 3 :revision 820 :events '(3))))
           (cmp (savepoint-compare sp
                                   :against (list :kind :session
                                                  :revision 820
                                                  :checkpoint 750
                                                  :records records))))
      (check-equal "sp-1" (getf cmp :savepoint) "compare names the savepoint")
      (check-equal 812 (getf cmp :local-revision) "compare shows the local revision")
      (check-equal 820 (getf cmp :against-revision)
                   "compare shows the against revision beside it")
      (check-equal 750 (getf cmp :shared-checkpoint)
                   "compare shows the shared checkpoint separately")
      (check-equal '(812) (getf cmp :unshared)
                   "the work above the checkpoint and at or below the savepoint is unshared")
      (check-equal '(820) (getf cmp :missing)
                   "the work the against holds beyond the savepoint is named missing")
      (check-equal nil (getf cmp :shared) "a savepoint is not a shared backup")
      (ok (search "rev=812" (getf cmp :line)) "the compare line names the local revision")
      (ok (search "checkpoint=750" (getf cmp :line))
          "the compare line names the shared checkpoint separately"))
    ;; a local-only copy states the exposure; an independent verified copy
    ;; removes it.
    (let ((cmp (savepoint-compare sp :against (list :kind :snapshot
                                                    :revision 812
                                                    :checkpoint 750))))
      (check-equal t (getf cmp :local-only) "a local-only copy states the exposure"))
    (let ((cmp (savepoint-compare sp :against (list :kind :snapshot
                                                    :revision 812
                                                    :checkpoint 750)
                                       :independent-copy "bench-b")))
      (check-equal nil (getf cmp :local-only)
                   "an independent verified copy removes the exposure"))
    ;; a savepoint is never printed where a checkpoint was asked for.
    (handler-case
        (progn (checkpoint-line sp)
               (fail "a savepoint was printed where a checkpoint was asked for"))
      (unsupported-input () t))
    (check-equal '(:checkpoint "cp-1" 800) (checkpoint-line cp)
                 "a checkpoint prints as itself")))

(deftest "a-stop-reaches-distributed-work" "docs/SPEC-WORK.md:6374"
    "expected=stop-reaches-all-distributed;durable-request-identity;delivery-ack-reconciled;night-fallback-persisted"
  (let* ((req (make-control-request :kind :stop :request "req-stop-1"
                                    :targets '("t1" "t2" "t3")))
         (dist (distribute-control req (control-request-targets req))))
    (ok (control-kind-p (control-request-kind req))
        "the control kind is one of pause, stop, correct or prioritise")
    ;; the stop reaches every already-distributed task, with delivery,
    ;; acknowledgement and reconciled execution handles.
    (check-equal t (stop-reaches-p dist)
                 "the stop reaches every already-distributed task")
    (check-equal 3 (length (getf dist :handles)) "one handle per distributed task")
    (check-equal "req-stop-1" (control-retry-identity dist)
                 "the request identity is durable")
    (check-equal "req-stop-1" (control-handle-request (first (getf dist :handles)))
                 "each handle carries the durable request id")
    (let ((later (distribute-control req '("t1" "t2" "t3"))))
      (check-equal (control-retry-identity dist) (control-retry-identity later)
                   "a retry keeps the one durable request id"))
    ;; a correction across already-distributed work bumps the generation of its
    ;; handle and never rewrites the original.
    (let* ((h (first (getf dist :handles)))
           (c (apply-correction h)))
      (check-equal 2 (control-handle-generation c)
                   "a correction bumps the generation")
      (check-equal 1 (control-handle-generation h)
                   "the original handle is unchanged"))
    ;; a blocked question and its bounded fallback plan are persisted, so a
    ;; missing answer at night does not stall every independent task.
    (let ((q (make-blocked-question :node "t2" :question "which bench?"
                                    :fallback '(:plan "use bench-b")
                                    :persisted t)))
      (check-equal '(:plan "use bench-b") (fallback-plan q)
                   "the bounded fallback plan is persisted")
      (check-equal nil (independent-tasks-stall-p (list q))
                   "a missing answer at night stalls nothing"))))

(deftest "add-field-order-is-complete" "docs/SPEC-WORK.md:5819"
    "expected=order=twelve;absent=:absent;empty=();clear=:absent;two-serializers-one-value;four-distinct;pre-fold-refused"
  (let* ((all (list (cons :verb :node-add) (cons :node-type :task)
                    (cons :title "t1") (cons :under '(:node "root/f"))
                    (cons :category "c") (cons :required :true)
                    (cons :acceptance '("a1" "a2")) (cons :repo "o/r")
                    (cons :links '("https://x/1")) (cons :private :false)
                    (cons :version 1) (cons :reason "seed")))
         (absent '())
         (empty-links (acons :links '() all))
         (clear-links (acons :links '(:absent) all)))
    ;; every field of the twelve is written, in the spec's order.
    (check-equal *node-add-structure-fields* (node-add-structure-keys all)
                 "the twelve fields are written in order")
    ;; the two serializers agree on one value for each request.
    (dolist (req (list all absent empty-links clear-links))
      (check-string= (node-add-digest req) (node-add-digest-second req)
                     "two serializers digest one value"))
    ;; the four requests digest to four distinct values.
    (check-equal 4
                 (length (remove-duplicates
                          (mapcar #'node-add-digest (list all absent empty-links clear-links))
                          :test #'string=))
                 "the four requests digest to four distinct values")
    (check-equal +absent+ (getf (node-add-structure absent) :links)
                 "an absent field is (:absent)")
    (check-equal nil (getf (node-add-structure empty-links) :links)
                 "links-empty writes ()")
    (check-equal '(:absent) (getf (node-add-structure clear-links) :links)
                 "clear-links writes (:absent)")
    ;; a fixture in the canonical order loads; the pre-fold order is refused
    ;; schema revision unsupported, never read as the new shape.
    (check-equal (node-add-structure all)
                 (load-node-add-fixture (node-add-structure all))
                 "the canonical fixture loads")
    (handler-case
        (progn (load-node-add-fixture (node-add-pre-fold-structure all))
               (fail "a pre-fold fixture was read as the new shape"))
      (unsupported-input (c)
        (ok (search "schema revision unsupported" (format nil "~A" c))
            "the pre-fold fixture is refused schema revision unsupported")))))

(deftest "archive-completeness" "docs/SPEC-WORK.md:6232"
    "expected=six-gaps-explicit;absorbable=no;mixed-external-unknown-retain-source"
  (let* ((gaps (mapcar (lambda (k)
                         (make-archive-gap :kind k
                                           :detail (format nil "gap ~(~A~)" k)
                                           :source-issue "acme/widget#7"))
                       *archive-gap-kinds*))
         (capture (make-archive-capture :source-issue "acme/widget#7"
                                        :author :mixed :gaps gaps)))
    ;; a missing attachment, an unavailable comment, unsupported fields, size
    ;; truncation, a rate limit and a mid-page failure each remain explicit gaps.
    (check-equal 6 (length gaps) "six explicit gap kinds")
    (check-equal *archive-gap-kinds* (mapcar #'archive-gap-kind gaps)
                 "each gap keeps its kind")
    (check-equal t (archive-gaps-explicit-p capture) "every gap is explicit")
    ;; an incomplete capture prohibits absorption.
    (check-equal nil (archive-absorbable-p capture)
                 "an incomplete capture prohibits absorption")
    ;; mixed, external and unknown authors retain their source issues.
    (dolist (author '(:mixed :external :unknown))
      (let ((c (make-archive-capture :source-issue "acme/widget#7"
                                     :author author :gaps gaps)))
        (check-equal t (author-retains-source-p c)
                     "the author retains its source issue")))
    (check-equal t (archive-absorbable-p
                    (make-archive-capture :source-issue "acme/widget#7"
                                          :author :known :gaps '()))
                 "a complete capture is absorbable")))

;;; ------------------------------------------------------------------
;;; TestE09F04LeaveDeletionPendingOnMissing      docs/SPEC-WORK.md:7598
;;;   acceptance criterion E09-F04-03 (docs/roadmaps/nova-work.sexp):
;;;   "Leave deletion pending on missing content, source change or
;;;   uncertain network result".
;;; ------------------------------------------------------------------
;;; Three prongs, each driven through archive-deletion-gate, the absorb
;;; deletion gate beside archive-absorbable-p:
;;;   - missing content (:7598): "Unavailable or unpreserved content is
;;;     reported and leaves deletion pending, not silently skipped." Each
;;;     of the six gap kinds leaves deletion :pending :missing-content.
;;;   - source change (:7602-7608): capture at a named remote revision,
;;;     recheck for source changes before deleting; "If the source changed,
;;;     reconcile and checkpoint the added content first." A source revision
;;;     differing from the capture revision (or an unnamed one) leaves
;;;     deletion pending.
;;;   - uncertain network result (:7610-7611): "A network failure or
;;;     uncertain delete result preserves the archive and a pending
;;;     reconciliation state." A :failed or :uncertain delete result (or any
;;;     result the gate does not know) is :pending :reconcile with the
;;;     archive preserved, never :deleted.
;;; Split from nova-tools#2098 (which pinned the missing-content prong only).

(deftest "TestE09F04LeaveDeletionPendingOnMissing" "docs/SPEC-WORK.md:7598"
    "expected=missing-content-source-change-uncertain-delete-leave-deletion-pending;clean-capture-deletes"
  (let ((clean (make-archive-capture :source-issue "acme/widget#7"
                                     :author :known :gaps '())))
    ;; Missing content: each of the six gap kinds is reported as an explicit
    ;; gap and leaves deletion pending, even at an unchanged source revision
    ;; with a confirmed delete result offered.
    (dolist (kind *archive-gap-kinds*)
      (let ((capture (make-archive-capture
                      :source-issue "acme/widget#7"
                      :author :known
                      :gaps (list (make-archive-gap
                                   :kind kind
                                   :detail (format nil "missing ~(~A~)" kind)
                                   :source-issue "acme/widget#7")))))
        (check-equal t (archive-gaps-explicit-p capture)
                     "missing content is reported as an explicit gap, not skipped")
        (check-equal nil (archive-absorbable-p capture)
                     "missing content leaves the capture unabsorbable")
        (multiple-value-bind (state reason archive)
            (archive-deletion-gate capture :capture-revision "r1"
                                           :source-revision "r1")
          (check-equal :pending state
                       (format nil "missing ~(~A~) leaves deletion pending" kind))
          (check-equal :missing-content reason "the pending reason names the missing content")
          (check-equal t (eq capture archive) "the archive is preserved"))
        (check-equal :pending
                     (archive-deletion-gate capture :capture-revision "r1"
                                                    :source-revision "r1"
                                                    :delete-result :deleted)
                     "a delete result never overrides missing content")))
    ;; Source change: the recheck finds the source at a revision other than
    ;; the captured one, so deletion stays pending until the added content is
    ;; reconciled and checkpointed; an unnamed revision is no recheck at all.
    (multiple-value-bind (state reason archive)
        (archive-deletion-gate clean :capture-revision "r1" :source-revision "r2")
      (check-equal :pending state "a changed source leaves deletion pending")
      (check-equal :source-changed reason "the pending reason names the source change")
      (check-equal t (eq clean archive) "the archive is preserved on a source change"))
    (dolist (revs '((nil "r1") ("r1" nil) (nil nil)))
      (check-equal :pending
                   (archive-deletion-gate clean :capture-revision (first revs)
                                                :source-revision (second revs))
                   (format nil "an unnamed revision ~S leaves deletion pending" revs)))
    ;; A blank revision names nothing: equal empty or whitespace-only
    ;; revisions are no recheck at all, so deletion stays pending on a source
    ;; change with the archive preserved, even with a confirmed delete offered.
    (let ((tab (string #\Tab)) (newline (string #\Newline)))
      (dolist (revs (list '("" "") '("  " "  ") (list tab tab)
                          (list newline newline) '("" "r1") '("r1" "")
                          '(" " "r1") '("r1" " ")))
        (dolist (result '(nil :deleted))
          (multiple-value-bind (state reason archive)
              (archive-deletion-gate clean :capture-revision (first revs)
                                           :source-revision (second revs)
                                           :delete-result result)
            (check-equal :pending state
                         (format nil "a blank revision ~S (delete result ~S) leaves deletion pending"
                                 revs result))
            (check-equal :source-changed reason
                         (format nil "a blank revision ~S is an unnamed revision" revs))
            (check-equal t (eq clean archive)
                         (format nil "a blank revision ~S preserves the archive" revs))))))
    (check-equal :pending
                 (archive-deletion-gate clean :capture-revision "r1"
                                              :source-revision "r2"
                                              :delete-result :deleted)
                 "a delete result never overrides a source change")
    ;; Uncertain network result: a failed or uncertain delete (or an unknown
    ;; result) preserves the archive and leaves a pending reconciliation.
    (dolist (result '(:failed :uncertain :timeout))
      (multiple-value-bind (state reason archive)
          (archive-deletion-gate clean :capture-revision "r1" :source-revision "r1"
                                       :delete-result result)
        (check-equal :pending state
                     (format nil "a ~(~A~) delete result leaves deletion pending" result))
        (check-equal :reconcile reason
                     (format nil "a ~(~A~) delete result leaves a pending reconciliation" result))
        (check-equal t (eq clean archive)
                     (format nil "a ~(~A~) delete result preserves the archive" result))))
    ;; Only a gap-free capture rechecked at its captured revision may be
    ;; deleted, and only a confirmed result settles it as deleted.
    (check-equal t (archive-absorbable-p clean) "a gap-free capture is absorbable")
    (check-equal :allowed
                 (archive-deletion-gate clean :capture-revision "r1" :source-revision "r1")
                 "a clean capture at an unchanged source may be deleted")
    (multiple-value-bind (state reason archive)
        (archive-deletion-gate clean :capture-revision "r1" :source-revision "r1"
                                     :delete-result :deleted)
      (check-equal :deleted state "a confirmed delete settles the deletion")
      (check-equal nil reason "a confirmed delete has no pending reason")
      (check-equal t (eq clean archive) "the archive outlives the deletion"))))

;;; ------------------------------------------------------------------
;;; E01-F04-02 (docs/SPEC-WORK.md:888, :945-947) --- represent leaf
;;; tasks separately from parent tasks and attempts.
;;; ------------------------------------------------------------------
;;; :888 makes features, tasks, attempts and leaf subtasks distinct units,
;;; and :945-947 states the rule: "A task with no :children is a leaf
;;; subtask ... a task with children is counted by its leaves, never
;;; itself; so the four units ... are :feature, :task, the leaf :task,
;;; and the :attempt event". A leaf subtask is therefore a kind of its
;;; own, told apart from the parent :task and never confused with an
;;; attempt, which is an event's field and not a node kind.

(deftest "TestE01F04RepresentLeafTasksSeparatelyFrom" "docs/SPEC-WORK.md:888"
    "expected=leaf-task-distinct-kind-from-parent-task;attempt-is-not-a-node-kind"
  (let ((state (make-seed-state
                '((:id "root"       :type :work-set :parent nil        :state :unknown)
                  (:id "root/f"     :type :feature  :parent "root"     :state :unknown)
                  (:id "root/f/p"   :type :task     :parent "root/f"   :state :doing)
                  (:id "root/f/p/l" :type :task     :parent "root/f/p" :state :doing)
                  (:id "root/f/l2"  :type :task     :parent "root/f"   :state :doing)))))
    ;; A task with children is the parent :task; a task with none is a leaf
    ;; subtask, a distinct kind read back from the model, never the same unit.
    (check-equal :task (node-kind state "root/f/p")
                 "a task with children is represented as the parent :task")
    (check-equal :leaf-task (node-kind state "root/f/p/l")
                 "a task with no children is represented as a leaf subtask")
    (check-equal :leaf-task (node-kind state "root/f/l2")
                 "every leaf subtask answers the leaf kind, never :task")
    (check-equal :feature (node-kind state "root/f")
                 "a container keeps its own kind, never inferred from a title")
    (check-equal nil (equal (node-kind state "root/f/p")
                            (node-kind state "root/f/p/l"))
                 "the leaf kind differs from its parent task's kind")
    ;; An attempt is a unit of its own and never a node kind: it rides an
    ;; :evidence event's :attempt field, separate from the leaf task it
    ;; addresses.
    (let ((ev (make-work-event
               :kind :evidence :node "root/f/p/l" :by "rowan"
               :fields (list :pointer "p1" :criterion "c1" :against "a1"
                             :generation "g4" :attempt "att-1")
               :stamp "2026-09-20T00:00:00Z" :clock :tool :request "r1"
               :generation-owner "g4" :rev 1)))
      (check-string= "att-1" (getf (work-event-fields ev) :attempt)
                     "the attempt is a field on the event, never a node kind")
      (check-string= "root/f/p/l" (work-event-node ev)
                     "the attempt's event addresses the leaf task, never replaces it"))))

;;; E10-F03-03 (nova-work.sexp): map each suite to an owner, command and CI
;;; lane without duplicating its acceptance evidence (SPEC-WORK.md:7045,
;;; :7064-7107, :7235-7240). Recut of PR #2269: the command is a real
;;; per-suite selector (`run-tests.sh --suite NAME`), each owner is the one the
;;; spec states for that row, and the lane is runnable (`run-tests.sh --lane`).
(deftest "TestE10F03MapEachSuiteToAn" "docs/SPEC-WORK.md:7239"
    "expected=21-suites;per-change=6;nightly=15;owner=spec-stated;command=per-suite-selector;cases=registered-and-real;evidence=not-duplicated"
  (let ((table '("source-inventory" "read-only-intake" "import-replay"
                 "moving-source" "archive-completeness" "full-round-trip"
                 "format-determinism" "old-history" "referential-integrity"
                 "atomic-mutation" "retry-protocol" "async-operations"
                 "batches-and-pipelines" "single-writer" "indexes-and-counters"
                 "materialized-working-set" "roadmap-proof" "undo-redo"
                 "recovery" "schema-evolution" "hostile-data"))
        (per-change '("format-determinism" "referential-integrity"
                      "retry-protocol" "read-only-intake" "undo-redo"
                      "roadmap-proof"))
        (adapter '("source-inventory" "read-only-intake" "import-replay"
                   "moving-source" "archive-completeness" "schema-evolution")))
    ;; every row of the spec's table is mapped, once, and nothing else is
    (check-equal (sort (copy-list table) #'string<)
                 (sort (mapcar #'acceptance-suite-name *suite-registry*) #'string<)
                 "the registry maps exactly the spec table's 21 suites")
    ;; lanes as the spec names them (SPEC-WORK.md:7091-7098)
    (check-equal (sort (copy-list per-change) #'string<)
                 (sort (copy-list (lane-suites :per-change)) #'string<)
                 "the six per-change suites")
    (check-equal 15 (length (lane-suites :nightly-pre-release)) "fifteen nightly suites")
    (dolist (name table)
      (let ((s (find-acceptance-suite name)))
        (ok s "~A is registered" name)
        ;; the owner is the one the spec states for the row: the six intake
        ;; rows are the adapter's gate (:7102-7107), the rest are the section
        ;; author's (:7045) -- never one owner for every row
        (check-string= (if (member name adapter :test #'string=)
                           "intake-adapter" "stella")
                       (acceptance-suite-owner s)
                       (format nil "~A owner" name))
        (ok (search "docs/SPEC-WORK.md:" (acceptance-suite-owner-source s))
            "~A cites where the spec names its owner" name)
        ;; the command selects this suite and nothing else
        (check-string= (format nil "lisp/nova-work/run-tests.sh --suite ~A" name)
                       (suite-command name)
                       (format nil "~A command" name))
        ;; a registered case is a real deftest; an empty suite is owed, never
        ;; green (SPEC-WORK.md:7098: each row still needs its fixture)
        (multiple-value-bind (status tests missing) (plan-suite name)
          (check-equal nil missing (format nil "~A registers only real cases" name))
          (if (acceptance-suite-cases s)
              (progn
                (check-equal :run status (format nil "~A runs" name))
                (ok (every (lambda (e) (member (first e) (acceptance-suite-cases s)
                                               :test #'string=))
                           tests)
                    "~A selects only its own cases" name)
                (ok (>= (length tests) (length (acceptance-suite-cases s)))
                    "~A selects every one of its cases" name))
              (check-equal :owed status (format nil "~A is owed" name))))
        ;; the mapping carries name, lane, owner and case names only; the
        ;; required cases and pass condition stay in the spec's table
        (ok (and (stringp (acceptance-suite-name s))
                 (member (acceptance-suite-lane name) '(:per-change :nightly-pre-release))
                 (every #'stringp (acceptance-suite-cases s)))
            "~A duplicates no acceptance evidence" name)))
    (check-string= "lisp/nova-work/run-tests.sh --lane per-change"
                   (lane-command :per-change) "the per-change lane command")
    (let ((*suite-registry* (list (find-acceptance-suite "retry-protocol")))
          (*per-change-suites* '("retry-protocol"))
          (*tests* '())
          (*pass* 0)
          (*fail* 0)
          (*problems* '()))
      (check-equal 3 (run-lane "per-change")
                   "an owed suite makes its lane nonzero"))
    ;; a real failure outranks an owed suite: a lane that owes a suite AND has
    ;; a failing case exits 1 (failed), never 3 (owed), so a caller that
    ;; tolerates owed never tolerates a failure hiding behind it
    (let ((*suite-registry*
            (list (find-acceptance-suite "retry-protocol")
                  (nova-work::%make-acceptance-suite
                   "failing-probe" "stella" "docs/SPEC-WORK.md:7045"
                   '("failing-probe-case"))))
          (*per-change-suites* '("retry-protocol" "failing-probe"))
          (*tests* (list (list "failing-probe-case" "E10-F03-03" "fails on purpose"
                               (lambda () (error "the probe case fails")))))
          (*pass* 0)
          (*fail* 0)
          (*problems* '())
          (*standard-output* (make-broadcast-stream)))
      (check-equal 1 (run-lane "per-change")
                   "a failure in a lane with an owed suite exits 1, not 3"))
    (check-equal :unknown (plan-suite "no-such-suite") "an unknown suite is refused")
    (check-equal t (suite-owed-p "retry-protocol")
                 "a suite with no case yet is owed, not passed")))
