;;;; replays-enforcement.lisp --- the five enforcement acceptance replays.
;;;;
;;;; Each deftest names the row of docs/SPEC-WORK.md's "Required enforcement
;;;; replays" tables it comes from, and asserts the Required outcome that row
;;;; states. The pure functions these call live in src/replays-enforcement.lisp;
;;;; this file is red before that file exists.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; bounds-are-not-prompts                      SPEC-WORK.md:4702
;;; ------------------------------------------------------------------

(deftest "bounds-are-not-prompts" "docs/SPEC-WORK.md:4702"
    "missing-required-limit=refused,deadline=terminal|unresolved,duplicates=0"
  ;; A launcher lacking a required hard limit refuses automatic dispatch.
  (let ((short (make-launcher :input-bound t :output-bound nil
                              :deadline nil :attempt-limit t)))
    (ok (not (auto-dispatch-allowed-p short
              '(:input-bound :output-bound :deadline :attempt-limit)))
        "a launcher missing a required hard limit refuses auto dispatch")
    (check-equal '(:output-bound :deadline)
                 (missing-hard-limits short
                   '(:input-bound :output-bound :deadline :attempt-limit))
                 "the missing hard limits are named, not guessed")
    (ok (auto-dispatch-allowed-p
         (make-launcher :input-bound t :output-bound t
                        :deadline t :attempt-limit t)
         '(:input-bound :output-bound :deadline :attempt-limit))
        "a launcher with every required limit may auto dispatch"))
  ;; A supported deadline returns a terminal or unresolved handle.
  (check-equal :terminal (deadline-handle t t)
               "supported, passed deadline is terminal")
  (check-equal :unresolved (deadline-handle t nil)
               "supported, future deadline is an unresolved handle")
  (check-equal :unresolved (deadline-handle nil nil)
               "unsupported deadline stays unresolved, never a guessed terminal")
  ;; No duplicate execution around an unresolved handle.
  (multiple-value-bind (h1 started1) (admit-execution "req-1" '())
    (ok started1 "first dispatch starts")
    (let ((active (list (cons "req-1" :unresolved))))
      (multiple-value-bind (h2 started2) (admit-execution "req-1" active)
        (ok (equal h1 h2) "the same request rejoins the same handle")
        (ok (not started2) "an unresolved handle is joined, never re-executed"))))
  (multiple-value-bind (h3 started3) (admit-execution "req-2" '())
    (declare (ignore h3))
    (ok started3 "a distinct request still starts exactly once")))

;;; ------------------------------------------------------------------
;;; batch-with-bounds-and-urgency                SPEC-WORK.md:4706
;;; ------------------------------------------------------------------

(deftest "batch-with-bounds-and-urgency" "docs/SPEC-WORK.md:4706"
    "coalesce=within-bounds,unchanged=no-call,padding=refused,urgent=bypasses-delay,deps-retry=survive"
  ;; Independent results coalesce within byte/record bounds.
  (let ((cfg (make-batch-config :max-bytes 10 :max-records 2 :max-delay-ms 500)))
    (ok (within-bounds-p (list (make-packet-fragment :id "a" :bytes 4)) cfg)
        "inside the byte and record bounds is admitted")
    (ok (not (within-bounds-p
              (list (make-packet-fragment :id "a" :bytes 6)
                    (make-packet-fragment :id "b" :bytes 6)) cfg))
        "over the byte bound is refused")
    (check-equal '(:bytes)
                 (exhausted-bound
                  (list (make-packet-fragment :id "a" :bytes 6)
                        (make-packet-fragment :id "b" :bytes 6)) cfg)
                 "the byte bound is the one named")
    (check-equal '(:records)
                 (exhausted-bound
                  (list (make-packet-fragment :id "a" :bytes 1)
                        (make-packet-fragment :id "b" :bytes 1)
                        (make-packet-fragment :id "c" :bytes 1)) cfg)
                 "the record bound is the one named"))
  ;; Unchanged batches cause no call.
  (ok (batch-unchanged-p
       (list (make-packet-fragment :id "a" :bytes 1 :kind :normal))
       (list (make-packet-fragment :id "a" :bytes 1 :kind :normal)))
      "an unchanged batch is detected with no model call")
  ;; Unreferenced padding refuses.
  (let ((pkt (make-packet
              :manifest '(("a" (4 "d1")))
              :fragments (list (make-packet-fragment :id "a" :bytes 4)
                               (make-packet-fragment :id "pad" :bytes 3)))))
    (let ((problems (validate-packet pkt
                                     (make-batch-config :max-bytes 100 :max-records 10))))
      (ok problems "unreferenced padding is refused")
      (ok (assoc :unreferenced problems) "the refusal names the padding")) )
  ;; Urgent corrections bypass the delay bound.
  (let ((urgent (make-packet-fragment :id "u" :bytes 1 :kind :urgent-correction))
        (normal (make-packet-fragment :id "n" :bytes 1 :kind :normal)))
    (ok (not (delay-applies-p urgent)) "an urgent correction bypasses the delay bound")
    (ok (delay-applies-p normal) "a normal fragment stays inside the delay bound"))
  ;; Dependencies survive coalescing.
  (check-equal '(("a" ("b")) ("c" ("d" "e")))
               (dependencies-of
                (list (make-packet-fragment :id "a" :dependencies '("b"))
                      (make-packet-fragment :id "c" :dependencies '("d" "e"))))
               "dependent work still waits for its prerequisite")
  ;; Partial retry identities survive.
  (check-equal '(("a" "parent-1") ("b" nil))
               (retry-identity-of
                (list (make-packet-fragment :id "a" :retry-of "parent-1")
                      (make-packet-fragment :id "b")))
               "a partial retry keeps its parent identity"))

;;; ------------------------------------------------------------------
;;; evidence-before-adoption                    SPEC-WORK.md:4708
;;; ------------------------------------------------------------------

(deftest "evidence-before-adoption" "docs/SPEC-WORK.md:4708"
    "missing-baseline|coverage|quality|retrospective=no-promote,qualified-prospective=promote"
  (ok (not (auto-promotion-supported-p
            (make-adoption-claim :baseline nil :coverage-complete t
                                 :quality-match t :prospective t)))
      "missing baseline cannot auto-promote")
  (ok (not (auto-promotion-supported-p
            (make-adoption-claim :baseline t :coverage-complete nil
                                 :quality-match t :prospective t)))
      "incomplete coverage cannot auto-promote")
  (ok (not (auto-promotion-supported-p
            (make-adoption-claim :baseline t :coverage-complete t
                                 :quality-match nil :prospective t)))
      "unmatched quality cannot auto-promote")
  (ok (not (auto-promotion-supported-p
            (make-adoption-claim :baseline t :coverage-complete t
                                 :quality-match t :prospective nil
                                 :retrospective-only t)))
      "a retrospective correlation alone cannot auto-promote")
  (ok (auto-promotion-supported-p
       (make-adoption-claim :baseline t :coverage-complete t
                            :quality-match t :prospective t))
      "a fully qualified prospective result can auto-promote"))

;;; ------------------------------------------------------------------
;;; gas-town-efficiency-accounting              SPEC-WORK.md:4768
;;; ------------------------------------------------------------------

(deftest "gas-town-efficiency-accounting" "docs/SPEC-WORK.md:4768"
    "root-only=1-row,inline-checklist=no-node-explosion,durable-trigger=empty-pulse-zero-reexecution"
  ;; Root-only step records avoid node explosion.
  (check-equal 1 (root-only-record-count
                  (list (make-step-record :node "root" :depth 0)))
               "one root-only step record")
  (check-equal 15 (fine-grained-record-count
                   (loop for i below 15
                         collect (make-step-record
                                  :node (format nil "n~D" i) :depth 1)))
               "fine-grained event emission is fifteen rows")
  ;; Inline checklists materialise no child node.
  (check-equal 0 (materialized-child-count
                  '((:id "c1") (:id "c2") (:id "c3")))
               "inline checklist steps materialise zero nodes")
  (check-equal 1 (materialized-child-count
                  '((:id "c1" :reason :dependency-edge)))
               "a dependency edge is the one pour that materialises a child")
  ;; Durable next-triggers ensure empty pulses re-execute nothing.
  (let ((durable (list (make-waiting-item :id "w" :next-trigger :job-handle)))
        (polling (list (make-waiting-item :id "w" :next-trigger nil))))
    (ok (every #'durable-next-trigger-p durable)
        "every waiting item holds a durable next-trigger")
    (check-equal 0 (model-reexecutions durable nil)
                 "an empty pulse with durable triggers reruns nothing")
    (check-equal 1 (model-reexecutions polling nil)
                 "without a durable trigger an empty pulse re-runs the waiting item")) )

;;; ------------------------------------------------------------------
;;; efficiency-lessons-gate                     SPEC-WORK.md:4769
;;; ------------------------------------------------------------------

(deftest "efficiency-lessons-gate" "docs/SPEC-WORK.md:4769"
    "prime=max-bytes,unpoured-not-in-O,tripped-needs-reason,delegate-refuses-edit,effort-required,spend-ceiling=refused"
  ;; prime read-only projection respects --max-bytes.
  (ok (projection-within-bytes-p 500 1000) "projection under --max-bytes is served")
  (ok (not (projection-within-bytes-p 1500 1000)) "projection over --max-bytes is refused")
  ;; Unpoured checklist items never count in |O|.
  (check-equal 2 (open-count-excluding-unpoured
                  '((:id "a" :materialized t :open t)
                    (:id "b" :materialized t :open t)
                    (:id "c" :materialized nil :open t)
                    (:id "d" :materialized nil :open t)))
               "unpoured checklist items never count in |O|")
  ;; Tripped nodes require --reason.
  (ok (not (tripped-lease-allowed-p t nil)) "a tripped node without --reason is refused")
  (ok (tripped-lease-allowed-p t "recover after failure") "a tripped node with --reason is allowed")
  (ok (tripped-lease-allowed-p nil nil) "an untripped node needs no reason")
  ;; Delegate mode refuses edits below the model.
  (ok (not (delegate-verb-allowed-p :delegate :file-edit t)) "delegate mode refuses edits")
  (ok (delegate-verb-allowed-p :delegate :query nil) "delegate mode admits read-only verbs")
  (ok (delegate-verb-allowed-p :coordinator :file-edit t) "a non-delegate may edit")
  ;; Packets lacking :effort are refused.
  (ok (not (packet-effort-present-p '(:objective "o"))) "a packet without :effort is refused")
  (ok (packet-effort-present-p '(:objective "o" :effort 3)) "a packet carrying :effort is admitted")
  ;; Dispatches crossing the daily fleet spend ceiling are refused.
  (let ((r (spend-ceiling-refusal 40 80 100)))
    (ok r "a dispatch crossing the ceiling is refused")
    (ok (= 100 (getf r :ceiling)) "the refusal names the configured ceiling")
    (ok (= 80 (getf r :local-spend)) "the refusal names the bench's own local spend"))
  (ok (null (spend-ceiling-refusal 10 80 100)) "a dispatch under the ceiling is admitted"))
