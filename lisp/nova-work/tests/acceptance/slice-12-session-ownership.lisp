;;;; slice-12-session-ownership.lisp --- the resume predicate, the ownership
;;;; record and the single command thread (nova-tools #749).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; ownership record (SPEC-WORK.md:202-214)
;;; ------------------------------------------------------------------

(deftest "ownership-record-round-trips" "docs/SPEC-WORK.md:202-214"
  "expected=format-then-parse-preserves-owner-generation-token-stamp-until-bench-successor"
  (let* ((rec (make-ownership-record
               :owner "emma"
               :generation 7
               :token "tok-abc"
               :stamp "2026-09-14T12:00:00Z"
               :until "2026-09-14T12:01:00Z"
               :bench "spacegame@64769:123456"
               :successor "stella"))
         (parsed (parse-ownership-record (format-ownership-record rec))))
    (check-string= (owner-owner rec) (owner-owner parsed) "owner round-trips")
    (check-equal (owner-generation rec) (owner-generation parsed) "generation round-trips")
    (check-string= (owner-token rec) (owner-token parsed) "token round-trips")
    (check-string= (owner-stamp rec) (owner-stamp parsed) "stamp round-trips")
    (check-string= (owner-until rec) (owner-until parsed) "until round-trips")
    (check-string= (owner-bench rec) (owner-bench parsed) "bench round-trips")
    (check-string= (owner-successor rec) (owner-successor parsed) "successor round-trips")))

(deftest "handoff-successor-takes-next-generation" "docs/SPEC-WORK.md:202-214"
  "expected=successor-takes-next-generation-immediately-without-waiting-for-expiry"
  (let ((current (make-ownership-record
                  :owner "emma"
                  :generation 3
                  :token "tok-emma"
                  :stamp "2026-09-14T11:59:00Z"
                  :until "2026-09-14T12:30:00Z"
                  :successor "stella")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "stella"
                                  :now "2026-09-14T12:01:00Z"
                                  :every "30s"
                                  :skew "5s"
                                  :token "tok-stella-new"
                                  :my-bench "bench-stella")
      (declare (ignore line exit-code))
      (check-equal :take action "successor handoff is a take")
      (check-string= "stella" (owner-owner record) "owner is stella")
      (check-equal 4 (owner-generation record) "generation bumped to 4")
      (check-string= "tok-stella-new" (owner-token record) "new token")
      (check-string= "bench-stella" (owner-bench record) "the successor's bench is recorded")
      (check-string= "2026-09-14T12:02:00Z" (owner-until record) "until is now + 2*every"))))

;;; ------------------------------------------------------------------
;;; W1: the resume predicate (bench identity + journal lock) and the single
;;; writer. SPEC-WORK.md:162-204, 2603-2616 (#749).
;;; ------------------------------------------------------------------

(defun journal-frame-seqs (path)
  "Read the frame sequence numbers of the journal at PATH, in order."
  (let ((seqs '()))
    (with-open-file (in path :direction :input :element-type 'character :external-format :utf-8)
      (read-header in path nil)
      (loop for n from 1
            for frame = (read-record-frame in path n)
            while frame
            do (push (getf (rest frame) :seq) seqs)))
    (nreverse seqs)))

(deftest "resume-refuses-a-copied-journal" "docs/SPEC-WORK.md:193-204"
    "expected=same-owner-and-token-but-different-bench-refused;no-lock-held-refused;reason-printed"
  (let ((current (make-ownership-record
                  :owner "emma"
                  :generation 3
                  :token "tok-emma-secret"
                  :stamp "2026-09-14T11:59:00Z"
                  :until "2026-09-14T12:01:00Z"
                  :bench "bench-a")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "emma"
                                  :now "2026-09-14T12:00:30Z"
                                  :every "30s"
                                  :skew "5s"
                                  :journal-token "tok-emma-secret"
                                  :my-bench "bench-b"
                                  :lock-held t)
      (declare (ignore record))
      (check-equal :fenced action "a copied journal must not resume")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "SESSION FAIL" line) "line is a failure: ~A" line)
      (ok (search "bench" line) "the reason names the bench: ~A" line))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "emma"
                                  :now "2026-09-14T12:00:30Z"
                                  :every "30s"
                                  :skew "5s"
                                  :journal-token "tok-emma-secret"
                                  :my-bench "bench-a"
                                  :lock-held nil)
      (declare (ignore record))
      (check-equal :fenced action "no lock held must not resume")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "SESSION FAIL" line) "line is a failure: ~A" line)
      (ok (search "lock" line) "the reason names the lock: ~A" line))))

(deftest "resume-needs-the-journal-lock" "docs/SPEC-WORK.md:162-183,193-204"
    "expected=second-holder-cannot-take-the-lock;open-refuses-held;journal-not-held-fences-resume"
  (let* ((path (test-journal-path "resume-lock"))
         (foreign-lock (take-journal-lock path)))
    (ok foreign-lock "took the outer lock")
    (unwind-protect
         (progn
           (ok (null (take-journal-lock path))
               "a second flock on the held lock must fail")
           (let ((held nil))
             (handler-case (open-file-journal path :take-lock t)
               (journal-held () (setf held t)))
             (ok held "open-file-journal did not refuse the held lock"))
           (let ((current (make-ownership-record
                           :owner "emma" :generation 3 :token "tok-emma"
                           :until "2026-09-14T12:01:00Z" :bench "bench-a")))
             (multiple-value-bind (action record line exit-code)
                 (evaluate-ownership-claim current "emma"
                                           :journal-token "tok-emma"
                                           :my-bench "bench-a"
                                           :lock-held nil)
               (declare (ignore record))
               (check-equal :fenced action "no journal lock fences the resume")
               (check-equal 1 exit-code "exit code is 1")
               (ok (search "lock" line) "the reason names the lock: ~A" line))))
      (release-journal-lock foreign-lock)
      (ignore-errors (delete-file path))
      (ignore-errors (delete-file (concatenate 'string path ".lock"))))))

(deftest "submits-serialize-on-one-thread" "docs/SPEC-WORK.md:2603-2616"
    "expected=two-concurrent-submits-land-in-one-total-order-with-consecutive-sequence-numbers"
  (let* ((path (test-journal-path "serialize"))
         (seed (loop for i below 20 collect (list :id (format nil "t~D" i) :type :task :state :todo)))
         (j (open-file-journal path :initial-state-hash (root-digest (make-seed-state seed))))
         (k (make-kernel :state (make-seed-state seed) :journal j)))
    (unwind-protect
         (progn
           (let ((threads (loop for i below 20
                                collect (let ((i i))
                                          (sb-thread:make-thread
                                           (lambda ()
                                             (submit k (list :verb :state-to-doing
                                                             :node (format nil "t~D" i)
                                                             :by "emma"
                                                             :reason "r"
                                                             :request (format nil "req-~D" i)
                                                             :stamp "2026-09-14T12:00:00Z"
                                                             :clock :tool
                                                             :generation-owner "emma")))
                                           :name (format nil "submit-~D" i))))))
             (mapc #'sb-thread:join-thread threads))
           (check-equal 20 (journal-seq j) "twenty commands issue twenty sequence numbers")
           (check-equal 20 (length (journal-order j)) "every command recorded exactly once")
           (check-equal (loop for n from 1 to 20 collect n)
                        (journal-frame-seqs path)
                        "the frames carry consecutive sequence numbers 1..20"))
      (close-file-journal j)
      (ignore-errors (delete-file path))
      (ignore-errors (delete-file (concatenate 'string path ".lock"))))))
