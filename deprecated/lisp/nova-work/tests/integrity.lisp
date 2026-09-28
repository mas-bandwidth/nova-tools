;;;; integrity.lisp --- structural tests of the test suite itself (#1987).
;;;;
;;;; A symbol in nova-work/tests must not be defined as both a macro and a
;;;; function (a name collision that makes load-order matter). And the
;;;; *acceptance-slices* list must have no duplicates.

(in-package #:nova-work/tests)

(defun extract-defs-from-form (form pattern names)
  "Extract symbol names defined by PATTERN from FORM, recursing into container forms."
  (when (consp form)
    (let ((op (first form)))
      (when (symbolp op)
        (cond
          ((string-equal (symbol-name op) pattern)
           (let ((name (second form)))
             (cond
               ((symbolp name)
                (pushnew (string-upcase (symbol-name name)) names :test #'string=))
               ((and (consp name) (symbolp (car name)))
                (pushnew (string-upcase (format nil "~A" name)) names :test #'string=)))))
          ((member (symbol-name op) '("PROGN" "EVAL-WHEN" "LOCALLY") :test #'string-equal)
           (dolist (sub (rest form))
             (setf names (extract-defs-from-form sub pattern names))))))))
  names)

(defun collect-defs-from-stream (stream pattern)
  "Return uppercase symbol names defined by PATTERN in STREAM."
  (let ((names '())
        (*read-eval* nil))
    (loop for form = (read stream nil :eof)
          until (eq form :eof)
          do (setf names (extract-defs-from-form form pattern names)))
    names))

(defun collect-defs-from-file (path pattern)
  "Return uppercase symbol names defined by PATTERN in PATH."
  (with-open-file (s path :direction :input :if-does-not-exist nil)
    (when s
      (collect-defs-from-stream s pattern))))

(defun collect-defs (directory pattern)
  "Return a list of symbol names defined by PATTERN (e.g. \"defmacro\" or \"defun\")
in all .lisp files under DIRECTORY."
  (let ((names '()))
    (dolist (f (directory (merge-pathnames "**/*.lisp" directory)))
      (dolist (name (collect-defs-from-file f pattern))
        (pushnew name names :test #'string=)))
    names))

(defun macro-function-collisions-in-string (string)
  "Return names defined as both defmacro and defun in STRING."
  (let ((macros (with-input-from-string (s string)
                  (collect-defs-from-stream s "defmacro")))
        (funcs  (with-input-from-string (s string)
                  (collect-defs-from-stream s "defun"))))
    (intersection macros funcs :test #'string=)))

(defun macro-function-collisions-in-tests ()
  "Return names that appear as both defmacro and defun in tests/."
  (let* ((test-dir (asdf:system-relative-pathname :nova-work/tests "tests/"))
         (macros (collect-defs test-dir "defmacro"))
         (funcs  (collect-defs test-dir "defun")))
    (intersection macros funcs :test #'string=)))

(deftest "no-symbol-is-both-macro-and-function-in-tests"
    "issue=nova-tools#1987"
    "expected=no-macro-function-collision"
  (let ((collisions (macro-function-collisions-in-tests)))
    (ok (null collisions)
        "symbols defined as both defmacro and defun in tests/: ~S" collisions)))

(deftest "macro-function-collision-detects-uppercase"
    "issue=nova-tools#2476"
    "expected=detect-uppercase-collision"
  (let ((collisions (macro-function-collisions-in-string
                     "(DEFUN collision-target () nil)
                      (defmacro COLLISION-TARGET () nil)")))
    (ok (member "COLLISION-TARGET" collisions :test #'string=)
        "expected COLLISION-TARGET detected as collision, got: ~S" collisions)))

(deftest "macro-function-collision-detects-whitespace-and-newline-variation"
    "issue=nova-tools#2476"
    "expected=detect-whitespace-collision"
  (let ((collisions (macro-function-collisions-in-string
                     "(defun
                         multiline-target
                         () nil)
                      (defmacro ; inline comment
                         multiline-target () nil)")))
    (ok (member "MULTILINE-TARGET" collisions :test #'string=)
        "expected MULTILINE-TARGET detected as collision, got: ~S" collisions)))

(deftest "macro-function-collision-normalizes-reader-casing"
    "issue=nova-tools#2476"
    "expected=detect-escaped-casing-collision"
  (let ((collisions (macro-function-collisions-in-string
                     "(defun |cased-target| () nil)
                      (DEFMACRO CASED-TARGET () nil)")))
    (ok (member "CASED-TARGET" collisions :test #'string=)
        "expected CASED-TARGET detected as collision, got: ~S" collisions)))

(deftest "acceptance-slices-has-no-duplicates"
    "issue=nova-tools#1987"
    "expected=no-duplicate-slices"
  (let ((seen '())
        (dups '()))
    (dolist (s *acceptance-slices*)
      (if (member s seen :test #'string=)
          (push s dups)
          (push s seen)))
    (ok (null dups)
        "duplicate acceptance slices: ~S" dups)))
