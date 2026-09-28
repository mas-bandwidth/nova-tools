;;;; slice-21-scope-verbs.lisp --- the E03-F03-01 criterion test (nova-tools
;;;; #3241, recut of #2271), which came in through stream/nova-work-w2
;;;; appended to acceptance.lisp and was moved here so that file keeps at
;;;; most one deftest (nova-tools #560: one file per slice,
;;;; TestOneFilePerSliceAndSection560).

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; E03-F03-01 "Support baseline, discovery, require, dependency add/remove
;;; and prioritize" (docs/roadmaps/nova-work.sexp E03-F03; recut of
;;; nova-tools#2271).
;;;
;;; docs/SPEC-WORK.md:2929 -- "scope and dependencies | baseline, discovery,
;;; dependency add/remove, prioritise, ...". `:required`, the baseline and the
;;; priority slots were seed-time only. Each verb is a MUTATION of the one
;;; writer: an event of its own kind (:require, :baseline, :discovery,
;;; :prioritise -- SPEC-WORK.md:1011, :1096-1100) journaled through the
;;; kernel's command thread, deduplicated by request id before any mutable
;;; precondition, and replayed by `apply-event`, so a reconstruction from the
;;; canonical bytes reads what the live verbs acknowledged and a retried
;;; request answers its original receipt without applying twice. Dependency
;;; add/remove is dev's durable `dep-edit` (dep-verb.lisp), exercised here in
;;; the same history.
;;; ------------------------------------------------------------------

(defparameter *scope-seed*
  '((:id "r"   :type :work-set :parent nil :state :unknown)
    (:id "r/a" :type :task     :parent "r"  :state :todo)
    (:id "r/b" :type :task     :parent "r"  :state :todo  :required nil)
    (:id "r/c" :type :task     :parent "r"  :state :doing)
    (:id "r/x" :type :task     :parent "r"  :state :todo))
  "A container r with required children a, c, x and one optional child b
(SPEC-WORK.md:1147 keeps an optional child in :children but out of the set).")

(defun %scope-call (fn k &rest args)
  "Call a scope verb with the author and stamp every case uses."
  (apply fn k :as "rowan" :stamp "2026-09-23T18:00:00Z" args))

(deftest "TestE03F03SupportBaselineDiscoveryRequireDependency" "docs/SPEC-WORK.md:2929"
    "expected=require-baseline-discovery-prioritise-and-dep-are-journaled;reconstruct-reads-the-same-set;a-retried-request-answers-its-receipt-once"
  (let* ((k (make-kernel :state (make-seed-state *scope-seed*)
                         :journal (make-ordering-journal :capacity 64) :rev-base 1))
         (off-line nil))
    ;; require --to false removes a from the set and detaches nothing.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/a" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok okp "require --to false refused: ~A" line)
      (check-equal 0 code "require --to false at exit 0")
      (setf off-line line))
    (ok (not (node-required-p (kernel-state k) "r/a"))
        "require --to false did not remove r/a from the required set")
    (check-equal 2 (node-required-count (kernel-state k) "r") "the set shrank by one")
    (ok (member "r/a" (node-children (kernel-state k) "r") :test #'string=)
        "require --to false detached r/a from :children, which it must not")
    ;; --to true restores it.
    (multiple-value-bind (okp line)
        (%scope-call 'node-require k :node "r/a" :to t :reason "needed again"
                                     :request "req-require-on")
      (ok okp "require --to true refused: ~A" line))
    (ok (node-required-p (kernel-state k) "r/a")
        "require --to true did not restore r/a to the required set")
    (check-equal 3 (node-required-count (kernel-state k) "r") "the set grew back by one")
    ;; baseline records the set as computed now, member by member.
    (multiple-value-bind (okp line)
        (%scope-call 'baseline k :node "r" :reason "sprint start" :request "req-baseline")
      (ok okp "baseline refused: ~A" line))
    (check-equal '("r/a" "r/c" "r/x") (node-baseline (kernel-state k) "r")
                 "baseline did not record the members it computed")
    ;; discovery adds the existing optional child; a non-child is refused.
    (multiple-value-bind (okp line)
        (%scope-call 'discovery k :node "r" :members '("r/b") :reason "found it"
                                  :request "req-discovery")
      (ok okp "discovery refused: ~A" line))
    (ok (node-required-p (kernel-state k) "r/b") "discovery did not add r/b to the set")
    (check-equal 4 (node-required-count (kernel-state k) "r") "the set is four after discovery")
    (multiple-value-bind (okp line)
        (%scope-call 'discovery k :node "r/a" :members '("r/b") :reason "wrong parent"
                                  :request "req-discovery-bad")
      (ok (not okp) "discovery of a non-child was accepted: ~A" line))
    ;; prioritise sets a rank on the node's :self slot.
    (multiple-value-bind (okp line)
        (%scope-call 'prioritise k :node "r/x" :set 2 :reason "first" :request "req-prio")
      (ok okp "prioritise refused: ~A" line))
    (check-equal 2 (getf (node-priority (kernel-state k) "r/x") :self)
                 "prioritise did not set the :self rank")
    ;; dependency add/remove is dev's durable dep-edit.
    (multiple-value-bind (okp line)
        (dep-edit k :node "r/a" :add "r/x" :as "rowan" :reason "order"
                    :request "req-dep-add" :stamp "2026-09-23T18:00:00Z")
      (ok okp "dep --add refused: ~A" line))
    (check-equal '("r/x") (node-deps (kernel-state k) "r/a") "dep --add did not record the edge")
    ;; Every accepted verb above is on the record: a reconstruction from the
    ;; canonical bytes reads the same set, baseline, rank and edge.
    (let ((rebuilt (reconstruct-state
                    (canonical-string (state-canonical-form (kernel-state k))))))
      (ok (node-required-p rebuilt "r/a") "reconstruct lost require --to true on r/a")
      (ok (node-required-p rebuilt "r/b") "reconstruct lost the discovery of r/b")
      (check-equal 4 (node-required-count rebuilt "r") "reconstruct read a different set size")
      (check-equal '("r/a" "r/c" "r/x") (node-baseline rebuilt "r")
                   "reconstruct lost the baseline")
      (check-equal 2 (getf (node-priority rebuilt "r/x") :self) "reconstruct lost the rank")
      (check-equal '("r/x") (node-deps rebuilt "r/a") "reconstruct lost the dep edge"))
    ;; Retrying the FIRST request after later changes answers its original
    ;; receipt and applies nothing: r/a stays required.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/a" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok okp "the identical retry was refused: ~A" line)
      (check-equal 0 code "the retry at exit 0")
      (check-string= off-line line "the retry answered a different receipt"))
    (ok (node-required-p (kernel-state k) "r/a") "the retry re-applied require --to false")
    (check-equal 4 (node-required-count (kernel-state k) "r") "the retry moved the set")
    ;; The same id with a changed payload is refused and writes nothing.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/c" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok (not okp) "a reused id with a new payload was accepted: ~A" line)
      (check-equal 1 code "the conflict at exit 1"))
    (ok (node-required-p (kernel-state k) "r/c") "the refused conflict wrote r/c")))
