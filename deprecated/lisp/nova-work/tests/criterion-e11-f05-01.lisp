;;;; criterion-e11-f05-01.lisp --- E11-F05-01 decision packet per item and revision
;;;;
;;;; Replay row `decision-packet-per-item-revision` (docs/SPEC-WORK.md:4648),
;;;; contract at docs/SPEC-WORK.md:4546-4556: machinery builds one packet per
;;;; item and revision; a newer revision supersedes it keeping its open
;;;; findings; while the reader is busy the packet is amended, not duplicated;
;;;; an empty pulse wakes no model and re-executes nothing.
;;;;
;;;; The production mechanism is `book-observation` in src/decide.lisp.

(in-package #:nova-work/tests)

(deftest "e11-f05-01-decision-packet-per-item"
    "docs/SPEC-WORK.md:4648"
    "expected=one-packet-per-item-revision;newer-revision-supersedes-with-open-findings;busy-reader-amendment-not-duplication;empty-pulse-wakes-no-model"
  (let ((book (nova-work::make-packet-book)))
    ;; 1. One packet per item and revision, and it wakes the reader once.
    (multiple-value-bind (p1 action)
        (nova-work::book-observation
         book (list :item "acme/work/f1" :revision 1
                    :facts '("check test red")
                    :findings '(("f-scope" . :open) ("f-ci" . :open))))
      (check-equal :built action "first observation of (item, revision) builds a packet")
      (check-equal 1 (nova-work::packet-book-wakes book) "a built packet wakes the reader once")
      ;; 2. Busy reader: the same revision amends the packet, never a second one.
      (multiple-value-bind (p1b action)
          (nova-work::book-observation
           book (list :item "acme/work/f1" :revision 1
                      :facts '("check lint green")
                      :findings '(("f-ci" . :fixed)))
           :reader-busy-p t)
        (check-equal :amended action "same (item, revision) amends")
        (ok (eq p1 p1b) "the amendment is the same packet, not a duplicate")
        (check-equal 1 (length (nova-work::packets-for book "acme/work/f1" 1))
                     "exactly one packet exists for (item, revision 1)")
        (check-equal 1 (nova-work::decision-packet-amendments p1) "one amendment recorded")
        (check-equal '("check test red" "check lint green")
                     (nova-work::decision-packet-facts p1) "amended facts appended")
        (check-equal 1 (nova-work::packet-book-wakes book)
                     "an amendment while the reader is busy wakes nothing"))
      ;; 3. Empty pulse: no packet, no amendment, no wake.
      (multiple-value-bind (p action)
          (nova-work::book-observation book (list :item "acme/work/f1" :revision 1))
        (declare (ignore p))
        (check-equal :empty action "an empty pulse books nothing")
        (check-equal 1 (nova-work::decision-packet-amendments p1) "empty pulse amends nothing")
        (check-equal 1 (nova-work::packet-book-wakes book) "an empty pulse wakes no model")
        (check-equal 1 (length (nova-work::packets-for book "acme/work/f1"))
                     "an empty pulse builds no packet"))
      ;; 4. A newer revision supersedes, carrying only the still-open findings.
      (multiple-value-bind (p2 action)
          (nova-work::book-observation
           book (list :item "acme/work/f1" :revision 2 :facts '("rebased onto dev")))
        (check-equal :superseded action "a newer revision supersedes")
        (ok (not (eq p1 p2)) "the newer revision has its own packet")
        (check-equal 2 (nova-work::decision-packet-superseded-by p1)
                     "the old packet names its successor revision")
        (check-equal '(("f-scope" . :open)) (nova-work::decision-packet-findings p2)
                     "the open finding survives the supersede; the fixed one does not")
        (check-equal 2 (nova-work::packet-book-wakes book) "the new revision wakes the reader")
        ;; A stale (older) revision is booked nowhere.
        (multiple-value-bind (p action)
            (nova-work::book-observation
             book (list :item "acme/work/f1" :revision 1 :facts '("late")))
          (check-equal :stale action "an older revision is stale")
          (ok (eq p p2) "the current packet stays the newer one")
          (check-equal 2 (length (nova-work::packets-for book "acme/work/f1"))
                       "one packet per revision, two revisions, two packets"))))))
