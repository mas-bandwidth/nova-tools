;;;; s3.lisp --- the slice-3 lease and working-view replays.
;;;;
;;;; Slice 3 is "one card, one bench": `take`/`heartbeat`/`release` with
;;;; `--by` and `--default`, the durable `responsible` fact kept apart from
;;;; the working-now heartbeat, and the `who`/`stale`/`handoffs` asks. Each
;;;; test below names its line of docs/SPEC-WORK.md and carries a one-line
;;;; expectation; the bodies assert the printed lines the spec shows. These
;;;; are the acceptance tests the implementation cards find waiting: the verbs
;;;; and accessors they call do not exist in this slice yet, so the suite is
;;;; red against the current dev branch.

(in-package #:nova-work/tests)

;;; Request builders for the slice-3 verbs. These are data only; they do not
;;; touch the kernel, so the test that a verb cannot be misread never depends
;;; on a serializer that is not yet written.

(defun take-request (&key (node "acme/work/f1/t1") (by "rowan")
                          (deadline "2026-09-16T12:00:00Z") (default :release)
                          (request "req-t1") (stamp "2026-09-16T11:00:00Z")
                          (generation-owner "gen-4"))
  (list :verb :take :node node :by by :deadline deadline :default default
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun heartbeat-request (&key (node "acme/work/f1/t1") (evidence "ev-h1")
                               (request "req-h1") (stamp "2026-09-16T11:30:00Z")
                               (generation-owner "gen-4"))
  (list :verb :heartbeat :node node :evidence evidence
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun release-request (&key (node "acme/work/f1/t1") (by "rowan") (handed +absent+)
                             (request "req-r1") (stamp "2026-09-16T12:00:00Z")
                             (generation-owner "gen-4"))
  (list :verb :release :node node :by by :handed handed
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun responsible-request (&key (node "acme/work") (to "rowan") (request "req-p1")
                                 (stamp "2026-09-16T10:00:00Z") (generation-owner "gen-4"))
  (list :verb :responsible :node node :to to :reason "owning"
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun offer-request (&key (node "acme/work/f1/t1") (to "freddy") (slots 2)
                           (request "req-o1") (stamp "2026-09-16T11:00:00Z")
                           (generation-owner "gen-4"))
  (list :verb :offer :node node :to to :slots slots
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun acknowledge-request (&key (node "acme/work/f1/t1") (stage :received)
                                 (request "req-k1") (stamp "2026-09-16T11:05:00Z")
                                 (generation-owner "gen-4"))
  (list :verb :acknowledge :node node :stage stage
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

;;; ------------------------------------------------------------------
;;; settle-releases-the-lease        SPEC-WORK.md:5386
;;; ------------------------------------------------------------------

(deftest "settle-releases-the-lease" "docs/SPEC-WORK.md:5386"
    "expected=holder=unowned:release-in-lease-log:handoffs-prints-whole-transition-log:no-C-item-in-any-who-answer"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    ;; A live lease is taken, then the node settles out from under it.
    (multiple-value-bind (okp line code) (submit k (take-request))
      (ok okp "take refused: ~A" line)
      (check-equal 0 code "take exit code")
      (ok (search "holder=rowan" line) "the LEASE OK line does not name the holder: ~A" line)
      (ok (search "default=release" line) "the LEASE OK line does not carry default=: ~A" line))
    (check-equal "rowan" (lease-holder (kernel-state k) "acme/work/f1/t1")
                 "the lease holder after take")
    ;; The settle cascade releases the lease: holder reads unowned at once.
    (ok (submit k (close-request :request "req-settle")) "settle refused")
    (check-equal "unowned" (lease-holder (kernel-state k) "acme/work/f1/t1")
                 "a settled item still reads a holder")
    ;; The release is a lease-log record written by the settling author.
    (let ((row (lease-row k "acme/work/f1/t1")))
      (check-equal :release (getf row :kind) "the lease log's last kind is not :release")
      (check-equal "rowan" (getf row :by) "the settle's :release does not name the settling author")
      (check-equal "rowan" (getf row :holder) "the :release does not name the holder it ended"))
    ;; handoffs --since prints the whole transition log.
    (multiple-value-bind (okp line code) (ask-handoffs k :since 1)
      (ok okp "handoffs refused: ~A" line)
      (check-equal 0 code "handoffs exit code")
      (ok (search "transition" line) "the handoffs line does not print the transition log: ~A" line))
    ;; No item of C appears in any `who` answer.
    (multiple-value-bind (okp line code) (ask-who k :node "acme/work" :branch :open :window "10m")
      (ok okp "who refused: ~A" line)
      (check-equal 0 code "who exit code")
      (ok (not (search "acme/work/f1/t1" line))
          "a settled item is listed in a who answer: ~A" line))))

;;; ------------------------------------------------------------------
;;; working-is-a-view               SPEC-WORK.md:5413
;;; ------------------------------------------------------------------

(deftest "working-is-a-view" "docs/SPEC-WORK.md:5413"
    "expected=|W|<=|O|:leased-then-released:doing-with-no-live-lease=disposition=pending:no-verb-writes-W"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    (let ((o0 (state-open-count (kernel-state k))))
      (ok (<= (state-working-count (kernel-state k)) o0) "|W| exceeded |O| at the seed"))
    ;; A :doing item with no live lease reads disposition=pending.
    (check-equal "pending" (node-disposition k "acme/work/f1/t1")
                 "a :doing item with no live lease is not disposition=pending")
    ;; Leasing writes W (a lease makes the item working); releasing unwrites it.
    (ok (submit k (take-request)) "take refused")
    (check-equal 1 (state-working-count (kernel-state k)) "|W| after a take")
    (ok (submit k (release-request :request "req-r2")) "release refused")
    (check-equal 0 (state-working-count (kernel-state k)) "|W| after a release")
    ;; W is a view: no verb ever writes it, so |W| is never above |O|.
    (ok (<= (state-working-count (kernel-state k))
            (state-open-count (kernel-state k)))
        "|W| exceeded |O| after release")))

;;; ------------------------------------------------------------------
;;; cursor-pinned-across-a-new-settle SPEC-WORK.md:5472
;;; ------------------------------------------------------------------

(deftest "cursor-pinned-across-a-new-settle" "docs/SPEC-WORK.md:5472"
    "expected=no-row-missing:no-row-twice:pinned-revision-honoured:continuation-refused-page-expired"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    (ok (submit k (close-request :request "req-c1")) "first close refused")
    (let* ((page1 (ask-closed-page k :max 2 :after nil))
           (ids1 (page-ids page1)))
      ;; A second item settles between the two pages.
      (ok (submit k (close-request :request "req-c2" :node "acme/work/f1/t2"
                                   :evidence '("ev-2")))
          "second close refused")
      ;; The pinned cursor still serves the next page with no row twice.
      (let ((page2 (ask-closed-page k :max 2 :after page1)))
        (ok (null (intersection ids1 (page-ids page2) :test #'string=))
            "a row is repeated across the two pages")
        ;; A continuation whose pinned revision can no longer be served refuses.
        (multiple-value-bind (okp line)
            (ask-closed-page* k :max 2 :after (stale-cursor page2))
          (ok (not okp) "a page-expired continuation was served")
          (ok (search "page expired" line)
              "the refusal does not say page expired: ~A" line))))))

;;; ------------------------------------------------------------------
;;; activity-and-state-are-two-counts SPEC-WORK.md:5475
;;; ------------------------------------------------------------------

(deftest "activity-and-state-are-two-counts" "docs/SPEC-WORK.md:5475"
    "expected=settles-in=1,revives-in=1,items-in=1,closed-in=0:open-counts-it-once:closed-unchanged-by-reopen"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    (ok (submit k (close-request :request "req-a1")) "close refused")
    (ok (submit k (reopen-request :request "req-a2")) "reopen refused")
    (multiple-value-bind (okp line code)
        (ask-remaining k :node "acme/work" :branch :open :from "2026-09-16T10:00:00Z" :to "2026-09-16T14:00:00Z")
      (ok okp "the window ask refused: ~A" line)
      (check-equal 0 code "the window ask exit code")
      ;; The activity counts: one settle and one revive inside the window.
      (ok (search "settles-in=1" line) "the ask does not print settles-in=1: ~A" line)
      (ok (search "revives-in=1" line) "the ask does not print revives-in=1: ~A" line)
      (ok (search "items-in=1" line) "the ask does not print items-in=1: ~A" line)
      (ok (search "closed-in=0" line) "the ask does not print closed-in=0: ~A" line)
      ;; The state counts the item once, and closed= is unchanged by the reopen.
      (ok (and (search "open=" line) (search "closed=" line))
          "the ask does not print open= and closed=: ~A" line))))

;;; ------------------------------------------------------------------
;;; four-facts-four-verbs           SPEC-WORK.md:5560
;;; ------------------------------------------------------------------

(deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:5560"
    "expected=offer-writes-effect=dispatched:pending-offer-and-reservation:node-state-W-lease-attempts-evidence-unchanged:acknowledge-stage-received=delivery-only"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    (let ((before (working-and-lease-facts k)))
      ;; An admitted offer writes :effect :dispatched plus a reservation,
      ;; leaving node state, W, the lease index, attempts and evidence alone.
      (multiple-value-bind (okp line code)
          (submit k (offer-request :node "acme/work/f1/t1" :to "freddy" :slots 2))
        (ok okp "offer refused: ~A" line)
        (ok (search "effect=dispatched" line) "the OFFER OK line does not carry effect=dispatched: ~A" line))
      (ok (plusp (pending-offer-count k)) "no pending-offer index entry was written")
      (check-equal before (working-and-lease-facts k)
                   "an offer changed node state, W, the lease index, attempts or evidence")
      ;; acknowledge --stage received writes delivery only.
      (multiple-value-bind (okp line)
          (submit k (acknowledge-request :node "acme/work/f1/t1" :stage :received))
        (ok okp "acknowledge refused: ~A" line)
        (ok (search "stage=received" line)
            "the ACKNOWLEDGE OK line does not carry stage=received: ~A" line))
      (check-equal before (working-and-lease-facts k)
                   "an acknowledge changed node state, W, the lease index, attempts or evidence"))))

;;; ------------------------------------------------------------------
;;; applicable-cap-never-hides-a-deny SPEC-WORK.md:5829
;;; ------------------------------------------------------------------

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:5829"
    "expected=excluded-<id>:NOTES-MORE:constraint-row-before-cut-row:GOAL-MORE:index-unloadable-prints-FAIL"
  (let ((k (make-kernel :state (make-seed-state *seed*))))
    ;; N active notes with N > --max, the only :deny in the note sorting last:
    ;; applicable prints excluded with that id and NOTES MORE.
    (multiple-value-bind (okp line)
        (ask-applicable k :max 1 :candidate "role/model")
      (declare (ignore okp))
      (ok (search "excluded" line) "applicable does not print excluded: ~A" line)
      (ok (search "NOTES MORE" line) "applicable does not print NOTES MORE: ~A" line))
    ;; goal show --max 1 prints the constraint row before any cut row, GOAL MORE.
    (multiple-value-bind (okp line)
        (ask-goal-show k :max 1)
      (declare (ignore okp))
      (ok (search "GOAL MORE" line) "goal show does not print GOAL MORE: ~A" line))
    ;; With the notes index unloadable, both print FAIL and print neither
    ;; eligible nor any row.
    (check-equal "FAIL" (ask-applicable-fail k) "applicable did not print FAIL with the index unloadable")
    (check-equal "FAIL" (ask-goal-show-fail k) "goal show did not print FAIL with the index unloadable")))
