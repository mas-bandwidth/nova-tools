;;;; replays-8644.lisp --- the five acceptance replays of card 8644, each named
;;;; as docs/SPEC-WORK.md names it and carrying the Required outcome the spec
;;;; paragraph promises.
;;;;
;;;;   efficiency-lessons-gate          SPEC-WORK.md:4916
;;;;   evidence-before-adoption         SPEC-WORK.md:4855
;;;;   gas-town-efficiency-accounting   SPEC-WORK.md:4915
;;;;   goal-crosses-harness             SPEC-WORK.md:5938
;;;;   goal-expect-is-required          SPEC-WORK.md:5975
;;;;
;;;; The pure model these test is src/replays-efficiency-goal.lisp. The live
;;;; session, CLI, dispatch, snapshot and lease wiring a later slice owns is
;;;; not in this file.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; efficiency-lessons-gate                  SPEC-WORK.md:4916
;;; ------------------------------------------------------------------

(deftest "efficiency-lessons-gate" "docs/SPEC-WORK.md:4916"
    "expected=max-bytes-respected;unpoured!=O;tripped=reason;no-effort=refused;ceiling=refused"
  ;; prime is a read-only projection bounded under --max-bytes.
  (let* ((projection (lambda (max-bytes)
                       (prime :goal "acme/work/f1/t1"
                              :notes (make-list 200 :initial-element "note")
                              :leases (make-list 5 :initial-element "lease")
                              :stop-requests (make-list 2 :initial-element "stop")
                              :max-bytes max-bytes)))
         (full (funcall projection 100000))
         (cut (funcall projection 40)))
    (ok (> (length full) 40) "the full projection exceeds the bound: ~D bytes" (length full))
    (ok (<= (length cut) 40) "prime respects --max-bytes: ~D bytes" (length cut))
    (check-equal cut (funcall projection 40)
                 "prime creates no shadow state and prints the same bytes"))
  ;; unpoured checklist items never count in |O|.
  (let* ((poured (make-checklist-item "verify the result"
                                      :independent-verification t))
         (unpoured (make-checklist-item "note the result"))
         (k (fresh))
         (before (state-open-count (kernel-state k))))
    (check-equal before (+ before (checklist-open-delta (list unpoured)))
                 "an unpoured checklist item adds nothing to |O|")
    (check-equal (+ before 1) (+ before (checklist-open-delta (list unpoured poured)))
                 "a poured item adds exactly one to |O|"))
  ;; a tripped node requires an explicit --reason for a lease.
  (ok (attempts-tripped-p 3) "three attempts trip the default bound")
  (multiple-value-bind (admitted line code) (lease-admission 3)
    (check-equal nil admitted "a tripped node without --reason refuses a lease")
    (check-equal 2 code "the tripped refusal is exit 2")
    (ok (search "--reason" line) "the refusal names --reason: ~A" line))
  (multiple-value-bind (admitted line code)
      (lease-admission 3 :reason "operator accepted the tripped node")
    (declare (ignore line))
    (check-equal t admitted "a tripped node with --reason admits the lease")
    (check-equal 0 code "the reasoned lease is exit 0"))
  ;; delegate mode refuses edits and builds below the model.
  (dolist (action '(:edit :build))
    (multiple-value-bind (admitted line code) (delegate-admission :delegate action)
      (check-equal nil admitted (format nil "delegate mode refuses ~A" action))
      (check-equal 2 code "the delegate refusal is exit 2")
      (ok (search "below the model" line) "the refusal names the boundary: ~A" line)))
  (multiple-value-bind (admitted line code)
      (delegate-admission :delegate :read-only-git)
    (declare (ignore line))
    (check-equal t admitted "delegate mode admits a read-only git operation")
    (check-equal 0 code "the read-only operation is exit 0"))
  ;; packets lacking :effort and over-ceiling dispatches are refused.
  (let ((complete '(:objective "ship the task" :source-revision "abc123"
                    :criteria ("acceptance criterion 1") :scope (:node)
                    :result-contract "done" :checkpoint "cp-1" :effort 3)))
    (let ((no-effort (loop for (k v) on complete by #'cddr
                           unless (eq k :effort) append (list k v))))
      (multiple-value-bind (admitted line code) (delegation-admission no-effort)
        (check-equal nil admitted "a packet lacking :effort is refused")
        (check-equal 2 code "the gate-2 refusal is exit 2")
        (ok (search "effort" line) "the refusal names :effort: ~A" line)))
    (multiple-value-bind (admitted line code)
        (delegation-admission complete :dispatch-cost 40 :spent-today 70 :ceiling 100)
      (check-equal nil admitted "a dispatch crossing the daily ceiling is refused")
      (check-equal 2 code "the gate-3 refusal is exit 2")
      (ok (search "ceiling" line) "the refusal names the ceiling: ~A" line))))

;;; ------------------------------------------------------------------
;;; evidence-before-adoption                 SPEC-WORK.md:4855
;;; ------------------------------------------------------------------

(deftest "evidence-before-adoption" "docs/SPEC-WORK.md:4855"
    "expected=missing-baseline=no-promote;prospective-qualified=promote"
  ;; a fully qualified prospective result can auto-promote.
  (multiple-value-bind (admitted line verdict)
      (adoption-verdict (make-trial-result :prospective t :baseline t :coverage t
                                           :quality t :tolerances-matched t))
    (declare (ignore line))
    (check-equal t admitted "a fully qualified prospective result is promoted")
    (check-equal :promote verdict "the qualified verdict is promote"))
  ;; a missing baseline cannot.
  (multiple-value-bind (admitted line verdict)
      (adoption-verdict (make-trial-result :prospective t :baseline nil :coverage t
                                           :quality t :tolerances-matched t))
    (declare (ignore verdict))
    (check-equal nil admitted "a missing baseline cannot auto-promote")
    (ok (search "baseline" line) "the refusal names the baseline: ~A" line))
  ;; missing coverage cannot.
  (multiple-value-bind (admitted line verdict)
      (adoption-verdict (make-trial-result :prospective t :baseline t :coverage nil
                                           :quality t :tolerances-matched t))
    (declare (ignore verdict))
    (check-equal nil admitted "missing coverage cannot auto-promote")
    (ok (search "coverage" line) "the refusal names coverage: ~A" line))
  ;; unmatched quality cannot.
  (multiple-value-bind (admitted line verdict)
      (adoption-verdict (make-trial-result :prospective t :baseline t :coverage t
                                           :quality nil :tolerances-matched t))
    (declare (ignore verdict))
    (check-equal nil admitted "unmatched quality cannot auto-promote")
    (ok (search "quality" line) "the refusal names quality: ~A" line))
  ;; a retrospective correlation alone cannot.
  (multiple-value-bind (admitted line verdict)
      (adoption-verdict (make-trial-result :prospective nil :baseline t :coverage t
                                           :quality t :tolerances-matched t))
    (declare (ignore verdict))
    (check-equal nil admitted "a retrospective correlation alone cannot auto-promote")
    (ok (search "retrospective" line) "the refusal names the retrospective trial: ~A" line)))

;;; ------------------------------------------------------------------
;;; gas-town-efficiency-accounting           SPEC-WORK.md:4915
;;; ------------------------------------------------------------------

(deftest "gas-town-efficiency-accounting" "docs/SPEC-WORK.md:4915"
    "expected=node-explosion=0;empty-pulse-reexecutions=0"
  ;; root-only step records and inline checklists avoid node explosion.
  (let* ((steps '("read the task" "change the code" "run the tests" "record the receipt"))
         (record (root-step-record "acme/work/f1/t1" steps)))
    (check-equal 1 (step-record-count (list record)) "one root-only step record")
    (check-equal "acme/work/f1/t1" (root-step-record-node record)
                 "the record is rooted at the task")
    (check-equal 4 (root-step-record-steps record) "the steps stay inline")
    (check-equal 0 (step-record-node-explosion (list record))
                 "the step records create no O nodes"))
  ;; durable next-triggers: an empty pulse causes zero model re-executions.
  (let ((idle (list (make-waiting-item
                     :id "owner-delivery"
                     :trigger (make-next-trigger :kind :owner-delivery :due nil))
                    (make-waiting-item
                     :id "review"
                     :trigger (make-next-trigger :kind :review-completion :due nil))))
        (due (list (make-waiting-item
                    :id "checkpoint"
                    :trigger (make-next-trigger :kind :due-checkpoint :due t)))))
    (check-equal 0 (pulse-reexecutions idle) "an empty pulse re-executes no model")
    (check-equal 1 (pulse-reexecutions due) "only a due trigger re-executes")))

;;; ------------------------------------------------------------------
;;; goal-crosses-harness                     SPEC-WORK.md:5938
;;; ------------------------------------------------------------------

(deftest "goal-crosses-harness" "docs/SPEC-WORK.md:5938"
    "expected=goal=G;stop=requested;note-byte-for-byte;progress=refused;stop=cancelled"
  (let ((store (make-goal-store :scope "C" :node-state :doing :rev 10)))
    ;; Harness A, as coordinator C, sets the goal at revision r.
    (multiple-value-bind (admitted line code)
        (goal-set store :goal "G" :expect 10 :as "C" :reason "start G")
      (ok admitted "A's goal set is admitted: ~A" line)
      (check-equal 0 code "goal set is exit 0")
      (ok (search "rev=11" line) "A's set reports rev=r+1: ~A" line))
    ;; A writes a (:coordinator "C") note with a :deny on a made-up model for :coding.
    (let ((note (make-note :scope '(:coordinator "C") :author "A"
                           :date "2026-09-16T00:00:00Z" :source "harness A"
                           :kind :instruction :text "no made-up model for coding"
                           :constraint '(:constraint
                                         (:deny (:model "made-up") (:task-class :coding))
                                         (:prefer ())
                                         (:reason "A's routing instruction")))))
      (goal-add-note store note)
      ;; A requests a stop on G.
      (multiple-value-bind (admitted line code)
          (goal-update store :stop t :reason "operator stop" :expect 11 :as "C")
        (ok admitted "A's stop request is admitted: ~A" line)
        (check-equal 0 code "the stop request is exit 0"))
      ;; Harness B, another build, shows against the live session and the
      ;; clipped snapshot. B copied no conversation; the answer is a field.
      (let ((snapshot (snapshot-goal-store store)))
        (multiple-value-bind (live live-line) (goal-show store :as "C")
          (multiple-value-bind (clip clip-line) (goal-show snapshot :as "C")
            (declare (ignore clip-line))
            (check-equal "G" (getf live :goal) "show prints goal=G")
            (check-equal :requested (getf live :stop)
                         "show prints stop=requested, not cancelled")
            (ok (>= (getf live :rev) 12)
                "rev= is at or after every write of A's: ~D" (getf live :rev))
            (check-equal (getf live :goal) (getf clip :goal)
                         "the clipped snapshot prints the same goal")
            (check-equal (getf live :rev) (getf clip :rev)
                         "the clipped snapshot answers at the same revision")
            (check-equal (getf live :stop) (getf clip :stop)
                         "the clipped snapshot prints the same stop")
            (check-equal (getf live :notes) (getf clip :notes)
                         "B reads the same constraint rows byte for byte")
            (check-equal (getf live :constraints) 1
                         "the constraint row A wrote is present"))
          ;; B's goal update --progress is refused `stop requested`, nothing written.
          (let ((before (goal-store-rev store)))
            (multiple-value-bind (admitted line code)
                (goal-update store :progress "still working" :expect 12 :as "C")
              (check-equal nil admitted "B's progress on a stopping node is refused")
              (check-equal 1 code "the refusal is exit 1")
              (ok (search "stop requested" line)
                  "the refusal names stop requested: ~A" line)
              (check-equal before (goal-store-rev store) "nothing is written"))))
        ;; A writes event --kind cancel --evidence; B's next show prints stop=cancelled.
        (multiple-value-bind (admitted line code)
            (goal-cancel store :evidence "ev-worker-stopped")
          (ok admitted "A's cancel evidence is admitted: ~A" line)
          (check-equal 0 code "the cancel is exit 0"))
        (multiple-value-bind (live2 line2) (goal-show store :as "C")
          (declare (ignore line2))
          (check-equal :cancelled (getf live2 :stop)
                       "B's next show prints stop=cancelled"))))))

;;; ------------------------------------------------------------------
;;; goal-expect-is-required                  SPEC-WORK.md:5975
;;; ------------------------------------------------------------------

(deftest "goal-expect-is-required" "docs/SPEC-WORK.md:5975"
    "expected=no-expect=exit-2;one-behind=stale;dry-run-writes-nothing"
  (let ((store (make-goal-store :scope "C" :node-state :doing :rev 5)))
    ;; goal set and goal update without --expect exit 2 naming the flag, nothing written.
    (multiple-value-bind (admitted line code) (goal-set store :goal "G" :as "C")
      (check-equal nil admitted "goal set without --expect is refused")
      (check-equal 2 code "the omission is exit 2")
      (ok (search "--expect" line) "the refusal names --expect: ~A" line))
    (multiple-value-bind (admitted line code)
        (goal-update store :progress "text" :as "C")
      (check-equal nil admitted "goal update without --expect is refused")
      (check-equal 2 code "the omission is exit 2")
      (ok (search "--expect" line) "the refusal names --expect: ~A" line))
    (check-equal 5 (goal-store-rev store) "neither omission wrote a revision")
    (check-equal 0 (hash-table-count (goal-store-dedup store)) "neither omission wrote dedup")
    ;; with --expect at the current local revision, admitted.
    (multiple-value-bind (admitted line code)
        (goal-set store :goal "G" :expect 5 :as "C" :request "r1")
      (ok admitted "an in-revision goal set is admitted: ~A" line)
      (check-equal 0 code "the in-revision set is exit 0"))
    (check-equal 6 (goal-store-rev store) "the admitted write moves the revision")
    (check-equal 1 (hash-table-count (goal-store-dedup store)) "the admitted write records dedup")
    ;; with --expect one behind, refused stale at exit 1 with the current value printed.
    (multiple-value-bind (admitted line code)
        (goal-update store :stop t :reason "stop" :expect 5 :as "C")
      (check-equal nil admitted "a one-behind expectation is refused")
      (check-equal 1 code "the stale refusal is exit 1")
      (ok (search "stale" line) "the refusal names stale: ~A" line)
      (ok (search "current=6" line) "the refusal prints the current value: ~A" line))
    (check-equal 6 (goal-store-rev store) "the stale write moved nothing")
    ;; --dry-run with the stale expectation prints the same refusal and writes
    ;; no event, no journal revision and no dedup entry.
    (let ((history-before (length (goal-store-history store)))
          (dedup-before (hash-table-count (goal-store-dedup store)))
          (rev-before (goal-store-rev store)))
      (multiple-value-bind (admitted line code)
          (goal-set store :goal "H" :expect 5 :as "C" :dry-run t :request "r2")
        (check-equal nil admitted "a dry-run with a stale expectation is refused")
        (check-equal 1 code "the dry-run refusal keeps exit 1")
        (ok (search "stale" line) "the dry-run prints the same refusal: ~A" line))
      (check-equal rev-before (goal-store-rev store) "the dry-run writes no revision")
      (check-equal history-before (length (goal-store-history store))
                   "the dry-run writes no event")
      (check-equal dedup-before (hash-table-count (goal-store-dedup store))
                   "the dry-run writes no dedup entry"))
    ;; a show never takes --expect and answers at the revision it prints.
    (multiple-value-bind (answer line code) (goal-show store :as "C")
      (check-equal 6 (getf answer :rev) "show answers at the revision it prints")
      (check-equal 0 code "show is exit 0")
      (ok (search "rev=6" line) "show prints its revision: ~A" line))
    (multiple-value-bind (answer line code) (goal-show store :as "C" :expect 6)
      (declare (ignore answer))
      (check-equal 2 code "show refuses --expect")
      (ok (search "--expect" line) "show names --expect as not a show flag: ~A" line))))

;;; ------------------------------------------------------------------
;;; represent-leaf-tasks-separately-from-par...  docs/SPEC-WORK.md:945
;;; ------------------------------------------------------------------
;;; E01-F04 (ROADMAP.md:235) — represent leaf tasks separately from
;;; parent tasks and attempts. docs/SPEC-WORK.md:888 makes features,
;;; tasks, attempts and leaf subtasks distinct units; :945-947 states
;;; the rule "a task with no :children is a leaf subtask ... a task with
;;; children is counted by its leaves, never itself; so the four units ...
;;; are :feature, :task, the leaf :task, and the :attempt event".

(deftest "represent-leaf-tasks-separately-from-parent-and-attempts"
    "docs/SPEC-WORK.md:945"
    "expected=parent-has-children;leaf-has-none;parent-not-counted-as-leaf;attempt-is-an-event-field"
  (let ((state (make-seed-state
                '((:id "root"       :type :work-set :parent nil        :state :unknown)
                  (:id "root/f"     :type :feature  :parent "root"     :state :unknown)
                  (:id "root/f/p"   :type :task     :parent "root/f"   :state :doing)
                  (:id "root/f/p/l" :type :task     :parent "root/f/p" :state :doing)
                  (:id "root/f/l2"  :type :task     :parent "root/f"   :state :doing)))))
    ;; A parent task carries children; a leaf task carries none.
    (check-equal (list "root/f/p/l") (node-children state "root/f/p")
                 "a parent task keeps its children, not an empty children list")
    (check-equal nil (node-children state "root/f/p/l")
                 "a leaf task is represented with no children")
    ;; The feature and the parent task are distinct kinds from the leaf task's
    ;; :task; every node has one type read back from the model.
    (check-equal :feature (node-type state "root/f")
                 "the container is a :feature, never inferred from a title")
    (check-equal :task (node-type state "root/f/p")
                 "the parent task reads back as :task")
    (check-equal :task (node-type state "root/f/p/l")
                 "the leaf task reads back as :task")
    ;; The leaf rollup counts a parent task by its leaves, never itself: the
    ;; one feature folds to the two leaf tasks, not to root/f/p.
    (multiple-value-bind (done total unknown)
        (nova-work::%percent-leaf-counts state "root/f")
      (check-equal 2 total "the feature counts its two leaves, not its parent task")
      (check-equal 0 done "no leaf is settled in this seed")
      (check-equal 0 unknown "the doing leaves are not unknown"))
    ;; An attempt is a field on an event, a unit of its own and not a node kind:
    ;; an :evidence event names its :attempt apart from the :task node it addresses.
    (let* ((ev (make-work-event
                :kind :evidence :node "root/f/p/l" :by "rowan"
                :fields (list :pointer "p1" :criterion "c1" :against "a1"
                              :generation "g4" :attempt "att-1")
                :stamp "2026-09-20T00:00:00Z" :clock :tool :request "r1"
                :generation-owner "g4" :rev 1))
           (fields (work-event-fields ev)))
      (check-equal "att-1" (getf fields :attempt)
                   "the evidence event carries its own :attempt, separate from the node")
      (check-equal "root/f/p/l" (work-event-node ev)
                   "the attempt's event addresses the leaf task, never replaces it"))))
