;;;; issue-2333.lisp --- SPEC-WORK work-a: periodic clipping triggers and retention-boundary logic
;;;;
;;;; nova-tools#2333, docs/SPEC-WORK.md:495-533. Covers the trigger rule
;;;; (--clip-after / --clip-every, never with zero pending), the retention
;;;; boundary (newest revision older than the clip stamp less --retain, forward
;;;; only), the snapshot's three components, and the sibling retention archive
;;;; (same directory as the snapshot, pre-boundary events unchanged and in order).

(in-package #:nova-work/tests)

(defun issue-2333-read-forms (path)
  "Every form in PATH, in order (comments skipped by the reader)."
  (with-open-file (in path)
    (loop for form = (read in nil in)
          until (eq form in)
          collect form)))

(defun issue-2333-events (now)
  "Four events: revs 1-2 are two and one-and-a-half days old, revs 3-4 are an
hour and a minute old."
  (list (list :rev 1 :stamp (nova-work::format-rfc3339 (- now 172800)) :op "a")
        (list :rev 2 :stamp (nova-work::format-rfc3339 (- now 129600)) :op "b")
        (list :rev 3 :stamp (nova-work::format-rfc3339 (- now 3600)) :op "c")
        (list :rev 4 :stamp (nova-work::format-rfc3339 (- now 60)) :op "d")))

(deftest "TestIssue2333ClipTriggersRetentionArchive" "docs/SPEC-WORK.md:495-533"
    "SPEC-WORK work-a: implement periodic clipping triggers and retention-boundary logic"
  ;; Trigger rule (SPEC-WORK.md:498-499).
  (let ((now (get-universal-time))
        (k (fresh)))
    (ok (nova-work::clip-should-trigger-p k :clip-after 10 :pending-count 10)
        "clip-after reached must trigger")
    (ok (not (nova-work::clip-should-trigger-p k :clip-after 10 :pending-count 9))
        "clip-after not reached and no clip-every must not trigger")
    (ok (nova-work::clip-should-trigger-p k :clip-after 10 :clip-every 300
                                            :last-clip-time (- now 600) :pending-count 1)
        "clip-every elapsed with one pending must trigger")
    (ok (not (nova-work::clip-should-trigger-p k :clip-after 10 :clip-every 300
                                                 :last-clip-time (- now 600) :pending-count 0))
        "a clip with nothing pending is never run")
    (ok (not (nova-work::clip-should-trigger-p k :clip-every 300 :last-clip-time (- now 60)
                                                 :pending-count 5))
        "clip-every not elapsed and no clip-after must not trigger"))
  ;; Retention boundary: newest revision whose stamp is older than stamp - retain;
  ;; it moves forward at a later clip, never backward (SPEC-WORK.md:522-525).
  (let* ((now (get-universal-time))
         (events (issue-2333-events now))
         (b1 (nova-work::calculate-retention-boundary events 86400 now))
         (b2 (nova-work::calculate-retention-boundary events 86400 (+ now 84000))))
    (check-equal 2 b1 "boundary at retain=1d")
    (check-equal 0 (nova-work::calculate-retention-boundary events 86400 (- now 172800))
                 "no event older than the cutoff names revision 0")
    (ok (>= b2 b1) "boundary moved backward: ~A then ~A" b1 b2)
    (check-equal 3 b2 "boundary at a clip 84000 s later"))
  ;; ctl-clip end to end: snapshot and its sibling archive.
  (let* ((now (get-universal-time))
         (events (issue-2333-events now))
         (dir (test-temp-dir "issue-2333"))
         (snapshot (namestring (merge-pathnames "clip-test.lisp" dir)))
         (archive (nova-work::retention-archive-path snapshot))
         (structure '(:root "work")))
    (check-string= (namestring (merge-pathnames "clip-test-archive.lisp" dir)) archive
                   "archive path is the snapshot's sibling")
    (check-equal (pathname-directory (pathname snapshot)) (pathname-directory (pathname archive))
                 "snapshot and archive share a directory")
    (multiple-value-bind (okp line code)
        (ctl-clip (fresh) :clip-after 3 :retain 86400 :pending-count 4
                          :events events :structure structure :path snapshot)
      (ok okp "ctl-clip should succeed when clip-after is reached")
      (check-string= "CLIP OK" line "ctl-clip line")
      (check-equal 0 code "ctl-clip exit code"))
    (ok (probe-file snapshot) "snapshot written at ~A" snapshot)
    (ok (probe-file archive) "retention archive written beside the snapshot at ~A" archive)
    (let ((forms (issue-2333-read-forms snapshot)))
      (check-equal 3 (length forms) "snapshot has three components")
      (check-equal structure (first forms) "snapshot structure")
      (check-equal 2 (second forms) "snapshot retention boundary")
      (check-equal (remove-if (lambda (e) (< (getf e :rev) 2)) events) (third forms)
                   "snapshot carries the events from the boundary on, in order"))
    (check-equal (list (first events)) (issue-2333-read-forms archive)
                 "archive holds the pre-boundary events unchanged and in order")
    ;; Nothing pending: no clip, no files.
    (let ((idle (namestring (merge-pathnames "idle.lisp" dir))))
      (ok (null (ctl-clip (fresh) :clip-after 3 :pending-count 0 :events events
                                  :structure structure :path idle))
          "ctl-clip with nothing pending does not run")
      (ok (not (probe-file idle)) "no snapshot written when nothing is pending"))))
