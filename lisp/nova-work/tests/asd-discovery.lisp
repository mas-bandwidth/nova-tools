;;;; asd-discovery.lisp --- the tests system is the tests directory.
;;;;
;;;; nova-work.asd stopped writing its test components out by hand
;;;; (nova-tools#1947): every PR that added a test appended a line at the same
;;;; position in one list, so every pair of open nova-work PRs conflicted on
;;;; that file and on no other file at all. The list is now the prelude
;;;; (tests/harness, then tests/acceptance) followed by every other regular
;;;; tests/*.lisp, sorted by canonical name.
;;;;
;;;; Discovery buys the merge back and sells a different risk: a list nobody
;;;; writes is a list nobody reads, and a file that quietly stops being
;;;; discovered is a file SBCL never reads -- which is exactly the hurt
;;;; docs/SPEC-CI.md's `kernel-components` rule exists for (28 of 60 src files
;;;; compiled by nobody while the suite said total=327 pass=327 fail=0). So the
;;;; cases below are the price of the idiom, and they run every time the suite
;;;; runs:
;;;;
;;;;   parity        -- the components ARE the directory, computed here from the
;;;;                    filesystem without asking the .asd's own function
;;;;   exactly once  -- no component registered twice, every one a real file
;;;;   prelude       -- harness before acceptance before everything, and those
;;;;                    two really are what the rest depends on
;;;;   no recursion  -- tests/acceptance/*.lisp are loaded by acceptance.lisp and
;;;;                    are NOT components; discovery never enters that directory
;;;;   refusals      -- duplicates, case collisions, path escapes and symlinks
;;;;                    are a loud error while the .asd is read, not a silent
;;;;                    change of which files compile
;;;;
;;;; The order-independence of everything after the prelude is measured, not
;;;; asserted here: tools/asd-order-check.sh runs the whole suite three times in
;;;; fresh images (current, sorted, reverse) and compares the test-name sets and
;;;; every case's outcome.

(in-package #:nova-work/tests)

(defun asd-system-directory ()
  (asdf:system-relative-pathname :nova-work/tests ""))

(defun asd-component-names ()
  "The component names nova-work/tests actually registered, in load order."
  (mapcar #'asdf:component-name
          (asdf:component-children (asdf:find-system "nova-work/tests"))))

(defun asd-directory-names (&optional (here (asd-system-directory)))
  "The canonical names of the regular files DIRECTLY in tests/, read from the
filesystem here rather than from the .asd, so this is a parity check and not a
restatement of the code under test."
  (sort (mapcar (lambda (f) (concatenate 'string "tests/" (pathname-name f)))
                (uiop:directory-files (merge-pathnames #p"tests/" here) "*.lisp"))
        #'string<))

(defun asd-write-file (path text)
  (with-open-file (s path :direction :output :if-exists :supersede
                          :if-does-not-exist :create)
    (write-string text s))
  path)

(defmacro with-fake-tests-tree ((var) &body body)
  "A throwaway system directory with its own tests/ holding just the prelude.
VAR is bound to the system directory. Removed however BODY leaves.

The directory comes from TEST-TEMP-DIR, under this run's own root
(nova-work-test-<pid>-<token>/), never a bare RANDOM call against
UIOP:TEMPORARY-DIRECTORY: a fresh SBCL image's *RANDOM-STATE* is saved in the
core, so RANDOM returns the same number every run, and the shared system temp
directory is written by every job on the host (nova-tools#1699)."
  `(let ((,var (uiop:ensure-directory-pathname (test-temp-dir "nova-work-asd"))))
     (unwind-protect
          (progn
            (uiop:ensure-all-directories-exist
             (list (merge-pathnames #p"tests/" ,var)))
            (asd-write-file (merge-pathnames #p"tests/harness.lisp" ,var) ";; fake")
            (asd-write-file (merge-pathnames #p"tests/acceptance.lisp" ,var) ";; fake")
            ,@body)
       (ignore-errors (uiop:delete-directory-tree ,var :validate t :if-does-not-exist :ignore)))))

;;; ------------------------------------------------------------------
;;; 1. The components ARE the directory.
;;; ------------------------------------------------------------------

(deftest "asd-tests-components-match-the-directory" "nova-tools#1947"
    "nova-work/tests registers exactly the prelude followed by every other regular tests/*.lisp, sorted"
  (let* ((components (asd-component-names))
         (on-disk (asd-directory-names))
         (expected (append nova-work-asdf:+test-prelude+
                           (remove-if (lambda (n)
                                        (member n nova-work-asdf:+test-prelude+ :test #'string=))
                                      on-disk))))
    (check-equal expected components
                 "the tests system's components against the tests directory")
    ;; and the other direction: nothing registered that is not a file
    (dolist (c components)
      (ok (probe-file (merge-pathnames (concatenate 'string c ".lisp")
                                       (asd-system-directory)))
          "the system registers ~S and lisp/nova-work/~A.lisp does not exist" c c))))

;;; ------------------------------------------------------------------
;;; 2. Exactly once.
;;; ------------------------------------------------------------------

(deftest "asd-tests-registered-exactly-once" "nova-tools#1947"
    "no test file is an ASDF component twice, so no file is compiled or registered twice"
  (let ((components (asd-component-names)))
    (dolist (c components)
      (check-equal 1 (count c components :test #'string=)
                   (format nil "how many times ~S is a component" c)))
    (check-equal (length components)
                 (length (remove-duplicates (mapcar #'string-downcase components) :test #'string=))
                 "components that differ only by case would be one file on darwin")))

;;; ------------------------------------------------------------------
;;; 3. The prelude, in Stella's order, and it really is the shared part.
;;; ------------------------------------------------------------------

(deftest "asd-tests-prelude-is-harness-then-acceptance" "nova-tools#1947"
    "tests/harness loads first and tests/acceptance second, because acceptance defines the shared helpers every other test file uses"
  (let ((components (asd-component-names)))
    (check-equal '("tests/harness" "tests/acceptance")
                 (subseq components 0 2)
                 "the first two components of nova-work/tests")
    (check-equal '("tests/harness" "tests/acceptance") nova-work-asdf:+test-prelude+
                 "the prelude the .asd declares"))
  ;; The reason for the order, held against the tree rather than the comment:
  ;; harness.lisp defines the runner and acceptance.lisp the shared fixtures and
  ;; the slice loader. If either moved, the prelude would be wrong.
  (ok (macro-function 'deftest) "tests/harness.lisp must define the DEFTEST macro")
  (ok (fboundp 'fresh) "tests/acceptance.lisp must define the shared FRESH fixture")
  (ok (fboundp 'close-request) "tests/acceptance.lisp must define the shared CLOSE-REQUEST fixture")
  (ok (boundp '*acceptance-slices*) "tests/acceptance.lisp must define the slice loader's list"))

;;; ------------------------------------------------------------------
;;; 4. Nested fixtures are loaded, never discovered.
;;; ------------------------------------------------------------------

(deftest "asd-tests-do-not-recurse-into-acceptance" "nova-tools#1947"
    "tests/acceptance/*.lisp are loaded by acceptance.lisp and are not ASDF components; discovery never enters that directory"
  (let ((components (asd-component-names))
        (nested (uiop:directory-files
                 (merge-pathnames #p"tests/acceptance/" (asd-system-directory)) "*.lisp")))
    (ok (plusp (length nested))
        "tests/acceptance/ must hold the slice files this case is about")
    (dolist (c components)
      (ok (not (search "tests/acceptance/" c))
          "~S is a component; the slice files under tests/acceptance/ are loaded by ~
tests/acceptance.lisp, and compiling them as components too would register every case twice" c))
    ;; Discovery on the real tree returns nothing from the nested directory.
    (dolist (n (nova-work-asdf:discovered-test-names (asd-system-directory)))
      (ok (not (search "/" (subseq n (length "tests/"))))
          "discovery returned ~S, which is not directly in tests/" n))))

;;; ------------------------------------------------------------------
;;; 5. The refusals. Each is a loud error while the .asd is read.
;;; ------------------------------------------------------------------

(deftest "asd-discovery-refuses-a-duplicate-component" "nova-tools#1947"
    "two identical canonical names are a DISCOVERY-ERROR, never a list with the file twice"
  (handler-case
      (progn (nova-work-asdf:canonical-test-names '("tests/a" "tests/b" "tests/a"))
             (fail "a duplicated component name was accepted"))
    (nova-work-asdf:discovery-error (c)
      (ok (search "twice" (princ-to-string c))
          "the refusal must say what is wrong: ~A" c))))

(deftest "asd-discovery-refuses-a-case-collision" "nova-tools#1947"
    "two names that differ only by case are a DISCOVERY-ERROR, because on darwin they are one file"
  (handler-case
      (progn (nova-work-asdf:canonical-test-names '("tests/Decide" "tests/decide"))
             (fail "a case collision was accepted"))
    (nova-work-asdf:discovery-error (c)
      (ok (search "case" (princ-to-string c))
          "the refusal must name the collision: ~A" c))))

(deftest "asd-discovery-sorts-with-string<" "nova-tools#1947"
    "the accepted names come back sorted by canonical system-relative name"
  (check-equal '("tests/a" "tests/b" "tests/z")
               (nova-work-asdf:canonical-test-names '("tests/z" "tests/a" "tests/b"))
               "canonical-test-names sorts with string<"))

(deftest "asd-discovery-refuses-a-path-escape" "nova-tools#1947"
    "a component name holding '..' or a character git and ASDF read differently is a DISCOVERY-ERROR"
  (dolist (bad '("a..b" "no good" "od\"d"))
    (handler-case
        (progn (nova-work-asdf::check-file-name bad)
               (fail "the file name ~S was accepted" bad))
      (nova-work-asdf:discovery-error (c)
        (ok (plusp (length (princ-to-string c))) "the refusal must say something")))))

(deftest "asd-discovery-refuses-a-symlink" "nova-tools#1947"
    "a symbolic link in tests/ is a DISCOVERY-ERROR: ASDF would compile a file from outside the tree and git archive would not carry it"
  (with-fake-tests-tree (root)
    (let ((real (asd-write-file (merge-pathnames #p"outside.lisp" root) ";; outside")))
      ;; A plain file is fine.
      (asd-write-file (merge-pathnames #p"tests/good.lisp" root) ";; good")
      (check-equal '("tests/good") (nova-work-asdf:discovered-test-names root)
                   "a regular tests/*.lisp is discovered")
      ;; The same file reached through a link is not.
      (uiop:run-program (list "ln" "-s" (namestring real)
                              (namestring (merge-pathnames #p"tests/linked.lisp" root)))
                        :output nil :error-output nil)
      (handler-case
          (progn (nova-work-asdf:discovered-test-names root)
                 (fail "a symbolic link in tests/ was discovered as a component"))
        (nova-work-asdf:discovery-error (c)
          (ok (search "symbolic link" (princ-to-string c))
              "the refusal must name the link: ~A" c))))))

(deftest "asd-discovery-refuses-a-missing-prelude" "nova-tools#1947"
    "a tests/ directory without harness.lisp or acceptance.lisp is a DISCOVERY-ERROR, not a system that loads with no fixtures"
  (with-fake-tests-tree (root)
    (delete-file (merge-pathnames #p"tests/acceptance.lisp" root))
    (handler-case
        (progn (nova-work-asdf:discovered-test-names root)
               (fail "a tests/ directory with no acceptance.lisp was accepted"))
      (nova-work-asdf:discovery-error (c)
        (ok (search "prelude" (princ-to-string c))
            "the refusal must name the prelude: ~A" c)))))

(deftest "asd-discovery-excludes-the-prelude-from-the-discovered-set" "nova-tools#1947"
    "the prelude is named explicitly and never discovered a second time"
  (with-fake-tests-tree (root)
    (asd-write-file (merge-pathnames #p"tests/zebra.lisp" root) ";; z")
    (check-equal '("tests/zebra") (nova-work-asdf:discovered-test-names root)
                 "discovery drops the prelude it found on disk")
    (check-equal '((:file "tests/harness") (:file "tests/acceptance") (:file "tests/zebra"))
                 (nova-work-asdf:test-components root)
                 "the whole component list: prelude first, then the discovered set")))
