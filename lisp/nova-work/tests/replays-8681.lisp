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

(defun needs-kernel (&optional (seed *needs-seed*))
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
  (let ((k (needs-kernel)))
    (ok (not (member "acme/work/a" (ready-nodes (kernel-state k)) :test #'string=))
        "a whose need is an open PR is not ready")))

;;; ------------------------------------------------------------------
;;; a merged-and-green need makes it ready         SPEC-WORK.md:2110
;;; ------------------------------------------------------------------

(deftest "ready-admits-a-dependent-once-its-need-is-merged-and-green"
    "docs/SPEC-WORK.md:2110"
    "expected=terminal-need-ready"
  (let ((k (needs-kernel)))
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
  (let ((k (needs-kernel)))
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
;;; E07-F04-01 — Preserve source revision, audit IDs, reports and
;;; aliases (docs/roadmaps/nova-work.sexp, feature E07-F04 "Fixed Tables
;;; imported baseline inventory"). The SPEC contract is
;;; docs/SPEC-WORK.md:7826 — "Original audit IDs, historical reports and
;;; superseding aliases remain retrievable. They are provenance, not
;;; current completion assertions." — together with :7786 "Pin the
;;; baseline membership, source revision and completion unit before
;;; starting the stream."
;;;
;;; A roadmap exported and loaded again must carry the provenance it was
;;; imported with: its pinned source revision, its audit id, its historical
;;; reports and its superseding aliases. The kernel's round-trip
;;; (export-roadmap-view / load-roadmap-view) keeps only :members :axes
;;; :retired :projections :revision, so the rest is dropped.
;;; ------------------------------------------------------------------

(deftest "TestE07F04PreserveSourceRevisionAuditIDs" "docs/SPEC-WORK.md:7826"
    "expected=source-revision,audit-id,reports,aliases=retrievable-after-export/load"
  (let* ((view (list :id "E07-F04"
                     :source-revision "8ea5ed8e4656875088250f564e88a965a7135e7c"
                     :reports '("report-2026-09-15")
                     :aliases '(("E07-F04-old" . "E07-F04"))
                     :members '("acme/work/f1")
                     :axes '()
                     :retired '()
                     :projections '()
                     :revision 3))
         (loaded (load-roadmap-view (export-roadmap-view view))))
    (check-string= "8ea5ed8e4656875088250f564e88a965a7135e7c"
                   (getf loaded :source-revision)
                   "a roadmap's source revision did not survive export/load")
    (check-string= "E07-F04" (getf loaded :id)
                   "a roadmap's audit id did not survive export/load")
    (check-equal '("report-2026-09-15") (getf loaded :reports)
                 "a roadmap's historical reports did not survive export/load")
    (check-equal '(("E07-F04-old" . "E07-F04")) (getf loaded :aliases)
                 "a roadmap's superseding aliases did not survive export/load")))
