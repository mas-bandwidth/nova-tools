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
;;;; The wire, protocol-version, disconnect, pipeline-correlation and
;;;; long-operation replays (nova-tools card 8608). The slice files keep
;;;; the names as placeholders; these are the executable paragraphs.
;;;; ------------------------------------------------------------------

(deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
    "expected=bignum-fields-round-trip-exact;json-number-frame-refused;null-and-absent-alike;empty-string-and-array-are-values"
  ;; An id, a revision, a counter and a token total, each above 2^53, cross
  ;; the wire and return unchanged (SPEC-WORK.md:2663-2666).
  (dolist (n '(9007199254740993 9007199254740995 9007199254740997 9007199254740999))
    (let ((wire (wire-encode-integer n)))
      (ok (stringp wire) "the integer ~D is a JSON string, not a number" n)
      (check-string= (format nil "\"~D\"" n) wire "exact decimal digits")
      (check-equal n (wire-decode-value wire) "round-trip unchanged")))
  ;; A frame carrying a JSON number is refused.
  (let ((refused nil))
    (handler-case (wire-decode-value "9007199254740993")
      (unsupported-input () (setf refused t)))
    (ok refused "a frame carrying a bare JSON number is refused"))
  ;; null and an absent key read alike; an empty string and an empty array
  ;; are values (:2669-2672).
  (check-equal +absent+ (wire-decode-value "null") "null reads as not given")
  (check-equal +absent+ (wire-field '(("id" . 1)) "missing") "an absent key reads as not given")
  (check-equal (wire-decode-value "null") (wire-field '() "missing")
               "null and an absent key read alike")
  (check-string= "" (wire-decode-value "\"\"") "an empty string is a value")
  (check-equal '() (wire-decode-value "[]") "an empty array is a value")
  (ok (not (absentp (wire-decode-value "[]"))) "an empty array is not absent")
  (let ((object (wire-object-decode "{\"revision\":\"9007199254740995\"}")))
    (check-equal 9007199254740995 (wire-field object "revision")
                 "an object field above 2^53 round-trips as a string")
    (check-equal +absent+ (wire-field object "missing") "an absent object key is absent")))

(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-supported-list;no-request-before-handshake;oversized-frame-refused-with-one-framed-error"
  ;; A client offering an unsupported version is refused with the supported
  ;; list named and the connection closed (SPEC-WORK.md:2682-2685).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("2"))
      (ok (null version) "an unsupported version is refused")
      (check-equal '("1") refusal "the supported list is named")
      (ok (protocol-session-closed-p sess) "the connection is closed"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok (null admitted) "no request is admitted after the refusal")
      (ok (stringp why) "with a reason")))
  ;; No request is admitted before the handshake.
  (let ((sess (make-protocol-session :supported '("1"))))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-0")
      (ok (null admitted) "no request is admitted before the handshake")
      (ok (stringp why) "with a reason")))
  ;; A supported version is answered; an oversized frame is refused with one
  ;; framed error before the close (:2661-2663, :2682).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("1"))
      (check-string= "1" version "the one version the session will speak")
      (ok (null refusal) "no refusal for a supported version"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok admitted "a request after the handshake is admitted")
      (ok (null why) "with no reason"))
    (ok (not (protocol-frame-ok-p sess 17)) "an oversized frame is not ok")
    (ok (protocol-frame-ok-p sess 16) "a frame at the bound is ok")
    (let ((line (protocol-framed-error sess "frame exceeds max-frame-bytes")))
      (ok (search "request=null" line) "one framed error carries a null id")
      (ok (protocol-session-closed-p sess) "the connection is closed after it"))))

(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=every-response-reaches-only-its-request;operation-id-distinct;unknown-duplicate-absent-close;batches"
  (let ((conn (make-wire-connection :supported '("1"))))
    (multiple-value-bind (version refusal) (protocol-hello (wire-connection-protocol conn) '("1"))
      (ok (and (stringp version) (null refusal)) "the handshake finishes first"))
    ;; Pipeline two queries, a mutation and a long-operation acceptance.
    (dolist (entry '(("q-1" . :query) ("q-2" . :query)
                     ("m-1" . :mutation) ("op-1" . :operation)))
      (check-equal (car entry) (wire-pipeline-request conn (car entry) (cdr entry))
                   "the request is in flight"))
    ;; A fragment dispatches nothing until its frame is complete, and frames
    ;; delivered out of order still reach only their matching request.
    (check-equal '() (wire-feed conn "q-2 ") "an incomplete frame dispatches nothing")
    (check-equal '() (wire-feed conn "bo") "a second fragment still dispatches nothing")
    (let ((frames (wire-feed conn (format nil "dy~%op-1 accept state=running~%")))
          (seen '()))
      (check-equal '("q-2 body" "op-1 accept state=running") frames "two complete frames")
      (dolist (frame frames)
        (multiple-value-bind (id body) (wire-dispatch conn frame)
          (ok (stringp id) "a complete response is correlated to a request")
          (ok (stringp body) "with its own body")
          (push id seen)))
      (check-equal '("q-2" "op-1") (reverse seen) "each response reached only its request"))
    ;; The operation id remains distinct from the request id.
    (check-equal '("q-1" "m-1") (mapcar #'car (wire-connection-outstanding conn))
                 "the operation id is not the mutation request id")
    ;; An independent batch stops at the first refusal and marks the rest.
    (let ((out (independent-batch-results
                '(("b-1" . :applied) ("b-2") ("b-3" . :applied)))))
      (check-equal '("b-1" :applied) (first out) "the first entry is applied")
      (check-equal '("b-2" :refused) (second out) "the refusal is named")
      (check-equal '("b-3" :not-attempted) (third out) "the rest is not attempted"))
    ;; An atomic batch names its validation failure and does not partially apply.
    (multiple-value-bind (all-ok failed) (atomic-batch-validate
                                           '(("a-1" . t) ("a-2") ("a-3" . t)))
      (ok (null all-ok) "the atomic batch is refused whole")
      (check-string= "a-2" failed "the failing entry is named"))
    (multiple-value-bind (all-ok failed) (atomic-batch-validate '(("a-1" . t) ("a-2" . t)))
      (ok all-ok "a valid atomic batch is admitted")
      (ok (null failed) "with no failing entry")))
  ;; An unknown response id closes the connection without settling anything.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-1" :query)
    (multiple-value-bind (id why) (wire-dispatch conn "nope body")
      (ok (null id) "an unknown response id settles no outstanding request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; A duplicate response id closes the connection too.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-1" :query)
    (wire-dispatch conn "q-1 first")
    (multiple-value-bind (id why) (wire-dispatch conn "q-1 second")
      (ok (null id) "a duplicate response id settles no second request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; A frame with no decodable id gets a null-id refusal and admits nothing.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn "q-9" :query)
    (multiple-value-bind (id why) (wire-dispatch conn "null oops")
      (ok (null id) "a null response id acknowledges no queued request")
      (ok (search "request=null" why) "the refusal carries a null id")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; Reconnect after a lost mutation response: same-id reconciliation applies
  ;; no second event.
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "lost-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (setf (wire-session-client-alive-p sess) nil)
      (let ((before (length (state-history (kernel-state kernel))))
            (reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2) (wire-session-mutate reconnected request)
          (ok ok2 "the retry on the new connection is accepted")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the recorded disposition is returned")
          (check-equal before (length (state-history (kernel-state kernel)))
                       "same-id reconciliation applies no second event"))))))

(deftest "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
    "expected=event-stands;same-id-same-disposition;changed-args-refused;rev!=pushed"
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "disc-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code response) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (check-equal 1 (length (state-history (kernel-state kernel)))
                   "the accepted event stands")
      (ok (getf response :rev) "the response carries rev=")
      (ok (getf response :pushed) "the response carries pushed=")
      (ok (not (equal (getf response :rev) (getf response :pushed)))
          "rev= and pushed= are distinct in the response")
      ;; The client is killed; a fresh connection retries the same id and body.
      (setf (wire-session-client-alive-p sess) nil)
      (let ((reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2 response2) (wire-session-mutate reconnected request)
          (ok ok2 "the same request id and body return the recorded disposition")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the disposition is unchanged")
          (check-equal 1 (length (state-history (kernel-state kernel)))
                       "the event stands and no second event is applied")
          (ok (not (equal (getf response2 :rev) (getf response2 :pushed)))
              "rev= and pushed= stay distinct")))
      ;; The same id with different arguments is refused.
      (let ((different (copy-list request)))
        (setf (getf different :reason) "something else")
        (multiple-value-bind (ok3 line3 code3) (wire-session-mutate sess different)
          (ok (null ok3) "the same id with different arguments is refused")
          (check-equal 1 code3 "exit 1")
          (ok (search "different payload" line3) "naming the payload reuse"))))))

(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=import-returns-op-id;cli-exit-leaves-work;result-by-id;wait-timeout-leaves-running"
  ;; A long import returns a durable operation id at once, recorded before it
  ;; is printed (SPEC-WORK.md:2719-2727).
  (let* ((registry (make-operation-registry))
         (op-id (operation-accept registry :id "op-1" :kind :import
                                   :request "req-import" :author "rowan"
                                   :stamp "2026-09-14T12:00:00Z")))
    (check-string= "op-1" op-id "a long import returns an operation id at once")
    (check-equal 1 (operation-journal-length registry)
                 "the id is durable before it is printed")
    (check-equal :running (registry-operation-state registry op-id) "the work is running")
    ;; The CLI exits while the work continues.
    (operation-client-exit registry)
    (check-equal :running (registry-operation-state registry op-id)
                 "the work continues after the client exits")
    ;; operation wait times out and leaves the operation running (:2733).
    (multiple-value-bind (result state) (registry-operation-wait registry op-id :timeout 5)
      (ok (null result) "a wait timeout returns no result")
      (check-equal :timeout state "the wait reports the timeout")
      (check-equal :running (registry-operation-state registry op-id)
                   "the operation is left running"))
    ;; A completed result is retrievable by its id afterwards.
    (operation-complete registry op-id "imported 42 items")
    (check-equal :done (registry-operation-state registry op-id) "the operation completes")
    (check-string= "imported 42 items" (registry-operation-result registry op-id)
                   "the result is retrievable by id afterwards")
    ;; An id no journal holds has a line of its own (:2727-2729).
    (multiple-value-bind (state line code) (registry-operation-state registry "op-missing")
      (ok (null state) "an unknown operation has no state")
      (check-equal 2 code "exit 2")
      (ok (search "no such operation" line) "and its own line"))))
;;;; Replays for the reversible-mistake and metadata-patch paragraphs
;;;; (SPEC-WORK.md:5627,5652,5783,5798,5802). Each drives the kernel
;;;; through its verbs and asserts the paragraph's promise.
;;;; ------------------------------------------------------------------

(defun edit-request (node &key (title '(:keep)) (category '(:keep)) (links '(:keep))
                                (private '(:keep)) (version '(:keep))
                                (request "edit-1") (by "rowan") (reason "tidy")
                                (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :node-edit :node node :by by :reason reason
        :title-patch title :category-patch category :links-patch links
        :private-patch private :version-patch version
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun undo-verb-request (of request &key (by "rowan")
                                  (stamp "2026-09-14T13:00:00Z") (generation-owner "gen-4"))
  (list :verb :undo :of of :by by :request request
        :stamp stamp :clock :tool :generation-owner generation-owner))

(deftest "metadata-patches-preserve-intent" "docs/SPEC-WORK.md:5798"
    "keep/clear/set-empty/set-false/set-value round-trip and digest distinctly; all-keep/malformed/wrong-type refused; version-on-feature refused"
  (let* ((k (fresh))
         (node "acme/work/f1/t1")
         (d0 (metadata-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (edit-request node :title '(:set "hello") :category '(:set "infra")
                                :links '(:set ("a" "b")) :private '(:set :true)
                                :version '(:set "v1")))
      (ok okp "set-value edit refused: ~A" line)
      (check-equal 0 code "set-value edit exit"))
    (check-string= "hello" (node-title (kernel-state k) node) "title set")
    (check-string= "infra" (node-category (kernel-state k) node) "category set")
    (check-equal '("a" "b") (node-links (kernel-state k) node) "links set")
    (check-equal :true (node-private (kernel-state k) node) "private set")
    (check-string= "v1" (node-version (kernel-state k) node) "version set")
    (let ((d1 (metadata-digest (kernel-state k))))
      (ok (not (string= d0 d1)) "set-value moves the digest")
      (multiple-value-bind (okp2 line2) (submit k (edit-request node :request "edit-keep"
                                                                :title '(:keep)
                                                                :category '(:set "infra")))
        (ok okp2 "keep edit refused: ~A" line2)
        (check-string= d1 (metadata-digest (kernel-state k)) "a keep plus an equal set does not move the digest"))
      (multiple-value-bind (okp3 line3) (submit k (edit-request node :request "edit-clear"
                                                                :title '(:clear)))
        (ok okp3 "clear edit refused: ~A" line3)
        (ok (absentp (node-title (kernel-state k) node)) "clear writes absent")
        (ok (not (string= d1 (metadata-digest (kernel-state k)))) "clear moves the digest"))
      (multiple-value-bind (okp4 line4) (submit k (edit-request node :request "edit-empty"
                                                                :links '(:set ())))
        (ok okp4 "set-empty edit refused: ~A" line4)
        (check-equal '() (node-links (kernel-state k) node) "set-empty writes ()")
        (ok (not (absentp (node-links (kernel-state k) node))) "empty is a value, not absent"))
      (multiple-value-bind (okp5 line5) (submit k (edit-request node :request "edit-false"
                                                                :private '(:set :false)))
        (ok okp5 "set-false edit refused: ~A" line5)
        (check-equal :false (node-private (kernel-state k) node) "set-false writes false")
        (ok (not (absentp (node-private (kernel-state k) node))) "false is a value, not absent")))
    (let ((rev (state-revision (kernel-state k)))
          (hist (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-allkeep"))
        (ok (not okp) "all-keep must refuse")
        (ok (search "all keep" line) "all-keep names itself: ~A" line))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-malformed"
                                                              :title '(:bogus "x")))
        (ok (not okp) "malformed patch must refuse")
        (ok (search "patch" line) "malformed patch is named: ~A" line))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-wrongtype"
                                                              :title '(:set 5)))
        (ok (not okp) "wrong type must refuse")
        (ok (search "title" line) "wrong type names the field: ~A" line))
      (check-equal rev (state-revision (kernel-state k)) "refusals move no revision")
      (check-equal hist (length (state-history (kernel-state k))) "refusals write no history"))
    (let ((rev1 (state-revision (kernel-state k))))
      (multiple-value-bind (okp line) (submit k (edit-request "acme/work/f1" :request "edit-feat-ver"
                                                              :version '(:set "v1")))
        (ok (not okp) "version on a feature must refuse")
        (ok (search "version on a" line) "version refusal names the kind: ~A" line))
      (check-equal rev1 (state-revision (kernel-state k)) "the version refusal writes nothing")
      (multiple-value-bind (okp line) (submit k (edit-request "acme/work/f2" :request "edit-feat-keep"
                                                              :title '(:set "f2") :version '(:keep)))
        (ok okp "keep on a feature admitted: ~A" line)
        (check-equal (1+ rev1) (state-revision (kernel-state k)) "the admitted keep advances the revision")))))

(deftest "edit-is-atomic-and-replayable" "docs/SPEC-WORK.md:5802"
    "bad-patch-writes-nothing; named-fields-only-move; retry-replays-original; changed-payload-refused; equal-value changed=0 rev+1"
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (let ((rev (state-revision (kernel-state k)))
          (hist (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-bad"
                                                              :title '(:set "ok")
                                                              :category '(:set 5)))
        (ok (not okp) "a bad one-of-five patch must refuse: ~A" line)
        (check-equal rev (state-revision (kernel-state k)) "bad patch writes no revision")
        (check-equal hist (length (state-history (kernel-state k))) "bad patch writes no history")))
    (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-mixed"
                                                            :title '(:set "t")
                                                            :private '(:set :true)))
      (ok okp "mixed edit refused: ~A" line)
      (check-string= "t" (node-title (kernel-state k) node) "title moved")
      (check-equal :true (node-private (kernel-state k) node) "private moved")
      (ok (absentp (node-category (kernel-state k) node)) "an unnamed field stays put")
      (let ((rev (state-revision (kernel-state k))))
        (multiple-value-bind (okp2 line2) (submit k (edit-request node :request "edit-mixed"
                                                                  :title '(:set "t") :private '(:set :true)))
          (ok okp2 "the same request id retried refused: ~A" line2)
          (check-string= line line2 "the retry replays the original NODE OK")
          (check-equal rev (state-revision (kernel-state k)) "the retry applies nothing"))
        (multiple-value-bind (okp3 line3) (submit k (edit-request node :request "edit-mixed"
                                                                  :title '(:set "other") :private '(:set :true)))
          (ok (not okp3) "a changed payload under the same id must refuse")
          (ok (search "different payload" line3) "names the payload conflict: ~A" line3))))
    (let ((rev (state-revision (kernel-state k))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-equal"
                                                              :title '(:set "t") :private '(:set :true)))
        (ok okp "equal-value edit refused: ~A" line)
        (ok (search "changed=0" line) "changed=0 on the no-effect receipt: ~A" line)
        (check-equal (1+ rev) (state-revision (kernel-state k)) "the no-effect edit advances rev by one")))))

(deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5627"
    "event-id-recorded;journal+1;changed=0;projection-digest-unchanged;rev+1"
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (edit-request node :request "edit-base" :title '(:set "t") :private '(:set :true)))
    (let* ((digest (metadata-digest (kernel-state k)))
           (rev (state-revision (kernel-state k)))
           (journal (kernel-journal k))
           (before (length (journal-order journal))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "no-effect-1"
                                                              :title '(:set "t") :private '(:set :true)))
        (ok okp "no-effect edit refused: ~A" line)
        (ok (search "changed=0" line) "changed=0: ~A" line)
        (check-equal (1+ before) (length (journal-order journal)) "journal length +1")
        (ok (member "no-effect-1" (journal-order journal) :test #'string=)
            "the event id is recorded in the journal")
        (check-string= digest (metadata-digest (kernel-state k))
                       "the domain-projection digest is unchanged")
        (check-equal (1+ rev) (state-revision (kernel-state k)) "the event revision advances by one")
        (ok (search "no-effect-1" line) "the OK line carries the request id")))))

(deftest "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5652"
    "sent/paid/published/deleted-refused-as-external;history-never-reset"
  (let ((k (fresh))
        (handles '()))
    (dolist (spec '(("ext-sent" :sent "msg-1")
                    ("ext-paid" :paid "inv-2")
                    ("ext-pub" :published "note-3")
                    ("ext-del" :deleted "src-4")))
      (destructuring-bind (rid effect handle) spec
        (push handle handles)
        (multiple-value-bind (okp line)
            (submit k (list :verb :external-effect :node "acme/work/f1/t1" :by "rowan"
                            :effect effect :handle handle :request rid
                            :stamp "2026-09-14T12:00:00Z" :clock :tool
                            :generation-owner "gen-4"))
          (ok okp "recording external ~A refused: ~A" effect line)
          (let ((hist-before (length (state-history (kernel-state k)))))
            (multiple-value-bind (uokp uline)
                (submit k (undo-verb-request rid (concatenate 'string "undo-" rid)))
              (ok (not uokp) "an undo over ~A must refuse" effect)
              (ok (search "effect=external" uline) "names the external effect: ~A" uline)
              (ok (search (string-downcase (symbol-name effect)) uline)
                  "names the external kind: ~A" uline)
              (ok (search handle uline) "names the external handle: ~A" uline)
              (ok (search "not reversible here" uline) "refused as not reversible: ~A" uline)
              (check-equal hist-before (length (state-history (kernel-state k)))
                           "a refused undo writes no history"))))))
    ;; shared history is never reset: the four recorded effects still stand.
    (dolist (rid '("ext-sent" "ext-paid" "ext-pub" "ext-del"))
      (ok (member rid (journal-order (kernel-journal k)) :test #'string=)
          "~A still stands in the journal" rid))))

(deftest "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5783"
    "each-reversible-verb-undone-by-table;refused-verb-refused-named;terminal-dispositions-refused"
  (let ((k (fresh)))
    (let ((node "acme/work/f1/t1"))
      (submit k (edit-request node :request "edit-r1" :title '(:set "t")))
      (check-string= "t" (node-title (kernel-state k) node) "the edit is applied")
      (multiple-value-bind (okp line) (submit k (undo-verb-request "edit-r1" "undo-edit-r1"))
        (ok okp "an undo of node edit refused: ~A" line)
        (ok (absentp (node-title (kernel-state k) node)) "the compensating edit restores :before")
        (ok (search "UNDO OK" line) "the undo appends its own envelope: ~A" line)))
    (let ((node "acme/work/f1/t2"))
      (check-equal :review (node-state (kernel-state k) node) "preimage is review")
      (submit k (list :verb :state-to-doing :node node :by "rowan" :reason "start"
                      :request "to-doing-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                      :generation-owner "gen-4"))
      (check-equal :doing (node-state (kernel-state k) node) "moved to doing")
      (multiple-value-bind (okp line) (submit k (undo-verb-request "to-doing-1" "undo-to-doing-1"))
        (ok okp "an undo of a transition refused: ~A" line)
        (check-equal :review (node-state (kernel-state k) node) "the preimage state is restored")))
    (submit k (list :verb :external-effect :node "acme/work/f1/t1" :by "rowan"
                    :effect :sent :handle "m-1" :request "ext-1"
                    :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-verb-request "ext-1" "undo-ext-1"))
      (ok (not okp) "an undo over a recorded effect must refuse")
      (ok (search "not reversible here" line) "refused by name: ~A" line))
    (submit k (list :verb :node-remove :node "acme/work/f1/t1" :by "rowan" :reason "drop"
                    :request "rm-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-verb-request "rm-1" "undo-rm-1"))
      (ok (not okp) "an undo over node remove must refuse")
      (ok (search "not reversible here" line) "the terminal remove is refused: ~A" line))
    (submit k (list :verb :event-cancel :node "acme/work/f1/t2" :by "rowan" :reason "drop"
                    :request "cancel-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-verb-request "cancel-1" "undo-cancel-1"))
      (ok (not okp) "an undo over event cancel must refuse")
      (ok (search "not reversible here" line) "the terminal cancel is refused: ~A" line))))
;;;; The five replays of nova-tools #362 (SPEC-WORK.md lines 2400-3600
;;;; part 1 of 4). Each drives the kernel through the verb its paragraph
;;;; names and asserts the outcome the paragraph promises.
;;;; ------------------------------------------------------------------

(deftest "edit-undo-preserves-later-work" "docs/SPEC-WORK.md:5355"
    "expected=undo-restores-before;late-undo=conflict;both-events-stand"
  ;; SPEC-WORK.md:5806 -- an edit undone restores :before; the same undo after
  ;; an intervening edit is refused conflict, both events standing.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/t" :type :task :parent "root" :state :doing :title "a")))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (node-edit k "root/t" :changes (list :title "b") :request "edit-1"
                   :reason "rename" :stamp "2026-09-17T00:00:00Z")
      (ok okp "first edit accepted: ~A" line)
      (check-equal 0 code "first edit exit"))
    (check-string= "b" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "edit postimage")
    (multiple-value-bind (okp line code)
        (node-undo k "root/t" :undo-of "edit-1" :request "undo-1"
                   :reason "revert" :stamp "2026-09-17T00:01:00Z")
      (ok okp "undo accepted: ~A" line)
      (check-equal 0 code "undo exit"))
    (check-string= "a" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "undo restores :before")
    (node-edit k "root/t" :changes (list :title "c") :request "edit-2"
               :reason "again" :stamp "2026-09-17T00:02:00Z")
    (multiple-value-bind (okp line code)
        (node-undo k "root/t" :undo-of "edit-1" :request "undo-2"
                   :reason "late" :stamp "2026-09-17T00:03:00Z")
      (ok (null okp) "late undo refused")
      (check-equal 1 code "late undo conflict exit")
      (ok (search "conflict" line) "conflict named: ~A" line))
    (check-string= "c" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "later work stands")
    (check-equal 2 (length (node-edit-log (kernel-state k) "root/t"))
                  "both edit events stand")))

(deftest "edit-never-fetches-a-link" "docs/SPEC-WORK.md:5357"
    "expected=fetches=0;nul=bad-link;private-node-prints-no-value"
  ;; SPEC-WORK.md:5808 -- a link that is a live URL to a counting endpoint
  ;; added, edited and rendered with zero requests observed; a link holding NUL
  ;; refused `bad link`; a refusal on a private node printing no value.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/t" :type :task :parent "root" :state :doing)
                 (:id "root/p" :type :task :parent "root" :state :doing :private t)))
         (k (make-kernel :state (make-seed-state seed))))
    (setf *link-fetch-count* 0)
    (node-edit k "root/t" :changes (list :links (list "http://127.0.0.1:9/count"))
               :request "link-1" :reason "add" :stamp "2026-09-17T00:00:00Z")
    (node-edit k "root/t" :changes (list :links (list "http://127.0.0.1:9/other"))
               :request "link-2" :reason "edit" :stamp "2026-09-17T00:01:00Z")
    (let ((rendered (render-node k "root/t")))
      (ok (search "127.0.0.1" rendered) "link rendered: ~A" rendered))
    (check-equal 0 *link-fetch-count* "zero requests on add, edit and render")
    (multiple-value-bind (okp line code)
        (node-edit k "root/t"
                   :changes (list :links (list (format nil "http://x/~C" #\Nul)))
                   :request "link-3" :reason "bad" :stamp "2026-09-17T00:02:00Z")
      (ok (null okp) "NUL link refused")
      (check-equal 1 code "NUL link exit")
      (ok (search "bad link" line) "bad link named: ~A" line))
    (multiple-value-bind (okp line code)
        (node-edit k "root/p" :changes (list :title 7)
                   :request "priv-1" :reason "bad type" :stamp "2026-09-17T00:03:00Z")
      (ok (null okp) "private node refusal")
      (check-equal 1 code "private refusal exit")
      (ok (search "private=1" line) "private marker printed: ~A" line)
      (ok (null (search "7" line)) "no value printed: ~A" line))))

(deftest "repo-only-at-the-root" "docs/SPEC-WORK.md:5341"
    "expected=repo-at-root-unique;second-refused-repo-held-by;under-parent-refused"
  ;; SPEC-WORK.md:5792 -- `--repo` under the open root and `--type work-set`
  ;; accepted and unique; the same `--repo` again refused `repo held by <id>`;
  ;; `--repo` under a parent refused `repo outside root`.
  (let ((k (make-kernel :state (make-seed-state '()))))
    (multiple-value-bind (okp line code)
        (node-add k :id "r1" :type :work-set :parent nil :repo "acme/x")
      (ok okp "root repo accepted: ~A" line)
      (check-equal 0 code "root repo exit"))
    (check-string= "acme/x" (node-repo (kernel-state k) "r1") "repo recorded")
    (multiple-value-bind (okp line code)
        (node-add k :id "r2" :type :work-set :parent nil :repo "acme/x")
      (ok (null okp) "second repo refused")
      (check-equal 1 code "second repo exit")
      (ok (search "repo held by r1" line) "holder named: ~A" line))
    (multiple-value-bind (okp line code)
        (node-add k :id "r3" :type :work-set :parent "r1" :repo "acme/y")
      (ok (null okp) "under-parent repo refused")
      (check-equal 1 code "under-parent exit")
      (ok (search "repo outside root" line) "outside root named: ~A" line))))

(deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
    "expected=node-add--type-roadmap-exit-2-naming-roadmap-create;one-node-and-one-view-atomic"
  ;; SPEC-WORK.md:5795 -- `node add --type roadmap` exit 2 naming `roadmap
  ;; create`; `roadmap create` writing one node and one view in one envelope.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (node-add k :id "rm" :type :roadmap :parent "root")
      (ok (null okp) "node add roadmap refused")
      (check-equal 2 code "roadmap add exit 2")
      (ok (search "roadmap create" line) "names roadmap create: ~A" line))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "Now"
                        :axes '() :members '()
                        :reason "new" :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "roadmap create accepted: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    (check-equal :roadmap (node-type (kernel-state k) "rm") "roadmap node exists")
    (ok (node-view (kernel-state k) "rm") "roadmap view created atomically")))

(deftest "move-keeps-every-count" "docs/SPEC-WORK.md:5360"
    "expected=source+destination-counts-move-by-subtree;ancestor-net-stable;no-whole-set-scan"
  ;; SPEC-WORK.md:5811 -- a required subtree moved between two features: the
  ;; source's and destination's required sets and open counts move by the
  ;; subtree, the common ancestor's net count is stable, |O|, |C| and every
  ;; task state unchanged, and no whole-set scan (visits asserted).
  (let* ((seed '((:id "root"     :type :work-set :parent nil       :state :unknown)
                 (:id "root/f1"  :type :feature  :parent "root"    :state :unknown)
                 (:id "root/f2"  :type :feature  :parent "root"    :state :unknown)
                 (:id "root/f1/t1" :type :task   :parent "root/f1" :state :doing)
                 (:id "root/f1/t2" :type :task   :parent "root/f1" :state :doing)
                 (:id "root/f2/t3" :type :task   :parent "root/f2" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)))
         (o-before (state-open-count (kernel-state k)))
         (c-before (state-closed-count (kernel-state k)))
         (t1-state (node-state (kernel-state k) "root/f1/t1")))
    (check-equal 3 (node-open-count (kernel-state k) "root/f1") "source open before")
    (check-equal 2 (node-open-count (kernel-state k) "root/f2") "destination open before")
    (let ((okp nil) (line nil) (code 0) (visits 0))
      (with-instrumentation
        (setf (values okp line code)
              (node-move k :id "root/f1/t1" :from "root/f1" :under "root/f2"
                         :reason "redistribute" :request "mv-1"
                         :stamp "2026-09-17T00:00:00Z"))
        (setf visits *visits*))
      (ok okp "move accepted: ~A" line)
      (check-equal 0 code "move exit")
      (ok (< visits 6) "no whole-set scan (visits=~D)" visits))
    (check-equal 2 (node-open-count (kernel-state k) "root/f1") "source count falls by subtree")
    (check-equal 3 (node-open-count (kernel-state k) "root/f2") "destination count rises by subtree")
    (check-equal 1 (node-required-count (kernel-state k) "root/f1") "source required set falls")
    (check-equal 2 (node-required-count (kernel-state k) "root/f2") "destination required set rises")
    (check-equal 1 (node-required-open (kernel-state k) "root/f1") "source required-open falls")
    (check-equal 2 (node-required-open (kernel-state k) "root/f2") "destination required-open rises")
    (check-equal o-before (state-open-count (kernel-state k)) "|O| unchanged")
    (check-equal c-before (state-closed-count (kernel-state k)) "|C| unchanged")
    (check-equal t1-state (node-state (kernel-state k) "root/f1/t1") "moved task state unchanged")
    (check-equal '("root/f2/t3" "root/f1/t1") (node-children (kernel-state k) "root/f2")
                 "appended at destination end")
    (check-equal '("root/f1/t2") (node-children (kernel-state k) "root/f1")
                 "removed from source")))
