;;;; isolation-collision.lisp --- the fixture allocator's collision regression.
;;;;
;;;; Two runs of this suite on one host (concurrent merge groups, 2026-09-18)
;;;; wrecked each other's fixtures: a candidate directory that already existed
;;;; was adopted, its payload read as this run's, and a wildcard cleanup then
;;;; deleted a neighbour the run never created.
;;;;
;;;; The replay below is deterministic. The allocator's first candidate is made
;;;; to collide and its second to be free, so the retry is really exercised --
;;;; no sleep, no second process, no chance that the collision fails to happen.

(in-package #:nova-work/tests)

(defun %collision-write (path text)
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create)
    (write-string text out)))

(defun %collision-read (path)
  (with-open-file (in path :direction :input)
    (let ((line (read-line in nil "")))
      line)))

(deftest "fixture-allocator-retries-past-a-collision" "docs/SPEC-WORK.md:290"
    "expected=one-owned-parent;first-candidate-untouched;second-candidate-created;ownership-after-creation;exact-cleanup"
  ;; (1) one owned fixture parent, below the suite root and nowhere else.
  (let* ((owned-before (copy-list *owned-fixtures*))
         (parent (allocate-fixture "collision"))
         (root (suite-root))
         (taken (concatenate 'string parent "cand-0"))
         (neighbour (concatenate 'string parent "neighbour")))
    (ok (eql 0 (search root parent))
        "the fixture parent is below the suite root: ~A not below ~A" parent root)
    (ok (fixture-owned-p parent) "the fixture parent is owned by this run")
    ;; Somebody else's fixture, and a neighbour beside it. Neither is this run's:
    ;; both are created here by hand, not through the allocator, exactly as a
    ;; concurrent run would have left them.
    (sb-posix:mkdir taken #o700)
    (sb-posix:mkdir neighbour #o700)
    (%collision-write (concatenate 'string taken "/payload") "candidate-payload")
    (%collision-write (concatenate 'string neighbour "/sentinel") "neighbour-sentinel")
    (unwind-protect
         (let ((*fixture-name-hook*
                 (lambda (name attempt)
                   (declare (ignore name))
                   (ecase attempt
                     (0 "cand-0")           ; taken: the mkdir must fail EEXIST
                     (1 "cand-1")))))       ; free: the retry must land here
           ;; (3) the allocator retries: first candidate collides, second wins.
           (let ((got (allocate-fixture "cand" :parent parent)))
             (check-string= (concatenate 'string parent "cand-1/") got
                            "the allocator retried onto the second candidate")
             (ok (probe-file (uiop:ensure-directory-pathname got))
                 "the second candidate was created: ~A" got)
             ;; the collided candidate is left exactly as it was found.
             (check-string= "candidate-payload"
                            (%collision-read (concatenate 'string taken "/payload"))
                            "the collided candidate's payload survived")
             ;; the neighbour is never in range of a cleanup.
             (check-string= "neighbour-sentinel"
                            (%collision-read (concatenate 'string neighbour "/sentinel"))
                            "the neighbouring sentinel survived")
             ;; (4) ownership recorded only after the creation succeeded: the
             ;; parent and the second candidate, never the collided first one.
             (let ((new (set-difference *owned-fixtures* owned-before
                                        :test #'string=)))
               ;; every newly recorded path was created by a mkdir that returned,
               ;; and every one of them still exists.
               (dolist (path new)
                 (ok (probe-file (uiop:ensure-directory-pathname path))
                     "a recorded path was never created: ~A" path))
               (ok (member (string-right-trim "/" parent) new :test #'string=)
                   "the created parent was recorded")
               (ok (member (string-right-trim "/" got) new :test #'string=)
                   "the created second candidate was recorded")
               (ok (not (member taken new :test #'string=))
                   "the collided candidate was recorded despite the failed mkdir"))
             (ok (not (fixture-owned-p taken))
                 "the collided candidate was never recorded as owned")
             (ok (not (fixture-owned-p neighbour))
                 "the neighbour was never recorded as owned")))
      ;; (2) cleanup by exact path, never a wildcard. The scaffolding above was
      ;; made by hand, so it is removed by hand, by name; everything the
      ;; allocator made is removed by RELEASE-FIXTURES from the owned list.
      (progn
        (ignore-errors (delete-file (concatenate 'string taken "/payload")))
        (ignore-errors (delete-file (concatenate 'string neighbour "/sentinel")))
        (ignore-errors (sb-posix:rmdir taken))
        (ignore-errors (sb-posix:rmdir neighbour))))))
