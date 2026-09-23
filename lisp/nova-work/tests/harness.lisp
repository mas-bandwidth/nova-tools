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

;;;; ------------------------------------------------------------------
;;;; Per-run temporary paths (nova-tools#1699).
;;;;
;;;; THE HURT. Every temp path this suite made was named from
;;;; `(get-universal-time)` plus a counter that starts at zero in every image,
;;;; under a directory name fixed in the source. Two suites that start inside
;;;; the same second on one host therefore build the SAME path: one finds the
;;;; destination already there, or finds the journal's lock held by the other,
;;;; and goes red for something no change in it caused. Measured: three reds on
;;;; PR #1682 (run 35445053795), a red `ci-ok` on PR #1692 on runner
;;;; air-nova-2, and 12-17 manufactured failures with four suites in parallel
;;;; on hulk -- the same suites, run one at a time, green. CI runners share
;;;; hosts (the Air runs two, the Studio several, superman ten), so this
;;;; reddens real PRs and trains reviewers to rerun a red.
;;;;
;;;; THE SECOND HURT, measured while fixing the first (SBCL 2.6.8, and 2.5.8 on
;;;; hetzner): SBCL saves `*random-state*` into the core, so a fresh image
;;;; returns the SAME sequence every time. Three separate images each printed
;;;; `113500 958198 129774` for three calls to `(random 1000000)`. The AF_UNIX
;;;; fixtures in slice-05 and slice-12 named their socket directories
;;;; `nw-<(random 1000000)>`, so two concurrent suites agreed on the path
;;;; exactly -- and `short-socket-base` then DELETED that directory tree before
;;;; binding, taking the other suite's live socket with it. A per-process token
;;;; that is not also random is not enough; the state has to be reseeded from a
;;;; real entropy source.
;;;;
;;;; THE RULE. Uniqueness comes from this run's token -- a real entropy source
;;;; plus the pid -- and never from a counter or a clock. Every temp path is
;;;; built under `test-run-root`, one directory this image creates and removes;
;;;; a counter inside it is safe because no other process or run can name it.
;;;; A path bounded by `sun_path` (108 bytes) cannot fit under that root and
;;;; uses `test-short-tag` instead. `internal/ci/lisptemppath_class_test.go`
;;;; holds a test to this rule.
;;;; ------------------------------------------------------------------

(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defun %urandom-integer (n-bytes)
  "N-BYTES of /dev/urandom as a non-negative integer, or NIL when the device
cannot be read. The real entropy source: two images started in the same second
must not agree, which a clock and a counter cannot promise."
  (ignore-errors
    (with-open-file (in "/dev/urandom" :direction :input
                                       :element-type '(unsigned-byte 8)
                                       :if-does-not-exist nil)
      (when in
        (let ((buf (make-array n-bytes :element-type '(unsigned-byte 8))))
          (when (= n-bytes (read-sequence buf in))
            (let ((n 0))
              (loop for b across buf do (setf n (+ (ash n 8) b)))
              n)))))))

(defvar *test-run-entropy* nil)

(defun test-run-entropy ()
  "This image's own entropy, drawn once. /dev/urandom when it is readable;
otherwise SB-EXT:SEED-RANDOM-STATE with T, which seeds from the system entropy
source and/or the clock. The pid is mixed in either way, so two images that
somehow draw the same bits still differ."
  (or *test-run-entropy*
      (setf *test-run-entropy*
            (logand (logxor (or (%urandom-integer 8)
                                (let ((*random-state* (sb-ext:seed-random-state t)))
                                  (random (expt 2 64))))
                            (ash (sb-posix:getpid) 24)
                            (sb-posix:getpid)
                            (get-internal-real-time))
                    (1- (expt 2 64))))))

(defun reseed-random-state ()
  "Reseed *RANDOM-STATE* from this run's entropy. SBCL saves the random state
into the core, so without this every fresh image returns the same sequence and
`(random 1000000)` is a constant across processes (nova-tools#1699)."
  (setf *random-state* (sb-ext:seed-random-state (test-run-entropy)))
  (values))

(defvar *test-run-token* nil)

(defun test-run-token ()
  "A name unique to THIS process and THIS run: the pid and a random token."
  (or *test-run-token*
      (setf *test-run-token*
            (string-downcase (format nil "~D-~36R" (sb-posix:getpid)
                                     (test-run-entropy))))))

(defun test-short-tag (label)
  "A short alphanumeric name unique to this run and to LABEL, for a path bounded
by `sun_path` (about 108 bytes), which the run root is far too long to fit.
LABEL is folded into the entropy rather than appended, so two call sites in one
image differ while a length budget that trims the tail cannot eat what makes the
name unique."
  (string-downcase
   (format nil "nw~36R"
           (mod (logxor (test-run-entropy)
                        (* 2654435761 (sxhash (string label))))
                (expt 36 9)))))

(defvar *test-run-root* nil)
(defvar *test-temp-extra* '()
  "Paths outside the run root -- the AF_UNIX fixtures, which cannot fit under
it -- removed by REMOVE-TEST-RUN-ROOT alongside it.")
(defvar *test-temp-seq* 0)

(defun test-temp-register (path)
  "Have REMOVE-TEST-RUN-ROOT remove PATH too. For a path `sun_path` keeps out
of the run root; nothing else needs it."
  (pushnew (namestring path) *test-temp-extra* :test #'string=)
  path)

(defun test-run-root ()
  "The ONE directory this run's temp files live under, created 0700 on first
use and removed when the image exits. Its name carries the pid and this image's
random token, so no other process and no later run can name it."
  (or *test-run-root*
      (let ((dir (merge-pathnames
                  (format nil "nova-work-test-~A/" (test-run-token))
                  (uiop:default-temporary-directory))))
        (ensure-directories-exist dir)
        (ignore-errors (sb-posix:chmod (namestring dir) #o700))
        (pushnew 'remove-test-run-root sb-ext:*exit-hooks*)
        (setf *test-run-root* dir))))

(defun test-temp-dir (name)
  "A fresh 0700 directory NAME-<n> under this run's root. The counter is safe
here: it is scoped inside a directory only this run can name."
  (let ((dir (merge-pathnames (format nil "~A-~D/" name (incf *test-temp-seq*))
                              (test-run-root))))
    (ensure-directories-exist dir)
    (ignore-errors (sb-posix:chmod (namestring dir) #o700))
    dir))

(defun test-temp-file (name type)
  "A fresh path NAME-<n>.TYPE under this run's root, as a string. The file is
not created; its parent is."
  (format nil "~A~A-~D.~A" (namestring (test-run-root)) name
          (incf *test-temp-seq*) type))

(defun remove-test-run-root ()
  "Remove this run's root, and the registered paths outside it, with everything
under them. Nothing another process owns can be reached: the root's name carries
this image's random token, which is exactly what the blind
`delete-directory-tree` in the old socket fixture could not say."
  (let ((root *test-run-root*)
        (extra *test-temp-extra*))
    (setf *test-run-root* nil *test-temp-extra* '())
    (dolist (path extra)
      (ignore-errors
        (uiop:delete-directory-tree (uiop:ensure-directory-pathname path)
                                    :validate t :if-does-not-exist :ignore)))
    (when root
      (ignore-errors
        (uiop:delete-directory-tree root :validate t :if-does-not-exist :ignore))))
  (values))

(defun run-all ()
  (reseed-random-state)
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
  ;; Cleanup on exit, on both paths: the unwind-protect covers the normal one
  ;; and a Lisp error, and REMOVE-TEST-RUN-ROOT is on SB-EXT:*EXIT-HOOKS* for
  ;; the rest (nova-tools#1699).
  (let ((code (unwind-protect (run-all) (remove-test-run-root))))
    #+sbcl (sb-ext:exit :code code :abort nil)
    #-sbcl (progn code)))
