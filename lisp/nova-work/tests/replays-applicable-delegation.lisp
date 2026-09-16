;;;; replays-applicable-delegation.lisp --- the seven applicable/delegation
;;;; acceptance replays of docs/SPEC-WORK.md:4472-4490 (the "Required replays"
;;;; table). Each deftest names the Required outcome the table states.
;;;;
;;;; The pure data model these test is the coordinator's notes, the read-before-
;;;; the-route `applicable`, the six delegation gates, and the machinery receipt
;;;; bound to an exact head (docs/SPEC-WORK.md:4140-4470). The live-session,
;;;; CLI and card-builder wiring that still lives in other slices is kept out of
;;;; this file; here the pure part is exercised directly and the wiring owed is
;;;; listed in RESULT.md.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; helpers
;;; ------------------------------------------------------------------

(defun applicable-verdict (answer candidate)
  (cdr (assoc candidate (applicable-answer-verdicts answer) :test #'equal)))

(defun no-eligible-p (answer)
  (notany (lambda (pair) (verdict-eligible-p (cdr pair)))
          (applicable-answer-verdicts answer)))

;;; ------------------------------------------------------------------
;;; applicable-before-route                     SPEC-WORK.md:4331
;;; ------------------------------------------------------------------

(deftest "applicable-before-route" "docs/SPEC-WORK.md:4331"
    "expected=eligible-only-priced;excluded-names-note;unknown-names-reason"
  (let* ((deny (make-note :scope '(:coordinator "stella")
                          :author "glenn" :date "2026-09-14T20:00Z"
                          :source "\"Astra default is for coordination and thinking. Not for coding.\""
                          :kind :instruction
                          :text "no coding on Astra"
                          :constraint '(:constraint
                                        (:deny (:model "astra"))
                                        (:prefer ())
                                        (:reason "coordinator instruction"))))
         (price-table '(("astra" . 50) ("beta" . 10)))
         (models '("astra" "beta")))
    (multiple-value-bind (answer priced)
        (route-selection "stella" :coding
                         '("coordinator/astra" "coordinator/beta" "coordinator/gamma")
                         (list deny) price-table :models models)
      ;; only the eligible route is priced; the excluded and unknown are not.
      (check-equal '("coordinator/beta") (mapcar #'car priced)
                   "only the eligible route is priced")
      ;; a card builder handed an excluded route refuses, naming the note id.
      (multiple-value-bind (ok line)
          (build-card "coordinator/astra" (applicable-verdict answer "coordinator/astra"))
        (check-equal nil ok "an excluded route is refused a card")
        (ok (search (getf deny :id) line)
            "the excluded refusal names the note id: ~A" line))
      ;; a card builder handed an unknown route refuses, naming the reason.
      (multiple-value-bind (ok line)
          (build-card "coordinator/gamma" (applicable-verdict answer "coordinator/gamma"))
        (check-equal nil ok "an unknown route is refused a card")
        (ok (search "not registered" line)
            "the unknown refusal names the reason: ~A" line)))))

;;; ------------------------------------------------------------------
;;; applicable-unknown-is-not-eligible          SPEC-WORK.md:4346
;;; ------------------------------------------------------------------

(deftest "applicable-unknown-is-not-eligible" "docs/SPEC-WORK.md:4346"
    "expected=fail-no-eligible-on-no-source;fail-on-past-bound;fail-on-missing-index;unknown-model-not-eligible"
  ;; no live session and no --snapshot
  (let ((a (applicable "stella" :coding '("coordinator/astra") '()
                       :source :none :models '("astra"))))
    (ok (applicable-answer-fail a) "no source prints NOTES FAIL")
    (check-equal t (no-eligible-p a) "no source: no candidate prints eligible"))
  ;; a snapshot past a bound
  (let ((a (applicable "stella" :coding '("coordinator/astra") '()
                       :source :snapshot :snapshot-bound-ok nil :models '("astra"))))
    (ok (applicable-answer-fail a) "a snapshot past a bound prints NOTES FAIL")
    (check-equal t (no-eligible-p a) "past-bound snapshot: no candidate eligible"))
  ;; a missing notes index
  (let ((a (applicable "stella" :coding '("coordinator/astra") '()
                       :source :live :notes-index nil :models '("astra"))))
    (ok (applicable-answer-fail a) "a missing notes index prints NOTES FAIL")
    (check-equal t (no-eligible-p a) "missing index: no candidate eligible"))
  ;; an unregistered model is unknown, never eligible
  (let ((a (applicable "stella" :coding '("coordinator/astra") '()
                       :source :live :models '())))
    (check-equal :unknown (applicable-verdict a "coordinator/astra")
                 "an unregistered model is unknown")
    (check-equal t (no-eligible-p a) "unregistered model: no candidate eligible")))

;;; ------------------------------------------------------------------
;;; applicable-snapshot-is-planning-only        SPEC-WORK.md:4351
;;; ------------------------------------------------------------------

(deftest "applicable-snapshot-is-planning-only" "docs/SPEC-WORK.md:4351"
    "expected=snapshot-admits-no-route;admission-expects-rev;stale-after-intervening-write"
  ;; an answer from=snapshot admits no route: the live-eligible row is planning only.
  (let ((a (applicable "stella" :coding '("coordinator/beta") '()
                       :rev 5 :source :snapshot :models '("beta"))))
    (check-equal :planning (applicable-verdict a "coordinator/beta")
                 "a snapshot row is planning, not eligible")
    (check-equal t (no-eligible-p a) "a snapshot answer admits no route"))
  ;; the admission write carries --expect the live rev=
  (check-equal 5
               (admission-expect
                (applicable "stella" :coding '("coordinator/beta") '()
                            :rev 5 :source :live :models '("beta")))
               "the admission write carries --expect the live rev=")
  ;; a stop or deny written between the check and the admission refuses it stale.
  (multiple-value-bind (ok line code)
      (admission :expect 5 :current-rev 6)
    (check-equal nil ok "admission after an intervening write is refused")
    (check-equal 2 code "the stale refusal is exit 2")
    (ok (search "stale" line) "the refusal names stale: ~A" line)))

;;; ------------------------------------------------------------------
;;; narrative-does-not-filter                  SPEC-WORK.md:4364
;;; ------------------------------------------------------------------

(deftest "narrative-does-not-filter" "docs/SPEC-WORK.md:4364"
    "expected=prose-prints-excludes-nothing;deny-matching-every-axis-excludes;disagreeing-constraints-both-exclude"
  ;; a note with prose and no :constraint is printed and excludes nothing.
  (let* ((prose (make-note :scope '(:node) :author "glenn" :date "2026-01-01T00:00Z"
                           :source "meeting" :kind :observation
                           :text "weigh routing price against context and rework"))
         (a (applicable "stella" :coding '("coordinator/beta")
                        (list prose) :source :live :models '("beta"))))
    (ok (member prose (applicable-answer-notes a) :test #'equal)
        "the narrative note is printed by applicable")
    (check-equal t (verdict-eligible-p (applicable-verdict a "coordinator/beta"))
                 "a narrative note excludes nothing"))
  ;; a :deny matching every named axis excludes.
  (let* ((deny (make-note :scope '(:coordinator "stella") :author "glenn"
                          :date "2026-09-14T20:00Z" :source "quote" :kind :instruction
                          :text "x"
                          :constraint '(:constraint
                                        (:deny (:model "astra") (:role "coordinator")
                                               (:task-class :coding))
                                        (:prefer ()) (:reason "r"))))
         (a (applicable "stella" :coding '("coordinator/astra")
                        (list deny) :source :live :models '("astra"))))
    (check-equal t (verdict-excluded-p (applicable-verdict a "coordinator/astra"))
                 "a :deny matching every named axis excludes"))
  ;; two disagreeing constraints print both and exclude.
  (let* ((n1 (make-note :scope '(:coordinator "stella") :author "glenn"
                        :date "2026-09-14T20:00Z" :source "q1" :kind :instruction
                        :text "a"
                        :constraint '(:constraint (:deny (:model "astra"))
                                                   (:prefer ()) (:reason "one"))))
         (n2 (make-note :scope '(:coordinator "stella") :author "glenn"
                        :date "2026-09-14T21:00Z" :source "q2" :kind :instruction
                        :text "b"
                        :constraint '(:constraint (:deny (:model "astra") (:role "coordinator"))
                                                   (:prefer ()) (:reason "two"))))
         (a (applicable "stella" :coding '("coordinator/astra")
                        (list n1 n2) :source :live :models '("astra"))))
    (ok (and (member n1 (applicable-answer-notes a) :test #'equal)
             (member n2 (applicable-answer-notes a) :test #'equal))
        "two disagreeing constraints are both printed")
    (let ((v (applicable-verdict a "coordinator/astra")))
      (check-equal t (verdict-excluded-p v) "disagreeing constraints exclude")
      (check-equal 2 (length (excluded-note-ids v))
                   "both note ids are named in the exclusion"))))

;;; ------------------------------------------------------------------
;;; delegation-admission-gates                 SPEC-WORK.md:4378
;;; ------------------------------------------------------------------

(deftest "delegation-admission-gates" "docs/SPEC-WORK.md:4378"
    "expected=gate2-names-missing-field;gate3-names-ceiling"
  (let ((complete '(:objective "ship the task" :source-revision "abc123"
                    :criteria ("acceptance criterion 1")
                    :scope (:node) :result-contract "done" :checkpoint "cp-1"
                    :effort 3)))
    ;; a complete packet passes admission.
    (multiple-value-bind (ok line code) (delegation-admission complete)
      (ok ok "a complete packet is admitted: ~A" line)
      (check-equal 0 code "admission exit code"))
    ;; a packet lacking any one of the seven is refused at gate 2, exit 2, by name.
    (dolist (field *delegation-packet-fields*)
      (let ((packet (loop for (k v) on complete by #'cddr
                          unless (eq k field) append (list k v))))
        (multiple-value-bind (ok line code) (delegation-admission packet)
          (ok (null ok) "a packet lacking ~A is refused" field)
          (check-equal 2 code "gate 2 refusal is exit 2")
          (ok (search "gate 2" line) "the refusal names its gate: ~A" line)
          (ok (search (string-downcase (symbol-name field)) line)
              "the refusal names the missing field ~A: ~A" field line))))
    ;; a dispatch that would cross the daily spend ceiling is refused at gate 3, naming it.
    (multiple-value-bind (ok line code)
        (delegation-admission complete :dispatch-cost 40 :spent-today 70 :ceiling 100)
      (check-equal nil ok "an over-ceiling dispatch is refused")
      (check-equal 2 code "gate 3 refusal is exit 2")
      (ok (search "gate 3" line) "the refusal names its gate: ~A" line)
      (ok (search "100" line) "the refusal names the ceiling: ~A" line))))

;;; ------------------------------------------------------------------
;;; delegation-result-gates                    SPEC-WORK.md:4390
;;; ------------------------------------------------------------------

(deftest "delegation-result-gates" "docs/SPEC-WORK.md:4390"
    "expected=gate4-refuses-asleep-unknown;limit-vs-expiry-separate;timeout-not-termination;no-silent-retry"
  ;; an offer to a friend reading asleep or unknown is refused at gate 4.
  (dolist (state '(:asleep :unknown))
    (multiple-value-bind (ok line code) (offer-to state)
      (ok (null ok) "an offer to an ~A recipient is refused" state)
      (check-equal 2 code "gate 4 refusal is exit 2")
      (ok (search "gate 4" line) "the refusal names gate 4: ~A" line)))
  ;; the requested execution limit and the observed expiry/stop are recorded separately.
  (let ((r (make-execution-record :requested-limit 10
                                  :expiry "2026-09-15T00:00Z"
                                  :stop "cpu spike")))
    (check-equal 10 (execution-record-requested-limit r) "requested limit recorded")
    (check-equal "2026-09-15T00:00Z" (execution-record-expiry r) "observed expiry recorded")
    (check-equal "cpu spike" (execution-record-stop r) "observed stop recorded")
    (check-equal t (and (not (equal 10 (execution-record-expiry r)))
                        (not (equal 10 (execution-record-stop r))))
                 "the observed expiry or stop is recorded apart from the requested limit"))
  ;; a timeout is not termination.
  (check-equal t (non-terminating-p :timeout) "a timeout is not termination")
  (check-equal nil (non-terminating-p :terminated) "termination is termination")
  ;; no second attempt starts silently after uncertainty about the first.
  (check-equal nil (retry-permitted-p :unknown)
               "an uncertain outcome permits no silent second attempt")
  (check-equal t (retry-permitted-p :terminated)
               "a determinate outcome does not forbid reconsideration"))

;;; ------------------------------------------------------------------
;;; receipt-at-exact-head                      SPEC-WORK.md:4395
;;; ------------------------------------------------------------------

(deftest "receipt-at-exact-head" "docs/SPEC-WORK.md:4395"
    "expected=receipt-booked-at-exact-head;review-binds-to-head;not-reused-across-head-or-unchecked-rebase"
  ;; a child's result is booked as a machinery receipt at the exact head it ran against.
  (let ((r (book-receipt "abc123" '(:done nil))))
    (check-equal "abc123" (machinery-receipt-head r) "receipt booked at the exact head")
    (check-equal '(:done nil) (machinery-receipt-result r) "receipt carries the child result"))
  ;; a review verdict binds to --head <sha>.
  (let ((v '(:head "abc123" :verdict :approved)))
    (check-equal t (review-binds-p v "abc123") "a verdict binds to its head")
    (check-equal nil (review-binds-p v "999999") "a verdict does not bind to another head")
    ;; and is not reused across a changed head or an unchecked rebase.
    (check-equal nil (review-reusable-p v "999999") "a verdict is not reused across a changed head")
    (check-equal nil (review-reusable-p v "abc123" :rebase-checked-p nil)
                 "a verdict is not reused across an unchecked rebase")
    (check-equal t (review-reusable-p v "abc123")
                 "a verdict is reusable while head, content and dependencies are unchanged")))
