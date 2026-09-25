;;;; replays-8681.lisp --- nova-tools #785, nova-work v2 dependencies.
;;;;
;;;; The smallest coherent slice of the v2 dependency issue: a node's `:deps`
;;;; reference edges to other nodes, `query ready` = every need terminal
;;;; accepted (settled in C), the validator's `:deps` cycle refusal, and the
;;;; `needs-broken` flag a revert of a need raises on its dependents. Each
;;;; deftest names the line of docs/SPEC-WORK.md it comes from.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; helpers
;;; ------------------------------------------------------------------

(defparameter *needs-seed*
  '((:id "acme/work"        :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/a"      :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/pr-1"))
    (:id "acme/work/pr-1"   :type :task     :parent "acme/work" :state :doing))
  "A dependent leaf a whose one need is pr-1; pr-1 is open, so a is not ready.")

(defparameter *needs-cycle-seed*
  '((:id "a" :type :task :parent nil :state :todo :deps ("b"))
    (:id "b" :type :task :parent nil :state :todo :deps ("a")))
  "a needs b and b needs a: SPEC-WORK.md:5041 rule 3, a dependency cycle is a
deadlock nobody can finish.")

(defun make-needs-kernel (&optional (seed *needs-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1))

(defun need-done-request (node request stamp)
  (list :verb :state-to-done :node node :by "rowan" :reason "merged"
        :evidence '("ev-1") :request request :stamp stamp :clock :tool
        :generation-owner "gen-1"))

(defun need-reopen-request (node request stamp)
  (list :verb :event-reopen :node node :by "rowan" :reason "reverted"
        :request request :stamp stamp :clock :tool :generation-owner "gen-1"))

;;; ------------------------------------------------------------------
;;; ready excludes an open need                    SPEC-WORK.md:2110
;;; ------------------------------------------------------------------

(deftest "ready-excludes-a-dependent-whose-need-is-open" "docs/SPEC-WORK.md:2110"
    "expected=open-need-not-ready"
  (let ((k (make-needs-kernel)))
    (ok (not (member "acme/work/a" (ready-nodes (kernel-state k)) :test #'string=))
        "a whose need is an open PR is not ready")))

;;; ------------------------------------------------------------------
;;; a merged-and-green need makes it ready         SPEC-WORK.md:2110
;;; ------------------------------------------------------------------

(deftest "ready-admits-a-dependent-once-its-need-is-merged-and-green"
    "docs/SPEC-WORK.md:2110"
    "expected=terminal-need-ready"
  (let ((k (make-needs-kernel)))
    (multiple-value-bind (ok line)
        (submit k (need-done-request "acme/work/pr-1" "req-pr-1" "2026-09-16T12:00:00Z"))
      (ok ok "the need closes: ~A" line))
    (ok (member "acme/work/a" (ready-nodes (kernel-state k)) :test #'string=)
        "a is ready once its need is merged and green")))

;;; ------------------------------------------------------------------
;;; a cycle refuses                                SPEC-WORK.md:5041
;;; ------------------------------------------------------------------

(deftest "a-needs-cycle-refuses-at-seed" "docs/SPEC-WORK.md:5041"
    "expected=dependency-cycle-refused"
  (ok (handler-case (progn (make-seed-state *needs-cycle-seed*) nil)
        (unsupported-input (c) (declare (ignore c)) t))
      "a :deps cycle must refuse before publication"))

;;; ------------------------------------------------------------------
;;; a revert marks dependents needs-broken         SPEC-WORK.md:2110
;;; ------------------------------------------------------------------

(deftest "reverting-a-need-marks-dependents-needs-broken" "docs/SPEC-WORK.md:2110"
    "expected=revert-flags-dependent"
  (let ((k (make-needs-kernel)))
    (multiple-value-bind (ok line)
        (submit k (need-done-request "acme/work/pr-1" "req-pr-1" "2026-09-16T12:00:00Z"))
      (ok ok "the need closes: ~A" line))
    (ok (member "acme/work/a" (ready-nodes (kernel-state k)) :test #'string=)
        "a is ready after the need merges")
    (multiple-value-bind (ok line)
        (submit k (need-reopen-request "acme/work/pr-1" "req-pr-2" "2026-09-16T13:00:00Z"))
      (ok ok "the need reopens: ~A" line))
    (ok (node-needs-broken (kernel-state k) "acme/work/a")
        "a is flagged needs-broken after its need is reverted")
    (ok (not (member "acme/work/a" (ready-nodes (kernel-state k)) :test #'string=))
        "a needs-broken dependent is not ready")))

;;; ------------------------------------------------------------------
;;; partial-child-never-closes-parent GUARD     SPEC-WORK.md:4655
;;; ------------------------------------------------------------------
;;;
;;; E11-F04-03 (ROADMAP.md:1126, docs/SPEC-WORK.md:4655) -- the criterion is
;;; verified by the production code and the test in replays-8648.lisp:303.
;;; This guard covers the spec's "blocked or asleep" case (one child done,
;;; one child still doing, parent stays open with outstanding=1) which the
;;; "refused" case alone does not exercise.
;;;
;;; Production path: submit -> %cascade-events in kernel.lisp checks that
;;; (zerop (wnode-required-open parent)) before settling a parent; a terminal
;;; event (refusal) writes no settle, so %adjust-counters never fires for it.
;;; The call site is kernel.lisp:300-323 (the :state-to-done cascade gate).

(deftest "TestE11F04PartialChildBlockedParentOpen" "docs/SPEC-WORK.md:4655"
    "expected=one-child-done-one-blocked-parent-open;outstanding=1"
  ;; One parent work-set with two required children: one that finishes and
  ;; one that stays blocked (never finishes, never refuses -- asleep).
  (let ((k (make-kernel
            :state (make-seed-state
                    '((:id "p"    :type :work-set :parent nil :state :unknown
                            :links ("acme/repo#55"))
                      (:id "p/t1" :type :task :parent "p" :state :doing)
                      (:id "p/t2" :type :task :parent "p" :state :doing))))))
    ;; Before anything moves: two required members outstanding.
    (check-equal 2 (node-required-open (kernel-state k) "p")
                 "the parent opens with both required children outstanding")
    (check-equal 1 (open-issue-count k)
                 "the mapped external issue is open")
    ;; The first child is done.
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "p/t1" :by "emma"
                        :reason "shipped" :evidence '("ev-1") :request "e11f04-guard-1"
                        :stamp "2026-09-20T12:00:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok okp "the first child settles: ~A" line))
    (check-equal :c (node-branch (kernel-state k) "p/t1")
                 "the done child is in C")
    ;; The second child is still doing (blocked/asleep): has not settled and
    ;; has not refused.  One child done beside one blocked/asleep never
    ;; closes the parent.
    (check-equal :o (node-branch (kernel-state k) "p")
                 "one child done beside one blocked leaves the parent open")
    (check-equal 1 (node-required-open (kernel-state k) "p")
                 "the parent stays open with outstanding=1")
    (check-equal :o (node-branch (kernel-state k) "p/t2")
                 "the blocked child is still in O")
    (check-equal 1 (open-issue-count k)
                 "the mapped external issue survives the partial child completion open")))

;;; ------------------------------------------------------------------
;;; partial-child-never-closes-parent BOUNDARY    SPEC-WORK.md:4655
;;; ------------------------------------------------------------------
;;;
;;; E11-F04-03 -- the binding constraint: one refused child beside one
;;; done child does not change the parent's outstanding count.  The
;;; "survive the child's refusal unchanged" clause means the count after
;;; refusal equals the count before refusal.

(deftest "TestE11F04PartialChildRefusalPreservesOutstanding" "docs/SPEC-WORK.md:4655"
    "expected=outstanding-survives-refusal-unchanged;count-before-equals-count-after"
  (let ((k (make-kernel
            :state (make-seed-state
                    '((:id "p"    :type :work-set :parent nil :state :unknown)
                      (:id "p/t1" :type :task :parent "p" :state :doing)
                      (:id "p/t2" :type :task :parent "p" :state :doing))))))
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "p/t1" :by "emma"
                        :reason "shipped" :evidence '("ev-1") :request "e11f04-boundary-1"
                        :stamp "2026-09-20T12:00:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok okp "the first child settles: ~A" line))
    (let ((outstanding-before (node-required-open (kernel-state k) "p")))
      (multiple-value-bind (okp line)
          (submit k (list :verb :event-cancel :node "p/t2" :by "emma"
                          :reason "refused: over budget" :request "e11f04-boundary-2"
                          :stamp "2026-09-20T12:01:00Z" :clock :tool
                          :generation-owner "gen-1"))
        (ok okp "the second child's refusal is recorded: ~A" line))
      (check-equal outstanding-before
                   (node-required-open (kernel-state k) "p")
                   "the outstanding count survives the child's refusal unchanged"))))
