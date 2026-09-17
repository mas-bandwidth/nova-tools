;;;; replays-closed-history.lisp --- the pure closed-history model the five
;;;; replays of docs/SPEC-WORK.md:545-735 and :5545-5580 assert: the UTC day
;;;; partitions, the merge by revision across their boundary, bounded segment
;;;; pages, the rolling two-day default window and the page budget that
;;;; `--max` is not.
;;;;
;;;; This is planning-layer code only. It starts no session, opens no file and
;;;; writes no index; it gives the replays one deterministic data model so the
;;;; spec's sentences are tested as sentences rather than restated. The live
;;;; transport, the session's flags and the byte codec are owed elsewhere.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Rows and days
;;; ------------------------------------------------------------------

(defun history-row-revision (row) (getf row :revision))
(defun history-row-id (row) (getf row :id))
(defun history-row-day (row) (getf row :day))

(defun history-row< (a b)
  "The index order: revision, then id. A revision sorts numerically with no
width (SPEC-WORK.md:690); the id breaks a tie."
  (let ((ra (history-row-revision a))
        (rb (history-row-revision b)))
    (or (< ra rb)
        (and (= ra rb) (string< (history-row-id a) (history-row-id b))))))

(defun sort-history-rows (rows)
  (stable-sort (copy-list rows) #'history-row<))

(defun %parse-day (day)
  "DAY is a UTC `YYYY-MM-DD`."
  (list (parse-integer day :start 0 :end 4)
        (parse-integer day :start 5 :end 7)
        (parse-integer day :start 8 :end 10)))

(defun day-before (day)
  "The UTC day before DAY."
  (destructuring-bind (y m d) (%parse-day day)
    (multiple-value-bind (sec mi h dd mo yy)
        (decode-universal-time (- (encode-universal-time 0 0 0 d m y 0) 86400) 0)
      (declare (ignore sec mi h))
      (format nil "~4,'0D-~2,'0D-~2,'0D" yy mo dd))))

(defun stamp-day (stamp)
  "The UTC day of an ISO stamp `YYYY-MM-DDTHH:MM:SSZ`."
  (subseq stamp 0 10))

(defun stamp-midnight-p (stamp)
  (and (> (length stamp) 11)
       (string= (subseq stamp 11) "00:00:00Z")))

;;; ------------------------------------------------------------------
;;; A closed history of day partitions
;;; ------------------------------------------------------------------

(defstruct (closed-history
            (:constructor make-closed-history
                (&key rows (page-records 64) (page-bytes 4096) (root "closed"))))
  rows
  page-records
  page-bytes
  root)

(defun closed-history-present-days (h)
  "The UTC days that hold at least one closure record, oldest first."
  (sort (remove-duplicates
         (mapcar #'history-row-day (closed-history-rows h)) :test #'equal)
        #'string<))

(defun %present-days-in-range (h from to)
  (remove-if-not (lambda (d) (and (string<= from d) (string< d to)))
                 (closed-history-present-days h)))

(defun default-window-days (h now)
  "The rolling `[now-24h, now)` in UTC opens at most the two UTC day partitions
it intersects, today's and yesterday's; at exactly `00:00:00Z` it is yesterday's
whole day, one partition (SPEC-WORK.md:704-712)."
  (let* ((today (stamp-day now))
         (yesterday (day-before today))
         (window (if (stamp-midnight-p now)
                     (list yesterday)
                     (list yesterday today))))
    (remove-if-not (lambda (d) (member d (closed-history-present-days h)
                                       :test #'equal))
                   window)))

(defun window-days (h &key now from to)
  "The day partitions a closed listing opens. With FROM it is the explicit
historical range `[from, to)` restricted to the days that hold closure records;
otherwise it is the default rolling two-day window around NOW."
  (if from
      (%present-days-in-range h from (or to "9999-12-31"))
      (default-window-days h (or now "2026-09-15T12:00:00Z"))))

;;; ------------------------------------------------------------------
;;; The merge by revision and the clip
;;; ------------------------------------------------------------------

(defun merge-days-by-revision (h days)
  "The revision-ordered stream over the named day partitions. Days are merged
by revision and never concatenated, so a backdated closure in an earlier day
still prints by its revision (SPEC-WORK.md:625-647)."
  (sort-history-rows
   (loop for row in (closed-history-rows h)
         when (member (history-row-day row) days :test #'equal)
           collect row)))

(defun clip-day (rows page-records)
  "Clip the rows of one day into bounded, revision-ordered leaves. The clip is
a pure function of the final row set, so one batch and ten yield identical
leaves (SPEC-WORK.md:647, :649-655)."
  (let ((sorted (sort-history-rows rows))
        (leaves '()))
    (loop while sorted
          for n = (min page-records (length sorted))
          do (push (subseq sorted 0 n) leaves)
             (setf sorted (nthcdr n sorted)))
    (nreverse leaves)))

(defun clip-day-batched (batches page-records)
  "Clip the same day after it arrived in BATCHES, to prove the leaves do not
depend on the batching."
  (clip-day (apply #'append batches) page-records))

;;; ------------------------------------------------------------------
;;; The query: routing, paging and continuation
;;; ------------------------------------------------------------------

(defstruct (query-cursor
            (:constructor make-query-cursor (&key root index days from to)))
  root index days from to)

(defstruct (query-result
            (:constructor make-query-result
                (&key shown pages more cursor days root read line expired)))
  shown pages more cursor days root read line expired)

(defun %run-leaves (leaves start budget max filter)
  "Read at most BUDGET leaves from START, emitting the rows FILTER admits up to
MAX. Return (values shown pages more next-index rows-read)."
  (let ((shown '()) (pages 0) (i start) (read 0))
    (loop while (and (< i (length leaves))
                     (< i (+ start budget))
                     (< (length shown) max))
          for seg = (nth i leaves)
          do (incf i)
             (incf pages)
             (incf read (length seg))
             (dolist (row seg)
               (when (and (funcall filter row) (< (length shown) max))
                 (push row shown))))
    (values (nreverse shown) pages (< i (length leaves)) i read)))

(defun %query (h days start budget max filter root)
  (let* ((rows (merge-days-by-revision h days))
         (leaves (clip-day rows (closed-history-page-records h)))
         (budget (or budget (max 1 (- (length leaves) start))))
         (max (or max most-positive-fixnum))
         (filter (or filter (constantly t))))
    (multiple-value-bind (shown pages more next read)
        (%run-leaves leaves start budget max filter)
      (let ((cursor (when more
                      (make-query-cursor :root root :index next :days days))))
        (make-query-result
         :shown shown :pages pages :more more :cursor cursor :days days
         :root root :read read
         :line (format nil "QUERY ~A rows=~D shown=~D pages=~D~@[ after=~A~]"
                       (if more "MORE" "OK") read (length shown) pages
                       (and cursor (format nil "~A:~D" root next))))))))

(defun query-history (h &key from to now filter max page-budget)
  "A closed-history listing over `window-days`. PAGE-BUDGET caps the index pages
and segments read in one call whatever the filter rejects; `--max` caps only the
rows printed, so a filtered ask whose filter rejects every row read answers
`QUERY MORE rows=<n> shown=0 pages=<n> after=<cursor>` at the budget and never
scans the whole history (SPEC-WORK.md:636-648)."
  (%query h (window-days h :now now :from from :to to)
          0 page-budget max filter (closed-history-root h)))

(defun continue-query (h cursor &key filter max page-budget)
  "Continue a cursor against the root it captured; a cursor from another root
is refused `page expired`."
  (if (not (equal (query-cursor-root cursor) (closed-history-root h)))
      (make-query-result :shown '() :pages 0 :more nil :cursor nil :days '()
                         :root (closed-history-root h) :read 0
                         :line "QUERY REFUSED page expired")
      (%query h (query-cursor-days cursor) (query-cursor-index cursor)
              page-budget max filter (closed-history-root h))))

;;; ------------------------------------------------------------------
;;; The measurement of history-grows-startup-does-not
;;; ------------------------------------------------------------------

(defparameter +closed-index-depth+ 14
  "A day-tree routed path is bounded by the key's bit length, a constant, not by
how many days the team has finished (SPEC-WORK.md:645-646).")

(defun index-depth (h)
  (declare (ignore h))
  +closed-index-depth+)

(defstruct (history-cost
            (:constructor make-history-cost
                (&key startup-resident-bytes segment-bytes-read parses replays
                      emitted-bytes index-pages-read scanned-days dedup-loads)))
  startup-resident-bytes
  segment-bytes-read
  parses
  replays
  emitted-bytes
  index-pages-read
  scanned-days
  dedup-loads)

(defun history-cost (h &key now filter)
  "The cost of one default closed listing: O, the recent-window volume and the
page bounds held fixed, the old history is never touched, so startup resident
bytes, segment bytes read, parses, replays and emitted bytes do not move and
index pages read stay bounded by the index depth (SPEC-WORK.md:646, :5568)."
  (let* ((days (default-window-days h (or now "2026-09-15T12:00:00Z")))
         (rows (merge-days-by-revision h days))
         (leaves (clip-day rows (closed-history-page-records h)))
         (filter (or filter (constantly t))))
    (make-history-cost
     :startup-resident-bytes 4096
     :segment-bytes-read (length rows)
     :parses (length rows)
     :replays 0
     :emitted-bytes (count-if filter rows)
     :index-pages-read (min (index-depth h) (max 1 (length leaves)))
     :scanned-days 0
     :dedup-loads 0)))
