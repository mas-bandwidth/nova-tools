;;;; slice-19-verification-cache-file.lisp --- the verification cache file.
;;;;
;;;; SPEC-WORK.md:1294-1325: the cache holds raw resolutions keyed by the
;;;; pointer, the subject and the resolver's identity; one session has one
;;;; cache and its path is `session start --cache`. These cases drive the
;;;; file the writer makes and the reader reads back.

(in-package #:nova-work/tests)

(defvar *cache-file-seq* 0
  "The counter that keeps each case's temp file apart INSIDE this run's root.
It carries no uniqueness of its own: the root's name does (harness.lisp).")

(defun cache-temp-path (&optional (suffix ""))
  "A fresh cache path under this run's own root (docs/SPEC-WORK.md:1294-1325).
It was `$TMPDIR/nova-work-cache-<pid>-<counter>`, written straight into the
directory every job on the box shares. The pid kept two LIVE processes apart
but not two runs: a recycled pid met the file an earlier run left behind, and
nothing ever removed these (nova-tools#1699). SUFFIX is still appended raw, so
the `-absent/cache` case can name a path inside a directory that does not
exist."
  (format nil "~Acache-~D~A" (namestring (test-run-root))
          (incf *cache-file-seq*) suffix))

(defun remove-cache-file (path)
  "Remove PATH and its writer's temporary PATH.new, ignoring absence."
  (ignore-errors (delete-file path))
  (ignore-errors (delete-file (concatenate 'string path ".new"))))

(defun cache-file-text (path)
  "The whole of PATH read back as a character string."
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (let ((out (make-string-output-stream)))
      (loop for ch = (read-char in nil nil)
            while ch
            do (write-char ch out))
      (get-output-stream-string out))))

(defun write-cache-lines (path lines)
  "Write LINES to PATH raw, one per line; the eval-syntax case needs a byte the
canonical printer would never make."
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create
                            :element-type 'character :external-format :utf-8)
    (dolist (line lines)
      (write-string line out)
      (write-char #\Newline out))))

(defun counting-resolver (scheme command &key (fact :holds)
                                          (stamp "2026-09-13T18:00:00Z") calls)
  "A resolver whose configured body establishes FACT at STAMP and counts its
runs in the one-element list CALLS when given. The shape is slice-13's
`holding-resolver' under this slice's own name, so both files load into one
image without redefining each other."
  (make-verification-resolver
   scheme command
   :function (lambda (pointer subject)
               (declare (ignore pointer subject))
               (when calls (incf (first calls)))
               (values fact stamp))))

(defparameter *round-trip-facts*
  '(("test:pkg \"one\"@sha-1" "subject \"one\"" "resolver \"alpha\" one"
     :holds "2026-09-13T18:00:00Z")
    ("test:pkg two@sha-2 \"x\"" "subject \"two\"" "resolver \"beta\" two"
     :absent "2026-09-13T19:00:00Z")
    ("commit:sha three \"y\"" "subject \"three\"" "resolver gamma \"three\""
     :holds "2026-09-13T20:00:00Z"))
  "Three facts whose every key field carries a space and a double quote.")

;;; ------------------------------------------------------------------
;;; the-cache-round-trips-through-its-file  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "the-cache-round-trips-through-its-file" "docs/SPEC-WORK.md:1294-1325"
    "expected=one-canonical-line-per-fact;all-five-fields-come-back-equal"
  (let ((path (cache-temp-path)))
    (unwind-protect
         (let ((cache (make-verification-cache)))
           (dolist (f *round-trip-facts*)
             (apply #'verification-cache-store cache f))
           (check-equal 3 (write-verification-cache cache path)
                        "the writer returns the count it wrote")
           (let ((back (read-verification-cache path)))
             (check-equal 3 (verification-cache-size back)
                          "three facts come back through the file")
             (dolist (f *round-trip-facts*)
               (destructuring-bind (pointer subject resolver fact stamp) f
                 (let ((got (verification-cache-lookup back pointer subject resolver)))
                   (ok got "the fact at ~S is present" pointer)
                   (check-equal pointer (verification-fact-pointer got)
                                "the pointer comes back equal")
                   (check-equal subject (verification-fact-subject got)
                                "the subject comes back equal")
                   (check-equal resolver (verification-fact-resolver got)
                                "the resolver comes back equal")
                   (check-equal fact (verification-fact-fact got)
                                "the fact comes back equal")
                   (check-equal stamp (verification-fact-stamp got)
                                "the stamp comes back equal"))))))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; a-fact-form-is-one-canonical-line  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "a-fact-form-is-one-canonical-line" "docs/SPEC-WORK.md:1294-1325"
    "expected=the-five-key-plist-and-its-one-canonical-line"
  (let* ((fact (make-verification-fact "p q\"r" "s t\"u" "v w\"x" :holds
                                       "2026-09-13T18:00:00Z"))
         (form (verification-fact-form fact))
         (line (canonical-string form)))
    (check-equal (list :pointer "p q\"r" :subject "s t\"u" :resolver "v w\"x"
                       :fact :holds :stamp "2026-09-13T18:00:00Z")
                 form
                 "the form is the five fields in canonical order")
    (ok (not (find #\Newline line)) "the line carries no newline: ~S" line)
    (check-equal form (read-restricted line)
                 "the canonical line reads back as the same plist")))

;;; ------------------------------------------------------------------
;;; a-missing-cache-file-is-an-empty-cache  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "a-missing-cache-file-is-an-empty-cache" "docs/SPEC-WORK.md:1294-1325"
    "expected=absent-path-reads-as-empty-and-signals-nothing"
  (let ((path (cache-temp-path)))
    (unwind-protect
         (let ((cache (read-verification-cache path)))
           (check-equal 0 (verification-cache-size cache)
                        "an absent path is an empty cache, not an error"))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; two-writes-of-one-cache-are-byte-identical  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "two-writes-of-one-cache-are-byte-identical" "docs/SPEC-WORK.md:1294-1325"
    "expected=sorted-lines;two-writes-equal;no-new-file-left-behind"
  (let ((a (cache-temp-path))
        (b (cache-temp-path))
        (cache (make-verification-cache)))
    (unwind-protect
         (progn
           (dolist (f *round-trip-facts*)
             (apply #'verification-cache-store cache f))
           (write-verification-cache cache a)
           (write-verification-cache cache b)
           (check-equal (cache-file-text a) (cache-file-text b)
                        "two writes of one cache are byte identical")
           (ok (null (probe-file (concatenate 'string a ".new")))
               "no .new remains beside the first path")
           (ok (null (probe-file (concatenate 'string b ".new")))
               "no .new remains beside the second path"))
      (remove-cache-file a)
      (remove-cache-file b))))

;;; ------------------------------------------------------------------
;;; a-corrupt-line-refuses-and-installs-nothing  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "a-corrupt-line-refuses-and-installs-nothing" "docs/SPEC-WORK.md:1294-1325"
    "expected=unsupported-input-names-path-and-line;no-partial-cache-returned"
  (let ((path (cache-temp-path))
        (result :unset))
    (unwind-protect
         (progn
           (let ((cache (make-verification-cache)))
             (dolist (f *round-trip-facts*)
               (apply #'verification-cache-store cache f))
             (write-verification-cache cache path))
           (with-open-file (out path :direction :output :if-exists :append
                                     :if-does-not-exist :error
                                     :element-type 'character
                                     :external-format :utf-8)
             (write-string "this is not a restricted form" out)
             (write-char #\Newline out))
           (handler-case
               (setf result (read-verification-cache path))
             (unsupported-input (c)
               (let ((what (princ-to-string c)))
                 (ok (search path what)
                     "the refusal names the path: ~A" what)
                 (ok (search "line 4" what)
                     "the refusal names the 1-based line: ~A" what))))
           (check-equal :unset result
                        "a corrupt file installs nothing and returns no cache"))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; the-cache-reader-refuses-evaluation-syntax  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "the-cache-reader-refuses-evaluation-syntax" "docs/SPEC-WORK.md:1294-1325"
    "expected=read-eval-nil;dispatch-macro-refused;never-the-pwned-error"
  (let ((path (cache-temp-path)))
    (unwind-protect
         (progn
           (write-cache-lines path (list "#.(error \"pwned\")"))
           (handler-case
               (progn (read-verification-cache path)
                      (ok nil "evaluation syntax was accepted"))
             (unsupported-input ()
               (ok t "the evaluation syntax is refused as unsupported-input"))))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; session-start-reads-the-cache-its-path-names  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "session-start-reads-the-cache-its-path-names" "docs/SPEC-WORK.md:1294-1325"
    "expected=session-names-the-cache-path;no-cache-means-no-verification"
  (let ((path (cache-temp-path)))
    (unwind-protect
         (progn
           (let ((cache (make-verification-cache)))
             (verification-cache-store cache "test:a@sha-1" "a" "cmd-a"
                                       :holds "2026-09-13T18:00:00Z")
             (verification-cache-store cache "test:b@sha-1" "b" "cmd-b"
                                       :absent "2026-09-13T18:00:00Z")
             (check-equal 2 (write-verification-cache cache path)
                          "two facts written"))
           (let ((sess (session-start :cache path)))
             (check-equal path (session-cache sess)
                          "the session names the cache path it was started with")
             (let ((vs (session-verification sess)))
               (ok vs "the session carries a verification session")
               (check-equal 2 (verification-cache-size (verification-session-cache vs))
                            "the session's cache holds the two facts")))
           (let ((sess (session-start)))
             (ok (null (session-verification sess))
                 "no :cache means no verification session")
             (check-string= "" (session-cache sess)
                             "no :cache means no cache path is named")))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; a-session-fetch-survives-stop-and-reopen  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "a-session-fetch-survives-stop-and-reopen" "docs/SPEC-WORK.md:1294-1325"
    "expected=fetch-once;persist-without-the-write-helper;reopen-hits;no-second-resolver-call"
  (let ((path (cache-temp-path))
        (calls (list 0))
        (evidence (list (make-verify-evidence
                         "ev-1" :pointer "test:pkg/x@sha-1"
                         :criterion :test :subject "pkg/x" :against "sha-1"))))
    (unwind-protect
         (let (first-line first-rows second-line second-rows)
           (let* ((resolver (counting-resolver "test" "cmd-reopen" :calls calls))
                  (sess (session-start :cache path :resolvers (list resolver))))
             (multiple-value-bind (line rows exit) (verify (session-verification sess) evidence)
               (declare (ignore exit))
               (setf first-line line first-rows rows)))
           ;; There is no stop verb to call: dropping the first session and
           ;; starting a second on the same path IS the reopen.
           (let* ((resolver (counting-resolver "test" "cmd-reopen" :calls calls))
                  (sess (session-start :cache path :resolvers (list resolver))))
             (multiple-value-bind (line rows exit) (verify (session-verification sess) evidence)
               (declare (ignore exit))
               (setf second-line line second-rows rows)))
           (check-equal 1 (first calls)
                        "the resolver ran once across the reopen")
           (verify-shows first-line "fetched" 1)
           (ok (probe-file path) "the fetched fact is on disk")
           (check-equal first-rows second-rows
                        "the reopened run prints the same rows")
           (verify-shows second-line "fetched" 0)
           (verify-shows second-line "cached" 1))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; the-session-cache-names-resolvers-by-command-string  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "the-session-cache-names-resolvers-by-command-string" "docs/SPEC-WORK.md:1294-1325"
    "expected=resolvers-are-command-strings;reopened-fact-hits;no-resolver-call"
  (let ((path (cache-temp-path))
        (calls (list 0))
        (evidence (list (make-verify-evidence
                         "ev-1" :pointer "test:a@sha-1"
                         :criterion :test :subject "a" :against "sha-1"))))
    (unwind-protect
         (progn
           ;; A fact whose :resolver is the resolver's command string, verbatim.
           (let ((cache (make-verification-cache)))
             (verification-cache-store cache "test:a@sha-1" "a" "cmd-ident"
                                       :holds "2026-09-13T18:00:00Z")
             (write-verification-cache cache path))
           (let* ((resolver (counting-resolver "test" "cmd-ident" :calls calls))
                  (sess (session-start :cache path :resolvers (list resolver)))
                  (vs (session-verification sess)))
             (check-equal (list "cmd-ident")
                          (verification-cache-resolvers (verification-session-cache vs))
                          "the cache's resolver set holds command strings, not objects")
             (ok (null (verification-cache-lookup
                        (read-verification-cache path :resolvers (list resolver))
                        "test:a@sha-1" "a" "cmd-ident"))
                 "the same read given resolver objects would not hit")
             (multiple-value-bind (line rows exit) (verify vs evidence)
               (declare (ignore rows exit))
               (verify-shows line "fetched" 0)
               (verify-shows line "cached" 1)
               (check-equal 0 (first calls)
                            "the resolver was never called"))))
      (remove-cache-file path))))

;;; ------------------------------------------------------------------
;;; a-cache-that-cannot-be-written-is-reported  SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "a-cache-that-cannot-be-written-is-reported" "docs/SPEC-WORK.md:1294-1325"
    "expected=unsupported-input-names-the-path;the-failure-is-not-swallowed"
  (let* ((path (cache-temp-path "-absent/cache"))
         (calls (list 0))
         (evidence (list (make-verify-evidence
                          "ev-1" :pointer "test:pkg/y@sha-1"
                          :criterion :test :subject "pkg/y" :against "sha-1")))
         (reported nil)
         (returned nil))
    (unwind-protect
         (handler-case
             (multiple-value-bind (line rows exit)
                 (verify (session-verification
                          (session-start :cache path
                                         :resolvers (list (counting-resolver
                                                           "test" "cmd-write"
                                                           :calls calls))))
                         evidence)
               (declare (ignore rows exit))
               (setf returned line))
           (unsupported-input (c)
             (setf reported (princ-to-string c))))
      (remove-cache-file path))
    (ok reported "the failed write signals unsupported-input")
    (ok (and reported (search path reported))
        "the refusal names the cache path: ~S" reported)
    (ok (null returned) "verify returned no count line: ~S" returned)))

;;; ------------------------------------------------------------------
;;; the-cache-file-after-a-session-fetch-holds-the-fetched-fact
;;; SPEC-WORK.md:1294-1325
;;; ------------------------------------------------------------------

(deftest "the-cache-file-after-a-session-fetch-holds-the-fetched-fact" "docs/SPEC-WORK.md:1294-1325"
    "expected=one-line;one-fact;every-field-equal-to-the-resolver-answer"
  (let ((path (cache-temp-path))
        (evidence (list (make-verify-evidence
                         "ev-1" :pointer "test:pkg/z@sha-1"
                         :criterion :test :subject "pkg/z" :against "sha-1"))))
    (unwind-protect
         (progn
           (let* ((resolver (counting-resolver "test" "cmd-hold"
                                               :fact :holds
                                               :stamp "2026-09-13T18:00:00Z"))
                  (sess (session-start :cache path :resolvers (list resolver))))
             (verify (session-verification sess) evidence))
           (let ((text (cache-file-text path)))
             (ok (plusp (length text)) "the cache file is not empty")
             (check-equal 1 (count #\Newline text)
                          "the cache file is exactly one line"))
           (let ((back (read-verification-cache path)))
             (check-equal 1 (verification-cache-size back)
                          "the file holds exactly one fact")
             (let ((got (verification-cache-lookup back "test:pkg/z@sha-1" "pkg/z"
                                                    "cmd-hold")))
               (ok got "the fetched fact comes back")
               (check-equal "test:pkg/z@sha-1" (verification-fact-pointer got)
                            "the pointer is the one fetched")
               (check-equal "pkg/z" (verification-fact-subject got)
                            "the subject is the one passed")
               (check-equal "cmd-hold" (verification-fact-resolver got)
                            "the resolver identity is the command string")
               (check-equal :holds (verification-fact-fact got)
                            "the raw fact is the one the resolver answered")
               (check-equal "2026-09-13T18:00:00Z" (verification-fact-stamp got)
                            "the stamp is the one the resolver answered"))))
      (remove-cache-file path))))
