;;;; replays-l3600-2.lisp --- the eight named acceptance replays of
;;;; docs/SPEC-WORK.md lines 3600-end, batch 2. Each deftest asserts the
;;;; observable the spec names for its replay, driven by the pure records and
;;;; functions in src/replays-l3600-2.lisp. The C/O transition kernel is slice
;;;; 1 only; what a replay needs from session/CLI/query/dispatch machinery is
;;;; implemented purely here and its kernel wiring is owed (see RESULT.md).

(in-package #:nova-work/tests)

;; ------------------------------------------------------------------
;; an-invalid-delta-leaves-the-old-config   SPEC-WORK.md:5615
;; ------------------------------------------------------------------

(deftest "an-invalid-delta-leaves-the-old-config" "docs/SPEC-WORK.md:5615"
    "expected=bounded;validated;atomic;invalid-delta-leaves-old-config"
  (let ((config '(:version 3 :roster "glenn" :window-days 2 :capacity 64 :retain-bytes 4096)))
    ;; A valid delta applies atomically and bumps the version once.
    (multiple-value-bind (new applied)
        (apply-config-delta config '(:base 3 :changes (:window-days 3)))
      (ok applied "the valid delta did not apply")
      (check-equal 4 (getf new :version) "the version did not advance by one")
      (check-equal 3 (getf new :window-days) "the changed field did not move")
      (check-equal "glenn" (getf new :roster) "an untouched field moved"))
    ;; An unknown key is refused and the old config left untouched.
    (multiple-value-bind (new applied)
        (apply-config-delta config '(:base 3 :changes (:credential "secret")))
      (ok (not applied) "a delta with an unknown key applied")
      (ok (equal config new) "a refused delta altered the old config"))
    ;; A stale base is refused.
    (multiple-value-bind (new applied)
        (apply-config-delta config '(:base 1 :changes (:capacity 32)))
      (ok (not applied) "a stale-base delta applied")
      (ok (equal config new) "a stale-base delta altered the old config"))
    ;; One bad field refuses the whole delta: nothing else applies.
    (multiple-value-bind (new applied)
        (apply-config-delta config '(:base 3 :changes (:window-days -1 :capacity 32)))
      (ok (not applied) "a delta with one bad field applied")
      (ok (equal config new) "a partial delta leaked a change"))))

;; ------------------------------------------------------------------
;; archive-completeness                    SPEC-WORK.md:6030
;; ------------------------------------------------------------------

(deftest "archive-completeness" "docs/SPEC-WORK.md:6030"
    "expected=every-gap-explicit;gap-prohibits-absorption"
  (let ((complete '(:gaps () :items (("i1") ("i2"))))
        (gappy    '(:gaps (:missing-attachment) :items (("i1")))))
    (ok (capture-complete-p complete) "a gap-free capture read as incomplete")
    ;; Every gap kind stays its own explicit entry and marks the capture.
    (dolist (kind *gap-kinds*)
      (let ((c (list :gaps (list kind) :items '(("i1")))))
        (check-equal (list kind) (capture-gaps c)
                     (format nil "~A was not kept explicit" kind))
        (ok (not (capture-complete-p c)) "~A read as complete" kind)))
    ;; A gap prohibits absorption: a gappy capture is never absorbed, and the
    ;; base is left unchanged rather than silently dropping the gap.
    (multiple-value-bind (result absorbed)
        (absorb-capture complete gappy)
      (ok (not absorbed) "a gappy capture was absorbed")
      (ok (equal complete result) "absorption dropped or altered the base"))))

;; ------------------------------------------------------------------
;; as-of-refuses-unavailable-partition     SPEC-WORK.md:5466
;; ------------------------------------------------------------------

(deftest "as-of-refuses-unavailable-partition" "docs/SPEC-WORK.md:5466"
    "expected=unavailable-partition-refused-and-named;never-answered-from-a-later-row"
  (let* ((rows '((:node "a" :day "2026-09-12" :rev 1)
                 (:node "a" :day "2026-09-13" :rev 2)
                 (:node "a" :day "2026-09-14" :rev 3)))
         (partitions '("2026-09-12" "2026-09-13")))
    ;; 2026-09-14 cannot be opened: refused, naming that partition, and never
    ;; answered from a later (or any) row.
    (multiple-value-bind (answer partition)
        (state-as-of rows partitions "2026-09-14")
      (check-equal :refused answer "an unavailable partition was answered")
      (check-equal "2026-09-14" partition "the refusal did not name its partition"))
    ;; An available day answers the newest row on or before it.
    (multiple-value-bind (answer partition)
        (state-as-of rows partitions "2026-09-13")
      (check-equal 2 answer "the wrong row answered an available partition")
      (check-equal nil partition "an available day printed a partition refusal"))))

;; ------------------------------------------------------------------
;; async-operations                        SPEC-WORK.md:6037
;; ------------------------------------------------------------------

(deftest "async-operations" "docs/SPEC-WORK.md:6037"
    "expected=no-double-launch;no-false-cancellation-success"
  (let ((op '(:id "op-1" :kind :import :state :pending :attempts 0)))
    ;; A launch moves pending -> running and counts one attempt.
    (let ((running (operation-launch op)))
      (check-equal :running (getf running :state) "a pending op did not enter running")
      (check-equal 1 (getf running :attempts) "the launch was not counted"))
    ;; Launching an already-running op is no double launch: attempts unchanged.
    (let* ((running '(:id "op-1" :kind :import :state :running :attempts 1))
           (relaunched (operation-launch running)))
      (check-equal :running (getf relaunched :state) "state changed on a second launch")
      (check-equal 1 (getf relaunched :attempts) "a running op was launched twice"))
    ;; Cancelling a pending op is a real cancellation.
    (check-equal :cancelled (getf (operation-cancel '(:id "o1" :kind :import :state :pending :attempts 0)) :state)
                 "cancel did not cancel a pending op")
    ;; Cancelling a finished op is never a false success: it reports already
    ;; complete and never claims a cancellation happened.
    (let ((done-cancel (operation-cancel '(:id "op-2" :kind :export :state :done :attempts 1))))
      (check-equal :already-complete (getf done-cancel :disposition)
                   "cancelling a finished op claimed a cancellation success")
      (check-equal :done (getf done-cancel :state)
                   "a finished op's state was falsified by cancellation"))))

;; ------------------------------------------------------------------
;; axisless-history                        SPEC-WORK.md:5714
;; ------------------------------------------------------------------

(deftest "axisless-history" "docs/SPEC-WORK.md:5714"
    "expected=ordered-rows;denominator-not-reduced-by-completion;retire-keeps-node"
  (let ((rows '()))
    (setf rows (add-ordered-row rows "a" '("e-a")))
    (setf rows (add-ordered-row rows "b" '("e-b")))
    (check-equal 2 (length rows) "the two rows were not kept")
    (check-equal '("a" "b") (mapcar (lambda (r) (getf r :id)) rows) "row order was not preserved")
    ;; Finishing a row does not reduce the denominator: completion is not removal.
    (setf rows (finish-row rows "a"))
    (check-equal 2 (active-denominator rows) "completion reduced the denominator")
    ;; A retired row records a scope movement and keeps its node.
    (setf rows (retire-row rows "b" "scope-2"))
    (let ((b (find "b" rows :key (lambda (r) (getf r :id)) :test #'string=)))
      (ok (getf b :retired) "the retired row was not marked retired")
      (check-equal "b" (getf b :node) "the retired row lost its node")
      (ok (getf b :scope-moved) "the retirement did not record the scope movement"))
    (check-equal 1 (active-denominator rows) "retirement failed to drop the denominator")))

;; ------------------------------------------------------------------
;; batches-and-pipelines                   SPEC-WORK.md:6038
;; ------------------------------------------------------------------

(deftest "batches-and-pipelines" "docs/SPEC-WORK.md:6038"
    "expected=atomic-all-or-none;prefix-exact;unattempted-marked"
  (let ((entries '((:id "e1" :payload 1)
                   (:id "e2" :payload 2)
                   (:id "e3" :payload 3))))
    (flet ((reject-middle (entry) (not (equal (getf entry :id) "e2"))))
      ;; Atomic: all-or-none. One bad entry publishes nothing.
      (multiple-value-bind (acc outcome)
          (apply-atomic-batch '() entries #'reject-middle)
        (check-equal :refused outcome "an atomic batch with a bad entry published")
        (check-equal '() acc "an atomic batch applied a partial prefix"))
      ;; An all-valid atomic batch applies everything, in order.
      (multiple-value-bind (acc outcome)
          (apply-atomic-batch '() entries (lambda (e) (declare (ignore e)) t))
        (check-equal :applied outcome "an all-valid atomic batch refused")
        (check-equal '("e1" "e2" "e3") acc "the atomic batch did not apply in order"))
      ;; Independent: exact accepted prefix applied, remainder not attempted.
      (multiple-value-bind (acc accepted not-attempted)
          (apply-independent-batch '() entries #'reject-middle)
        (check-equal '("e1") acc "the independent batch did not stop at the refusal")
        (check-equal '("e1") accepted "the accepted prefix was not exact")
        (check-equal '("e3") not-attempted "the remainder was not marked not-attempted")))))

;; ------------------------------------------------------------------
;; default-window-opens-two-days           SPEC-WORK.md:5441
;; ------------------------------------------------------------------

(deftest "default-window-opens-two-days" "docs/SPEC-WORK.md:5441"
    "expected=at-most-two-day-partitions;midnight-one;--from-only-record-days"
  ;; Early morning and midday open at most two UTC days; midnight exactly one.
  (check-equal '("2026-09-13" "2026-09-14")
               (window-days "2026-09-14T06:00:00Z") "early morning opened the wrong days")
  (check-equal '("2026-09-13" "2026-09-14")
               (window-days "2026-09-14T12:00:00Z") "midday opened the wrong days")
  (check-equal '("2026-09-14")
               (window-days "2026-09-14T00:00:00Z") "midnight opened more than one partition")
  ;; A --from reaching back a month opens exactly the days holding records.
  (let ((records '((:node "a" :day "2026-09-12")
                   (:node "b" :day "2026-09-14")
                   (:node "c" :day "2026-09-14"))))
    (check-equal '("2026-09-12" "2026-09-14")
                 (open-days-from records "2026-08-14")
                 "the from-range did not open exactly the record-holding days")))

;; ------------------------------------------------------------------
;; dispatch-ack-and-ownership-are-three    SPEC-WORK.md:5550
;; ------------------------------------------------------------------

(deftest "dispatch-ack-and-ownership-are-three" "docs/SPEC-WORK.md:5550"
    "expected=four-facts-distinct;reservation-declared-only;timeout-no-duplicate-launch"
  (ok (dispatch-facts-distinct-p '(:dispatch :delivery :acknowledgement :ownership))
      "the four dispatch facts did not stay distinct")
  (ok (not (dispatch-facts-distinct-p '(:dispatch :delivery :ownership :ownership)))
      "a duplicated fact read as distinct")
  ;; A pending offer reserves only declared capacity.
  (let* ((offer (make-offer "o1" "rowan" 4))
         (reserved (reserve-offer offer 7)))
    (check-equal 4 (getf reserved :reserved) "an offer reserved more than its declared capacity")
    (check-equal 0 (getf offer :reserved) "the original offer's reservation was mutated"))
  ;; A timeout alone launches no duplicate: the launch count is unchanged.
  (let* ((disp (make-dispatch :offers '(("o1" "rowan" 4)) :launches 1))
         (timed (dispatch-timeout disp)))
    (check-equal 1 (dispatch-launches timed) "a timeout alone launched a duplicate")))
