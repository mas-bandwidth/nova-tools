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
