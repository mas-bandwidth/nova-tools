;;;; savepoint-create.lisp --- `savepoint create` over the real resident state,
;;;; the real journal and real files (docs/SPEC-WORK.md:7109-7190, the grammar
;;;; at :2276, the lines at :5983-5986).
;;;;
;;;; `src/savepoint.lisp` carries the pure model: its `savepoint-write` takes a
;;;; STORE and a list of records the replay builds, opens no file and touches
;;;; neither the kernel nor WSTATE. This file is the verb.
;;;;
;;;; **Writing one is four steps** (SPEC-WORK.md:7161-7165):
;;;;
;;;;   1. one immutable image and one record cut captured at the SAME revision
;;;;      UNDER THE SINGLE WRITER -- here, on the kernel's own command thread,
;;;;      through `submit`, writing no event and moving no revision;
;;;;   2. the image and the reply objects written to real files and synced;
;;;;   3. the candidate manifest validated against their EXACT identities --
;;;;      the bytes are read back off the disk and rehashed, never trusted from
;;;;      the variable that wrote them;
;;;;   4. published atomically -- a temporary manifest written, synced, renamed
;;;;      over the published name, and the directory synced -- with the prior
;;;;      verified savepoint kept through it.
;;;;
;;;; **The cut can never fall between an envelope's events** (:7147): it is the
;;;; last COMPLETE record of the real journal file, found by reading its frames,
;;;; and a torn tail is diagnosed as an interrupted append rather than truncated
;;;; (:7171-7172). **The savepoint is local and the checkpoint is shared**
;;;; (:7109): `rev=` is this bench's savepoint and `checkpoint=` the newest
;;;; clipped one, so a local success can never be read as a shared backup
;;;; (:5983).
;;;;
;;;; NOT HERE. `savepoint list`, `verify`, `restore` and `compare` are their own
;;;; rows; this file publishes what they will read, and reads back only what
;;;; step 3 must. Journal rotation and compaction are E05 row 6.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The journal file, read as records
;;; ------------------------------------------------------------------

(defstruct (journal-scan (:constructor %make-journal-scan (records torn-p)))
  "What one pass over a real journal file found: its complete records, newest
last, and whether its tail is torn. A torn tail is evidence of an interrupted
append and is never a truncation of acknowledged work (SPEC-WORK.md:7171)."
  records torn-p)

(defun scan-journal-file (path)
  "Read PATH's complete frames in sequence. Each record answers its sequence,
its record hash, and the request, payload digest and original reply it
retained. A frame that will not parse, or whose checksum or sequence does not
hold, ends the scan and marks the tail torn."
  (let ((records '())
        (torn nil)
        (seq 0))
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8 :if-does-not-exist nil)
      (unless in
        (error 'unsupported-input
               :what (format nil "no journal file at ~A" path)))
      ;; The header is the journal's own; a savepoint reads past it.
      (read-line in nil :eof)
      (loop
        (let ((frame (handler-case (read-record-frame in path (1+ seq))
                       (error () (setf torn t) nil))))
          (unless frame (return))
          (incf seq)
          (let* ((plist (rest frame))
                 (record (getf plist :record)))
            (push (list :sequence seq
                        :record-sha256 (getf plist :checksum)
                        :request (getf record :request)
                        :payload-sha256 (getf record :digest)
                        :reply (getf record :line)
                        :rev (getf record :rev))
                  records)))))
    (%make-journal-scan (nreverse records) torn)))

(defun journal-scan-cut (scan)
  "The savepoint's replay cut: the last COMPLETE record the image reflects,
named as a sequence and a record hash, or `(:absent)` for an initial image
(SPEC-WORK.md:7135-7147)."
  (let ((last (car (last (journal-scan-records scan)))))
    (if last
        (list :sequence (getf last :sequence)
              :sha256 (getf last :record-sha256))
        +absent+)))

;;; ------------------------------------------------------------------
;;; Step 1: the capture, under the single writer
;;; ------------------------------------------------------------------

(defun savepoint-capture-submit (kernel request)
  "The `:savepoint-capture` command. It runs on the kernel's one command thread
and is a READ: it writes no event, appends nothing to the journal and moves no
revision. What it answers is the image and the revision, taken with no mutation
able to interleave -- which is what `captured at the same revision under the
single writer` means here (SPEC-WORK.md:7161)."
  (declare (ignore request))
  (let* ((state (kernel-state kernel))
         (rev (state-revision state))
         (image (canonical-string (state-canonical-form state))))
    (values t (format nil "SAVEPOINT CAPTURE rev=~D" rev) 0
            (list :image image :rev rev))))

;;; ------------------------------------------------------------------
;;; Steps 2 and 3: the objects, written, synced and read back
;;; ------------------------------------------------------------------

(defun %savepoint-write-object (path bytes)
  "Write BYTES to PATH and sync them. Answers nothing: what the manifest names
is read back off the disk in step 3, never carried from here."
  (with-open-file (out path :direction :output :element-type 'character
                            :external-format :utf-8
                            :if-exists :supersede :if-does-not-exist :create)
    (write-string bytes out)
    (sync-stream out :path (namestring path)))
  path)

(defun %savepoint-read-object (path)
  "The bytes PATH holds, read back off the disk."
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8 :if-does-not-exist nil)
    (unless in
      (error 'unsupported-input
             :what (format nil "savepoint object ~A is unreadable" path)))
    (let ((text (make-string (file-length in))))
      (subseq text 0 (read-sequence text in)))))

(defun %savepoint-object-reference (name path)
  "One hash-checked content reference inside the savepoint's own root, taken
over the bytes ON DISK: the path, their SHA-256 and their byte size
(SPEC-WORK.md:7137-7140)."
  (let ((bytes (%savepoint-read-object path)))
    (list :path name :sha256 (sha256-hex bytes) :size (length bytes))))

(defun %savepoint-reference-holds-p (reference path)
  "Step 3: the candidate manifest's reference is validated against the object's
EXACT identity, by rehashing what the disk holds."
  (let ((actual (%savepoint-object-reference (getf reference :path) path)))
    (and (equal (getf reference :sha256) (getf actual :sha256))
         (eql (getf reference :size) (getf actual :size)))))

;;; ------------------------------------------------------------------
;;; The verb
;;; ------------------------------------------------------------------

(defun %savepoint-rename (from to)
  "One atomic rename, POSIX, so the published manifest is never half a file."
  #+sbcl (sb-posix:rename from to)
  #-sbcl (error 'unsupported-input
                :what "no atomic rename outside SBCL; the engine's platform is pinned"))

(defun %directory-namestring (root)
  (let ((s (namestring root)))
    (if (and (plusp (length s)) (char= #\/ (char s (1- (length s)))))
        s
        (concatenate 'string s "/"))))

(defun journal-file-identity (journal)
  "The journal's own id: the SHA-256 of its header line, which no two journals
share and which a restore checks before it replays anything
(SPEC-WORK.md:7145, :7163)."
  (with-open-file (in (journal-path journal) :direction :input
                                             :element-type 'character
                                             :external-format :utf-8)
    (let ((header (read-line in nil "")))
      (sha256-hex header))))

(defun savepoint-directory (root id)
  (merge-pathnames (format nil "~A/" id) (pathname (%directory-namestring root))))

(defun savepoint-manifest-path (root id)
  (merge-pathnames "manifest" (savepoint-directory root id)))

(defun read-savepoint-manifest (root id)
  "The published manifest of the savepoint ID names, as the restricted
S-expression it is, or NIL when none is published."
  (let ((path (savepoint-manifest-path root id)))
    (when (probe-file path)
      (read-restricted (%savepoint-read-object path)))))

(defun %savepoint-fail (store id rev stage reason)
  "A failed write publishes nothing and keeps the prior verified savepoint; the
attempt is listed verdict=failed (SPEC-WORK.md:7165-7168, :5986)."
  (values nil
          (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A" id rev reason)
          1
          (make-savepoint-store
           :verified (savepoint-store-verified store)
           :published (savepoint-store-published store)
           :attempts (cons (list :id id :rev rev :verdict :failed :stage stage)
                           (savepoint-store-attempts store)))))

(defun savepoint-create (kernel &key root id (as "rowan") reason
                                     (store (make-savepoint-store))
                                     (checkpoint-revision nil) (age "0s")
                                     (unshared 0) fail-stage
                                     (schema "work-savepoint-v1"))
  "`savepoint create --session <path> --as <name> --reason <text>`
(SPEC-WORK.md:2276). ROOT is the session's savepoint root, a real directory.
Answers (values OK-P LINE EXIT-CODE STORE); the STORE is the writer's resident
savepoint state, carrying the last verified savepoint, and a failure at any
step returns the previous one unchanged."
  (declare (ignore reason))
  (unless (and root id)
    (error 'unsupported-input :what "savepoint create: no root and no id"))
  (unless (and (stringp as) (plusp (length as)))
    (error 'unsupported-input :what "savepoint create: no --as"))
  (let ((journal (kernel-journal kernel)))
    (unless (typep journal 'file-journal)
      (error 'unsupported-input
             :what "savepoint create: the session's journal is not a durable file journal")))
  (let* ((journal (kernel-journal kernel))
         (scan (scan-journal-file (journal-path journal))))
    ;; A torn tail is evidence of an interrupted append. It is diagnosed and
    ;; kept, never truncated, and no savepoint is published over it
    ;; (SPEC-WORK.md:7171-7172).
    (when (journal-scan-torn-p scan)
      (return-from savepoint-create
        (%savepoint-fail store id (state-revision (kernel-state kernel))
                         :cut "torn tail: an interrupted append, not a truncation")))
    ;; Step 1: the image and the cut, captured at the same revision under the
    ;; single writer.
    (multiple-value-bind (okp line code capture) (submit kernel (list :verb :savepoint-capture))
      (declare (ignore line))
      (unless okp
        (return-from savepoint-create
          (%savepoint-fail store id 0 :capture (format nil "capture refused, exit ~D" code))))
      (let* ((rev (getf capture :rev))
             (image (getf capture :image))
             (cut (journal-scan-cut scan))
             (replies (journal-scan-records scan))
             (dir (savepoint-directory root id))
             (state-path (merge-pathnames "state" dir))
             (replies-path (merge-pathnames "local-replies" dir))
             (manifest-path (merge-pathnames "manifest" dir))
             (temp-path (merge-pathnames "manifest.candidate" dir)))
        (when (eq fail-stage :capture)
          (return-from savepoint-create
            (%savepoint-fail store id rev :capture "capture failed")))
        (ensure-directories-exist dir)
        ;; Step 2: the image and the reply objects written and synced.
        (handler-case
            (progn
              (%savepoint-write-object state-path image)
              (when (eq fail-stage :image) (error "injected image failure"))
              (%savepoint-write-object replies-path (canonical-string replies)))
          (error ()
            (return-from savepoint-create
              (%savepoint-fail store id rev :image "the image write failed"))))
        ;; Step 3: the candidate manifest, validated against their exact
        ;; identities read back off the disk.
        (let* ((state-ref (%savepoint-object-reference "state" state-path))
               (replies-ref (%savepoint-object-reference "local-replies" replies-path))
               (manifest (list :schema schema
                               :journal (journal-file-identity journal)
                               :local-revision rev
                               :replay-cut cut
                               :boundary +absent+
                               :state state-ref
                               :local-replies replies-ref)))
          (unless (and (%savepoint-reference-holds-p state-ref state-path)
                       (%savepoint-reference-holds-p replies-ref replies-path))
            (return-from savepoint-create
              (%savepoint-fail store id rev :manifest "journal mismatch")))
          (when (eq fail-stage :manifest)
            (return-from savepoint-create
              (%savepoint-fail store id rev :manifest "the manifest validation failed")))
          ;; Step 4: published atomically and synced, the prior verified
          ;; savepoint kept through it.
          (handler-case
              (progn
                (%savepoint-write-object temp-path (canonical-string manifest))
                (when (eq fail-stage :sync) (error "injected publish failure"))
                ;; The publication is one atomic rename over the published
                ;; name: a reader sees the previous manifest or this one and
                ;; never a half-written file. `cl:rename-file` merges the old
                ;; pathname's type into the new name, so the POSIX call is the
                ;; one that actually publishes (SPEC-WORK.md:7164).
                (%savepoint-rename (namestring temp-path) (namestring manifest-path))
                (sync-directory dir))
            (error ()
              (ignore-errors (delete-file temp-path))
              (return-from savepoint-create
                (%savepoint-fail store id rev :sync "the manifest publication failed"))))
          (let* ((manifest-sha (sha256-hex (%savepoint-read-object manifest-path)))
                 (sp (make-savepoint
                      :id id :schema schema
                      :journal-id (journal-file-identity journal)
                      :local-revision rev :replay-cut cut :boundary +absent+
                      :manifest manifest :manifest-sha manifest-sha
                      :image state-ref :local-replies replies-ref :age age
                      :failed-backup nil)))
            (values t
                    (format nil "SAVEPOINT OK id=~A rev=~D checkpoint=~A pushed=- boundary=~A age=~A unshared=~D manifest=~A shown=1"
                            id rev
                            (if checkpoint-revision
                                (format nil "~D" checkpoint-revision) "-")
                            (if (absentp cut) "-" (getf cut :sequence))
                            age unshared manifest-sha)
                    0
                    (make-savepoint-store
                     :verified sp
                     :published (cons sp (savepoint-store-published store))
                     :attempts (cons (list :id id :rev rev :verdict :ok)
                                     (savepoint-store-attempts store))))))))))

