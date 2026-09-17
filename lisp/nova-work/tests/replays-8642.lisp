;;;; replays-8642.lisp --- the five named acceptance replays of nova-tools #362.
;;;;
;;;; Each deftest is named exactly as docs/SPEC-WORK.md names it and asserts the
;;;; Required outcome its paragraph promises. The pure functions the tests call
;;;; live in src/replays-8642.lisp; this file is red before that file exists.
;;;;
;;;; The rows are the "Required enforcement replays" table
;;;; (docs/SPEC-WORK.md:4843-4856) for bounds-are-not-prompts,
;;;; batch-with-bounds-and-urgency and cache-aware-context-choice, and the
;;;; "Required suites" table (docs/SPEC-WORK.md:6226-6248) for
;;;; async-operations and batches-and-pipelines. The live session, CLI,
;;;; transport and provider wiring those rows also name is owed outside this
;;;; internal C/O kernel and is listed in RESULT.md.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; bounds-are-not-prompts                      SPEC-WORK.md:4849
;;; ------------------------------------------------------------------

(deftest "bounds-are-not-prompts" "docs/SPEC-WORK.md:4849"
    "expected=missing-required-limit=refused,deadline=terminal|unresolved,duplicates=0"
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
;;; batch-with-bounds-and-urgency                SPEC-WORK.md:4853
;;; ------------------------------------------------------------------

(deftest "batch-with-bounds-and-urgency" "docs/SPEC-WORK.md:4853"
    "expected=coalesce=within-bounds,unchanged=no-call,padding=refused,urgent=bypasses-delay,deps-retry=survive"
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
      (ok (assoc :unreferenced problems) "the refusal names the padding")))
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
;;; async-operations                            SPEC-WORK.md:6239
;;; ------------------------------------------------------------------

(deftest "async-operations" "docs/SPEC-WORK.md:6239"
    "expected=no-double-launch;no-false-cancellation-success"
  (let ((op '(:id "op-1" :kind :import :state :pending :attempts 0)))
    ;; A launch moves pending -> running and counts one attempt.
    (let ((running (operation-launch op)))
      (check-equal :running (getf running :state) "a pending op did not enter running")
      (check-equal 1 (getf running :attempts) "the launch was not counted"))
    ;; Launching an already-running op is no double launch: attempts unchanged.
    (let* ((running '(:id "op-1" :kind :import :state :running :attempts 1))
           (relaunched (operation-launch running)))
      (check-equal :running (getf relaunched :state) "state changed on a second launch")
      (check-equal 1 (getf relaunched :attempts) "a running op was launched twice"))
    ;; Cancelling a pending op is a real cancellation.
    (check-equal :cancelled
                 (getf (async-operation-cancel '(:id "o1" :kind :import :state :pending :attempts 0)) :state)
                 "cancel did not cancel a pending op")
    ;; Cancelling a finished op is never a false success: it reports already
    ;; complete and never claims a cancellation happened.
    (let ((done-cancel (async-operation-cancel '(:id "op-2" :kind :export :state :done :attempts 1))))
                 (getf (operation-cancel '(:id "o1" :kind :import :state :pending :attempts 0)) :state)
                 "cancel did not cancel a pending op")
    ;; Cancelling a finished op is never a false success: it reports already
    ;; complete and never claims a cancellation happened.
    (let ((done-cancel (operation-cancel '(:id "op-2" :kind :export :state :done :attempts 1))))
      (check-equal :already-complete (getf done-cancel :disposition)
                   "cancelling a finished op claimed a cancellation success")
      (check-equal :done (getf done-cancel :state)
                   "a finished op's state was falsified by cancellation"))))

;;; ------------------------------------------------------------------
;;; batches-and-pipelines                       SPEC-WORK.md:6240
;;; ------------------------------------------------------------------

(deftest "batches-and-pipelines" "docs/SPEC-WORK.md:6240"
    "expected=atomic=all-or-none,prefix=exact,unattempted=marked"
  (let ((entries '((:id "e1" :payload 1)
                   (:id "e2" :payload 2)
                   (:id "e3" :payload 3))))
    (flet ((reject-middle (entry) (not (equal (getf entry :id) "e2"))))
      ;; Atomic: all-or-none. One bad entry publishes nothing.
      (multiple-value-bind (acc outcome)
          (apply-atomic-batch '() entries #'reject-middle)
        (check-equal :refused outcome "an atomic batch with a bad entry published")
        (check-equal '() acc "an atomic batch applied a partial prefix"))
      ;; An all-valid atomic batch applies everything, in order.
      (multiple-value-bind (acc outcome)
          (apply-atomic-batch '() entries (lambda (e) (declare (ignore e)) t))
        (check-equal :applied outcome "an all-valid atomic batch refused")
        (check-equal '("e1" "e2" "e3") acc "the atomic batch did not apply in order"))
      ;; Independent: exact accepted prefix applied, remainder not attempted.
      (multiple-value-bind (acc accepted not-attempted)
          (apply-independent-batch '() entries #'reject-middle)
        (check-equal '("e1") acc "the independent batch did not stop at the refusal")
        (check-equal '("e1") accepted "the accepted prefix was not exact")
        (check-equal '("e3") not-attempted "the remainder was not marked not-attempted")))))

;;; ------------------------------------------------------------------
;;; cache-aware-context-choice                   SPEC-WORK.md:4854
;;; ------------------------------------------------------------------

(deftest "cache-aware-context-choice" "docs/SPEC-WORK.md:4854"
    "expected=cache-read-write-tier-threshold-priced-separately;reset-costs-refused-when-separate;lower-hit-can-win-on-cost"
  (let ((price (make-cache-price :input-rate 3 :cache-read-rate 1 :cache-write-rate 5)))
    ;; Cache reads and writes are never one charge.
    (check-equal 10 (cache-cost price 10 0) "cache reads priced at the read rate")
    (check-equal 50 (cache-cost price 0 10) "cache writes priced at the write rate")
    (check-equal 60 (cache-cost price 10 10) "a read and a write priced as one charge"))
  ;; A long-context tier threshold prices separately.
  (check-equal 600 (tiered-input-cost 150 100 1 10)
               "tokens over the threshold did not price at the long rate")
  (check-equal 400 (tiered-input-cost 100 100 4 8)
               "tokens under the threshold did not price at the base rate")
  ;; A reset includes its rebuild cost, which is charged, not assumed away.
  (let ((price (make-cache-price :input-rate 3 :cache-read-rate 1 :cache-write-rate 5))
        (without (make-context-plan :hit-rate 0.5 :retention-tokens 100
                                    :new-prefix-tokens 100 :rebuild-tokens 0))
        (with (make-context-plan :hit-rate 0.5 :retention-tokens 100
                                 :new-prefix-tokens 100 :rebuild-tokens 200)))
    (check-equal 1000 (- (plan-cost with price) (plan-cost without price))
                 "the reset did not include its rebuild cost"))
  ;; A lower hit rate can still win when total matched-work cost falls.
  (let ((price (make-cache-price :input-rate 3 :cache-read-rate 1 :cache-write-rate 5))
        (retained (make-context-plan :hit-rate 0.9 :retention-tokens 5000
                                     :new-prefix-tokens 0 :rebuild-tokens 0))
        (refreshed (make-context-plan :hit-rate 0.5 :retention-tokens 100
                                      :new-prefix-tokens 100 :rebuild-tokens 200)))
    (ok (< (context-plan-hit-rate refreshed) (context-plan-hit-rate retained))
        "the chosen plan did not have the lower hit rate")
    (check-equal refreshed (choose-context-plan retained refreshed price)
                 "the lower total cost did not win over the higher hit rate"))
  ;; A missing decision, adapter or evidence refuses the automatic refresh.
  (check-equal '(:adapter :evidence)
               (refresh-refusal (make-refresh-admission :decision t
                                                        :adapter nil :evidence nil))
               "the missing refresh inputs were not named")
  (ok (refresh-allowed-p (make-refresh-admission :decision t :adapter t :evidence t))
      "a fully evidenced automatic refresh was refused")
  (ok (not (refresh-allowed-p (make-refresh-admission :decision t :adapter t :evidence nil)))
      "a refresh with no evidence was allowed"))
