;;;; replays-execution-reconcile.lisp --- `execution reconcile` and `execution
;;;; status` over the kernel's own controls (docs/SPEC-WORK.md:3997-4019, output
;;;; grammar :6020-6023). The subject is src/execution-reconcile.lisp.
;;;;
;;;; Every case here runs over a real kernel: a durable hold installed by
;;;; `execution-stop`, real attempts and offers in the kernel's control state,
;;;; and the kernel's own journal as the only store of what a reconcile
;;;; retained. Each fake-journal case has a real-file-journal twin that closes
;;;; the journal, reopens it, recovers into a brand-new kernel and replays.

(in-package #:nova-work/tests)

(defun obs (control attempt outcome at &rest extra)
  "One manifest record. EXTRA comes first so a case can override any default
(a duplicate key takes its leftmost value)."
  (apply #'make-observation
         (append extra
                 (list :control control :attempt attempt :outcome outcome
                       :observed-at at :source "probe/1"
                       :offer (format nil "offer-~A" attempt)
                       :generation 1
                       :handle (format nil "pid/~A" attempt)))))

(defun reconcile-seed-kernel (&key journal (control "ctl-rec-1")
                                   (attempts '("att-1" "att-2")))
  "A kernel holding ATTEMPTS on one node under a durable stop hold named
CONTROL. The hold is what `execution reconcile` reconciles against."
  (let ((k (if journal (fresh :journal journal) (fresh))))
    (dolist (a attempts)
      (record-attempt k "acme/work/f1/t1" a))
    (multiple-value-bind (ok line) (execution-stop k :scope '(:node "acme/work/f1/t1")
                                                     :request control)
      (ok ok "execution stop refused: ~A" line))
    k))

;;; ------------------------------------------------------------------
;;; Contradictions are preserved unresolved, never last-write-wins.
;;; SPEC-WORK.md:4009-4010.
;;; ------------------------------------------------------------------

(deftest "reconcile-preserves-contradiction-over-the-kernel" "docs/SPEC-WORK.md:4009"
    "expected=both-records-retained;disposition=unresolved;a-later-manifest-does-not-settle-it"
  (let* ((k (reconcile-seed-kernel))
         (running (obs "ctl-rec-1" "att-1" :running "2026-09-19T12:00:00Z"))
         (stopped (obs "ctl-rec-1" "att-1" :stopped "2026-09-19T12:01:00Z")))
    (multiple-value-bind (okp line code answer)
        (execution-reconcile k :control "ctl-rec-1" :records (list running stopped)
                               :request "rec-1")
      (ok okp "reconcile refused: ~A" line)
      (check-equal 0 code "reconcile exit code")
      (ok (search "change=reconcile" line) "the OK line does not name the change: ~A" line)
      (ok (search "operation=-" line) "a reconcile's operation= is not - : ~A" line)
      (check-equal 2 (length (getf answer :retained)) "a record was dropped"))
    ;; The disposition is unresolved and both outcomes are still readable.
    (let ((retained (control-observations k "ctl-rec-1")))
      (check-equal :unresolved (target-disposition retained "att-1")
                   "a contradiction was settled")
      (check-equal '(:running :stopped)
                   (mapcar #'observation-outcome retained)
                   "the retained outcomes are not both there, in order"))
    ;; A second manifest keeps the first: a later reconciliation records a new
    ;; set and keeps the earlier one, contradictions included.
    (multiple-value-bind (okp line)
        (execution-reconcile k :control "ctl-rec-1"
                               :records (list (obs "ctl-rec-1" "att-1" :completed
                                                   "2026-09-19T12:02:00Z"))
                               :request "rec-2")
      (ok okp "the second reconcile refused: ~A" line))
    (let ((retained (control-observations k "ctl-rec-1")))
      (check-equal 3 (length retained) "the earlier manifest was replaced, not kept")
      (check-equal :unresolved (target-disposition retained "att-1")
                   "the newest record settled a contradiction: last-write-wins"))
    ;; The status counts it as unresolved and the row prints it.
    (multiple-value-bind (okp line code rows) (execution-status k :control "ctl-rec-1")
      (ok okp "status refused: ~A" line)
      (check-equal 0 code "status exit code")
      (ok (search "unresolved=1" line) "status did not count the contradiction: ~A" line)
      (ok (search "pending-delivery=1" line)
          "the untouched target is not pending-delivery: ~A" line)
      (ok (search "acknowledged=0" line)
          "a reconcile invented an acknowledgement receipt: ~A" line)
      (check-equal 2 (length rows) "one row per target")
      (ok (search "disposition=unresolved" (first rows))
          "the row does not carry the disposition: ~A" (first rows))
      (ok (search "node=acme/work/f1/t1" (first rows))
          "the row does not name the node: ~A" (first rows)))))

;;; ------------------------------------------------------------------
;;; The manifest is content-addressed and its records bind their control.
;;; SPEC-WORK.md:4003-4006. A refusal writes nothing.
;;; ------------------------------------------------------------------

(deftest "reconcile-refuses-and-writes-nothing" "docs/SPEC-WORK.md:4003"
    "expected=no-such-control;content-address-mismatch;eighth-outcome;unbound-record;over-bound=refused,nothing-written"
  (let* ((k (reconcile-seed-kernel))
         (record (obs "ctl-rec-1" "att-1" :running "2026-09-19T12:00:00Z"))
         (address (observation-manifest-id (list record))))
    (flet ((refused (what &rest args)
             (multiple-value-bind (okp line code) (apply #'execution-reconcile k args)
               (ok (not okp) "~A was admitted: ~A" what line)
               (check-equal 1 code (format nil "~A exit code" what))
               (ok (search "EXECUTION FAIL" line) (format nil "~A line shape: ~A" what line))
               line)))
      ;; A manifest whose --from is not the records' content address.
      (let ((line (refused "a content-address mismatch"
                           :control "ctl-rec-1" :records (list record)
                           :from "0000" :request "bad-1")))
        (ok (search "content address" line) "the refusal does not name it: ~A" line))
      ;; An outcome outside the seven.
      (refused "an eighth outcome"
               :control "ctl-rec-1" :request "bad-2"
               :records (list (obs "ctl-rec-1" "att-1" :vanished "2026-09-19T12:00:00Z")))
      ;; A record binding no attempt.
      (refused "an unbound record"
               :control "ctl-rec-1" :request "bad-3"
               :records (list (make-observation :control "ctl-rec-1" :outcome :running
                                                :observed-at "2026-09-19T12:00:00Z"
                                                :source "probe/1")))
      ;; A record bound to another control.
      (refused "a record of another control"
               :control "ctl-rec-1" :request "bad-4"
               :records (list (obs "ctl-other" "att-1" :running "2026-09-19T12:00:00Z")))
      ;; An empty manifest, and one past its bound.
      (refused "an empty manifest" :control "ctl-rec-1" :records '() :request "bad-5")
      (refused "a manifest past its bound"
               :control "ctl-rec-1" :request "bad-6" :bound 1
               :records (list record (obs "ctl-rec-1" "att-2" :running "2026-09-19T12:00:00Z")))
      ;; A control that does not exist -- checked after the payload, never before.
      (let ((line (refused "an unknown control"
                           :control "ctl-missing" :request "bad-7"
                           :records (list (obs "ctl-missing" "att-1" :running
                                               "2026-09-19T12:00:00Z")))))
        (ok (search "no such control" line) "the refusal does not name it: ~A" line)))
    ;; Nothing was written by any of them.
    (check-equal '() (control-observations k "ctl-rec-1")
                 "a refused manifest was retained")
    (check-equal '() (control-reconciliations k "ctl-rec-1")
                 "a refused manifest left a record")
    ;; And the address the engine computes is the one a caller may name.
    (multiple-value-bind (okp line)
        (execution-reconcile k :control "ctl-rec-1" :records (list record)
                               :from address :request "good-1")
      (ok okp "the records' own content address was refused: ~A" line))))

;;; ------------------------------------------------------------------
;;; A retry answers the original reply and applies no second manifest.
;;; SPEC-WORK.md:315, :2879.
;;; ------------------------------------------------------------------

(deftest "reconcile-retry-answers-the-original-reply" "docs/SPEC-WORK.md:315"
    "expected=same-request-same-manifest=replayed;same-request-other-manifest=refused"
  (let* ((k (reconcile-seed-kernel))
         (records (list (obs "ctl-rec-1" "att-1" :running "2026-09-19T12:00:00Z"))))
    (multiple-value-bind (okp line) (execution-reconcile k :control "ctl-rec-1"
                                                           :records records :request "rr-1")
      (ok okp "reconcile refused: ~A" line))
    (multiple-value-bind (okp line code answer)
        (execution-reconcile k :control "ctl-rec-1" :records records :request "rr-1")
      (ok okp "the retry was refused: ~A" line)
      (check-equal 0 code "retry exit code")
      (ok (getf answer :replayed) "the retry applied a second manifest"))
    (check-equal 1 (length (control-reconciliations k "ctl-rec-1"))
                 "the retry recorded a second manifest")
    ;; The same request id under different records is a conflict, not a replay.
    (multiple-value-bind (okp line code)
        (execution-reconcile k :control "ctl-rec-1" :request "rr-1"
                               :records (list (obs "ctl-rec-1" "att-2" :running
                                                   "2026-09-19T12:03:00Z")))
      (ok (not okp) "a reused request id took new records: ~A" line)
      (check-equal 1 code "conflict exit code")
      (ok (search "reused with a different payload" line) "the conflict is not named: ~A" line))
    (check-equal 1 (length (control-reconciliations k "ctl-rec-1"))
                 "the conflict wrote a manifest")))

;;; ------------------------------------------------------------------
;;; `not-started` alone releases an unlaunched reservation, and a confirmed
;;; stop signs for no holder. SPEC-WORK.md:4011-4018.
;;; ------------------------------------------------------------------

(deftest "only-a-qualifying-not-started-releases-a-reservation" "docs/SPEC-WORK.md:4011"
    "expected=durable-reject=released;queue-miss=held;unknown=held;confirmed-stop=holder-only-release-not-bypassed"
  (let ((k (fresh)))
    (dolist (a '("att-1" "att-2" "att-3" "att-4" "att-5"))
      (record-attempt k "acme/work/f1/t1" a))
    ;; The reservations are prepared before the hold: an offer under a hold is
    ;; refused at admission (src/control.lisp:256).
    (dolist (a '("att-1" "att-2" "att-3" "att-4" "att-5"))
      (prepare-offer k (format nil "offer-~A" a) "acme/work/f1/t1" a))
    ;; att-4 alone was launched.
    (send-offer k "offer-att-4")
    (execution-stop k :scope '(:node "acme/work/f1/t1") :request "ctl-rel-1")
    (multiple-value-bind (okp line code answer)
        (execution-reconcile
         k :control "ctl-rel-1" :request "rel-1"
         :records (list
                   ;; a durable launch rejection: this one releases
                   (obs "ctl-rel-1" "att-1" :not-started "2026-09-19T12:00:00Z"
                        :launch-rejects-p t)
                   ;; a queue miss is not a durable rejection
                   (obs "ctl-rel-1" "att-2" :not-started "2026-09-19T12:00:00Z"
                        :launch-rejects-p t :queue-miss-p t)
                   ;; an unknown execution's capacity is never advertised free
                   (obs "ctl-rel-1" "att-3" :unknown "2026-09-19T12:00:00Z")
                   ;; a confirmed stop: capacity reconciliation, not a release
                   (obs "ctl-rel-1" "att-4" :stopped "2026-09-19T12:00:00Z")
                   ;; a confirmed stop on a reservation that never launched:
                   ;; still not a release, because a release is the holder's
                   (obs "ctl-rel-1" "att-5" :stopped "2026-09-19T12:00:00Z")))
      (ok okp "reconcile refused: ~A" line)
      (check-equal 0 code "reconcile exit code")
      (check-equal '("att-1") (getf answer :released)
                   "the wrong set of reservations was released"))
    (check-equal :released (offer-effect k "offer-att-1") "the qualifying not-started held")
    (check-equal :prepared (offer-effect k "offer-att-2") "a queue miss released a reservation")
    (check-equal :prepared (offer-effect k "offer-att-3") "an unknown released a reservation")
    (check-equal :dispatched (offer-effect k "offer-att-4")
                 "a confirmed stop signed for the holder")
    (check-equal :prepared (offer-effect k "offer-att-5")
                 "a confirmed stop released an unlaunched reservation the holder holds")
    (let ((retained (control-observations k "ctl-rel-1")))
      (check-equal :confirmed (target-disposition retained "att-4")
                   "an observed stop is not confirmed")
      (check-equal :unresolved (target-disposition retained "att-3")
                   "an unknown outcome settled its target")
      ;; Missing usage stays unknown: a stop report never synthesises zero cost.
      (check-equal :unknown (target-usage retained "att-4")
                   "a stop report synthesised a usage"))))

;;; ------------------------------------------------------------------
;;; Silence, an expired lease and an elapsed estimate are not stop evidence.
;;; SPEC-WORK.md:4008-4009.
;;; ------------------------------------------------------------------

(deftest "a-stop-that-is-not-evidence-settles-nothing" "docs/SPEC-WORK.md:4008"
    "expected=silence|expired-lease|elapsed-estimate|unbound-negative-lookup=unresolved"
  (let ((k (reconcile-seed-kernel :control "ctl-ev-1" :attempts '("att-1" "att-2" "att-3" "att-4"))))
    (execution-reconcile
     k :control "ctl-ev-1" :request "ev-1"
     :records (list (obs "ctl-ev-1" "att-1" :stopped "2026-09-19T12:00:00Z"
                         :evidence-quality :silence)
                    (obs "ctl-ev-1" "att-2" :stopped "2026-09-19T12:00:00Z"
                         :evidence-quality :expired-lease)
                    (obs "ctl-ev-1" "att-3" :stopped "2026-09-19T12:00:00Z"
                         :evidence-quality :elapsed-estimate)
                    (obs "ctl-ev-1" "att-4" :stopped "2026-09-19T12:00:00Z"
                         :negative-lookup-p t)))
    (let ((retained (control-observations k "ctl-ev-1")))
      (dolist (a '("att-1" "att-2" "att-3" "att-4"))
        (check-equal :unresolved (target-disposition retained a)
                     (format nil "~A: a stop that is not evidence confirmed a target" a))))
    ;; The same negative lookup, bound to its execution identity, is evidence.
    (execution-reconcile
     k :control "ctl-ev-1" :request "ev-2"
     :records (list (obs "ctl-ev-1" "att-4" :stopped "2026-09-19T12:05:00Z"
                         :negative-lookup-p t :identity-bound-p t)))
    (check-equal :confirmed (target-disposition (control-observations k "ctl-ev-1") "att-4")
                 "a bound negative lookup is not stop evidence")))

;;; ------------------------------------------------------------------
;;; The rows are bounded by --max. SPEC-WORK.md:3998-3999.
;;; ------------------------------------------------------------------

(deftest "execution-status-rows-are-bounded-by-max" "docs/SPEC-WORK.md:3998"
    "expected=selected=all;shown<=max;one-row-per-shown-target"
  (let ((k (reconcile-seed-kernel :control "ctl-max-1"
                                  :attempts '("att-1" "att-2" "att-3"))))
    (multiple-value-bind (okp line code rows) (execution-status k :control "ctl-max-1" :max 2)
      (ok okp "status refused: ~A" line)
      (check-equal 0 code "status exit code")
      (ok (search "selected=3" line) "selected does not count every target: ~A" line)
      (ok (search "shown=2" line) "shown is not the bound: ~A" line)
      (check-equal 2 (length rows) "--max did not bound the rows"))
    (multiple-value-bind (okp line) (execution-status k :control "ctl-missing")
      (ok (not okp) "a status of an unknown control answered: ~A" line)
      (ok (search "no such control" line) "the refusal is not named: ~A" line))))

;;; ------------------------------------------------------------------
;;; The durable half: a fake journal, then the real-file-journal twin with
;;; close, reopen and replay. SPEC-WORK.md:3962, :4002.
;;; ------------------------------------------------------------------

(deftest "reconcile-is-durable-before-it-acknowledges" "docs/SPEC-WORK.md:3962"
    "expected=record-before-ack;a-new-kernel-over-the-same-journal-answers-the-same"
  (let* ((journal (make-ordering-journal))
         (k (reconcile-seed-kernel :journal journal :control "ctl-dur-1")))
    (execution-reconcile k :control "ctl-dur-1" :request "dur-1"
                           :records (list (obs "ctl-dur-1" "att-1" :running "2026-09-19T12:00:00Z")
                                          (obs "ctl-dur-1" "att-1" :stopped "2026-09-19T12:01:00Z")))
    ;; A crash: a brand-new kernel over the same journal, no memory of either
    ;; the hold or the manifest.
    (let ((k2 (fresh :journal journal)))
      (recover-controls k2)
      (check-equal 1 (length (kernel-holds k2)) "the hold did not recover")
      (let ((retained (control-observations k2 "ctl-dur-1")))
        (check-equal 2 (length retained) "the manifest did not recover")
        (check-equal '(:running :stopped) (mapcar #'observation-outcome retained)
                     "the recovered records are not the admitted ones")
        (check-equal :unresolved (target-disposition retained "att-1")
                     "the recovered contradiction was settled"))
      (multiple-value-bind (okp line) (execution-status k2 :control "ctl-dur-1")
        (ok okp "status after recovery refused: ~A" line)
        (ok (search "unresolved=1" line) "the recovered status lost the contradiction: ~A" line)))))

(deftest "reconcile-is-durable-in-a-real-file-journal" "docs/SPEC-WORK.md:3962"
    "expected=close-reopen-replay-answers-the-same-records-and-the-same-hold"
  (let* ((path (test-journal-path "execution-reconcile"))
         (init (root-digest (make-seed-state *seed*)))
         (j1 (open-file-journal path :initial-state-hash init)))
    (unwind-protect
         (let ((k (reconcile-seed-kernel :journal j1 :control "ctl-file-1")))
           (multiple-value-bind (okp line)
               (execution-reconcile k :control "ctl-file-1" :request "file-1"
                                      :records (list (obs "ctl-file-1" "att-1" :running
                                                          "2026-09-19T12:00:00Z")
                                                     (obs "ctl-file-1" "att-1" :stopped
                                                          "2026-09-19T12:01:00Z")))
             (ok okp "reconcile over a real journal refused: ~A" line)))
      (close-file-journal j1))
    ;; Reopen the file, recover into a brand-new kernel, replay.
    (let ((j2 (open-file-journal path :initial-state-hash init)))
      (unwind-protect
           (let ((k2 (fresh :journal j2)))
             (replay-journal j2 k2)
             (recover-controls k2)
             (check-equal 1 (length (kernel-holds k2)) "the hold did not survive the file")
             (check-equal '("att-1" "att-2") (hold-targets (first (kernel-holds k2)))
                          "the recovered hold lost its captured targets")
             (let ((retained (control-observations k2 "ctl-file-1")))
               (check-equal 2 (length retained) "the manifest did not survive the file")
               (check-equal '(:running :stopped) (mapcar #'observation-outcome retained)
                            "the reopened records are not the admitted ones")
               (check-equal :unresolved (target-disposition retained "att-1")
                            "the reopened contradiction was settled"))
             ;; The retry after recovery is still answered from the record.
             (multiple-value-bind (okp line code answer)
                 (execution-reconcile k2 :control "ctl-file-1" :request "file-1"
                                         :records (list (obs "ctl-file-1" "att-1" :running
                                                             "2026-09-19T12:00:00Z")
                                                        (obs "ctl-file-1" "att-1" :stopped
                                                             "2026-09-19T12:01:00Z")))
               (ok okp "the retry after recovery was refused: ~A" line)
               (check-equal 0 code "retry exit code")
               (ok (getf answer :replayed) "the retry after recovery applied a second manifest")))
        (close-file-journal j2)))))
