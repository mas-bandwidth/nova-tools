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
