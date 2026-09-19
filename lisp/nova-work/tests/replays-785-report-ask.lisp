;;;; replays-785-report-ask.lisp --- engagement and the one reports ask
;;;; (nova-tools #854 item 7).
;;;;
;;;; Rules 9 and 10 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:5114-5163, with replays
;;;; `a-hand-launch-is-refused-when-asked-and-recorded-when-told` (:6995),
;;;; `a-report-of-a-stop-releases-nothing` (:7002),
;;;; `reports-are-read-in-one-ask` (:7006), and, from the approved follow-up
;;;; #1580 at 191b9ab7 (SPEC-WORK.md:5186 there),
;;;; `a-report-of-a-stop-ends-the-engagement-a-launch-report-began`.
;;;;
;;;; Three clauses here are #1580's and are not yet on `dev`: that a `:stopped`
;;;; report ends the engagement a `:launched` one began (its rule 8 sentence),
;;;; the privacy floor including the id, and that replay. They are approved and
;;;; are implemented; the PR body says so.

(in-package #:nova-work/tests)

(defparameter *ask-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/p" :type :task     :parent "acme/work" :state :todo
     :private :true))
  "D depends on the open N; P is a private node.")

(defun ask-kernel (&optional (seed *ask-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1
               :friends '("rowan" "emma")))

(defun launch-report (k node &key (as "rowan") (request (format nil "launch-~A" node))
                                  (stamp "2026-09-16T12:00:30Z"))
  (report-submit k :subject (format nil "node:~A" node) :act :launched
                   :what "started a worker from a shell"
                   :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                   :reason "the card was already cut"
                   :as as :request request :stamp stamp))

(defun stop-report (k node &key (as "rowan") (request (format nil "stop-~A" node))
                                (stamp "2026-09-16T13:00:30Z"))
  (report-submit k :subject (format nil "node:~A" node) :act :stopped
                   :what "killed the worker"
                   :acted-at "2026-09-16T13:00:00Z" :instead-of "-"
                   :reason "it was going nowhere"
                   :as as :request request :stamp stamp))

;;; ------------------------------------------------------------------
;;; a-hand-launch-is-refused-when-asked-and-recorded-when-told
;;;                                              SPEC-WORK.md:5114, :6995
;;; ------------------------------------------------------------------

(deftest "a-hand-launch-is-refused-when-asked-and-recorded-when-told"
    "docs/SPEC-WORK.md:5114"
    "expected=the-record-is-evidence-of-a-breach-and-never-an-override"
  (let ((k (ask-kernel)))
    ;; asked, it is refused, and no flag changes it
    (dolist (dry '(t nil))
      (let ((line (handler-case (progn (take-lease k "acme/work/d" "emma" :dry-run dry) nil)
                    (unsupported-input (c) (unsupported-input-what c)))))
        (ok (and line (search "unmet need" line))
            "take --node D~:[~; --dry-run~] refuses: ~A" dry line)))
    ;; told, it is recorded and never refused
    (multiple-value-bind (okp line code) (launch-report k "acme/work/d")
      (ok okp "the launch report is written whatever the node's needs are: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "unmet=1" line) "and prints the count for that revision: ~A" line)
      (ok (search "need=acme/work/n" line) "and the need: ~A" line)
      (ok (search "holder=unowned" line) "and the holder: ~A" line))
    ;; it grants no lease, adds nothing to W, moves no state
    (ok (null (node-holder (kernel-state k) "acme/work/d")) "no lease is granted")
    (check-equal 0 (working-count k) "nothing is added to W")
    (check-equal :todo (node-state (kernel-state k) "acme/work/d") "no state moves")
    ;; from that event the node reads needs-broken=true, because a node carrying
    ;; a report of a launch is engaged
    (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
        "a node carrying a report of a launch is engaged, so it is flagged")
    ;; and the next admission verb still refuses
    (ok (search "unmet need"
                (or (handler-case (progn (take-lease k "acme/work/d" "emma") nil)
                      (unsupported-input (c) (unsupported-input-what c)))
                    ""))
        "the next take still refuses: the record is never an override")
    ;; N met: needs-broken=false and take is admitted
    (submit k (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                    :reason "start" :request "doing-n"
                    :stamp "2026-09-16T13:30:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (submit k (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                    :reason "merged" :evidence (list "ev-1") :request "done-n"
                    :stamp "2026-09-16T14:00:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (refresh-needs-view k)
    (ok (not (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k)))
        "once the need is met the reading costs the node nothing")
    (ok (null (handler-case (progn (take-lease k "acme/work/d" "emma") nil)
                (unsupported-input (c) (unsupported-input-what c))))
        "and take is admitted")
    ;; the same report over a needs-met node held by another name
    (multiple-value-bind (okp line)
        (launch-report k "acme/work/d" :request "launch-2"
                                       :stamp "2026-09-16T15:00:30Z")
      (ok okp "a hand launch of a needs-met node is recorded the same way")
      (ok (search "unmet=0 need=- holder=emma" line)
          "with unmet=0 need=- and holder= saying whose lease it went round: ~A" line))))

;;; ------------------------------------------------------------------
;;; a-report-of-a-stop-ends-the-engagement-a-launch-report-began
;;;                      #1580 at 191b9ab7, SPEC-WORK.md:5186 there
;;; ------------------------------------------------------------------

(deftest "a-report-of-a-stop-ends-the-engagement-a-launch-report-began"
    "docs/SPEC-WORK.md:5137"
    "expected=launched-begins-an-engagement-and-stopped-ends-it-and-a-lease-outlives-both"
  (let ((k (ask-kernel)))
    ;; over an unleased :todo D with N open
    (launch-report k "acme/work/d")
    (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
        "D's row prints needs-broken=true")
    (stop-report k "acme/work/d")
    ;; with no other command
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
      (check-equal 1 unmet "the need is still unmet"))
    (ok (not (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k)))
        "but D reads needs-broken=false with no other command")
    (check-equal 0 (state-needs-broken-count (kernel-state k) :view (kernel-needs-view k))
                 "and check counts one fewer")
    ;; a second launch report raises it again
    (launch-report k "acme/work/d" :request "launch-2"
                                   :stamp "2026-09-16T14:00:30Z")
    (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
        "a second launch report raises it again"))
  ;; a stop report on a leased D leaves needs-broken=true, because the lease
  ;; engages it by itself
  (let ((k (ask-kernel)))
    (submit k (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                    :reason "start" :request "doing-n"
                    :stamp "2026-09-16T10:30:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (submit k (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                    :reason "merged" :evidence (list "ev-1") :request "done-n"
                    :stamp "2026-09-16T11:00:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (refresh-needs-view k)
    (take-lease k "acme/work/d" "emma")
    (submit k (list :verb :event-reopen :node "acme/work/n" :by "rowan"
                    :reason "reverted" :request "reopen-n"
                    :stamp "2026-09-16T11:30:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (launch-report k "acme/work/d")
    (stop-report k "acme/work/d")
    (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k))
        "a stop report on a leased D leaves it flagged: the lease engages it")))

;;; ------------------------------------------------------------------
;;; a-report-of-a-stop-releases-nothing          SPEC-WORK.md:5159, :7002
;;; ------------------------------------------------------------------

(deftest "a-report-of-a-stop-releases-nothing" "docs/SPEC-WORK.md:5159"
    "expected=a-report-of-a-stop-is-not-stop-evidence"
  (let ((k (ask-kernel)))
    (submit k (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                    :reason "start" :request "doing-n"
                    :stamp "2026-09-16T10:30:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (submit k (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                    :reason "merged" :evidence (list "ev-1") :request "done-n"
                    :stamp "2026-09-16T11:00:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (refresh-needs-view k)
    (take-lease k "acme/work/d" "emma")
    (let ((leases-before (length (state-lease-log (kernel-state k)))))
      (multiple-value-bind (okp line) (stop-report k "acme/work/d")
        (ok okp "the stop report is recorded: ~A" line)
        (ok (search "holder=emma" line) "and prints the node's holder: ~A" line))
      (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                     "the lease stands")
      (check-equal leases-before (length (state-lease-log (kernel-state k)))
                   "no :release is appended to the lease log"))))

;;; ------------------------------------------------------------------
;;; reports-are-read-in-one-ask                  SPEC-WORK.md:5142, :7006
;;; ------------------------------------------------------------------

(deftest "reports-are-read-in-one-ask" "docs/SPEC-WORK.md:5142"
    "expected=three-counts-one-row-per-event-newest-last-and-the-privacy-floor"
  (let ((k (ask-kernel)))
    ;; three reports: one with --instead-of -, one the launch of the replay
    ;; above, one naming a verb it reached past
    (launch-report k "acme/work/d")
    (report-submit k :subject "external:the merge queue" :act :other
                     :what "dropped a stuck entry by hand"
                     :acted-at "2026-09-16T12:10:00Z"
                     :instead-of "nova-swarm drop"
                     :reason "it blocked every other row"
                     :as "emma" :request "rep-ext"
                     :stamp "2026-09-16T12:11:00Z")
    (stop-report k "acme/work/d")
    (multiple-value-bind (line rows code) (query-reports (kernel-state k) :since 0)
      (check-equal 0 code "the ask answers at exit 0")
      (ok (search "ask=reports" line) "QUERY OK names the ask: ~A" line)
      (ok (search "reports=3" line) "reports= counts the events in the range: ~A" line)
      (ok (search "no-verb=2" line)
          "no-verb= counts those whose :instead-of is -: ~A" line)
      (ok (search "launched-unmet=1" line)
          "launched-unmet= counts the launches over an unmet need: ~A" line)
      (check-equal 3 (length rows) "one row per :report event")
      (ok (search "kind=report" (first rows)) "each row names its kind: ~A" (first rows))
      ;; newest last
      (ok (search "act=launched" (first rows)) "oldest first: ~A" (first rows))
      (ok (search "act=stopped" (car (last rows))) "newest last: ~A" (car (last rows)))
      ;; the row prints the why beside the what, reason= last
      (let ((r (first rows)))
        (ok (search "what=" r) "what= is on the row: ~A" r)
        (ok (search "reason=" r) "and reason=: ~A" r)
        (ok (> (search "reason=" r) (search "what=" r)) "reason= last: ~A" r)))
    ;; --max 1 prints one row and a MORE line
    (multiple-value-bind (line rows code more)
        (query-reports (kernel-state k) :since 0 :max 1)
      (declare (ignore line code))
      (check-equal 1 (length rows) "--max 1 prints one row")
      (ok more "and a MORE line: ~A" more)
      (ok (search "MORE" more) "which says MORE: ~A" more))
    ;; --node D prints D's alone
    (multiple-value-bind (line rows)
        (query-reports (kernel-state k) :since 0 :node "acme/work/d")
      (declare (ignore line))
      (check-equal 2 (length rows) "--node narrows to that node's reports"))
    ;; --branch closed and --branch root are exit 2, as they are for handoffs
    (dolist (branch '(:closed :root))
      (multiple-value-bind (line rows code)
          (query-reports (kernel-state k) :since 0 :branch branch)
        (declare (ignore rows))
        (check-equal 2 code (format nil "~A is exit 2" branch))
        (ok (search "not admitted under branch" line) "by name: ~A" line)))
    ;; --since is required, and is refused before the retention boundary
    (multiple-value-bind (line rows code) (query-reports (kernel-state k))
      (declare (ignore rows))
      (check-equal 2 code "--since is required")
      (ok (search "--since is required" line) "by name: ~A" line))
    (multiple-value-bind (line rows code)
        (query-reports (kernel-state k) :since 1 :floor 5)
      (declare (ignore rows))
      (check-equal 2 code "--since before the retention boundary is refused")
      (ok (search "before the retention boundary" line) "by name: ~A" line))
    ;; the range is counted in revisions: a backdated --acted-at moves no report
    (report-submit k :subject "external:a year ago" :act :other
                     :what "did a thing long ago"
                     :acted-at "2025-09-16T12:00:00Z" :instead-of "-"
                     :reason "it is only being told now"
                     :as "rowan" :request "rep-old"
                     :stamp "2026-09-16T16:00:00Z")
    (multiple-value-bind (line rows) (query-reports (kernel-state k) :since 0)
      (declare (ignore line))
      (check-equal 4 (length rows)
                   "a report whose --acted-at is a year old is in the answer for
the revision it was written at and no other"))))

;;; ------------------------------------------------------------------
;;; the privacy floor, the id included
;;;                      #1580 at 191b9ab7, SPEC-WORK.md:5175 there
;;; ------------------------------------------------------------------

(deftest "a-report-of-a-private-node-names-nothing-of-it"
    "docs/SPEC-WORK.md:5152"
    "expected=subject-node-dash-need-dash-what-dash-reason-dash-and-still-counted"
  (let ((k (ask-kernel)))
    (report-submit k :subject "node:acme/work/p" :act :launched
                     :what "started a worker on the private node"
                     :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                     :reason "a reason that is nobody else's business"
                     :as "rowan" :request "rep-p"
                     :stamp "2026-09-16T12:00:30Z")
    ;; the owning session sees it all
    (multiple-value-bind (line rows)
        (query-reports (kernel-state k) :since 0 :owning t)
      (declare (ignore line))
      (let ((r (first rows)))
        (ok (search "subject=node:acme/work/p" r) "the owning session reads the id: ~A" r)
        (ok (search "started a worker" r) "and the what: ~A" r)))
    ;; every other read names nothing of the node
    (multiple-value-bind (line rows) (query-reports (kernel-state k) :since 0)
      (let ((r (first rows)))
        (ok (search "subject=node:-" r) "subject=node:- on every other read: ~A" r)
        (ok (search "need=-" r) "need=-: ~A" r)
        (ok (search "what=-" r) "what=-: ~A" r)
        (ok (search "reason=-" r) "reason=-: ~A" r)
        (ok (not (search "acme/work/p" r)) "and nothing of the node: ~A" r))
      (ok (search "reports=1" line) "it is still counted in reports=: ~A" line)
      (ok (search "private=1" line) "and in the line's private=: ~A" line))))

;;; ------------------------------------------------------------------
;;; Stella's two owed implementation tests
;;;            #1580 at 191b9ab7, "Two implementation tests are owed with the
;;;            code, and are recorded here so a card carries them"
;;; ------------------------------------------------------------------

(deftest "a-reports-engagement-survives-a-clip-and-a-fresh-session-start"
    "docs/SPEC-WORK.md:5137"
    "expected=engaged-is-derived-from-events-and-rebuilt-from-them-and-nothing-held-in-memory"
  ;; The first of the two tests Stella's read of this section asks for:
  ;; "a `:launched` report, a clip, a restart, and the node still reads
  ;; `needs-broken=true`, then a `:stopped` report, a clip, a restart, and it
  ;; still reads `false` -- because *engaged* is derived from events and must be
  ;; rebuilt from them and from nothing held in memory."
  ;;
  ;; The restart here is a full canonical round-trip: the state is serialized,
  ;; parsed by a fresh reader and replayed from the seed and the journal, which
  ;; is what a clip and a fresh `session start` on that clip do to this kernel.
  (flet ((restart-of (k)
           (reconstruct-state (canonical-string (state-canonical-form (kernel-state k))))))
    (let ((k (ask-kernel)))
      (launch-report k "acme/work/d")
      (ok (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k)) "flagged before the clip")
      (let ((fresh (restart-of k)))
        (ok (node-report-engaged-p fresh "acme/work/d")
            "the engagement is rebuilt from the events alone")
        (ok (node-needs-broken fresh "acme/work/d")
            "and the node still reads needs-broken=true after a restart"))
      (stop-report k "acme/work/d")
      (ok (not (node-needs-broken (kernel-state k) "acme/work/d" :view (kernel-needs-view k)))
          "false before the second clip")
      (let ((fresh (restart-of k)))
        (ok (not (node-report-engaged-p fresh "acme/work/d"))
            "the stop is rebuilt too")
        (ok (not (node-needs-broken fresh "acme/work/d"))
            "and it still reads false after a restart")
        (check-equal 2 (length (state-reports fresh))
                     "both reports survive the round-trip")))))

(defparameter *container-cache-seed*
  '((:id "acme/work"      :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/d"    :type :task     :parent "acme/work"    :state :todo
     :deps ("acme/work/f"))
    (:id "acme/work/f"    :type :feature  :parent "acme/work"    :state :todo)
    (:id "acme/work/f/m1" :type :task     :parent "acme/work/f"  :state :doing))
  "A dependent D whose need is a container F of one member M1.")

(deftest "a-container-needs-readiness-moves-when-only-the-cache-revision-moves"
    "docs/SPEC-WORK.md:4849"
    "expected=verify-alone-with-no-mutation-of-O-moves-the-dependents-row"
  ;; The second of the two tests Stella's read asks for: "a member's evidence
  ;; verified by `verify` with no mutation of O, and the dependent's row going
  ;; from `need-unverified` to `unmet=0` on the next read -- because the cache
  ;; key *Cost* gives a rollup includes that revision and a fold keyed without
  ;; it would stay stale."
  ;;
  ;; This kernel holds no cached rollup at all: `need-met-p` derives the
  ;; container fold at read time, as rule 4 requires of every derived value, so
  ;; there is nothing to key and nothing to invalidate. The test pins the
  ;; OBSERVABLE the spec names, which is the thing a later cache must not break.
  (let* ((k (make-kernel :state (make-seed-state *container-cache-seed*)
                         :journal (make-ordering-journal) :rev-base 1))
         (evidence (gate-job-evidence "acme/work/f/m1"))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/f/m1" evidence)))))
    ;; the member settles, which settles its container with it
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "acme/work/f/m1" :by "rowan"
                        :reason "merged" :evidence '("ev-1") :request "done-m1"
                        :stamp "2026-09-16T12:00:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok okp "the member settles and its container with it: ~A" line))
    (check-equal :c (node-branch (kernel-state k) "acme/work/f")
                 "the container is in C")
    ;; the dependent reads need-unverified while the member's evidence is not
    ;; in the cache
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "the dependent is not needs-met")
      (check-string= "acme/work/f" need "and names the container")
      (check-equal :need-unverified reason "reading need-unverified"))
    (let ((revision-before (state-revision (kernel-state k))))
      ;; `verify` caches the raw fact. O is not mutated: no event, no revision.
      (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/f/m1")
      (check-equal revision-before (state-revision (kernel-state k))
                   "verify mutates no work revision")
      ;; and the dependent's row moves on the NEXT READ, with no command
      (multiple-value-bind (unmet need reason)
          (node-needs-status (kernel-state k) "acme/work/d" :view view)
        (check-equal 0 unmet "the dependent now reads unmet=0")
        (check-string= "-" need "and names no need")
        (ok (null reason) "and carries no token"))
      (ok (ready-p (kernel-state k) "acme/work/d" :view view)
          "and its row reads ready=true"))))

;;; ------------------------------------------------------------------
;;; the reports ask is bounded the way every listing is
;;;    Stella's [P2] on 6f472f1e          SPEC-WORK.md:5142
;;; ------------------------------------------------------------------

(deftest "the-reports-ask-defaults-to-twenty-and-zero-means-all"
    "docs/SPEC-WORK.md:5142"
    "expected=default-20-zero-means-all-negative-refused"
  ;; The first cut defaulted to 21 and printed 21 rows with no `MORE` line, and
  ;; `--max 0` printed ZERO rows WITH a `MORE` line -- exactly backwards.
  (let ((k (ask-kernel)))
    (dotimes (i 25)
      (report-submit k :subject (format nil "external:act ~D" i) :act :other
                       :what (format nil "hand act ~D" i)
                       :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                       :reason "a page of them" :as "rowan"
                       :request (format nil "page-~D" i)
                       :stamp "2026-09-16T12:01:00Z"))
    ;; the default caps at twenty and says there is more
    (multiple-value-bind (line rows code more) (query-reports (kernel-state k) :since 0)
      (check-equal 0 code "the default ask answers at exit 0")
      (check-equal 20 (length rows) "the default page is twenty rows")
      (ok more "and a MORE line follows it: ~A" more)
      (ok (search "reports=25" line) "while the count is the whole range: ~A" line))
    ;; --max 0 means ALL, and there is no MORE
    (multiple-value-bind (line rows code more)
        (query-reports (kernel-state k) :since 0 :max 0)
      (declare (ignore line))
      (check-equal 0 code "at exit 0")
      (check-equal 25 (length rows) "--max 0 means all")
      (ok (null more) "and prints no MORE line"))
    ;; an explicit max below the count caps and says more
    (multiple-value-bind (line rows code more)
        (query-reports (kernel-state k) :since 0 :max 3)
      (declare (ignore line))
      (check-equal 0 code "at exit 0")
      (check-equal 3 (length rows) "--max 3 prints three")
      (ok more "and a MORE line"))
    ;; an explicit max at or above the count prints them all with no MORE
    (multiple-value-bind (line rows code more)
        (query-reports (kernel-state k) :since 0 :max 25)
      (declare (ignore line))
      (check-equal 0 code "at exit 0")
      (check-equal 25 (length rows) "--max 25 prints all twenty-five")
      (ok (null more) "with no MORE line"))
    ;; a negative max is refused
    (multiple-value-bind (line rows code) (query-reports (kernel-state k) :since 0 :max -1)
      (check-equal '() rows "a negative --max prints nothing")
      (check-equal 2 code "and is refused at exit 2")
      (ok (search "--max" line) "naming the flag: ~A" line))))
