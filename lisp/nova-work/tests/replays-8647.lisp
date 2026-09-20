;;;; replays-8647.lisp --- the five acceptance replays of docs/SPEC-WORK.md's
;;;; required-enforcement and preservation tables that the slice-1 kernel now
;;;; supports directly: policy-round-trip-and-replay (:4847), 
;;;; pricing-is-pinned-by-revision (:4650), quiet-until-actionable (:4850),
;;;; read-only-intake (:6229) and regression-and-recovery (:4856).
;;;;
;;;; Each drives the pure functions in src/replays-8647.lisp; no session, no
;;;; model and no network adapter is started here.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; policy-round-trip-and-replay                 SPEC-WORK.md:4847
;;; ------------------------------------------------------------------

(deftest "policy-round-trip-and-replay" "docs/SPEC-WORK.md:4847"
    "expected=survives-round-trip;malformed-intake-no-partial-effect"
  (let* ((p1 (make-efficiency-policy
              :id "pol-1" :schema-version 1 :scope "friend/rowan" :author "rowan"
              :quality '("task-class-acceptance" "required-review")
              :routing (list :capabilities '("code" "review")
                             :preference '("beta" "astra"))
              :bounds '(:input-packet-bytes 65536 :report-bytes 8192 :attempt-count 3
                        :execution-limit-ms 600000 :full-history :false)
              :measurement '(:accepted-unit "merged-change" :rate-revision "rate-1")
              :trial '(:stage :trial :expiry "2026-10-01T00:00Z"
                       :tolerances (:quality 90 :tokens 1000 :cost 5 :wall 60))))
         (manifest (make-trial-manifest
                    :hypothesis "cache compact prefixes" :workload "coding"
                    :source-revision "src-1" :acceptance-revision "acc-1"
                    :baseline "baseline-1" :changed-variables '("context-mode")
                    :procedure "paired" :sample-stop "20-tasks"
                    :tolerances '(:quality 90 :tokens 1000)))
         (execution (make-execution-reference
                     :policy-revision (getf p1 :revision) :task-generation 4
                     :attempt 1 :parent-execution nil :packet-digest "pkt-1"
                     :packet-bytes 4096 :requested-model "beta" :observed-model "beta"
                     :harness "sbcl" :bench "local" :execution-limit-ms 600000
                     :started-at "2026-09-17T00:00Z" :expires-at "2026-09-17T00:10Z"
                     :stop-outcome :not-requested :stop-observed-at +absent+
                     :checkpoint "cp-1" :result "res-1" :usage '(:input 10 :output 5)))
         (store (make-policy-store :policy p1 :manifests (list manifest)
                                   :executions (list execution))))
    ;; Export, import and restart reproduce the same canonical bytes.
    (let* ((bytes (policy-store-export store))
           (loaded (policy-store-import bytes)))
      (check-string= bytes (policy-store-export loaded) "the policy store round trip")
      (check-equal (policy-store-policy store) (policy-store-policy loaded)
                   "the policy survived the round trip")
      (check-equal (policy-store-manifests store) (policy-store-manifests loaded)
                   "the trial manifest survived the round trip")
      (check-equal (policy-store-executions store) (policy-store-executions loaded)
                   "the execution reference survived the round trip")
      (check-equal (getf p1 :revision)
                   (execution-reference-policy-revision execution)
                   "the execution reference pins the policy revision")
      ;; Undo rules survive the round trip: the prior policy is restored.
      (let* ((p2 (make-efficiency-policy
                  :id "pol-2" :schema-version 1 :scope "friend/rowan" :author "rowan"
                  :quality '("task-class-acceptance")
                  :routing '(:capabilities ("code"))
                  :bounds '(:input-packet-bytes 32768 :report-bytes 4096 :attempt-count 2
                            :execution-limit-ms 300000 :full-history :true)
                  :measurement '(:accepted-unit "merged-change")
                  :trial '(:stage :adopt)))
             (adopted (policy-intake loaded p2)))
        (check-equal p2 (policy-store-policy adopted) "the second intake was accepted")
        (multiple-value-bind (undone ok) (policy-undo adopted)
          (ok ok "the intake is undoable")
          (check-equal p1 (policy-store-policy undone) "undo restored the prior policy")))
      ;; Malformed intake has no partial effect: revision, policy, manifests and
      ;; executions are all exactly what they were.
      (let ((malformed (loop for (k v) on p1 by #'cddr
                             unless (eq k :bounds) append (list k v))))
        (multiple-value-bind (after ok line code) (policy-intake loaded malformed)
          (check-equal nil ok "a malformed intake is refused")
          (check-equal 2 code "the malformed intake is exit 2")
          (ok (search "POLICY FAIL" line) "the refusal names POLICY FAIL: ~A" line)
          (check-string= bytes (policy-store-export after)
                         "a refused intake changed the stored revision")
          (check-equal (policy-store-manifests loaded)
                       (policy-store-manifests after)
                       "a refused intake changed the manifests")
          (check-equal (policy-store-executions loaded)
                       (policy-store-executions after)
                       "a refused intake changed the executions")))))
  ;; Revision replay folds the same intake sequence to the same policy.
  (let* ((p1 (make-efficiency-policy :id "pol-1" :schema-version 1 :scope "s"
                                     :author "rowan" :quality '("a")
                                     :routing '(:capabilities ("code"))
                                     :bounds '(:input-packet-bytes 1 :report-bytes 1
                                               :attempt-count 1 :execution-limit-ms 1
                                               :full-history :false)
                                     :measurement '(:accepted-unit "u")
                                     :trial '(:stage :observe)))
         (p2 (make-efficiency-policy :id "pol-2" :schema-version 1 :scope "s"
                                     :author "rowan" :quality '("a")
                                     :routing '(:capabilities ("code"))
                                     :bounds '(:input-packet-bytes 1 :report-bytes 1
                                               :attempt-count 1 :execution-limit-ms 1
                                               :full-history :true)
                                     :measurement '(:accepted-unit "u")
                                     :trial '(:stage :adopt)))
         (replayed (policy-replay (list p1 p2)))
         (direct (policy-intake (policy-intake (make-policy-store) p1) p2)))
    (check-equal (policy-store-policy direct) (policy-store-policy replayed)
                 "revision replay did not reproduce the live policy")
    (check-equal (policy-store-history direct) (policy-store-history replayed)
                 "revision replay did not reproduce the intake history")))

;;; ------------------------------------------------------------------
;;; pricing-is-pinned-by-revision                SPEC-WORK.md:4650
;;; ------------------------------------------------------------------

(deftest "pricing-is-pinned-by-revision" "docs/SPEC-WORK.md:4650"
    "expected=old-estimate-reproducible;missing-dimension-unknown;three-cost-values-separate"
  (let* ((registry (make-pricing-registry))
         (r1 (make-pricing-record :currency "usd" :unit-scale 1000000
                                  :input 3 :output 6 :cache-write 4 :cache-read 1
                                  :reasoning :included :source "vendor-2026-09-01"
                                  :effective-time "2026-09-01T00:00Z"))
         (usage '(:input 1000000 :output 1000000)))
    (register-pricing registry r1)
    (let* ((e1 (estimate-cost registry (pricing-record-id r1) usage :virtual-weight 2)))
      (check-equal 9 (getf e1 :total-cost) "input 3 + output 6 at unit scale")
      (check-equal 0 (getf e1 :cash) "the declared API cash charge is zero")
      (check-equal 9 (getf e1 :marginal) "the estimated marginal cash is separate")
      (check-equal 18 (getf e1 :virtual) "the virtual reference cost is separate")
      (check-equal (pricing-record-id r1) (getf e1 :pricing-revision)
                   "the estimate pins the pricing revision")
      ;; A refresh is a new revision with a source and effective time; the old
      ;; record is immutable and the old estimate stays reproducible.
      (let* ((r2 (pricing-refresh r1 :input 5 :source "vendor-2026-09-15"
                                  :effective-time "2026-09-15T00:00Z"))
             (_ (register-pricing registry r2))
             (e1-again (estimate-cost registry (pricing-record-id r1) usage))
             (e2 (estimate-cost registry (pricing-record-id r2) usage)))
        (check-equal 3 (getf r1 :input) "a refresh edited the historical record")
        (check-equal 5 (getf r2 :input) "the refresh carries the new rate")
        (check-equal "vendor-2026-09-15" (getf r2 :source) "the refresh carries its source")
        (check-equal "2026-09-15T00:00Z" (getf r2 :effective-time)
                     "the refresh carries its effective time")
        (check-equal 9 (getf e1-again :total-cost)
                     "the pinned estimate moved after a rate change")
        (check-equal 11 (getf e2 :total-cost) "the new revision prices the change")
        (check-equal nil (equal (getf e1 :total-cost) (getf e2 :total-cost))
                     "the two revisions are not the same price")))
    ;; A missing dimension is unknown, never zero.
    (let* ((r3 (make-pricing-record :currency "usd" :unit-scale 1000000
                                    :input 3 :output 6 :cache-write 4 :cache-read nil))
           (_ (register-pricing registry r3))
           (e3 (estimate-cost registry (pricing-record-id r3)
                              '(:input 1000000 :output 1000000 :cache-read 1000))))
      (check-equal :unknown (getf e3 :total-cost)
                   "a missing cache-read rate is unknown, not zero")
      (check-equal nil (eql 0 (getf e3 :total-cost)) "unknown is not zero"))))

;;; ------------------------------------------------------------------
;;; quiet-until-actionable                       SPEC-WORK.md:4850
;;; ------------------------------------------------------------------

(deftest "quiet-until-actionable" "docs/SPEC-WORK.md:4850"
    "expected=zero-model-dispatch-for-unchanged;batching-bounded;urgent-bypass"
  (let* ((pulse (make-dispatch-pulse :record-bound 2 :byte-bound 4096))
         (o1 '(:node "n1" :state :doing :rev 1 :bytes 10))
         (o2 '(:node "n2" :state :review :rev 2 :bytes 20))
         (o3 '(:node "n3" :state :done :rev 3 :bytes 30)))
    ;; Unchanged observations cause zero model dispatches, even twice over.
    (observe-pulse pulse o1)
    (observe-pulse pulse o1)
    (check-equal 0 (dispatch-pulse-dispatches pulse)
                 "an unchanged observation dispatched a model")
    (check-equal 1 (length (dispatch-pulse-pending pulse))
                 "the first actionable delta is queued, not dispatched")
    ;; The second distinct delta reaches the record bound and dispatches once.
    (observe-pulse pulse o2)
    (check-equal 1 (dispatch-pulse-dispatches pulse) "the record bound did not flush")
    (check-equal 0 (length (dispatch-pulse-pending pulse)) "the flush left the queue dirty")
    ;; One more delta waits below the bound; an explicit flush empties it.
    (observe-pulse pulse o3)
    (check-equal 1 (dispatch-pulse-dispatches pulse) "a sub-bound delta dispatched early")
    (dispatch-pulse-flush pulse)
    (check-equal 2 (dispatch-pulse-dispatches pulse) "the explicit flush dispatched nothing")
    ;; Urgent corrections bypass the delay, dispatch at once, and leave the
    ;; pending queue inside its bound.
    (observe-pulse pulse '(:node "n4" :state :doing :rev 4 :bytes 40))
    (observe-pulse pulse '(:kind :correction :node "n1" :rev 5))
    (check-equal 3 (dispatch-pulse-dispatches pulse)
                 "an urgent correction did not bypass the delay")
    (check-equal 1 (length (dispatch-pulse-pending pulse))
                 "the urgent bypass dropped or overfilled the pending queue")
    (ok (<= (length (dispatch-pulse-pending pulse))
            (dispatch-pulse-record-bound pulse))
        "the pending queue exceeded its record bound"))
  ;; Batching respects the record bound across a run.
  (let ((pulse (make-dispatch-pulse :record-bound 2 :byte-bound 4096)))
    (dotimes (i 5)
      (observe-pulse pulse (list :node (format nil "n~D" i) :rev i :bytes 1))
      (ok (<= (length (dispatch-pulse-pending pulse))
              (dispatch-pulse-record-bound pulse))
          "the queue exceeded its record bound at step ~D" i))
    (check-equal 2 (dispatch-pulse-dispatches pulse)
                  "five deltas at bound two did not make two dispatches")
    (check-equal 1 (length (dispatch-pulse-pending pulse))
                  "the trailing remainder was not left queued")))

;;; ------------------------------------------------------------------
;;; read-only-intake                             SPEC-WORK.md:6229
;;; ------------------------------------------------------------------

(deftest "read-only-intake" "docs/SPEC-WORK.md:6229"
    "expected=recording-adapter-fails-on-mutation-endpoint;remote-inventory-compared-before-after"
  (let* ((source (make-recording-adapter :inventory '(:issues 3 :comments 7)))
         (destination (list :applied 0))
         (before (adapter-inventory source)))
    ;; A dry-run capture and a normal initial import call no mutation endpoint,
    ;; and the remote inventory is unchanged.
    (dry-run-capture source)
    (initial-import source)
    (check-equal 0 (length (adapter-mutation-calls source))
                 "a read-only pass called a mutation endpoint")
    (check-equal before (adapter-inventory source)
                 "a read-only pass changed the remote inventory")
    (ok (plusp (length (adapter-read-calls source)))
        "the read-only pass made no reads at all")
    (let ((plan (dry-run-capture source)))
      ;; Applying the plan changes only the destination, after revalidation.
      (apply-plan source destination plan)
      (check-equal 0 (length (adapter-mutation-calls source))
                   "applying a plan called a source mutation endpoint")
      (check-equal before (adapter-inventory source)
                   "applying a plan changed the source inventory")
      (check-equal 3 (getf destination :applied)
                   "applying a plan did not change the destination"))
    ;; Revalidation refuses a plan whose source moved, writing nothing.
    (let* ((plan (dry-run-capture source))
           (dest2 (list :applied 0))
           (moved (make-recording-adapter :inventory '(:issues 4 :comments 7))))
      (multiple-value-bind (result line) (apply-plan moved dest2 plan)
        (check-equal nil result "a stale plan was applied")
        (ok (search "stale" line) "the refusal names staleness: ~A" line)
        (check-equal 0 (getf dest2 :applied) "a stale plan wrote the destination")))
    ;; The recording adapter itself fails on a mutation endpoint, so a stray
    ;; call could never pass silently.
    (ok (handler-case (progn (adapter-mutate source :post :issues) nil)
          (unsupported-input () t))
        "the recording adapter did not fail on POST")))

;;; ------------------------------------------------------------------
;;; regression-and-recovery                      SPEC-WORK.md:4856
;;; ------------------------------------------------------------------

(defun r8647-recovery-policy (&key stage tolerances fallback role-limits (id "pol"))
  (list :id id
        :trial (list :stage stage :tolerances tolerances :fallback fallback)
        :routing (list :role-limits role-limits)))

(deftest "regression-and-recovery" "docs/SPEC-WORK.md:4856"
    "expected=breached-trial-stops-automatic-assignment;fallback-preserves-limits-history-handles"
  (let* ((approved (r8647-recovery-policy :id "pol-approved" :stage :adopt
                                          :role-limits '(:coordinator 1 :worker 2)))
         (trial (r8647-recovery-policy :id "pol-trial" :stage :trial
                                       :tolerances '(:quality 90 :tokens 1000 :cost 5 :wall 60)
                                       :fallback approved
                                       :role-limits '(:coordinator 1 :worker 2)))
         (control (make-assignment-control
                   :policy trial
                   :attempts '((:attempt 1 :outcome :failed))
                   :handles '((:handle "h1" :outcome :unknown)))))
    ;; A tolerance breach is detected when quality drops or cost/tokens/wall rise.
    (check-equal :quality (trial-tolerance-breach trial '(:quality 80 :tokens 900))
                 "a quality regression was not detected")
    (check-equal :tokens (trial-tolerance-breach trial '(:quality 95 :tokens 1200))
                 "a token regression was not detected")
    (check-equal nil (trial-tolerance-breach trial '(:quality 95 :tokens 900 :cost 4 :wall 50))
                 "a passing trial was reported breached")
    ;; The breach suspends new automatic assignments and adopts the fallback
    ;; while preserving failed attempts and the uncertain live handle.
    (multiple-value-bind (after info) (regression-recover control '(:quality 80 :tokens 900))
      (check-equal :quality (getf info :breached) "the breach axis was not recorded")
      (check-equal t (assignment-control-suspended after) "the breach did not suspend the trial")
      (check-equal "pol-approved" (getf (assignment-control-policy after) :id)
                   "the eligible fallback was not adopted")
      (check-equal 1 (length (assignment-control-attempts after))
                   "the failed attempt was erased")
      (check-equal 1 (length (assignment-control-handles after))
                   "the uncertain live handle was erased")
      (check-equal :unknown (getf (first (assignment-control-handles after)) :outcome)
                   "the uncertain live handle was resolved by the breach")
      (multiple-value-bind (assigned ok line) (assign-automatic after '(:node "n2"))
        (check-equal nil ok "a suspended trial accepted a new automatic assignment")
        (ok (search "suspended" line) "the refusal names the suspension: ~A" line)
        (check-equal 1 (length (assignment-control-attempts assigned))
                     "a refused assignment still recorded an attempt"))))
  ;; An ineligible or absent fallback leaves an explicit scheduling blocker.
  (let* ((trial (r8647-recovery-policy :id "pol-trial" :stage :trial
                                       :tolerances '(:quality 90)
                                       :fallback nil
                                       :role-limits '(:coordinator 1)))
         (lonely (make-assignment-control :policy trial :attempts '() :handles '())))
    (multiple-value-bind (after info) (regression-recover lonely '(:quality 10))
      (check-equal :quality (getf info :breached) "the breach was not recorded")
      (ok (assignment-control-blocker after)
          "no eligible fallback left no scheduling blocker")
      (ok (search "no eligible fallback" (assignment-control-blocker after))
          "the blocker does not name the missing fallback: ~A"
          (assignment-control-blocker after))))
  ;; A fallback that would exceed the trial's role limits is not eligible.
  (let* ((approved-wide (r8647-recovery-policy :id "pol-wide" :stage :adopt
                                               :role-limits '(:coordinator 3 :worker 2)))
         (trial (r8647-recovery-policy :id "pol-trial" :stage :trial
                                       :tolerances '(:quality 90)
                                       :fallback approved-wide
                                       :role-limits '(:coordinator 1 :worker 2))))
    (check-equal nil (fallback-eligible-p approved-wide trial)
                 "a fallback exceeding the role limit was called eligible")))

;;; ------------------------------------------------------------------
;;; TestE02F04AdmitEachRequestAgainstUntil (roadmap nova-work.sexp
;;; E02-F04-02: "Admit each request against until and fence on expiry or
;;; divergence"). SPEC-WORK.md:250-252 states admission is checked per
;;; request, "a request that arrives after `until` is refused `fenced`";
;;; SPEC-WORK.md:419-421 states a session whose clip is refused by
;;; divergence is fenced (here the reconfirm RACED path).
;;; ------------------------------------------------------------------

(deftest "TestE02F04AdmitEachRequestAgainstUntil" "docs/SPEC-WORK.md:250-252,419-421"
    "expected=live-session-admits-up-to-until;post-until-write-refused-fenced-exit-1;diverged-tip-reconfirm-fences-session-raced"
  ;; Expiry: a live session admits every request right up to `until`; the
  ;; first request to arrive after `until` is refused `fenced` at exit 1 and
  ;; the session self-fences.
  (let ((sess (make-session :owner "emma" :generation 3 :token "tok-3"
                            :until "2026-09-14T12:01:00Z" :base "abc123"
                            :state :live :every "30s")))
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:00:59Z")
      (declare (ignore reason))
      (ok admitted "a request before `until` was refused")
      (check-equal 0 code "a pre-`until` request did not exit 0")
      (check-equal :live (session-state sess)
                   "a pre-`until` request fenced the session"))
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :state-to-doing :now "2026-09-14T12:01:01Z")
      (check-equal nil admitted "a request after `until` was admitted")
      (check-equal 1 code "the post-`until` refusal is not exit 1")
      (ok (search "fenced" reason) "the refusal does not name the fence: ~A" reason)
      (check-equal :fenced (session-state sess)
                   "a post-`until` request did not fence the session")))
  ;; Divergence: a reconfirm whose tip moved off the session's base fences the
  ;; session and reports the race.
  (let ((sess (make-session :owner "emma" :generation 4 :token "tok-4"
                            :until "2026-09-14T12:02:00Z" :base "abc123"
                            :state :live :every "30s")))
    (multiple-value-bind (okp line code)
        (session-reconfirm sess "def456" :now "2026-09-14T12:01:00Z")
      (check-equal nil okp "a diverged tip reconfirmed")
      (check-equal 1 code "the divergence refusal is not exit 1")
      (ok (search "SESSION RACED" line) "the divergence does not say RACED: ~A" line)
      (check-equal :fenced (session-state sess)
                   "the divergence did not fence the session"))))
