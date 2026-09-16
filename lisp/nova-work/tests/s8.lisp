;;;; s8.lisp --- the S8 slice cases: Presence, friends and escalations, the duty
;;;; tier (#500). One deftest per named replay, in the splice order the slice
;;;; names them.
;;;;
;;;; Own files only: the records and pure functions these exercise live in
;;;; src/s8-*.lisp; wiring into the command thread is a later card.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; duty-tier-executes-the-policy        SPEC-WORK.md:2567
;;; ------------------------------------------------------------------

(deftest "duty-tier-executes-the-policy" "docs/SPEC-WORK.md:2567"
    "an approved policy with :by on every rule chooses the cheapest qualified model or none, and escalates what it cannot decide; a rule with no :by is refused at load"
  (let* ((rules (list (make-policy-rule :id "r1" :by "glenn" :model "deepseek" :cost 30
                                        :qualifies (lambda (d) (eq :feature (getf d :class)))
                                        :default :release)
                      (make-policy-rule :id "r2" :by "stella" :model "sol" :cost 10
                                        :qualifies (lambda (d) (eq :feature (getf d :class)))
                                        :default :release)))
         (policy (load-policy "P1" rules)))
    (check-equal (list :decided "r2" "sol" 10)
                 (execute-policy policy (list :class :feature))
                 "cheapest qualified model")
    (let ((none-policy (load-policy "P2"
                                    (list (make-policy-rule :id "n1" :by "glenn" :model :none :cost 0
                                                            :qualifies (lambda (d) (eq :trivial (getf d :class)))
                                                            :default :release)))))
      (check-equal (list :none "n1")
                   (execute-policy none-policy (list :class :trivial))
                   "a rule deliberately decides none"))
    (let ((gap-policy (load-policy "P3"
                                   (list (make-policy-rule :id "g1" :by "rowan" :model nil :cost 0
                                                           :qualifies (lambda (d) (eq :new (getf d :class)))
                                                           :default "release-on-silence")))))
      (let* ((result (execute-policy gap-policy (list :class :new) :now 90))
             (e (cadr result)))
        (check-equal :escalated (car result) "the judgment escalates")
        (check-equal "g1" (escalation-rule e) "the rule that could not decide")
        (check-equal "release-on-silence" (escalation-default e) "the default")
        (check-equal 90 (escalation-raised-at e) "raised at the clock")))
    (let ((result (execute-policy policy (list :class :unknown) :now 0)))
      (check-equal :escalated (car result) "an unmatched decision escalates")
      (check-equal :absent (escalation-rule (cadr result)) "no specific rule named"))
    (ok (handler-case (progn (load-policy "P4"
                                          (list (make-policy-rule :id "x" :by +absent+ :model "m" :cost 1
                                                                  :qualifies (lambda (d) t) :default :release)))
                             nil)
          (unsupported-input () t))
        "a policy rule with no :by is refused at load")))

;;; ------------------------------------------------------------------
;;; escalation-carries-rule-default-age  SPEC-WORK.md:2577
;;; ------------------------------------------------------------------

(deftest "escalation-carries-rule-default-age" "docs/SPEC-WORK.md:2577"
    "an escalation row carries the rule that could not decide, the default that fires on silence, and its age; stale reads the three and reassigns nothing"
  (let ((e (make-escalation :rule "rule-3" :default "release" :raised-at 100)))
    (check-equal "rule-3" (escalation-rule e) "rule")
    (check-equal "release" (escalation-default e) "default")
    (check-equal 80 (escalation-age e 180) "age grows with the clock")
    (check-equal (list :rule "rule-3" :default "release" :age 80)
                 (read-escalation e 180) "stale reads the three")
    (check-equal (list :rule "rule-3" :default "release" :age 80)
                 (read-escalation e 180) "reading reassigns nothing")))

;;; ------------------------------------------------------------------
;;; wait-table-four-presence-columns     SPEC-WORK.md:2588
;;; ------------------------------------------------------------------

(deftest "wait-table-four-presence-columns" "docs/SPEC-WORK.md:2588"
    "the per-harness wait table carries the four presence facts; a harness with the fourth unproven holds no resident session"
  (let* ((full (make-wait-row :friend "emma" :process-alive t :beat-written t
                              :delivery-handled t :parent-woke t))
         (three (make-wait-row :friend "liam" :process-alive t :beat-written t
                               :delivery-handled t :parent-woke nil)))
    (check-equal :resident (session-kind full) "all four proven holds a resident session")
    (check-equal :duty (session-kind three) "the fourth unproven holds a duty session only")
    (ok (holds-resident-session-p full) "the full row is resident")
    (ok (not (holds-resident-session-p three)) "the three-proven row is not resident")))

;;; ------------------------------------------------------------------
;;; quiet-time-calls-nothing             SPEC-WORK.md:2596
;;; ------------------------------------------------------------------

(deftest "quiet-time-calls-nothing" "docs/SPEC-WORK.md:2596"
    "a session with nothing changed makes no model call and sends no note, yet still publishes; the cost of the event is the spend it caused"
  (let ((tick (duty-tick :changed nil)))
    (check-equal 0 (getf tick :model-calls) "no model call")
    (check-equal 0 (getf tick :notes) "no note")
    (check-equal '(:clip :beat :projection) (getf tick :publications) "mechanical publication still happens")
    (check-equal 0 (event-cost tick) "the cost of the event is the spend it caused"))
  (let ((tick (duty-tick :changed t)))
    (check-equal 1 (getf tick :model-calls) "a change runs the one call")))

;;; ------------------------------------------------------------------
;;; cost-per-accepted-decision          SPEC-WORK.md:2604
;;; ------------------------------------------------------------------

(deftest "cost-per-accepted-decision" "docs/SPEC-WORK.md:2604"
    "cost per accepted decision gates out wrong, missed and slowly-recovered decisions"
  (let* ((decisions (list (make-decision :cost 100 :wrong-p nil :missed-p nil :recovery-latency 5)
                          (make-decision :cost 40 :wrong-p t   :missed-p nil :recovery-latency 5)
                          (make-decision :cost 60 :wrong-p nil :missed-p t   :recovery-latency 5)
                          (make-decision :cost 20 :wrong-p nil :missed-p nil :recovery-latency 500)))
         (m (coordination-measure decisions :latency-gate 100)))
    (check-equal 1 (getf m :accepted) "accepted count excludes the wrong, missed and slow")
    (check-equal 100 (getf m :cost-per-accepted) "what the accepted decisions cost")
    (check-equal 3 (getf m :excluded) "three gated out")))

;;; ------------------------------------------------------------------
;;; four-capability-groups-and-three-fields SPEC-WORK.md:3373
;;; ------------------------------------------------------------------

(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:3373"
    "child agents, swarms, local models and one-shots each carry declared support, verified runtime and free capacity as three fields that never collapse"
  (let ((entries (mapcar (lambda (g)
                           (make-capability g (format nil "c-~A" g)
                                            :declared-support :declared
                                            :verified-runtime nil
                                            :free-capacity 0))
                         '(:child :swarm :local :one-shot))))
    (check-equal 4 (length entries) "four groups")
    (dolist (e entries)
      (check-equal :declared (capability-declared-support e) "declared support")
      (check-equal nil (capability-verified-runtime e) "verified runtime unknown, never inferred")
      (check-equal 0 (capability-free-capacity e) "free capacity is a third field")))
  (ok (handler-case (progn (make-capability :car "c-1") nil)
        (unsupported-input () t))
      "a non-group is refused"))

;;; ------------------------------------------------------------------
;;; four-facts-four-verbs                SPEC-WORK.md:3727
;;; ------------------------------------------------------------------

(deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:3727"
    "an admitted offer writes dispatch and a reservation; a received acknowledgement writes delivery only; none is inferred from another"
  (let ((intent (make-offer-intent :id "o1" :to "emma" :profile "sonnet@7" :reserve 1)))
    (let ((r (admit-offer intent :free-slots 1)))
      (check-equal :dispatched (offer-result-effect r) "dispatch effect")
      (check-equal 1 (offer-result-reserve r) "a reservation from declared free slots")
      (check-equal "o1" (offer-result-offer-id r) "the offer id"))
    (check-equal :refused (offer-result-effect (admit-offer intent :free-slots 0))
                 "no declared free slots refuses"))
  (let ((r (acknowledge-received "o1" "r7")))
    (check-equal :received (receipt-stage r) "received writes delivery only")
    (check-equal "o1" (receipt-offer-id r) "delivery names the offer"))
  (let ((r (acknowledge-accepted "o1" "r8")))
    (check-equal :accepted (receipt-stage r) "accepted writes accepted ownership"))
  (let ((r (decline-offer "o1" "r9")))
    (check-equal :declined (receipt-stage r) "decline writes a verified refusal")))

;;; ------------------------------------------------------------------
;;; pipeline-replies-are-correlated      SPEC-WORK.md:5496
;;; ------------------------------------------------------------------

(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5496"
    "out-of-order and fragmented responses reach only their matching request; unknown, duplicate and absent response ids refuse"
  (let ((requests (list (list :id "q1" :op :query)
                        (list :id "q2" :op :query)
                        (list :id "m1" :op :mutation)
                        (list :id "l1" :op :long-op)))
        (responses (list (list :op-id "op-m1" :request-id "m1" :payload "done")
                         (list :op-id "op-q2" :request-id "q2" :payload "rows2")
                         (list :op-id "op-l1" :request-id "l1" :payload "started")
                         (list :op-id "op-q1" :request-id "q1" :payload "rows1"))))
    (check-equal (list (list "q1" "op-q1" "rows1")
                       (list "q2" "op-q2" "rows2")
                       (list "m1" "op-m1" "done")
                       (list "l1" "op-l1" "started"))
                 (correlate-replies requests responses)
                 "every reply reaches its request, in request order"))
  (check-equal (list (list "op-q1" "s1s2"))
               (reassemble-frames (list (list :op-id "op-q1" :seq 2 :frag "s2")
                                        (list :op-id "op-q1" :seq 1 :frag "s1")))
               "fragments reassemble in sequence order")
  (ok (handler-case (progn (correlate-replies (list (list :id "q1" :op :query))
                                              (list (list :op-id nil :request-id "q1" :payload "x")))
                           nil)
        (unsupported-input () t))
      "a response with no decodable id refuses")
  (ok (handler-case (progn (correlate-replies (list (list :id "q1" :op :query))
                                              (list (list :op-id "op-x" :request-id "q1" :payload "a")
                                                    (list :op-id "op-x" :request-id "q1" :payload "b")))
                           nil)
        (unsupported-input () t))
      "a duplicate response id refuses")
  (ok (handler-case (progn (correlate-replies (list (list :id "q1" :op :query))
                                              (list (list :op-id "op-x" :request-id "q2" :payload "x")))
                           nil)
        (unsupported-input () t))
      "a response naming no request refuses"))
