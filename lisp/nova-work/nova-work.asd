;;;; nova-work.asd --- slice 1 of the nova-work engine.
;;;;
;;;; The spec fixes the engine's language (Common Lisp, docs/SPEC-WORK.md:290)
;;;; and fixes no directory for it. `lisp/nova-work/` with this ASDF system is a
;;;; layout choice for review, beside the Go client's existing `cmd/` and
;;;; `internal/`.
;;;;
;;;; TWO LISTS, ON PURPOSE (nova-tools#1947).
;;;;
;;;; `nova-work`'s `src` components are an ORDERED SEMANTIC LIST and stay
;;;; written out here: `:serial t` makes the order the load order, and several
;;;; files depend on earlier ones (src/package before everything, src/state
;;;; before src/kernel). Adding a kernel file is a decision about where it goes,
;;;; so it is made here, in the open, by a human or a reviewer.
;;;;
;;;; `nova-work/tests`'s components are NOT a decision. Every one is a leaf that
;;;; registers `deftest` cases against the same two shared files, and nothing
;;;; else reads it. Writing them out cost thirteen pull requests one night:
;;;; every PR that adds a test appends one more component line at the same
;;;; position in this list, so EVERY PAIR of open nova-work PRs conflicted on
;;;; this file and on no other file at all (#1947). Putting the closing parens
;;;; on their own line does not fix that — two insertions at one position still
;;;; conflict under `git merge-tree`, measured 2026-09-19.
;;;;
;;;; So the test list is DISCOVERED from the directory at the moment this file
;;;; is read, and adding a test no longer touches this file at all. The order is
;;;; not arbitrary: an explicit PRELUDE (`tests/harness`, then
;;;; `tests/acceptance`) is loaded first, because harness.lisp defines `deftest`
;;;; and acceptance.lisp defines the shared fixtures and the loader for
;;;; `tests/acceptance/*.lisp` that every other test file uses. Everything after
;;;; it is sorted by its canonical system-relative name with `string<`, so the
;;;; list is the same on every machine and in every checkout. That the rest of
;;;; the order does not matter is MEASURED, not assumed: see
;;;; `tools/asd-order-check.sh` and its receipt, which runs the whole suite with
;;;; the tests in current, sorted and reverse-sorted order in three fresh images
;;;; and compares the test-name sets and every case's outcome.
;;;;
;;;; THE RELOAD CAVEAT. ASDF discovers when THIS FILE is read, not when the
;;;; system is loaded. A long-lived image that has already read nova-work.asd
;;;; will not see a test file added since; it has to reread the .asd
;;;; (`(asdf:clear-system :nova-work/tests)` and
;;;; `(asdf:load-asd #p".../nova-work.asd")`, or just start a fresh image).
;;;; `run-tests.sh` and `tools/ci/lisp-test.sh` are always fresh images, so this
;;;; costs CI nothing; it costs an interactive session one form. It is written
;;;; down again in README.md ("Adding a test file") and in docs/SPEC-CI.md's
;;;; `kernel-components` rule, which is the entry AGENTS.md points a friend at.

(defpackage #:nova-work-asdf
  (:documentation "Read-time helpers for nova-work.asd. Not part of the system.")
  (:use #:common-lisp)
  (:export #:discovery-error
           #:+test-prelude+
           #:test-components
           #:discovered-test-names
           #:canonical-test-names))

(in-package #:nova-work-asdf)

(defparameter *here*
  (make-pathname :name nil :type nil :version nil
                 :defaults (or *load-truename* *load-pathname* *default-pathname-defaults*))
  "The directory holding this .asd, captured while it is being loaded.")

(defparameter +test-prelude+
  '("tests/harness" "tests/acceptance")
  "The test files that are NOT discovered, in the order they must load.

tests/harness.lisp defines `deftest` and the runner; tests/acceptance.lisp
defines the shared seed and request fixtures AND `load`s the per-slice files
under tests/acceptance/. Every other test file uses both, so both load before
any of them. This list is two names because the tree says two: docs/SPEC-CI.md
`kernel-components` and the parity test hold it against the tree.")

(define-condition discovery-error (error)
  ((text :initarg :text :reader discovery-error-text))
  (:report (lambda (c s) (write-string (discovery-error-text c) s)))
  (:documentation "Signalled while nova-work.asd is being READ, so a bad tests/
directory refuses the system loudly instead of silently compiling the wrong set
or a file from outside the tree."))

(defun fail (control &rest args)
  (error 'discovery-error
         :text (apply #'format nil (concatenate 'string "nova-work.asd: " control) args)))

(defun name-char-p (c)
  "A test file name holds only characters that mean the same thing to git, to
ASDF's pathname parsing and to every filesystem in the fleet: ASCII letters,
ASCII digits, '-', '_' and '.'. The ranges are explicit on purpose: ALPHA-CHAR-P
and DIGIT-CHAR-P accept non-ASCII letters and digits (SBCL takes U+00E9 as
a letter), and those normalise differently on the darwin runners' filesystem."
  (or (char<= #\a c #\z)
      (char<= #\A c #\Z)
      (char<= #\0 c #\9)
      (member c '(#\- #\_ #\.) :test #'char=)))

(defun check-file-name (name)
  "Refuse a name that could escape tests/ or be read two ways. NAME is the
pathname-name of a file found directly in tests/."
  (unless (and (stringp name) (plusp (length name)))
    (fail "tests/ holds a .lisp entry with no usable name"))
  (unless (every #'name-char-p name)
    (fail "tests/~A.lisp: a test file name may hold only ASCII letters, digits, '-', '_' and '.'; ~
this one does not, and ASDF would read its component name differently from git" name))
  (when (char= (char name 0) #\.)
    (fail "tests/~A.lisp: a test file name may not begin with '.'" name))
  (when (search ".." name)
    (fail "tests/~A.lisp: a test file name may not hold '..'; a component name is joined to the ~
system directory, so this is a path escape" name))
  name)

(defun check-not-a-link (file dir-true)
  "Refuse FILE if it is a symbolic link. ASDF compiles what the name resolves
to, so a link would pull a file from outside the tree into the system while the
component name says it is in tests/ -- and `git archive` would not carry it."
  (let ((true (ignore-errors (truename file))))
    (unless true
      (fail "tests/~A.lisp cannot be resolved; a dangling symbolic link, or it was removed while ~
this file was being read" (pathname-name file)))
    (unless (and (equal (pathname-name true) (pathname-name file))
                 (equal (pathname-type true) (pathname-type file))
                 (equal (namestring (make-pathname :name nil :type nil :version nil :defaults true))
                        (namestring dir-true)))
      (fail "tests/~A.lisp is a symbolic link (it resolves to ~A); the tests system takes only ~
regular files that live in tests/ itself" (pathname-name file) (namestring true)))
    true))

(defun canonical-test-names (names)
  "Refuse duplicates and case collisions in NAMES, then return them sorted with
STRING<. NAMES are canonical system-relative names like \"tests/decide\"."
  (let ((seen (make-hash-table :test #'equal))
        (folded (make-hash-table :test #'equal)))
    (dolist (n names)
      (when (gethash n seen)
        (fail "the tests directory yields the component ~S twice; ASDF would register it twice ~
and its cases would be counted twice" n))
      (setf (gethash n seen) t)
      (let ((key (string-downcase n)))
        (when (gethash key folded)
          (fail "~S and ~S differ only by case; on a case-insensitive filesystem (the darwin ~
runners) they are one file, so the system would mean something different there" (gethash key folded) n))
        (setf (gethash key folded) n))))
  (sort (copy-list names) #'string<))

(defun discovered-test-names (&optional (here *here*))
  "The canonical system-relative names of the tests to discover, sorted.

Only regular files matching tests/*.lisp DIRECTLY in tests/: `directory-files`
does not recurse, so tests/acceptance/ is never entered -- those slice files are
`load`ed by tests/acceptance.lisp and must not also be ASDF components. The
prelude is excluded here because the system names it explicitly, first."
  (let* ((dir (merge-pathnames #p"tests/" here))
         (dir-true (or (ignore-errors (truename dir))
                       (fail "~A does not exist; the tests system has no files to discover"
                             (namestring dir))))
         (files (uiop:directory-files dir "*.lisp"))
         (names '()))
    (dolist (f files)
      (check-file-name (pathname-name f))
      (check-not-a-link f dir-true)
      (push (concatenate 'string "tests/" (pathname-name f)) names))
    (let ((all (canonical-test-names names)))
      (dolist (p +test-prelude+)
        (unless (member p all :test #'string=)
          (fail "the prelude names ~S and lisp/nova-work/~A.lisp is not in the tests directory; ~
the prelude is what every other test file depends on, so this is not a discovery problem" p p)))
      (remove-if (lambda (n) (member n +test-prelude+ :test #'string=)) all))))

(defun test-components (&optional (here *here*))
  "The whole :components list for nova-work/tests: the prelude, in its own
order, then the discovered tests sorted by canonical name."
  (mapcar (lambda (n) (list :file n))
          (append +test-prelude+ (discovered-test-names here))))

(asdf:defsystem "nova-work"
  :description "nova-work slice 1: the internal C/O transition kernel with a write-maintained |O|."
  :author "Rowan <rowan@mas-bandwidth.com>"
  :license "See LICENSE at the repository root"
  :serial t
  ;; ORDERED AND SEMANTIC: `:serial t` makes this the load order and later files
  ;; depend on earlier ones. One component per line, closing parens on their own
  ;; line, so two branches adding different kernel files touch different lines.
  :components (
               (:file "src/package")
               (:file "src/conditions")
               (:file "src/sha256")
               (:file "src/value")
               (:file "src/event")
               (:file "src/state")
               (:file "src/indexes")
               (:file "src/dependencies")
               (:file "src/lock")
               (:file "src/journal-identity")
               (:file "src/journal")
               (:file "src/kernel")
               (:file "src/command-thread")
               (:file "src/session")
               (:file "src/state-export")
               (:file "src/verifier")
               (:file "src/verification-cache-file")
               (:file "src/verify-resolver")
               (:file "src/receipt-admission")
               (:file "src/needs")
               (:file "src/fleet")
               (:file "src/routes")
               (:file "src/closed-history")
               (:file "src/node-verbs")
               (:file "src/render")
               (:file "src/render-filesystem")
               (:file "src/edit-undo")
               (:file "src/take-verb")
               (:file "src/dep-verb")
               (:file "src/undo")
               (:file "src/pricing")
               (:file "src/operations")
               (:file "src/transport")
               (:file "src/scheduler")
               (:file "src/operation-recovery")
               (:file "src/receipts")
               (:file "src/capture")
               (:file "src/assignment")
               (:file "src/control")
               (:file "src/roadmap")
               (:file "src/savepoint")
               (:file "src/savepoint-create")
               (:file "src/savepoint-restore")
               (:file "src/compaction")
               (:file "src/journal-rotate")
               (:file "src/new-verbs")
               (:file "src/decide")
               (:file "src/request-line")
               (:file "src/replays-verdict-state")
               (:file "src/replays-notes")
               (:file "src/dedup-root")
               (:file "src/execution-reconcile")
               ))

(asdf:defsystem "nova-work/tests"
  :description "The slice-1 acceptance cases, each naming its line of docs/SPEC-WORK.md."
  :depends-on ("nova-work")
  :serial t
  ;; DISCOVERED: the prelude, then every other regular tests/*.lisp sorted by
  ;; canonical name. Adding a test file adds no line here, so two branches that
  ;; each add a test no longer conflict (#1947). tests/asd-discovery.lisp holds
  ;; this against the directory every run.
  :components #.(nova-work-asdf:test-components))
