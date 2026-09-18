;;;; replays-8648.lisp --- five acceptance replays named by docs/SPEC-WORK.md.
;;;;
;;;; Each deftest names the paragraph(s) it comes from and drives the pure
;;;; model the kernel exposes for it. The five:
;;;;
;;;;   regression-opens-repair-work                     :4943-4951,5782-5785
;;;;   reply-retired-only-under-verified-coverage       :6020-6024,6316-6325
;;;;   restore-is-isolated-and-dispatches-nothing       :6285-6290,5790-5793
;;;;   reuse-only-valid-review                          :4851
;;;;   review-cycles-stay-visible                       :6368-6370

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; regression-opens-repair-work            SPEC-WORK.md:4943-4951,5782-5785
;;; ------------------------------------------------------------------
;;;
;;; "When code, criteria or dependencies change, the past completion and its
;;; receipts are retained, the affected current-verification summaries are
;;; invalidated, and the answer says a recheck is needed ... A confirmed
;;; regression creates linked open repair work under the ordinary policy."

(deftest "regression-opens-repair-work" "docs/SPEC-WORK.md:4943-4951,5782-5785"
    "expected=history-retained;recheck-needed;no-silent-reopen;linked-open-repair-work"
  (let* ((summary (make-verification-summary
                   :node "acme/work/f1/t1"
                   :pinned-revision 7
                   :current-revision 7
                   :state :verified
                   :history '("settled at rev 7")))
         ;; code/criteria changed: the historic tick stays at its pinned
         ;; revision, the current summary needs a recheck, and nothing is erased.
         (after (invalidate-verification summary 11
                                         :reason :source-change)))
    (check-equal 7 (verification-summary-pinned-revision after)
                 "the historic tick is retained at its pinned revision")
    (check-equal 11 (verification-summary-current-revision after)
                 "the current view moves to the new revision")
    (check-equal '("settled at rev 7") (verification-summary-history after)
                 "history is retained, never erased")
    (ok (recheck-needed-p after) "the answer says a recheck is needed")
    ;; it does not silently reopen the closed task because evidence went stale.
    (ok (not (eq :doing (verification-summary-state after)))
        "a stale receipt never silently reopens the closed task")
    ;; a confirmed regression creates linked open repair work under the ordinary
    ;; policy, and the closed node stays closed.
    (let ((repair (confirm-regression after :id "acme/work/f1/t1-repair")))
      (check-equal :todo (repair-work-state repair)
                   "the repair work is open under the ordinary policy")
      (check-equal "acme/work/f1/t1" (repair-work-node repair)
                   "the repair work is linked to the regressed node")
      (ok (not (eq :doing (verification-summary-state after)))
          "confirming the regression did not reopen the original node"))))

;;; ------------------------------------------------------------------
;;; reply-retired-only-under-verified-coverage  SPEC-WORK.md:6020-6024,6316-6325
;;; ------------------------------------------------------------------
;;;
;;; "a disposition whose events lie before the clip boundary is retired to the
;;; dedup index's `already applied` only once the committed snapshot, its
;;; retained events and the dedup root that boundary record names are verified
;;; reachable ... until then the original `OK` still answers or `recovery-gap
;;; kind=coverage-unverified` prints, and a reply is never deleted, never
;;; invented".

(deftest "reply-retired-only-under-verified-coverage"
    "docs/SPEC-WORK.md:6020-6024,6316-6325"
    "expected=retire-only-when-reachable;original-ok-retained;coverage-gap-named;never-invented"
  (let ((disposition (make-retained-disposition
                      :request "req-1" :payload-digest "sha256:aaa"
                      :sequence 420 :record-hash "sha256:bbb"
                      :reply "NODE OK id=acme/work/f1/t1 rev=8"
                      :boundary "clip-390"))
        (covered (make-coverage :snapshot-reachable t :retained-events-reachable t
                                :dedup-root-reachable t :push-ok t)))
    ;; fully verified coverage: the retry is answered `already applied`.
    (multiple-value-bind (verdict line gaps) (retire-reply disposition covered)
      (check-equal :already-applied verdict
                   "a fully covered reply is retired to already applied")
      (check-equal "already applied" line "the dedup index answers already applied")
      (check-equal nil gaps "verified coverage prints no gap"))
    ;; a commit that exists locally is not a push that succeeded: the original
    ;; OK still answers from the retained disposition.
    (let ((push-failed (make-coverage :snapshot-reachable t :retained-events-reachable t
                                      :dedup-root-reachable t :push-ok nil)))
      (multiple-value-bind (verdict line gaps) (retire-reply disposition push-failed)
        (check-equal :retained verdict "a failed push retires nothing")
        (check-string= (retained-disposition-reply disposition) line
                       "the original OK still answers from the retained disposition")
        (check-equal nil gaps "a reachable-but-unpushed reply prints no coverage gap")))
    ;; an unreadable dedup root prints the coverage gap, never an invented reply.
    (let ((root-unreadable (make-coverage :snapshot-reachable t :retained-events-reachable t
                                          :dedup-root-reachable nil :push-ok t)))
      (multiple-value-bind (verdict line gaps) (retire-reply disposition root-unreadable)
        (check-equal :retained verdict "an unreadable root retires nothing")
        (check-string= (retained-disposition-reply disposition) line
                       "the original reply is never deleted or invented")
        (ok (member "recovery-gap kind=coverage-unverified" gaps :test #'string=)
            "the coverage gap is printed by its kind: ~S" gaps)))
    ;; the retained disposition keeps every identity field even before the cut.
    (check-equal 420 (retained-disposition-sequence disposition)
                 "the accepted record's sequence is retained")
    (check-equal "clip-390" (retained-disposition-boundary disposition)
                 "the boundary record is retained")
    ;; a dedup root that is reachable but is not the bytes the boundary record
    ;; names does not retire the reply: the gap prints instead.
    (let ((wrong-root (make-coverage :snapshot-reachable t :retained-events-reachable t
                                     :dedup-root-reachable t :push-ok t
                                     :boundary-root-digest "sha256:bbb"
                                     :observed-root-digest "sha256:zzz")))
      (multiple-value-bind (verdict line gaps) (retire-reply disposition wrong-root)
        (check-equal :retained verdict "a root that is not the boundary's retires nothing")
        (check-string= (retained-disposition-reply disposition) line
                       "the original OK still answers for a mismatched root")
        (ok (member "recovery-gap kind=coverage-unverified" gaps :test #'string=)
            "a mismatched root prints the coverage gap by its kind: ~S" gaps)))
    ;; the ledger keeps the disposition; retirement never deletes the reply.
    (let* ((ledger (receipt-ledger-record (make-receipt-ledger) "req-1" disposition))
           (kept (receipt-ledger-lookup ledger "req-1")))
      (ok (receipt-ledger-retains-p ledger "req-1")
          "the ledger retains the accepted request's disposition")
      (check-equal "sha256:aaa" (retained-disposition-payload-digest kept)
                   "the ledger copy keeps the payload digest")
      (check-equal 420 (retained-disposition-sequence kept)
                   "the ledger copy keeps the accepted sequence")
      (check-string= (retained-disposition-reply disposition)
                     (retained-disposition-reply kept)
                     "the ledger copy keeps the original reply byte for byte"))))

;;; ------------------------------------------------------------------
;;; restore-is-isolated-and-dispatches-nothing  SPEC-WORK.md:6285-6290,5790-5793
;;; ------------------------------------------------------------------
;;;
;;; "A restore opens a read-only, isolated, non-dispatching recovery session:
;;; it inherits no coordinator ownership, reanimates no assignment, replays no
;;; bus message and duplicates no external side effect, and a selected repair is
;;; promoted only through a fenced validated reconciliation with the current
;;; state."

(deftest "restore-is-isolated-and-dispatches-nothing"
    "docs/SPEC-WORK.md:6285-6290,5790-5793"
    "expected=read-only;owns-nothing;dispatches-nothing;replays-nothing;fenced-promotion-only;records-after-cut-once;missing-reply-refused"
  (let* ((records (list (list :seq 1 :request "r1" :reply '("OK r1") :payload-sha256 "p1" :events '(1))
                        (list :seq 2 :request "r2" :reply '("OK r2") :payload-sha256 "p2" :events '(2 3))
                        (list :seq 3 :request "r3" :reply '("OK r3") :payload-sha256 "p3" :events '(4))))
         (journal (make-journal-chain
                   :id "journal-abc"
                   :segments (list (make-journal-segment :path "journal.1"
                                                         :header '() :records '()))))
         (store (savepoint-write (make-savepoint-store) "sp-1" 3 3 records :journal journal))
         (sp (savepoint-store-verified store))
         (image (savepoint-image-events 3 records))
         (replies (savepoint-retained-replies records)))
    ;; a real savepoint restores as one isolated recovery session.
    (multiple-value-bind (session line code)
        (savepoint-restore (make-savepoint-load :savepoint sp :journal journal
                                                :image image :replies replies
                                                :records records))
      (ok session "a real savepoint restores: ~A" line)
      (check-equal 0 code "a restore is exit 0")
      (ok (restore-session-read-only-p session) "a restore is read-only")
      (check-equal nil (restore-session-ownership session)
                   "a restore inherits no coordinator ownership")
      (check-equal nil (restore-session-assignments session)
                   "a restore reanimates no assignment")
      (check-equal 0 (restore-session-dispatch-count session)
                   "a restore dispatches nothing")
      (check-equal nil (restore-session-replayed-messages session)
                   "a restore replays no bus message")
      (check-equal nil (restore-session-external-effects session)
                   "a restore duplicates no external side effect")
      ;; the image and its retained replies are loaded, and only the complete
      ;; records strictly after the cut replay once in sequence.
      (check-equal image (restore-session-image session) "the image is loaded")
      (check-equal replies (restore-session-replies session)
                   "the retained replies are loaded")
      (check-equal '(3)
                   (mapcar (lambda (r) (getf r :seq))
                           (restore-session-replayed-records session))
                   "only the complete records strictly after the cut replay once")
      ;; a dispatch through the isolated session is refused outright.
      (multiple-value-bind (ok line code) (restore-dispatch session :node "acme/work/f1/t1")
        (check-equal nil ok "the isolated restore refuses a dispatch")
        (check-equal 2 code "the refusal is exit 2")
        (ok (search "isolated" line) "the refusal names the isolation: ~A" line))
      ;; a repair is promoted only through a fenced validated reconciliation.
      (multiple-value-bind (ok line code) (promote-repair session :repair)
        (check-equal nil ok "an unfenced promotion is refused")
        (check-equal 2 code "the refusal is exit 2")
        (ok (search "fenced" line) "the refusal names the fence: ~A" line))
      (multiple-value-bind (ok line code)
          (promote-repair session :repair :fenced t :validated t :reconciled t)
        (ok ok "a fenced validated reconciliation promotes the repair: ~A" line)
        (check-equal 0 code "a successful promotion is exit 0")))
    ;; a savepoint-load whose disposition list lost a reply refuses the restore
    ;; and exposes no session.
    (let ((holed (mapcar (lambda (d) (if (equal "r2" (getf d :request))
                                         (list :request "r2" :payload-sha256 "p2"
                                               :sequence 2 :record-sha256 (getf d :record-sha256)
                                               :reply nil)
                                         d))
                         replies)))
      (multiple-value-bind (session line code)
          (savepoint-restore (make-savepoint-load :savepoint sp :journal journal
                                                  :image image :replies holed
                                                  :records records))
        (check-equal nil session "a restore missing a retained reply exposes nothing")
        (check-equal 2 code "the refusal is exit 2")
        (ok (search "recovery-gap kind=missing-reply" line)
            "the missing reply is named by its kind: ~A" line)))))

;;; ------------------------------------------------------------------
;;; reuse-only-valid-review                 SPEC-WORK.md:4851
;;; ------------------------------------------------------------------
;;;
;;; "Same-scope review is reusable; changed acceptance/dependencies invalidate
;;; it; independent friend gates cannot be replaced by reuse."

(deftest "reuse-only-valid-review" "docs/SPEC-WORK.md:4851"
    "expected=same-scope-reused;changed-acceptance-invalidates;changed-deps-invalidate;friend-gate-not-reusable"
  (let ((review (make-scope-review :friend "stella" :scope '(:node "acme/work/f1")
                                   :head "abc123"
                                   :acceptance '("criterion-1")
                                   :dependencies '("dep-1")
                                   :verdict :approved)))
    ;; same scope, same head, acceptance and dependencies unchanged: reusable.
    (ok (scope-review-reusable-p review :scope '(:node "acme/work/f1") :head "abc123"
                                 :acceptance '("criterion-1") :dependencies '("dep-1"))
        "an unchanged same-scope review is reusable")
    ;; changed acceptance invalidates it.
    (ok (not (scope-review-reusable-p review :scope '(:node "acme/work/f1") :head "abc123"
                                      :acceptance '("criterion-2") :dependencies '("dep-1")))
        "a changed acceptance invalidates the review")
    ;; changed dependencies invalidate it.
    (ok (not (scope-review-reusable-p review :scope '(:node "acme/work/f1") :head "abc123"
                                      :acceptance '("criterion-1") :dependencies '("dep-2")))
        "changed dependencies invalidate the review")
    ;; an independent friend gate cannot be replaced by reuse.
    (let ((gate (make-scope-review :friend "stella" :scope '(:node "acme/work/f1")
                                   :head "abc123" :acceptance '("criterion-1")
                                   :dependencies '("dep-1") :independent-gate-p t
                                   :verdict :approved)))
      (ok (not (scope-review-reusable-p gate :scope '(:node "acme/work/f1") :head "abc123"
                                        :acceptance '("criterion-1") :dependencies '("dep-1")))
          "an independent friend gate cannot be replaced by reuse"))))

;;; ------------------------------------------------------------------
;;; review-cycles-stay-visible              SPEC-WORK.md:6368-6370
;;; ------------------------------------------------------------------
;;;
;;; "each required friend's exact-revision review and finding ids, the author's
;;; dispositions and the clearance recorded; valid evidence reused and only the
;;; affected delta reread; repeated review and repair cycles visible as work and
;;; as operational cost."

(deftest "review-cycles-stay-visible" "docs/SPEC-WORK.md:6368-6370"
    "expected=friend-revision-and-findings-recorded;dispositions-and-clearance;evidence-reused-delta-reread;cycles-counted-as-work-and-cost"
  (let* ((first (make-review-cycle :friend "stella" :revision "abc123"
                                   :finding-ids '("finding-1" "finding-2")
                                   :dispositions '("finding-1 fixed" "finding-2 accepted")
                                   :clearance nil :evidence-reused-p nil
                                   :reread-delta nil :cost 5))
         ;; the repair changed only part of the scope: prior evidence is reused
         ;; and only the affected delta is reread.
         (second (make-review-cycle :friend "stella" :revision "def456"
                                    :finding-ids '("finding-3")
                                    :dispositions '("finding-3 fixed")
                                    :clearance :cleared :evidence-reused-p t
                                    :reread-delta '("acme/work/f1/t1") :cost 3))
         (ledger (record-review-cycle (record-review-cycle (make-review-ledger) first)
                                      second)))
    ;; each required friend's exact revision, findings, dispositions and clearance.
    (ok (review-ledger-covers-friends-p ledger '("stella"))
        "every required friend's cycle is recorded")
    (check-equal "abc123" (review-cycle-revision first) "the exact revision is kept")
    (check-equal '("finding-1" "finding-2") (review-cycle-finding-ids first)
                 "the finding ids are kept")
    (check-equal '("finding-1 fixed" "finding-2 accepted")
                 (review-cycle-dispositions first) "the author's dispositions are kept")
    (check-equal :cleared (review-cycle-clearance second) "the clearance is recorded")
    ;; valid evidence reused and only the affected delta reread.
    (ok (review-cycle-evidence-reused-p second) "valid evidence is reused")
    (check-equal '("acme/work/f1/t1") (review-cycle-reread-delta second)
                 "only the affected delta is reread")
    ;; repeated review and repair cycles are visible as work and as cost.
    (check-equal 2 (review-ledger-cycle-count ledger)
                 "the repeated cycles are visible as work")
    (check-equal 8 (review-ledger-total-cost ledger)
                 "the repeated cycles are visible as operational cost")))
