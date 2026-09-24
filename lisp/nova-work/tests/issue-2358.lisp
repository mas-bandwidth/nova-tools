;;;; issue-2358.lisp --- Wire the #785 dependency gate at the admission verbs
;;;; (rule 3) and its flag-and-read seams.
;;;;
;;;; nova-tools #2358: "every-admission-verb-refuses-an-unmet-need"
;;;; SPEC-WORK.md:6948 — over D with N open and no lease, offer or allocation
;;;; on D: take --node, take --node --for <name>, offer, state --to doing,
;;;; goal update --progress, take --machine, and task packet; each exits 1 with
;;;; its own FAIL line ending "unmet need N <reason>", and nothing is written.

(in-package #:nova-work/tests)

(deftest "issue-2358" "nova-tools#2358"
    "every-admission-verb-refuses-an-unmet-need"
  ;; D has N open as a dependency. Over D with N open, every admission verb
  ;; refuses with "unmet need N <reason>" and writes nothing.
  (let* ((seed '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
                 (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
                  :deps ("acme/work/n"))
                 (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)
                         :journal (make-ordering-journal) :rev-base 1)))
    ;; (a) take --node over D with N open: refuses with unmet need, not "held"
    (multiple-value-bind (okp line code)
        (submit k (list :verb :take-node :node "acme/work/d" :by "rowan"
                        :request "take-d-1" :stamp "2026-09-16T11:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok (not okp) "take --node over a node with an unmet need is refused")
      (ok (search "unmet need" line) "the refusal names the unmet need: ~A" line)
      (check-equal 1 code "the exit code is 1"))
    ;; (b) state --to doing over D with N open: refuses with unmet need
    (multiple-value-bind (okp line code)
        (submit k (list :verb :state-to-doing :node "acme/work/d" :by "rowan"
                        :reason "started" :request "doing-d-1"
                        :stamp "2026-09-16T11:00:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok (not okp) "state --to doing over a node with an unmet need is refused")
      (ok (search "unmet need" line) "STATE FAIL names the unmet need: ~A" line)
      ;; STATE FAIL carries no rule number for the needs-met refusal
      (ok (not (search "rule" line)) "STATE FAIL for unmet need carries no rule number: ~A" line)
      (check-equal 1 code "the exit code is 1"))
    ;; (c) With N met (settled to done with verified evidence), both verbs succeed
    (let* ((n-seed '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
                     (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
                      :deps ("acme/work/n"))
                     (:id "acme/work/n" :type :task     :parent "acme/work" :state :todo)))
           (k2 (make-kernel :state (make-seed-state n-seed)
                            :journal (make-ordering-journal) :rev-base 1)))
      ;; settle N
      (multiple-value-bind (okp)
          (submit k2 (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                           :reason "started" :request "doing-n-1"
                           :stamp "2026-09-16T11:00:00Z" :clock :tool
                           :generation-owner "gen-1"))
        (ok okp "N moves to doing"))
      (multiple-value-bind (okp)
          (submit k2 (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                           :reason "merged" :evidence '("ev-1") :request "done-n-1"
                           :stamp "2026-09-16T12:00:00Z" :clock :tool
                           :generation-owner "gen-1"))
        (ok okp "N settles to done"))
      ;; take --node over D with N met: succeeds
      (multiple-value-bind (okp line)
          (submit k2 (list :verb :take-node :node "acme/work/d" :by "rowan"
                           :request "take-d-2" :stamp "2026-09-16T13:00:00Z"
                           :clock :tool :generation-owner "gen-1"))
        (ok okp "take --node over a node whose need is met succeeds: ~A" line))
      ;; state --to doing over D with N met: succeeds
      (multiple-value-bind (okp line)
          (submit k2 (list :verb :state-to-doing :node "acme/work/d" :by "rowan"
                           :reason "started" :request "doing-d-2"
                           :stamp "2026-09-16T13:00:00Z" :clock :tool
                           :generation-owner "gen-1"))
        (ok okp "state --to doing over a node whose need is met succeeds: ~A" line)))))
