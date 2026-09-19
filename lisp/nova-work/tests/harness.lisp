;;;; harness.lisp --- the smallest runner that prints one summary line.

(in-package #:nova-work/tests)

(defvar *tests* '())
(defvar *pass* 0)
(defvar *fail* 0)
(defvar *current* nil)
(defvar *problems* '())

(defmacro deftest (name spec-line expected &body body)
  `(push (list ,name ,spec-line ,expected (lambda () ,@body)) *tests*))

(defvar *needs-kernel* '()
  "Replays parked as NEEDS-KERNEL. Registered and counted, never run.")

(defmacro deftest-pending (name spec-line expected need)
  "Park a replay NAME asserting EXPECTED (from SPEC-LINE) blocked on NEED."
  (declare (ignore spec-line expected need))
  `(push (list ,name) *needs-kernel*))

(defmacro needs-kernel (name spec-line need what)
  "Park a replay NAME blocked on the kernel feature WHAT (SPEC-LINE)."
  (declare (ignore spec-line need what))
  `(push (list ,name) *needs-kernel*))

(define-condition check-failed (error)
  ((detail :initarg :detail :reader check-failed-detail))
  (:report (lambda (c s) (write-string (check-failed-detail c) s))))

(defun fail (fmt &rest args)
  (error 'check-failed :detail (apply #'format nil fmt args)))

(defun ok (test fmt &rest args)
  (unless test (apply #'fail fmt args))
  t)

(defun check-equal (expected actual what)
  (unless (equal expected actual)
    (fail "~A: expected ~S got ~S" what expected actual))
  t)

(defun check-string= (expected actual what)
  (unless (and (stringp actual) (string= expected actual))
    (fail "~A: expected ~S got ~S" what expected actual))
  t)

(defun run-all ()
  (setf *pass* 0 *fail* 0 *problems* '())
  (dolist (entry (reverse *tests*))
    (destructuring-bind (name spec expected thunk) entry
      (let ((*current* name))
        (handler-case
            (progn (funcall thunk)
                   (incf *pass*)
                   (format t "TEST ~A PASS spec=~A ~A~%" name spec expected))
          (error (c)
            (incf *fail*)
            (push (list name spec c) *problems*)
            (format t "TEST ~A FAIL spec=~A ~A: ~A~%" name spec expected c))))))
  (format t "NOVA-WORK SLICE1 total=~D pass=~D fail=~D~%"
          (+ *pass* *fail*) *pass* *fail*)
  (finish-output)
  (if (zerop *fail*) 0 1))

(defun main ()
  (let ((code (unwind-protect (run-all) (release-fixtures))))
    #+sbcl (sb-ext:exit :code code :abort nil)
    #-sbcl (progn code)))

;;;; ------------------------------------------------------------------
;;;; Fixture isolation: one owned parent, exact cleanup, an allocator that
;;;; retries past a collision.
;;;;
;;;; Two runs of this suite share a host (concurrent merge groups). #1301 gives
;;;; each run its own private TMPDIR; below that, every directory this suite
;;;; creates hangs off ONE owned suite root, so nothing is written beside the
;;;; checkout or into a directory the suite did not make. A candidate name that
;;;; already exists belongs to somebody else: the allocator never adopts it, it
;;;; moves to the next candidate. Ownership is recorded only after the mkdir
;;;; returns, so cleanup deletes exactly the paths this run created -- by exact
;;;; path, never by wildcard or pattern.
;;;; ------------------------------------------------------------------

(defvar *suite-root* nil
  "The one owned parent, below the private TMPDIR, of every suite fixture.")

(defvar *owned-fixtures* '()
  "Exactly the paths this run created, newest first. Nothing else is deleted.")

(defvar *fixture-name-hook* nil
  "Test-only: (lambda (name attempt) -> string), to make a collision deterministic.")

(defun %fixture-candidate (name attempt)
  (if *fixture-name-hook*
      (funcall *fixture-name-hook* name attempt)
      (format nil "~A-~D-~D" name (sb-posix:getpid) attempt)))

(defun %eexist-p (condition)
  (and (typep condition 'sb-posix:syscall-error)
       (eql (sb-posix:syscall-errno condition) sb-posix:eexist)))

(defun suite-root ()
  "The suite's own directory below the private TMPDIR, created once."
  (or *suite-root*
      (let ((tmp (string-right-trim "/" (namestring (uiop:temporary-directory)))))
        (loop for attempt from 0 below 64
              for path = (format nil "~A/nw-suite-~D-~D" tmp (sb-posix:getpid) attempt)
              do (handler-case
                     (progn (sb-posix:mkdir path #o700)
                            ;; created: the root is owned too, and goes last.
                            (push path *owned-fixtures*)
                            (return (setf *suite-root* (concatenate 'string path "/"))))
                   (error (c) (unless (%eexist-p c) (error c))))
              finally (fail "suite-root: no free name below ~A" tmp)))))

(defun allocate-fixture (name &key parent (attempts 16))
  "Create one fresh directory NAME under PARENT (the suite root by default) and
answer its namestring with a trailing slash.

The allocator retries: when a candidate already exists the mkdir fails EEXIST,
that path is left exactly as it was found, and the next candidate is tried. The
path is pushed onto *OWNED-FIXTURES* only after the mkdir has returned, so a
directory this run did not create is never recorded and never cleaned up."
  (let ((base (or parent (suite-root))))
    (loop for attempt from 0 below attempts
          for path = (concatenate 'string base (%fixture-candidate name attempt))
          do (handler-case
                 (progn
                   (sb-posix:mkdir path #o700)
                   ;; created: only now does this run own it.
                   (push path *owned-fixtures*)
                   (return (concatenate 'string path "/")))
               (error (c) (unless (%eexist-p c) (error c))))
          finally (fail "allocate-fixture: ~D candidates for ~A all taken" attempts name))))

(defun fixture-owned-p (path)
  (and (member (string-right-trim "/" (namestring path)) *owned-fixtures*
               :test #'string=)
       t))

(defun release-fixtures ()
  "Delete exactly the recorded paths, deepest (newest) first. No wildcard, no
pattern: a path this run did not create cannot be named here."
  (dolist (path *owned-fixtures*)
    (ignore-errors
      (uiop:delete-directory-tree (uiop:ensure-directory-pathname path)
                                  :validate #'fixture-owned-p
                                  :if-does-not-exist :ignore)))
  (setf *owned-fixtures* '() *suite-root* nil))
