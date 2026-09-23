;;;; replays-8648.lisp --- six acceptance replays named by docs/SPEC-WORK.md.
;;;;
;;;; Each deftest names the paragraph(s) it comes from and drives the pure
;;;; model the kernel exposes for it. The six:
;;;;
;;;;   regression-opens-repair-work                     :4943-4951,5782-5785
;;;;   reply-retired-only-under-verified-coverage       :6020-6024,6316-6325
;;;;   restore-is-isolated-and-dispatches-nothing       :6285-6290,5790-5793
;;;;   reuse-only-valid-review                          :4851
;;;;   review-cycles-stay-visible                       :6368-6370
;;;;   TestE11F04PartialChildNeverClosesParent          :4655,4312

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

;;; ------------------------------------------------------------------
;;; partial-child-never-closes-parent       SPEC-WORK.md:4655,4312
;;; ------------------------------------------------------------------
;;;
;;; E11-F04 (ROADMAP.md:1126). docs/SPEC-WORK.md:4655 -- "One child done and
;;; one refused, blocked or asleep leaves the parent open with
;;; `outstanding=<n>` and the mapped external issue open; the parent's
;;; outstanding count and its issue mapping survive the child's refusal
;;; unchanged." Duty 5 (docs/SPEC-WORK.md:4312): "partial child success never
;;; closes the parent or the mapped external issue."

(deftest "TestE11F04PartialChildNeverClosesParent" "docs/SPEC-WORK.md:4655"
    "expected=one-child-done-parent-stays-open;outstanding=1;mapped-issue-open;refusal-changes-neither-count-nor-mapping"
  ;; One parent work-set mapped to one external issue, with two required
  ;; children: one that will finish and one that will refuse.
  (let ((k (make-kernel
            :state (make-seed-state
                    '((:id "p"    :type :work-set :parent nil :state :unknown
                           :links ("acme/repo#42"))
                      (:id "p/t1" :type :task :parent "p" :state :doing)
                      (:id "p/t2" :type :task :parent "p" :state :doing))))))
    ;; before anything moves: two required members outstanding, and the one
    ;; mapped external issue open.
    (check-equal 2 (node-required-open (kernel-state k) "p")
                 "the parent opens with both required children outstanding")
    (check-equal 1 (open-issue-count k)
                 "the mapped external issue opens open")
    ;; the first child is done.
    (multiple-value-bind (okp line)
        (submit k (list :verb :state-to-done :node "p/t1" :by "stella"
                        :reason "shipped" :evidence '("ev-1") :request "e11f04-done-1"
                        :stamp "2026-09-20T12:00:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok okp "the first child settles: ~A" line))
    (check-equal :c (node-branch (kernel-state k) "p/t1")
                 "the done child is in C")
    ;; one child done beside one still outstanding (blocked or asleep reads the
    ;; same here: it simply has not settled) never closes the parent.
    (check-equal :o (node-branch (kernel-state k) "p")
                 "one child done beside one outstanding leaves the parent open")
    (check-equal 1 (node-required-open (kernel-state k) "p")
                 "the parent stays open with outstanding=1")
    (check-equal 1 (open-issue-count k)
                 "the mapped external issue is still open")
    ;; the second child refuses: a named terminal refusal, never silence and
    ;; never success.
    (multiple-value-bind (okp line)
        (submit k (list :verb :event-cancel :node "p/t2" :by "stella"
                        :reason "refused: over budget" :request "e11f04-refuse-1"
                        :stamp "2026-09-20T12:01:00Z" :clock :tool
                        :generation-owner "gen-1"))
      (ok okp "the second child's refusal is recorded: ~A" line))
    (check-equal :cancelled (node-state (kernel-state k) "p/t2")
                 "the refusal is recorded as a named disposition, never as success")
    ;; the parent is still open, and the outstanding count and the issue
    ;; mapping survive the child's refusal unchanged.
    (check-equal :o (node-branch (kernel-state k) "p")
                 "the refused child never closes the parent")
    (check-equal 1 (node-required-open (kernel-state k) "p")
                 "the outstanding count survives the child's refusal unchanged")
    (check-equal 1 (open-issue-count k)
                 "the mapped external issue survives the child's refusal open")
    (check-equal '("acme/repo#42") (node-links (kernel-state k) "p")
                 "the issue mapping itself survives the child's refusal unchanged")))


;;; ------------------------------------------------------------------
;;; TestE10F04OrchestrateProcessLevelRunsAgainst  E10-F04-01
;;; docs/SPEC-WORK.md:7223-7227
;;; ------------------------------------------------------------------
;;;
;;; E10-F04-01 (ROADMAP.md:1014). docs/SPEC-WORK.md:7223-7227 -- "process-level
;;; fault injection and restart-and-replay against temporary Git remotes and
;;; fake providers, two-process fencing and interrupted I/O included."
;;;
;;; The orchestrator lives here, beside its one test, not in src/. It creates
;;; a temporary bare Git remote on disk and two clones, one per owner, and
;;; drives real OS processes against it:
;;;
;;;   two-process fencing  two `git push --force-with-lease` processes race
;;;                        from the same observed tip; exactly one lands.
;;;   stale owner          the loser, still believing the old tip, pushes
;;;                        again and the remote's compare-and-swap refuses it.
;;;   handoff              the ownership claim names a successor, who takes
;;;                        the next generation at once and pushes on the tip
;;;                        it fetched.
;;;   crash / interrupted  a process is killed (SIGKILL) in the middle of the
;;;   I/O                  ref write, leaving a torn ref lock; the next push is
;;;                        refused and the tip is unchanged.
;;;   restart and replay   recovery clears the dead writer's lock and replays
;;;                        the push once; a second replay applies nothing, and
;;;                        a restarted kernel over the same journal answers the
;;;                        recorded receipt without applying the command twice.
;;;
;;; The fake provider answers the bounded question once. Every outcome is read
;;; back from the remote, the provider and the ownership claims.

(defun e10f04-run (dir program &rest args)
  "Run PROGRAM with ARGS in DIR as a child process; answer exit code and
trimmed standard output."
  (multiple-value-bind (out err code)
      (uiop:run-program (cons program args) :directory dir
                        :output :string :error-output :string
                        :ignore-error-status t)
    (declare (ignore err))
    (values code (string-trim '(#\Space #\Newline #\Return #\Tab) out))))

(defun e10f04-git (dir &rest args)
  (apply #'e10f04-run dir "git"
         "-c" "user.name=nova-work-test" "-c" "user.email=test@nova.invalid"
         "-c" "commit.gpgsign=false" "-c" "init.defaultBranch=work"
         args))

(defun e10f04-tip (remote)
  (nth-value 1 (e10f04-git remote "rev-parse" "refs/heads/work")))

(defun e10f04-commit (clone message)
  "Commit an empty change in CLONE on top of what it last fetched; answer the sha."
  (e10f04-git clone "commit" "-q" "--allow-empty" "-m" message)
  (nth-value 1 (e10f04-git clone "rev-parse" "HEAD")))

(defun e10f04-push-process (clone expected)
  "Launch (without waiting) a push from CLONE that lands only if the remote
tip is still EXPECTED: the compare-and-swap an owner uses."
  (uiop:launch-program
   (list "git" "push" "-q" (format nil "--force-with-lease=work:~A" expected)
         "origin" "HEAD:refs/heads/work")
   :directory clone :output nil :error-output nil))

(defun e10f04-push (clone expected)
  (zerop (uiop:wait-process (e10f04-push-process clone expected))))

(defun orchestrate-process-level-run
    (&key (t0 "2026-09-20T12:00:00Z") (t1 "2026-09-20T12:00:30Z"))
  "Orchestrate one process-level lifecycle against a temporary Git remote on
disk and real child processes. Answers a report plist of outcomes read back
from the remote, the fake provider and the ownership claims."
  (let* ((root (uiop:ensure-directory-pathname
                (merge-pathnames (format nil "nova-work-e10f04-~D-~D/"
                                         (get-universal-time) (random 1000000))
                                 (uiop:temporary-directory))))
         (remote (merge-pathnames "remote.git/" root))
         (seed-clone (merge-pathnames "seed/" root))
         (emma (merge-pathnames "emma/" root))
         (stella (merge-pathnames "stella/" root))
         (provider (make-fake-decider :answer :run))
         (seed '((:id "w" :type :work-set :parent nil :state :unknown)
                 (:id "w/t1" :type :task :parent "w" :state :todo))))
    (ensure-directories-exist remote)
    (unwind-protect
         (progn
           (e10f04-git remote "init" "-q" "--bare")
           (e10f04-git root "clone" "-q" (namestring remote) (namestring seed-clone))
           (e10f04-commit seed-clone "genesis")
           (e10f04-git seed-clone "push" "-q" "origin" "HEAD:refs/heads/work")
           (e10f04-git root "clone" "-q" "-b" "work" (namestring remote) (namestring emma))
           (e10f04-git root "clone" "-q" "-b" "work" (namestring remote) (namestring stella))
           (let ((genesis (e10f04-tip remote)))
             ;; 1. Vacant take: emma takes generation 1.
             (multiple-value-bind (vacant-action vacant-record)
                 (evaluate-ownership-claim nil "emma" :now t0 :token "tok-emma"
                                           :every "30s" :skew "5s" :my-bench "bench-emma")
               ;; 2. Two-process fencing: both owners observed GENESIS and race
               ;;    two push processes; the remote admits exactly one.
               (let* ((emma-race (e10f04-commit emma "emma gen-1 race"))
                      (stella-race (e10f04-commit stella "stella race"))
                      (pe (e10f04-push-process emma genesis))
                      (ps (e10f04-push-process stella genesis))
                      (emma-landed (zerop (uiop:wait-process pe)))
                      (stella-landed (zerop (uiop:wait-process ps)))
                      (race-tip (e10f04-tip remote))
                      (winner (if emma-landed emma stella))
                      (loser (if emma-landed stella emma)))
                 (declare (ignore winner))
                 ;; 3. Stale owner: the loser still believes GENESIS; its claim
                 ;;    against the live hold is fenced and its push refused.
                 (multiple-value-bind (stale-action)
                     (evaluate-ownership-claim
                      (make-ownership-record :owner "emma" :generation 1 :token "tok-emma"
                                             :stamp t0 :until t1 :bench "bench-emma")
                      "zoe" :now t0 :token "tok-zoe" :every "30s" :skew "5s"
                      :my-bench "bench-zoe")
                   (let* ((stale-pushed (e10f04-push loser genesis))
                          (tip-after-stale (e10f04-tip remote)))
                     ;; 4. Handoff: the record names stella successor; stella
                     ;;    takes gen 2 at once, fetches the tip and pushes on it.
                     (multiple-value-bind (handoff-action handoff-record)
                         (evaluate-ownership-claim
                          (make-ownership-record :owner "emma" :generation 1 :token "tok-emma"
                                                 :stamp t0 :until t1 :bench "bench-emma"
                                                 :successor "stella")
                          "stella" :now t1 :token "tok-stella" :every "30s" :skew "5s"
                          :my-bench "bench-stella")
                       (e10f04-git stella "fetch" "-q" "origin")
                       (e10f04-git stella "reset" "-q" "--hard" "origin/work")
                       (let* ((handoff-base (e10f04-tip remote))
                              (handoff-commit (e10f04-commit stella "stella gen-2 handoff"))
                              (handoff-pushed (e10f04-push stella handoff-base))
                              (handoff-tip (e10f04-tip remote))
                              ;; 5. Crash mid-I/O: a writer process takes the ref
                              ;;    lock, writes half a sha and is SIGKILLed.
                              (lock (merge-pathnames "refs/heads/work.lock" remote))
                              (crash-code
                                (e10f04-run remote "/bin/sh" "-c"
                                            (format nil "printf '~A' > refs/heads/work.lock; kill -KILL $$"
                                                    (subseq handoff-tip 0 20))))
                              (torn-lock (probe-file lock))
                              (crash-commit (e10f04-commit stella "stella gen-2 after crash"))
                              (pushed-during-crash (e10f04-push stella handoff-tip))
                              (tip-after-crash (e10f04-tip remote)))
                         ;; 6. Restart and replay: recovery clears the dead
                         ;;    writer's lock, the push replays once and lands,
                         ;;    a second replay applies nothing.
                         (delete-file lock)
                         (let* ((replay-pushed (e10f04-push stella handoff-tip))
                                (replay-tip (e10f04-tip remote))
                                (second-replay-exit (e10f04-push stella handoff-tip))
                                (second-replay-tip (e10f04-tip remote))
                                (commits (parse-integer
                                          (nth-value 1 (e10f04-git remote "rev-list" "--count"
                                                                   "refs/heads/work")))))
                           (declare (ignore second-replay-exit))
                           ;; 7. The fake provider answers the bounded question once.
                           (multiple-value-bind (answer)
                               (decide-consult provider '(:kind :choice :options (:run :stop)))
                             ;; 8. Kernel restart over the same journal answers
                             ;;    the recorded receipt and applies nothing twice.
                             (let* ((journal (make-ordering-journal))
                                    (live (make-kernel :state (make-seed-state seed)
                                                       :journal journal))
                                    (request (list :verb :state-to-doing :node "w/t1" :by "stella"
                                                   :reason "start" :request "restart-1"
                                                   :stamp t1 :clock :tool
                                                   :generation-owner "gen-2"))
                                    (okp (submit live request))
                                    (restarted (make-kernel :state (make-seed-state seed)
                                                            :journal journal))
                                    (retry (nth-value 3 (submit restarted request))))
                               (list :vacant-action vacant-action
                                     :vacant-generation (owner-generation vacant-record)
                                     :race-landed (count t (list emma-landed stella-landed))
                                     :race-tip-is-winner
                                     (string= race-tip (if emma-landed emma-race stella-race))
                                     :stale-owner-action stale-action
                                     :stale-pushed stale-pushed
                                     :tip-unchanged-by-stale (string= race-tip tip-after-stale)
                                     :handoff-action handoff-action
                                     :handoff-generation (owner-generation handoff-record)
                                     :handoff-pushed handoff-pushed
                                     :handoff-tip-is-commit (string= handoff-tip handoff-commit)
                                     :crash-killed (/= 0 crash-code)
                                     :torn-lock (and torn-lock t)
                                     :pushed-during-crash pushed-during-crash
                                     :tip-unchanged-by-crash (string= handoff-tip tip-after-crash)
                                     :replay-pushed replay-pushed
                                     :replay-tip-is-commit (string= replay-tip crash-commit)
                                     :second-replay-changed (not (string= replay-tip second-replay-tip))
                                     :remote-commits commits
                                     :provider-answer answer
                                     :provider-calls (fake-decider-calls provider)
                                     :restart-live-ok okp
                                     :restart-live-state (node-state (kernel-state live) "w/t1")
                                     :restart-replayed (getf retry :replayed)
                                     :restart-state (node-state (kernel-state restarted) "w/t1")))))))))))))
      (uiop:delete-directory-tree root :validate t :if-does-not-exist :ignore))))

(deftest "TestE10F04OrchestrateProcessLevelRunsAgainst"
    "E10-F04-01 (docs/SPEC-WORK.md:7223-7227)"
    "expected=vacant-take-gen1;two-process-race-lands-once;stale-owner-fenced;stale-push-refused;handoff-successor-takes-gen2;crash-mid-write-refuses;restart-replays-once;provider-consulted-once;kernel-restart-answers-once-only"
  (let ((r (orchestrate-process-level-run)))
    ;; ownership claims.
    (check-equal :take (getf r :vacant-action)
                 "a vacant claim is a take")
    (check-equal 1 (getf r :vacant-generation)
                 "the first owner takes generation 1")
    ;; two-process fencing against the temporary remote.
    (check-equal 1 (getf r :race-landed)
                 "exactly one of two racing push processes lands")
    (ok (getf r :race-tip-is-winner)
        "the remote tip is the winning process's commit")
    ;; stale owner.
    (check-equal :fenced (getf r :stale-owner-action)
                 "a stale owner contesting a live hold is fenced")
    (check-equal nil (getf r :stale-pushed)
                 "the stale owner's compare-and-swap push is refused")
    (ok (getf r :tip-unchanged-by-stale)
        "the remote tip is unchanged by the stale owner")
    ;; handoff.
    (check-equal :take (getf r :handoff-action)
                 "the successor's handoff claim is a take")
    (check-equal 2 (getf r :handoff-generation)
                 "the successor takes the next generation at once")
    (ok (getf r :handoff-pushed)
        "the successor's push on the fetched tip lands")
    (ok (getf r :handoff-tip-is-commit)
        "the remote tip is the successor's commit")
    ;; crash with interrupted I/O.
    (ok (getf r :crash-killed)
        "the writer process died by SIGKILL mid-write")
    (ok (getf r :torn-lock)
        "the killed writer left its torn ref lock behind")
    (check-equal nil (getf r :pushed-during-crash)
                 "a push against the torn ref lock is refused")
    (ok (getf r :tip-unchanged-by-crash)
        "the torn write never becomes the remote tip")
    ;; restart and replay.
    (ok (getf r :replay-pushed)
        "after recovery the replayed push lands")
    (ok (getf r :replay-tip-is-commit)
        "the remote tip is the replayed commit")
    (check-equal nil (getf r :second-replay-changed)
                 "a second replay applies nothing")
    (check-equal 4 (getf r :remote-commits)
                 "the remote holds genesis, the race winner, the handoff and the replay, nothing more")
    ;; the fake provider participates exactly once.
    (check-equal :run (getf r :provider-answer)
                 "the fake provider answers the bounded question with :run")
    (check-equal 1 (getf r :provider-calls)
                 "the fake provider is consulted exactly once")
    ;; kernel restart over the same journal.
    (ok (getf r :restart-live-ok)
        "the live kernel accepts the command")
    (check-equal :doing (getf r :restart-live-state)
                 "the live kernel applies the accepted command")
    (ok (getf r :restart-replayed)
        "the restarted kernel answers the recorded receipt")
    (check-equal :todo (getf r :restart-state)
                 "the restarted kernel does not apply the command a second time")))
