;;;; s6.lisp --- the S6 acceptance cases. The clip, the snapshot, the day
;;;; partitions, the long operation and the savepoint, each naming its line of
;;;; docs/SPEC-WORK.md. Pure functions and records only: nothing here touches the
;;;; command thread, the kernel, the session or the journal files of earlier
;;;; slices; a wiring card connects these to the loop.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; The long operation. SPEC-WORK.md:2711-2721, replay clip-is-one-long-operation
;;; ------------------------------------------------------------------

(deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:2711"
    "expected=clip-prints-OPERATION-OK-and-exits,CLIP-OK-arrives-by-operation-wait"
  (let* ((op (start-clip :id "op-1" :request "req-9" :stamp "2026-09-16T10:00:00Z"))
         (ack (operation-ack-line op :emitted 13)))
    ;; The clip itself prints OPERATION OK at once and nothing terminal.
    (ok (search "OPERATION OK" ack) "the clip ack is not OPERATION OK")
    (ok (search "id=op-1" ack) "the ack carries no operation id")
    (ok (search "op=clip" ack) "the ack carries no op=clip")
    (ok (search "state=queued" ack) "the ack carries no queued state")
    ;; Nothing terminal has been printed for the clip yet.
    (ok (null (operation-terminal op)) "the clip printed a terminal line up front")
    ;; The transport settles; the CLIP OK line carrying operation= is what wait prints.
    (setf (operation-state op) :running)
    (setf (operation-result op)
          (clip-terminal-line "sock-1" "op-1" "req-9" 4 "base0" "commit0" 12 3 9))
    (setf (operation-state op) :done)
    (let ((term (operation-wait op)))
      (ok (search "CLIP OK" term) "operation wait did not print the CLIP OK line")
      (ok (search "operation=op-1" term) "the CLIP OK line carries no operation=<id>")
      (ok (search "attempts=3" term) "the CLIP OK line carries no attempts="))))

;;; ------------------------------------------------------------------
;;; Bounded startup load. SPEC-WORK.md:604-617, replay history-grows-startup-does-not
;;; ------------------------------------------------------------------

(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:605"
    "expected=lookup-reads-a-bounded-number-of-pages-not-the-whole-C-index"
  (let* ((leaves '())
         (root-entries '()))
    ;; A large closed index: thousands of ids across many leaf pages.
    (loop for i from 1 to 500
          do (let ((leaf (make-page :name (format nil "closed/pages/l~D.sexp" i)
                                    :kind :closed :records 10 :bytes 400
                                    :entries (list (format nil "T-~D" i)))))
               (push leaf leaves)
               (push (cons (format nil "T-~D" i) (page-name leaf)) root-entries)))
    (let* ((root-page (make-page :name "closed-root" :kind :closed
                                 :records 500 :bytes 20000 :entries root-entries))
           (index (make-closed-index :pages (cons root-page leaves))))
      (let ((*pages-read* 0))
        (multiple-value-bind (page err) (closed-row-lookup index "T-250")
          (ok (and (null err) page) "the lookup refused a key the index holds")
          (check-equal 2 *pages-read* "the lookup read the whole index rather than a bounded path")
          (ok (< *pages-read* 500) "startup load cost grew with history"))))))

;;; ------------------------------------------------------------------
;;; Days merge by revision. SPEC-WORK.md:562-575, replay days-merge-by-revision-never-concatenate
;;; ------------------------------------------------------------------

(deftest "days-merge-by-revision-never-concatenate" "docs/SPEC-WORK.md:562"
    "expected=backdated-row-stays-in-its-day-but-revision-orders-the-merge"
  (let* ((day-a (list (make-closed-entry :rev 10 :id "T-1" :stamp "2026-09-15T12:00:00Z")))
         ;; A backdated stamp lands in day-b but carries a *lower* revision.
         (day-b (list (make-closed-entry :rev 5 :id "T-2" :stamp "2026-09-14T09:00:00Z")))
         (merged (merge-days-by-revision (list day-a day-b))))
    ;; Two days laid end to end would print rev 10 before rev 5.
    (check-equal 2 (length merged) "the merge dropped a row")
    (check-equal 5 (ce-rev (first merged)) "the merge concatenated instead of merging by revision")
    (check-equal 10 (ce-rev (second merged)) "the merge did not order the later revision last")))

;;; ------------------------------------------------------------------
;;; Default window opens two days. SPEC-WORK.md:588-594, replay default-window-opens-two-days
;;; ------------------------------------------------------------------

(deftest "default-window-opens-two-days" "docs/SPEC-WORK.md:588"
    "expected=midday-intersects-today-and-yesterday,mnidnight-intersects-yesterday-only"
  (let ((two (window-day-partitions "2026-09-15T12:00:00Z" (parse-duration "24h"))))
    (check-equal 2 (length two) "a midday rolling window opened the wrong number of days")
    (check-equal '("2026-09-14" "2026-09-15") two "the two partitions are not today and yesterday")
    (let ((one (window-day-partitions "2026-09-15T00:00:00Z" (parse-duration "24h"))))
      (check-equal 1 (length one) "a midnight-to-midnight window opened two partitions")
      (check-equal '("2026-09-14") one "at 00:00:00Z the interval is not yesterday's whole day"))))

;;; ------------------------------------------------------------------
;;; A busy day has many segments. SPEC-WORK.md:548-560, replay busy-day-many-segments
;;; ------------------------------------------------------------------

(deftest "busy-day-many-segments" "docs/SPEC-WORK.md:548"
    "expected=one-file-per-day-is-not-one-unbounded-read"
  (let* ((entries (loop for i from 1 to 100
                        collect (make-closed-entry :rev i :id (format nil "T-~D" i)
                                                   :stamp (format nil "2026-09-14T~2,'0D:00:00Z" (mod i 24)))))
         (segs (segmentize-day "2026-09-14" entries 25)))
    (check-equal 4 (length segs) "a busy day did not split into many bounded segments")
    (check-equal 25 (length (seg-entries (first segs))) "a segment exceeded its record bound")
    (check-equal 25 (length (seg-entries (second segs))) "the second segment lost its bound")))

;;; ------------------------------------------------------------------
;;; One revision publishes together. SPEC-WORK.md:612-623, replay one-revision-publishes-together
;;; ------------------------------------------------------------------

(deftest "one-revision-publishes-together" "docs/SPEC-WORK.md:612"
    "expected=reader-never-sees-a-root-pointing-at-a-missing-file"
  (let* ((files '(("state/snapshot.sexp" . "aaa") ("closed/index.sexp" . "bbb")
                  ("dedup/index.sexp" . "ccc") ("closed/segments/2026-09-14-s1.sexp" . "ddd")))
         (pub (make-publication :revision 42 :snapshot-sha "aaa" :closed-sha "bbb"
                                :dedup-sha "ccc" :manifests '("2026-09-14") :files files)))
    (multiple-value-bind (ok missing) (verify-publication pub files)
      (ok ok "a complete publication did not verify")
      (ok (null missing) "a complete publication reported missing files"))
    (multiple-value-bind (ok missing) (verify-publication pub (cdr files))
      (ok (not ok) "a publication with a missing member verified clean")
      (check-equal '("state/snapshot.sexp") missing "the missing member was not named"))))

;;; ------------------------------------------------------------------
;;; Index replayed after a crash. SPEC-WORK.md:623-628, replay index-replayed-after-crash
;;; ------------------------------------------------------------------

(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:623"
    "expected=the-overlay-covers-the-settle-exactly-once,no-id-in-none"
  (let* ((events '((:kind :settle :node "acme/work/f1/t1" :rev 1)
                   (:kind :settle :node "acme/work/f1/t2" :rev 2)))
         (ov (replay-overlay events 64 25)))
    (check-equal 1 (ov-pages ov) "the overlay built more pages than the index cache allows")
    (let ((entries (ov-entries ov)))
      (check-equal 2 (length entries) "a settle was lost or doubled by the replay")
      (check-equal "acme/work/f1/t1" (getf (first entries) :node) "the first settle moved")
      (check-equal "acme/work/f1/t2" (getf (second entries) :node) "the second settle was not in the overlay"))))

;;; ------------------------------------------------------------------
;;; Closed index paged without full load. SPEC-WORK.md:604, replay closed-paged-without-full-load
;;; ------------------------------------------------------------------

(deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:604"
    "expected=one-row-lookup-reads-pages-not-the-closed-body"
  (let* ((leaf (make-page :name "closed/pages/x.sexp" :kind :closed :records 1000 :bytes 400000
                          :entries '("T-1" "T-2" "T-3")))
         (root (make-page :name "closed-root" :kind :closed :records 1 :bytes 40
                          :entries '(("T-1" . "closed/pages/x.sexp"))))
         (index (make-closed-index :pages (list root leaf)))
         (before (state-open-count (make-seed-state *seed*)))
         (*pages-read* 0))
    (declare (ignore before))
    (multiple-value-bind (page err) (closed-row-lookup index "T-1")
      (ok (and (null err) page) "the paged lookup refused a present row")
      (check-equal 2 *pages-read* "a single row read the whole closed index")
      ;; The body of the index (its 1000 records) is never walked into memory.
      (check-equal 1000 (page-records leaf) "the leaf lost its own record count")
      (ok (null (member "T-2" (page-entries root) :test #'equal))
          "the root page carried leaf bodies"))))

;;; ------------------------------------------------------------------
;;; An absent day is not a gap. SPEC-WORK.md:641-645, replay absent-day-is-not-a-gap
;;; ------------------------------------------------------------------

(deftest "absent-day-is-not-a-gap" "docs/SPEC-WORK.md:641"
    "expected=no-manifest-means-no-events-that-day,not-a-coverage-gap"
  (let* ((seg (make-closed-segment :name "closed/segments/2026-09-14-s1.sexp"
                                   :entries (list (make-closed-entry :rev 1 :id "T-1" :stamp "2026-09-14T12:00:00Z"))))
         (man (manifest-for-day "2026-09-14" (list seg)))
         (table (list (cons (seg-name seg) seg))))
    (multiple-value-bind (rows gap notes)
        (closed-day-selection '("2026-09-13" "2026-09-14") (list man) table)
      (check-equal 1 (length rows) "the absent day's neighbour lost its rows")
      (check-equal 0 gap "an absent day was reported as a coverage gap")
      (check-equal '() notes "an absent day produced a coverage-gap note"))))

;;; ------------------------------------------------------------------
;;; A missing segment is a gap. SPEC-WORK.md:641-645, replay missing-segment-is-a-gap
;;; ------------------------------------------------------------------

(deftest "missing-segment-is-a-gap" "docs/SPEC-WORK.md:643"
    "expected=missing-segment-prints-coverage-gap,never-an-empty-closed-set"
  (let* ((seg (make-closed-segment :name "closed/segments/2026-09-14-s1.sexp"
                                   :entries (list (make-closed-entry :rev 1 :id "T-1" :stamp "2026-09-14T12:00:00Z"))))
         (man (manifest-for-day "2026-09-14" (list seg)))
         (table '()))  ; the manifest names a segment the disk does not hold
    (multiple-value-bind (rows gap notes)
        (closed-day-selection '("2026-09-14") (list man) table)
      (check-equal 0 (length rows) "a missing segment was answered as rows")
      (check-equal 1 gap "a missing segment was not counted as a coverage gap")
      (check-equal 1 (length notes) "a missing segment printed no note")
      (ok (search "QUERY NOTE coverage-gap" (first notes)) "the note is not a coverage-gap note"))))

;;; ------------------------------------------------------------------
;;; As-of refuses an unavailable partition. SPEC-WORK.md:645-649, replay as-of-refuses-unavailable-partition
;;; ------------------------------------------------------------------

(deftest "as-of-refuses-unavailable-partition" "docs/SPEC-WORK.md:645"
    "expected=refusal-names-the-one-partition-it-could-not-read"
  (let ((line (as-of-refusal "closed" "2026-09-14T23:00:00Z" "2026-09-14")))
    (ok (search "QUERY FAIL" line) "the as-of refusal is not a QUERY FAIL")
    (ok (search "as-of=2026-09-14T23:00:00Z" line) "the refusal does not name the window end")
    (ok (search "partition=2026-09-14" line) "the refusal does not name the unreadable partition")
    (ok (search "historical window unavailable" line) "the refusal does not say what failed")))

;;; ------------------------------------------------------------------
;;; A clip names the index that overflowed. SPEC-WORK.md:650-662, replay clip-names-the-index-that-overflowed
;;; ------------------------------------------------------------------

(deftest "clip-names-the-index-that-overflowed" "docs/SPEC-WORK.md:650"
    "expected=all-four-numbers-printed-and-the-remedy-names-a-flag-that-moves"
  (let ((line (clip-overflow-line 90 40 55 50 100)))
    (ok (search "snapshot=90" line) "the refusal does not name the whole snapshot bytes")
    (ok (search "retained=40" line) "the refusal does not name the retained bytes")
    (ok (search "index=55" line) "the refusal does not name the dedup index bytes")
    (ok (search "closed-index=50" line) "the refusal does not name the closed index bytes")
    (ok (search "past --max-bytes=100" line) "the refusal does not name the bound")
    (ok (search "lower --retain or raise --max-bytes" line) "the remedy names no flag that moves")))

;;; ------------------------------------------------------------------
;;; A dedup page unavailable refuses. SPEC-WORK.md:516-518, replay dedup-page-unavailable-refuses
;;; ------------------------------------------------------------------

(deftest "dedup-page-unavailable-refuses" "docs/SPEC-WORK.md:517"
    "expected=refusal-and-never-evidence-that-the-request-is-new"
  (let* ((page (make-page :name "dedup/pages/req-9.sexp" :kind :dedup
                          :records 1 :bytes 40 :entries '(("req-9" "digest0" "STATE OK ..."))
                          :available nil))
         (index (make-dedup-index :pages (list page))))
    (multiple-value-bind (found err page-name) (dedup-lookup index "req-9" "digest0")
      (declare (ignore found))
      (check-equal :unavailable err "an unreadable dedup page did answer")
      (let ((line (dedup-unavailable-line "STATE" "req-9" page-name)))
        (ok (search "STATE FAIL request=req-9" line) "the refusal is not a mutation FAIL")
        (ok (search "dedup unavailable" line) "the refusal does not say dedup unavailable")))))

;;; ------------------------------------------------------------------
;;; The page budget is not --max. SPEC-WORK.md:569-575, replay page-budget-is-not-max
;;; ------------------------------------------------------------------

(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:569"
    "expected=budget-met-before-a-row-emits-prints-QUERY-MORE-shown=0"
  (let* ((pages (loop for i from 1 to 5
                      collect (make-page :name (format nil "closed/pages/p~D.sexp" i)
                                         :kind :closed :records 3 :bytes 120
                                         :entries (loop for j from 1 to 3
                                                        collect (format nil "T-~D" (+ (* i 10) j))))))
         (filter (lambda (e) (declare (ignore e)) nil))  ; rejects every row it reads
         )
    (multiple-value-bind (kind pages-read cursor) (budget-limited-query pages filter 3)
      (check-equal :more kind "a budget met before any row did not answer MORE")
      (check-equal 3 pages-read "the pages read did not hit the budget")
      (ok (stringp cursor) "the continuation cursor is not present")
      (let ((line (query-more-line 0 0 3 cursor)))
        (ok (search "QUERY MORE" line) "the continuation is not a QUERY MORE")
        (ok (search "shown=0" line) "a filtered-but-empty answer did not print shown=0")
        (ok (search "pages=3" line) "the MORE line does not carry the pages read")))))

;;; ------------------------------------------------------------------
;;; The overlay is bounded and rebuilt. SPEC-WORK.md:629-638, replay overlay-is-bounded-and-rebuilt
;;; ------------------------------------------------------------------

(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:629"
    "expected=rebuilt-from-the-journal-and-never-replayed-per-query"
  (let* ((events (loop for i from 1 to 120
                       collect (list :kind :settle :node (format nil "n-~D" i) :rev i)))
         (index-cache 8)
         (page-records 25)
         (ov (replay-overlay events index-cache page-records)))
    ;; The overlay is held in bounded paged scratch under the index cache.
    (check-equal 5 (ov-pages ov) "the overlay pages are not ceil(entries/page-records)")
    (ok (<= (ov-pages ov) index-cache) "the overlay pages exceed the index cache")
    (check-equal 120 (length (ov-entries ov)) "rebuilding lost an overlay entry")
    ;; Rebuilding reproduces the same overlay deterministically, from the journal.
    (let ((again (replay-overlay events index-cache page-records)))
      (check-equal (ov-entries ov) (ov-entries again) "a rebuild drifted from the journal"))))

;;; ------------------------------------------------------------------
;;; As-of reconstructs settle, revive, settle. SPEC-WORK.md:539-551, replay as-of-reconstructs-settle-revive-settle
;;; ------------------------------------------------------------------

(deftest "as-of-reconstructs-settle-revive-settle" "docs/SPEC-WORK.md:539"
    "expected=settles=2,revived=- on the newest row,earlier-rows-unchanged"
  (let* ((id "acme/work/f1/t1")
         (mk (lambda (kind rev)
               (make-work-event :kind kind :node id :by "rowan" :clock :tool
                                :request "req-p" :generation-owner "gen-4" :rev rev
                                :stamp "2026-09-14T12:00:00Z" :session-written-p t
                                :fields (if (eq kind :settle)
                                            '(:disposition :done :reason "a")
                                            '(:reason "b")))))
         (events (list (funcall mk :settle 1)
                       (funcall mk :revive 2)
                       (funcall mk :settle 3))))
    (let ((rows (as-of-closed-rows *seed* events 3)))
      (check-equal 3 (length rows) "as-of reconstruction lost a row")
      (check-equal 2 (getf (first (last rows)) :settles) "the newest row did not count twice")
      (check-string= "-" (getf (first (last rows)) :revived) "the second settle retained a revived="))
    ;; Reconstruct only as-of the revise: the second settle has not happened.
    (let ((rows (as-of-closed-rows *seed* events 2)))
      (check-equal 2 (length rows) "as-of the revive includes the later settle")
      (check-equal :revive (getf (first (last rows)) :kind) "the newest row as-of rev 2 is not the revive"))))
