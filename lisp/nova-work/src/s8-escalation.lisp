;;;; s8-escalation.lisp --- the duty tier, policy execution, escalation, quiet
;;;; time and the coordination measure (S8, #500).
;;;;
;;;; docs/SPEC-WORK.md:2548-2613 — *The duty tier and the single-writer kernel*.
;;;; This file holds the pure records and functions for the resident session's
;;;; authority (execute an approved policy, never author it, escalate what it
;;;; cannot decide), the escalation node kind, quiet time and the coordination
;;;; measure. Rule 6 (the one command thread) is the S1 kernel and is not here.
;;;;
;;;; Replays: `duty-tier-executes-the-policy` (2567), `escalation-carries-rule
;;;; -default-age` (2577), `quiet-time-calls-nothing` (2596),
;;;; `cost-per-accepted-decision` (2604).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The policy and its rules.
;;;
;;; A rule is a versioned word: `:by` names the author of the rule, exactly as
;;; every event carries `:by`. A rule with no `:by` is refused at load
;;; (SPEC-WORK.md:2565-2567). `:qualifies` decides whether the rule can make
;;; THIS judgment; `:model` is the model it names, or the keyword `:none` for a
;;; deliberate no-model decision, or NIL when the rule owns the decision but
;;; has no model to run — the judgment it cannot make, which escalates.
;;; ------------------------------------------------------------------

(defstruct policy-rule
  id by qualifies model cost default)

(defstruct duty-policy
  id rules default)

(defun load-policy (id rules &key (default :release))
  "Refuse an unapproved policy: an approved finite policy records whose word
  every rule is, so a rule whose `:by` is absent is refused at load."
  (dolist (r rules)
    (when (absentp (policy-rule-by r))
      (error 'unsupported-input
             :what (format nil "policy rule ~A has no :by; an approved policy records whose word every rule is"
                           (or (policy-rule-id r) "?")))))
  (make-duty-policy :id id :rules rules :default default))

;;; ------------------------------------------------------------------
;;; Escalation: a node kind carrying `:rule`, `:default` and age
;;; (SPEC-WORK.md:2570-2577). Age is how long the escalation has stood, so the
;;; record keeps `:raised-at` and the reading computes the age from the clock.
;;; ------------------------------------------------------------------

(defstruct escalation rule default raised-at)

(defun raise-escalation (rule default &key (now 0))
  (make-escalation :rule rule :default default :raised-at now))

(defun escalation-age (escalation now)
  "How long the escalation has stood, measured against the reader's clock."
  (- now (escalation-raised-at escalation)))

(defun read-escalation (escalation now)
  "`stale` reads the three fields and reassigns nothing: they are information
  (SPEC-WORK.md:2575). A pure read; calling it again returns the same values."
  (list :rule (escalation-rule escalation)
        :default (escalation-default escalation)
        :age (escalation-age escalation now)))

;;; ------------------------------------------------------------------
;;; Execution: the cheapest qualified model or none; escalate what cannot be
;;; decided (SPEC-WORK.md:2560-2567). Returns
;;;   (:decided <rule-id> <model> <cost>)  — a real model, cheapest among the
;;;                                           rules that qualify
;;;   (:none <rule-id>)                    — a rule deliberately decided none
;;;   (:escalated <escalation>)            — nothing could decide it
;;; ------------------------------------------------------------------

(defun execute-policy (policy decision &key (now 0))
  (let ((matches (remove-if-not (lambda (r)
                                  (funcall (policy-rule-qualifies r) decision))
                                (duty-policy-rules policy))))
    (if (null matches)
        (list :escalated (raise-escalation :absent (duty-policy-default policy) :now now))
        (let ((decidable (remove-if (lambda (r) (null (policy-rule-model r))) matches))
              (undecided (remove-if-not (lambda (r) (null (policy-rule-model r))) matches)))
          (if (null decidable)
              (let ((r (car undecided)))
                (list :escalated (raise-escalation (policy-rule-id r)
                                                   (policy-rule-default r)
                                                   :now now)))
              (let ((best (first (sort (copy-list decidable) #'<
                                       :key #'policy-rule-cost))))
                (if (eq :none (policy-rule-model best))
                    (list :none (policy-rule-id best))
                    (list :decided (policy-rule-id best)
                          (policy-rule-model best)
                          (policy-rule-cost best)))))))))

;;; ------------------------------------------------------------------
;;; Quiet time (SPEC-WORK.md:2591-2596). Nothing changed is a trigger that
;;; fires on nothing: no model call, no note; the mechanical publication still
;;; happens, so the cost of one event is the spend of the one call it caused.
;;; ------------------------------------------------------------------

(defun duty-tick (&key changed)
  (if changed
      (list :changed t :model-calls 1 :notes 1
            :publications (list :clip :beat :projection))
      (list :changed nil :model-calls 0 :notes 0
            :publications (list :clip :beat :projection))))

(defun event-cost (tick)
  "The measured spend the one event caused: the model calls it ran."
  (getf tick :model-calls))

;;; ------------------------------------------------------------------
;;; The coordination measure: cost per accepted decision (SPEC-WORK.md:2598
;;; -2604). Wrong or missed decisions and a recovery latency past the gate are
;;; not accepted decisions; what is measured is what the accepted ones cost.
;;; ------------------------------------------------------------------

(defstruct decision cost wrong-p missed-p recovery-latency)

(defun accepted-decision-p (d &key (latency-gate most-positive-fixnum))
  (and (not (decision-wrong-p d))
       (not (decision-missed-p d))
       (<= (or (decision-recovery-latency d) 0) latency-gate)))

(defun coordination-measure (decisions &key (latency-gate most-positive-fixnum))
  (let* ((accepted (remove-if-not (lambda (d)
                                    (accepted-decision-p d :latency-gate latency-gate))
                                  decisions))
         (n (length accepted)))
    (if (zerop n)
        (list :accepted 0 :cost-per-accepted 0 :excluded (length decisions))
        (list :accepted n
              :cost-per-accepted (/ (reduce #'+ (mapcar #'decision-cost accepted)) n)
              :excluded (- (length decisions) n)))))
