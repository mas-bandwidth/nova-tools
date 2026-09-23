;;;; journal-rotate.lisp --- rotate a real journal file, publishing a
;;;; validated replacement first.
;;;;
;;;; docs/SPEC-WORK.md:402-410: a clip may rotate the journal, opening a fresh
;;;; physical segment whose first concern is bounded, so a long-lived set's
;;;; journal need not grow forever. docs/SPEC-WORK.md:471-482: the new segment's
;;;; header names the journal id, the previous segment and the copied boundary
;;;; record; where #333 differs, this is the contract and #333 is the slice to
;;;; change.
;;;;
;;;; docs/SPEC-WORK.md:7176-7182: **Rotating or pruning a journal keeps a
;;;; reachable verified cut and every tail record and disposition each retained
;;;; savepoint needs, or publishes a validated replacement first** -- the newest
;;;; filename and the largest revision are never that proof, and a rotation may
;;;; hand a bounded locator to the same retained record rather than a scan.
;;;;
;;;; docs/SPEC-WORK.md:7119-7121: retention may never remove the only
;;;; recoverable copy. Rotation therefore deletes NOTHING: the old segment stays
;;;; on disk, byte-identical, and only the boundary record is carried forward.

(in-package #:nova-work)

(defun %rotation-scan (path)
  "One pass over PATH for a rotation. Answers (values HEADER-FORM RECORDS STATE)
where RECORDS are the complete records in sequence and STATE is :ok, :torn or
:corrupt. A line the reader cannot parse is a torn tail (an interrupted append);
a line that parses but whose sequence, length or checksum does not hold is a
corrupt record (docs/SPEC-WORK.md:7174-7175 keeps both)."
  (let ((records '())
        (state :ok)
        (seq 0))
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8 :if-does-not-exist nil)
      (unless in
        (error 'unsupported-input :what (format nil "no journal file at ~A" path)))
      (let ((header-line (read-line in nil :eof)))
        (when (eq header-line :eof)
          (error 'journal-corrupt-data :path path
                 :reason "unexpected EOF reading header"))
        (let ((header (handler-case (read-restricted header-line)
                        (error (c)
                          (error 'journal-corrupt-data :path path
                                 :reason (format nil "header parse error: ~A" c))))))
          (let* ((plist (and (consp header) (rest header)))
                 (boundary (getf plist :copied-boundary))
                 (start (if (and (consp boundary)
                                 (integerp (getf boundary :sequence)))
                            (1- (getf boundary :sequence))
                            0)))
            (setf seq start)
            (loop
              (let ((line (read-line in nil :eof)))
                (cond
                  ((eq line :eof) (return))
                  ((zerop (length line)) (return))
                  (t
                   (let ((frame (handler-case (read-restricted line)
                                  (error () (setf state :torn) nil))))
                     (cond
                       ((null frame) (return))
                       ((not (and (listp frame) (eq (first frame) :frame)))
                        (setf state :corrupt) (return))
                       (t
                        (let* ((fplist (rest frame))
                               (fseq (getf fplist :seq))
                               (len (getf fplist :len))
                               (checksum (getf fplist :checksum))
                               (record (getf fplist :record)))
                          (unless (and (eql fseq (1+ seq)) (listp record))
                            (setf state :corrupt) (return))
                          (let* ((canon (canonical-string record))
                                 (actual-len (length canon))
                                 (actual-checksum (sha256-hex canon)))
                            (unless (and (eql len actual-len)
                                         (string= checksum actual-checksum))
                              (setf state :corrupt) (return))
                            (incf seq)
                            (push (list :sequence fseq
                                        :record-sha256 checksum
                                        :request (getf record :request)
                                        :payload-sha256 (getf record :digest)
                                        :reply (getf record :line)
                                        :rev (getf record :rev)
                                        :digest (getf record :digest)
                                        :events (getf record :events)
                                        :boundary-state (getf record :digest)
                                        :record record)
                                  records)))))))))))
          (values header (nreverse records) state))))))

(defun %write-boundary-frame (stream record seq)
  (let* ((canon (canonical-string record))
         (frame (list :frame :seq seq :len (length canon)
                      :checksum (sha256-hex canon) :record record)))
    (write-string (canonical-string frame) stream)
    (write-char #\Newline stream)))

(defun rotate-file-journal (path &key root retained-ids)
  "Rotate the real journal file PATH into a new physical segment of the SAME
logical journal. In order: (a) scan and refuse a torn tail or a corrupt record
first, before any byte is written; (b) write a CANDIDATE file -- never the live
name -- whose header names the logical identity, the previous segment and the
copied boundary record at its ORIGINAL sequence and hash; (c) re-verify every
retained savepoint against the CANDIDATE; (d) on any failure delete the
candidate, leave the old file byte-identical and refuse naming the gap; (e) only
then rename into place and sync the directory. RETAINED-IDS name the savepoints
under ROOT whose cuts are handed forward as bounded locators. Answers
(values NEW-PATH NEW-HEADER)."
  (let* ((file (namestring (merge-pathnames path))))
    (multiple-value-bind (header records state) (%rotation-scan file)
      (when (eq state :torn)
        (error 'unsupported-input
               :what (format nil "rotation refused: torn tail; ~A is unchanged" file)))
      (when (eq state :corrupt)
        (error 'unsupported-input
               :what (format nil "rotation refused: corrupt record; ~A is unchanged" file)))
      (let* ((plist (rest header))
             (identity (or (header-journal-identity plist) (mint-journal-identity)))
             (boundary (car (last records)))
             (bseq (and boundary (getf boundary :sequence)))
             (bsha (and boundary (getf boundary :record-sha256)))
             (brecord (and boundary (getf boundary :record)))
             (bstate (and boundary (getf boundary :boundary-state)))
             (segment (1+ (or (getf plist :segment) 1)))
             (new-path (format nil "~A.~D" file segment))
             (candidate (format nil "~A.candidate" new-path))
             (retained-cuts
               (loop for id in retained-ids
                     for m = (and root (read-savepoint-manifest root id))
                     for cut = (and m (getf m :replay-cut))
                     when (and (consp cut) (getf cut :sequence))
                       collect (list :sequence (getf cut :sequence)
                                     :sha256 (getf cut :sha256))))
             (new-header (list :journal-header
                               :magic "nova-work/journal"
                               :version 1
                               :initial-state (getf plist :initial-state)
                               :capacity (getf plist :capacity)
                               :bench (getf plist :bench)
                               :created-at (getf plist :created-at)
                               :journal-identity identity
                               :segment segment
                               :previous-segment file
                               :copied-boundary (and bseq
                                                     (list :sequence bseq :sha256 bsha))
                               :boundary-state bstate
                               :retained-cuts retained-cuts)))
        (handler-case
            (progn
              ;; (b) the candidate, never the live name.
              (with-open-file (out candidate :direction :output
                                            :element-type 'character
                                            :external-format :utf-8
                                            :if-exists :supersede
                                            :if-does-not-exist :create)
                (write-string (canonical-string new-header) out)
                (write-char #\Newline out)
                (when (and brecord bseq)
                  (%write-boundary-frame out brecord bseq))
                (sync-stream out :path candidate))
              ;; (c) re-verify every retained savepoint against the CANDIDATE.
              (dolist (id retained-ids)
                (multiple-value-bind (okp line)
                    (savepoint-verify-published root id :journal-path candidate)
                  (unless okp
                    (error 'unsupported-input
                           :what (format nil "rotation refused: retained savepoint ~A does not verify against the candidate: ~A"
                                         id line)))))
              ;; (e) only then rename into place and sync the directory.
              (%savepoint-rename candidate new-path)
              (sync-directory (directory-namestring (merge-pathnames new-path)))
              (values new-path new-header))
          (error (c)
            ;; (d) delete the candidate; the old file was never written.
            (ignore-errors (delete-file candidate))
            (error c)))))))

(defun journal-rotation-locator (header cut)
  "Where the retained CUT lives, from the segment HEADER alone -- its copied
boundary record or one of its bounded retained-cut locators. Reads no old
segment. Answers the locator plist, or NIL when the header names no such cut."
  (let* ((plist (if (and (consp header) (eq (first header) :journal-header))
                    (rest header)
                    header))
         (seq (getf cut :sequence))
         (hash (getf cut :sha256))
         (boundary (getf plist :copied-boundary)))
    (or (and (consp boundary)
             (consp (cdr boundary))
             (eql seq (or (getf boundary :sequence) (getf boundary :seq)))
             (equal hash (getf boundary :sha256))
             (list :segment (getf plist :previous-segment)
                   :sequence seq :sha256 hash))
        (find-if (lambda (c)
                   (and (eql seq (or (getf c :sequence) (getf c :seq)))
                        (equal hash (getf c :sha256))))
                 (getf plist :retained-cuts)))))
