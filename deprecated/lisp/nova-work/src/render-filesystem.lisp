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

;;; ==================================================================
;;; The storage split (nova-tools#3174 part (i); SPEC-WORK.md section 9 of
;;; "nova-work is the primary source; GitHub is an ingest", "The storage
;;; split").
;;; ==================================================================
;;;
;;; One logical forest, several files, all under one record root:
;;;
;;;   work.sexp                       the manifest: :repositories, one row per
;;;                                   declared repository, and the root :digest
;;;   work/<owner>/<name>.sexp        O: that repository's forest, its work set
;;;                                   first and every node beneath it in
;;;                                   preorder
;;;   work/<owner>/<name>.closed.sexp C: that repository's closed-index rows,
;;;                                   one per :settle and per :revive, oldest
;;;                                   first, append-only
;;;   blobs/<aa>/<sha256>             bodies, content-addressed, write-once
;;;
;;; THE PARTITION IS ONE DERIVED FACT ("The root is COW": "C and O hold the
;;; same nodes ... told apart by one derived fact"). A node record in O carries
;;; no branch. The branch of an id is read from its latest C row (a :settle puts
;;; it in C, a :revive back in O), so appending a :settle or :revive row changes
;;; the C file and leaves the O file byte-identical -- which is exactly why the
;;; manifest's digest rule folds the C file into the repository and root
;;; digests: a digest over O alone would not move on a settle.
;;;
;;; THE DIGEST RULE, byte for byte. :file-digest and :closed-digest are the
;;; lower-case hex SHA-256 of the file's bytes on disk (an empty C file is
;;; +EMPTY-SHA256+). The repository :digest is SHA-256 of the two hex strings
;;; concatenated, :file-digest first. The root :digest is SHA-256 of the
;;; repository digests concatenated in manifest order.
;;;
;;; The split is the forest's record, not its history: the journal keeps the
;;; envelopes, so a loaded state starts with an empty history, lease log and
;;; meta logs, and its seed is the loaded forest.

(defparameter +empty-sha256+
  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
  "SHA-256 of zero bytes: the :closed-digest of a repository with no C rows.")

(defparameter *split-manifest-schema* "nova-work-manifest-1")

(defparameter +split-default-priority+ (list :self +absent+ :subtree +absent+)
  "A node's priority before any :prioritise event; the O record omits it.")

(defun %split-refuse (fmt &rest args)
  (error 'unsupported-input :what (apply #'format nil fmt args)))

(defun %split-segment-ok-p (text)
  "One path segment of a repository name: ASCII letters, digits, '.', '_' and
'-', not empty and not starting with '.', so no name can climb out of work/."
  (and (stringp text)
       (plusp (length text))
       (char/= #\. (char text 0))
       (every (lambda (ch)
                (or (char<= #\a ch #\z) (char<= #\A ch #\Z) (char<= #\0 ch #\9)
                    (find ch "._-")))
              text)))

(defun split-repo-paths (repo)
  "The O and C paths of REPO (\"<owner>/<name>\"), relative to the record root.
A name that is not exactly two clean segments is refused."
  (let ((slash (and (stringp repo) (position #\/ repo))))
    (unless (and slash
                 (%split-segment-ok-p (subseq repo 0 slash))
                 (%split-segment-ok-p (subseq repo (1+ slash))))
      (%split-refuse "WORK REFUSED repository ~S: a repository is <owner>/<name>, each ASCII letters, digits, '.', '_' or '-'"
                     repo))
    (values (format nil "work/~A.sexp" repo)
            (format nil "work/~A.closed.sexp" repo))))

(defun %split-hex64-p (text)
  (and (stringp text) (= 64 (length text))
       (every (lambda (ch) (or (char<= #\0 ch #\9) (char<= #\a ch #\f))) text)))

(defun split-blob-path (sha)
  (format nil "blobs/~A/~A" (subseq sha 0 2) sha))

(defun split-repo-digest (file-digest closed-digest)
  (sha256-hex (concatenate 'string file-digest closed-digest)))

(defun split-root-digest (repo-digests)
  (sha256-hex (apply #'concatenate 'string repo-digests)))

(defun %split-join (root relative)
  (format nil "~A/~A" (string-right-trim "/" (namestring root)) relative))

(defun %split-read-octets (path)
  "The bytes of PATH, or NIL when there is no such file."
  (when (probe-file path)
    (with-open-file (in path :direction :input :element-type '(unsigned-byte 8))
      (let* ((bytes (make-array (file-length in) :element-type '(unsigned-byte 8)))
             (n (read-sequence bytes in)))
        (if (= n (length bytes)) bytes (subseq bytes 0 n))))))

(defun %split-octets-text (bytes path)
  (handler-case
      #+sbcl (sb-ext:octets-to-string bytes :external-format :utf-8)
      #-sbcl (error "no UTF-8 decoder outside SBCL")
    (error ()
      (%split-refuse "WORK FAIL ~A: not UTF-8" path))))

(defun %split-write-octets (path bytes)
  "Replace PATH with BYTES atomically: a sibling temporary, synced, renamed over
PATH, then the directory synced -- the same discipline as RENDER-TARGET-WRITE."
  (ensure-directories-exist path)
  (let ((temporary (concatenate 'string path ".nova-split.tmp"))
        (directory (subseq path 0 (position #\/ path :from-end t))))
    (handler-case
        (progn
          (with-open-file (out temporary :direction :output
                                         :if-exists :supersede
                                         :if-does-not-exist :create
                                         :element-type '(unsigned-byte 8))
            (write-sequence bytes out)
            (sync-stream out :path temporary))
          #+sbcl (sb-posix:rename temporary path)
          #-sbcl (rename-file temporary path)
          (sync-directory directory))
      (error (c)
        (ignore-errors (delete-file temporary))
        (error c)))
    bytes))

;;; ------------------------------------------------------------------
;;; Printing: one node record, one O file, one C file, the manifest
;;; ------------------------------------------------------------------

(defun split-node-record (node)
  "NODE as the O file holds it: a seed spec MAKE-SEED-STATE reads back to the
same node. Fixed key order; a field at its seed default is omitted, and the
node's preserved unknown keys follow in their own order. No branch, no
counters: those are derived on load."
  (let ((out (list :id (wnode-id node) :type (wnode-type node))))
    (flet ((put (key value) (setf out (append out (list key value)))))
      (when (wnode-parent node) (put :parent (wnode-parent node)))
      (when (wnode-coordinator node) (put :coordinator (wnode-coordinator node)))
      (unless (wnode-required node) (put :required nil))
      (put :state (wnode-state node))
      (when (wnode-deps node) (put :deps (wnode-deps node)))
      (dolist (field '(:links :title :category :private :version))
        (let ((value (wnode-field node field)))
          (unless (absentp value) (put field value))))
      (when (wnode-repo node) (put :repo (wnode-repo node)))
      (unless (absentp (wnode-estimate node)) (put :estimate (wnode-estimate node)))
      (when (wnode-holder node) (put :holder (wnode-holder node)))
      (unless (equal (wnode-priority node) +split-default-priority+)
        (put :priority (wnode-priority node)))
      (append out (copy-list (wnode-extra node))))))

(defun %split-print (form where)
  (handler-case (canonical-string form)
    (restricted-data-violation (c)
      (%split-refuse "WORK REFUSED ~A: not restricted data: ~A" where c))))

(defun split-o-text (repo records)
  (format nil "(:repo ~A~% :nodes~% (~{~A~^~%  ~}))~%"
          (canonical-string repo)
          (mapcar (lambda (r) (%split-print r (format nil "node ~A" (getf r :id))))
                  records)))

(defun split-c-text (rows)
  (format nil "~{~A~%~}"
          (mapcar (lambda (r) (%split-print r (format nil "row ~A" (getf r :key)))) rows)))

(defun split-manifest-text (revision rows root-digest)
  (format nil "(:schema ~A~% :revision ~D~% :repositories~% (~{~A~^~%  ~})~% :digest ~A)~%"
          (canonical-string *split-manifest-schema*)
          revision
          (mapcar #'canonical-string rows)
          (canonical-string root-digest)))

;;; ------------------------------------------------------------------
;;; Writing
;;; ------------------------------------------------------------------

(defun split-body-shas (state)
  "Every distinct :body-sha256 named by a node's :correspondence, first seen
first."
  (let ((seen (make-hash-table :test #'equal)) (out '()))
    (dolist (id (wstate-order state))
      (let ((corr (getf (wnode-extra (%node-quiet state id)) :correspondence)))
        (when (listp corr)
          (dolist (entry corr)
            (let ((sha (and (listp entry) (getf entry :body-sha256))))
              (when (and sha (not (absentp sha)))
                (unless (%split-hex64-p sha)
                  (%split-refuse "WORK REFUSED node ~A: :body-sha256 ~S is not 64 lower-case hex" id sha))
                (unless (gethash sha seen)
                  (setf (gethash sha seen) t)
                  (push sha out))))))))
    (nreverse out)))

(defun %split-forest (state repositories)
  "Each declared repository's records in preorder, and the id -> repository
table. Refuses a top-level node that is not a declared repository's work set,
two work sets for one repository, and any node outside every declared forest."
  (let ((roots (make-hash-table :test #'equal))
        (owner (make-hash-table :test #'equal))
        (declared (make-hash-table :test #'equal)))
    (dolist (repo repositories)
      (split-repo-paths repo)
      (when (gethash repo declared)
        (%split-refuse "WORK REFUSED repository ~A is declared twice" repo))
      (setf (gethash repo declared) t))
    (dolist (id (wstate-order state))
      (let ((node (%node-quiet state id)))
        (unless (wnode-parent node)
          (let ((repo (wnode-repo node)))
            (unless (and (eq :work-set (wnode-type node)) (stringp repo)
                         (gethash repo declared))
              (%split-refuse "WORK REFUSED node ~A: every top-level node is a declared repository's work set" id))
            (when (gethash repo roots)
              (%split-refuse "WORK REFUSED repository ~A has two work sets, ~A and ~A"
                             repo (gethash repo roots) id))
            (setf (gethash repo roots) id)))))
    (let ((forests
            (loop for repo in repositories
                  collect (let ((records '()) (stack (let ((r (gethash repo roots)))
                                                       (and r (list r)))))
                            (loop while stack
                                  do (let* ((id (pop stack))
                                            (node (%node-quiet state id)))
                                       (setf (gethash id owner) repo)
                                       (push (split-node-record node) records)
                                       (setf stack (append (wnode-children node) stack))))
                            (cons repo (nreverse records))))))
      (unless (= (hash-table-count owner) (length (wstate-order state)))
        (%split-refuse "WORK REFUSED ~D node(s) are in no declared repository's forest"
                       (- (length (wstate-order state)) (hash-table-count owner))))
      (values forests owner))))

(defun write-work-split (state root &key (repositories (%split-refuse "WORK REFUSED missing :repositories"))
                                         bodies)
  "Write STATE under the record ROOT as the storage split: blobs first, then
each declared repository's O and C files, then the manifest last, each file
replaced atomically. REPOSITORIES is the declared list, \"<owner>/<name>\", in
manifest order. BODIES maps a body SHA-256 to its text (a hash table); a blob
already on disk is never rewritten, only checked. Answers the receipt line and
the manifest's digests as a plist."
  (multiple-value-bind (forests owner) (%split-forest state repositories)
    (let ((rows-by-repo (make-hash-table :test #'equal))
          (blobs 0))
      (dolist (row (state-closed-rows state))
        (let ((repo (gethash (getf row :node) owner)))
          (unless repo
            (%split-refuse "WORK REFUSED closed row ~A names no node of a declared repository"
                           (getf row :key)))
          (push row (gethash repo rows-by-repo))))
      ;; Bodies, write-once and content-addressed.
      (dolist (sha (split-body-shas state))
        (let* ((path (%split-join root (split-blob-path sha)))
               (existing (%split-read-octets path)))
          (if existing
              (unless (string= sha (sha256-hex existing))
                (%split-refuse "WORK FAIL digest ~A: the blob on disk does not hash to its name" path))
              (let ((text (and bodies (gethash sha bodies))))
                (unless (stringp text)
                  (%split-refuse "WORK REFUSED body ~A: referenced by :body-sha256 and not supplied" sha))
                (let ((bytes (utf8-octets text)))
                  (unless (string= sha (sha256-hex bytes))
                    (%split-refuse "WORK REFUSED body ~A: the supplied text hashes to ~A" sha (sha256-hex bytes)))
                  (%split-write-octets path bytes)
                  (incf blobs))))))
      (let ((manifest-rows '()) (repo-digests '()) (nodes 0) (row-count 0))
        (dolist (forest forests)
          (destructuring-bind (repo . records) forest
            (multiple-value-bind (o-path c-path) (split-repo-paths repo)
              (let* ((rows (reverse (gethash repo rows-by-repo)))
                     (o-bytes (utf8-octets (split-o-text repo records)))
                     (c-bytes (utf8-octets (split-c-text rows)))
                     (file-digest (sha256-hex o-bytes))
                     (closed-digest (sha256-hex c-bytes))
                     (digest (split-repo-digest file-digest closed-digest)))
                (%split-write-octets (%split-join root o-path) o-bytes)
                (%split-write-octets (%split-join root c-path) c-bytes)
                (incf nodes (length records))
                (incf row-count (length rows))
                (push digest repo-digests)
                (push (list :repo repo :file o-path :file-digest file-digest
                            :closed c-path :closed-digest closed-digest :digest digest)
                      manifest-rows)))))
        (let* ((manifest-rows (nreverse manifest-rows))
               (root-digest (split-root-digest (nreverse repo-digests))))
          (%split-write-octets (%split-join root "work.sexp")
                               (utf8-octets (split-manifest-text (wstate-revision state)
                                                                 manifest-rows root-digest)))
          (list :line (format nil "WORK OK split repositories=~D nodes=~D rows=~D blobs-written=~D digest=~A"
                              (length manifest-rows) nodes row-count blobs root-digest)
                :digest root-digest
                :repositories manifest-rows))))))

;;; ------------------------------------------------------------------
;;; Loading
;;; ------------------------------------------------------------------

(defun %split-read-checked (root relative expected max-bytes max-depth max-nodes &key wrap)
  "Read ROOT/RELATIVE, check its bytes hash to EXPECTED (the lint of the digest
rule: WORK FAIL digest), and read it as restricted data. WRAP reads a file of
several forms (the C file) as one list."
  (let* ((path (%split-join root relative))
         (bytes (%split-read-octets path)))
    (unless bytes
      (%split-refuse "WORK FAIL missing ~A" relative))
    (let ((actual (sha256-hex bytes)))
      (unless (string= expected actual)
        (%split-refuse "WORK FAIL digest ~A: manifest ~A bytes ~A" relative expected actual)))
    (let ((text (%split-octets-text bytes relative)))
      (read-bounded (if wrap (format nil "(~%~A~%)" text) text)
                    :max-bytes (+ max-bytes (if wrap 4 0))
                    :max-depth (+ max-depth (if wrap 1 0))
                    :max-nodes (+ max-nodes (if wrap 1 0))))))

(defun load-work-split (root &key (max-bytes (missing-read-bound "max-bytes"))
                                  (max-depth (missing-read-bound "max-depth"))
                                  (max-nodes (missing-read-bound "max-nodes")))
  "Load the storage split under ROOT into a fresh state. Every file's bytes are
checked against the manifest's digests, and the repository and root digests
against the rule, before anything is read as a form; a mismatch refuses with
WORK FAIL digest. The O records seed the forest in manifest order, then the C
rows are applied oldest first by revision to derive each id's branch and the
counters the write path keeps. The three read bounds apply to every file."
  (let* ((manifest-bytes (or (%split-read-octets (%split-join root "work.sexp"))
                             (%split-refuse "WORK FAIL missing work.sexp")))
         (manifest (read-bounded (%split-octets-text manifest-bytes "work.sexp")
                                 :max-bytes max-bytes :max-depth max-depth :max-nodes max-nodes))
         (revision (getf manifest :revision))
         (rows (getf manifest :repositories))
         (specs '())
         (closed '())
         (priorities '()))
    (unless (equal *split-manifest-schema* (getf manifest :schema))
      (%split-refuse "WORK FAIL work.sexp: :schema is not ~S" *split-manifest-schema*))
    (unless (and (integerp revision) (>= revision 0))
      (%split-refuse "WORK FAIL work.sexp: :revision is not a non-negative integer"))
    (unless (listp rows)
      (%split-refuse "WORK FAIL work.sexp: :repositories is not a list"))
    (let ((repo-digests '()) (seen (make-hash-table :test #'equal)))
      (dolist (row rows)
        (let ((repo (getf row :repo)))
          (multiple-value-bind (o-path c-path) (split-repo-paths repo)
            (when (gethash repo seen)
              (%split-refuse "WORK FAIL work.sexp: repository ~A appears twice" repo))
            (setf (gethash repo seen) t)
            (unless (and (equal o-path (getf row :file)) (equal c-path (getf row :closed)))
              (%split-refuse "WORK FAIL work.sexp: repository ~A names files other than ~A and ~A"
                             repo o-path c-path))
            (dolist (key '(:file-digest :closed-digest :digest))
              (unless (%split-hex64-p (getf row key))
                (%split-refuse "WORK FAIL work.sexp: repository ~A ~(~S~) is not 64 lower-case hex" repo key)))
            (unless (string= (getf row :digest)
                             (split-repo-digest (getf row :file-digest) (getf row :closed-digest)))
              (%split-refuse "WORK FAIL digest ~A: :digest is not SHA-256 of :file-digest then :closed-digest" repo))
            (push (getf row :digest) repo-digests)
            (let ((o (%split-read-checked root o-path (getf row :file-digest)
                                          max-bytes max-depth max-nodes))
                  (c (%split-read-checked root c-path (getf row :closed-digest)
                                          max-bytes max-depth max-nodes :wrap t))
                  (ids (make-hash-table :test #'equal)))
              (unless (and (equal repo (getf o :repo)) (listp (getf o :nodes)))
                (%split-refuse "WORK FAIL ~A: not the O file of ~A" o-path repo))
              ;; Preorder, checked: the work set first, then every record's
              ;; parent already seen in this file, so no record can sit in
              ;; another repository's forest.
              (loop for record in (getf o :nodes)
                    for first = t then nil
                    do (let ((id (getf record :id)) (parent (getf record :parent)))
                         (unless (stringp id)
                           (%split-refuse "WORK FAIL ~A: a node record has no :id" o-path))
                         (if first
                             (unless (and (null parent) (eq :work-set (getf record :type))
                                          (equal repo (getf record :repo)))
                               (%split-refuse "WORK FAIL ~A: the first record is not ~A's work set" o-path repo))
                             (unless (and parent (gethash parent ids))
                               (%split-refuse "WORK FAIL ~A: node ~A is not beneath an earlier node of this file" o-path id)))
                         (setf (gethash id ids) t)
                         (push record specs)
                         (let ((priority (getf record :priority)))
                           (when priority (push (cons id priority) priorities)))))
              (dolist (crow c)
                (unless (and (gethash (getf crow :node) ids)
                             (member (getf crow :kind) '(:settle :revive))
                             (integerp (getf crow :rev)))
                  (%split-refuse "WORK FAIL ~A: row ~S is not a settle or revive of a node of ~A"
                                 c-path (getf crow :key) repo))
                (push crow closed))))))
      (unless (equal (getf manifest :digest) (split-root-digest (nreverse repo-digests)))
        (%split-refuse "WORK FAIL digest work.sexp: :digest is not SHA-256 of the repository digests")))
    (let ((state (make-seed-state (nreverse specs))))
      (loop for (id . priority) in priorities
            do (setf (wnode-priority (%node-quiet state id)) (copy-tree priority)))
      ;; C rows oldest first by revision; a stable sort keeps each file's own
      ;; order among rows of one revision.
      (dolist (crow (stable-sort (nreverse closed) #'< :key (lambda (r) (getf r :rev))))
        (let* ((id (getf crow :node))
               (node (%node-quiet state id)))
          (ecase (getf crow :kind)
            (:settle
             (unless (eq :o (wnode-branch node))
               (%split-refuse "WORK FAIL rule 18: ~A settles at ~A while already in C" id (getf crow :rev)))
             (setf (wnode-branch node) :c)
             (%adjust-counters state id -1))
            (:revive
             (unless (eq :c (wnode-branch node))
               (%split-refuse "WORK FAIL rule 18: ~A revives at ~A while not in C" id (getf crow :rev)))
             (setf (wnode-branch node) :o)
             (%adjust-counters state id 1)))
          (setf (wnode-settles node) (getf crow :settles (wnode-settles node))
                (wnode-revived node) (getf crow :revived (wnode-revived node)))
          (push crow (wstate-rows state))))
      (dolist (id (wstate-order state))
        (let ((node (%node-quiet state id)))
          (when (wnode-deps node)
            (setf (wnode-needs-broken node) (%needs-broken-by-a-revert-p state id)))))
      (setf (wstate-revision state) revision)
      state)))
