;;;; slice-18-dedup-root.lisp --- the dedup root a savepoint's boundary record
;;;; names (docs/SPEC-WORK.md:7155-7164, the replay at :6740-6744).
;;;;
;;;; SPEC-WORK.md:7160-7161 conditions the retirement of a retained reply on
;;;; the committed snapshot, its retained events and "the dedup root that
;;;; boundary record names" being verified reachable. This file is the verb-side
;;;; precondition only: a REAL dedup-root object with a digest, written beside
;;;; the savepoint's other objects, and a boundary record carried inside the
;;;; manifest that names it. Retirement itself is a later row.
;;;;
;;;; `slice-16-savepoint-create.lisp` asserts the manifest's boundary is a
;;;; record, not the cut reprinted (SPEC-WORK.md:7156 forbids the latter).

(in-package #:nova-work/tests)

(defvar *dedup-root-test-counter* 0)

(defvar *dedup-root-evaluated* nil
  "Set by the payload of the reader-refusal case if evaluation ever runs.")

(defun dedup-root-temp-dir ()
  "A fresh 0700 directory under TMPDIR, the one place this file writes."
  (let* ((tmp (sb-posix:getenv "TMPDIR"))
         (base (if (and tmp (plusp (length tmp)))
                   (concatenate 'string tmp
                                (if (char= #\/ (char tmp (1- (length tmp)))) "" "/"))
                   (namestring (uiop:default-temporary-directory)))))
    (let ((dir (merge-pathnames
                (format nil "nova-work-test-dedup-root/~D-~D/"
                        (get-universal-time) (incf *dedup-root-test-counter*))
                (pathname base))))
      (ensure-directories-exist dir)
      #+sbcl (ignore-errors (sb-posix:chmod (namestring dir) #o700))
      dir)))

(defun dedup-root-delete-tree (dir)
  (ignore-errors (uiop:delete-directory-tree dir :validate t
                                                 :if-does-not-exist :ignore)))

(defun sample-dedup-entries ()
  "Two accepted records of the shape SPEC-WORK.md:7155-7164 names."
  (list (list :request "d-1" :payload-sha256 (sha256-hex "payload one")
              :sequence 1 :record-sha256 (sha256-hex "record one"))
        (list :request "d-2" :payload-sha256 (sha256-hex "payload two")
              :sequence 2 :record-sha256 (sha256-hex "record two"))))

(defun flip-one-ascii-byte (path)
  "Change exactly one byte of PATH, keeping it valid UTF-8 text."
  (let ((text (savepoint-file-text path)))
    (let ((i (floor (length text) 2)))
      (setf (char text i) (if (char= (char text i) #\a) #\b #\a)))
    (with-open-file (out path :direction :output :element-type 'character
                              :external-format :utf-8 :if-exists :supersede)
      (write-string text out)))
  path)

;;; ------------------------------------------------------------------
;;; dedup-root-round-trips-and-its-digest-is-stable  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "dedup-root-round-trips-and-its-digest-is-stable" "docs/SPEC-WORK.md:7155-7164"
    "expected=entries-read-back-equal;two-writes-are-byte-identical-with-one-digest"
  (let* ((dir (dedup-root-temp-dir))
         (path (merge-pathnames "dedup-root" dir))
         (path2 (merge-pathnames "dedup-root-again" dir))
         (entries (sample-dedup-entries)))
    (unwind-protect
         (progn
           (multiple-value-bind (digest size) (write-dedup-root path entries)
             (multiple-value-bind (read-back digest-back) (read-dedup-root path)
               (check-equal entries read-back "the root did not round-trip")
               (check-equal digest digest-back "the digest read back differs")
               (check-equal size (length (savepoint-file-text path))
                            "the size is not the bytes on disk")))
           (let ((digest-a (write-dedup-root path2 entries))
                 (digest-b (write-dedup-root path entries)))
             (check-equal digest-a digest-b "two writes of one root differ in digest")
             (check-equal (savepoint-file-text path) (savepoint-file-text path2)
                          "two writes of one root differ in bytes")))
      (dedup-root-delete-tree dir))))

;;; ------------------------------------------------------------------
;;; the-manifest-boundary-record-names-the-dedup-root  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "the-manifest-boundary-record-names-the-dedup-root" "docs/SPEC-WORK.md:7155-7164"
    "expected=the-boundary-record-carries-sequence-sha256-and-the-digest-of-the-object-on-disk"
  (let* ((fx (savepoint-fixture :id "sp-db"))
         (root (getf fx :root))
         (journal (getf fx :journal)))
    (unwind-protect
         (let* ((manifest (read-savepoint-manifest root "sp-db"))
                (boundary (getf manifest :boundary))
                (dedup-root-path (merge-pathnames
                                  "dedup-root" (savepoint-directory root "sp-db"))))
           ;; The object is real and its digest comes off the disk.
           (multiple-value-bind (entries digest) (read-dedup-root dedup-root-path)
             (declare (ignore entries))
             (ok (getf boundary :sequence) "the boundary names no sequence")
             (ok (getf boundary :sha256) "the boundary names no record hash")
             (check-equal digest (getf boundary :dedup-root)
                          "the boundary's dedup-root is not the object on disk")))
      (close-file-journal journal))))

;;; ------------------------------------------------------------------
;;; a-dedup-root-altered-by-one-byte-refuses  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "a-dedup-root-altered-by-one-byte-refuses" "docs/SPEC-WORK.md:7155-7164"
    "expected=a-root-whose-bytes-changed-is-refused-by-verify-naming-the-path"
  (let* ((fx (savepoint-fixture :id "sp-da"))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (let ((dedup-root-path (merge-pathnames
                                 "dedup-root" (savepoint-directory root "sp-da"))))
           (flip-one-ascii-byte dedup-root-path)
           (multiple-value-bind (okp line code)
               (savepoint-verify-published root "sp-da" :journal-path journal-path)
             (check-equal nil okp "an altered dedup root verified")
             (check-equal 1 code "the refusal exits 1")
             (ok (search "dedup-root" line)
                 "the refusal does not name the path: ~A" line)))
      (close-file-journal journal))))

;;; ------------------------------------------------------------------
;;; a-deleted-dedup-root-refuses  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "a-deleted-dedup-root-refuses" "docs/SPEC-WORK.md:7155-7164"
    "expected=a-missing-root-is-refused-by-verify-naming-the-path-never-read-as-empty"
  (let* ((fx (savepoint-fixture :id "sp-dd"))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (let ((dedup-root-path (merge-pathnames
                                 "dedup-root" (savepoint-directory root "sp-dd"))))
           (ok (probe-file dedup-root-path) "the dedup root was not written")
           (delete-file dedup-root-path)
           (multiple-value-bind (okp line code)
               (savepoint-verify-published root "sp-dd" :journal-path journal-path)
             (check-equal nil okp "a savepoint missing its dedup root verified")
             (check-equal 1 code "the refusal exits 1")
             (ok (search "dedup-root" line)
                 "the refusal does not name the path: ~A" line)))
      (close-file-journal journal))))

;;; ------------------------------------------------------------------
;;; the-dedup-root-reader-refuses-evaluation-syntax  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "the-dedup-root-reader-refuses-evaluation-syntax" "docs/SPEC-WORK.md:7155-7164"
    "expected=a-line-holding-a-dispatch-macro-signals-and-the-evaluation-never-runs"
  (let ((dir (dedup-root-temp-dir)))
    (unwind-protect
         (let ((path (merge-pathnames "dedup-root" dir)))
           (with-open-file (out path :direction :output :element-type 'character
                                     :external-format :utf-8 :if-exists :supersede)
             (write-string "#.(setf *dedup-root-evaluated* t)" out)
             (write-char #\Newline out))
           (setf *dedup-root-evaluated* nil)
           (let ((refused nil))
             (handler-case (read-dedup-root path)
               (restricted-data-violation () (setf refused t)))
             (ok refused "evaluation syntax was not refused"))
           (check-equal nil *dedup-root-evaluated* "the evaluation ran"))
      (dedup-root-delete-tree dir))))

;;; ------------------------------------------------------------------
;;; the-root-digest-survives-close-reopen-and-replay  SPEC-WORK.md:7155-7164
;;; ------------------------------------------------------------------

(deftest "the-root-digest-survives-close-reopen-and-replay" "docs/SPEC-WORK.md:7155-7164"
    "expected=the-digest-is-read-from-the-file-alone-across-close-reopen-and-replay"
  (let* ((fx (savepoint-fixture :id "sp-dr"))
         (root (getf fx :root))
         (journal-path (getf fx :journal-path))
         (init-digest (root-digest (make-seed-state *seed*)))
         (dedup-root-path (merge-pathnames
                           "dedup-root" (savepoint-directory root "sp-dr")))
         (manifest (read-savepoint-manifest root "sp-dr"))
         (named (let ((boundary (getf manifest :boundary)))
                  (and (consp boundary) (consp (cdr boundary))
                       (getf boundary :dedup-root)))))
    (close-file-journal (getf fx :journal))
    (let ((journal (open-file-journal journal-path :initial-state-hash init-digest)))
      (unwind-protect
           (progn
             (multiple-value-bind (entries digest) (read-dedup-root dedup-root-path)
               (declare (ignore entries))
               (check-equal named digest "the root's digest changed across close"))
             (let ((k (make-kernel :state (make-seed-state *seed*) :journal journal)))
               (replay-journal journal k)
               (multiple-value-bind (okp line)
                   (savepoint-verify-published root "sp-dr" :journal-path journal-path)
                 (ok okp "the savepoint stopped verifying after reopen: ~A" line))
               (multiple-value-bind (entries digest) (read-dedup-root dedup-root-path)
                 (declare (ignore entries))
                 (check-equal named digest "the root's digest changed across replay"))))
        (close-file-journal journal)))))
