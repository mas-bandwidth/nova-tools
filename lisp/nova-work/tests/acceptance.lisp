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
    "slice-10-routes.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-09-replays-holds.lisp"
    "slice-11-dependencies.lisp"
    "slice-12-session-ownership.lisp"
    "slice-13-verifier.lisp"
    "slice-14-receipt-admission.lisp"
    "slice-15-staged-admission.lisp"
    "slice-16-savepoint-create.lisp"
    "slice-17-savepoint-restore.lisp"
    "slice-19-verification-cache-file.lisp"
    "slice-18-dedup-root.lisp"
    "slice-19-journal-rotation.lisp"
    "slice-18-verify-resolver.lisp"))

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

;;;; ------------------------------------------------------------------
;;;; E10-F01 "Attach stable error code, stage, verb, request/operation ID
;;;; and known revisions" (ROADMAP.md:974).
;;;;
;;;; docs/SPEC-WORK.md:2682 pins the structured response envelope
;;;; `{"request","ok","exit","lines","rev","pushed"}`; :2695 pins that a
;;;; refusal still echoes its request id; *Output grammar* (:5921-5934) pins
;;;; the verb as every line's first token and rev=/pushed= on every OK line;
;;;; :2727-2730 pins the operation diagnostic carrying the operation id, the
;;;; op, the state (the stage) and a named reason. A refusal must therefore
;;;; attach all five: the stable error code (a named reason and a nonzero
;;;; exit), the verb, the request/operation id, and the two known revisions.
;;;; ------------------------------------------------------------------

(deftest "TestE10F01AttachStableErrorCodeStage" "docs/SPEC-WORK.md:2682"
    "expected=refusal-carries-verb+reason+exit;envelope-carries-request+ok+rev+pushed+operation;op-diag-carries-id+op+state+reason"
  ;; (a) A refused mutation attaches the verb, a stable named reason and a
  ;; nonzero exit (the stable error code), and the response envelope still
  ;; carries the request id and the two known revisions.
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7")))
    (multiple-value-bind (okp line code response)
        (wire-session-mutate sess (append (close-request :request "e10-f01-1")
                                          (list :priority "high")))
      (ok (null okp) "an unknown request field was accepted, not refused")
      (check-equal 2 code "the refusal's exit code")
      (ok (eql 0 (search "STATE" line)) "the refusal does not name the verb: ~A" line)
      (ok (search "FAIL" line) "the line is not a FAIL line: ~A" line)
      (ok (search "unknown field" line) "the refusal does not name its reason: ~A" line)
      (ok (equal "e10-f01-1" (getf response :request))
          "the refusal envelope does not carry the request id: ~S" response)
      (ok (null (getf response :ok)) "the refusal envelope does not mark ok=false")
      (ok (not (null (getf response :rev))) "the refusal envelope carries no rev=")
      (ok (not (null (getf response :pushed))) "the refusal envelope carries no pushed=")))
  ;; (b) The wire frame itself — the literal :2682 shape — keeps every
  ;; attachment even for a failure, and carries a long operation's id beside
  ;; (never in place of) the request id that asked for it.
  (let ((frame (wire-frame-response "e10-f01-2" :ok "false" :exit "2"
                                    :lines (list "STATE FAIL node=acme/work/f1/t1: rule 3: not ready")
                                    :rev "5" :pushed "shared-7" :operation "op-9")))
    (ok (search "\"request\": \"e10-f01-2\"" frame) "the frame omits request id: ~A" frame)
    (ok (search "\"ok\": false" frame) "the frame omits the error signal: ~A" frame)
    (ok (search "\"exit\": \"2\"" frame) "the frame omits the exit code: ~A" frame)
    (ok (search "\"rev\": \"5\"" frame) "the frame omits rev=: ~A" frame)
    (ok (search "\"pushed\": \"shared-7\"" frame) "the frame omits pushed=: ~A" frame)
    (ok (search "\"operation\": \"op-9\"" frame) "the frame carries no operation id: ~A" frame))
  ;; (c) The operation diagnostic attaches the operation id, the op (verb), the
  ;; state (the stage) and a stable named reason, in the grammar's own FAIL line.
  (multiple-value-bind (state line code)
      (registry-operation-state (make-operation-registry) "op-missing")
    (ok (null state) "an unknown operation invented a state")
    (check-equal 2 code "the unknown operation's exit code")
    (ok (search "id=op-missing" line) "the operation id is not attached: ~A" line)
    (ok (search "op=" line) "the op (verb) is not attached: ~A" line)
    (ok (search "state=" line) "the stage/state is not attached: ~A" line)
    (ok (search "no such operation" line) "the stable reason is not attached: ~A" line)))


;;; ------------------------------------------------------------------
;;; E03-F03-01 "Support baseline, discovery, require, dependency add/remove
;;; and prioritize" (docs/roadmaps/nova-work.sexp E03-F03; recut of
;;; nova-tools#2271).
;;;
;;; docs/SPEC-WORK.md:2929 -- "scope and dependencies | baseline, discovery,
;;; dependency add/remove, prioritise, ...". `:required`, the baseline and the
;;; priority slots were seed-time only. Each verb is a MUTATION of the one
;;; writer: an event of its own kind (:require, :baseline, :discovery,
;;; :prioritise -- SPEC-WORK.md:1011, :1096-1100) journaled through the
;;; kernel's command thread, deduplicated by request id before any mutable
;;; precondition, and replayed by `apply-event`, so a reconstruction from the
;;; canonical bytes reads what the live verbs acknowledged and a retried
;;; request answers its original receipt without applying twice. Dependency
;;; add/remove is dev's durable `dep-edit` (dep-verb.lisp), exercised here in
;;; the same history.
;;; ------------------------------------------------------------------

(defparameter *scope-seed*
  '((:id "r"   :type :work-set :parent nil :state :unknown)
    (:id "r/a" :type :task     :parent "r"  :state :todo)
    (:id "r/b" :type :task     :parent "r"  :state :todo  :required nil)
    (:id "r/c" :type :task     :parent "r"  :state :doing)
    (:id "r/x" :type :task     :parent "r"  :state :todo))
  "A container r with required children a, c, x and one optional child b
(SPEC-WORK.md:1147 keeps an optional child in :children but out of the set).")

(defun %scope-call (fn k &rest args)
  "Call a scope verb with the author and stamp every case uses."
  (apply fn k :as "rowan" :stamp "2026-09-23T18:00:00Z" args))

(deftest "TestE03F03SupportBaselineDiscoveryRequireDependency" "docs/SPEC-WORK.md:2929"
    "expected=require-baseline-discovery-prioritise-and-dep-are-journaled;reconstruct-reads-the-same-set;a-retried-request-answers-its-receipt-once"
  (let* ((k (make-kernel :state (make-seed-state *scope-seed*)
                         :journal (make-ordering-journal :capacity 64) :rev-base 1))
         (off-line nil))
    ;; require --to false removes a from the set and detaches nothing.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/a" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok okp "require --to false refused: ~A" line)
      (check-equal 0 code "require --to false at exit 0")
      (setf off-line line))
    (ok (not (node-required-p (kernel-state k) "r/a"))
        "require --to false did not remove r/a from the required set")
    (check-equal 2 (node-required-count (kernel-state k) "r") "the set shrank by one")
    (ok (member "r/a" (node-children (kernel-state k) "r") :test #'string=)
        "require --to false detached r/a from :children, which it must not")
    ;; --to true restores it.
    (multiple-value-bind (okp line)
        (%scope-call 'node-require k :node "r/a" :to t :reason "needed again"
                                     :request "req-require-on")
      (ok okp "require --to true refused: ~A" line))
    (ok (node-required-p (kernel-state k) "r/a")
        "require --to true did not restore r/a to the required set")
    (check-equal 3 (node-required-count (kernel-state k) "r") "the set grew back by one")
    ;; baseline records the set as computed now, member by member.
    (multiple-value-bind (okp line)
        (%scope-call 'baseline k :node "r" :reason "sprint start" :request "req-baseline")
      (ok okp "baseline refused: ~A" line))
    (check-equal '("r/a" "r/c" "r/x") (node-baseline (kernel-state k) "r")
                 "baseline did not record the members it computed")
    ;; discovery adds the existing optional child; a non-child is refused.
    (multiple-value-bind (okp line)
        (%scope-call 'discovery k :node "r" :members '("r/b") :reason "found it"
                                  :request "req-discovery")
      (ok okp "discovery refused: ~A" line))
    (ok (node-required-p (kernel-state k) "r/b") "discovery did not add r/b to the set")
    (check-equal 4 (node-required-count (kernel-state k) "r") "the set is four after discovery")
    (multiple-value-bind (okp line)
        (%scope-call 'discovery k :node "r/a" :members '("r/b") :reason "wrong parent"
                                  :request "req-discovery-bad")
      (ok (not okp) "discovery of a non-child was accepted: ~A" line))
    ;; prioritise sets a rank on the node's :self slot.
    (multiple-value-bind (okp line)
        (%scope-call 'prioritise k :node "r/x" :set 2 :reason "first" :request "req-prio")
      (ok okp "prioritise refused: ~A" line))
    (check-equal 2 (getf (node-priority (kernel-state k) "r/x") :self)
                 "prioritise did not set the :self rank")
    ;; dependency add/remove is dev's durable dep-edit.
    (multiple-value-bind (okp line)
        (dep-edit k :node "r/a" :add "r/x" :as "rowan" :reason "order"
                    :request "req-dep-add" :stamp "2026-09-23T18:00:00Z")
      (ok okp "dep --add refused: ~A" line))
    (check-equal '("r/x") (node-deps (kernel-state k) "r/a") "dep --add did not record the edge")
    ;; Every accepted verb above is on the record: a reconstruction from the
    ;; canonical bytes reads the same set, baseline, rank and edge.
    (let ((rebuilt (reconstruct-state
                    (canonical-string (state-canonical-form (kernel-state k))))))
      (ok (node-required-p rebuilt "r/a") "reconstruct lost require --to true on r/a")
      (ok (node-required-p rebuilt "r/b") "reconstruct lost the discovery of r/b")
      (check-equal 4 (node-required-count rebuilt "r") "reconstruct read a different set size")
      (check-equal '("r/a" "r/c" "r/x") (node-baseline rebuilt "r")
                   "reconstruct lost the baseline")
      (check-equal 2 (getf (node-priority rebuilt "r/x") :self) "reconstruct lost the rank")
      (check-equal '("r/x") (node-deps rebuilt "r/a") "reconstruct lost the dep edge"))
    ;; Retrying the FIRST request after later changes answers its original
    ;; receipt and applies nothing: r/a stays required.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/a" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok okp "the identical retry was refused: ~A" line)
      (check-equal 0 code "the retry at exit 0")
      (check-string= off-line line "the retry answered a different receipt"))
    (ok (node-required-p (kernel-state k) "r/a") "the retry re-applied require --to false")
    (check-equal 4 (node-required-count (kernel-state k) "r") "the retry moved the set")
    ;; The same id with a changed payload is refused and writes nothing.
    (multiple-value-bind (okp line code)
        (%scope-call 'node-require k :node "r/c" :to nil :reason "not needed"
                                     :request "req-require-off")
      (ok (not okp) "a reused id with a new payload was accepted: ~A" line)
      (check-equal 1 code "the conflict at exit 1"))
    (ok (node-required-p (kernel-state k) "r/c") "the refused conflict wrote r/c")))
