;;;; slice-09-fleet-assignment.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.
;;;;
;;;; The fleet recommendation (SPEC-WORK.md:3557-3590) and the four assignment
;;;; facts / verified receipt (SPEC-WORK.md:3829-3860, 5680-5686). The pure
;;;; layer these call is src/replays-fleet-assignment.lisp: no session, no CLI,
;;;; no socket and no launcher. Each deftest names the spec paragraph it comes
;;;; from and the outcome that paragraph promises.

(in-package #:nova-work/tests)

(defun honest-verifier (bytes)
  "The operator-configured verifier: the configured recipient identity, a stable
receipt id and the digest of the received bytes. Test-only stand-in."
  (list :sender "rowan" :recipient "glenn" :receipt-id "receipt-1"
        :digest (sha256-hex bytes)))

(defun failing-verifier (bytes)
  "A verifier that fails validation."
  (declare (ignore bytes))
  nil)

;;; ------------------------------------------------------------------
;;; fleet-for-is-a-recommendation-not-a-lease       SPEC-WORK.md:3587
;;; ------------------------------------------------------------------

(deftest "fleet-for-is-a-recommendation-not-a-lease" "docs/SPEC-WORK.md:3587,5680"
    "expected=for-writes-no-lease;who-unchanged;facts-and-dates-on-the-row"
  (let* ((astra (make-machine :id "m-astra" :name "Astra" :owner "glenn"
                              :roles '(:build :test) :permits '(:coding)
                              :excludes '(:think) :limits '(:cores 8 :concurrent 2)
                              :facts '((:value "/opt/nova-harness" :declared-by "glenn"
                                               :declared-at "2026-09-01T04:00Z"))))
         (beta (make-machine :id "m-beta" :name "Beta" :owner "rowan"
                             :roles '(:think) :permits nil :excludes nil
                             :limits '(:cores 4)
                             :facts '((:value "/srv/nova-harness" :declared-by "rowan"
                                              :declared-at "2026-09-02T05:00Z"))))
         (session (make-fleet-session :who "glenn" :leases nil))
         (ask (fleet-for session (list astra beta) :coding)))
    (check-equal '("m-astra") (mapcar (lambda (row) (getf row :id)) (fleet-ask-rows ask))
                 "only the member whose roles and permits admit the kind is listed")
    (check-equal nil (fleet-ask-fail ask) "a recommendation is not a refusal")
    (check-equal nil (fleet-ask-lease ask) "the ask is never a lease")
    (check-equal "glenn" (fleet-session-who session) "the ask leaves who unchanged")
    (check-equal nil (fleet-session-leases session) "the ask writes no lease")
    (let* ((row (first (fleet-ask-rows ask)))
           (fact (first (getf row :facts))))
      (check-equal "2026-09-01T04:00Z" (getf fact :declared-at)
                   "each row carries the declared facts and their dates"))))

;;; ------------------------------------------------------------------
;;; an-excluded-choice-is-refused-not-empty         SPEC-WORK.md:3588
;;; ------------------------------------------------------------------

(deftest "an-excluded-choice-is-refused-not-empty" "docs/SPEC-WORK.md:3588"
    "expected=excluded-kind-refused;never-empty-rows;exit-1"
  (let* ((astra (make-machine :id "m-astra" :name "Astra" :owner "glenn"
                              :roles '(:build :test) :permits '(:coding)
                              :excludes '(:coding) :limits nil :facts nil))
         (session (make-fleet-session :who "glenn" :leases '(:live-lease)))
         (ask (fleet-for session (list astra) :coding :node "m-astra")))
    (ok (fleet-ask-fail ask) "an excluded choice is refused, not answered empty")
    (check-equal '() (fleet-ask-rows ask) "the refusal carries no rows")
    (check-string= "QUERY FAIL ask=fleet rows=0 shown=0: m-astra excludes coding"
                   (fleet-ask-fail ask)
                   "the refusal names the member and the kind")
    (check-equal 1 (fleet-ask-exit-code ask) "the refusal exits 1")
    (check-equal '(:live-lease) (fleet-session-leases session)
                 "the refusal writes no lease and disturbs no existing one")))

;;; ------------------------------------------------------------------
;;; four-facts-four-verbs                           SPEC-WORK.md:3843,5680
;;; ------------------------------------------------------------------

(deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:3843,5680"
    "expected=dispatch-delivery-accepted-ownership-apart;nothing-inferred;nothing-launched"
  (let ((state (make-assignment-state :who "glenn" :responsible "glenn"
                                      :node-state :doing :w '(:w-a)
                                      :attempts '(:attempt-a) :evidence '(:ev-a)
                                      :completed '(:done-a) :free-slots 4 :launches 0)))
    ;; offer writes dispatch and a reservation, and nothing else.
    (multiple-value-bind (ok line code)
        (assignment-offer state :offer-id "o-1" :node "acme/work/f1/t1"
                          :attempt "attempt-7" :to "glenn" :profile "coding@rev-3"
                          :payload-sha256 "sha256:beef" :reserve 2
                          :until "2026-09-17T00:00:00Z")
      (ok ok "an offer with declared free slots is admitted: ~A" line)
      (check-equal 0 code "offer exit code"))
    (check-equal 1 (length (assignment-state-offers state)) "one pending-offer entry")
    (check-equal :dispatched (getf (first (assignment-state-offers state)) :effect)
                 "offer writes :effect :dispatched")
    (check-equal 1 (length (assignment-state-reservations state)) "one reservation")
    (check-equal '("o-1") (mapcar #'car (assignment-state-reservations state))
                 "the reservation is keyed by the offer id")
    (check-equal '() (assignment-state-deliveries state) "offer writes no delivery")
    (check-equal '() (assignment-state-acceptances state) "offer writes no accepted ownership")
    (check-equal '() (assignment-state-declines state) "offer writes no refusal")
    (check-equal nil (assignment-state-leases state) "offer writes no lease")
    (check-equal '(:attempt-a) (assignment-state-attempts state) "offer writes no attempt")
    (check-equal '(:ev-a) (assignment-state-evidence state) "offer writes no evidence")
    (check-equal '(:done-a) (assignment-state-completed state) "offer writes no completion")
    (check-equal :doing (assignment-state-node-state state) "offer changes no node state")
    (check-equal '(:w-a) (assignment-state-w state) "offer changes no W")
    (check-equal "glenn" (assignment-state-responsible state) "offer changes no responsible")
    (check-equal 0 (assignment-state-launches state) "offer launches nothing")
    ;; acknowledge --stage received writes delivery only.
    (let ((staged (stage-provenance "received o-1" #'honest-verifier :rev 1)))
      (multiple-value-bind (ok line code)
          (assignment-acknowledge state :received :staged staged :offer-id "o-1" :current-rev 1)
        (ok ok "a verified received receipt is admitted: ~A" line)
        (check-equal 0 code "received exit code"))
      (check-equal 1 (length (assignment-state-deliveries state)) "received writes delivery")
      (check-equal '() (assignment-state-acceptances state)
                   "received consents to nothing (no accepted ownership)")
      (check-equal nil (assignment-state-leases state) "received writes no lease")
      (check-equal :doing (assignment-state-node-state state)
                   "received writes no node state")
      ;; acknowledge --stage accepted writes accepted ownership, and no lease or responsible change.
      (multiple-value-bind (ok line code)
          (assignment-acknowledge state :accepted :staged staged :offer-id "o-1" :current-rev 1)
        (ok ok "a verified accepted receipt is admitted: ~A" line)
        (check-equal 0 code "accepted exit code"))
      (check-equal 1 (length (assignment-state-acceptances state))
                   "accepted writes accepted ownership")
      (check-equal "glenn" (assignment-state-responsible state)
                   "accepted changes no :responsible")
      (check-equal nil (assignment-state-leases state)
                   "accepted alone creates no lease and proves no remote work began")
      (check-equal '() (assignment-state-declines state) "accepted writes no refusal"))
    ;; decline writes a verified refusal, and is inferred from nothing.
    (let ((staged (stage-provenance "declined o-1" #'honest-verifier :rev 1)))
      (multiple-value-bind (ok line code)
          (assignment-decline state :staged staged :offer-id "o-1" :current-rev 1)
        (ok ok "a verified decline is admitted: ~A" line)
        (check-equal 0 code "decline exit code"))
      (check-equal 1 (length (assignment-state-declines state)) "decline writes a refusal")
      (check-equal 1 (length (assignment-state-deliveries state))
                   "decline does not erase or duplicate the delivery")
      (check-equal 1 (length (assignment-state-acceptances state))
                   "decline does not erase the accepted ownership"))
    (check-equal 0 (assignment-state-launches state) "no verb launched anything")))

;;; ------------------------------------------------------------------
;;; a-receipt-needs-a-verifier                     SPEC-WORK.md:3858,5685
;;; ------------------------------------------------------------------

(deftest "a-receipt-needs-a-verifier" "docs/SPEC-WORK.md:3858,5685"
    "expected=unverified-provenance-refused;no-canonical-write;no-bus-body-authority"
  (let ((state (make-assignment-state :who "glenn"
                                      :offers (list (list :offer-id "o-1" :effect :dispatched)))))
    ;; a copied note with no verifier result is refused.
    (let ((staged (stage-provenance "BUS NOTE: accepted by rowan" nil :rev 1)))
      (check-equal nil (staged-input-valid-p staged) "a copied note with no verifier is not a valid stage")
      (multiple-value-bind (ok line code)
          (assignment-acknowledge state :accepted :staged staged :offer-id "o-1" :current-rev 1)
        (check-equal nil ok "a copied note is refused")
        (check-equal 2 code "the refusal is exit 2")
        (ok (search "provenance unverified" line)
            "a verifier outage reads provenance unverified: ~A" line)))
    ;; --as <recipient> with no verifier result is refused.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge state :accepted :offer-id "o-1" :current-rev 1)
      (check-equal nil ok "--as with no verifier result is refused")
      (check-equal 2 code "that refusal is exit 2")
      (ok (search "provenance unverified" line) "the refusal names provenance unverified: ~A" line))
    ;; a plain request carrying the session's own half is refused.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge state :received
                                :staged (stage-provenance "x" #'honest-verifier :rev 1)
                                :request (list :sender "rowan") :offer-id "o-1" :current-rev 1)
      (check-equal nil ok "a plain request carrying :sender is refused")
      (check-equal 2 code "that refusal is exit 2")
      (ok (search "session" line) "the refusal names the session's own half: ~A" line))
    (multiple-value-bind (ok line code)
        (assignment-acknowledge state :received
                                :staged (stage-provenance "x" #'honest-verifier :rev 1)
                                :request (list :receipt-digest "sha256:0")
                                :offer-id "o-1" :current-rev 1)
      (check-equal nil ok "a plain request carrying :receipt-digest is refused")
      (check-equal 2 code "that refusal is exit 2")
      (ok (search "session" line) "the refusal names the session's own half: ~A" line))
    ;; no bus body is promoted to authority and nothing canonical was written.
    (check-equal '() (assignment-state-deliveries state) "no delivery was written")
    (check-equal '() (assignment-state-acceptances state) "no accepted ownership was written")
    (check-equal '() (assignment-state-declines state) "no refusal was written")
    (check-equal nil (assignment-state-leases state) "no lease was written")
    (check-equal '() (assignment-state-reservations state) "no reservation was written")
    (check-equal '((:offer-id "o-1" :effect :dispatched)) (assignment-state-offers state)
                 "the pending offer is untouched")
    (ok (notany (lambda (record) (search "BUS NOTE" (princ-to-string record)))
                (assignment-state-acceptances state))
        "no bus body became an accepted record")
    ;; The verifier result a receipt needs: the configured recipient identity, a
    ;; stable receipt id and the digest of the received bytes; the session's own
    ;; half is derived from it and never carried on the request
    ;; (SPEC-WORK.md:3851-3853, 5837-5838).
    (let* ((bytes "received o-1")
           (verifier (make-receipt-verifier
                      :recipient "glenn"
                      :verify (lambda (b)
                                (list :recipient "glenn" :receipt-id "receipt-1"
                                      :digest (sha256-hex b))))))
      (let ((staged (stage-provenance bytes verifier :rev 1)))
        (ok (staged-input-valid-p staged) "a full verifier result stages")
        (check-equal "glenn" (getf (staged-input-result staged) :recipient)
                     "the result names the configured recipient")
        (check-equal (sha256-hex bytes) (staged-input-digest staged)
                     "the staged digest is the digest of the received bytes")
        (check-equal bytes (staged-input-bytes staged) "the stage holds the bytes"))
      ;; a result that names another recipient is not the configured verifier's.
      (check-equal nil
                   (staged-input-valid-p
                    (stage-provenance bytes
                                      (make-receipt-verifier
                                       :recipient "glenn"
                                       :verify (lambda (b)
                                                 (list :recipient "rowan"
                                                       :receipt-id "receipt-x"
                                                       :digest (sha256-hex b))))
                                      :rev 1))
                   "a result for another recipient is refused")
      ;; the writer derives :sender, :receipt-digest and :effect from the result.
      (let ((fresh (make-assignment-state
                    :who "glenn" :free-slots 4
                    :offers (list (list :offer-id "o-1" :effect :dispatched)))))
        (multiple-value-bind (ok line code)
            (assignment-acknowledge fresh :received
                                    :staged (stage-provenance bytes verifier :rev 1)
                                    :offer-id "o-1" :current-rev 1 :expect 1)
          (ok ok "a verified received receipt is admitted: ~A" line)
          (check-equal 0 code "the verified receipt exits 0"))
        (let ((receipt (first (assignment-state-deliveries fresh))))
          (check-equal "glenn" (getf receipt :sender)
                       "the sender is derived from the verifier's recipient")
          (check-equal "receipt-1" (getf receipt :receipt-id)
                       "the receipt carries the verifier's stable receipt id")
          (check-equal (sha256-hex bytes) (getf receipt :receipt-digest)
                       "the receipt digest is the verifier's digest of the bytes")
          (check-equal :delivered (getf receipt :effect)
                       "the effect is the session's own half"))))))

;;; ------------------------------------------------------------------
;;; staged-admission-refuses                       SPEC-WORK.md:3859,5685
;;; ------------------------------------------------------------------

(deftest "staged-admission-refuses" "docs/SPEC-WORK.md:3859,5685"
    "expected=stale-or-failed-stage-writes-nothing;status-answers-while-the-stage-runs"
  (let ((state (make-assignment-state :who "glenn" :free-slots 4)))
    ;; a verifier that returns after a conflicting revision is a stale stage.
    (let ((staged (stage-provenance "o-1 bytes" #'honest-verifier :rev 5)))
      (multiple-value-bind (ok line code)
          (assignment-acknowledge state :received :staged staged :offer-id "o-1" :current-rev 6)
        (check-equal nil ok "a stage from a conflicting revision is refused")
        (check-equal 2 code "the stale stage refusal is exit 2")
        (ok (search "stale" line) "the refusal names stale: ~A" line)))
    ;; a payload/verifier that fails validation writes nothing.
    (let ((staged (stage-provenance "o-1 bytes" #'failing-verifier :rev 6)))
      (check-equal nil (staged-input-valid-p staged) "a failed stage is invalid")
      (multiple-value-bind (ok line code)
          (assignment-acknowledge state :accepted :staged staged :offer-id "o-1" :current-rev 6)
        (check-equal nil ok "a failed stage is refused")
        (check-equal 2 code "the failed stage refusal is exit 2")))
    ;; neither wrote a reservation, receipt, lease or W change.
    (check-equal '() (assignment-state-deliveries state) "no receipt was written")
    (check-equal '() (assignment-state-acceptances state) "no accepted ownership was written")
    (check-equal '() (assignment-state-declines state) "no refusal was written")
    (check-equal '() (assignment-state-reservations state) "no reservation was written")
    (check-equal nil (assignment-state-leases state) "no lease was written")
    (check-equal '() (assignment-state-w state) "no W change was written")
    (check-equal 4 (assignment-state-free-slots state) "no capacity was spent")
    ;; session status answers while a stage is still running.
    (multiple-value-bind (ok line) (session-status state)
      (ok ok "session status answers while the stage runs: ~A" line)
      (ok (search "SESSION OK" line) "status prints its line: ~A" line)))
  ;; The one writer revalidates --expect, the offer's immutable tuple, the
  ;; profile and the capacity, and the offered payload's staged digest, before
  ;; admitting one envelope (SPEC-WORK.md:3854-3858, 5839-5840).
  (let* ((bytes "o-1 bytes")
         (staged (stage-provenance bytes #'honest-verifier :rev 6))
         (fresh (make-assignment-state
                 :who "glenn" :free-slots 4
                 :offers (list (list :offer-id "o-1" :node "n-1" :attempt "a-1")))))
    ;; a stage behind the writer's --expect.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 5)
      (check-equal nil ok "the writer refuses when --expect trails the revision")
      (check-equal 2 code "the stale --expect refusal is exit 2")
      (ok (search "expect" line) "the refusal names --expect: ~A" line))
    ;; the offer's immutable tuple is revalidated.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 6 :attempt "a-2")
      (check-equal nil ok "the writer refuses an offer tuple that moved")
      (check-equal 2 code "the moved-tuple refusal is exit 2")
      (ok (search "attempt" line) "the refusal names the attempt: ~A" line))
    ;; the profile is revalidated against the friend's CONFIG at that revision.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 6 :profile-ok nil)
      (check-equal nil ok "the writer refuses a profile that is not that friend's")
      (check-equal 2 code "the profile refusal is exit 2")
      (ok (search "profile" line) "the refusal names the profile: ~A" line))
    ;; the offered payload's staged digest must match --payload-sha256.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 6
                                :staged-payload (stage-payload "other payload")
                                :payload-sha256 (sha256-hex bytes))
      (check-equal nil ok "the writer refuses a mismatched staged payload")
      (check-equal 2 code "the payload refusal is exit 2")
      (ok (search "payload" line) "the refusal names the payload: ~A" line))
    ;; the capacity is revalidated at the write.
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 6 :reserve 5)
      (check-equal nil ok "the writer refuses more than the free capacity")
      (check-equal 2 code "the capacity refusal is exit 2")
      (ok (search "capacity" line) "the refusal names the capacity: ~A" line))
    ;; none of the refusals wrote, and a fully revalidated stage is admitted.
    (check-equal '() (assignment-state-deliveries fresh)
                 "no refused stage wrote a receipt")
    (multiple-value-bind (ok line code)
        (assignment-acknowledge fresh :received :staged staged :offer-id "o-1"
                                :current-rev 6 :expect 6 :node "n-1" :attempt "a-1"
                                :staged-payload (stage-payload bytes)
                                :payload-sha256 (sha256-hex bytes))
      (ok ok "a stage that revalidates is admitted: ~A" line)
      (check-equal 0 code "the admitted stage exits 0"))
    (check-equal 1 (length (assignment-state-deliveries fresh))
                 "exactly one admitted envelope wrote exactly one receipt")
    (multiple-value-bind (ok line) (session-status fresh)
      (ok ok "session status still answers: ~A" line))))
