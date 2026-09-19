;;;; slice-18-verify-resolver.lisp --- a resolver is a real command, run
;;;; directly and never through a shell (nova-work E03 row 3).
;;;;
;;;; SPEC-WORK.md:1251-1266  `session start --resolver <scheme>=<command>' and
;;;;                         the resolver wire: the command is executed
;;;;                         directly, never through a shell, passed two
;;;;                         arguments (the pointer and the criterion's
;;;;                         subject), and answers one stdout line
;;;;                         `<fact> <stamp>', fact `holds'/`absent' agreeing
;;;;                         with the exit; exit 2 or any other, a fact
;;;;                         disagreeing with the exit, no line, or a line past
;;;;                         `--max-bytes' is unreachable and never cached.
;;;;
;;;; Each case drives src/verify-resolver.lisp with a REAL resolver script: a
;;;; real process, a real argument vector and real stdout. The witness script
;;;; records the exact two arguments it was handed, so an injection through
;;;; either argument is visible and never silently dropped.

(in-package #:nova-work/tests)

(defvar *verify-resolver-counter* 0)

(defun %verify-resolver-root ()
  "The directory the wall exports as TMPDIR, with a trailing slash, or `/tmp/'
only when TMPDIR is unset."
  (let ((tmp (sb-posix:getenv "TMPDIR")))
    (if (and tmp (plusp (length tmp)))
        (concatenate 'string tmp
                     (if (char= (char tmp (1- (length tmp))) #\/) "" "/"))
        "/tmp/")))

(defun %verify-resolver-dir ()
  "A fresh 0700 directory for one case's resolver scripts, named on the pid and
a counter."
  (let* ((name (format nil "nova-work-verify-resolver-~D-~D"
                       (sb-posix:getpid)
                       (incf *verify-resolver-counter*)))
         (dir (merge-pathnames (concatenate 'string name "/")
                               (%verify-resolver-root))))
    (ensure-directories-exist dir)
    (sb-posix:chmod (namestring dir) #o700)
    dir))

(defun %verify-resolver-script (dir n body)
  "Write BODY after a `#!/bin/sh' line as resolver script N in DIR, chmod 0700,
and return its namestring. The script is a real executable, not a shell line."
  (let ((path (merge-pathnames (format nil "resolver-~D" n) dir)))
    (with-open-file (out path :direction :output :if-exists :supersede
                              :if-does-not-exist :create :external-format :utf-8)
      (write-string "#!/bin/sh" out)
      (terpri out)
      (write-string body out))
    (sb-posix:chmod (namestring path) #o700)
    (namestring path)))

(defun %verify-resolver-clean (dir)
  (dolist (f (ignore-errors (directory (merge-pathnames "*" dir))))
    (ignore-errors (sb-posix:unlink (namestring f))))
  (ignore-errors (sb-posix:rmdir (namestring dir))))

(defun %witness-resolver-body (witness)
  "A resolver that records its two arguments, one per line, in WITNESS, then
establishes a `holds' fact at a fixed stamp and exits 0. `printf '%s'` never
expands its arguments, so a shell would be visible in the witness."
  (format nil "printf '%s\\n%s\\n' \"$1\" \"$2\" > '~A'~%printf 'holds 2026-09-13T18:00:00Z\\n'~%exit 0"
          witness))

(defun %fact-disagreeing-body (fact)
  (format nil "printf '~A <stamp>\\n'~%exit 0" fact))

(defun %verify-resolver-pid-body (pid-file body)
  "BODY for a resolver that first records its own process id in PID-FILE, so a
case can assert the child was killed and reaped."
  (format nil "printf '%s' \"$$\" > '~A'~%~A" pid-file body))

(defun %verify-resolver-seconds-since (start)
  "Seconds of real time elapsed since START, an INTERNAL-REAL-TIME stamp."
  (/ (- (get-internal-real-time) start)
     (float internal-time-units-per-second)))

(defun %verify-resolver-await-pid (pid-file bound)
  "Poll PID-FILE until it holds a positive integer, for at most BOUND seconds,
and return that integer. The wait is observed, never slept for: a bounded poll
whose own bound is not the deadline under test, and BOUND zero gives up at
once. When the child has not written its pid inside BOUND the case fails naming
that the child never started -- a finding, never a silent pass."
  (let ((start (get-internal-real-time)))
    (loop
      (when (>= (%verify-resolver-seconds-since start) bound)
        (fail "the resolver child never started: no pid written to ~A within ~,2F s"
              pid-file bound))
      (let* ((present (ignore-errors (probe-file pid-file)))
             (text (and present
                        (ignore-errors
                          (uiop:read-file-string (namestring present))))))
        (when (and text (plusp (length text)))
          (let ((pid (ignore-errors (parse-integer text :junk-allowed t))))
            (when pid (return pid)))))
      (sleep 0.01))))

(defmacro %signals-unreachable (&body body)
  `(handler-case (progn ,@body
                       (fail "expected verification-unreachable but none was signalled"))
     (verification-unreachable () t)))

;;; ------------------------------------------------------------------
;;; verify-resolver-runs-the-operators-command-directly  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "verify-resolver-runs-the-operators-command-directly"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=command-runs-directly;pointer-then-subject;holds-line"
  (let* ((dir (%verify-resolver-dir))
         (witness (namestring (merge-pathnames "witness" dir))))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 (%witness-resolver-body witness)))
                (resolver (make-command-resolver "test" script)))
           (multiple-value-bind (fact stamp)
               (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")
             (check-equal :holds fact "the exit-0 resolver holds")
             (check-string= "2026-09-13T18:00:00Z" stamp "the stamp it answered")
             (check-string= (format nil "~A~%~A~%" "test:pkg/name@sha-1" "pkg/name")
                            (uiop:read-file-string witness)
                            "the witness holds the pointer then the subject, exactly")))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; a-resolver-is-never-run-through-a-shell  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "a-resolver-is-never-run-through-a-shell"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=arguments-reach-the-process-byte-for-byte;no-substitution"
  (let* ((dir (%verify-resolver-dir))
         (witness (namestring (merge-pathnames "witness" dir)))
         (pwned (merge-pathnames "pwned" (%verify-resolver-root)))
         (pointer "commit:$(touch ${TMPDIR}/pwned);x")
         (subject "a b;c"))
    (ignore-errors (sb-posix:unlink (namestring pwned)))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 (%witness-resolver-body witness)))
                (resolver (make-command-resolver "commit" script)))
           (multiple-value-bind (fact stamp)
               (fetch-resolver-fact resolver pointer subject)
             (check-equal :holds fact "the witness resolver holds")
             (check-string= "2026-09-13T18:00:00Z" stamp "the stamp it answered")
             (check-string= (format nil "~A~%~A~%" pointer subject)
                            (uiop:read-file-string witness)
                            "the arguments reach the process byte for byte")
             (ok (not (probe-file pwned))
                 "a shell substitution would have made ~A" pwned)))
      (ignore-errors (sb-posix:unlink (namestring pwned)))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; an-absent-answer-is-a-fact-and-exit-one-carries-it  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "an-absent-answer-is-a-fact-and-exit-one-carries-it"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=exit-1-absent-is-a-cached-fact"
  (let* ((dir (%verify-resolver-dir))
         (stamp "2026-09-13T18:00:00Z"))
    (unwind-protect
         (let* ((script (%verify-resolver-script
                         dir 1 (format nil "printf 'absent ~A\\n'~%exit 1" stamp)))
                (resolver (make-command-resolver "test" script)))
           (multiple-value-bind (fact answered)
               (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")
             (check-equal :absent fact "a negative is a fact")
             (check-string= stamp answered "the stamp it answered")))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; exit-two-is-unreachable  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "exit-two-is-unreachable" "docs/SPEC-WORK.md:1251-1266"
    "expected=exit-2-signals-verification-unreachable"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 "exit 2"))
                (resolver (make-command-resolver "test" script)))
           (%signals-unreachable
            (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; a-fact-disagreeing-with-the-exit-is-unreachable  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "a-fact-disagreeing-with-the-exit-is-unreachable"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=absent-on-exit-0-signals-verification-unreachable"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 (%fact-disagreeing-body "absent")))
                (resolver (make-command-resolver "test" script)))
           (%signals-unreachable
            (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; no-line-and-an-over-long-line-are-unreachable  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "no-line-and-an-over-long-line-are-unreachable"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=no-line-and-a-first-line-past-max-bytes-signal"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (progn
           (let* ((script (%verify-resolver-script dir 1 "exit 0"))
                  (resolver (make-command-resolver "test" script)))
             (%signals-unreachable
              (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")))
           (let* ((script (%verify-resolver-script
                           dir 2 (format nil "printf '%s\\n' '~A'~%exit 0"
                                         (make-string 5000 :initial-element #\x))))
                  (resolver (make-command-resolver "test" script)))
             (%signals-unreachable
              (run-resolver-command script "test:pkg/name@sha-1" "pkg/name"
                                    :max-bytes 4096)))
           (let* ((script (%verify-resolver-script dir 3 "exit 0"))
                  (resolver (make-command-resolver "test" script :max-bytes 4096)))
             (%signals-unreachable
              (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name"))))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; an-unreachable-resolver-answer-is-never-cached  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "an-unreachable-resolver-answer-is-never-cached"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=verify-row-unverified;cache-size-stays-0"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 "exit 2"))
                (cache (make-verification-cache))
                (session (make-verification-session
                          :cache cache
                          :resolvers (list (make-command-resolver "test" script))
                          :source-revision "sha-1"))
                (evidence (list (make-verify-evidence
                                 "ev-1" :pointer "test:pkg/name@sha-1"
                                 :criterion :test :subject "pkg/name"
                                 :against "sha-1" :generation "gen-1" :node "t1"))))
           (check-equal 0 (verification-cache-size cache) "the cache is empty before")
           (multiple-value-bind (line rows exit) (verify session evidence :node "t1")
             (declare (ignore line))
             (check-equal 1 (length rows) "one row")
             (check-equal (format nil "VERIFY ROW ev-1 pointer=test:pkg/name@sha-1 verdict=unverified at=-")
                          (first rows)
                          "an unreachable answer is an unverified row")
             (check-equal 1 exit "the verb answers FAIL"))
           (check-equal 0 (verification-cache-size cache) "the cache is still empty after"))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; a-resolver-past-the-deadline-is-unreachable-and-its-child-is-reaped
;;; SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "a-resolver-past-the-deadline-is-unreachable-and-its-child-is-reaped"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=deadline-signals-unreachable;elapsed-under-7s;child-reaped"
  (let* ((dir (%verify-resolver-dir))
         (pid-file (merge-pathnames "deadline.pid" dir))
         ;; The child is a real /bin/sh that only writes a tiny pid file before
         ;; `exec sleep 10'. Two seconds is a generous readiness bound even on a
         ;; loaded bench; the five-second deadline is comfortably longer than
         ;; it, so readiness is observed before the deadline, yet half the
         ;; child's own ten-second sleep, so a deadline that never fired is
         ;; caught by the elapsed assertion.
         (readiness-bound 2.0)
         (deadline 5.0)
         (body (%verify-resolver-pid-body pid-file "exec sleep 10")))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 body))
                (resolver (make-command-resolver "test" script))
                (outcome nil)
                (thread (sb-thread:make-thread
                         (lambda ()
                           (setf outcome
                                 (handler-case
                                     (progn
                                       (fetch-resolver-fact
                                        resolver "test:pkg/name@sha-1" "pkg/name"
                                        :timeout deadline)
                                       :returned)
                                   (verification-unreachable () :unreachable)))))))
           (unwind-protect
                (let ((pid (%verify-resolver-await-pid pid-file readiness-bound))
                      (start (get-internal-real-time)))
                  (sb-thread:join-thread thread)
                  (check-equal :unreachable outcome
                               "the deadline makes the fetch unreachable")
                  (let ((elapsed (%verify-resolver-seconds-since start)))
                    (ok (< elapsed 7)
                        "a ~,2F s deadline against a 10 s child answered in ~,3F s"
                        deadline elapsed))
                  (let ((reaped nil))
                    (handler-case (progn (sb-posix:kill pid 0) nil)
                      (error () (setf reaped t)))
                    (ok reaped "the resolver child ~D was not reaped" pid)))
             (ignore-errors
               (sb-thread:join-thread thread :timeout 15 :default nil))))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; a-flooding-resolver-is-refused-and-its-output-is-never-drained
;;; SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "a-flooding-resolver-is-refused-and-its-output-is-never-drained"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=over-max-bytes-signals-unreachable;flood-not-drained;child-reaped"
  (let* ((dir (%verify-resolver-dir))
         (pid-file (merge-pathnames "flood.pid" dir))
         (marker (merge-pathnames "flood-drained" dir))
         (line (make-string 64 :initial-element #\x))
         (body (%verify-resolver-pid-body
                pid-file
                (format nil "printf 'holds 2026-09-13T18:00:00Z\\n'~%i=0~%while [ $i -lt 65536 ]; do printf '%s\\n' '~A'; i=$((i+1)); done~%printf 'drained' > '~A'~%exit 0"
                        line marker))))
    (unwind-protect
         (let* ((script (%verify-resolver-script dir 1 body))
                (resolver (make-command-resolver "test" script :max-bytes 4096))
                (start (get-internal-real-time)))
           (%signals-unreachable
            (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name"))
           (let ((elapsed (/ (- (get-internal-real-time) start)
                             (float internal-time-units-per-second))))
             (ok (< elapsed 3)
                 "a bounded read against a 4 MiB flood answered in ~,3F s"
                 elapsed))
           (ok (not (probe-file marker))
               "the reader drained the flood all the way to ~A" marker)
           (let ((pid (parse-integer
                       (uiop:read-file-string (namestring pid-file)))))
             (let ((reaped nil))
               (handler-case (progn (sb-posix:kill pid 0) nil)
                 (error () (setf reaped t)))
               (ok reaped "the flooding resolver child ~D was not reaped" pid))))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; output-after-the-one-agreed-line-is-refused  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "output-after-the-one-agreed-line-is-refused"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=a-valid-first-line-plus-extra-output-is-unreachable"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (let* ((script (%verify-resolver-script
                         dir 1 "printf 'holds 2026-09-13T18:00:00Z\\nunexpected second line\\n'~%exit 0"))
                (resolver (make-command-resolver "test" script)))
           (%signals-unreachable
            (fetch-resolver-fact resolver "test:pkg/name@sha-1" "pkg/name")))
      (%verify-resolver-clean dir))))

;;; ------------------------------------------------------------------
;;; max-bytes-counts-utf8-bytes-not-characters  SPEC-WORK.md:1251-1266
;;; ------------------------------------------------------------------

(deftest "max-bytes-counts-utf8-bytes-not-characters"
    "docs/SPEC-WORK.md:1251-1266"
    "expected=seven-characters-eight-utf8-bytes;refused-at-7;accepted-at-8"
  (let ((dir (%verify-resolver-dir)))
    (unwind-protect
         (let* ((script (%verify-resolver-script
                         dir 1 (format nil "printf 'holds ~A\\n'~%exit 0"
                                       (string (code-char #xe9))))))
           (%signals-unreachable
            (run-resolver-command script "test:pkg/name@sha-1" "pkg/name"
                                  :max-bytes 7))
           (multiple-value-bind (fact stamp)
               (run-resolver-command script "test:pkg/name@sha-1" "pkg/name"
                                     :max-bytes 8)
             (check-equal :holds fact "eight UTF-8 bytes is under the 8-byte bound")
             (check-string= (string (code-char #xe9)) stamp "the stamp it answered")))
      (%verify-resolver-clean dir))))
