;;;; replays-l2400-1.lisp --- batch 1 of the #362 acceptance replays named in
;;;; docs/SPEC-WORK.md lines 2400-3600, red first.
;;;;
;;;; Eight replays, one deftest each, asserting what the spec says the replay
;;;; must show. The names below are fixed by the spec's *Replays* list and by
;;;; the sections that cite them; the pure models they exercise live in
;;;; `src/replays-l2400-1.lisp`, written after these so the failing lines stand.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; axisless-history                          SPEC-WORK.md:3103 / 5714
;;; ------------------------------------------------------------------

(deftest "axisless-history" "docs/SPEC-WORK.md:3103"
    "expected=two-ordered-rows;denominator-not-reduced-by-completion;retire-keeps-node;prior-view-reconstructs"
  (let ((rm (nova-work::make-roadmap "view-1")))
    (nova-work::roadmap-add-row rm "task-a" '("ev-a-1"))
    (nova-work::roadmap-add-row rm "task-b" '("ev-b-1"))
    (check-equal 2 (nova-work::roadmap-denominator rm) "two ordered rows added")
    (check-equal 0 (nova-work::rrow-order (nova-work::roadmap-row rm "task-a"))
                 "first row ordered 0")
    (check-equal 1 (nova-work::rrow-order (nova-work::roadmap-row rm "task-b"))
                 "second row ordered 1")
    ;; One row finished; the denominator is not reduced by completion.
    (nova-work::roadmap-finish-row rm "task-b")
    (check-equal 2 (nova-work::roadmap-denominator rm)
                 "completion does not reduce the denominator")
    (check-equal t (nova-work::rrow-finished (nova-work::roadmap-row rm "task-b"))
                 "the finished row is marked finished")
    ;; Export and load: both rows and their evidence survive.
    (let* ((exp (nova-work::roadmap-export rm))
           (back (nova-work::roadmap-load "view-1" exp)))
      (check-equal exp (nova-work::roadmap-export back)
                   "export/load reproduces both rows and their evidence")
      (ok (and (nova-work::roadmap-row back "task-a")
               (nova-work::roadmap-row back "task-b"))
          "both rows present after load"))
    ;; A retired row records a scope movement and keeps its node.
    (let ((rev-before (nova-work::roadmap-revision rm)))
      (nova-work::roadmap-retire-row rm "task-a")
      (check-equal t (nova-work::rrow-retired (nova-work::roadmap-row rm "task-a"))
                   "the retired row is marked retired")
      (ok (nova-work::roadmap-row rm "task-a") "a retired row keeps its node")
      (check-equal nil (nova-work::rrow-retired
                        (nova-work::roadmap-row
                         (nova-work::roadmap-load "view-1" (nova-work::roadmap-view-at rm rev-before))
                         "task-a"))
                   "the prior view reconstructs at its captured revision"))))

;;; ------------------------------------------------------------------
;;; a-root-id-grants-nothing                 SPEC-WORK.md:3132 / 5733
;;; ------------------------------------------------------------------

(deftest "a-root-id-grants-nothing" "docs/SPEC-WORK.md:3132"
    "expected=unmapped-root-refuses-file-but-chat-renders;escape-symlink-identity-refused;lock-cooperative"
  (let ((root (nova-work::make-permitted-root "repo-root" "acme/work"))
        (mapped (nova-work::map-render-root (nova-work::make-permitted-root "repo-root" "acme/work")
                                            '("opt" "acme") "acme/work")))
    ;; A stored permitted root with no --render-root mapping: file refuses, chat renders.
    (check-equal t (nova-work::render-chat-permitted-p root) "chat renders with a permitted root")
    (check-equal nil (nova-work::render-file-permitted-p root)
                 "file mode refuses a permitted root with no mapping")
    (check-equal t (nova-work::render-file-permitted-p mapped)
                 "file mode admits a permitted root WITH a mapping")
    ;; An escaping target refuses.
    (check-equal nil (nova-work::target-inside-root-p '("opt" ".." "etc" "passwd")
                                                      mapped)
                 "an escaping path refuses")
    (check-equal t (nova-work::target-inside-root-p '("opt" "acme" "proj" "main.md")
                                                    mapped)
                 "a clean path inside the root is admitted")
    ;; A symlink escape refuses.
    (check-equal nil (nova-work::target-symlink-ok-p '("opt" "acme" "link" "x")
                                                     '("link"))
                 "a symlink escape refuses")
    ;; A target identity other than the mapping's refuses.
    (check-equal nil (nova-work::target-repo-matches-p "acme/other" mapped)
                 "a target identity other than the mapping's refuses")
    (check-equal t (nova-work::target-repo-matches-p "acme/work" mapped)
                 "the mapping's own identity matches")
    ;; The cooperative lock is exercised: a second holder refuses while held.
    (let ((lock (nova-work::make-render-lock)))
      (check-equal t (nova-work::render-lock-acquire lock "holder-a") "first holder acquires")
      (check-equal nil (nova-work::render-lock-acquire lock "holder-b") "second holder refuses")
      (nova-work::render-lock-release lock "holder-a")
      (check-equal t (nova-work::render-lock-acquire lock "holder-b") "release admits the next holder")
      (check-equal t (nova-work::render-lock-advisory-p lock)
                   "the lock stays advisory; an external editor limit is retained"))))

;;; ------------------------------------------------------------------
;;; state-export-refuses-a-gap                SPEC-WORK.md:3274 / 5759
;;; ------------------------------------------------------------------

(deftest "state-export-refuses-a-gap" "docs/SPEC-WORK.md:3274"
    "expected=gap-digest-dangling-escape-symlink-overrun-corrupt-proof-each-refused;range-declares-omissions"
  ;; A changed digest refuses.
  (let ((m (nova-work::make-export-member '("state" "root.sexp") :state "abc" "def...")))
    (check-equal nil (nova-work::export-member-digest-ok-p m) "a changed digest refuses"))
  (check-equal t (nova-work::export-member-digest-ok-p
                  (let ((b "abc")) (nova-work::make-export-member '("m") :x b (nova-work::sha256-hex b))))
               "a matching digest is admitted")
  ;; Missing mandatory member and dangling internal reference refuse.
  (let ((b (nova-work::make-export-bundle :members '() :required-members '("schema")
                                          :refs '())))
    (check-equal '("schema") (nova-work::export-missing-mandatory b)
                 "a missing mandatory member refuses")
    (check-equal nil (nova-work::export-missing-mandatory
                      (nova-work::make-export-bundle
                       :members (list (nova-work::make-export-member
                                       '("schema") :schema "x" "x"))
                       :required-members '("schema") :refs '()))
                 "a present mandatory member is whole"))
  (check-equal '("state/root.sexp") (nova-work::export-dangling-refs
                                     (nova-work::make-export-bundle
                                      :members '() :required-members '()
                                      :refs '(("manifest" . "state/root.sexp"))))
               "a dangling internal reference refuses")
  ;; Path escape and symlink refuse.
  (check-equal nil (nova-work::export-member-path-ok-p '("a" ".." "b")) "a path escape refuses")
  (check-equal nil (nova-work::export-member-path-ok-p '("a" "." "b")) "a dot segment refuses")
  (check-equal nil (nova-work::export-member-symlink-ok-p '("a" "link" "b") '("link"))
               "a symlink member refuses")
  ;; An output overrun refuses (exact running sum of bytes).
  (check-equal nil (nova-work::export-output-within-p
                    (list (nova-work::make-export-member '("a") :x "12345" "x")
                          (nova-work::make-export-member '("b") :x "67" "x"))
                    6)
               "an overrun past --max-output-bytes refuses")
  (check-equal t (nova-work::export-output-within-p
                  (list (nova-work::make-export-member '("a") :x "12345" "x")) 5)
               "exactly the bound is within it")
  ;; A corrupt S-expression refuses, not as a valid-looking empty load.
  (check-equal t (nova-work::export-member-corrupt-p "((")
               "a corrupt s-expression refuses")
  ;; A historical export whose resolver observations are gone: a named proof gap.
  (check-equal "proof gap: resolver observation obs-1"
               (nova-work::export-proof-gap-reason
                (nova-work::make-export-bundle
                 :required-observations '(("obs-1" . :gone)) :members '() :required-members '() :refs '()))
               "a gone resolver observation is a named proof gap, never current ones")
  (check-equal nil (nova-work::export-proof-gap-reason
                    (nova-work::make-export-bundle
                     :required-observations '(("obs-1" . :present)) :members '() :required-members '() :refs '()))
               "a present observation carries no gap")
  ;; --closed-history range [from,to) declares its omissions while keeping closure.
  (check-equal '("2026-09-13" "2026-09-15")
               (nova-work::closed-history-omissions :range "2026-09-14" "2026-09-15"
                                                    '("2026-09-13" "2026-09-14" "2026-09-15"))
               "a half-open range lists the excluded members"))

;;; ------------------------------------------------------------------
;;; fenced-export-can-finish                 SPEC-WORK.md:3274 / 5782
;;; ------------------------------------------------------------------

(deftest "fenced-export-can-finish" "docs/SPEC-WORK.md:3274"
    "expected=own-export-status-wait-cancel-admitted;unknown-and-writes-refused-fenced"
  (let ((op (nova-work::make-export-operation "op-1" :running)))
    ;; The fenced session reads its own export by id.
    (check-equal :running (nova-work::fenced-operation-status "op-1" op "op-1")
                 "a fenced session reads its own export status by id")
    (check-equal nil (nova-work::fenced-operation-status "op-1" op "op-9")
                 "an unknown id refuses")
    ;; An unfinished export cancels with publication reconciled.
    (nova-work::cancel-export op)
    (check-equal :cancelled (nova-work::export-operation-status op)
                 "an unfinished export cancels")
    (check-equal :reconciled (nova-work::export-operation-publication op)
                 "publication is reconciled on cancel")
    ;; operation list, another id's wait and every canonical write refuse fenced.
    (check-equal nil (nova-work::fenced-allow-p "op-1" :operation-list nil)
                 "operation list refuses in a fenced session")
    (check-equal t (nova-work::fenced-allow-p "op-1" :operation-status "op-1")
                 "status on the own export id is admitted")
    (check-equal nil (nova-work::fenced-allow-p "op-1" :operation-status "op-3")
                 "status on another id refuses")
    (check-equal nil (nova-work::fenced-allow-p "op-1" :canonical-write nil)
                 "a canonical write refuses fenced")))

;;; ------------------------------------------------------------------
;;; four-capability-groups-and-three-fields    SPEC-WORK.md:3373 / 5611
;;; ------------------------------------------------------------------

(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:3373"
    "expected=four-groups-expressible;support-verified-capacity-are-three-fields"
  (let ((caps (list (nova-work::make-capability "cap-1" :child-agent "bench-a")
                    (nova-work::make-capability "cap-2" :swarm "bench-a")
                    (nova-work::make-capability "cap-3" :local-model "bench-a")
                    (nova-work::make-capability "cap-4" :one-shot "bench-a"))))
    (dolist (group '(:child-agent :swarm :local-model :one-shot))
      (ok (find group caps :key #'nova-work::capability-group :test #'eq)
          "capability group ~A expressible" group))
    ;; Each entry carries its stable id, source, last-verified, availability, constraints.
    (let ((c (nova-work::make-capability "cap-1" :child-agent "bench-a")))
      (check-equal "cap-1" (nova-work::capability-id c) "stable capability id")
      (check-equal "bench-a" (nova-work::capability-source c) "source")
      (check-equal "2026-09-14T00:00:00Z" (nova-work::capability-last-verified c) "last-verified stamp")
      (check-equal :available (nova-work::capability-availability c) "availability")
      (check-equal '() (nova-work::capability-constraints c) "constraints"))
    ;; Declared support, verified runtime and free capacity are three fields, never one.
    (let ((c (nova-work::make-capability "cap-1" :child-agent "bench-a"
                                         :declared-support t :verified-runtime nil
                                         :free-capacity 0)))
      (check-equal t (nova-work::capability-declared-support c) "declared support field")
      (check-equal nil (nova-work::capability-verified-runtime c) "verified runtime field (distinct)")
      (check-equal 0 (nova-work::capability-free-capacity c) "free capacity field (distinct)")
      (ok (and (nova-work::capability-declared-support c)
               (null (nova-work::capability-verified-runtime c)))
          "declared support and verified runtime never collapse into one"))))

;;; ------------------------------------------------------------------
;;; dispatch-ack-and-ownership-are-three       SPEC-WORK.md:3390 / 5550
;;; ------------------------------------------------------------------

(deftest "dispatch-ack-and-ownership-are-three" "docs/SPEC-WORK.md:3390"
    "expected=dispatch-delivery-ack-ownership-distinguished;offer-reserves-declared-only;timeout-no-duplicate;return-reconciles"
  (let ((ds (nova-work::make-dispatch-state)))
    (nova-work::dispatch-intent ds "req-1" "task-a")
    (nova-work::dispatch-deliver ds "req-1")
    (nova-work::dispatch-ack ds "req-1")
    (nova-work::dispatch-accept ds "req-1")
    (check-equal t (nova-work::dispatch-intent-p ds "req-1") "dispatch is its own fact")
    (check-equal t (nova-work::dispatch-delivered-p ds "req-1") "delivery is its own fact")
    (check-equal t (nova-work::dispatch-acknowledged-p ds "req-1") "acknowledgement is its own fact")
    (check-equal t (nova-work::dispatch-accepted-p ds "req-1") "accepted ownership is its own fact")
    ;; A pending offer reserves only declared capacity.
    (check-equal 3 (nova-work::offer-reserve 5 3) "an offer caps at declared capacity")
    (check-equal 5 (nova-work::offer-reserve 5 9) "an offer never exceeds declared capacity")
    ;; A live worker, a timeout; no duplicate launch.
    (nova-work::dispatch-worker-live ds "task-a")
    (check-equal nil (nova-work::timeout-may-launch-p ds "task-a")
                 "a timeout alone launches no duplicate while the worker runs")
    (check-equal t (nova-work::timeout-may-launch-p ds "task-b")
                 "a node with no live worker may launch")
    ;; A return reconciles before any new dispatch.
    (check-equal nil (nova-work::dispatch-gate-open-p ds) "the gate is shut before a return")
    (nova-work::dispatch-reconcile ds)
    (check-equal t (nova-work::dispatch-gate-open-p ds) "a return reconciles before new dispatch")))

;;; ------------------------------------------------------------------
;;; a-retry-does-not-overwrite-its-attempt      SPEC-WORK.md:3391 / 5553
;;; ------------------------------------------------------------------

(deftest "a-retry-does-not-overwrite-its-attempt" "docs/SPEC-WORK.md:3391"
    "expected=retry-mints-a-new-attempt;old-attempt-model-and-usage-kept;usual-model-not-proof"
  (let* ((log (nova-work::make-attempt-log))
         (first (nova-work::make-attempt "at-1" "opus" "opus" 120))
         (_ (nova-work::attempt-log-add log first))
         (second (nova-work::retry log first "opus")))
    (check-equal 2 (nova-work::attempt-log-count log) "a retry mints a new attempt beside the old")
    (check-equal "opus" (nova-work::attempt-observed-model first)
                 "the first attempt keeps its observed model")
    (check-equal 120 (nova-work::attempt-usage first) "the first attempt keeps its usage")
    (ok (not (eq first second)) "the retry is a distinct attempt, not an overwrite")
    (check-equal '(:absent) (nova-work::attempt-observed-model second)
                 "the new attempt's observed model stays unknown")
    (check-equal 0 (nova-work::attempt-usage second) "the new attempt carries no copied usage")
    (ok (not (equal (nova-work::attempt-observed-model first)
                    (nova-work::attempt-observed-model second)))
        "the friend's usual model never stands as proof of the delegated executor")))

;;; ------------------------------------------------------------------
;;; explicit-rest-is-not-pinged                SPEC-WORK.md:3407 / 5556
;;; ------------------------------------------------------------------

(deftest "explicit-rest-is-not-pinged" "docs/SPEC-WORK.md:3407"
    "expected=resting-and-reserved-not-pinged;one-bounded-ping;unconfirmed-not-sleep;probe-is-unresolved"
  (let ((resting (nova-work::make-availability :resting))
        (reserved (nova-work::make-availability :awake :reserved-from-wakeups t))
        (awake (nova-work::make-availability :awake)))
    (check-equal nil (nova-work::ping-eligible-p resting) "explicit rest is not pinged")
    (check-equal nil (nova-work::ping-eligible-p reserved) "a friend reserved from wakeups is not pinged")
    (check-equal t (nova-work::ping-eligible-p awake) "an awake friend may be pinged")
    ;; The ping is one and bounded.
    (nova-work::ping-once awake)
    (nova-work::ping-once awake)
    (check-equal 1 (nova-work::availability-ping-count awake) "the ping is one and bounded")
    (let ((no-answer (nova-work::make-availability :awake)))
      (nova-work::record-no-answer no-answer)
      (check-equal :unavailable (nova-work::availability-status no-answer)
                   "a nonresponsive capacity is marked unavailable")
      (check-equal :unconfirmed (nova-work::availability-reason no-answer)
                   "the reason is unconfirmed, never sleep or exhausted credit"))
    (check-equal :unresolved-delivery (nova-work::probe-failure-verdict)
                 "a failed probe is unresolved delivery, not a failed friend")))
