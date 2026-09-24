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

 ;;;; ------------------------------------------------------------------
 ;;;; The sun_path-bounded paths, moved inside the run root
 ;;;; (nova-tools#2463 #1699 #2156).
 ;;;;
 ;;;; THE HURT. The fixtures whose endpoint an AF_UNIX `sun_path` bounds
 ;;;; (104 bytes on darwin, 108 on Linux) named their directories under the
 ;;;; SHARED temporary directory -- /tmp -- on the strength of `test-short-tag`
 ;;;; alone. On a runner whose temporary directory is short that worked: the tag
 ;;;; kept two live runs apart. Inside the card wall it does not even start: the
 ;;;; wall grants no write to /tmp, so `ensure-directories-exist` on the
 ;;;; fixture's absolute name is refused before a socket is reached
 ;;;; (`Can't create directory /tmp/nova-work-reqline-...`), and the wall's own
 ;;;; writable tree sits deeper than `sun_path` can carry, so no absolute name
 ;;;; it allows can hold a socket either. Measured on the wall: eight reds, and
 ;;;; the suite cannot pass inside it at all.
 ;;;;
 ;;;; THE RULE, extended. A socket path bounded by `sun_path` lives under this
 ;;;; run's root too, and is named RELATIVELY: the image's working directory is
 ;;;; the run root (`install-run-local-paths` chdir'd), so the kernel resolves
 ;;;; the name inside the one directory this run owns and removes. A relative
 ;;;; name is short wherever the process can write at all, and the counter under
 ;;;; it is safe because no other process or run can name the root. Eight
 ;;;; copies at once, each with its own TMPDIR, now share nothing -- not the
 ;;;; journals, not the endpoints.
 ;;;; ------------------------------------------------------------------

(defvar *run-root-socket-counter* 0)

(defun %request-line-dir-under-run-root ()
  "The fresh 0700 directory ONE request-line endpoint lives in, named relative
to this run's root. The endpoint refuses a directory whose mode is not 0700,
which is the spec's own requirement, so the test makes one rather than borrowing
the bench's temp directory. Uniqueness comes from `test-short-tag` plus a
counter that only ever counts inside the root; registering the directory hands
it to the harness's exit cleanup."
  (let ((name (format nil "reqline-~A-~D"
                      (test-short-tag "reqline")
                      (incf *run-root-socket-counter*))))
    (ensure-directories-exist (concatenate 'string name "/"))
    (sb-posix:chmod name #o700)
    (test-temp-register name)
    (pathname (concatenate 'string name "/"))))

(defun short-socket-base-under-run-root (prefix)
  "A base name short enough to carry a `sun_path`, named relative to TMPDIR --
not to the working directory. The candidates the old spelling tried --
/dev/shm, /run/user/<uid>, /var/tmp, the ambient temporary directory -- are
each either refused by the card wall or too deep for the 108-byte bound:
inside the wall no absolute path is both writable and short, and a relative
one is short wherever the process can write at all.

The caller (slice-05-durable-journal.lisp's long-TMPDIR fixture) chdir's to
TMPDIR before it ever uses the name this returns, so a bare CLEAN and the
absolute `merge-pathnames ... (test-run-root)` registered below for exit
cleanup named two different directories: the fixture's own `mkdir` landed the
socket directory straight in TMPDIR, while cleanup only ever knew to look
under `test-run-root`. A normal run's own unwind-protect removes both, so the
mismatch was invisible; an interrupted run -- no unwind-protect, no exit-hook,
just gone -- left the one under TMPDIR behind for good (nova-tools#2769 hold
7, review 5291983669).

The run root is always TMPDIR's direct child, so prefixing CLEAN with the run
root's own single path component keeps the name just as short and makes both
resolutions agree: read relative to TMPDIR (the caller's chdir'd cwd) or
merged onto `test-run-root` (this function's own bookkeeping), the name lands
in the one directory a run owns and removes."
  (let ((clean (remove-if-not #'alphanumericp prefix)))
    (if (plusp (length clean))
        (progn
          (test-temp-register
           (namestring (merge-pathnames clean (test-run-root))))
          (let* ((root (string-right-trim "/" (namestring (test-run-root))))
                 (tmp (string-right-trim "/" (namestring (uiop:temporary-directory))))
                 (root-name (if (and (> (length root) (length tmp))
                                      (string= tmp root :end2 (length tmp)))
                                (string-left-trim "/" (subseq root (length tmp)))
                                root)))
            (concatenate 'string root-name "/" clean)))
        clean)))

;;; long-tmpdir-interrupted-run-leaves-nothing-outside-its-root
;;;
;;; Regression for Stella's hold on nova-tools#2769 (review 5291983669, hold
;;; 7): a normal run's own unwind-protect always removed both the run root and
;;; the socket directory the long-TMPDIR branch actually created, so the
;;; mismatch above was invisible until a run never got the chance to unwind at
;;; all. This drives a real child process down exactly that path -- its own
;;; TMPDIR, its own `install-run-local-paths`, its own long-TMPDIR
;;; `short-socket-base` call and `mkdir` -- then kills it outright (SIGKILL:
;;; no unwind-protect, no exit-hook) and checks what the child left directly
;;; under its TMPDIR: the run root, one directory every run makes and a later
;;; reaper's job, and -- the property this test exists to pin -- nothing
;;; beside it.
(deftest "long-tmpdir-interrupted-run-leaves-nothing-outside-its-root"
    "nova-tools#2769 hold 7 (review 5291983669)"
    "interrupted-child-leaves-exactly-one-entry-directly-under-tmpdir"
  (let* ((probe-dir (test-temp-dir "long-tmpdir-probe"))
         (padded (concatenate 'string (string-right-trim "/" (namestring probe-dir))
                              "/" (make-string 60 :initial-element #\x)))
         (sysdir (asdf:system-source-directory "nova-work")))
    (ensure-directories-exist (concatenate 'string padded "/"))
    (ok (>= (length padded) 80)
        "the probe TMPDIR is long enough to hit slice-05's >=80 branch (measured, not assumed)")
    (let* ((child-forms
             (list '(require :asdf)
                   `(push ,sysdir asdf:*central-registry*)
                   '(handler-bind ((warning (function muffle-warning)))
                     (asdf:load-system :nova-work/tests))
                   '(in-package :nova-work/tests)
                   '(install-run-local-paths)
                   `(let ((base (short-socket-base-under-run-root "interruptprobe")))
                      (sb-posix:chdir ,padded)
                      (sb-posix:mkdir base #o700))
                   '(sb-posix:kill (sb-posix:getpid) sb-posix:sigkill)))
           (args (list* "--non-interactive"
                        (mapcan (lambda (f) (list "--eval" (prin1-to-string f)))
                                child-forms)))
           (env (cons (concatenate 'string "TMPDIR=" padded "/")
                      (remove-if (lambda (kv)
                                   (and (>= (length kv) 7)
                                        (string= "TMPDIR=" kv :end2 7)))
                                 (sb-ext:posix-environ))))
           (process (sb-ext:run-program "sbcl" args
                                        :environment env
                                        :output nil :error nil
                                        :search t :wait t)))
      (ok (eq :signaled (sb-ext:process-status process))
          "the child was killed outright -- no unwind-protect, no exit-hook ran")
      (let ((left (uiop:subdirectories (uiop:ensure-directory-pathname padded))))
        (check-equal 1 (length left)
                     "the fixture's directory landed inside the run root, not beside it -- exactly one entry sits directly under TMPDIR")))))

(defun install-run-local-paths ()
  "Move this run's temporary paths where this run owns them, end to end
(nova-tools#2463 #1699 #2156). The working directory becomes the run root and
the default pathname defaults follow it, so the two ways a relative name is
resolved -- merged against the defaults by the ANSI operators, raw against the
working directory by SB-POSIX -- land in the same place: inside the one
directory REMOVE-TEST-RUN-ROOT takes away, which no other process or run can
name. The two sun_path-bounded fixtures are pointed at relative spellings:
their shipped defuns named /tmp or tried absolute candidates, which the wall
refuses outright and eight parallel copies on one runner shared. The ambient
temporary directory is left alone: on a runner whose own is short, the
fixtures' length checks keep their original branch and their original absolute
-- still short -- names."
  (sb-posix:chdir (namestring (test-run-root)))
  (setf *default-pathname-defaults* (test-run-root))
  (setf (fdefinition '%request-line-dir) #'%request-line-dir-under-run-root)
  (setf (fdefinition 'short-socket-base) #'short-socket-base-under-run-root)
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
  ;; the rest (nova-tools#1699). The run's paths are installed first
  ;; (nova-tools#2463 #1699 #2156): the working directory is the run root
  ;; before the first case, so a relative name anywhere in the suite lands
  ;; inside what the exit removes.
  (let ((code (unwind-protect (progn (install-run-local-paths) (run-all))
                (remove-test-run-root))))
    #+sbcl (sb-ext:exit :code code :abort nil)
    #-sbcl (progn code)))
