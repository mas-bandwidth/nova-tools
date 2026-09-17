;;;; slice-06-replays-early.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;; one-stop-note-cannot-cancel-two-attempts now lives in slice-09-replays-holds.lisp.

;; operation-survives-the-client is now the executable replay in
;; ../acceptance.lisp (card 8608).

;; NEEDS-KERNEL: oversized packet, stale route and escalation gates before dispatch.
(deftest "packet-and-route-gates" "docs/SPEC-WORK.md:4371"
    "expected=oversized/stale/unexplained-escalation-refuse-before-dispatch"
  (slice1-refuses-verb :route))

;; page-budget-is-not-max now lives in ../acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).

;; pipeline-replies-are-correlated is now the executable replay in
;; ../acceptance.lisp (card 8608).

;; policy-round-trip-and-replay and pricing-is-pinned-by-revision now run in
;; lisp/nova-work/tests/replays-8647.lisp (nova-tools #362).

(deftest "priority-grants-nothing" "docs/SPEC-WORK.md:5872"
    "expected=who-unchanged;no-lease;no-bypass"
  (let* ((view (make-work-view :who "rowan" :lease nil :worker nil :approval :pending))
         (table (make-priority-table))
         (ids (mapcar (lambda (n) (getf n :id)) *seed*)))
    (dolist (id ids)
      (multiple-value-bind (receipt line code)
          (priority-table-set table id :self 7 "ordering only" (format nil "ev-~A" id))
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

(deftest "priority-undo-is-history-not-value" "docs/SPEC-WORK.md:5869"
    "expected=same-value-set-and-clear-of-absent-slot-are-no-effect"
  ;; undo is now implemented; redo is still outside the slice.
  (slice1-refuses-verb :redo)
  (let ((table (make-priority-table)))
    (multiple-value-bind (receipt line code) (priority-table-set table "n" :self 2 "first" "ev-1")
      (declare (ignore line))
      (check-equal 0 code "the first set")
      (check-equal 1 (getf receipt :changed) "the first set did not move the slot"))
    (multiple-value-bind (receipt line code) (priority-table-set table "n" :self 2 "same" "ev-2")
      (declare (ignore line))
      (check-equal 0 code "the same-value set")
      (check-equal 0 (getf receipt :changed) "a same-value set is not the no-effect receipt")
      (ok (getf receipt :no-effect) "a same-value set is marked no-effect"))
    (multiple-value-bind (receipt line code) (priority-table-clear table "other" :self "absent" "ev-3")
      (declare (ignore line))
      (check-equal 0 code "the clear of an absent slot")
      (check-equal 0 (getf receipt :changed)
                   "a clear of an absent slot is not the no-effect receipt"))
    (priority-table-set table "n" :self 9 "second" "ev-4")
    (priority-table-set table "n" :self 2 "third" "ev-5")
    (multiple-value-bind (okp line code) (priority-undo table "n" :self "ev-1")
      (declare (ignore line))
      (ok (not okp) "undo of the first set was admitted although the value matches")
      (check-equal 2 code "undo-of-first exit code"))
    (multiple-value-bind (okp line code) (priority-undo table "n" :self "ev-5")
      (declare (ignore line))
      (ok okp "the latest change does not undo")
      (check-equal 0 code "undo-of-latest exit code"))))

;; protocol-version-negotiated-or-refused is now the executable replay in
;; ../acceptance.lisp (card 8608).

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
    (priority-table-set table "two" :self 2 "r" "e2")
    (priority-table-set table "ten" :self 10 "r" "e10")
    (priority-table-set table "a" :self 5 "r" "ea")
    (priority-table-set table "b" :self 5 "r" "eb")
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


;; quiet-until-actionable and read-only-intake now run in
;; lisp/nova-work/tests/replays-8647.lisp (nova-tools #362).

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:5262"
    "expected=contradictory-observations-kept-unresolved;no-forged-inference"
  ;; NEEDS-KERNEL: the reconcile/index surface over receipts and targets.
  (ok t "pending; needs reconcile"))
