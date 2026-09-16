;;;; s8.lisp --- the S8 acceptance cases: presence, friends and escalations,
;;;; the duty tier (#500).
;;;;
;;;; One deftest per named replay the slice implements, named exactly as
;;;; docs/SPEC-WORK.md names it, with the SPEC-WORK.md line and a one-line
;;;; expectation, asserting the printed lines the spec shows. These are red now:
;;;; every symbol they call is the S8 contract the implementation cards fill in,
;;;; and none of it exists on the current dev head.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; 1. the session's authority to execute, never author
;;;    SPEC-WORK.md:2560
;;; ------------------------------------------------------------------

(deftest "duty-tier-executes-the-policy" "docs/SPEC-WORK.md:2560"
    "expected=every-rule-carries-by,executes-only-never-authors,escalates-undecidable,no-by-refused-at-load"
  (let ((approved (make-policy :rules '((:id :cut-work :by "rowan"
                                         :decision :stale :do :reassign)))))
    (ok (every #'policy-rule-by (policy-rules approved))
        "an approved policy rule without :by was loaded")
    ;; A policy rule with no :by is refused at load (SPEC-WORK.md:2559-2566).
    (handler-case (make-policy :rules '((:id :no-owner :decision :stale :do :reassign)))
      (unsupported-input (c)
        (ok (search "by" (princ-to-string c))
            "a rule without :by refused without naming :by: ~A" c))
      (error () (fail "a rule without :by was accepted at load")))
    ;; Executing the approved policy over an undecidable judgment escalates
    ;; rather than authoring a decision.
    (multiple-value-bind (okp line escalation)
        (execute-policy approved :judgment (list :node "acme/work/f1/t1" :stale-p t))
      (ok okp "the policy refused a judgment it cannot decide: ~A" line)
      (ok escalation "an undecidable judgment did not become an escalation"))))

;;; ------------------------------------------------------------------
;;; 2. escalation is a node kind carrying rule, default and age
;;;    SPEC-WORK.md:2570
;;; ------------------------------------------------------------------

(deftest "escalation-carries-rule-default-age" "docs/SPEC-WORK.md:2570"
    "expected=row-carries-rule-default-age,stale-reads-the-three-reassigns-nothing"
  (let ((row (make-escalation-row :rule "policy:cut-work" :default "reassign")))
    (check-string= "policy:cut-work" (escalation-rule row) "the rule that could not decide")
    (check-string= "reassign" (escalation-default row) "the default that fires on silence")
    (ok (plusp (escalation-age row)) "the escalation age is not positive")
    ;; stale reads the three as information and reassigns nothing.
    (let ((line (stale-line row)))
      (ok (search "escalated-age=" line) "the stale pass omits escalated-age=: ~A" line)
      (ok (search "reread=" line) "the stale pass omits reread=: ~A" line))))

;;; ------------------------------------------------------------------
;;; 3. the wait table gains the four presence columns
;;;    SPEC-WORK.md:2580
;;; ------------------------------------------------------------------

(deftest "wait-table-four-presence-columns" "docs/SPEC-WORK.md:2580"
    "expected=process-alive-beat-written-delivery-handled-parent-woke,fourth-unproven-holds-only-duty"
  (check-equal '(:process-alive :beat-written :delivery-handled :parent-woke)
               (wait-table-columns) "the four presence columns")
  (check-equal :resident
               (session-tier (make-wait-row :process-alive t :beat-written t
                                            :delivery-handled t :parent-woke t))
               "a full drill did not hold a resident session")
  (check-equal :duty
               (session-tier (make-wait-row :process-alive t :beat-written t
                                            :delivery-handled t :parent-woke nil))
               "an unproven parent-woke did not hold a duty session driven by notes"))

;;; ------------------------------------------------------------------
;;; 4. quiet time calls nothing
;;;    SPEC-WORK.md:2591
;;; ------------------------------------------------------------------

(deftest "quiet-time-calls-nothing" "docs/SPEC-WORK.md:2591"
    "expected=empty-pulse-calls-nothing,cost-of-one-event-is-measured-spend-of-one-call"
  (let ((p (pulse :nothing-changed-p t)))
    (check-equal 0 (model-calls p) "a quiet pulse made a model call")
    (check-equal 0 (notes-sent p) "a quiet pulse sent a note")
    (ok (mechanical-publication-p p)
        "a quiet pulse skipped its mechanical publication (the beat, the clip)")))

;;; ------------------------------------------------------------------
;;; 5. the coordination measure: cost per accepted decision
;;;    SPEC-WORK.md:2599
;;; ------------------------------------------------------------------

(deftest "cost-per-accepted-decision" "docs/SPEC-WORK.md:2599"
    "expected=cost-per-accepted-decision,wrong-or-missed-and-latency-are-gates"
  (let ((m (coordination-measure :spend 120 :accepted 40 :wrong 1 :missed 2
                                 :recovery-latency 17)))
    (check-equal 3 (coordination-cost-per-decision m)
                 "cost per accepted decision is not spend / accepted")
    (ok (coordination-wrong-gate m) "a wrong-or-missed decision gated nothing")
    (ok (coordination-latency-gate m) "a recovery-latency gate gated nothing")))

;;; ------------------------------------------------------------------
;;; 6. four capability groups, three fields never collapsed
;;;    SPEC-WORK.md:3363
;;; ------------------------------------------------------------------

(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:3363"
    "expected=groups-child-swarm-local-oneshot,declared-verified-free-stay-three-fields"
  (check-equal '(:child-agents :swarms :local-models :one-shots)
               (capability-groups (make-capability-inventory)) "the four groups")
  (let ((entry (make-capability-entry :declared :child-agents :verified nil :free 2)))
    (ok (not (equal (capability-declared entry) (capability-verified entry)))
        "declared support collapsed into verified runtime")
    (ok (not (equal (capability-verified entry) (capability-free entry)))
        "verified runtime collapsed into free capacity")
    (check-equal 2 (capability-free entry) "free capacity was not its own field")))

;;; ------------------------------------------------------------------
;;; 7. dispatch, delivery, acknowledgement, accepted ownership: four facts
;;;    SPEC-WORK.md:3719
;;; ------------------------------------------------------------------

(deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:3719"
    "expected=dispatch-delivery-acknowledgement-accepted-ownership-kept-apart"
  (check-equal '(:dispatched :delivered :acknowledged :accepted-ownership)
               (four-dispatch-facts) "the four facts are not the four")
  (check-string= "dispatched" (fact-of-verb :offer)
                 "offer did not write dispatch and nothing else")
  (check-string= "delivered" (fact-of-verb :acknowledge-received)
                 "acknowledge --stage received did not write delivery")
  (check-string= "accepted-ownership" (fact-of-verb :acknowledge-accepted)
                 "acknowledge --stage accepted did not write accepted ownership")
  (check-string= "declined" (fact-of-verb :decline)
                 "decline did not write a verified refusal"))

;;; ------------------------------------------------------------------
;;; 8. pipelined replies are correlated by request id, not arrival order
;;;    SPEC-WORK.md:2684
;;; ------------------------------------------------------------------

(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:2684"
    "expected=reply-reaches-only-its-matching-request,out-of-order-still-correlated"
  (let ((frames (deliver-replies
                 ;; two queries and a mutation, delivered out of order
                 '((:request "req-b" :line "QUERY OK ask=size open=5")
                   (:request "req-a" :line "QUERY OK ask=size open=4")
                   (:request "req-m" :line "STATE OK id=ev-1 request=req-m")))))
    (check-string= "QUERY OK ask=size open=4"
                   (reply-for "req-a" frames) "req-a's reply was correlated to another request")
    (check-string= "QUERY OK ask=size open=5"
                   (reply-for "req-b" frames) "req-b's reply was correlated to another request")
    (check-string= "STATE OK id=ev-1 request=req-m"
                   (reply-for "req-m" frames) "the mutation's reply was not echoed with its request id")))
