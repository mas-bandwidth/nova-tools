;;;; integrity.lisp --- structural tests of the test suite itself (#1987).
;;;;
;;;; A symbol in nova-work/tests must not be defined as both a macro and a
;;;; function (a name collision that makes load-order matter). And the
;;;; *acceptance-slices* list must have no duplicates.

(in-package #:nova-work/tests)

(defun collect-defs (directory pattern)
  "Return a list of symbol names defined by PATTERN (e.g. \"defmacro\" or \"defun\")
in all .lisp files under DIRECTORY."
  (let ((names '()))
    (dolist (f (directory (merge-pathnames "**/*.lisp" directory)))
      (with-open-file (s f :direction :input :if-does-not-exist nil)
        (when s
          (loop for line = (read-line s nil nil) while line
                for pos = (search pattern line)
                when (and pos
                          (> pos 0)
                          (char= #\( (char line (1- pos)))
                          (< (+ pos (length pattern)) (length line))
                          (char= #\Space (char line (+ pos (length pattern)))))
                  do (let* ((after (+ pos (length pattern)))
                            (start (position-if-not (lambda (c) (member c '(#\Space #\Tab)))
                                                    line :start after))
                            (end (when start
                                   (position-if (lambda (c) (member c '(#\Space #\Tab #\( #\))))
                                                line :start start)))
                            (name (when start
                                    (string-downcase (subseq line start (or end (length line)))))))
                       (when (and name (> (length name) 0))
                         (pushnew name names :test #'string=)))))))
    names))

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
