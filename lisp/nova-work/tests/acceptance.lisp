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
    "slice-09-state-export-replays.lisp"
    "slice-10-fleet.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
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
