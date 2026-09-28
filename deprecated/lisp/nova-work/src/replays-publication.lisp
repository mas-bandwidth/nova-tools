;;;; replays-publication.lisp --- the publication, overlay and day-coverage
;;;; primitives the five replays of docs/SPEC-WORK.md:716-762 assert.
;;;;
;;;; These are pure records and functions, additional to and independent of the
;;;; C/O transition kernel: one revision names the snapshot, the closure
;;;; segments, both index roots and the day manifests together so a reader never
;;;; sees a root pointing at a missing file (SPEC-WORK.md:716-734); a journal
;;;; replay builds a bounded overlay over the published index, rebuilt from the
;;;; durable journal and never by replaying the journal on a query
;;;; (SPEC-WORK.md:735-746); and a manifest is what tells an absent day (no
;;;; events, gap=0, no note) from a missing segment (a coverage gap, never an
;;;; empty closed set) (SPEC-WORK.md:747-762).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; One revision publishes together.
;;; ------------------------------------------------------------------

(defstruct (publication (:conc-name pub-))
  revision snapshot-sha closed-sha dedup-sha manifests files)

(defun verify-publication (pub file-table)
  "Confirm every file the publication names is present with the hash its
manifest recorded, so a reader never sees a root pointing at a file that is not
there. FILE-TABLE is an alist path -> sha. Answer (values ok missing)."
  (let ((missing '()))
    (dolist (f (pub-files pub))
      (destructuring-bind (path . sha) f
        (unless (string= sha (cdr (assoc path file-table :test #'string=)))
          (push path missing))))
    (values (null missing) (nreverse missing))))

;;; ------------------------------------------------------------------
;;; The overlay: bounded, rebuilt from the journal, never per query.
;;; ------------------------------------------------------------------

(defstruct (overlay (:conc-name ov-))
  pages entries)

(defun replay-overlay (events index-cache page-records)
  "Rebuild the overlay from the durable journal's `:settle`/`:revive` events.
The overlay is what the next clip writes into the pages; until it does, a query
reads the pages and the overlay as one. Its page count is
`ceil(entries / page-records)`, held in bounded paged scratch under
`--index-cache`; evicting an overlay page erases nothing, because the journal is
the truth."
  (let ((entries (loop for e in events
                       when (member (getf e :kind) '(:settle :revive))
                       collect e)))
    (make-overlay :pages (min (ceiling (length entries) (max 1 page-records))
                              index-cache)
                  :entries entries)))

;;; ------------------------------------------------------------------
;;; An absent day and a missing segment are two different answers.
;;; ------------------------------------------------------------------

(defstruct (closed-entry (:conc-name ce-))
  rev id stamp)

(defstruct (closed-segment (:conc-name seg-))
  name entries)

(defstruct (day-manifest (:conc-name man-))
  name refs)                            ; refs = (segment-name from-rev to-rev)

(defun %entry-less (a b)
  (or (< (ce-rev a) (ce-rev b))
      (and (= (ce-rev a) (ce-rev b)) (string< (ce-id a) (ce-id b)))))

(defun manifest-for-day (day-name segments)
  "The dated manifest: each segment's name with its revision bounds. The
segments are revision-ordered so the first entry is the from-rev and the last the
to-rev."
  (make-day-manifest :name day-name
                     :refs (loop for s in segments
                                 for es = (seg-entries s)
                                 collect (list (seg-name s)
                                               (ce-rev (first es))
                                               (ce-rev (car (last es)))))))

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
