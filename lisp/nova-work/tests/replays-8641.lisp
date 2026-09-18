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
