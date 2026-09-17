;;;; slice-09-replays-holds.lisp --- the execution-control replays of
;;;; docs/SPEC-WORK.md:3921-3980. Loaded by ../acceptance.lisp; an amendment
;;;; edits one slice file (nova-tools #560).

(in-package #:nova-work/tests)

;;; The hold is durable and is not a transition. `goal show`'s stop= stays
;;; derived from the state and never reads it.
(deftest "stop-is-a-hold-not-a-cancel" "docs/SPEC-WORK.md:3944"
    "expected=hold-plus-directives;no-transition;not-a-cancel"
  (let ((k (fresh)))
    (record-attempt k "acme/work/f1/t1" "att-1")
    (record-attempt k "acme/work/f1/t1" "att-2")
    (let ((before (root-digest (kernel-state k)))
          (rev (state-revision (kernel-state k))))
      (multiple-value-bind (ok line code hold)
          (execution-stop k :scope '(:node "acme/work/f1/t1") :request "ctrl-stop-1")
        (ok ok "execution stop refused: ~A" line)
        (check-equal 0 code "execution stop exit code")
        (check-equal 1 (length (kernel-holds k)) "one hold")
        (check-string= "ctrl-stop-1" (hold-id hold) "hold id")
        (check-equal :stop (hold-action hold) "hold action")
        (check-equal '("att-1" "att-2") (hold-targets hold) "captured targets")
        (check-equal 2 (length (hold-directives hold)) "one directive per target")
        (ok (hold-durable-p hold) "the hold is durable")
        ;; no transition: the state bytes and the revision are unmoved
        (check-string= before (root-digest (kernel-state k)) "execution stop moved state")
        (check-equal rev (state-revision (kernel-state k)) "execution stop moved revision")
        ;; goal show derives stop= from the state, and no hold is read into it
        (check-string= "stop=none" (goal-stop k "acme/work/f1/t1")
                       "goal show read the hold into stop=")))))

;;; The :cancel's evidence covers the attempt set, so one worker's stop note
;;; cannot cancel a node with another live attempt.
(deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:3944"
    "expected=one-note-cannot-cancel-two-live-attempts"
  (let ((k (fresh)))
    (record-attempt k "acme/work/f1/t1" "att-1")
    (record-attempt k "acme/work/f1/t1" "att-2")
    (request-cancel k "acme/work/f1/t1" :request "cancel-req-1")
    ;; one worker's note covers only its own attempt: refused
    (multiple-value-bind (ok line code)
        (cancel-confirm k "acme/work/f1/t1" :evidence '("att-1"))
      (ok (not ok) "a one-attempt note cancelled a two-attempt node")
      (check-equal 1 code "refusal exit code")
      (ok (search "cannot cancel" line) "refusal does not name the rule: ~A" line))
    ;; both attempts covered: the same evidence set admits it
    (multiple-value-bind (ok line code)
        (cancel-confirm k "acme/work/f1/t1" :evidence '("att-1" "att-2"))
      (ok ok "two covered attempts still refused: ~A" line)
      (check-equal 0 code "admission exit code"))
    ;; the cancellation edge's stop= is the state's, and execution stop is not it
    (execution-stop k :scope '(:node "acme/work/f1/t1") :request "ctrl-stop-2")
    (check-string= "stop=requested" (goal-stop k "acme/work/f1/t1")
                   "the state edge's stop= is not what execution stop wrote")))

;;; A crash after the hold is durable and before capture or send recovers the
;;; same hold and target identities with no duplicate launch.
(deftest "hold-survives-a-crash" "docs/SPEC-WORK.md:3962"
    "expected=hold-and-target-identities-durable-before-ack;no-duplicate-launch"
  (let* ((journal (make-ordering-journal))
         (k (fresh :journal journal)))
    (record-attempt k "acme/work/f1/t1" "att-1")
    (record-attempt k "acme/work/f1/t1" "att-2")
    (multiple-value-bind (ok line code hold)
        (execution-stop k :scope '(:node "acme/work/f1/t1") :request "ctrl-crash-1")
      (ok ok "execution stop refused: ~A" line)
      (check-equal 0 code "execution stop exit code")
      ;; a crash: a brand-new kernel over the same journal, no memory of the hold
      (let* ((k2 (fresh :journal journal))
             (recovered (recover-controls k2)))
        (check-equal 1 (length recovered) "one recovered hold")
        (let ((h (first recovered)))
          (check-string= "ctrl-crash-1" (hold-id h) "recovered hold id")
          (check-equal (hold-targets hold) (hold-targets h) "recovered target identities")
          (check-equal (hold-directives hold) (hold-directives h) "recovered directives")
          (check-equal (hold-pin hold) (hold-pin h) "recovered anchor pin")
          (ok (hold-durable-p h) "recovered hold is durable")
          ;; no duplicate launch: the recorded set is staged exactly once
          (check-equal (length (hold-targets hold)) (length (hold-targets h))
                       "recovery added or dropped a target"))))))

;;; A clip publishes between the anchor and the manifest and carries the pin
;;; forward, the capture never reconstructs from a newer scope, and an
;;; unrepresentable pin refuses admission before EXECUTION OK.
(deftest "capture-survives-clip" "docs/SPEC-WORK.md:3962"
    "expected=clip-carries-pin-forward;no-reconstruct-from-newer-scope;unrepresentable-pin-refused-before-ack"
  (let ((k (fresh)))
    (record-attempt k "acme/work/f1/t1" "att-1")
    (record-attempt k "acme/work/f1/t1" "att-2")
    (multiple-value-bind (ok line code hold)
        (execution-pause k :scope '(:node "acme/work/f1/t1") :request "ctrl-clip-1"
                           :retention 2)
      (ok ok "pause refused: ~A" line)
      (check-equal 0 code "pause exit code")
      (let ((pin (hold-pin hold)))
        ;; a clip completes between the anchor and the manifest
        (ctl-clip k)
        (check-equal pin (hold-pin hold) "the clip moved the pin")
        ;; a later assignment is not part of the anchored capture
        (record-attempt k "acme/work/f1/t1" "att-3")
        (let ((manifest (capture-manifest k hold)))
          (check-equal '("att-1" "att-2") (manifest-target-ids manifest)
                       "capture reconstructed from the newer scope")
          (check-equal (car pin) (manifest-revision manifest) "manifest revision")
          (check-equal (cdr pin) (manifest-span manifest) "manifest span")))))
  ;; a retention window too small for the exact references refuses before ack
  (let ((k (fresh)))
    (record-attempt k "acme/work/f1/t1" "a1")
    (record-attempt k "acme/work/f1/t1" "a2")
    (multiple-value-bind (ok line code hold)
        (execution-pause k :scope '(:node "acme/work/f1/t1") :request "ctrl-bad-1"
                           :retention 1)
      (declare (ignore hold))
      (ok (not ok) "an unrepresentable pin was admitted")
      (check-equal 2 code "refusal exit code")
      (ok (search "unrepresentable" line) "refusal does not name the pin: ~A" line)
      (check-equal 0 (length (kernel-holds k)) "a refused control installed a hold")
      (check-equal 0 (length (journal-order (kernel-journal k)))
                   "a refused control journaled a record"))))

;;; The dispatch barrier is checked at offer, at conversion and at the last
;;; send; an acceptance under a hold is retained :accepted-held and a lift of
;;; the hold converts nothing.
(deftest "no-dispatch-slips-past-a-hold" "docs/SPEC-WORK.md:3979"
    "expected=barrier-at-offer-conversion-send;:accepted-held;lift-converts-nothing"
  (let ((k (fresh)))
    ;; an offer prepared before the hold
    (multiple-value-bind (ok line code offer)
        (prepare-offer k "off-1" "acme/work/f1/t1" "att-1")
      (declare (ignore offer))
      (ok ok "offer refused before the hold: ~A" line)
      (check-equal 0 code "offer exit code"))
    (multiple-value-bind (ok line code hold)
        (execution-stop k :scope '(:node "acme/work/f1/t1") :request "ctrl-send-1")
      (declare (ignore hold))
      (ok ok "stop refused: ~A" line)
      (check-equal 0 code "stop exit code")
      ;; the last send refuses the offer prepared before the hold
      (multiple-value-bind (sok sline scode) (send-offer k "off-1")
        (ok (not sok) "an offer slipped past the hold at send")
        (check-equal 1 scode "send refusal exit code")
        (ok (search "held" sline) "send refusal does not name the hold: ~A" sline))
      ;; a launch, a correction and a move raced against the pause all hold
      (ok (not (launch-attempt k "acme/work/f1/t1" "att-1")) "a launch slipped past the hold")
      (ok (not (correct-attempt k "acme/work/f1/t1" "att-1")) "a correction slipped past the hold")
      (ok (not (move-node k "acme/work/f1/t1" "acme/work/f2")) "a move slipped past the hold")
      ;; a conversion under the hold retains :accepted-held
      (multiple-value-bind (aok effect acode) (ctl-accept-offer k "off-1")
        (ok aok "held acceptance refused instead of retained: ~A" effect)
        (check-equal 0 acode "accept exit code")
        (check-equal :accepted-held effect "held acceptance effect")
        (check-equal :accepted-held (offer-effect k "off-1") "offer effect")
        (ok (not (offer-lease-p k "off-1")) "held acceptance leased")
        (ok (not (offer-launched-p k "off-1")) "held acceptance launched")
        (check-equal 0 (lease-count k "acme/work/f1/t1") "held acceptance created a lease")
        (check-equal 0 (launch-count k "acme/work/f1/t1") "held acceptance launched"))
      ;; lifting the hold converts nothing; only reconcile rechecks
      (ctl-release-hold k "ctrl-send-1")
      (check-equal :accepted-held (offer-effect k "off-1") "lift converted the held acceptance")
      (check-equal 0 (lease-count k "acme/work/f1/t1") "lift created a lease")
      (multiple-value-bind (rok rline rcode) (reconcile-offer k "off-1")
        (ok rok "reconcile refused: ~A" rline)
        (check-equal 0 rcode "reconcile exit code")
        (check-equal :accepted (offer-effect k "off-1") "reconcile did not convert")))))
