;;;; replays-8664.lisp --- three acceptance replays for the fleet allocation
;;;; rules and the counters-and-indexes preservation suite of
;;;; docs/SPEC-WORK.md at origin/dev:
;;;;
;;;;   preparation-interrupted-before-launch   SPEC-WORK.md:6153-6159
;;;;   concurrent-slots-refuse-third-job       SPEC-WORK.md:6160-6164
;;;;   indexes-and-counters                    SPEC-WORK.md:6242
;;;;
;;;; Each deftest sets up the state its paragraph describes, drives the pure
;;;; model the paragraph promises and asserts the promised outcome. The live
;;;; session, journal and CLI wiring those paragraphs sit on is out of this
;;;; slice; where a name's paragraph fixes a reading, the reading is stated in
;;;; RESULT.md as `read: <name>: ...`.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; preparation-interrupted-before-launch        SPEC-WORK.md:6153-6159
;;; ------------------------------------------------------------------

(deftest "preparation-interrupted-before-launch" "docs/SPEC-WORK.md:6153-6159"
    "expected=slot-retained-through-interruption;second-session-refused-capacity;no-reuse-before-reconciliation"
  (let* ((m0 (make-replay-machine "m-a1" :cores 16 :concurrent 1))
         (m1 (nth-value 2 (take-slot m0 "N1" :slots 1 :holder "N1" :id "alloc-c"))))
    ;; the take allocated slot 1 on m-a1 to node N1, allocation alloc-c.
    (check-equal 1 (machine-live-slots m1) "alloc-c holds slot 1")
    (check-equal "N1" (alloc-holder (find-allocation m1 "alloc-c"))
                 "alloc-c names N1 as holder")
    ;; preparation (clone/scp) is interrupted before the model launches.
    (let ((m2 (preparation-interrupted m1 "alloc-c")))
      (check-equal 1 (machine-live-slots m2)
                   "an interrupted preparation still holds its slot")
      (check-equal :interrupted (alloc-state (find-allocation m2 "alloc-c"))
                   "the allocation is retained, never silently cancelled")
      ;; a second session's take for N2 is refused because alloc-c holds the slot.
      (multiple-value-bind (taken2 line2 m3)
          (take-slot m2 "N2" :slots 1 :holder "session-b")
        (check-equal nil taken2 "the second session's take is refused")
        (check-string= "ALLOC FAIL machine=m-a1 slots=1 holder=N1: capacity" line2
                       "the refusal names the machine, the slot and the holder N1")
        (check-equal m2 m3 "the refusal writes no partial grant"))
      ;; no second session reuses the capacity until reconciliation happens.
      (multiple-value-bind (taken3 line3 m4)
          (take-slot m2 "N2" :slots 1 :holder "session-b")
        (declare (ignore line3))
        (check-equal nil taken3 "a repeated take stays refused before reconciliation")
        (check-equal m2 m4 "nothing is reused before reconciliation"))
      ;; an expiry is not stop evidence and frees nothing.
      (multiple-value-bind (ok4 line4 m5)
          (reconcile-allocation m2 "alloc-c" :expiry)
        (check-equal nil ok4 "an expiry does not free the slot")
        (ok (search "not fenced" line4) "the expiry refusal names not fenced: ~A" line4)
        (check-equal 1 (machine-live-slots m5) "the capacity is retained through the expiry"))
      ;; a verified stop observation is confirmed termination: it frees the slot.
      (multiple-value-bind (ok5 line5 m6)
          (reconcile-allocation m2 "alloc-c" :verified-stop)
        (ok ok5 "a verified stop observation reconciles alloc-c: ~A" line5)
        (check-equal :released (alloc-state (find-allocation m6 "alloc-c"))
                     "the reconciled allocation is released")
        (check-equal 0 (machine-live-slots m6) "the slot is now free")
        ;; only now does the second session's take become available.
        (multiple-value-bind (taken7 line7 m7)
            (take-slot m6 "N2" :slots 1 :holder "session-b")
          (ok taken7 "the freed slot admits N2: ~A" line7)
          (check-equal 1 (machine-live-slots m7) "N2 now holds the slot"))))))

;;; ------------------------------------------------------------------
;;; concurrent-slots-refuse-third-job            SPEC-WORK.md:6160-6164
;;; ------------------------------------------------------------------

(deftest "concurrent-slots-refuse-third-job" "docs/SPEC-WORK.md:6160-6164"
    "expected=slots-bounded-by-declared-concurrency;idle-cores-separate;third-refused-capacity"
  (let* ((m0 (make-replay-machine "m-a1" :cores 16 :concurrent 2))
         (m1 (nth-value 2 (take-slot m0 "N1" :slots 1 :holder "sess-a" :id "alloc-a")))
         (m2 (nth-value 2 (take-slot m1 "N2" :slots 1 :holder "sess-b" :id "alloc-b"))))
    (check-equal 16 (replay-machine-cores m2) "machine m-a1 declares :cores 16")
    (check-equal 2 (replay-machine-concurrent m2) "machine m-a1 declares :concurrent 2")
    (check-equal 16 (machine-cores m2) "machine m-a1 declares :cores 16")
    (check-equal 2 (machine-concurrent m2) "machine m-a1 declares :concurrent 2")
    (check-equal 2 (machine-live-slots m2) "two allocations each consume one slot")
    ;; the third take is refused even though fourteen cores sit idle.
    (multiple-value-bind (taken3 line3 m3)
        (take-slot m2 "N3" :slots 1 :holder "sess-c")
      (check-equal nil taken3 "the third job is refused")
      (ok (search "ALLOC FAIL machine=m-a1 slots=1 holder=" line3)
          "the refusal names the machine, the slot and a holder: ~A" line3)
      (ok (search ": capacity" line3) "the refusal names capacity: ~A" line3)
      (check-equal 2 (machine-live-slots m3) "no partial grant: two slots still held")
      (check-equal 2 (length (replay-machine-allocations m3))
                   "no third allocation is written")
      (check-equal 14 (- (replay-machine-cores m2) (machine-live-slots m2))
      (check-equal 2 (length (machine-allocations m3))
                   "no third allocation is written")
      (check-equal 14 (- (machine-cores m2) (machine-live-slots m2))
                   "fourteen cores sit idle and cores are a separate constraint"))))

;;; ------------------------------------------------------------------
;;; indexes-and-counters                            SPEC-WORK.md:6242
;;; ------------------------------------------------------------------

(defparameter *replay-index-seed*
  '((:id "a" :parent nil :state :open :holder "alice")
    (:id "b" :parent "a" :state :open :holder "alice")
    (:id "b1" :parent "b" :state :open :holder "bob")
    (:id "b2" :parent "b" :state :open :holder "bob")
    (:id "c" :parent "a" :state :open :holder "alice")
    (:id "d" :parent nil :state :open :holder "carol")
    (:id "d1" :parent "d" :state :open :holder "carol"))
  "Seven open items in one forest; b1's holder is bob, both roots are held.")

(defun %replay-lcg (seed)
  (mod (+ (* seed 1103515245) 12345) 2147483648))

(defun %replay-next-op (seed)
  "A deterministic pseudo-random legal verb request and the next seed."
  (let* ((s1 (%replay-lcg seed))
         (ops '(close reopen reparent assign))
         (ids '("a" "b" "b1" "b2" "c" "d" "d1"))
         (holders '("alice" "bob" "carol"))
         (roots '("a" "d")))
    (values (list :op (nth (mod s1 4) ops)
                  :id (nth (mod (floor s1 4) 7) ids)
                  :holder (nth (mod (floor s1 8) 3) holders)
                  :parent (nth (mod (floor s1 16) 2) roots))
            (%replay-lcg s1))))

(defun %replay-apply-legal-op (index op)
  "Apply OP when it is legal for the current state; skip it otherwise, so the
sequence driven is a random sequence of legal verbs."
  (let ((id (getf op :id)))
    (case (getf op :op)
      (close (if (replay-index-open-p index id)
                 (replay-index-close index id) index))
      (reopen (if (replay-index-closed-p index id)
                  (replay-index-reopen index id) index))
      (reparent (let ((p (getf op :parent)))
                  (if (and p (not (equal p id))
                           (not (replay-index-ancestor-p index p id)))
                      (replay-index-reparent index id p)
                      index)))
      (assign (if (replay-index-open-p index id)
                  (replay-index-assign index id (getf op :holder))
                  index))
      (t index))))

(deftest "indexes-and-counters" "docs/SPEC-WORK.md:6242"
    "expected=counters-and-indexes-equal-reconstruction;closure-reopen-reparent-never-double-count"
  (let ((st (make-replay-index *replay-index-seed*)))
    ;; the seeded counters and indexes agree with an independent reconstruction.
    (check-equal '() (index-mismatches st) "the seeded counters and indexes agree")
    (check-equal 7 (rindex-open-count st) "|O| counts every open item once")
    ;; closure lowers |O| by exactly one and reopen restores it.
    (setf st (replay-index-close st "b1"))
    (check-equal 6 (rindex-open-count st) "closure lowers |O| exactly once")
    (check-equal '() (index-mismatches st) "counters and indexes agree after a close")
    (setf st (replay-index-reopen st "b1"))
    (check-equal 7 (rindex-open-count st) "reopen restores |O|")
    (check-equal '() (index-mismatches st) "reopen never double-counts")
    ;; reparent moves the per-container counts and touches neither |O| nor the
    ;; friend index; the old and new ancestor chains are neither missed nor doubled.
    (setf st (replay-index-reparent st "b1" "d"))
    (check-equal 7 (rindex-open-count st) "reparent changes no |O|")
    (check-equal '() (index-mismatches st)
                 "reparent moves the container counters without double-counting")
    (setf st (replay-index-reparent st "b1" "b"))
    (check-equal '() (index-mismatches st) "reparenting back agrees again")
    ;; reassigning a holder moves the friend index and no counter.
    (setf st (replay-index-assign st "b1" "carol"))
    (check-equal '() (index-mismatches st) "a holder assignment agrees with reconstruction")
    ;; a deterministic pseudo-random sequence of legal verbs: after every step
    ;; the maintained counters and indexes equal a full independent reconstruction.
    (let ((seed 20260917))
      (dotimes (i 60)
        (multiple-value-bind (op next) (%replay-next-op seed)
          (setf seed next)
          (setf st (%replay-apply-legal-op st op))
          (check-equal '() (index-mismatches st)
                       (format nil "step ~D (~S) keeps counters and indexes equal" i op)))))
    ;; shared references never double-count: an item carrying two links is one
    ;; open item and lists once under its holder.
    (let ((shared (make-replay-index
                   '((:id "x" :parent nil :state :open :holder "alice"
                      :links ("l1" "l2"))))))
      (check-equal 1 (rindex-open-count shared)
                   "an item with two shared links counts once in |O|")
      (check-equal '("x") (holder-open-ids shared "alice")
                   "a shared item lists once under its holder")
      (check-equal '() (index-mismatches shared)
                   "shared references never double-count"))))
