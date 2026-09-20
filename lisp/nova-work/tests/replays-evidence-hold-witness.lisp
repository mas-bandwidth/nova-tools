;;;; replays-evidence-hold-witness.lisp --- witness test for evidence holds and verified receipts
;;;;
;;;; docs/SPEC-WORK.md:4780-4867 (rules 1 and 2 of *The dependency gate and the hand report*),
;;;; and docs/SPEC-WORK.md:6926-6940 (replays of the dependency gate).
;;;;
;;;; Sprint Row 12 (#1839):
;;;; 1. Implement witness test for evidence holds and verified receipts in nova-work and nova-check.
;;;; 2. Prove that an unverified evidence record admits nothing, and stale evidence does not unmeet a need.
;;;;
;;;; PROVEN HERE:
;;;; - An unverified evidence record admits nothing:
;;;;   A need whose standing done names evidence that has not been verified by a qualifying
;;;;   fact in the verification cache leaves the need unmet (reason: need-unverified).
;;;;   The dependent task counts unmet=1, names the need, and reports need-unverified.
;;;;   An unverified fact, missing fact, or absent fact admits nothing.
;;;;   Only verified receipts (:holds in the verification cache) satisfy rule 1 and admit the dependent.
;;;; - Stale evidence does not unmeet a need:
;;;;   When the repository work set advances to a new revision, the verified evidence becomes stale
;;;;   (verify-evidence-stale-p is true). However, the dependency gate asks the historical-delivery
;;;;   question and leaves out the against-versus-source comparison: the need remains met (met=t),
;;;;   and the dependent task remains admitted (unmet=0).

(in-package #:nova-work/tests)

(deftest "witness-evidence-holds-and-verified-receipts"
    "docs/SPEC-WORK.md:4780,4790,6930-6939"
    "expected=unverified-evidence-admits-nothing;verified-receipts-admit;stale-evidence-does-not-unmeet-need"
  ;; 1. AN UNVERIFIED EVIDENCE RECORD ADMITS NOTHING.
  ;; N settles done with evidence, but the verification session's cache holds no fact for it.
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n" :sha "sha-v1"))
         (session (gate-session :source-revision "sha-v1"))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n")

    ;; Unverified: cache holds no fact yet.
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "an unverified evidence record must not be met")
      (check-equal :need-unverified reason "unverified evidence reason is need-unverified"))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "dependent D counts 1 unmet need on unverified evidence")
      (check-string= "acme/work/n" need "unmet need names N")
      (check-equal :need-unverified reason "reason is need-unverified"))
    (ok (not (node-needs-met-p (kernel-state k) "acme/work/d" :view view))
        "dependent D is not needs-met on unverified evidence")

    ;; Cache holding :absent also admits nothing.
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n" :fact :absent)
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "cache holding :absent must admit nothing")
      (check-equal :need-unverified reason "reason remains need-unverified"))
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "D still counts 1 unmet need when cache holds :absent"))

    ;; VERIFIED RECEIPTS ADMIT.
    ;; The session verification cache holds the qualifying raw fact :holds.
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n" :fact :holds)
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok met "the need is met once the cache holds verified evidence receipt")
      (check-equal nil reason "reason is nil when met"))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "dependent D reads unmet=0 once evidence is verified")
      (check-string= "-" need "first-need is - when unmet=0")
      (check-equal nil reason "reason is nil when unmet=0"))
    (ok (node-needs-met-p (kernel-state k) "acme/work/d" :view view)
        "dependent D is needs-met on verified receipts")

    ;; 2. STALE EVIDENCE DOES NOT UNMEET A NEED.
    ;; Advance the work set's source revision to a new sha.
    (setf (verification-session-source-revision session) "sha-v2")
    ;; Witness that the evidence is indeed recognized as stale by the verifier:
    (ok (nova-work::verify-evidence-stale-p session evidence)
        "the evidence record is verified to be stale against the new source revision")
    ;; But the gate leaves out the against-versus-source comparison (rule 1):
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok met "stale evidence does not unmeet need N")
      (check-equal nil reason "reason remains nil"))
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "dependent D still reads unmet=0 under stale evidence"))
    (ok (node-needs-met-p (kernel-state k) "acme/work/d" :view view)
        "dependent D remains needs-met under stale evidence")

    ;; Negative control: if the node generation moves, older evidence qualifies nothing.
    (setf (needs-view-generations view) '(("acme/work/n" . 2)))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "generation bump invalidates older evidence")
      (check-equal :need-unverified reason "corrected node reads need-unverified"))
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "dependent D re-blocks when need generation changes"))))
