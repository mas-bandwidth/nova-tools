;;;; acceptance.lisp --- the five slice-1 cases.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md at
;;;; 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64 it comes from, and carries either
;;;; the acceptance table's own expected= string or the executable invariant the
;;;; table names where that row has none.

(in-package #:nova-work/tests)

(defparameter *seed*
  '((:id "acme/work"       :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/f1"    :type :feature  :parent "acme/work"    :state :unknown)
    (:id "acme/work/f1/t1" :type :task     :parent "acme/work/f1" :state :doing
     :links ("https://github.com/acme/work/issues/11"
             "https://github.com/acme/work/issues/12"))
    (:id "acme/work/f1/t2" :type :task     :parent "acme/work/f1" :state :review
     :links ("https://github.com/acme/work/issues/21"))
    (:id "acme/work/f2"    :type :feature  :parent "acme/work"    :state :unknown))
  "Five canonical item ids, all in O at the seed: |O| = 5, of which two are
leaf tasks and three are open linked issues (SPEC-WORK.md:1577 keeps the three
counters apart).")

(defparameter *cyclic-seed*
  '((:id "a" :type :task :parent "b" :state :doing)
    (:id "b" :type :task :parent "a" :state :doing))
  "a -> b -> a. SPEC-WORK.md:3347 referential-integrity: cycles fail BEFORE
publication.")

(defun fresh (&key (journal (make-ordering-journal)) (rev-base 1))
  (make-kernel :state (make-seed-state *seed*) :journal journal :rev-base rev-base))

(defun close-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "shipped")
                           (evidence '("ev-1")) (request "req-1")
                           (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :state-to-done :node node :by by :reason reason :evidence evidence
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun reopen-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "regressed")
                            (request "req-2") (stamp "2026-09-14T13:00:00Z")
                            (generation-owner "gen-4"))
  (list :verb :event-reopen :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun independent-open-count (state)
  "Walk every node and count the ones in O. This is the full count the counter
is compared against; it is never the path `query --ask size` takes."
  (let ((n 0))
    (dolist (node *seed*)
      (when (eq :o (node-branch state (getf node :id))) (incf n)))
    n))

;;;; acceptance.lisp --- loader for the per-slice replay files (nova-tools #560).
;;;;
;;;; One file per replay slice so parallel PRs stop conflicting: each slice
;;;; lives in acceptance/<slice>.lisp and this top file only loads them.
;;;; An amendment edits one slice file, never this loader and never two
;;;; slices at once. The shared seed and request helpers stay here so every
;;;; slice sees the same canonical fixtures.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (unless (find-package :asdf)
    (require :asdf)))

;; The slice files, in canonical order. Each is one replay slice; an
;; amendment edits one of them (nova-tools #560).
(defparameter *acceptance-slices*
  '("slice-01-reader.lisp"
    "slice-02-close-and-counters.lisp"
    "slice-03-containers.lisp"
    "slice-04-doing-and-journal.lisp"
    "slice-05-durable-journal.lisp"
    "slice-06-replays-early.lisp"
    "slice-07-replays-mid.lisp"
    "slice-08-replays-late.lisp"
    "slice-09-replays-publication.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-10-fleet.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
;;;; The closed-history replays of docs/SPEC-WORK.md:545-735 and
;;;; :5545-5580 (Go card 8601). One pure closed-history model carries
;;;; them: day partitions, a revision merge across days, bounded segment
;;;; pages, the rolling two-day default window and the page budget that
;;;; `--max` is not.
;;;; ------------------------------------------------------------------

(defun history-row (day revision id &optional (repo "acme/work"))
  "One closed-history row: its recorded UTC day and its revision."
  (list :day day :revision revision :id id :repo repo))

(deftest "days-merge-by-revision-never-concatenate" "docs/SPEC-WORK.md:5998"
    "expected=backdated-closure-in-earlier-day;from-to-prints-revision-order-across-boundary;one-batch-and-ten-yield-identical-leaves"
  (let* ((rows (list (history-row "2026-09-14" 100 "T-100")
                     (history-row "2026-09-15" 50  "T-50")
                     (history-row "2026-09-14" 200 "T-200")))
         (h (make-closed-history :rows rows :page-records 2 :root "rev-root-1")))
    ;; a closure backdated into an earlier day keeps its row in that day.
    (let ((backdated (find 200 (merge-days-by-revision h '("2026-09-14" "2026-09-15"))
                           :key (lambda (r) (getf r :revision)))))
      (check-equal "2026-09-14" (getf backdated :day)
                   "the backdated closure stays in its recorded day"))
    ;; a --from/--to over both days prints rows in revision order across the
    ;; day boundary, never one day concatenated after the other.
    (check-equal '(50 100 200)
                 (mapcar (lambda (r) (getf r :revision))
                         (merge-days-by-revision h '("2026-09-14" "2026-09-15")))
                 "rows merge by revision across the day boundary")
    ;; the same day clipped in one batch and in ten yields identical leaves.
    (let ((one (clip-day rows 2))
          (ten (clip-day-batched (list (subseq rows 0 1)
                                       (subseq rows 1 2)
                                       (subseq rows 2 3))
                                 2)))
      (check-equal one ten "one batch and ten yield the same leaves")
      (check-equal one (clip-day (reverse rows) 2)
                   "clip order does not change the leaves"))))

(deftest "page-budget-is-not-max" "docs/SPEC-WORK.md:6001"
    "expected=shown=0;pages=<n>;whole-history-never-scanned;max-bounds-rows-not-pages;continuation-same-root;other-root-page-expired"
  (let* ((rows (loop for i from 1 to 40
                     collect (history-row "2026-09-14" i (format nil "T-~D" i))))
         (h (make-closed-history :rows rows :page-records 4 :root "root-1"))
         (leaves (length (clip-day rows 4)))
         (reject (constantly nil))
         (res (query-history h :from "2026-09-14" :to "2026-09-15"
                             :filter reject :page-budget 3 :max 5)))
    ;; the filter rejects every row read: shown=0 at the budget.
    (check-equal 0 (length (query-result-shown res)) "shown=0")
    (check-equal 3 (query-result-pages res) "pages=<n> is the budget")
    (ok (query-result-more res) "the answer is MORE")
    (ok (search "shown=0" (query-result-line res)) "the line prints shown=0")
    (ok (search "pages=3" (query-result-line res)) "the line prints pages=<n>")
    ;; the whole history is never scanned.
    (ok (< (query-result-pages res) leaves)
        "only the budgeted pages are read, not the whole history")
    ;; --max caps the rows printed and bounds nothing else: a tiny --max does
    ;; not move the pages the budget read.
    (let ((small-max (query-history h :from "2026-09-14" :to "2026-09-15"
                                    :filter reject :page-budget 3 :max 1)))
      (check-equal 3 (query-result-pages small-max)
                   "--max bounds the rows, never the pages")
      (check-equal 0 (length (query-result-shown small-max))
                   "a rejected filter prints no row even under --max"))
    ;; the continuation answers from the same captured root.
    (let ((cont (continue-query h (query-result-cursor res)
                                :filter reject :page-budget 3)))
      (check-equal 3 (query-result-pages cont)
                   "the continuation reads the next budgeted pages"))
    ;; a continuation against a different root is refused page expired.
    (let ((other (make-closed-history :rows rows :page-records 4 :root "root-2")))
      (ok (search "page expired"
                  (query-result-line (continue-query other (query-result-cursor res))))
          "a continuation on another root is refused page expired"))))

(deftest "default-window-opens-two-days" "docs/SPEC-WORK.md:5561"
    "expected=at-most-two-day-partitions;midnight=one;no-partition-older-than-window;--from=only-days-holding-records"
  (let* ((rows (list (history-row "2026-09-13" 1 "T-old")
                     (history-row "2026-09-14" 2 "T-y")
                     (history-row "2026-09-15" 3 "T-t")))
         (h (make-closed-history :rows rows :page-records 1 :root "win-root")))
    ;; at early morning and at midday the rolling [now-24h, now) opens the two
    ;; UTC day partitions it intersects, today's and yesterday's.
    (dolist (now '("2026-09-15T03:00:00Z" "2026-09-15T12:00:00Z"))
      (let ((days (window-days h :now now)))
        (check-equal '("2026-09-14" "2026-09-15") days
                     "the default window opens today and yesterday")
        (ok (<= (length days) 2) "at most two day partitions")))
    ;; at exactly 00:00:00Z the interval is yesterday's whole day: one partition.
    (check-equal '("2026-09-14") (window-days h :now "2026-09-15T00:00:00Z")
                 "at midnight the window opens exactly one partition")
    ;; no partition older than the window is opened for it.
    (ok (every (lambda (d) (string>= d "2026-09-14"))
               (window-days h :now "2026-09-15T12:00:00Z"))
        "no partition older than the window is opened")
    ;; an explicit --from reaching back a month opens exactly the days in range
    ;; that hold closure records, and prints pages= for them.
    (let ((days (window-days h :from "2026-08-15" :to "2026-09-16")))
      (check-equal '("2026-09-13" "2026-09-14" "2026-09-15") days
                   "--from opens exactly the days holding closure records")
      (check-equal (length days)
                   (query-result-pages
                    (query-history h :from "2026-08-15" :to "2026-09-16"))
                   "the historical listing prints pages= for the days opened"))))

(deftest "busy-day-many-segments" "docs/SPEC-WORK.md:5566"
    "expected=many-segments-read-in-bounded-pages;max-caps-rows;more-names-after;day-never-read-whole"
  (let* ((rows (loop for i from 1 to 50
                     collect (history-row "2026-09-14" i (format nil "T-~3,'0D" i))))
         (h (make-closed-history :rows rows :page-records 5 :root "busy-root"))
         (total-leaves (length (clip-day rows 5))))
    ;; --max caps the rows printed.
    (let ((res (query-history h :from "2026-09-14" :to "2026-09-15" :max 12)))
      (check-equal 12 (length (query-result-shown res)) "--max caps the rows")
      (ok (query-result-more res) "a capped answer is MORE")
      (ok (search "after=" (query-result-line res)) "MORE names --after")
      (ok (< (query-result-pages res) total-leaves) "the day is never read whole"))
    ;; the bounded pages stop at the page budget.
    (let ((res (query-history h :from "2026-09-14" :to "2026-09-15"
                              :max 100 :page-budget 4)))
      (check-equal 20 (length (query-result-shown res))
                   "four bounded pages of five rows print twenty rows")
      (check-equal 4 (query-result-pages res) "the read is bounded by the budget")
      (ok (query-result-more res) "the rest of the day is MORE")
      (ok (< (query-result-pages res) total-leaves) "the day is never read whole"))))

(deftest "history-grows-startup-does-not" "docs/SPEC-WORK.md:5568"
    "expected=startup-resident-bytes-flat;segment-bytes-read-flat;parses-flat;replays-flat;emitted-bytes-flat;pages-bounded-by-depth;no-whole-C-load;no-directory-scan;no-whole-dedup-load"
  (flet ((history (old-days)
           (make-closed-history
            :rows (append
                   (loop for i from 1 to 12
                         collect (history-row "2026-09-14" i (format nil "T-w~D" i)))
                   (loop for d from 1 to old-days
                         collect (history-row
                                  (format nil "2020-01-~2,'0D" (1+ (mod (1- d) 28)))
                                  d (format nil "T-old~D" d))))
            :page-records 4 :root "grow-root")))
    (let ((small (history-cost (history 0) :now "2026-09-15T12:00:00Z"))
          (huge  (history-cost (history 5000) :now "2026-09-15T12:00:00Z")))
      ;; O, the recent-window volume and the page bounds held fixed while the old
      ;; history grows by orders of magnitude: these five do not move.
      (check-equal (history-cost-startup-resident-bytes small)
                   (history-cost-startup-resident-bytes huge)
                   "startup resident bytes do not move")
      (check-equal (history-cost-segment-bytes-read small)
                   (history-cost-segment-bytes-read huge)
                   "segment bytes read do not move")
      (check-equal (history-cost-parses small) (history-cost-parses huge)
                   "parses do not move")
      (check-equal (history-cost-replays small) (history-cost-replays huge)
                   "replays do not move")
      (check-equal (history-cost-emitted-bytes small)
                   (history-cost-emitted-bytes huge)
                   "emitted bytes do not move")
      ;; index pages read stay bounded by the index depth.
      (ok (<= (history-cost-index-pages-read huge) (index-depth huge))
          "index pages read stay bounded by the index depth")
      ;; no whole-C load, no directory scan, no whole-history dedup load.
      (ok (< (history-cost-segment-bytes-read huge) 5012)
          "no whole-C load on the ordinary path")
      (check-equal 0 (history-cost-scanned-days huge) "no directory scan")
      (check-equal 0 (history-cost-dedup-loads huge)
                   "no whole-history dedup load"))))
