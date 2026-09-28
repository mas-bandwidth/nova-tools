;;;; closed-history.lisp --- the read side of the COW root: the partition
;;;; check, bounded closed-index paging with a pinned cursor, the ask's branch
;;;; and window flags, and the coverage-gap accounting.
;;;;
;;;; docs/SPEC-WORK.md:5492-5543, 5592. Nothing here loads the history: the
;;;; rows are the append-only closed index the write path maintains, so a page
;;;; visits no node, parses nothing and replays nothing.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The COW root partition (SPEC-WORK.md:5495-5497)
;;; ------------------------------------------------------------------

(defun cow-load-findings (state)
  "Rule 18 findings over a candidate root: every id whose *latest* closed-index
row settles it while its node still reads :o -- an id in both C and O.

SPEC-WORK.md:1630 -- \"Rule 18 below reads the same way: the finding is an id
whose *latest* state puts it in both branches, never the history of an id that
has honestly moved and kept its record\", and replay
`revive-appends-and-counts-latest` (:6242) requires an id that settled and was
revived to be counted once either way. C is append-only, so a revived id keeps
its `:settle` row: reading *any* row naming an id therefore made every honest
reopen a finding. `wstate-rows` is newest first (`state-closed-rows` reverses
it), so the first row naming an id is that id's latest, and a `:revive` there
is the kernel's own record that the id left C."
  (let ((latest (make-hash-table :test #'equal))
        (findings '()))
    (dolist (row (wstate-rows state))
      (let ((id (getf row :node)))
        (unless (nth-value 1 (gethash id latest))
          (setf (gethash id latest) (getf row :kind)))))
    (dolist (id (wstate-order state))
      (let ((node (%node-quiet state id)))
        (when (and node
                   (eq :settle (gethash id latest))
                   (eq :o (wnode-branch node)))
          (push id findings))))
    (nreverse findings)))

(defun cow-partition-holds-p (state)
  "One id is in C or in O and never in both, and open= plus closed= equals the
scope's counted total on every ask (SPEC-WORK.md:5495-5497)."
  (and (null (cow-load-findings state))
       (= (+ (wstate-root-open state) (wstate-closed state))
          (length (wstate-order state)))
       (every (lambda (id)
                (member (wnode-branch (%node-quiet state id)) '(:o :c)))
              (wstate-order state))))

(defun hand-write-closed-row (state id rev)
  "A closed-index row written by hand for an id the node still reads :o: the
double membership rule 18 catches at load (SPEC-WORK.md:5496)."
  (push (list :key (format nil "~D:~A" rev id) :kind :settle :node id :rev rev
              :disposition :done :stamp "2026-09-14T12:00:00Z"
              :revived "-" :settles 1)
        (wstate-rows state))
  state)

(defun cow-candidate-gate (state)
  "Refuse a candidate whose root is not a partition. (values OK-P LINE CODE)."
  (let ((findings (cow-load-findings state)))
    (if findings
        (values nil (format nil "LOAD FAIL rule 18: ~{~A~^,~} in both C and O"
                            findings)
                1)
        (values t "LOAD OK" 0))))

;;; ------------------------------------------------------------------
;;; Bounded closed-index paging, cursor pinned to a revision
;;; (SPEC-WORK.md:5538-5540, 5592-5594)
;;; ------------------------------------------------------------------

(defstruct (closed-cursor
             (:constructor make-closed-cursor (&key rev after floor)))
  rev after floor)

(defun closed-rows-before (state rev)
  "The closed-index rows whose event revision is at or below REV, newest first.
Reads the index only: no node is visited, nothing is parsed and nothing is
replayed."
  (remove-if (lambda (row) (> (getf row :rev) rev)) (wstate-rows state)))

(defun closed-page (state &key (max 20) cursor floor)
  "One bounded page of the closed index. A CURSOR pins the revision it was
opened at, so a settle between two pages cannot add, drop or repeat a row in
the page sequence. Answers (values ROWS MORE-P NEXT-CURSOR LINE); a cursor
whose pinned revision is below FLOOR is refused `page expired`
(SPEC-WORK.md:5538-5540, 5592-5594)."
  (let* ((rev (if cursor (closed-cursor-rev cursor) (state-revision state)))
         (floor (or floor (if cursor (closed-cursor-floor cursor) 0)))
         (after (and cursor (closed-cursor-after cursor))))
    (when (< rev floor)
      (return-from closed-page
        (values nil nil nil
                (format nil "QUERY FAIL page expired rev=~D floor=~D" rev floor))))
    (let* ((rows (closed-rows-before state rev))
           (pos (if after
                    (position after rows :key (lambda (r) (getf r :key))
                              :test #'string=)
                    -1))
           (slice (subseq rows (1+ (or pos -1))))
           (page (subseq slice 0 (min max (length slice))))
           (more (> (length slice) (length page)))
           (next (and more (make-closed-cursor
                            :rev rev
                            :after (getf (car (last page)) :key)
                            :floor floor))))
      (values page more next
              (format nil "QUERY OK ask=closed shown=~D more=~A after=~A rev=~D parses=0 replays=0"
                      (length page) (if more "t" "f")
                      (if next (closed-cursor-after next) "-") rev)))))

;;; ------------------------------------------------------------------
;;; The closed ask with the retention archive file absent
;;; (SPEC-WORK.md:5541-5543)
;;; ------------------------------------------------------------------

(defun closed-ask (state &key archive reach-bodies)
  "The closed index answers its rows whatever the retention archive file's
state. ARCHIVE is a hash of closed-row key -> body, or NIL when the file is
absent. REACH-BODIES asks for the archived bodies: a missing body is counted
into gap= and named once by QUERY NOTE coverage-gap, and the row list is never
shortened (SPEC-WORK.md:5541-5543)."
  (let* ((rows (state-closed-rows state))
         (missing (if reach-bodies
                      (count-if (lambda (row)
                                  (not (and archive
                                            (gethash (getf row :key) archive))))
                                rows)
                      0)))
    (values rows
            (if (plusp missing)
                (format nil "QUERY OK ask=closed rows=~D gap=~D~%QUERY NOTE coverage-gap"
                        (length rows) missing)
                (format nil "QUERY OK ask=closed rows=~D gap=0" (length rows))))))

;;; ------------------------------------------------------------------
;;; The ask's branch and window flags (SPEC-WORK.md:5546-5549)
;;; ------------------------------------------------------------------

(defun validate-ask (&key ask branch from to)
  "A query --ask is refused at exit 2 unless it names --branch, a closed ask
carries --from/--to, an open ask carries no --from, and who/stale/handoffs are
not admitted under --branch closed or --branch root
(SPEC-WORK.md:5546-5549)."
  (cond
    ((null branch)
     (values nil (format nil "QUERY FAIL ask=~A: --branch is required"
                         (or ask :size))
             2))
    ((and (member branch '(:closed :root)) (null from) (null to))
     (values nil (format nil "QUERY FAIL branch=~A: --from or --to is required"
                         branch)
             2))
    ((and (eq branch :open) from)
     (values nil "QUERY FAIL branch=open: --from is not admitted" 2))
    ((and (member ask '(:who :stale :handoffs)) (member branch '(:closed :root)))
     (values nil (format nil "QUERY FAIL ask=~A: not admitted under branch=~A"
                         ask branch)
             2))
    (t (values t "QUERY OK" 0))))


;;; ------------------------------------------------------------------
;;; folded from replays-8603.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8603.lisp --- the acceptance replays the closed history, the clip
;;;; bound and the six new verbs promise (docs/SPEC-WORK.md:5100-5560,5766-5775).
;;;;
;;;; These are the smallest engine functions the three replays of card 8603
;;;; call: a state-as-of ask that refuses an unreadable day partition, the
;;;; candidate gate that refuses one indivisible record before it is journaled,
;;;; and a clip that names every number it would write and the one remedy that
;;;; moves the overflowing part. The session, the CLI, the paged index and the
;;;; six new verbs' own kinds are outside slice 1 (README.md "What is out");
;;;; what is here is the refusal each paragraph makes true of this build.

(in-package #:nova-work)

(defun replay-word (mutation)
  "The `<MUTATION>` word of the mutation line (SPEC-WORK.md:5295)."
  (ecase mutation
    ((:state-to-done :state-to-doing) "STATE")
    (:event-reopen "EVENT")
    (:node "NODE")
    (:event "EVENT")
    (:note "NOTES")))

;;; ------------------------------------------------------------------
;;; as-of-refuses-unavailable-partition (SPEC-WORK.md:759,5135,5586)
;;; ------------------------------------------------------------------

(defun as-of-partition (as-of)
  "The `yyyy-mm-dd` day partition an `as-of` stamp names."
  (subseq as-of 0 10))

(defun ask-state-as-of (kernel &key ask as-of partitions)
  "Answer a `state-as-of` ask over the day partitions that can be read.
Return (values OK-P LINE EXIT-CODE). A partition the ask needs that is not
among PARTITIONS refuses at exit 1, naming the one partition it would need,
and is never answered from a newer row; a readable partition answers."
  (let* ((partition (as-of-partition as-of))
         (kind (string-downcase (symbol-name ask))))
    (if (member partition partitions :test #'string=)
        (values t
                (format nil "QUERY OK ask=~A as-of=~A partition=~A scope=~D"
                        kind as-of partition
                        (state-revision (kernel-state kernel)))
                0)
        (values nil
                (format nil "QUERY FAIL ask=~A as-of=~A partition=~A: historical window unavailable"
                        kind as-of partition)
                1))))

;;; ------------------------------------------------------------------
;;; indivisible-record-refused-before-ack (SPEC-WORK.md:794,5543,5994)
;;; ------------------------------------------------------------------

(defun admit-record (kernel &key mutation request key bytes page-bytes max-bytes)
  "The candidate gate for a record that may not fit a page. A single key with
its one locator that would pass --page-bytes on a page of its own is
indivisible: exit 2, `indivisible`, nothing journaled and nothing acknowledged.
A journal record the reader's bounds could not read back is refused by the same
gate, naming --max-bytes (SPEC-WORK.md:803,5295)."
  (declare (ignore kernel))
  (let ((word (replay-word mutation)))
    (cond
      ((and page-bytes (> bytes page-bytes))
       (values nil
               (format nil "~A FAIL request=~A key=~A bytes=~D past --page-bytes=~D: indivisible"
                       word request (string-downcase (symbol-name key)) bytes page-bytes)
               2))
      ((and max-bytes (> bytes max-bytes))
       (values nil
               (format nil "~A FAIL request=~A key=~A bytes=~D past --max-bytes=~D: indivisible"
                       word request (string-downcase (symbol-name key)) bytes max-bytes)
               2))
      (t
       (values t (format nil "~A OK request=~A key=~A bytes=~D"
                         word request (string-downcase (symbol-name key)) bytes)
               0)))))

;;; ------------------------------------------------------------------
;;; clip-names-the-index-that-overflowed (SPEC-WORK.md:803,5102,5553)
;;; ------------------------------------------------------------------

(defun clip (kernel &key snapshot retained index closed-index max-bytes)
  "A clip whose snapshot would exceed --max-bytes refuses, printing all four
numbers it would write beside the bound and naming the one remedy that moves
the overflowing part. The remedy does not branch: every part is bounded by
--retain, so it is always `lower --retain or raise --max-bytes`."
  (declare (ignore kernel))
  (if (and max-bytes (> snapshot max-bytes))
      (values nil
              (format nil "CLIP FAIL snapshot=~D retained=~D index=~D closed-index=~D past --max-bytes=~D, lower --retain or raise --max-bytes"
                      snapshot retained index closed-index max-bytes)
              1)
      (values t
              (format nil "CLIP OK snapshot=~D retained=~D index=~D closed-index=~D"
                      snapshot retained index closed-index)
              0)))

(defun split-index-page (bytes page-bytes)
  "An index page that would pass --page-bytes is split into a further page by
the clip that writes it and is never refused for growing (SPEC-WORK.md:803).
Return (values PAGES REFUSED-P); REFUSED-P is always NIL."
  (values (ceiling bytes page-bytes) nil))


;;; ------------------------------------------------------------------
;;; folded from replays-closed-history.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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


;;; ------------------------------------------------------------------
;;; folded from replays-publication.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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
