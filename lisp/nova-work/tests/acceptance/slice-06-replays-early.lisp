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

;; NEEDS-KERNEL: priority verbs and the grants-nothing invariant.
(deftest "priority-grants-nothing" "docs/SPEC-WORK.md:5421"
    "expected=who-unchanged;no-lease;no-bypass"
  (slice1-refuses-verb :priority))

;;; priority: the two slots, the nearest-context rank and the ready order
;;; (SPEC-WORK.md:3156-3193; replays at :5857 and :5861).

(deftest "priority-inherits-and-clears" "docs/SPEC-WORK.md:5410"
    "expected=order-only;no-lease-attempt-state-counter-moved"
  (let* ((leaf (priority-field))
         (root (priority-field :subtree 2))
         (mid (priority-field :subtree 5))
         (ancestors (list (cons "mid" mid) (cons "root" root))))
    ;; effective rank is the nearest :subtree on the containment path.
    (multiple-value-bind (rank context source) (effective-priority leaf ancestors)
      (check-equal 5 rank "the deepest subtree rank is the effective one")
      (check-equal :subtree context "the effective context is subtree")
      (check-equal "mid" source "the source is the nearest ancestor"))
    ;; a child's :self overrides the inherited rank.
    (multiple-value-bind (rank context source)
        (effective-priority (priority-field :self 1) ancestors)
      (check-equal 1 rank "a child's :self rank wins")
      (check-equal :self context "the overriding context is self"))
    ;; a clear reveals the parent.
    (multiple-value-bind (new changed) (priority-clear (priority-field :self 1) :self)
      (ok changed "clearing a set slot changes the field")
      (multiple-value-bind (rank context source) (effective-priority new ancestors)
        (declare (ignore context))
        (check-equal 5 rank "after a clear the parent's subtree rank is revealed")
        (check-equal "mid" source "the revealed source is the parent")))
    ;; a clear of an absent slot is the no-effect receipt.
    (multiple-value-bind (new changed) (priority-clear leaf :self)
      (declare (ignore new))
      (check-equal nil changed "a clear of an absent slot is a no-effect receipt"))
    ;; settle and reopen keep the slots.
    (let ((f (priority-field :self 3 :subtree 7)))
      (check-equal f (priority-settle f) "a settle keeps the slots")
      (check-equal f (priority-reopen f) "a reopen restores the slots"))
    ;; a move re-reads inheritance and clones no event.
    (multiple-value-bind (rank context source) (effective-priority leaf (list (cons "root" root)))
      (declare (ignore context))
      (check-equal 2 rank "a move re-reads the new path")
      (check-equal "root" source "the re-read source is the new ancestor"))
    ;; a root :subtree rank changes ready order with nothing else moved.
    (let* ((a (list :id "a" :priority (effective-priority leaf (list (cons "root" root)))))
           (b (list :id "b" :priority (effective-priority (priority-field :self 9))))
           (order (ready-order (list b a) nil :order :priority)))
      (check-equal '("a" "b") (mapcar (lambda (r) (getf r :id)) order)
                   "the inherited rank orders the ready rows")
      (ok (priority-only-moves-its-field-p
           (priority-event :node "mid" :change :set :context :subtree :rank 2 :reason "r"))
          "a priority event moves only its own field"))))

(deftest "priority-orders-only-the-eligible" "docs/SPEC-WORK.md:5406"
    "expected=blocked-rank-0-stays;rank-9-ready-first"
  (let* ((blocked (list :id "task-a" :priority 0 :state :blocked
                        :reason "awaits schema" :resolver "owner/b"))
         (ready-9 (list :id "task-b" :priority 9))
         (ready-default (list :id "task-c" :priority +absent+)))
    ;; discovery order leaves the eligible rows as given, blocked last.
    (check-equal '("task-c" "task-b" "task-a")
                 (mapcar (lambda (r) (getf r :id))
                         (ready-order (list ready-default ready-9) (list blocked)))
                 "discovery order leaves eligible arrival order, blocked last")
    ;; priority order: the rank-9 eligible row first, default next, blocked last.
    (let ((order (ready-order (list ready-default ready-9) (list blocked) :order :priority)))
      (check-equal '("task-b" "task-c" "task-a")
                   (mapcar (lambda (r) (getf r :id)) order)
                   "rank 9 eligible first, blocked rank-0 not dropped")
      (let ((b (find "task-a" order :key (lambda (r) (getf r :id)) :test #'equal)))
        (check-equal "awaits schema" (getf b :reason) "the blocked row keeps its reason")
        (check-equal "owner/b" (getf b :resolver) "the blocked row keeps its resolver"))))
  ;; --order priority is refused under every ask but ready, at exit 2.
  (multiple-value-bind (ok line code) (priority-order-refusal :done :priority)
    (check-equal nil ok "--order priority under done is refused")
    (check-equal 2 code "the refusal is exit 2")
    (ok (search "priority" line) "the refusal names the order: ~A" line))
  (multiple-value-bind (ok line code) (priority-order-refusal :ready :priority)
    (declare (ignore line))
    (ok ok "the ready ask admits --order priority")
    (check-equal 0 code "the ready ask exits 0"))
  ;; capacity loss, approval withdrawal, a dependency change or a hold is
  ;; rechecked before ranking and starts or interrupts nothing.
  (check-equal nil (priority-eligible-p (list :id "task-b" :priority 9)
                                        :capacity-p nil)
               "a row that lost capacity is not eligible")
  (check-equal nil (priority-eligible-p (list :id "task-b" :priority 9)
                                        :approval-p nil)
               "a row whose approval was withdrawn is not eligible")
  (check-equal nil (priority-eligible-p (list :id "task-b" :priority 9)
                                        :dependency-ok-p nil)
               "a row whose dependency moved is not eligible")
  (check-equal nil (priority-eligible-p (list :id "task-b" :priority 9) :held-p t)
               "a held row is not eligible")
  (ok (priority-starts-nothing-p) "priority selects no worker and starts nothing"))

;; NEEDS-KERNEL: priority undo treated as history, not value.
(deftest "priority-undo-is-history-not-value" "docs/SPEC-WORK.md:5418"
    "expected=same-value-set-and-clear-of-absent-slot-are-no-effect"
  (slice1-refuses-verb :undo))

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

(deftest "rank-2-precedes-10" "docs/SPEC-WORK.md:5414"
    "expected=integer-rank-order;equal-and-default-by-id;restart-stable;unknown-only-first-unseen"
  ;; NEEDS-KERNEL: priority rank slots and history-pinned ordering/pagination.
  (ok t "pending; needs priority ranks and the ready cursor"))


(deftest "read-only-intake" "docs/SPEC-WORK.md:5583"
    "expected=recording-adapter-fails-on-mutation-endpoint;remote-inventory-compared-before-after"
  ;; NEEDS-KERNEL: the recording intake adapter and source mutation endpoint guard.
  (ok t "pending; needs the intake adapter"))

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:5262"
    "expected=contradictory-observations-kept-unresolved;no-forged-inference"
  ;; NEEDS-KERNEL: the reconcile/index surface over receipts and targets.
  (ok t "pending; needs reconcile"))
