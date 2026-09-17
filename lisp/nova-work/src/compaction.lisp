;;;; compaction.lisp --- journal rotation and savepoint compaction.
;;;;
;;;; docs/SPEC-WORK.md:5791, :5998, :6119-6123, :6280, :6312, :6338,
;;;; :6459-6463. A clip rotates the journal after a savepoint: the new
;;;; segment's header names the same journal id and hands forward the copied
;;;; boundary record and every retained cut and disposition a savepoint needs,
;;;; as a bounded locator rather than a scan of the old segment. Rotation that
;;;; would lose a retained cut refuses and publishes no replacement. Compaction
;;;; keys on the copy's own revision and never removes the only recoverable
;;;; copy of a retained cut: it keeps the newest verified copy plus the last
;;;; copy that still covers a cut the newest does not.
;;;;
;;;; The journal's record identities and the chain's segments live in
;;;; roadmap.lisp; this file owns what a rotation keeps and what a compaction
;;;; may drop.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; rotation keeps one journal (docs/SPEC-WORK.md:5998, :6338)
;;; ------------------------------------------------------------------

(defun journal-segment-by-path (chain path)
  "The segment of CHAIN whose file is PATH, or NIL when the chain holds none."
  (find path (journal-chain-segments chain)
        :key #'journal-segment-path :test #'string=))

(defun rotate-journal (chain &key retained-cuts retained-dispositions)
  "Rotate CHAIN: the new segment's header names the same journal id and copies
the boundary record -- its sequence and hash -- so the chain stays reachable
without scanning an old segment. RETAINED-CUTS are the savepoint cuts the
rotation must keep reachable, each a `(:sequence N :sha256 H)`; RETAINED-
DISPOSITIONS are the retained replies. Every retained cut is verified against
the segment being rotated and handed forward as a bounded locator, so a
rotation that would lose one refuses rather than publishing a replacement
(docs/SPEC-WORK.md:5998, :6338, :6459-6463)."
  (let* ((segments (journal-chain-segments chain))
         (old (car (last segments)))
         (records (journal-segment-records old))
         (boundary (car (last records)))
         (number (1+ (length segments))))
    (dolist (cut retained-cuts)
      (let ((rec (and (getf cut :sequence)
                      (find (getf cut :sequence) records
                            :key #'journal-record-seq))))
        (unless (and rec (string= (getf cut :sha256) (journal-record-hash rec)))
          (error 'unsupported-input
                 :what (format nil "rotation would lose retained cut ~A"
                               (getf cut :sequence))))))
    (let* ((header (list :journal-id (journal-chain-id chain)
                        :segment number
                        :copied-from (journal-segment-path old)
                        :copied-boundary (and boundary
                                              (list :seq (journal-record-seq boundary)
                                                    :sha256 (journal-record-hash boundary)))
                        :retained-cuts (or retained-cuts '())
                        :retained-dispositions (or retained-dispositions '())))
          (new (make-journal-segment
                :path (format nil "~A.~D" (journal-chain-id chain) number)
                :header header
                :records (if boundary (list boundary) '()))))
      (make-journal-chain :id (journal-chain-id chain)
                          :segments (append segments (list new))))))

(defun savepoint-cut-reachable-p (chain cut)
  "The cut is reachable from the newest segment's copied boundary or its
retained-cut locators alone, with no scan of an old segment
(docs/SPEC-WORK.md:5998, :6338)."
  (let* ((header (journal-segment-header (journal-last-segment chain)))
         (boundary (getf header :copied-boundary))
         (locs (getf header :retained-cuts)))
    (and boundary
         (or (= cut (getf boundary :seq))
             (and (find cut locs :key (lambda (c) (getf c :sequence)))
                  t)))))

(defun export-journal-bundle (chain &key from)
  "The offline bundle written from the file FROM names. The retained boundary it
carries is the same record whichever segment file it starts from, so the bundle
is byte-identical either way; a file the chain does not hold refuses rather than
being guessed (docs/SPEC-WORK.md:6001)."
  (let ((seg (if from
                 (or (journal-segment-by-path chain from)
                     (error 'unsupported-input
                            :what (format nil "no journal segment ~A in the chain" from)))
                 (journal-last-segment chain))))
    (let* ((header (journal-segment-header seg))
           (boundary (or (getf header :copied-boundary)
                         (let ((last-rec (car (last (journal-segment-records seg)))))
                           (and last-rec
                                (list :seq (journal-record-seq last-rec)
                                      :sha256 (journal-record-hash last-rec)))))))
      (canonical-string (list :journal-bundle
                              :journal-id (journal-chain-id chain)
                              :boundary (if boundary
                                            (list :seq (getf boundary :seq)
                                                  :sha256 (getf boundary :sha256))
                                            +absent+))))))

;;; ------------------------------------------------------------------
;;; compaction keeps the last copy (docs/SPEC-WORK.md:5791, :6280)
;;; ------------------------------------------------------------------

(defun copy-id (copy)
  (getf copy :id))

(defun compact-copies (copies)
  "Plan a compaction that may never remove the only recoverable copy of accepted
work. The newest verified copy is kept, chosen by its own revision and never by
the order it is listed in; an unverified copy is pruned even when it is newer.
Every retained cut some verified copy still covers is kept recoverable: a copy
that is the last to cover a cut the newest does not is kept beside it. With no
verified copy nothing is recoverable and nothing is pruned
(docs/SPEC-WORK.md:5791, :6280)."
  (let ((verified (remove-if-not (lambda (c) (getf c :verified)) copies)))
    (if (null verified)
        (list :keep (mapcar #'copy-id copies) :pruned '())
        (let* ((newest (car (sort (copy-list verified) #'>
                                 :key (lambda (c) (getf c :revision 0)))))
               (covers (reduce (lambda (a c) (union a (getf c :covers)))
                               verified :initial-value '()))
               (needed (set-difference covers (getf newest :covers)
                                       :test #'equal))
               (keepers (remove-if-not
                         (lambda (c)
                           (or (eq c newest)
                               (intersection (getf c :covers) needed :test #'equal)))
                         verified))
               (keep-ids (mapcar #'copy-id keepers)))
          (list :keep keep-ids
                :pruned (set-difference (mapcar #'copy-id copies) keep-ids
                                        :test #'equal))))))
