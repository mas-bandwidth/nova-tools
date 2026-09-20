;;;; replays-8660.lisp --- the five duty-tier replays of docs/SPEC-WORK.md:5547
;;;; and the amendment #500 replay list at docs/SPEC-WORK.md:6067-6079.
;;;;
;;;; Each deftest is named exactly as the spec names it in backticks:
;;;;
;;;;   revive-appends-and-counts-latest      SPEC-WORK.md:5547
;;;;   duty-tier-executes-the-policy         SPEC-WORK.md:6067
;;;;   escalation-carries-rule-default-age   SPEC-WORK.md:6071
;;;;   wait-table-four-presence-columns      SPEC-WORK.md:6074
;;;;   quiet-time-calls-nothing              SPEC-WORK.md:6077
;;;;
;;;; The revive replay drives the slice-1 kernel through submit and apply-event.
;;;; The duty-tier, escalation, wait-table and quiet-time replays read the pure
;;;; duty-tier model in src/replays-8660.lisp, because the amendment says of
;;;; itself "nothing here is built" and the session/CLI surfaces it names are
;;;; outside this slice.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; revive-appends-and-counts-latest           SPEC-WORK.md:5547
;;; ------------------------------------------------------------------

(deftest "revive-appends-and-counts-latest" "docs/SPEC-WORK.md:5547"
    "expected=closed-record-kept;revive-row-revived=<rev>-settles=1;third-row-settles=2;earlier-rows-unchanged;window-counts-latest-once"
  (let* ((k (fresh))
         (id "acme/work/f1/t1")
         (total (+ (state-open-count (kernel-state k))
                   (state-closed-count (kernel-state k)))))
    (check-equal 5 total "the seed is five open and no rule 18 finding")
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let* ((rows (state-closed-rows (kernel-state k)))
           (settle-row (first (last rows))))
      ;; The window that ends between the settle and the revive counts the id
      ;; once, in closed=.
      (check-equal 4 (state-open-count (kernel-state k)) "settle leaves |O|=4")
      (check-equal 1 (state-closed-count (kernel-state k))
                   "the window ending here has the id in closed=")
      (check-equal :c (node-branch (kernel-state k) id) "the id is in C")
      (check-equal 1 (length rows) "one closed record after the settle")
      (check-equal :settle (getf settle-row :kind) "the closed record is the settle")
      (check-equal id (getf settle-row :node) "the record names the id")
      (check-equal 1 (getf settle-row :settles) "its settles=1")
      (check-string= "-" (getf settle-row :revived) "not yet revived")
      (ok (submit k (reopen-request :request "req-2")) "reopen refused")
      (let* ((rows2 (state-closed-rows (kernel-state k)))
             (revive-row (first (last rows2))))
        ;; The closed record, and every event under it, is still in C after the
        ;; :revive -- C is append-only.
        (ok (member settle-row rows2 :test #'equal)
            "the closed record survives the revive")
        (ok (find (getf settle-row :key)
                  (mapcar (lambda (r) (getf r :key)) rows2) :test #'equal)
            "the closed-index key is still in C")
        (check-equal 2 (length rows2) "the revive appends its own row")
        (check-equal :revive (getf revive-row :kind) "the newest row is the revive")
        (check-equal (getf revive-row :rev) (getf revive-row :revived)
                     "the revive row carries revived=<rev>")
        (check-equal 1 (getf revive-row :settles) "settles=1 on the revive row")
        ;; The window that ends after the revive counts the same id once, in
        ;; open=.
        (check-equal 5 (state-open-count (kernel-state k)) "revive restores |O|=5")
        (check-equal 0 (state-closed-count (kernel-state k))
                     "the window ending later has the same id in open=")
        (check-equal :o (node-branch (kernel-state k) id) "the id is open again")
        (check-equal total (+ (state-open-count (kernel-state k))
                              (state-closed-count (kernel-state k)))
                     "no rule 18 finding: counted once either way"))))
  ;; settles=2 -- the third row -- needs one id settled twice, and :reopen lands
  ;; at :todo, which has no edge back to :done. As the slice-1 suite does, this
  ;; second settle is driven on apply-event, the primitive the live and replay
  ;; paths share.
  (let* ((state (make-seed-state *seed*))
         (id "acme/work/f1/t1")
         (event (lambda (kind rev fields)
                  (make-work-event :kind kind :node id :by "rowan" :fields fields
                                   :stamp "2026-09-14T12:00:00Z" :clock :tool
                                   :request "req-p" :generation-owner "gen-4"
                                   :rev rev :session-written-p t))))
    (setf state (apply-event state (funcall event :settle 1 '(:disposition :done :reason "a"))))
    (setf state (apply-event state (funcall event :revive 2 '(:reason "b"))))
    (let* ((first-two (copy-seq (state-closed-rows state)))
           (cursor-rev 3))
      (setf state (apply-event state (funcall event :settle cursor-rev '(:disposition :done :reason "c"))))
      (let* ((rows (state-closed-rows state))
             (newest (first (last rows))))
        (check-equal 3 (length rows) "three rows after the second settle")
        (check-equal (first first-two) (first rows) "the first row is unchanged")
        (check-equal (second first-two) (second rows) "the second row is unchanged")
        (check-equal 2 (getf newest :settles) "settles=2 on the newest row")
        (check-string= "-" (getf newest :revived) "the new settle row is not revived")
        (check-equal cursor-rev (getf newest :rev) "the cursor names the newest event revision")))))

;;; ------------------------------------------------------------------
;;; duty-tier-executes-the-policy              SPEC-WORK.md:6067
;;; ------------------------------------------------------------------

(deftest "duty-tier-executes-the-policy" "docs/SPEC-WORK.md:6067"
    "expected=rule-without-by-refused-at-load;cheapest-qualified-or-none;escalates-what-it-cannot-decide;never-authors"
  (let* ((rules (list (make-policy-rule :id "r1" :by "glenn" :task-class :coding
                                        :models '("beta" "astra"))
                      (make-policy-rule :id "r2" :by "rowan" :task-class :review
                                        :models '("gamma"))))
         (policy (load-policy :version "v1" :rules rules))
         (price '(("astra" . 50) ("beta" . 10) ("gamma" . 5))))
    (check-equal "v1" (duty-policy-version policy) "the approved record version")
    ;; Executes with the cheapest qualified model.
    (multiple-value-bind (disposition model) (execute-policy policy :coding price)
      (check-equal :execute disposition "a rule decides")
      (check-equal "beta" model "the cheapest qualified model"))
    ;; Or none when no named model is qualified.
    (multiple-value-bind (disposition rule) (execute-policy policy :coding '(("zeta" . 1)))
      (check-equal :none disposition "no qualified model means none")
      (check-equal "r1" (policy-rule-id rule) "the rule that could not decide"))
    ;; Escalates a judgment no rule can make.
    (multiple-value-bind (disposition rule) (execute-policy policy :deploy price)
      (check-equal :escalate disposition "no rule becomes an escalation")
      (check-equal nil rule "an escalation names no deciding rule"))
    ;; A rule with no :by is refused at load.
    (handler-case
        (progn
          (load-policy :version "v1"
                       :rules (list (make-policy-rule :id "r9" :by nil :task-class :coding)))
          (fail "a policy rule without :by was loaded"))
      (unsupported-input (c)
        (ok (search "r9" (princ-to-string c)) "the refusal names the rule: ~A" c)))
    ;; The duty tier authors no policy of its own.
    (handler-case
        (progn
          (author-policy :version "v2" :rules rules)
          (fail "a duty session authored a policy"))
      (unsupported-input (c)
        (ok (search "never authors" (princ-to-string c))
            "authoring is refused: ~A" c)))))

;;; ------------------------------------------------------------------
;;; escalation-carries-rule-default-age        SPEC-WORK.md:6071
;;; ------------------------------------------------------------------

(deftest "escalation-carries-rule-default-age" "docs/SPEC-WORK.md:6071"
    "expected=row-carries-rule-default-age;stale-reads-the-three-and-reassigns-nothing"
  (let* ((row (make-escalation-row :rule "r7" :default :close-on-silence :age 3600))
         (rows (list row)))
    (check-equal "r7" (escalation-row-rule row) "the policy rule that could not decide")
    (check-equal :close-on-silence (escalation-row-default row)
                 "the default that fires on silence")
    (check-equal 3600 (escalation-row-age row) "how long the escalation has stood")
    (multiple-value-bind (read line) (stale-pass rows)
      (ok (eq read rows) "the stale pass reads the same rows")
      (ok (search "reassigned=0" line) "it reassigns nothing: ~A" line)
      (check-equal "r7" (escalation-row-rule (first read)) "the rule is unchanged")
      (check-equal :close-on-silence (escalation-row-default (first read))
                   "the default is unchanged")
      (check-equal 3600 (escalation-row-age (first read)) "the age is unchanged"))))

;;; ------------------------------------------------------------------
;;; wait-table-four-presence-columns           SPEC-WORK.md:6074
;;; ------------------------------------------------------------------

(deftest "wait-table-four-presence-columns" "docs/SPEC-WORK.md:6074"
    "expected=four-presence-columns;fourth-unproven-holds-no-resident;only-a-notes-driven-duty-session"
  (check-equal '(:process-alive :beat-written :delivery-handled :parent-woke)
               *wait-presence-columns* "the four presence facts are the columns")
  (let ((full (make-wait-row :harness "h1" :process-alive t :beat-written t
                             :delivery-handled t :parent-woke t))
        (partial (make-wait-row :harness "h2" :process-alive t :beat-written t
                                :delivery-handled t :parent-woke nil)))
    (check-equal 4 (length *wait-presence-columns*) "four presence columns")
    (check-equal t (wait-holds-resident-p full)
                 "a harness that proved the whole drill holds a resident session")
    (check-equal nil (wait-holds-resident-p partial)
                 "a harness with the fourth unproven holds no resident session")
    (check-equal t (wait-holds-duty-p partial)
                 "it may hold only a duty session driven by notes")
    (check-equal '(:process-alive t :beat-written t :delivery-handled t :parent-woke nil)
                 (wait-presence partial) "the row carries the four facts")))

;;; ------------------------------------------------------------------
;;; quiet-time-calls-nothing                   SPEC-WORK.md:6077
;;; ------------------------------------------------------------------

(deftest "quiet-time-calls-nothing" "docs/SPEC-WORK.md:6077"
    "expected=no-change-zero-calls-zero-notes;publication-mechanical;one-event-one-measured-call"
  (let ((quiet (quiet-pulse :changed nil)))
    (check-equal 0 (pulse-result-model-calls quiet)
                 "nothing changed makes no model call")
    (check-equal 0 (pulse-result-notes quiet) "nothing changed sends no note")
    (check-equal '(:clip :beat :projection) (pulse-result-published quiet)
                 "state is published mechanically")
    (check-equal 0 (pulse-result-cost quiet) "quiet time costs nothing"))
  (let ((busy (quiet-pulse :changed t :decision '(:model "beta")
                           :price-table '(("beta" . 10)))))
    (check-equal 1 (pulse-result-model-calls busy) "one event causes one call")
    (check-equal 10 (pulse-result-cost busy)
                 "the cost is the measured spend of the one call that event caused")
    (check-equal '(:clip :beat :projection) (pulse-result-published busy)
                 "publication is still mechanical")))

;;; ------------------------------------------------------------------
;;; rule 18 reads the LATEST closed-index row     SPEC-WORK.md:1630
;;; ------------------------------------------------------------------
;;;
;;; The replay above proves the COUNTS after a revive. It never asks the rule
;;; 18 predicate itself, which is what let `cow-load-findings` read *any* row
;;; naming an id instead of that id's latest one. :1630 is the sentence:
;;; "the finding is an id whose *latest* state puts it in both branches, never
;;; the history of an id that has honestly moved and kept its record (replay
;;; `revive-appends-and-counts-latest`)".

(deftest "rule-18-finds-the-latest-row-not-the-history" "docs/SPEC-WORK.md:1630"
    "expected=seed-no-finding;settled-id-no-finding;settled-then-revived-id-no-finding;LOAD-OK;a-row-over-a-still-open-node-is-found-and-refused"
  (let* ((k (fresh))
         (id "acme/work/f1/t1"))
    (ok (null (cow-load-findings (kernel-state k))) "the seed has no finding")
    (ok (cow-partition-holds-p (kernel-state k)) "the seed partitions O and C")
    (ok (submit k (close-request :request "req-1")) "close refused")
    (check-equal :c (node-branch (kernel-state k) id) "the id is in C")
    (ok (null (cow-load-findings (kernel-state k)))
        "an id in C with its settle row is no finding")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (check-equal :o (node-branch (kernel-state k) id) "the id is open again")
    ;; C is append-only: the settle row is still there beside the revive.
    (check-equal 2 (length (state-closed-rows (kernel-state k)))
                 "the settle row survives the revive")
    (check-equal :revive (getf (first (wstate-rows (kernel-state k))) :kind)
                 "and the id's latest row is the revive")
    (ok (null (cow-load-findings (kernel-state k)))
        "an id that settled and was honestly revived is no rule 18 finding")
    (ok (cow-partition-holds-p (kernel-state k))
        "the partition still holds after the revive")
    (multiple-value-bind (admitted line code) (cow-candidate-gate (kernel-state k))
      (ok admitted "the candidate gate admits a revived id")
      (check-string= "LOAD OK" line "and says so")
      (check-equal 0 code "at exit 0")))
  ;; The finding rule 18 is actually for: a closed-index row that settles an id
  ;; whose node still reads :o, with nothing over it (SPEC-WORK.md:5496).
  (let* ((k (fresh))
         (id "acme/work/f1/t2")
         (state (hand-write-closed-row (kernel-state k) id 7)))
    (check-equal :o (node-branch state id) "the node still reads :o")
    (check-equal (list id) (cow-load-findings state) "the double membership is found")
    (ok (not (cow-partition-holds-p state)) "the partition does not hold")
    (multiple-value-bind (admitted line code) (cow-candidate-gate state)
      (ok (not admitted) "the candidate gate refuses it")
      (check-equal 1 code "at exit 1")
      (ok (search "rule 18" line) "and the line names rule 18")
      (ok (search id line) "and the id in both branches"))))

;;; ------------------------------------------------------------------
;;; E05-F01-02 Require matching criterion kind, exact subject and
;;; predicate                               SPEC-WORK.md:936-942, :1281-1288
;;; ------------------------------------------------------------------
;;;
;;; docs/SPEC-WORK.md:936-937: "an evidence pointer qualifies a criterion
;;; only when its kind matches, its subject is the criterion's subject (a
;;; passing test of another name qualifies nothing)". The qualification gate
;;; is verify-qualifies-p (docs/SPEC-WORK.md:1281-1288), and its three
;;; dimensions are the criterion's kind, subject and predicate. The kind half
;;; is enforced (a test: pointer qualifies a :test criterion, a run: pointer
;;; does not); the exact-subject half is not: the gate never compares the
;;; evidence subject to the criterion's subject, so a passing test of another
;;; name still qualifies.

(deftest "TestE05F01RequireMatchingCriterionKindExact" "docs/SPEC-WORK.md:936"
    "expected=matching-kind-qualifies;another-name-subject-qualifies-nothing"
  ;; matching criterion kind: a :test criterion is qualified by a test: pointer
  ;; naming its own subject.
  (let ((named (make-verify-evidence "ev-1"
                                     :pointer "test:pkg/TargetTest@sha1"
                                     :criterion :test
                                     :subject "test:pkg/TargetTest@sha1")))
    (check-equal t (verify-qualifies-p named)
                 "a matching test: pointer qualifies a :test criterion"))
  ;; a kind that does not match qualifies nothing.
  (let ((wrong-kind (make-verify-evidence "ev-2"
                                          :pointer "run:ci/1@sha1"
                                          :criterion :test
                                          :subject "test:pkg/TargetTest@sha1")))
    (check-equal nil (verify-qualifies-p wrong-kind)
                 "a run: pointer qualifies no :test criterion"))
  ;; exact subject: a passing test of another name qualifies nothing
  ;; (docs/SPEC-WORK.md:937). The evidence names TargetTest as its subject but
  ;; its pointer is a different test; the gate must refuse it.
  (let ((another-name (make-verify-evidence "ev-3"
                                            :pointer "test:pkg/OtherTest@sha2"
                                            :criterion :test
                                            :subject "test:pkg/TargetTest@sha1")))
    (check-equal nil (verify-qualifies-p another-name)
                 "a passing test of another name qualifies no :test criterion")))
