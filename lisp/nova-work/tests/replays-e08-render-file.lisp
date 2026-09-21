;;;; replays-e08-render-file.lisp --- `render --file` and `render --check` over
;;;; a real repository file (AUDIT rows E08.2 and E08.3).
;;;;
;;;;   render-file-replaces-the-region-in-a-repository-file  SPEC-WORK.md:3147-3150
;;;;   render-check-writes-nothing                           SPEC-WORK.md:3147-3150
;;;;   render-file-refuses-and-leaves-the-file-untouched     SPEC-WORK.md:3151-3154
;;;;   render-file-refuses-a-symlink-escape-on-disk          SPEC-WORK.md:3151-3154
;;;;   chat-and-file-render-are-byte-identical-on-disk             SPEC-WORK.md:3138
;;;;
;;;; The pure model of src/render.lisp rewrites a CONTENT string handed to it.
;;;; These cases hand it nothing: a directory is made, a file is written into
;;;; it, and every assertion reads the bytes back off the disk with an
;;;; independent WITH-OPEN-FILE, because "replaces its region atomically after a
;;;; hash recheck" and "`--check` writing nothing" are claims about a file.

(in-package #:nova-work/tests)

(defvar *render-test-counter* 0)

(defun test-render-root (name)
  "A fresh empty directory to serve as one permitted root's bench directory."
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames (format nil "nova-work-test-renders/~A-~D-~D/"
                                       name (get-universal-time)
                                       (incf *render-test-counter*))
                               base)))
    (ensure-directories-exist dir)
    ;; The canonical spelling: on Darwin the temporary directory is itself
    ;; reached through a symlink, and a root that is not resolved would make
    ;; every containment check a false escape.
    (string-right-trim "/" (namestring (truename dir)))))

(defun write-test-file (path text)
  (ensure-directories-exist path)
  (with-open-file (out path :direction :output :if-exists :supersede
                            :if-does-not-exist :create
                            :element-type 'character :external-format :utf-8)
    (write-string text out))
  path)

(defun read-test-file (path)
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (let ((text (make-string (file-length in))))
      (subseq text 0 (read-sequence text in)))))

(defun directory-entry-names (dir)
  (sort (mapcar #'file-namestring
                (directory (merge-pathnames "*.*" (concatenate 'string dir "/"))))
        #'string<))

(defun render-to-file (session projection &rest args)
  "`render --file`: RENDER-FILE with the writing mode, which the verb has no
default for. RENDER-CHECK supplies its own :check mode and is called directly."
  (apply #'render-file session projection :mode :file args))

(defparameter *render-marker-start* "<!--nova:rows-->")
(defparameter *render-marker-end* "<!--/nova:rows-->")

(defun test-render-body-file (root &key (rel "docs/board.md")
                                        (before (format nil "# Board~%~%"))
                                        (region (format nil "~%old rows~%"))
                                        (after (format nil "~%tail~%")))
  "Write the target file under ROOT and answer (values absolute-path text)."
  (let* ((path (format nil "~A/~A" root rel))
         (text (concatenate 'string before *render-marker-start* region
                            *render-marker-end* after)))
    (write-test-file path text)
    (values path text)))

(defun test-render-session (root &key (repo "acme/work") (modes '(:file :chat)))
  (make-filesystem-render-session
   :permissions (list (cons "r1" modes))
   :mappings (list (cons "r1" (list :repo repo :directory root)))))

(defun test-render-projection (&key (repo "acme/work") (rel "docs/board.md")
                                    (start *render-marker-start*)
                                    (end *render-marker-end*))
  (make-render-projection :id "p1" :repo repo :root "r1" :path rel
                          :start start :end end))

;;; ------------------------------------------------------------------
;;; render-file-replaces-the-region-in-a-repository-file   :3144-3147
;;; ------------------------------------------------------------------

(deftest "render-file-replaces-the-region-in-a-repository-file" "docs/SPEC-WORK.md:3147-3150"
    "expected=region-replaced-on-disk;every-other-byte-preserved;receipt-carries-old-and-new-hash;no-temp-file-left"
  (let* ((root (test-render-root "replace")))
    (multiple-value-bind (path text) (test-render-body-file root)
      (let* ((session (test-render-session root))
             (projection (test-render-projection))
             (result (render-to-file session projection
                                          :body (format nil "~%new rows~%")
                                          :revision 12)))
        (ok (render-result-ok result) "the render succeeds: ~A" (render-result-reason result))
        (ok (render-result-wrote result) "--file wrote the target")
        (let ((on-disk (read-test-file path)))
          (ok (search "new rows" on-disk) "the new body is in the file on disk")
          (ok (null (search "old rows" on-disk)) "the old region is gone from the file on disk")
          (ok (search "# Board" on-disk) "the bytes before the start marker are preserved")
          (ok (search "tail" on-disk) "the bytes after the end marker are preserved")
          (ok (search *render-marker-start* on-disk) "the start marker is preserved")
          (ok (search *render-marker-end* on-disk) "the end marker is preserved")
          (let ((receipt (render-result-receipt result)))
            (check-string= (sha256-hex text) (getf receipt :old)
                           "the receipt's old hash is the file's hash before the render")
            (check-string= (sha256-hex on-disk) (getf receipt :new)
                           "the receipt's new hash is the file's hash on disk after the render")
            (check-equal 12 (getf receipt :revision) "the receipt carries the render revision")
            (check-string= path (getf receipt :target) "the receipt names the target identity")))
        (check-equal '("board.md") (directory-entry-names (format nil "~A/docs" root))
                     "the atomic replace leaves no temporary file behind")))))

;;; ------------------------------------------------------------------
;;; render-check-writes-nothing                            :3144-3147
;;; ------------------------------------------------------------------

(deftest "render-check-writes-nothing" "docs/SPEC-WORK.md:3147-3150"
    "expected=check-verifies-and-writes-nothing;file-byte-identical;receipt-still-carries-both-hashes"
  (let* ((root (test-render-root "check")))
    (multiple-value-bind (path text) (test-render-body-file root)
      (let* ((session (test-render-session root))
             (projection (test-render-projection))
             (result (render-check session projection
                                           :body (format nil "~%new rows~%")
                                           :revision 13)))
        (ok (render-result-ok result) "the check succeeds: ~A" (render-result-reason result))
        (check-equal nil (render-result-wrote result) "--check wrote nothing")
        (check-string= text (read-test-file path) "the file on disk is byte-identical")
        (let ((receipt (render-result-receipt result)))
          (check-string= (sha256-hex text) (getf receipt :old)
                         "the check's old hash is the file's hash")
          (ok (not (equal (getf receipt :old) (getf receipt :new)))
              "the check still reports the hash the render would produce"))
        (check-equal '("board.md") (directory-entry-names (format nil "~A/docs" root))
                     "--check leaves no temporary file behind")))))

;;; ------------------------------------------------------------------
;;; render-file-refuses-and-leaves-the-file-untouched      :3147-3151
;;; ------------------------------------------------------------------

(deftest "render-file-refuses-and-leaves-the-file-untouched" "docs/SPEC-WORK.md:3151-3154"
    "expected=hash-moved;marker-missing;target-identity;path-outside-root;file-untouched-in-every-case"
  (let ((root (test-render-root "refuse")))
    (multiple-value-bind (path text) (test-render-body-file root)
      (flet ((untouched (what)
               (check-string= text (read-test-file path)
                              (format nil "~A leaves the file untouched" what))))
        ;; a target whose hash moved
        (let ((r (render-to-file (test-render-session root) (test-render-projection)
                                      :body "x" :expected-hash "not-the-hash-on-disk")))
          (check-equal nil (render-result-ok r) "a moved hash refuses")
          (check-string= "a hash moved" (render-result-reason r) "the refusal names the moved hash")
          (untouched "a moved hash"))
        ;; a missing marker
        (let ((r (render-to-file (test-render-session root)
                                      (test-render-projection :end "<!--/nova:absent-->")
                                      :body "x")))
          (check-equal nil (render-result-ok r) "a missing marker refuses")
          (check-string= "a marker missing" (render-result-reason r) "the refusal names the marker")
          (untouched "a missing marker"))
        ;; a mapping whose repository identity is not the projection's stored :repo
        (let ((r (render-to-file (test-render-session root :repo "other/repo")
                                      (test-render-projection) :body "x")))
          (check-equal nil (render-result-ok r) "a remapped root refuses")
          (check-string= "target identity" (render-result-reason r)
                         "remapping a root never redirects a projection to another repository")
          (untouched "a remapped root"))
        ;; a path outside the effective root
        (let ((r (render-to-file (test-render-session root)
                                      (test-render-projection :rel "../escape.md")
                                      :body "x")))
          (check-equal nil (render-result-ok r) "a path outside the root refuses")
          (check-string= "a path outside its root" (render-result-reason r)
                         "the refusal names the root")
          (untouched "a path outside the root"))
        ;; a stored root id grants no access: the mapping without :file permission
        (let ((r (render-to-file (test-render-session root :modes '(:chat))
                                      (test-render-projection) :body "x")))
          (check-equal nil (render-result-ok r) "file mode needs the stored permission")
          (untouched "a root id without the file permission"))))))

;;; ------------------------------------------------------------------
;;; render-file-refuses-a-symlink-escape-on-disk           :3147-3149
;;; ------------------------------------------------------------------

(deftest "render-file-refuses-a-symlink-escape-on-disk" "docs/SPEC-WORK.md:3151-3154"
    "expected=real-symlink-out-of-the-root-refuses;the-file-outside-the-root-is-untouched"
  (let* ((root (test-render-root "escape-root"))
         (outside (test-render-root "escape-outside"))
         (victim (format nil "~A/target.md" outside))
         (text (concatenate 'string *render-marker-start* (format nil "~%old~%")
                            *render-marker-end*)))
    (write-test-file victim text)
    ;; A real symlink inside the root pointing out of it.
    (sb-posix:symlink outside (format nil "~A/link" root))
    (let ((r (render-to-file (test-render-session root)
                                  (test-render-projection :rel "link/target.md")
                                  :body (format nil "~%new~%"))))
      (check-equal nil (render-result-ok r) "a symlink escape refuses")
      (check-string= "symlink escape" (render-result-reason r) "the refusal names the escape")
      (check-string= text (read-test-file victim)
                     "the file outside the root is untouched"))))

;;; ------------------------------------------------------------------
;;; chat-and-file-render-are-byte-identical                :3138
;;; ------------------------------------------------------------------

(deftest "chat-and-file-render-are-byte-identical-on-disk" "docs/SPEC-WORK.md:3138"
    "expected=the-chat-artifact-body-and-the-region-written-to-disk-are-the-same-bytes"
  (let* ((root (test-render-root "identical"))
         (body (format nil "~%| a | b |~%| 1 | 2 |~%")))
    (test-render-body-file root)
    (let* ((chat (render-chat body))
           (file (render-to-file (test-render-session root)
                                      (test-render-projection) :body body))
           (on-disk (read-test-file (format nil "~A/docs/board.md" root))))
      (ok (render-result-ok chat) "the chat render succeeds")
      (ok (render-result-ok file) "the file render succeeds")
      (let* ((artifact (render-result-artifact chat))
             (start (+ (search *render-marker-start* on-disk)
                       (length *render-marker-start*)))
             (end (search *render-marker-end* on-disk)))
        (check-string= (getf artifact :body) (subseq on-disk start end)
                       "the chat artifact's body and the region on disk are the same bytes")
        (check-string= (sha256-hex (subseq on-disk start end)) (getf artifact :sha256)
                       "and they hash the same")))))
