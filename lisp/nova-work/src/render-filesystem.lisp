;;;; render-filesystem.lisp --- `render --file` and `render --check` against
;;;; real repository files (SPEC-WORK.md:3147-3157).
;;;;
;;;; src/render.lisp holds the render targets as a pure model: its session's
;;;; store is an alist of path -> bytes, so `render --file` rewrote a string
;;;; handed to it and no repository file was ever read or written. The paragraph
;;;; is about a file: it "captures the file's SHA-256 and marker offsets, and
;;;; replaces its region atomically after a hash recheck, `--check` writing
;;;; nothing" (:3148-3150), and a symlink escape "refuses and leaves the file
;;;; untouched" (:3151-3154).
;;;;
;;;; This file is the other implementation of the seam src/render.lisp already
;;;; declared: FILESYSTEM-RENDER-SESSION includes RENDER-SESSION, so the whole
;;;; of RENDER-FILE -- the mapping, the stored permission, the repository
;;;; identity, the marker offsets, the hash recheck, the refusal lines and the
;;;; receipt -- is the same code over real bytes. Three methods change:
;;;;
;;;;   RENDER-TARGET-READ    reads the file, or answers NIL when it is missing
;;;;   RENDER-TARGET-WRITE   replaces it atomically: a sibling temporary file,
;;;;                         fsynced, renamed over the target, then the
;;;;                         directory fsynced, so a crash leaves either the old
;;;;                         file or the new one and never a half-written one
;;;;   RENDER-TARGET-ESCAPE-P  resolves the effective root and the target
;;;;                         through the OS instead of through a handed-in
;;;;                         alist of pretend links
;;;;
;;;; The renderer lock stays advisory: an external editor can still race the
;;;; final replace and this file claims no more than that (:3156-3157).

(in-package #:nova-work)

(defstruct (filesystem-render-session
            (:include render-session)
            (:constructor make-filesystem-render-session
                (&key permissions mappings files)))
  ;; No slot of its own: the permitted roots, their mappings and the stored
  ;; permissions are the same records the pure session carries. Only where the
  ;; bytes live differs, and that is the seam.
  )

;;; ------------------------------------------------------------------
;;; Resolving a path through the OS (:3151-3154)
;;; ------------------------------------------------------------------

(defun truename-string (path)
  "PATH resolved through the OS with no trailing separator, or NIL when no such
file or directory exists."
  (let ((resolved (ignore-errors (truename (pathname path)))))
    (when resolved
      (let ((text (namestring resolved)))
        (if (and (> (length text) 1)
                 (char= (char text (1- (length text))) #\/))
            (subseq text 0 (1- (length text)))
            text)))))

(defun resolve-through-links (path)
  "PATH with its longest existing prefix resolved through the OS and the rest
appended unchanged, or NIL when not even the first segment exists. A target that
does not exist yet is still resolved through the directories that do, so a link
in the middle of the path cannot hide behind a missing leaf."
  (let ((head path)
        (tail '()))
    (loop
      (let ((resolved (truename-string head)))
        (when resolved
          (return (if tail
                      (format nil "~A/~{~A~^/~}" resolved (reverse tail))
                      resolved))))
      (let ((slash (position #\/ head :from-end t)))
        (when (or (null slash) (zerop slash))
          (return nil))
        (push (subseq head (1+ slash)) tail)
        (setf head (subseq head 0 slash))))))

(defun filesystem-target-escapes-root-p (target root)
  "True when TARGET, resolved through the OS, is not inside ROOT resolved the
same way. This is the real form of the escape the pure model takes as an alist:
the root itself is resolved too, because a bench directory is often reached
through a link of its own and resolving only one side makes every path an
escape."
  (let ((real-root (resolve-through-links root))
        (real-target (resolve-through-links target)))
    (cond
      ((null real-root) t)
      ((null real-target) t)
      (t (not (path-within-p real-target real-root))))))

(defmethod render-target-escape-p ((session filesystem-render-session) target root symlinks)
  ;; The handed-in alist is the pure model's stand-in for the filesystem; here
  ;; the filesystem answers for itself.
  (declare (ignore symlinks))
  (filesystem-target-escapes-root-p target root))

;;; ------------------------------------------------------------------
;;; Reading and atomically replacing the file (:3148-3150)
;;; ------------------------------------------------------------------

(defmethod render-target-read ((session filesystem-render-session) target)
  (when (probe-file target)
    (with-open-file (in target :direction :input :element-type 'character
                               :external-format :utf-8)
      (let ((text (make-string (file-length in))))
        ;; FILE-LENGTH counts octets; a multi-byte target decodes to fewer
        ;; characters, so the fill pointer READ-SEQUENCE answers is the length.
        (subseq text 0 (read-sequence text in))))))

(defun render-temporary-target (target)
  "The sibling temporary the atomic replace writes first. It is a sibling so the
rename never crosses a filesystem."
  (concatenate 'string target ".nova-render.tmp"))

(defmethod render-target-write ((session filesystem-render-session) target content)
  "Replace TARGET with CONTENT atomically: write a sibling temporary, fsync it,
rename it over the target, then fsync the directory so the new name is durable.
A crash leaves the old file or the new one, never a half-written one
(SPEC-WORK.md:3148-3150)."
  (let* ((temporary (render-temporary-target target))
         (directory (let ((slash (position #\/ target :from-end t)))
                      (if slash (subseq target 0 slash) "."))))
    (handler-case
        (progn
          (with-open-file (out temporary :direction :output
                                         :if-exists :supersede
                                         :if-does-not-exist :create
                                         :element-type 'character
                                         :external-format :utf-8)
            (write-string content out)
            (sync-stream out :path temporary))
          #+sbcl (sb-posix:rename temporary target)
          #-sbcl (rename-file temporary target)
          (sync-directory directory))
      (error (c)
        ;; A failed replace leaves the target as it was and takes the
        ;; temporary with it.
        (ignore-errors (delete-file temporary))
        (error c)))
    content))
