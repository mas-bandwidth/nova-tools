;;;; replays-785-report.lisp --- the hand report (nova-tools #854 item 7).
;;;;
;;;; Rules 7 and 8 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:5033-5112, with replays `report-records-and-changes-nothing`
;;;; (:6981) and `report-refuses-by-name` (:6989).
;;;;
;;;; Rule 7's line is the whole reason this verb exists: where a hand act's
;;;; effect is tree state it is done by that field's typed verb, and where it
;;;; lies outside the tree the tree cannot do it and cannot undo it, so what it
;;;; holds is a record that it happened. The typed verbs change state; the one
;;;; report verb changes none.

(in-package #:nova-work/tests)

(defparameter *report-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing))
  "D depends on the open N, so a node subject has an unmet need to print.")

(defun report-kernel (&optional (seed *report-seed*))
  (let ((k (make-kernel :state (make-seed-state seed)
                        :journal (make-ordering-journal) :rev-base 1
                        :friends '("rowan" "emma"))))
    (submit k (list :verb :machine :change :register :machine "space"
                    :name "space" :owner "rowan" :connect "profile:space"
                    :roles (list :build :test) :permits (list "go-test")
                    :excludes (quote ()) :limits (list :concurrent 4)
                    :facts nil :declared-by "rowan"
                    :request "reg-space" :stamp "2026-09-16T10:00:00Z"
                    :clock :tool :generation-owner "gen-1"))
    k))

(defun a-report (k &rest overrides)
  (apply #'report-submit k
         (append overrides
                 (list :subject "machine:space" :act :other
                       :what "restarted the agent service on the bench"
                       :acted-at "2026-09-16T12:00:00Z"
                       :instead-of "-"
                       :reason "the queue had stopped draining"
                       :as "rowan" :request "rep-1"
                       :stamp "2026-09-16T12:01:30Z"))))

;;; ------------------------------------------------------------------
;;; report-records-and-changes-nothing           SPEC-WORK.md:5052, :6981
;;; ------------------------------------------------------------------

(deftest "report-records-and-changes-nothing" "docs/SPEC-WORK.md:5052"
    "expected=one-event-six-fields-in-order-and-not-one-count-moves"
  (let* ((k (report-kernel))
         (state (kernel-state k))
         (before-open (state-open-count state))
         (before-closed (state-closed-count state))
         (before-leases (length (state-lease-log state)))
         (before-holder (node-holder state "acme/work/d"))
         (before-state (node-state state "acme/work/d")))
    ;; `--now` is the acted-at plus 90s, so lag= is exact
    (multiple-value-bind (okp line code) (a-report k)
      (ok okp "the report is recorded: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "subject=machine:space" line) "subject=: ~A" line)
      (ok (search "act=other" line) "act=: ~A" line)
      (ok (search "instead-of=-" line) "instead-of=: ~A" line)
      (ok (search "acted-at=2026-09-16T12:00:00Z" line) "acted-at=: ~A" line)
      (ok (search "lag=90s" line) "lag= is the stamp less the acted-at: ~A" line)
      (ok (search "unmet=- need=- holder=-" line)
          "a non-node subject prints all three as -: ~A" line))
    ;; one :report event, :node (:absent), its six fields in order
    (let ((events (state-reports (kernel-state k))))
      (check-equal 1 (length events) "the journal gains one :report event")
      (let ((e (first events)))
        (ok (absentp (getf e :node)) ":node is (:absent) on every :report")
        (check-equal '(:act :subject :what :acted-at :instead-of :reason)
                     (kind-fields :report)
                     "its six fields, in the order the payload digest serializes them")
        (check-equal :other (getf e :act) ":act")
        (check-string= "rowan" (getf e :by) "the reporter is the event's :by")))
    ;; nothing moved
    (let ((after (kernel-state k)))
      (check-equal before-open (state-open-count after) "|O| is unchanged")
      (check-equal before-closed (state-closed-count after) "|C| is unchanged")
      (check-equal before-leases (length (state-lease-log after))
                   "every lease is unchanged")
      (check-equal before-holder (node-holder after "acme/work/d") "and W with them")
      (check-equal before-state (node-state after "acme/work/d")
                   "no transition is written"))
    ;; a retry of the request id prints the original line
    (multiple-value-bind (okp line) (a-report k)
      (ok okp "a retry is admitted")
      (ok (search "REPORT OK" line) "and prints the original line: ~A" line))
    (check-equal 1 (length (state-reports (kernel-state k)))
                 "and writes no second event")
    ;; a changed payload under the same id is refused
    (multiple-value-bind (okp line code) (a-report k :what "something else")
      (ok (not okp) "a changed payload under the same request id is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "reused with a different payload" line) "by name: ~A" line))
    ;; an `undo` of it is refused `not reversible here`
    (multiple-value-bind (okp line code)
        (submit k (list :verb :undo :of "rep-1"
                        :at-rev (getf (first (state-reports (kernel-state k))) :rev)
                        :by "rowan"
                        :request "undo-rep-1" :stamp "2026-09-16T13:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok (not okp) "an undo of a report is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "not reversible here" line) "not reversible here: ~A" line))))

;;; ------------------------------------------------------------------
;;; :unmet and :need are outside the payload digest
;;;                                              SPEC-WORK.md:5071
;;; ------------------------------------------------------------------

(deftest "a-reports-session-half-is-outside-its-payload-digest"
    "docs/SPEC-WORK.md:5071"
    "expected=two-serializers-digest-it-to-one-value-with-unmet-and-need-outside"
  (let ((k (report-kernel)))
    ;; a node subject carries the count and the first unmet need
    (multiple-value-bind (okp line)
        (report-submit k :subject "node:acme/work/d" :act :other
                         :what "copied a key file to the bench"
                         :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                         :reason "the worker could not read it"
                         :as "rowan" :request "rep-node"
                         :stamp "2026-09-16T12:00:30Z")
      (ok okp "a node subject is admitted: ~A" line)
      (ok (search "unmet=1" line) "and carries the count: ~A" line)
      (ok (search "need=acme/work/n" line) "and the first unmet need: ~A" line)
      (ok (search "holder=unowned" line) "and the holder: ~A" line))
    ;; the digest is taken over the six payload fields only
    (let* ((e (first (state-reports (kernel-state k))))
           (event (record-form->event e))
           (same (make-work-event
                  :kind :report :node +absent+ :by (getf e :by)
                  :fields (list :act (getf e :act) :subject (getf e :subject)
                                :what (getf e :what) :acted-at (getf e :acted-at)
                                :instead-of (getf e :instead-of)
                                :reason (getf e :reason)
                                ;; a different session half
                                :unmet 99 :need "somewhere/else")
                  :stamp (getf e :stamp) :clock (getf e :clock)
                  :request (getf e :request)
                  :generation-owner (getf e :generation-owner)
                  :rev (getf e :rev))))
      (check-string= (payload-digest (list event)) (payload-digest (list same))
                     "two serializers digest one report to one value")
      ;; but the durable record keeps them
      (check-equal 1 (getf e :unmet) "the record carries :unmet")
      (check-string= "acme/work/n" (getf e :need) "and :need"))))

;;; ------------------------------------------------------------------
;;; report-refuses-by-name                       SPEC-WORK.md:5089, :6989
;;; ------------------------------------------------------------------

(deftest "report-refuses-by-name" "docs/SPEC-WORK.md:5089"
    "expected=exit-2-for-an-invocation-exit-1-by-name-and-never-an-event"
  (let ((k (report-kernel)))
    ;; each of the six flags missing: exit 2, one line ending `run: nova-work help`
    (dolist (missing '(:subject :act :what :acted-at :instead-of :reason))
      (multiple-value-bind (okp line code)
          (apply #'report-submit k (list missing nil))
        (ok (not okp) "a missing ~A is refused" missing)
        (check-equal 2 code "at exit 2")
        (ok (search "run: nova-work help" line)
            "on one line ending run: nova-work help: ~A" line)))
    ;; an --act outside the three
    (multiple-value-bind (okp line code) (a-report k :act :rebooted)
      (ok (not okp) "an act outside the three is refused")
      (check-equal 2 code "at exit 2")
      (ok (search "launched, stopped or other" line) "naming the three: ~A" line))
    ;; a --subject with no colon, and one of an unknown kind
    (dolist (bad '("space" "bench:space"))
      (multiple-value-bind (okp line code) (a-report k :subject bad)
        (ok (not okp) "~S is refused" bad)
        (check-equal 2 code "at exit 2")
        (ok (search "run: nova-work help" line) "by the invocation line: ~A" line)))
    ;; whitespace-only --what, --reason, --instead-of and external: text
    (dolist (case '((:what "   ") (:reason "  ") (:instead-of " ")
                    (:subject "external:   ")))
      (multiple-value-bind (okp line code) (apply #'a-report k case)
        (ok (not okp) "a whitespace-only ~A is refused" (first case))
        (check-equal 2 code "at exit 2")
        (ok (search "run: nova-work help" line) "as an invocation: ~A" line)))
    ;; a stamp that is not RFC 3339 UTC
    (multiple-value-bind (okp line code) (a-report k :acted-at "16 September 2026")
      (ok (not okp) "a stamp that is not RFC 3339 UTC is refused")
      (check-equal 2 code "at exit 2")
      (ok (search "RFC 3339" line) "by name: ~A" line))
    ;; exit 1: a subject naming no node, machine, friend, route or offer
    (dolist (case '(("node:acme/work/nowhere" "no such node")
                    ("machine:hulk" "no such machine")
                    ("friend:nobody" "no such friend")
                    ("route:none" "no such route")
                    ("offer:none" "no such offer")))
      (multiple-value-bind (okp line code) (a-report k :subject (first case))
        (ok (not okp) "~A is refused" (first case))
        (check-equal 1 code "at exit 1")
        (ok (search (second case) line) "by name: ~A" line)))
    ;; exit 1: an --as that is no friend of `friends`
    (multiple-value-bind (okp line code) (a-report k :as "stranger")
      (ok (not okp) "an unregistered reporter is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "unknown reporter" line) "by name: ~A" line))
    ;; exit 1: an --acted-at ahead of the clock past --skew
    (multiple-value-bind (okp line code)
        (a-report k :acted-at "2026-09-16T13:00:00Z" :skew "30s")
      (ok (not okp) "an acted-at ahead of the clock past --skew is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "acted-at ahead of the clock" line) "by name: ~A" line))
    ;; exit 1: a stale --expect, by its own line
    (multiple-value-bind (okp line code) (a-report k :expect 99)
      (ok (not okp) "a stale --expect is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "expect=99 current=" line) "by its own line: ~A" line)
      (ok (search ": stale" line) "which ends `stale`: ~A" line))
    ;; and in every case: no event, no journal record, no dedup entry
    (check-equal '() (state-reports (kernel-state k))
                 "not one refusal wrote an event")))

;;; ------------------------------------------------------------------
;;; the whole report mutation happens inside the single writer
;;;    Stella's [P1] on 12236baa          SPEC-WORK.md:5052
;;; ------------------------------------------------------------------

(deftest "a-report-is-one-command-in-the-writers-total-order"
    "docs/SPEC-WORK.md:5052"
    "expected=unique-monotone-revisions-authoritative-facts-correct-replay-atomic-failure"
  ;; The first cut prepared the event, took its revision from `kernel-next-rev`
  ;; and then asked the journal for acceptance -- all on the CALLER's thread.
  ;; With the prepared report paused before acceptance, a normal `submit`
  ;; committed revisions 1 and 2; the report then resumed, wrote revision 1
  ;; again and reset `next-rev` to 2. Its `:unmet` and `:need` were read at the
  ;; same stale moment.
  ;;
  ;; UNIQUE, MONOTONE REVISIONS under concurrency.
  (let* ((k (report-kernel))
         (threads '()))
    (dotimes (i 8)
      (let ((n i))
        (push (sb-thread:make-thread
               (lambda ()
                 (report-submit k :subject "machine:space" :act :other
                                  :what (format nil "hand act ~D" n)
                                  :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                                  :reason "concurrency" :as "rowan"
                                  :request (format nil "conc-~D" n)
                                  :stamp "2026-09-16T12:01:30Z"))
               :name (format nil "reporter-~D" n))
              threads)))
    (dolist (thread threads) (sb-thread:join-thread thread :default nil))
    (let* ((events (state-reports (kernel-state k)))
           (revs (mapcar (lambda (e) (getf e :rev)) events)))
      (check-equal 8 (length events) "every concurrent report was written")
      (check-equal 8 (length (remove-duplicates revs))
                   "each took a revision of its own: no two share one")
      (check-equal (sort (copy-list revs) #'<) revs
                   "and the journal holds them in revision order")))
  ;; FACTS APPLIED AGAINST THE AUTHORITATIVE STATE: a report's node half is read
  ;; on the writer, so it cannot be stale with respect to a settle that the
  ;; writer applied before it.
  (let ((k (report-kernel)))
    (submit k (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                    :reason "start" :request "n-doing"
                    :stamp "2026-09-16T11:00:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (submit k (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                    :reason "merged" :evidence (list "ev-1") :request "n-done"
                    :stamp "2026-09-16T11:30:00Z" :clock :tool
                    :generation-owner "gen-1"))
    (setf (kernel-needs-view k) (session-needs-view k))
    (multiple-value-bind (okp line)
        (report-submit k :subject "node:acme/work/d" :act :launched
                         :what "started a worker" :acted-at "2026-09-16T12:00:00Z"
                         :instead-of "-" :reason "after the settle" :as "rowan"
                         :request "after-settle" :stamp "2026-09-16T12:00:30Z")
      (ok okp "the report is written: ~A" line)
      (ok (search "unmet=0 need=-" line)
          "and its diagnostics are read at the revision it was applied at: ~A" line)))
  ;; CORRECT REPLAY: a retransmission answers the original receipt and writes
  ;; no second event or revision.
  (let ((k (report-kernel)))
    (multiple-value-bind (okp first-line) (a-report k)
      (ok okp "the first report: ~A" first-line)
      (let ((rev (state-revision (kernel-state k))))
        (multiple-value-bind (okp again) (a-report k)
          (ok okp "the retransmission is admitted")
          (check-string= first-line again "and answers the original receipt"))
        (check-equal rev (state-revision (kernel-state k))
                     "and takes no second revision")
        (check-equal 1 (length (state-reports (kernel-state k)))
                     "and writes no second event"))))
  ;; ATOMIC FAILURE: a refusal takes no revision and leaves no event.
  (let* ((k (report-kernel))
         (rev (state-revision (kernel-state k))))
    (multiple-value-bind (okp line code) (a-report k :as "stranger")
      (declare (ignore line))
      (ok (not okp) "an unknown reporter is refused")
      (check-equal 1 code "at exit 1"))
    (check-equal rev (state-revision (kernel-state k))
                 "the refusal took no revision")
    (check-equal '() (state-reports (kernel-state k))
                 "and left no event")))

;;; ------------------------------------------------------------------
;;; the report's replay answer, and its ONE durable record
;;;    Stella's [P2] on 1a11652d applied here, and her [P1] on 1a558076
;;;    checked for before she reads this slice
;;; ------------------------------------------------------------------

(defun report-file-kernel (path &key (seed *report-seed*))
  (let* ((state (make-seed-state seed))
         (k (make-kernel :state state
                         :journal (open-file-journal
                                   path :initial-state-hash (root-digest state))
                         :rev-base 1
                         :friends (list "rowan" "emma"))))
    (submit k (list :verb :machine :change :register :machine "space"
                    :name "space" :owner "rowan" :connect "profile:space"
                    :roles (list :build :test) :permits (list "go-test")
                    :excludes '() :limits (list :concurrent 4)
                    :facts nil :declared-by "rowan"
                    :request "reg-space" :stamp "2026-09-16T10:00:00Z"
                    :clock :tool :generation-owner "gen-1"))
    k))

(deftest "a-report-replays-before-any-state-is-read-and-records-once"
    "docs/SPEC-WORK.md:315"
    "expected=one-record-per-report-and-an-identical-retry-answers-it-whatever-moved"
  ;; Stella asked that this path not repeat the receipt-after-apply pattern she
  ;; found in `dep`, and that the replay answer come before mutable state on
  ;; EVERY command on this path. Both checked here, against the REAL file
  ;; journal, before she reads the slice.
  (let* ((path (test-journal-path "report-file-journal"))
         (k (report-file-kernel path)))
    (unwind-protect
         (progn
           ;; an ordinary report: one record, the complete receipt
           (multiple-value-bind (okp original code)
               (report-submit k :subject "node:acme/work/d" :act :launched
                                :what "started a worker from a shell"
                                :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                                :reason "the card was already cut" :as "rowan"
                                :request "file-report"
                                :stamp "2026-09-16T12:00:30Z")
             (ok okp "the report is written against a real journal: ~A" original)
             (check-equal 0 code "at exit 0")
             (ok (search "REPORT OK" original) "with the complete receipt: ~A" original)
             (ok (search "unmet=1" original) "and its node half: ~A" original)
             (ok (not (search "MUTATION FAIL" original)) "and no mutation failure")
             ;; the state moves: the need settles, so a FRESH report would read
             ;; unmet=0 -- but an identical replay must answer the ORIGINAL
             (submit k (list :verb :state-to-doing :node "acme/work/n" :by "rowan"
                             :reason "start" :request "n-doing"
                             :stamp "2026-09-16T12:10:00Z" :clock :tool
                             :generation-owner "gen-1"))
             (submit k (list :verb :state-to-done :node "acme/work/n" :by "rowan"
                             :reason "merged" :evidence (list "ev-1")
                             :request "n-done" :stamp "2026-09-16T12:20:00Z"
                             :clock :tool :generation-owner "gen-1"))
             (let ((events (length (state-reports (kernel-state k))))
                   (rev (state-revision (kernel-state k))))
               (multiple-value-bind (okp replayed code)
                   (report-submit k :subject "node:acme/work/d" :act :launched
                                    :what "started a worker from a shell"
                                    :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                                    :reason "the card was already cut" :as "rowan"
                                    :request "file-report"
                                    :stamp "2026-09-16T12:00:30Z")
                 (ok okp "the identical request replays")
                 (check-equal 0 code "at exit 0")
                 (check-string= original replayed
                                "to the original receipt, with its original unmet="))
               (check-equal events (length (state-reports (kernel-state k)))
                            "and writes no second event")
               (check-equal rev (state-revision (kernel-state k))
                            "and takes no second revision")))
           ;; a different payload under the same id is refused
           (multiple-value-bind (okp line code)
               (report-submit k :subject "node:acme/work/d" :act :stopped
                                :what "something else"
                                :acted-at "2026-09-16T12:00:00Z" :instead-of "-"
                                :reason "different" :as "rowan"
                                :request "file-report"
                                :stamp "2026-09-16T12:00:30Z")
             (ok (not okp) "a different payload under the same id is refused")
             (check-equal 1 code "at exit 1")
             (ok (search "reused with a different payload" line) "by name: ~A" line))
           ;; close, reopen, replay: the reports come back
           (close-file-journal (kernel-journal k))
           (let* ((state (make-seed-state *report-seed*))
                  (j2 (open-file-journal path :initial-state-hash (root-digest state))))
             (unwind-protect
                  (let* ((k2 (make-kernel :state state :journal j2 :rev-base 1))
                         (replayed (progn (replay-journal j2 k2) (kernel-state k2))))
                    (check-equal 1 (length (state-reports replayed))
                                 "the report replays from the durable record")
                    (let ((e (first (state-reports replayed))))
                      (check-equal :launched (getf e :act) "with its act")
                      (check-equal 1 (getf e :unmet)
                                   "and the session half it was written with")))
               (close-file-journal j2))))
      (ignore-errors (close-file-journal (kernel-journal k))))))
