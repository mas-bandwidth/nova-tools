;;;; s6-partition.lisp --- C partitioned by day, segments, manifests, the merge.
;;;;
;;;; docs/SPEC-WORK.md:548-575 — "C is partitioned by day, and the day is the
;;;; event's own." A closure record is written into the UTC day of its recorded
;;;; stamp in bounded immutable segments; a dated manifest names a day's segments
;;;; with their revision and stamp bounds, their hashes and their record counts,
;;;; and a busy day has many segments. Recorded event time chooses the partition
;;;; and the revision remains the authoritative ordering, so a selection merges
;;;; the intersecting days by revision and never concatenates them.
;;;;
;;;; docs/SPEC-WORK.md:641-649 — an absent day and a missing segment are two
;;;; different answers, and the manifest is what tells them apart. A query that
;;;; asks what an item's state was as of a window end whose partition it cannot
;;;; read refuses rather than answering from a newer row.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Stamps and durations.
;;; ------------------------------------------------------------------

(defun parse-duration (spec)
  "`24h` -> seconds. The spang is `<n><h|m|s>`."
  (let ((unit (subseq spec (position-if-not #'digit-char-p spec))))
    (* (parse-integer spec :junk-allowed t)
       (cond ((string= unit "h") 3600)
             ((string= unit "m") 60)
             ((string= unit "s") 1)
             (t (error 'unsupported-input :what (format nil "duration ~A" spec)))))))

(defun stamp-day (stamp)
  "The UTC day of an RFC 3339 Z stamp, e.g. \"2026-09-14T12:00:00Z\" -> \"2026-09-14\"."
  (subseq stamp 0 10))

(defun stamp->ut (stamp)
  (let ((y (parse-integer stamp :start 0 :end 4))
        (mo (parse-integer stamp :start 5 :end 7))
        (d (parse-integer stamp :start 8 :end 10))
        (h (parse-integer stamp :start 11 :end 13))
        (mi (parse-integer stamp :start 14 :end 16))
        (s (parse-integer stamp :start 17 :end 19)))
    (encode-universal-time s mi h d mo y 0)))

(defun ut->day (ut)
  (multiple-value-bind (s mi h d mo y) (decode-universal-time ut 0)
    (declare (ignore s mi h))
    (format nil "~4,'0D-~2,'0D-~2,'0D" y mo d)))

(defun next-midnight (ut)
  "The first UTC midnight strictly after UT."
  (let ((r (mod ut 86400)))
    (if (zerop r) (+ ut 86400) (+ ut (- 86400 r)))))

(defun window-day-partitions (now duration-seconds)
  "The day partitions a rolling [now - duration, now) window intersects, in
order. `duration-seconds` is `--closed-window`; the default 24h meets at most
today's and yesterday's partitions, and at exactly 00:00:00Z only yesterday's."
  (let* ((end (stamp->ut now))
         (start (- end duration-seconds))
         (days '()))
    (loop for cur = start then (next-midnight cur)
          while (< cur end)
          do (pushnew (ut->day cur) days :test #'string=))
    (nreverse days)))

;;; ------------------------------------------------------------------
;;; Closed rows, segments and manifests.
;;; ------------------------------------------------------------------

(defstruct (closed-entry (:conc-name ce-))
  rev id stamp)

(defstruct (closed-segment (:conc-name seg-))
  name entries)

(defstruct (day-manifest (:conc-name man-))
  name refs) ; refs = list of (segment-name from-rev to-rev)

(defun %entry-less (a b)
  (or (< (ce-rev a) (ce-rev b))
      (and (= (ce-rev a) (ce-rev b)) (string< (ce-id a) (ce-id b)))))

(defun %chunk (list n)
  (if (null list)
      '()
      (cons (subseq list 0 (min n (length list)))
            (%chunk (nthcdr n list) n))))

(defun segmentize-day (day-name entries page-records)
  "Split one day's entries into bounded immutable segments of at most
PAGE-RECORDS each, so a busy day has many segments and never one unbounded read."
  (loop for chunk in (%chunk (coerce entries 'list) page-records)
        for i from 1
        collect (make-closed-segment
                 :name (format nil "closed/segments/~A-s~D.sexp" day-name i)
                 :entries chunk)))

(defun manifest-for-day (day-name segments)
  "The dated manifest: each segment's name with its revision bounds. ENTRIES are
revision-ordered so the first is the from-rev and the last the to-rev."
  (make-day-manifest :name day-name
                     :refs (loop for s in segments
                                 for es = (seg-entries s)
                                 collect (list (seg-name s)
                                               (ce-rev (first es))
                                               (ce-rev (car (last es)))))))

(defun merge-days-by-revision (streams)
  "K-way merge of the day streams, each already revision-ordered, into one
revision-ordered list on `<event-rev>:<id>`. A backdated stamp stays in its
recorded day while the revision remains the order, so two days laid end to end
would print a later revision before an earlier one; this never does."
  (let ((out '()))
    (loop
      (let ((best nil) (best-stream -1))
        (dotimes (i (length streams))
          (let ((h (nth i streams)))
            (when (and h (or (null best) (%entry-less (car h) best)))
              (setf best (car h) best-stream i))))
        (unless best (return))
        (push best out)
        (setf (nth best-stream streams) (cdr (nth best-stream streams)))))
    (nreverse out)))

(defun closed-day-selection (day-names manifests segment-table)
  "Answer (values rows gap notes) over the complete manifested day range.
DAY-NAMES is ordered; MANIFESTS is the list of `day-manifest`; SEGMENT-TABLE is
an alist segment-name -> closed-segment holding only the segments the disk
actually has. A day with no manifest means no events that day (gap 0, no note);
a segment a manifest names that is missing is a coverage gap. The rows are the
revision-ordered merge."
  (let ((rows '()) (gap 0) (notes '()))
    (dolist (dn day-names)
      (let ((m (find dn manifests :key #'man-name :test #'string=)))
        (when m
          (dolist (ref (man-refs m))
            (destructuring-bind (name from to) ref
              (let ((seg (cdr (assoc name segment-table :test #'string=))))
                (if seg
                    (setf rows (nconc rows (seg-entries seg)))
                    (progn (incf gap)
                           (push (format nil "QUERY NOTE coverage-gap file=~A range=~D-~D"
                                         name from to)
                                 notes)))))))))
    (setf rows (sort (copy-list rows) #'%entry-less))
    (values rows gap (nreverse notes))))

(defun as-of-refusal (ask as-of partition)
  "`QUERY FAIL ask=<kind> as-of=<stamp> partition=<yyyy-mm-dd>: historical window
unavailable` — refused rather than answered from a newer row."
  (format nil "QUERY FAIL ask=~A as-of=~A partition=~A: historical window unavailable"
          ask as-of partition))

(defun as-of-closed-rows (seed events revision)
  "Reconstruct the closed rows of SEED as of REVISION by replaying only the
events whose :rev is at or below REVISION. This is the primitive the --at /
--as-of path shares with the recovery replay."
  (let ((state (make-seed-state seed)))
    (dolist (e events)
      (when (<= (work-event-rev e) revision)
        (setf state (apply-event state e))))
    (state-closed-rows state)))
