;;;; private-root.lisp --- the suite process's one private temporary root.
;;;;
;;;; Every filesystem fixture the acceptance replays write (journals, state
;;;; exports, AF_UNIX socket directories) lives beneath one root that this
;;;; suite process alone created. Two concurrent suites sharing one ambient
;;;; TMPDIR (concurrent merge groups, 2026-09-18) previously collided:
;;;; "held by another process", "destination exists", MKDIR ENOENT. The root
;;;; is created with an exclusive mkdir and a name that does not rest on the
;;;; PID alone, so a recycled PID or an inherited random state cannot adopt
;;;; another run's namespace. A collision retries a fresh name and never
;;;; deletes, adopts or modifies the candidate it found.

(in-package #:nova-work/tests)

(defvar *suite-root* nil
  "The one private root this suite process created, or NIL before first use.")

(defvar *suite-attempt* 0
  "Bumped for each candidate name, so retries cannot repeat one another.")

(defun %suite-token ()
  "An unpredictable token for one candidate name. PID alone is not unique
(recycled PIDs) and a forked child inherits the parent's random state, so mix
the process, the clocks, a per-process counter and a fresh entropy-seeded draw."
  (format nil "~D-~D-~D-~36R"
          (sb-posix:getpid)
          (get-universal-time)
          (incf *suite-attempt*)
          (random (ash 1 64) (make-random-state t))))

(defun %mkdir-exclusive (path)
  "Create PATH mode 0700. Answer T when this process created it, NIL when the
candidate already exists, :denied when the parent cannot be written. Never
deletes, adopts or modifies an existing candidate."
  (handler-case
      (progn (sb-posix:mkdir path #o700) t)
    (sb-posix:syscall-error (e)
      (case (sb-posix:syscall-errno e)
        (#.sb-posix:eexist nil)
        ((#.sb-posix:eacces #.sb-posix:enoent #.sb-posix:eperm) :denied)
        (t (error "private-root: cannot create ~A: ~A" path e))))))

(defun private-root-parents ()
  "The candidate ambient parents, in order: a short parent first so an absolute
AF_UNIX endpoint would fit where the platform allows one, then TMPDIR (the
card sandbox permits only the job directory)."
  (remove-duplicates
   (remove nil
           (list (ignore-errors (uiop:getenv "TMPDIR"))
                 (ignore-errors (namestring (uiop:temporary-directory)))
                 "/dev/shm"
                 (format nil "/run/user/~D" (sb-posix:getuid))
                 "/var/tmp"
                 "/tmp"))
   :test #'string=))

(defun test-private-root ()
  "The suite's one private root, created on first use with an exclusive mkdir.
Each call in one process answers the same root. A candidate that already exists
is abandoned for a fresh unpredictable name; the existing path is untouched."
  (or *suite-root*
      (dolist (parent (private-root-parents) nil)
        (let ((created
                (loop repeat 64
                      for clean = (string-right-trim "/" parent)
                      for candidate = (concatenate 'string clean "/nw-" (%suite-token))
                      for made = (%mkdir-exclusive candidate)
                      when (eq made t) return candidate
                      when (eq made :denied) return nil)))
          (when created
            (return (setf *suite-root* created)))))
      (error "private-root: no permitted parent could host the suite root; tried ~S"
             (private-root-parents))))

(defun test-private-dir (name)
  "A directory NAME beneath the suite's private root, created if needed."
  (let ((dir (concatenate 'string
                          (string-right-trim "/" (test-private-root))
                          "/" name "/")))
    (ensure-directories-exist dir)
    dir))

(defun test-private-root-cleanup ()
  "Remove the root this process successfully created, once its own children and
servers are torn down. Only a root this process created is ever removed, and
an ambient or neighbouring path is never touched."
  (when *suite-root*
    (ignore-errors
      (uiop:delete-directory-tree (uiop:ensure-directory-pathname *suite-root*)
                                  :validate nil :if-does-not-exist :ignore))
    (setf *suite-root* nil)))

;;; ------------------------------------------------------------------
;;; Regression: exclusive creation, collision retry, no adoption
;;; ------------------------------------------------------------------

(deftest "private-root-collision-retries-without-adoption" "docs/SPEC-WORK.md:5896"
    "expected=candidate-and-neighbour-unchanged;second-create-loses;fresh-name-wins"
  (let* ((parent (string-right-trim "/" (first (private-root-parents))))
         (candidate (concatenate 'string parent "/nw-regression-" (%suite-token)))
         (neighbour (concatenate 'string candidate "-neighbour")))
    (ok (eq t (%mkdir-exclusive candidate)) "the first exclusive create wins")
    (ok (eq t (%mkdir-exclusive neighbour)) "the neighbouring sentinel is created")
    (unwind-protect
         (progn
           (with-open-file (f (concatenate 'string candidate "/payload")
                              :direction :output :if-exists :supersede
                              :if-does-not-exist :create)
             (write-string "keep" f))
           (ok (null (%mkdir-exclusive candidate))
               "a second create of the same name loses rather than adopting")
           (check-string= "keep"
                          (uiop:read-file-string
                           (concatenate 'string candidate "/payload"))
                          "the pre-existing candidate is unchanged")
           (ok (eq t (%mkdir-exclusive
                      (concatenate 'string candidate "-fresh" "-" (%suite-token))))
               "allocation retries with a different exclusively owned name"))
      (ignore-errors
        (uiop:delete-directory-tree (uiop:ensure-directory-pathname candidate)
                                    :validate nil :if-does-not-exist :ignore))
      (ignore-errors
        (uiop:delete-directory-tree (uiop:ensure-directory-pathname neighbour)
                                    :validate nil :if-does-not-exist :ignore))
      (mapc (lambda (p) (ignore-errors
                          (uiop:delete-directory-tree
                           (uiop:ensure-directory-pathname p)
                           :validate nil :if-does-not-exist :ignore)))
            (directory (concatenate 'string candidate "-fresh" "-*"))))))

(deftest "suite-root-is-private-and-shared-within-the-process" "docs/SPEC-WORK.md:5896"
    "expected=one-root-per-process;root-exists-under-a-permitted-parent"
  (let* ((a (test-private-root))
         (b (test-private-root)))
    (check-string= a b "one process answers one root")
    (ok (probe-file (uiop:ensure-directory-pathname a)) "the root exists")
    (ok (some (lambda (p)
                (let ((clean (string-right-trim "/" p)))
                  (and (>= (length a) (length clean))
                       (string= clean (subseq a 0 (length clean))))))
              (private-root-parents))
        "the root sits beneath a permitted ambient parent: ~A" a)))
