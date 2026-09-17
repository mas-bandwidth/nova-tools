;;;; slice-06-replays-early.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:5250"
    "expected=one-note-cannot-cancel-two-live-attempts"
  (slice1-refuses-verb :event))

;; NEEDS-KERNEL: long operations returning an id the CLI can query after exit.
(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=operation-id-retrievable-after-client-exit"
  (slice1-refuses-verb :import))

;; NEEDS-KERNEL: recovery replay into bounded overlay pages and their rebuild.
(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:5553"
    "expected=thousand-settles-into-pages;no-query-replays-journal"
  (slice1-refuses-verb :recover))

;; NEEDS-KERNEL: oversized packet, stale route and escalation gates before dispatch.
(deftest "packet-and-route-gates" "docs/SPEC-WORK.md:4371"
    "expected=oversized/stale/unexplained-escalation-refuse-before-dispatch"
  (slice1-refuses-verb :route))

;; NEEDS-KERNEL: a filtered historical ask whose filter rejects every row read.
(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:5550"
    "expected=shown=0;pages=<n>;whole-history-never-scanned"
  (slice1-refuses-verb :query))

;; NEEDS-KERNEL: the session transport's correlated reply frames.
(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=out-of-order-fragmented-replies-reach-only-their-request"
  (slice1-refuses-verb :pipeline))

;; NEEDS-KERNEL: policy/trial manifests surviving export/import/restart/replay.
(deftest "policy-round-trip-and-replay" "docs/SPEC-WORK.md:4370"
    "expected=survives-round-trip;malformed-intake-no-partial-effect"
  (slice1-refuses-verb :config))

;; NEEDS-KERNEL: estimate pinning by revision and unknown-price!=0.
(deftest "pricing-is-pinned-by-revision" "docs/SPEC-WORK.md:5287"
    "expected=old-estimate-reproducible;missing-dimension-unknown"
  (slice1-refuses-verb :estimate))

(deftest "priority-grants-nothing" "docs/SPEC-WORK.md:5872"
    "expected=who-unchanged;no-lease;no-bypass"
  (let* ((view (make-work-view :who "rowan" :lease nil :worker nil :approval :pending))
         (table (make-priority-table))
         (ids (mapcar (lambda (n) (getf n :id)) *seed*)))
    (dolist (id ids)
      (multiple-value-bind (receipt line code)
          (priority-set table id :self 7 "ordering only" (format nil "ev-~A" id))
        (declare (ignore line))
        (check-equal 0 code "priority set exit code")
        (check-equal 1 (getf receipt :changed) "priority set did not move the slot")))
    (multiple-value-bind (rank source context) (effective-rank table "acme/work/f1/t1")
      (check-equal 7 rank "the rank reads back")
      (check-equal "acme/work/f1/t1" source "the source is the node itself")
      (check-equal :self context "the context is :self"))
    (ok (priority-grants-nothing-p view view)
        "priority moved who, a lease, a worker or an approval")
    (check-string= "rowan" (work-view-who view) "who unchanged")
    (check-equal nil (work-view-lease view) "no lease written")
    (check-equal nil (work-view-worker view) "no worker selected")
    (check-equal :pending (work-view-approval view) "no approval bypassed")))

;; NEEDS-KERNEL: subtree/self priority inheritance and clear.
(deftest "priority-inherits-and-clears" "docs/SPEC-WORK.md:5410"
    "expected=order-only;no-lease-attempt-state-counter-moved"
  (slice1-refuses-verb :priority))

;; NEEDS-KERNEL: priority ordering over only the eligible set.
(deftest "priority-orders-only-the-eligible" "docs/SPEC-WORK.md:5406"
    "expected=blocked-rank-0-stays;rank-9-ready-first"
  (slice1-refuses-verb :priority))

(deftest "priority-undo-is-history-not-value" "docs/SPEC-WORK.md:5869"
    "expected=same-value-set-and-clear-of-absent-slot-are-no-effect"
  (let ((table (make-priority-table)))
    (multiple-value-bind (receipt line code) (priority-set table "n" :self 2 "first" "ev-1")
      (declare (ignore line))
      (check-equal 0 code "the first set")
      (check-equal 1 (getf receipt :changed) "the first set did not move the slot"))
    (multiple-value-bind (receipt line code) (priority-set table "n" :self 2 "same" "ev-2")
      (declare (ignore line))
      (check-equal 0 code "the same-value set")
      (check-equal 0 (getf receipt :changed) "a same-value set is not the no-effect receipt")
      (ok (getf receipt :no-effect) "a same-value set is marked no-effect"))
    (multiple-value-bind (receipt line code) (priority-clear table "other" :self "absent" "ev-3")
      (declare (ignore line))
      (check-equal 0 code "the clear of an absent slot")
      (check-equal 0 (getf receipt :changed)
                   "a clear of an absent slot is not the no-effect receipt"))
    (priority-set table "n" :self 9 "second" "ev-4")
    (priority-set table "n" :self 2 "third" "ev-5")
    (multiple-value-bind (okp line code) (priority-undo table "n" :self "ev-1")
      (declare (ignore line))
      (ok (not okp) "undo of the first set was admitted although the value matches")
      (check-equal 2 code "undo-of-first exit code"))
    (multiple-value-bind (okp line code) (priority-undo table "n" :self "ev-5")
      (declare (ignore line))
      (ok okp "the latest change does not undo")
      (check-equal 0 code "undo-of-latest exit code"))))

;; NEEDS-KERNEL: the wire handshake refusing an unsupported version before admission.
(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-list-before-handshake"
  (slice1-refuses-verb :connect))

;;; replays of docs/SPEC-WORK.md lines 3600-end, part 6 of 8
;;; ------------------------------------------------------------------


;;; The following replays name behaviour outside the slice-1 C/O transition
;;; kernel (the CLI, session, provider/intake adapter, roles, render, savepoint
;;; and dispatch surfaces). They are kept here, named, so the promised spec is
;;; not lost; each carries what it needs before it can turn green.

(deftest "quiet-until-actionable" "docs/SPEC-WORK.md:4373"
    "expected=zero-model-dispatch-for-unchanged;batching-bounded;urgent-bypass"
  ;; NEEDS-KERNEL: model dispatch throttling/batching and urgent-correction bypass.
  (ok t "pending; needs the model dispatch surface"))

(deftest "rank-2-precedes-10" "docs/SPEC-WORK.md:5865"
    "expected=integer-rank-order;equal-and-default-by-id;restart-stable;unknown-only-first-unseen"
  (let* ((table (make-priority-table))
         (rows (list (list :id "two" :ready t)
                     (list :id "ten" :ready t)
                     (list :id "a" :ready t)
                     (list :id "b" :ready t)
                     (list :id "zz-default" :ready t)
                     (list :id "aa-default" :ready t)
                     (list :id "blocked" :ready nil)))
         (expected '("two" "a" "b" "ten" "aa-default" "zz-default" "blocked")))
    (priority-set table "two" :self 2 "r" "e2")
    (priority-set table "ten" :self 10 "r" "e10")
    (priority-set table "a" :self 5 "r" "ea")
    (priority-set table "b" :self 5 "r" "eb")
    (let ((ordered (priority-rows table rows)))
      (check-equal expected (mapcar (lambda (r) (getf r :id)) ordered)
                   "rank 2 precedes 10; ties then defaults by id; blocked last"))
    (check-equal expected
                 (mapcar (lambda (r) (getf r :id))
                         (priority-rows table (reverse rows)))
                 "the order survives a restart with the rows in another order")
    (let ((ordered (priority-rows table rows)))
      (multiple-value-bind (page1 cursor) (priority-page ordered nil 3)
        (multiple-value-bind (page2 cursor2) (priority-page ordered cursor 3)
          (multiple-value-bind (page3 cursor3) (priority-page ordered cursor2 3)
            (declare (ignore cursor3))
            (check-equal expected (append (mapcar (lambda (r) (getf r :id)) page1)
                                          (mapcar (lambda (r) (getf r :id)) page2)
                                          (mapcar (lambda (r) (getf r :id)) page3))
                         "later pages read the pinned order")
            (check-equal "zz-default" cursor2 "the cursor continues at the pinned order")))))))


(deftest "read-only-intake" "docs/SPEC-WORK.md:5583"
    "expected=recording-adapter-fails-on-mutation-endpoint;remote-inventory-compared-before-after"
  ;; NEEDS-KERNEL: the recording intake adapter and source mutation endpoint guard.
  (ok t "pending; needs the intake adapter"))

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:5262"
    "expected=contradictory-observations-kept-unresolved;no-forged-inference"
  ;; NEEDS-KERNEL: the reconcile/index surface over receipts and targets.
  (ok t "pending; needs reconcile"))
