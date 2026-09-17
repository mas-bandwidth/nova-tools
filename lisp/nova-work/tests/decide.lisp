;;;; decide.lisp (tests) --- the slice-2 red test of docs/SPEC-DECIDE.md.
;;;;
;;;; The named case is the Lisp-kernel red test of SPEC-DECIDE.md:185-187:
;;;; a decision journaled with the in-process fake present replays with the
;;;; fake ABSENT, reads the same answer back from the event's question hash,
;;;; and never makes a fresh provider call. The kernel's decisive order is
;;;; bit-for-bit identical before and after: a decision advises, the machinery
;;;; decides (rules 5, 6 and 9).

(in-package #:nova-work/tests)

(defun decide-question ()
  "One bounded choice question (SPEC-DECIDE rule 1)."
  (list :kind :choice
        :question "Which ready node should the kernel take first?"
        :options '("ship" "wait")
        :subject "acme/work/f1"
        :evidence "acme/work/f1/1"))

(deftest "a-journaled-decision-replays-without-the-provider"
    "docs/SPEC-DECIDE.md:185-187"
    "expected=journal-carries-the-question-hash;replay-answers-from-the-journal;no-fresh-provider-call;kernel-order-bit-for-bit-identical"
  (let* ((journal (make-ordering-journal))
         (kernel (make-kernel :state (make-seed-state *seed*) :journal journal
                              :rev-base 1))
         (fake (make-fake-decider :answer "ship" :confidence 0.9))
         (question (decide-question))
         (eligible (list (list :id "acme/work/f1/t1" :priority 5)
                         (list :id "acme/work/f1/t2" :priority 9)))
         (order-before (ready-order eligible nil :order :priority))
         (revision-before (state-revision (kernel-state kernel))))
    ;; A fresh decision consults the fake once and journals the question hash.
    (let ((first (decide kernel question 0.5 :provider fake)))
      (check-equal "ship" (decision-answer first)
                   "the fake's above-floor answer is decisive")
      (check-equal 1 (fake-decider-calls fake) "the fake is consulted once")
      (check-equal (question-hash question) (decision-question-hash first)
                   "the decision names the question hash")
      (let ((event (journaled-decision-event journal question)))
        (ok event "the decision is journaled as an event")
        (check-equal :decide (work-event-kind event)
                     "the journaled event has the :decide kind")
        (check-equal (question-hash question)
                     (getf (work-event-fields event) :question-hash)
                     "the journaled event carries the question hash")))
    ;; Replay with the provider ABSENT: the journal answers, and the provider
    ;; is never consulted again.
    (let ((replay (decide kernel question 0.5 :provider nil)))
      (check-equal "ship" (decision-answer replay)
                   "the replay reproduces the journaled answer")
      (check-equal :journal (decision-source replay)
                   "the replay answers from the journal")
      (check-equal 1 (fake-decider-calls fake)
                   "the replay made no fresh provider call"))
    ;; The kernel's decisive order is bit-for-bit identical before and after.
    (check-equal order-before (ready-order eligible nil :order :priority)
                 "the kernel's decisive order is bit-for-bit identical")
    (check-equal revision-before (state-revision (kernel-state kernel))
                 "the decision moved no revision")))

(deftest "a-below-floor-decision-keeps-the-kernels-own-answer"
    "docs/SPEC-DECIDE.md:177-187"
    "expected=below-the-floor-the-kernel-keeps-its-own-abstain;the-provider-answer-is-a-suggestion;the-confidence-and-floor-are-carried"
  (let* ((kernel (make-kernel :state (make-seed-state *seed*)
                              :journal (make-ordering-journal) :rev-base 1))
         (question (decide-question))
         (fake (make-fake-decider :answer "ship" :confidence 0.5))
         (decision (decide kernel question 0.9 :provider fake)))
    (check-equal :abstain (decision-answer decision)
                 "below the floor the kernel keeps its own abstain")
    (check-equal "ship" (decision-proposed decision)
                 "the provider's answer is still recorded")
    (ok (decision-suggestion-p decision)
        "the below-floor answer is a suggestion")
    (check-equal 500 (decision-confidence decision) "the provider confidence is carried")
    (check-equal 900 (decision-floor decision) "the floor is carried")))
