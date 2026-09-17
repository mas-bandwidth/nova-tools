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
    "slice-10-fleet.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-09-replays-roadmap.lisp"
    "slice-09-fleet-assignment.lisp"
    "slice-10-fleet.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-09-replays-holds.lisp"
    "slice-11-dependencies.lisp"))

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

;;;; The wire, protocol-version, disconnect, pipeline-correlation and
;;;; long-operation replays (nova-tools card 8608). The slice files keep
;;;; the names as placeholders; these are the executable paragraphs.
;;;; ------------------------------------------------------------------

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

;;;; The five replays of nova-tools #362 (SPEC-WORK.md lines 2400-3600
;;;; part 1 of 4). Each drives the kernel through the verb its paragraph
;;;; names and asserts the outcome the paragraph promises.
;;;; ------------------------------------------------------------------

;;;; Assignment and execution control replays (docs/SPEC-WORK.md:3835-3919,
;;;; the replay summary at :5229-5243). The pure book lives in
;;;; src/assignment.lisp; each replay drives it and asserts the paragraph.
;;;; ------------------------------------------------------------------

(defun alist-get (key alist)
  (cdr (assoc key alist :test #'equal)))

(defun accepted-book (&key (holder "alice") (deadline "2026-09-14T12:30:00Z")
                           (default "release"))
  "One admitted, verified-received and accepted offer off-1 on n1."
  (let ((book (nth-value 3
                (leasebook-offer
                 (make-leasebook :nodes '(("n1" . :doing)))
                 :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                 :to holder :profile "cap@1" :reserve 2
                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t))))
    (let ((book (nth-value 3
                  (leasebook-received book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1"
                                      :receipt-digest "rd-1" :receipt-id "rc-1"
                                      :request "qr" :verifier "v"))))
      (nth-value 3
                 (leasebook-accepted book :offer "off-1" :node "n1"
                                     :generation "gen-4" :attempt "att-1"
                                     :by holder :default default :deadline deadline)))))

(defun declined-book ()
  "One admitted, received and declined offer off-1 on n1."
  (let ((book (nth-value 3
                (leasebook-offer
                 (make-leasebook :nodes '(("n1" . :doing)))
                 :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                 :to "alice" :profile "cap@1" :reserve 2
                 :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t))))
    (let ((book (nth-value 3
                  (leasebook-received book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1"
                                      :receipt-digest "rd-1" :receipt-id "rc-1"
                                      :request "qr" :verifier "v"))))
      (nth-value 3 (leasebook-decline book :offer "off-1" :node "n1"
                                      :generation "gen-4" :attempt "att-1")))))

