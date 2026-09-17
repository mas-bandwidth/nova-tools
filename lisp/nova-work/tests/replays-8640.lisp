;;;; replays-8640.lisp --- the five execution-control acceptance replays of
;;;; docs/SPEC-WORK.md:3964-4037, named by the "Required replays" table at
;;;; docs/SPEC-WORK.md:5740-5757. Each deftest asserts the paragraph's promise:
;;;; the durable hold and its dispatch barrier, the acceptance retained under
;;;; it, reconciliation that keeps contradiction, the two resume actions, and
;;;; execution correct as a linked segment with its bare verb refused.
;;;;
;;;; Nothing here starts a session, a transport or a worker: the pure records
;;;; and decisions live in src/replays-8640.lisp.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; held-acceptance-converts-nothing                  SPEC-WORK.md:3980
;;; ------------------------------------------------------------------

(deftest "held-acceptance-converts-nothing" "docs/SPEC-WORK.md:3980"
    "expected=offer-before-pause-refused-at-send;:accepted-held;no-lease-no-launch-no-release;lift-converts-nothing"
  ;; an offer prepared before a pause is admitted at offer, then refused at the
  ;; last send once the hold is installed.
  (ok (dispatch-admitted-p nil "n1" :offer) "an offer with no hold is admitted")
  (let ((held (install-hold nil :control "c1" :scope "n1")))
    (ok (not (dispatch-admitted-p held "n1" :send))
        "a hold installed after the offer refuses at the last send")
    ;; a launch, a correction and a move raced against the scope pause are held.
    (dolist (action '(:launch :correct :move))
      (ok (not (race-admitted-p held "n1" action))
          "a ~A raced against the pause is held" action))
    ;; an acceptance under the hold is retained :accepted-held, reservation intact.
    (let ((accepted (accept-offer :holds held :scope "n1" :offer "o1" :attempt "a1"
                                  :generation 2 :request "req-1")))
      (check-equal :accepted-held (getf accepted :effect)
                   "an acceptance under a hold is :accepted-held")
      (check-equal t (getf accepted :reservation) "the reservation stands")
      (check-equal nil (getf accepted :lease) "no lease is created")
      (check-equal nil (getf accepted :launch) "no launch is made")
      (check-equal nil (getf accepted :release) "no capacity is released")
      ;; lifting the hold does not convert it: only reconcile rechecks it.
      (let ((lifted (release-hold held "c1")))
        (check-equal nil (hold-for-scope lifted "n1") "the lift removes the hold")
        (check-equal :accepted-held (getf accepted :effect)
                     "the lift converts nothing: the acceptance stays :accepted-held")
        (check-equal nil (getf accepted :lease) "the lift creates no lease"))
      ;; a reconcile with a stale generation leaves it held; the right one converts it.
      (check-equal :accepted-held
                   (getf (reconcile-acceptance accepted :holds nil :generation 3) :effect)
                   "a stale-generation reconcile leaves it held")
      (let ((reconciled (reconcile-acceptance accepted :holds nil :generation 2)))
        (check-equal :accepted (getf reconciled :effect)
                     "the right reconcile converts the held acceptance")
        (ok (getf reconciled :lease) "the conversion creates the lease")))))

;;; ------------------------------------------------------------------
;;; reconcile-preserves-contradiction                 SPEC-WORK.md:4009
;;; ------------------------------------------------------------------

(deftest "reconcile-preserves-contradiction" "docs/SPEC-WORK.md:4009"
    "expected=contradictions-preserved-unresolved;stop-carries-no-synthesised-zero;holder-only-release-unbypassed"
  ;; two contradictory observations about one attempt stay unresolved, both kept.
  (let* ((running (make-observation :attempt "a1" :outcome :running :usage 7 :source "h1"))
         (stopped (make-observation :attempt "a1" :outcome :stopped :usage nil :source "h2"))
         (r (reconcile-observations (list running stopped))))
    (check-equal :unresolved (getf r :status) "contradictory observations stay unresolved")
    (check-equal 2 (length (getf r :retained)) "both contradictory observations are retained")
    (ok (member running (getf r :retained) :test #'equal) "the first is not overwritten")
    (ok (member stopped (getf r :retained) :test #'equal) "the second is not overwritten"))
  ;; a stop report carries no usage; missing usage stays unknown, never a zero.
  (let ((stop (make-observation :attempt "a2" :outcome :stopped :usage nil :source "h1")))
    (check-equal nil (observation-usage stop) "a stop report carries no usage")
    (check-equal :absent (synthesised-usage stop) "a stop report synthesises no zero cost"))
  ;; a confirmed termination permits capacity reconciliation but does not let the
  ;; coordinator sign for the holder.
  (check-equal nil (release-permitted-p "coordinator" "holder" :confirmed-exit-p t)
               "a confirmed exit makes no holder-only release")
  (check-equal t (release-permitted-p "holder" "holder" :confirmed-exit-p t)
               "the holder's own release is admitted"))

;;; ------------------------------------------------------------------
;;; resume-is-two-actions                             SPEC-WORK.md:4019
;;; ------------------------------------------------------------------

(deftest "resume-is-two-actions" "docs/SPEC-WORK.md:4019"
    "expected=release-hold-lifts-only-its-control;resume-workers-refuses-unsupported;hold-kept-until-running"
  ;; release-hold removes only its named control's hold; an overlapping hold stays.
  (let* ((holds (install-hold (install-hold nil :control "c1" :scope "n1")
                              :control "c2" :scope "n1"))
         (after (release-hold holds "c1")))
    (check-equal nil (hold-for-control after "c1") "release-hold lifts its named control")
    (ok (hold-for-control after "c2") "the overlapping control's hold stays effective")
    (ok (hold-for-scope after "n1") "the overlapping hold is still effective for the node"))
  ;; resume-workers refuses an unsupported capability before it sends.
  (multiple-value-bind (ok line) (resume-workers :capability :unsupported)
    (check-equal nil ok "an unsupported capability is refused")
    (ok (search "unsupported" line) "the refusal names the unsupported capability: ~A" line))
  ;; a supported resume stages a directive but keeps the hold until a running
  ;; observation at the resumed boundary arrives.
  (multiple-value-bind (ok line) (resume-workers :capability :resume)
    (ok ok "a supported capability stages a resume directive: ~A" line))
  (check-equal t (resume-hold-kept-p :delivery-receipt)
               "a delivery receipt alone releases nothing")
  (check-equal nil (resume-hold-kept-p :running-observation)
               "a running observation releases the hold")
  ;; an unsupported outcome closes the transport failed and clears neither.
  (let ((result (unsupported-outcome :control "c1" :holds nil)))
    (check-equal :failed (getf result :transport) "the transport operation is closed failed")
    (check-equal t (getf result :hold) "the hold is not cleared")
    (check-equal t (getf result :uncertain) "the uncertainty is not cleared")))

;;; ------------------------------------------------------------------
;;; correct-is-a-linked-segment                       SPEC-WORK.md:4037
;;; ------------------------------------------------------------------

(deftest "correct-is-a-linked-segment" "docs/SPEC-WORK.md:4037"
    "expected=one-envelope-hold-correct-binding;retry-bumps-generation-once;linked-segment-not-rewrite"
  ;; one envelope, one request id, writes the hold, the :correct event and the binding.
  (let ((env (execution-correct :node "n1" :request "req-1" :generation 2 :old-generation 1
                                :bytes "bytes v2" :old-usage 7 :old-result "r-1")))
    (ok (getf env :hold) "the envelope installs the node's hold")
    (check-equal :correct (getf (getf env :event) :kind)
                 "the envelope writes the node's own :correct event")
    (check-equal (sha256-hex "bytes v2") (getf env :instruction-sha256)
                 "the envelope binds the new generation to the bytes and their SHA-256")
    (check-equal "req-1" (getf env :request) "one envelope under one request id")
    ;; a retry of the same request id bumps the generation once, not twice.
    (let ((retry (execution-correct :node "n1" :request "req-1" :generation 3
                                    :old-generation 1 :bytes "bytes v2"
                                    :old-usage 7 :old-result "r-1" :seen env)))
      (check-equal 2 (getf retry :generation) "a retry does not bump the generation twice")
      (ok (getf retry :retried) "the retry is recognised as a retry"))
    ;; the old generation's usage and result are a linked segment, not a rewrite.
    (let ((segment (correct-segment 1 2 7 "r-1")))
      (check-equal 7 (getf segment :usage) "old-generation usage is kept")
      (check-equal "r-1" (getf segment :result) "old-generation result is kept")
      (check-equal :linked (getf segment :link) "the old segment links to the new generation")
      (check-equal 2 (getf segment :to-generation) "the linked segment names the new generation")
      (check-equal nil (getf segment :rewrite) "the old segment is not rewritten")))
  ;; a missing, mismatched or stale stage refuses whole.
  (multiple-value-bind (ok line) (execution-correct :node "n1" :request "req-2"
                                                    :generation 3 :old-generation 2 :bytes nil)
    (check-equal nil ok "a missing stage refuses whole")
    (ok (search "stage" line) "the refusal names the stage: ~A" line)))

;;; ------------------------------------------------------------------
;;; bare-correct-refuses-under-execution               SPEC-WORK.md:4037
;;; ------------------------------------------------------------------

(deftest "bare-correct-refuses-under-execution" "docs/SPEC-WORK.md:4037"
    "expected=bare-correct-refused-while-live;demands-execution-correct"
  (multiple-value-bind (ok line) (bare-correct "n1" :live t)
    (check-equal nil ok "the bare correct is refused while an execution is live")
    (ok (search "CORRECT FAIL node=n1" line) "the refusal names the node: ~A" line)
    (ok (search "execution live, use execution correct" line)
        "the refusal names the way on: ~A" line))
  (multiple-value-bind (ok line) (bare-correct "n1" :uncertain t)
    (check-equal nil ok "the bare correct is refused while an execution is uncertain")
    (ok (search "use execution correct" line) "the uncertain refusal names the way on"))
  ;; admitted once none is live or uncertain, and it claims no delivery.
  (multiple-value-bind (ok line result) (bare-correct "n1" :live nil :uncertain nil)
    (ok ok "the bare correct is admitted once none is live or uncertain: ~A" line)
    (check-equal nil (getf result :delivery) "the bare correct claims no delivery")
    (check-equal t (getf result :evidence-invalidated)
                 "the bare correct still invalidates evidence")))
