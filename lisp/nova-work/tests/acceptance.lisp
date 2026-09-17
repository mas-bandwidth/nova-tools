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
    "slice-09-replays-8603.lisp"
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
;;;; CARD-8605 / #362 -- the five named acceptance replays. They live here
;;;; because the card names this file; the slice files keep every other
;;;; replay. Each sets up the state its paragraph describes, drives the
;;;; kernel through its verbs, and asserts the outcome the paragraph
;;;; promises.
;;;; ------------------------------------------------------------------

(defun independent-working-count (state)
  "Walk every node and count the O ones holding a live lease. W is this view,
never a stored branch; the counter must agree with the walk."
  (let ((n 0))
    (dolist (node *seed*)
      (when (and (eq :o (node-branch state (getf node :id)))
                 (node-holder state (getf node :id)))
        (incf n)))
    n))

(deftest "reopen-revives" "docs/SPEC-WORK.md:1584-1586,5062"
    "expected=open+1-closed-1;todo;revive-written"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "rr-1")) "close refused")
    (check-equal 4 (state-open-count (kernel-state k)) "open after settle")
    (check-equal 1 (state-closed-count (kernel-state k)) "closed after settle")
    (multiple-value-bind (okp line code envelope) (submit k (reopen-request :request "rr-2"))
      (declare (ignore line code))
      (ok okp "reopen refused")
      (check-equal :reopen (work-event-kind (first (getf envelope :events))) "requester kind")
      (check-equal :revive (work-event-kind (second (getf envelope :events))) "session kind"))
    (check-equal :todo (node-state (kernel-state k) "acme/work/f1/t1") "reopened lands in :todo")
    (check-equal :o (node-branch (kernel-state k) "acme/work/f1/t1") "reopened is back in O")
    (check-equal 5 (state-open-count (kernel-state k)) "open +1 after the revive")
    (check-equal 0 (state-closed-count (kernel-state k)) "closed -1 after the revive")))

(deftest "roadmap-outlives-its-work" "docs/SPEC-WORK.md:1648-1664"
    "expected=roadmap-view-retained-across-settle"
  (let* ((seed '((:id "r"       :type :roadmap :parent nil   :state :unknown)
                 (:id "r/f"     :type :feature :parent "r"   :state :unknown)
                 (:id "r/f/t"   :type :task    :parent "r/f" :state :doing
                  :links ("https://github.com/acme/work/issues/31"))))
         (k (make-kernel :state (make-seed-state seed))))
    ;; The roadmap settles with its members, like any container.
    (ok (submit k (close-request :node "r/f/t" :request "rol-1")) "close refused")
    (check-equal :c (node-branch (kernel-state k) "r/f/t") "the task settled")
    (check-equal :c (node-branch (kernel-state k) "r/f") "the feature settled with its member")
    (check-equal :c (node-branch (kernel-state k) "r") "the roadmap settled with its feature")
    ;; Its named view record is retained whatever branch the roadmap is in.
    (check-equal '("r/f") (roadmap-members (kernel-state k) "r")
                 "the roadmap keeps its row")
    (let ((view (roadmap-open k "r")))
      (check-equal "r" (getf view :id) "the view names the roadmap")
      (check-equal '("r/f") (getf view :members) "the retained row is returned")
      (ok (find "r/f/t" (getf view :rows) :key (lambda (r) (getf r :node)) :test #'equal)
          "the closed member's row is read from the retained record"))))

(deftest "roadmap-opened-after-the-window" "docs/SPEC-WORK.md:1648-1664"
    "expected=opening-a-named-roadmap-is-never-narrowed-by-the-default-window"
  (let* ((seed '((:id "r"     :type :roadmap :parent nil :state :unknown)
                 (:id "r/f"   :type :feature :parent "r"   :state :unknown)
                 (:id "r/f/t" :type :task    :parent "r/f" :state :doing
                  :links ("https://github.com/acme/work/issues/41"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "r/f/t" :request "roatw-1")) "close refused")
    ;; The settle stamp is 2026-09-14; a read whose window ended long before it
    ;; still opens the named view with the member's row, because the default
    ;; [now - 24h, now) window bounds a closed-activity listing and not a named
    ;; view (read: the kernel carries no clock, so the window is inert and the
    ;; view is never narrowed by it).
    (let ((unwindowed (roadmap-open k "r"))
          (early (roadmap-open k "r" :window "2000-01-01T00:00:00Z")))
      (check-equal unwindowed early "the window did not narrow the named view")
      (ok (find "r/f/t" (getf early :rows) :key (lambda (r) (getf r :node)) :test #'equal)
          "the historical row is still in the table"))))

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:1674-1680,5055"
    "expected=settled-item-reads-holder-unowned"
  (let ((k (fresh)))
    (take-lease k "acme/work/f1/t1" "emma")
    (check-equal "emma" (node-holder (kernel-state k) "acme/work/f1/t1") "held by emma")
    ;; One live lease per node; a second take is refused and names the holder.
    (let ((refused nil))
      (handler-case (take-lease k "acme/work/f1/t1" "sam")
        (unsupported-input () (setf refused t)))
      (ok refused "a second take was accepted"))
    ;; A release from a third name is refused too.
    (let ((refused nil))
      (handler-case (release-lease k "acme/work/f1/t1" "sam")
        (unsupported-input () (setf refused t)))
      (ok refused "a third name ended the claim"))
    (ok (submit k (close-request :request "srl-1" :by "rowan")) "close refused")
    (check-equal :c (node-branch (kernel-state k) "acme/work/f1/t1") "the item settled")
    (check-equal nil (node-holder (kernel-state k) "acme/work/f1/t1")
                 "a settled item reads holder=unowned")
    (let ((rel (first (state-lease-log (kernel-state k)))))
      (check-equal :release (getf rel :kind) "the settle wrote a release")
      (check-equal "rowan" (getf rel :by) "the settling author wrote it")
      (check-equal "emma" (getf rel :holder) "it names the holder it ended"))))

(deftest "working-is-a-view" "docs/SPEC-WORK.md:1682-1689,5082"
    "expected=w-subset-o-no-verb-writes-w"
  (let ((k (fresh)))
    (check-equal 5 (state-open-count (kernel-state k)) "seed |O|")
    (check-equal 0 (working-count k) "|W| starts at zero")
    (check-equal :pending (node-disposition (kernel-state k) "acme/work/f1/t1")
                 "a :doing item with no live lease is pending")
    (take-lease k "acme/work/f1/t1" "emma")
    (take-lease k "acme/work/f1/t2" "sam")
    (check-equal 2 (working-count k) "two live leases")
    (check-equal :working (node-disposition (kernel-state k) "acme/work/f1/t1")
                 "a leased open item is working")
    (ok (<= (working-count k) (state-open-count (kernel-state k))) "|W| <= |O|")
    ;; W is a view: an independent walk agrees and no slot writes it.
    (check-equal (independent-working-count (kernel-state k)) (working-count k)
                 "W is the walk's own count")
    (ok (null (find-symbol "WSTATE-WORKING" :nova-work)) "no verb writes W")
    (release-lease k "acme/work/f1/t1" "emma")
    (check-equal 1 (working-count k) "release shrinks W")
    ;; A settled item leaves O and so leaves W; it is done, not working.
    (ok (submit k (close-request :node "acme/work/f1/t2" :request "wiv-1" :by "sam"))
        "close of a leased item refused")
    (check-equal 0 (working-count k) "the settled item left W")
    (check-equal :done (node-disposition (kernel-state k) "acme/work/f1/t2") "settled is done")
    (ok (<= (working-count k) (state-open-count (kernel-state k))) "|W| <= |O| after settle")))
;;;; The root's named COW replays and the closed-history replays
;;;; (docs/SPEC-WORK.md:5492-5543, 5592). Each drives the kernel through
;;;; its verbs; the read side is src/closed-history.lisp.
;;;; ------------------------------------------------------------------

(defun settled-kernel (n &key spare)
  "A kernel over N settled top-level tasks (and one spare open task when SPARE),
so a later settle has a node to move."
  (let* ((count (+ n (if spare 1 0)))
         (seed (loop for i from 1 to count
                     collect (list :id (format nil "t~D" i) :type :task
                                   :parent nil :state :doing)))
         (state (make-seed-state seed)))
    (loop for i from 1 to n
          do (nova-work::apply-event
              state
              (make-work-event :kind :settle :node (format nil "t~D" i)
                               :by "rowan"
                               :fields (list :disposition :done :reason "shipped"
                                             :already-closed '())
                               :stamp "2026-09-14T12:00:00Z" :clock :tool
                               :request (format nil "seed-~D" i)
                               :generation-owner "gen-4" :rev i
                               :session-written-p t)))
    (make-kernel :state state)))

(defun closed-four-kernel ()
  "Four top-level tasks settled through submit: the closed index holds four
rows, the worked acceptance's four ids (SPEC-WORK.md:5550)."
  (let* ((seed (loop for i from 1 to 4
                     collect (list :id (format nil "c~D" i) :type :task
                                   :parent nil :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (dotimes (i 4)
      (multiple-value-bind (okp line code)
          (submit k (list :verb :state-to-done :node (format nil "c~D" (1+ i))
                          :by "rowan" :reason "shipped" :evidence '("ev-1")
                          :request (format nil "ar-~D" i)
                          :stamp "2026-09-14T12:00:00Z" :clock :tool
                          :generation-owner "gen-4"))
        (declare (ignore code))
        (unless okp (error "close ~D refused: ~A" i line))))
    k))

(defun instrumented-closed-page (state &rest args)
  "CLOSED-PAGE measured: (values ROWS MORE CURSOR LINE PARSES REPLAYS VISITS)."
  (let ((captured '()))
    (with-instrumentation
      (multiple-value-bind (rows more cursor line)
          (apply #'nova-work::closed-page state args)
        (setf captured (list rows more cursor line *parses* *replays* *visits*))))
    (values-list captured)))

(deftest "closed-row-with-archive-absent" "docs/SPEC-WORK.md:5541"
    "expected=same-four-rows;gap=;QUERY-NOTE-coverage-gap"
  (let* ((k (closed-four-kernel))
         (state (kernel-state k)))
    (multiple-value-bind (rows line) (nova-work::closed-ask state :archive nil)
      (check-equal 4 (length rows) "the four index rows answer with the archive absent")
      (ok (search "gap=0" line) "an unreached body is not a gap: ~A" line))
    (multiple-value-bind (rows line)
        (nova-work::closed-ask state :archive nil :reach-bodies t)
      (check-equal 4 (length rows) "still four rows, never a shorter list")
      (ok (search "gap=4" line) "gap counts the missing bodies: ~A" line)
      (ok (search "QUERY NOTE coverage-gap" line)
          "one coverage-gap note, not a shorter list: ~A" line))))

(deftest "closed-paged-without-full-load" "docs/SPEC-WORK.md:5538"
    "expected=shown=20;MORE;after=;parses=0;replays=0;history-never-loaded"
  (let* ((k (settled-kernel 1000 :spare t))
         (state (kernel-state k)))
    (multiple-value-bind (rows more cursor line parses replays visits)
        (instrumented-closed-page state :max 20)
      (check-equal 20 (length rows) "the first page reads twenty rows")
      (ok more "the first page prints MORE")
      (check-equal 1000 (nova-work::closed-cursor-rev cursor) "the cursor pins rev 1000")
      (ok (search "after=" line) "MORE names --after: ~A" line)
      (ok (search "parses=0 replays=0" line) "the page reads and replays nothing: ~A" line)
      (check-equal 0 parses "no parse")
      (check-equal 0 replays "no replay")
      (check-equal 0 visits "no node visited: the whole history was never loaded")
      (multiple-value-bind (rows2 more2 cursor2 line2)
          (instrumented-closed-page state :max 20 :cursor cursor)
        (declare (ignore more2 cursor2))
        (check-equal 20 (length rows2) "the next page reads the next twenty")
        (let ((k1 (mapcar (lambda (r) (getf r :key)) rows))
              (k2 (mapcar (lambda (r) (getf r :key)) rows2)))
          (ok (null (intersection k1 k2 :test #'string=)) "no row twice across pages")
          (check-string= "981:t981" (car (last k1)) "the first page ends at rev 981")
          (check-string= "980:t980" (car k2) "the second page starts at the next row"))
        (ok (search "parses=0 replays=0" line2)
            "the second page reads and replays nothing")))))

(deftest "branch-and-window-required" "docs/SPEC-WORK.md:5546"
    "expected=branch-required;closed-window-required;open-refuses-from;who/stale/handoffs-refused"
  (multiple-value-bind (okp line code) (nova-work::validate-ask :ask :size)
    (ok (not okp) "an ask with no branch refuses")
    (check-equal 2 code "exit 2")
    (ok (search "branch" line) "the refusal names the flag: ~A" line))
  (multiple-value-bind (okp line code)
      (nova-work::validate-ask :ask :size :branch :closed)
    (ok (not okp) "closed with no window refuses")
    (check-equal 2 code "exit 2")
    (ok (search "from" line) "the refusal names the window: ~A" line))
  (multiple-value-bind (okp line code)
      (nova-work::validate-ask :ask :size :branch :open :from 1)
    (ok (not okp) "a window under open refuses")
    (check-equal 2 code "exit 2"))
  (dolist (ask '(:who :stale :handoffs))
    (dolist (branch '(:closed :root))
      (multiple-value-bind (okp line code)
          (nova-work::validate-ask :ask ask :branch branch)
        (declare (ignore line))
        (ok (not okp) "~A under ~A refuses" ask branch)
        (check-equal 2 code "exit 2"))))
  (multiple-value-bind (okp line code)
      (nova-work::validate-ask :ask :size :branch :closed :from 1 :to 2)
    (ok okp "a closed ask with its window is admitted: ~A" line)
    (check-equal 0 code "exit 0")))

(deftest "cow-root-partition" "docs/SPEC-WORK.md:5495"
    "expected=one-id-in-C-or-O;open+closed=total;rule-18-at-load-and-candidate-gate"
  (let ((k (fresh)))
    (ok (nova-work::cow-partition-holds-p (kernel-state k)) "the seed is a partition")
    (check-equal 5 (+ (state-open-count (kernel-state k))
                      (state-closed-count (kernel-state k)))
                 "open+closed is the counted total")
    (dolist (node '("acme/work/f1/t1" "acme/work/f1/t2"))
      (multiple-value-bind (okp line code)
          (submit k (close-request :node node
                                   :request (format nil "cow-~A" node)))
        (ok okp "close ~A refused: ~A" node line)
        (check-equal 0 code "close exit")
        (ok (nova-work::cow-partition-holds-p (kernel-state k))
            "the partition holds after closing ~A" node)
        (check-equal 5 (+ (state-open-count (kernel-state k))
                          (state-closed-count (kernel-state k)))
                     "open+closed stays the counted total")))
    (let ((bad (make-seed-state *seed*)))
      (nova-work::hand-write-closed-row bad "acme/work/f2" 99)
      (ok (nova-work::cow-load-findings bad)
          "the hand-written double membership is found")
      (multiple-value-bind (okp line code) (nova-work::cow-candidate-gate bad)
        (ok (not okp) "the candidate gate refuses it")
        (check-equal 1 code "the candidate gate exits 1")
        (ok (search "rule 18" line) "the finding names rule 18: ~A" line)))))

(deftest "cursor-pinned-across-a-new-settle" "docs/SPEC-WORK.md:5592"
    "expected=no-row-missing;no-row-twice;pinned-revision;page-expired"
  (let* ((k (settled-kernel 50 :spare t))
         (state (kernel-state k)))
    (multiple-value-bind (rows more cursor line)
        (instrumented-closed-page state :max 20)
      (declare (ignore line))
      (check-equal 20 (length rows) "the first page reads twenty rows")
      (ok more "the first page prints MORE")
      (check-equal 50 (nova-work::closed-cursor-rev cursor) "the cursor pins rev 50")
      (multiple-value-bind (okp l c)
          (submit k (list :verb :state-to-done :node "t51" :by "rowan"
                          :reason "shipped" :evidence '("ev-1")
                          :request "between-pages"
                          :stamp "2026-09-14T12:00:00Z" :clock :tool
                          :generation-owner "gen-4"))
        (declare (ignore c))
        (ok okp "the second settle applies: ~A" l)
        (check-equal 52 (state-revision (kernel-state k))
                     "the close envelope (transition + session settle) moved the revision"))
      (multiple-value-bind (rows2 more2 cursor2 line2)
          (instrumented-closed-page state :max 20 :cursor cursor)
        (declare (ignore more2))
        (let ((k1 (mapcar (lambda (r) (getf r :key)) rows))
              (k2 (mapcar (lambda (r) (getf r :key)) rows2)))
          (ok (null (intersection k1 k2 :test #'string=)) "no row twice")
          (ok (null (find "t51" rows2 :key (lambda (r) (getf r :node))
                          :test #'string=))
              "the item settled between pages is not in the pinned page")
          (check-string= "31:t31" (car (last k1)) "the first page ends at rev 31")
          (check-string= "30:t30" (car k2) "the second page continues with no row missing"))
        (check-equal 50 (nova-work::closed-cursor-rev cursor2) "the cursor stays pinned")
        (ok (search "parses=0 replays=0" line2) "the continuation replays nothing"))
      (multiple-value-bind (rows3 more3 cursor3 line3)
          (instrumented-closed-page state :max 20 :cursor cursor :floor 51)
        (declare (ignore more3 cursor3))
        (ok (null rows3) "an expired continuation answers no rows")
        (ok (search "page expired" line3) "the refusal names page expired: ~A" line3)))))
