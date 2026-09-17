;;;; slice-05-durable-journal.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:1986-1997,5093"
    "expected=landed-sha-released-dash-while-release-open"
  (let* ((finding (make-finding "acme/sec/alex-2" :branch :c :disposition :done
                                :evidence (list (list :criterion :version :against "deadbeef")
                                                (list :criterion :merged :against "4b81c0d5"))))
         (open-release (list (make-release-task "acme/release/v0.4.3"
                                                :version "v0.4.3"
                                                :deps '("acme/sec/alex-2")
                                                :branch :o))))
    (let ((row (disposition-row finding open-release)))
      (check-equal :c (getf row :branch) "the fix is in C")
      (check-equal :done (getf row :disposition) "disposition=done")
      (check-equal "4b81c0d5" (getf row :landed) "landed is the :merged :against sha")
      (check-equal "-" (getf row :released) "released=- while the release task is open"))
    (let* ((settled (list (make-release-task "acme/release/v0.4.3"
                                             :version "v0.4.3"
                                             :deps '("acme/sec/alex-2")
                                             :branch :c
                                             :settle-stamp "2026-09-13T00:00:00Z")))
           (row (disposition-row finding settled)))
      (check-equal "v0.4.3" (getf row :released) "released=<version> once the task settles"))
    (let* ((settled (list (make-release-task "acme/release/v0.4.3"
                                             :version "v0.4.3" :deps '("acme/sec/alex-2")
                                             :branch :c :settle-stamp "2026-09-13T00:00:00Z")
                          (make-release-task "acme/release/v0.4.4"
                                             :version "v0.4.4" :deps '("acme/sec/alex-2")
                                             :branch :c :settle-stamp "2026-09-14T00:00:00Z")))
           (row (disposition-row finding settled)))
      (check-equal "v0.4.3" (getf row :released) "the release that first carried the fix"))
    (let ((row (disposition-row (make-finding "acme/sec/alex-9" :branch :o :disposition :pending
                                              :evidence '())
                                open-release)))
      (check-equal "-" (getf row :landed) "no merged evidence reads landed=-")
      (check-equal "-" (getf row :released) "no settled release reads released=-"))))

(deftest "ready-names-the-blocker-and-the-resolver" "docs/SPEC-WORK.md:2019"
    "expected=every-blocked-row-names-reason-and-resolver"
  (let* ((items (list (make-ready-item "acme/f/t1" :branch :o :deps '("acme/f/t2"))
                      (make-ready-item "acme/f/t2" :branch :o :holder "freddy")
                      (make-ready-item "acme/f/t3" :branch :o :deps '("acme/f/t-done"))
                      (make-ready-item "acme/f/t-done" :branch :c :state :done
                                       :responsible "emma")
                      (make-ready-item "acme/f/t4" :branch :o :responsible "rowan")))
         (rows (ready-rows items))
         (by-id (lambda (id) (find id rows :key #'ready-row-id :test #'equal))))
    (let ((r (funcall by-id "acme/f/t1")))
      (ok r "t1 has a row")
      (ok (not (ready-row-ready r)) "t1 is not ready")
      (check-equal "blocked by acme/f/t2" (ready-row-reason r) "reason names the blocker")
      (check-equal "freddy" (ready-row-resolver r) "resolver is the blocker's holder"))
    (let ((r (funcall by-id "acme/f/t2")))
      (check-equal t (ready-row-ready r) "a held but unblocked item is ready")
      (check-equal "freddy" (ready-row-resolver r) "its own resolver is its holder"))
    (let ((r (funcall by-id "acme/f/t3")))
      (check-equal t (ready-row-ready r) "a settled dependency does not block")
      (check-equal "-" (ready-row-reason r) "a ready row prints reason=-"))
    (let ((r (funcall by-id "acme/f/t4")))
      (check-equal t (ready-row-ready r) "an unheld item is ready")
      (check-equal "rowan" (ready-row-resolver r) "its resolver is its responsible"))
    (check-equal 4 (length rows) "one row per O item; the C item is not listed")))

;; history-grows-startup-does-not is implemented in ../acceptance.lisp (card 8601).

(deftest "remove-settles-only-open-items" "docs/SPEC-WORK.md:2305,5075"
    "expected=closed-leaf-untouched-already-closed-named"
  (let* ((seed '((:id "rw"      :type :work-set :state :unknown)
                 (:id "rw/f"    :type :feature  :parent "rw" :state :doing)
                 (:id "rw/f/t1" :type :task     :parent "rw/f" :state :doing)
                 (:id "rw/f/t2" :type :task     :parent "rw/f" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "rw/f/t1" :request "rw-close-1")) "t1 closes")
    (check-equal 1 (state-closed-count (kernel-state k)) "|C| after the close")
    (let* ((t1-stamp (getf (find "rw/f/t1" (state-closed-rows (kernel-state k))
                                :key (lambda (r) (getf r :node)) :test #'equal)
                           :stamp)))
      (multiple-value-bind (okp line code) (node-remove k "rw/f" :request "rw-remove-1")
        (ok okp line)
        (check-equal 0 code "remove exits 0")
        (check-equal :c (node-branch (kernel-state k) "rw/f") "the feature settles into C")
        (check-equal :c (node-branch (kernel-state k) "rw/f/t2") "the open leaf settles")
        (check-equal :c (node-branch (kernel-state k) "rw/f/t1") "the closed leaf stays in C")
        (check-equal :done (node-state (kernel-state k) "rw/f/t1") "the done leaf stays done")
        (check-equal :removed (getf (find "rw/f/t2" (state-closed-rows (kernel-state k))
                                         :key (lambda (r) (getf r :node)) :test #'equal)
                                    :disposition)
                     "the open leaf's settle carries disposition=removed")
        (check-equal 3 (state-closed-count (kernel-state k)) "|C| moves by two, not three")
        (check-equal 1 (state-open-count (kernel-state k)) "only the work set stays open")
        (check-equal 4 (+ (state-open-count (kernel-state k))
                          (state-closed-count (kernel-state k)))
                     "no rule 18 finding: the branches still partition the set")
        (let ((t1-after (find "rw/f/t1" (state-closed-rows (kernel-state k))
                              :key (lambda (r) (getf r :node)) :test #'equal))
              (f-row (find "rw/f" (state-closed-rows (kernel-state k))
                           :key (lambda (r) (getf r :node)) :test #'equal)))
          (check-equal :done (getf t1-after :disposition) "t1's disposition is untouched")
          (check-equal t1-stamp (getf t1-after :stamp) "t1's settle stamp is untouched")
          (check-equal :removed (getf f-row :disposition) "the feature reads disposition=removed")
          (check-equal '("rw/f/t1") (getf f-row :already-closed)
                       "the feature's settle names the already-closed leaf"))
        ;; A fresh-id repeat while still closed is the no-effect case.
        (multiple-value-bind (okp2 line2 code2) (node-remove k "rw/f" :request "rw-remove-2")
          (ok okp2 line2)
          (check-equal 0 code2 "repeat exits 0")
          (ok (search "already-closed" line2) "the repeat prints NODE NOTE already-closed")
          (check-equal 3 (state-closed-count (kernel-state k))
                       "the repeat writes no second settle")
          (check-equal 1 (state-open-count (kernel-state k)) "the repeat moves no counter"))))))

(deftest "applicable-cap-never-hides-a-deny" "docs/SPEC-WORK.md:5498-5502"
    "expected=constraint-row-before-cut-rows;notes-more;fail-no-eligible-no-row"
  ;; N active notes, N > --max, the only :deny in the note that sorts last. The
  ;; cap orders the constraint-bearing note ahead of the narrative rows, so
  ;; --max 1 still prints the deny row and sets NOTES MORE. With the notes index
  ;; unloadable the read prints FAIL and neither an eligible verdict nor a row.
  (let* ((n1 (make-note :scope '(:node) :author "glenn" :date "2026-01-01T00:00Z"
                        :source "meeting" :kind :observation :text "weigh price"))
         (n2 (make-note :scope '(:node) :author "glenn" :date "2026-01-02T00:00Z"
                        :source "meeting" :kind :observation :text "weigh rework"))
         (deny (make-note :scope '(:node) :author "glenn" :date "2026-01-03T00:00Z"
                          :source "instruction" :kind :instruction
                          :text "no coding on Astra"
                          :constraint '(:constraint
                                        (:deny (:model "astra"))
                                        (:prefer ())
                                        (:reason "coordinator instruction"))))
         (a (applicable "stella" :coding '("coordinator/astra")
                        (list n1 n2 deny)
                        :source :live :models '("astra") :max 1)))
    (check-equal 1 (length (applicable-answer-shown a))
                 "the cap shows one row")
    (ok (applicable-answer-more a) "--max cuts rows, so NOTES MORE is set")
    (ok (member deny (applicable-answer-shown a) :test #'equal)
        "the constraint row is shown before any cut row: the deny is never hidden")
    (let ((v (cdr (assoc "coordinator/astra" (applicable-answer-verdicts a)
                         :test #'equal))))
      (ok (verdict-excluded-p v) "the capped read still excludes the denied route")
      (ok (member (getf deny :id) (excluded-note-ids v) :test #'equal)
          "the excluded verdict names the note id that --max tried to cut")))
  ;; the notes index unloadable: FAIL, no eligible verdict and no row at all.
  (let ((a (applicable "stella" :coding '("coordinator/astra") '()
                       :source :live :notes-index nil :models '("astra") :max 1)))
    (ok (applicable-answer-fail a) "an unloadable notes index prints FAIL")
    (check-equal '() (applicable-answer-shown a) "a failed read prints no row")
    (check-equal '() (applicable-answer-verdicts a) "a failed read prints no verdict")))
;;; Slice-1 boundary replays promised by docs/SPEC-WORK.md lines
;;; 2400-3600 (part 1 of 4). Every name is dated to the acceptance
;;; table's own paragraph (the `docs/SPEC-WORK.md:` reference below) and
;;; to the prose line where it is first named. Each is now a real deftest,
;;; in this file or in the slice file named beside it.
;;; ------------------------------------------------------------------

;;; endpoint-is-local-and-private  SPEC-WORK.md prose :2646-2653 / table :5727
(deftest "endpoint-is-local-and-private" "docs/SPEC-WORK.md:5727"
    "session-dir=0700;socket=0600;wider-mode-refused;no-network-bind"
  ;; the session's directory created 0700 and its socket 0600, both owned
  ;; by the running account; a pre-existing directory or socket with wider
  ;; modes refused rather than reused; no listener on any network address.
  (let* ((tmp (namestring (uiop:temporary-directory)))
         (base (if (< (length tmp) 80)
                   (concatenate 'string tmp (format nil "nw-~D" (random 1000000)))
                   (format nil "nw-~D" (random 1000000))))
         (dir (concatenate 'string base "/s"))
         (sock (concatenate 'string dir "/w")))
    (unwind-protect
         (progn
           (unless (probe-file base) (sb-posix:mkdir base #o700))
           (let ((ep (make-session-endpoint dir sock)))
             (check-equal #o700 (session-endpoint-directory-mode ep)
                          "the session directory is 0700")
             (check-equal #o600 (session-endpoint-socket-mode ep) "the socket is 0600")
             (check-equal (current-account-uid) (session-endpoint-owner ep)
                          "both are owned by the running account")
             (check-equal (local-socket-family) (session-endpoint-socket-family ep)
                          "the socket is a local (AF_UNIX) socket")
             (ok (not (endpoint-network-listener-p ep)) "no network listener"))
           ;; A pre-existing directory with wider modes refuses rather than reuses.
           (let ((wide (concatenate 'string base "/w")))
             (sb-posix:mkdir wide #o755)
             (handler-case
                 (progn (make-session-endpoint wide (concatenate 'string wide "/w"))
                        (ok nil "a 0755 directory must be refused"))
               (nova-work-error (c) (declare (ignore c))
                 (ok t "a pre-existing 0755 directory is refused"))))
           ;; A pre-existing socket path with wider modes refuses the same way.
           (let* ((d2 (concatenate 'string base "/d"))
                  (s2 (concatenate 'string d2 "/w")))
             (sb-posix:mkdir d2 #o700)
             (with-open-file (f s2 :direction :output :if-exists :supersede)
               (declare (ignore f)))
             (sb-posix:chmod s2 #o644)
             (handler-case
                 (progn (make-session-endpoint d2 s2)
                        (ok nil "a 0644 socket must be refused"))
               (nova-work-error (c) (declare (ignore c))
                 (ok t "a pre-existing 0644 socket is refused")))))
      (ignore-errors (sb-posix:unlink sock))
      (ignore-errors (sb-posix:rmdir dir))
      (ignore-errors (sb-posix:rmdir (concatenate 'string base "/w")))
      (ignore-errors (sb-posix:unlink (concatenate 'string base "/d/w")))
      (ignore-errors (sb-posix:rmdir (concatenate 'string base "/d")))
      (ignore-errors (sb-posix:rmdir base)))))

;;; wire-integers-are-strings, protocol-version-negotiated-or-refused,
;;; pipeline-replies-are-correlated and disconnect-is-not-a-rollback are the
;;; executable replays in ../acceptance.lisp (nova-tools #362) over the wire
;;; codec and protocol session in src/replays-wire-and-operations.lisp. The
;;; NEEDS-KERNEL stubs are gone with them.

;;; no-effect-mutation-is-journaled  SPEC-WORK.md prose :2797 / table :5176
;;; The real replay now lives in tests/acceptance.lisp over the node-edit verbs
;;; of src/node-verbs.lisp (nova-tools #362).

;;; operation-survives-the-client  SPEC-WORK.md prose :2580 / table :5183
;;; The real replay lives in tests/acceptance.lisp:749 (nova-tools #362):
;;; a long import returns an operation id at once and durably, the client
;;; exits while the work runs, `operation wait` times out leaving it running,
;;; and the completed result is retrievable by id.

;;; status-answers-while-io-runs  SPEC-WORK.md prose :2581 / table :5186
;;; The real replay lives in tests/acceptance/slice-08-replays-late.lisp:147
;;; (nova-tools #362): status and cancel answer within bound while a capture,
;;; export and clip are in flight, queues/staged/retained stay bounded, and a
;;; restart reconciles the pending operation ids.

;;; cancel-is-a-request-not-an-erasure  SPEC-WORK.md prose :5789-5791 / table :5189
(deftest "cancel-is-a-request-not-an-erasure" "docs/SPEC-WORK.md:5789-5791"
    "cancel-ack-own-disposition;accepted-mutation-not-erased;uncertain-external-reported-uncertain"
  ;; A cancellation is a request with its own acknowledgement and its own final
  ;; disposition: it erases no accepted mutation, cancels once on a replay, and
  ;; an uncertain external effect is reported uncertain rather than cancelled.
  (let* ((events '((:id "ev-accepted" :kind :state-to-done :request "req-accepted")))
         (session (make-work-session
                   :events events :receipts '(:receipt-1)
                   :operations (list (make-operation :id "op-cap" :op :capture
                                                      :request "req-cap" :state :running))))
         (before-events (work-session-events session)))
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-cap" :request "req-cancel-1")
      (check-equal :cancelled (getf disposition :state)
                   "the cancel ack carries its own final disposition")
      (check-equal "req-cancel-1" (getf disposition :request)
                   "the ack echoes the cancel's own request id")
      (check-equal before-events (work-session-events after)
                   "the accepted mutation is not erased")
      ;; a cancel replayed twice cancels once.
      (multiple-value-bind (again disposition-2)
          (operation-cancel after "op-cap" :request "req-cancel-1")
        (check-equal :cancelled (getf disposition-2 :state) "the replay answers cancelled")
        (check-equal t (getf disposition-2 :replayed) "the replay applies nothing")
        (check-equal before-events (work-session-events again)
                     "the replay erases nothing")))
    ;; an uncertain external effect is reported uncertain, not cancelled.
    (let ((uncertain (make-work-session
                      :events events
                      :operations (list (make-operation :id "op-pay" :op :pay
                                                         :request "req-pay" :state :running)))))
      (multiple-value-bind (after disposition)
          (operation-cancel uncertain "op-pay" :request "req-cancel-2"
                            :external-effect :uncertain)
        (declare (ignore after))
        (check-equal :uncertain (getf disposition :state)
                     "an uncertain external effect reads uncertain, never cancelled")))))
;;; cancel-is-a-request-not-an-erasure  SPEC-WORK.md prose :2580 / table :5189
;;; The real replay lives in tests/acceptance/slice-08-replays-late.lisp:298
;;; (nova-tools #362): a cancel is acknowledged with its own final disposition,
;;; erases no accepted mutation, and reports an uncertain external effect as
;;; uncertain rather than cancelled.

;;; `undo-appends-and-preserves`, `redo-refuses-a-stale-plan`,
;;; `undo-refuses-an-external-effect` and `undo-names-its-reversible-set` are
;;; real replays now (nova-tools #362): the first two over
;;; src/replays-operations-undo.lisp in slice-08 and slice-07, the last two over
;;; src/edit-undo.lisp in tests/acceptance.lisp. The parked stubs are gone.

;;; clip-is-one-long-operation  SPEC-WORK.md prose :2568 / table :5327
;;; The real replay lives in tests/acceptance/slice-08-replays-late.lisp:335
;;; (nova-tools #362): clip prints OPERATION OK id= op=clip and exits, wait
;;; prints CLIP OK, a raced transport prints CLIP RACED, and session stop waits
;;; on its own clip within --git-timeout.

;;; repo-only-at-the-root  SPEC-WORK.md prose :2846 / table :5341
;;; The real replay now lives in tests/acceptance.lisp over the node verbs of
;;; src/node-verbs.lisp (nova-tools #362).

;;; metadata-patches-preserve-intent  SPEC-WORK.md prose :2844 / table :5347
;;; The real replay now lives in tests/acceptance.lisp over the node-edit verbs
;;; of src/node-verbs.lisp (nova-tools #362).

;;; edit-is-atomic-and-replayable  SPEC-WORK.md prose :2845 / table :5351
;;; The real replay now lives in tests/acceptance.lisp over the node-edit verbs
;;; of src/node-verbs.lisp (nova-tools #362).

;;; edit-undo-preserves-later-work  SPEC-WORK.md prose :2845 / table :5355
;;; The real replay now lives in tests/acceptance.lisp over the node-edit and
;;; node-undo verbs of src/node-verbs.lisp (nova-tools #362).
;;; `edit-undo-preserves-later-work` is a real replay now, over
;;; src/edit-undo.lisp in tests/acceptance.lisp (nova-tools #362).

;;; edit-never-fetches-a-link  SPEC-WORK.md prose :2845 / table :5357
;;; The real replay now lives in tests/acceptance.lisp over the node-edit link
;;; validation and renderer of src/node-verbs.lisp (nova-tools #362).

;;; The node-move replays (move-keeps-every-count, move-keeps-the-lease,
;;; move-refuses-by-name, move-same-parent-is-a-receipt and
;;; move-undo-refuses-a-reorder) are real deftests in the move section at the
;;; end of this file; move-keeps-every-count also stands in tests/acceptance.lisp.
;;; Replays promised by docs/SPEC-WORK.md:2400-3600. The verb and kernel
;;; machinery each names (move, roadmap/axis, render, priority, state-export)
;;; now exists, so each is a real deftest and none is parked.
;;; ------------------------------------------------------------------

;;; The roadmap replays promised by SPEC-WORK.md prose :2891 / :2948 / :2949 —
;;; move-updates-every-roadmap-scope, axisless-history, matrix-retirement,
;;; configure-no-effect-and-undo-conflict and completed-view-mutation — are
;;; real deftests in tests/acceptance/slice-09-replays-roadmap.lisp over
;;; src/roadmap.lisp (nova-tools #362).
;;; The `move` replays — move-refuses-by-name, move-keeps-the-lease and
;;; move-undo-refuses-a-reorder — are real deftests in the node-move section
;;; at the end of this file (nova-tools #362).

;;; The render, priority and rank replays now run as real deftests:
;;; render-refuses-a-target-outside-its-roots and render-artifact-is-bounded in
;;; tests/acceptance/slice-07-replays-mid.lisp; chat-and-file-render-are-byte-identical
;;; in tests/acceptance/slice-09-replays-roadmap.lisp; priority-orders-only-the-eligible,
;;; priority-inherits-and-clears, priority-undo-is-history-not-value, rank-2-precedes-10
;;; and priority-grants-nothing in tests/acceptance/slice-06-replays-early.lisp
;;; (nova-tools #362). No parked stub remains for this group.

;;; The state-export/load replays of SPEC-WORK.md:3197-3225 now run against
;;; src/state-export.lisp: state-export-describes-exactly-r and
;;; state-export-is-one-long-operation live in slice-08-replays-late.lisp, and
;;; state-export-pin-survives-clip, state-export-disconnect-and-cancel,
;;; state-export-refuses-a-gap, state-load-is-isolated and fenced-export-can-finish
;;; live in slice-09-state-export-replays.lisp. No NEEDS-KERNEL stub remains.
;;; ------------------------------------------------------------------
;;; 50-71. replays promised by docs/SPEC-WORK.md:2400-3600 (part 3 of 4)
;;;
;;; Every one of these names a line of SPEC-WORK.md and is a real deftest:
;;; CONFIG and ACTIVE roles, the fleet, dispatch/ack/offers, model attribution,
;;; silence pinging and the bounded config exchange now all ship in this system.
;;; ------------------------------------------------------------------

;;; 50. state-load-is-isolated   docs/SPEC-WORK.md:5445
;;; ------------------------------------------------------------------
;;; Moved to slice-09-state-export-replays.lisp, which owns the five
;;; state-export/load replays of SPEC-WORK.md:5879-5905.

;;; 51. full-round-trip   docs/SPEC-WORK.md:5587
;;; ------------------------------------------------------------------

(deftest "full-round-trip" "docs/SPEC-WORK.md:5587"
    "expected=re-export=equal;ids-links-history-equal"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1" :evidence '("ev-1"))) "close refused")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2" :evidence '("ev-2")))
        "second close refused")
    ;; Export a captured revision, load it into a fresh isolated engine, export
    ;; again, and compare every semantic field the slice keeps: stable ids,
    ;; links, O and C history, closed rows and the root digest.
    (let* ((source (kernel-state k))
           (exported (state-canonical-form source))
           (rebuilt (reconstruct-state (canonical-string exported))))
      (check-equal exported (state-canonical-form rebuilt)
                   "the re-export compares different in some field")
      (check-string= (root-digest source) (root-digest rebuilt) "the re-export root digest")
      (check-equal (state-history source) (state-history rebuilt) "the re-export history")
      (check-equal (state-closed-rows source) (state-closed-rows rebuilt)
                   "the re-export closed rows"))))

;;; 52. old-history   docs/SPEC-WORK.md:5589
;;; ------------------------------------------------------------------

(deftest "old-history" "docs/SPEC-WORK.md:5589"
    "expected=full-history-in-export;oldest-record-included"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1" :evidence '("ev-1"))) "close refused")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (ok (submit k (close-request :request "req-3" :node "acme/work/f1/t2" :evidence '("ev-2")))
        "second close refused")
    (let* ((source (kernel-state k))
           (history (state-history source))
           (exported-history (getf (state-canonical-form source) :history)))
      ;; An export includes the whole archive: the oldest record is present, not
      ;; pruned to a resident window, and the canonical bytes carry it whole.
      (check-equal 3 (length history) "a record was dropped from the history")
      (check-equal "req-1" (getf (first history) :request)
                   "the oldest record is not the first transition")
      (check-equal history exported-history "the export omitted the old history"))))

;;; 53. fenced-export-can-finish   docs/SPEC-WORK.md:5451
;;;
;;; Moved to slice-09-state-export-replays.lisp, which owns the fenced session
;;; replay (an export started; its status and terminal line read by id; an
;;; unfinished one cancelled; an unknown/non-export id and every canonical
;;; write refused `fenced`) of SPEC-WORK.md:5902.

;;; 54. roles-are-configured-not-inferred   docs/SPEC-WORK.md:3172
;;;
;; Now asserted by tests/acceptance/slice-07-replays-mid.lisp.
;;   A role is read from CONFIG and never from the underlying model; agreed
;;   limits are never raised silently; essential-security-only and reserved-plan
;;   roles and agreed participation are each expressible.

;;; 55. reserved-role-is-not-spent-on-routine-work   docs/SPEC-WORK.md:3173
;;;
;; Now asserted by tests/acceptance/slice-07-replays-mid.lisp.
;;   A role reserved for essential security work on a paid plan is never spent
;;   by routine work; a model capability never cancels an agreed limit.

;;; 56. no-friend-name-in-the-tool   docs/SPEC-WORK.md:5209
;;;
;; Now asserted by tests/acceptance/slice-05-durable-journal.lisp.
;;   Shipped binary/defaults/fixtures carrying no friend, bench,
;;   repository or house name. Every identity arrives as configuration.
;;   (Not a test of this document, which cites friends by name for provenance.)

;;; 58. dispatch-ack-and-ownership-are-three (SPEC-WORK.md:5219) now runs as
;;; the real deftest below, over the four-fact dispatch records.
;;; 58. dispatch-ack-and-ownership-are-three   docs/SPEC-WORK.md:5219

;;; 59. requested-model-is-not-observed-model   docs/SPEC-WORK.md:5222
;;;
;; Now asserted by tests/acceptance/slice-07-replays-mid.lisp.
;;   Unknown stays unknown; a friend's usual model never stands as proof of the
;;   executor of a delegated task.

;;; 61. silence-is-a-ping-not-a-verdict   docs/SPEC-WORK.md:5225
;;;
;; Now asserted by tests/acceptance/slice-08-replays-late.lisp.
;;   A configured threshold triggers one bounded ping; a configured answer
;;   window marks capacity unavailable with reason `unconfirmed`, never sleep
;;   nor exhausted credit.

;;; 63. return-reconciles-before-dispatch (SPEC-WORK.md:5226) now runs as the
;;; real deftest in slice-07-replays-mid.lisp.
;;; 63. return-reconciles-before-dispatch   docs/SPEC-WORK.md:5226

;;; 64. unchanged-config-is-one-bounded-answer   docs/SPEC-WORK.md:5284
;;;
;; Now asserted by tests/acceptance/slice-08-replays-late.lisp.
;;   A request naming a friend and its last-known config hash/revision answers
;;   UNCHANGED with that identity in one bounded reply, no roster/prose repeated.

;;; 67-71. The five fleet replays (fleet-is-static-config,
;;; no-machine-name-in-the-tool, one-profile-one-unit, no-credential-in-a-member,
;;; unknown-owner-is-refused) now run against src/fleet.lisp and live in
;;; slice-10-fleet.lisp beside their fixtures (card #8628).
;;; Assignment & execution control replays (SPEC-WORK.md:2400-3600).
;;;
;;; The slice-1 kernel implements only the three C/O transition verbs
;;; (state-to-done, state-to-doing, event-reopen). Every replay named
;;; below promises a verb or index that does not exist in this slice —
;;; offer, acknowledge, decline, execution pause/stop/resume/correct/
;;; reconcile, and the fleet query — so each is kept in the file's
;;; deftest shape, marked, and counted as needs-kernel rather than run.
;;; ------------------------------------------------------------------

;; four-facts-four-verbs (SPEC-WORK.md:3398) now runs as the real deftest in
;; tests/acceptance/slice-09-fleet-assignment.lisp.

;; a-receipt-needs-a-verifier and staged-admission-refuses now run as the real
;; deftests in tests/acceptance/slice-09-fleet-assignment.lisp (nova-tools #362).

;; offer-writes-intent-and-a-reservation (SPEC-WORK.md:5229) now runs as the
;; real deftest in tests/acceptance.lisp.

;; no-shadow-lease-across-holders (SPEC-WORK.md:5238) now runs as the real
;; deftest in tests/acceptance.lisp.

;; accepted-creates-one-lease-or-binds (SPEC-WORK.md:5238) now runs as the
;; real deftest in tests/acceptance.lisp.

;; until-is-overdue-not-released (SPEC-WORK.md:5243) now runs as the real
;; deftest in tests/acceptance.lisp.

;; late-and-duplicate-receipts-are-retained now lives in tests/acceptance.lisp
;; over the leasebook of src/assignment.lisp (nova-tools #362).

;; stop-is-a-hold-not-a-cancel (SPEC-WORK.md:3944) now runs as the real deftest
;; in slice-09-replays-holds.lisp, with the hold and dispatch gate it needed.

;;; one-stop-note-cannot-cancel-two-attempts  SPEC-WORK.md prose :3940-3942
(deftest "one-stop-note-cannot-cancel-two-attempts" "docs/SPEC-WORK.md:3940-3942"
    "expected=one-stop-note-cannot-cancel-node-with-another-live-attempt"
  ;; The :cancel's evidence must cover every attempt live at the request, so a
  ;; stop note covering one of two live attempts is refused; the same note with
  ;; both covered is admitted and the task is cancelled only then.
  (let ((k (fresh)))
    (record-attempt k "acme/work/f1/t1" "att-1")
    (record-attempt k "acme/work/f1/t1" "att-2")
    (request-cancel k "acme/work/f1/t1" :request "cancel-req-1")
    (multiple-value-bind (ok line code)
        (cancel-confirm k "acme/work/f1/t1" :evidence '("att-1"))
      (ok (not ok) "a one-attempt note cancelled a two-attempt node")
      (check-equal 1 code "refusal exit code")
      (ok (search "cannot cancel" line) "refusal does not name the rule: ~A" line))
    (multiple-value-bind (ok line code)
        (cancel-confirm k "acme/work/f1/t1" :evidence '("att-1" "att-2"))
      (ok ok "two covered attempts still refused: ~A" line)
      (check-equal 0 code "admission exit code"))
    (check-equal t (cancel-requested-p k "acme/work/f1/t1")
                 "the request edge stands after the confirmation")
    (check-string= "stop=requested" (goal-stop k "acme/work/f1/t1")
                   "goal show reads the state edge, not a hold")))

;; hold-survives-a-crash (SPEC-WORK.md:3962) now runs as the real deftest in
;; slice-09-replays-holds.lisp.

;; capture-survives-clip now runs against src/state-export.lisp in
;; slice-09-replays-holds.lisp (a clip publishing while a capture is live
;; carries the pin forward; the capture never rebuilds from a newer scope).

;; no-dispatch-slips-past-a-hold (SPEC-WORK.md:3979) now runs as the real
;; deftest in slice-09-replays-holds.lisp, with the dispatch barrier it named.

;; held-acceptance-converts-nothing (SPEC-WORK.md:3980) now runs as the real
;; deftest in tests/replays-8640.lisp.

;; reconcile-preserves-contradiction (SPEC-WORK.md:4009) now runs as the real
;; deftest in tests/replays-8640.lisp.

;; resume-is-two-actions (SPEC-WORK.md:4019) now runs as the real deftest in
;; tests/replays-8640.lisp.

;; correct-is-a-linked-segment (SPEC-WORK.md:4037) now runs as the real deftest
;; in tests/replays-8640.lisp.

;; bare-correct-refuses-under-execution (SPEC-WORK.md:4037) now runs as the
;; real deftest in tests/replays-8640.lisp.
;;; lines 3600-end part 1 of 8 (rowan/replays-278)
;;; ------------------------------------------------------------------


;; a-broken-assertion-must-fail (SPEC-WORK.md:6371) now runs as the real deftest
;; in tests/replays-8641.lisp.

;; Now asserted by tests/acceptance/slice-07-replays-mid.lisp.
;; a-root-id-grants-nothing (SPEC-WORK.md:5402) --- a stored permitted root with no
;; --render-root mapping refusing file mode while --chat renders; escaping/symlink/target-identity
;; refusals; the cooperative lock and external-editor limit retained.

;; a-savepoint-is-not-a-shared-backup moved to tests/replays-8641.lisp when its
;; kernel landed (card 8641): restore takes no ownership, reanimates no
;; assignment, replays no message; savepoint age, local and shared revisions,
;; unshared work and failed backups readable; a savepoint never printed where a
;; checkpoint asked. The parked stub is gone with it.

;; a-stop-reaches-distributed-work (SPEC-WORK.md:6374) now runs as the real
;; deftest in tests/replays-8641.lisp, with the control handles it named.

;; accepted-creates-one-lease-or-binds (SPEC-WORK.md:5238) now runs as the
;; real deftest in tests/acceptance.lisp.

;; add-field-order-is-complete (SPEC-WORK.md:5337) --- the real replay now lives
;; in tests/replays-8641.lisp over src/replays-8641.lisp and src/node-verbs.lisp
;; (nova-tools #362).

;; as-of-reconstructs-settle-revive-settle now lives in
;; tests/acceptance/slice-04-doing-and-journal.lisp over the settle/revive
;; chains of src/kernel.lisp (nova-tools #362).

;; as-of-refuses-unavailable-partition now lives in
;; tests/acceptance/slice-09-replays-8603.lisp over the as-of ask of
;; src/closed-history.lisp (nova-tools #362).

;;; async-operations: the real replay lives in tests/replays-8642.lisp:117
;;; (nova-tools #362) --- launch/cancel dispositions with no double launch and no
;;; false cancellation success.

;; axisless-history now lives in tests/acceptance/slice-09-replays-roadmap.lisp
;; (nova-tools #362).

;; bare-correct-refuses-under-execution (SPEC-WORK.md:5272) now runs as the
;; real deftest in tests/replays-8640.lisp.

;;; batch-with-bounds-and-urgency: the real replay lives in
;;; tests/replays-8642.lisp:60 (nova-tools #362) --- byte/record bounds,
;;; unchanged batches causing no call, unreferenced padding refused, urgent
;;; corrections bypassing delay, dependencies and retry identities surviving.

;;; batches-and-pipelines: the real replay lives in tests/replays-8642.lisp:145
;;; (nova-tools #362) --- atomic batches all-or-none, independent batches
;;; preserving their exact accepted prefix and marking the remainder not attempted.

;;; batch-with-bounds-and-urgency: the real replay lives in
;;; tests/replays-8642.lisp:60 (nova-tools #362) --- byte/record bounds,
;;; unchanged batches causing no call, unreferenced padding refused, urgent
;;; corrections bypassing delay, dependencies and retry identities surviving.

;;; batches-and-pipelines: the real replay lives in tests/replays-8642.lisp:145
;;; (nova-tools #362) --- atomic batches all-or-none, independent batches
;;; preserving their exact accepted prefix and marking the remainder not attempted.

;; bounds-are-not-prompts (SPEC-WORK.md:4849) now runs as the real deftest in
;; tests/replays-8642.lisp, with the launcher hard limits it named.
;;; Acceptances promised by docs/SPEC-WORK.md (lines 3600-end) and absent
;;; from slice 1. Each is kept in the file's replay shape but needs session,
;;; CLI, query, paging, capture, clip, render, savepoint, cost, undo or
;;; execution machinery that this internal C/O kernel does not ship. Uncomment
;;; a deftest when its kernel code lands.
;;; ------------------------------------------------------------------

;; busy-day-many-segments and branch-and-window-required are real deftests
;; later in this file, over the closed-history model of
;; src/replays-closed-history.lisp (nova-tools #362).

;; cache-aware-context-choice now runs in
;; lisp/nova-work/tests/replays-8642.lisp (nova-tools #362).

;;; cancel-is-a-request-not-an-erasure: the real replay lives in
;;; tests/acceptance/slice-08-replays-late.lisp:298 (nova-tools #362); this
;;; duplicate parked copy is removed.

;;; chat-and-file-render-are-byte-identical: the real replay lives in
;;; tests/acceptance/slice-09-replays-roadmap.lisp:163 (nova-tools #362); this
;;; duplicate parked copy is removed.

;;; clip-is-one-long-operation: the real replay lives in
;;; tests/acceptance/slice-08-replays-late.lisp:335 (nova-tools #362); this
;;; duplicate parked copy is removed.

;;; clip-names-the-index-that-overflowed: the real replay lives in
;;; tests/acceptance/slice-09-replays-8603.lisp:81 (nova-tools #362); this
;;; duplicate parked copy is removed.

;; closed-paged-without-full-load now lives in tests/acceptance.lisp over the
;; closed-index paging of src/replays-closed-history.lisp (nova-tools #362).

;; closed-row-with-archive-absent now lives in tests/acceptance.lisp over the
;; closed-history index of src/replays-closed-history.lisp (nova-tools #362).

;; compaction-keeps-the-last-copy moved to tests/replays-8643.lisp when its
;; kernel landed (card 8643): compaction never removes the only recoverable
;; copy. The parked stub is gone with it.

;; complete-cost-lineage now runs in
;; lisp/nova-work/tests/replays-8643.lisp (nova-tools #362).

;; copied-journal-grants-nothing moved to tests/replays-8643.lisp when its kernel
;; landed (card 8643): a copied restore inspects in isolation, takes no ownership
;; and dispatches nothing, and a session start over the copy is refused by
;; fencing. The parked stub is gone with it.

;; cost-joins-include-the-coordinator now runs in
;; lisp/nova-work/tests/replays-8643.lisp (nova-tools #362).

;; cursor-pinned-across-a-new-settle now lives in
;; tests/acceptance/slice-04-doing-and-journal.lisp and tests/acceptance.lisp
;; over the closed-index cursor of src/replays-closed-history.lisp (#362).

;; days-merge-by-revision-never-concatenate now lives in tests/acceptance.lisp
;; over the day tree of src/replays-closed-history.lisp (nova-tools #362).

;; dedup-page-unavailable-refuses now lives in
;; tests/acceptance/slice-04-doing-and-journal.lisp over the dedup page gate of
;; src/journal.lisp (nova-tools #362).
;;; ------------------------------------------------------------------
;;; Replays promised by docs/SPEC-WORK.md lines 3600-end (card 280,
;;; part 3 of 8). Each names a replay named in the acceptance appendix or
;;; the efficiency/roadmap tables, and each is a real deftest or a pointer
;;; to where its real deftest lives.
;;; ------------------------------------------------------------------

;; default-window-opens-two-days is implemented in ../acceptance.lisp (card 8601).

;; disconnect-is-not-a-rollback is the executable replay in ../acceptance.lisp
;; (nova-tools #362) over the disconnecting wire session.

;; dispatch-ack-and-ownership-are-three: dispatch, delivery, acknowledgement
;; and accepted ownership distinguished; a pending offer reserves only
;; declared capacity; a timeout launches no duplicate while the old worker
;; may run; a return reconciles before new dispatch.
(deftest "dispatch-ack-and-ownership-are-three" "docs/SPEC-WORK.md:5219"
    "expected=dispatch!=delivery!=ack!=ownership;reservation=declared-only;duplicate=0"
  (let* ((offer (dispatch-offer "req-1" "root/f/t1" "bob" 2))
         (delivery (delivery-receipt offer))
         (ack (acknowledgement offer :stage :accepted))
         (ownership (accepted-ownership offer "bob")))
    ;; the four facts are four records and never one.
    (check-equal :offer (dispatch-fact-kind offer) "dispatch records intent")
    (check-equal :delivery (dispatch-fact-kind delivery) "delivery is its own fact")
    (check-equal :acknowledge (dispatch-fact-kind ack) "acknowledgement is its own fact")
    (check-equal :ownership (dispatch-fact-kind ownership) "accepted ownership is its own fact")
    (check-equal nil (facts-collapsed-p offer delivery ack ownership)
                 "the four facts never collapse into one")
    ;; a pending offer reserves only the explicitly declared capacity.
    (check-equal 2 (pending-offer-reserved offer) "reserves the declared capacity")
    (check-equal 2 (pending-offer-declared offer) "declared capacity is kept apart")
    (check-equal t (pending-offer-p offer) "the offer is pending until reconciled")
    ;; a timeout alone launches no duplicate while the old worker may run.
    (let ((after (offer-after-timeout offer)))
      (check-equal nil (dispatch-launched-p after) "a timeout launches no duplicate")
      (check-equal :overdue (dispatch-fact-state after) "the offer is overdue, unreconciled")
      (check-equal 2 (pending-offer-reserved after) "the reservation stands"))
    ;; one holder has one lease: a second offer to another name is refused.
    (check-equal nil (second-offer-admitted-p offer
                                              (dispatch-offer "req-2" "root/f/t1" "carol" 2))
                 "no shadow lease: a cross-holder offer is refused")))

;; edit-is-atomic-and-replayable now lives in tests/acceptance.lisp over the
;; node-edit verb of src/node-verbs.lisp (nova-tools #362).
;; edit-is-atomic-and-replayable: a bad one-of-five patch writes nothing; an
;; accepted mixed edit moves only its named fields and the category index; the
;; same request id retried answers NODE OK; a changed payload refuses; an
;; equal-value edit is the no-effect receipt, changed=0, rev up by one.

;; efficiency-lessons-gate (SPEC-WORK.md:4916) now runs as the real deftest in
;; tests/replays-8644.lisp, with the dispatch/effort/ceiling gates it named.
;; edit-never-fetches-a-link: a live URL to a counting endpoint added, edited
;; and rendered with zero requests; a link holding NUL refused `bad link`; a
;; refusal on a private node prints no value.
;; efficiency-lessons-gate: prime projection respects --max-bytes; unpoured
;; checklist items never count in |O|; tripped nodes require --reason; delegate
;; mode refuses edits below the model; packets lacking :effort are refused;
;; dispatches crossing the configured daily fleet spend ceiling are refused.
;; efficiency-lessons-gate (SPEC-WORK.md:4439) now runs as the real deftest in
;; tests/replays-8644.lisp, with the prime/effort/ceiling gates it named.
;; edit-never-fetches-a-link now lives in tests/acceptance.lisp over the
;; node-edit link validation and renderer of src/node-verbs.lisp (nova-tools #362).

;; edit-undo-preserves-later-work now lives in tests/acceptance.lisp over the
;; node-edit and node-undo verbs of src/node-verbs.lisp (nova-tools #362).
;; edit-undo-preserves-later-work is a real replay now, over
;; src/edit-undo.lisp in tests/acceptance.lisp (nova-tools #362).

;; endpoint-is-local-and-private is the executable replay earlier in this file
;; (a real deftest over src/replays-slice-05.lisp), so the duplicate stub is
;; deleted.

;; explicit-rest-is-not-pinged: the configured silence threshold triggers one
;; bounded ping; a nonresponsive capacity marked unavailable with reason
;; `unconfirmed` and no claim of sleep or exhausted credit; a failed probe read
;; as unresolved delivery; explicit rest respected.
(deftest "explicit-rest-is-not-pinged" "docs/SPEC-WORK.md:5225"
    "expected=one-bounded-ping;unavailable=unconfirmed;rest=respected"
  (let ((resting (make-availability :state :resting :last-contact 0
                                    :silence-threshold 60))
        (active (make-availability :state :active :last-contact 0
                                   :silence-threshold 60))
        (reserved (make-availability :state :active :last-contact 0
                                     :silence-threshold 60 :reserved t)))
    ;; explicit rest past the threshold is respected: no ping.
    (multiple-value-bind (action reason) (silence-ping resting 1000)
      (check-equal :none action "a resting friend is not pinged")
      (check-equal :explicit-rest reason "the reason names the explicit rest"))
    ;; a reserved friend is not woken by routine silence either.
    (multiple-value-bind (action reason) (silence-ping reserved 1000)
      (check-equal :none action "a reserved friend is not pinged")
      (check-equal :reserved reason "the reason names the reservation"))
    ;; an active friend past the threshold is pinged exactly once.
    (multiple-value-bind (action reason) (silence-ping active 1000)
      (check-equal :ping action "an active friend past the threshold is pinged")
      (check-equal :silence-threshold reason "the reason names the threshold"))
    (mark-pinged active)
    (multiple-value-bind (action reason) (silence-ping active 1000)
      (check-equal :none action "the ping is bounded to one")
      (check-equal :already-pinged reason "the second ask names the bound"))
    ;; a nonresponse is unavailable-unconfirmed, never sleep nor exhausted credit.
    (mark-unconfirmed active)
    (check-equal :unconfirmed (availability-state active)
                 "a nonresponse reads unconfirmed")))

;; fenced-export-can-finish now runs against src/state-export.lisp in
;; slice-09-state-export-replays.lisp (terminal read by id; cancel reconciled;
;; canonical write refused `fenced`; one owner).

;; findings-across-c-and-o is the real deftest in
;; tests/acceptance/slice-04-doing-and-journal.lisp (nova-tools #362).

;; four-capability-groups-and-three-fields: child agents, swarms, local models
;; and one-shots each expressible as a capability group with stable id, source,
;; last-verified stamp, availability and constraints; declared support,
;; verified runtime and current free capacity kept as three fields.
(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:5280"
    "expected=groups=4;fields=support,runtime,free-capacity;never-collapse"
  (let ((groups
          (mapcar (lambda (kind)
                    (make-capability-group
                     :id (format nil "cap-~(~A~)" kind)
                     :kind kind
                     :source "CONFIG"
                     :last-verified "2026-09-15T00:00:00Z"
                     :availability :available
                     :constraints '(:budget "metered")))
                  '(:child-agents :swarms :local-models :one-shots))))
    ;; the four execution capability groups are each expressible.
    (check-equal '(:child-agents :swarms :local-models :one-shots)
                 (mapcar #'capability-group-kind groups)
                 "four capability groups")
    ;; every entry carries a stable id, source, verified stamp, availability
    ;; and its budget or permission constraints.
    (dolist (g groups)
      (ok (stringp (capability-group-id g)) "~A has a stable id"
          (capability-group-kind g))
      (check-string= "CONFIG" (capability-group-source g) "the source is carried")
      (check-string= "2026-09-15T00:00:00Z" (capability-group-last-verified g)
                     "the last-verified stamp is carried")
      (check-equal :available (capability-group-availability g) "availability is carried")
      (check-string= "metered" (getf (capability-group-constraints g) :budget)
                     "the constraints are carried"))
    ;; declared support, runtime verification and free capacity stay three
    ;; fields; a catalog entry is not evidence of a live child or free credits.
    (let ((g (make-capability-group :id "cap-1" :kind :child-agents
                                    :source "CONFIG"
                                    :last-verified "2026-09-15T00:00:00Z"
                                    :availability :available :constraints '()
                                    :declared-support t :runtime-verified nil
                                    :free-capacity nil)))
      (check-equal t (capability-declared-support-p g) "declared support is true")
      (check-equal nil (capability-runtime-verified-p g) "runtime is not verified")
      (check-equal :unknown (capability-free-capacity g) "free capacity is unknown")
      (check-equal nil (capability-fields-collapse-p g)
                   "the three fields never collapse into one"))))

;; a-retry-does-not-overwrite-its-attempt: a retry never overwrites the attempt
;; before it; concurrent attempts keep separate model and usage attribution.
(deftest "a-retry-does-not-overwrite-its-attempt" "docs/SPEC-WORK.md:5222"
    "expected=unknown-stays-unknown;attempts-separate-attribution;never-overwrite"
  (let* ((first (make-attempt :id "att-1" :node "root/f/t1"
                              :requested-model "astra" :observed nil :usage 10))
         (retry (make-attempt :id "att-2" :node "root/f/t1"
                              :requested-model "astra" :observed "beta" :usage 7))
         (attempts (append-attempt (list first) retry)))
    (check-equal 2 (length attempts) "the retry is a second attempt")
    (check-equal t (equal first (first attempts))
                 "the attempt before the retry is not overwritten")
    (check-equal :unknown (attempt-observed-model (first attempts))
                 "the first attempt's unknown observation survives the retry")
    (check-equal 10 (attempt-usage (first attempts)) "the first attempt's usage is intact")
    (check-equal "att-2" (attempt-id (second attempts))
                 "the retry keeps its own identity")))

;; full-round-trip is the real deftest earlier in this file, over
;; state-canonical-form and reconstruct-state (nova-tools #362).

;;; ------------------------------------------------------------------
;;; SPEC-WORK.md lines 3600-end, part 4 of 8: session/CLI-level replays.
;;; Every replay below is a real deftest, in this file or in the slice file
;;; named beside it, over the session, paging, indexes, roadmaps, leases,
;;; moves, provider and hostile-data intake modules the system now ships.
;;; ------------------------------------------------------------------

;; held-acceptance-converts-nothing (SPEC-WORK.md:5258) now runs as the real
;; deftest in tests/replays-8640.lisp.

;; ------------------------------------------------------------------
;; historic-tick-survives-a-source-change   docs/SPEC-WORK.md:5300
;; ------------------------------------------------------------------
;; historic-tick-survives-a-source-change now lives in tests/replays-8645.lisp
;; over the tick records of src/replays-8645.lisp (nova-tools #362).

;; ------------------------------------------------------------------
;; history-grows-startup-does-not   docs/SPEC-WORK.md:5117
;; ------------------------------------------------------------------
;; history-grows-startup-does-not now lives in tests/acceptance.lisp over the
;; closed-history instrumentation of src/replays-closed-history.lisp (#362).

;; hold-survives-a-crash (SPEC-WORK.md:5254) now runs as the real deftest in
;; tests/acceptance/slice-09-replays-holds.lisp.

;; ------------------------------------------------------------------
;; indivisible-record-refused-before-ack   docs/SPEC-WORK.md:5543
;; ------------------------------------------------------------------
;; indivisible-record-refused-before-ack now lives in
;; tests/acceptance/slice-09-replays-8603.lisp over the admission gate of
;; src/closed-history.lisp (nova-tools #362).

;; ------------------------------------------------------------------
;; late-and-duplicate-receipts-are-retained   docs/SPEC-WORK.md:5243
;; ------------------------------------------------------------------
;; late-and-duplicate-receipts-are-retained now lives in tests/acceptance.lisp
;; over the leasebook of src/assignment.lisp (nova-tools #362).

;; ------------------------------------------------------------------
;; local-tokens-cost-zero-api   docs/SPEC-WORK.md:5288
;; ------------------------------------------------------------------
;; local-tokens-cost-zero-api now runs in
;; lisp/nova-work/tests/replays-8646.lisp (nova-tools #362).

;; ------------------------------------------------------------------
;; materialized-working-set   docs/SPEC-WORK.md:5597
;; ------------------------------------------------------------------
;; materialized-working-set now lives in tests/replays-8646.lisp over the
;; materialized W index of src/replays-8646.lisp (nova-tools #362).

;; ------------------------------------------------------------------
;; matrix-retirement   docs/SPEC-WORK.md:5838
;; ------------------------------------------------------------------
;; matrix-retirement now lives in tests/acceptance/slice-09-replays-roadmap.lisp
;; (nova-tools #362).
;; merged-is-not-distributed   docs/SPEC-WORK.md:5093
;; ------------------------------------------------------------------
;; merged-is-not-distributed is the real deftest at the top of this file.

;; ------------------------------------------------------------------
;; metadata-patches-preserve-intent   docs/SPEC-WORK.md:5347
;; ------------------------------------------------------------------
;; The real replay now lives in tests/acceptance.lisp over the node-edit metadata
;; patches of src/node-verbs.lisp (nova-tools #362): keep, clear, set-empty,
;; set-false and set-value round-trip and digest distinctly, a malformed or
;; wrong-type patch is refused with no event and no counter moved, and
;; --version on a :feature is refused.

;; ------------------------------------------------------------------
;; node move   docs/SPEC-WORK.md:3027-3070, :5360-5379
;; ------------------------------------------------------------------
;; move-keeps-every-count lives in tests/acceptance.lisp, where the card that
;; implemented the counters put it. The remaining move replays are real here.

(defun move-request (id from under &key (reason "redistribute")
                                        (request "mv-1")
                                        (stamp "2026-09-17T00:00:00Z"))
  (list :id id :from from :under under :reason reason :request request :stamp stamp))

(deftest "move-keeps-the-lease" "docs/SPEC-WORK.md:5370"
    "lease=same,attempt=same,usage=same,active-change=refused,privacy-reduction=refused"
  (let* ((seed '((:id "root"    :type :work-set :parent nil       :state :unknown)
                 (:id "root/resp-a" :type :feature :parent "root" :state :unknown)
                 (:id "root/resp-b" :type :feature :parent "root" :state :unknown)
                 (:id "root/f1" :type :feature  :parent "root"    :state :unknown
                  :coordinator "root/resp-a")
                 (:id "root/f2" :type :feature  :parent "root"    :state :unknown
                  :coordinator "root/resp-a")
                 (:id "root/f3" :type :feature  :parent "root"    :state :unknown
                  :coordinator "root/resp-b")
                 (:id "root/f4" :type :feature  :parent "root"    :state :unknown
                  :private :true)
                 (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing
                  :holder "carol")
                 (:id "root/f1/t2" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f1/t3" :type :task :parent "root/f1" :state :doing
                  :private :true)
                 (:id "root/f1/t4" :type :task :parent "root/f1" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    ;; A live, unreconciled attempt on t2, and a lease on t1.
    (record-attempt k "root/f1/t2" "att-2")
    (check-equal "carol" (node-holder (kernel-state k) "root/f1/t1") "t1 holds a lease")
    ;; Same effective :responsible: admitted, and the lease/attempt do not move.
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t1" "root/f1" "root/f2"
                                           :request "lease-1"))
      (ok okp "a same-responsible move refused: ~A" line))
    (check-equal "carol" (node-holder (kernel-state k) "root/f1/t1") "the lease is kept")
    (check-equal '("att-2") (live-attempt-ids k "root/f1/t2") "the attempt is kept")
    ;; A move that would change the effective :responsible over a live attempt
    ;; refuses `active context change`, naming the affected ids, and writes nothing.
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t2" "root/f1" "root/f3"
                                           :request "lease-2"))
      (ok (not okp) "a context-changing move must refuse")
      (ok (search "active context change" line) "names the guard: ~A" line)
      (ok (search "root/f1/t2" line) "names the affected id: ~A" line))
    (check-equal "root/f1" (node-parent (kernel-state k) "root/f1/t2")
                 "the refused move wrote nothing")
    ;; Lowering privacy is refused; raising it is admitted.
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t3" "root/f1" "root/f2"
                                           :request "lease-3"))
      (ok (not okp) "a privacy-lowering move must refuse")
      (ok (search "privacy reduction" line) "names the guard: ~A" line))
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t4" "root/f1" "root/f4"
                                           :request "lease-4"))
      (ok okp "raising privacy admitted: ~A" line))
    (check-equal "root/f4" (node-parent (kernel-state k) "root/f1/t4")
                 "the privacy-raising move is applied")))

(deftest "move-refuses-by-name" "docs/SPEC-WORK.md:5367"
    "each-refusal=named,nothing-written"
  (let* ((seed '((:id "root"    :type :work-set :parent nil    :state :unknown
                  :repo "acme/work")
                 (:id "root/f1" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f1/t1/s1" :type :task :parent "root/f1/t1" :state :doing)
                 (:id "root/f2" :type :feature :parent "root"  :state :unknown)
                 (:id "other"   :type :work-set :parent nil    :state :unknown
                  :repo "acme/y")
                 (:id "other/f" :type :feature :parent "other" :state :unknown)))
         (k (make-kernel :state (make-seed-state seed)))
         (f1 (node-children (kernel-state k) "root/f1"))
         (f2 (node-children (kernel-state k) "root/f2")))
    (ok (roadmap-create k :id "root/rm" :parent "root" :title "R" :axes '()
                        :members '() :reason "new" :request "rm-1"
                        :stamp "2026-09-17T00:00:00Z")
        "roadmap create refused")
    (flet ((refuses (reason args)
             (multiple-value-bind (okp line)
                 (apply #'node-move k args)
               (ok (not okp) "~A must refuse: ~A" reason (or line ""))
               (ok (search reason line) "~A names the reason: ~A" reason line))))
      ;; a wrong --from is a `parent conflict`
      (refuses "parent conflict"
               (move-request "root/f1/t1" "root/f2" "root/f2" :request "r1"))
      ;; a destination inside the subtree is a `cycle`
      (refuses "cycle"
               (move-request "root/f1/t1" "root/f1" "root/f1/t1/s1" :request "r2"))
      ;; a root container is `not movable`
      (refuses "not movable"
               (move-request "root" nil "root/f1" :request "r3"))
      ;; a destination under another repository is a `repository change`
      (refuses "repository change"
               (move-request "root/f1/t1" "root/f1" "other/f" :request "r4"))
      ;; a roadmap as the destination is `roadmap operation required`
      (refuses "roadmap operation required"
               (move-request "root/f1/t1" "root/f1" "root/rm" :request "r5"))
      ;; a way is found through the named reason and both parents are unchanged
      (check-equal f1 (node-children (kernel-state k) "root/f1") "source unchanged")
      (check-equal f2 (node-children (kernel-state k) "root/f2") "destination unchanged")
      (check-equal "root/f1" (node-parent (kernel-state k) "root/f1/t1")
                   "the node never moved"))))

(deftest "move-same-parent-is-a-receipt" "docs/SPEC-WORK.md:5364"
    "changed=0,one-envelope,sibling-order=unchanged"
  (let* ((seed '((:id "root"    :type :work-set :parent nil    :state :unknown)
                 (:id "root/f1" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f1/t2" :type :task :parent "root/f1" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)))
         (before (node-children (kernel-state k) "root/f1")))
    (multiple-value-bind (okp line code)
        (apply #'node-move k (move-request "root/f1/t1" "root/f1" "root/f1"
                                           :request "same-1"))
      (ok okp "a same-parent move refused: ~A" line)
      (check-equal 0 code "same-parent exit")
      (ok (search "changed=0" line) "the no-effect receipt: ~A" line)
      ;; a lost reply retried yields the original line and one envelope
      (multiple-value-bind (okp2 line2 code2)
          (apply #'node-move k (move-request "root/f1/t1" "root/f1" "root/f1"
                                             :request "same-1"))
        (ok okp2 "the retry refused")
        (check-equal 0 code2 "retry exit")
        (check-string= line line2 "the retry replays the original line")))
    (check-equal before (node-children (kernel-state k) "root/f1")
                 "the receipt and its retry leave the sibling order unchanged")
    ;; a different payload under the id refuses and still writes nothing
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t2" "root/f1" "root/f1"
                                           :request "same-1"))
      (ok (not okp) "a changed payload under the id must refuse")
      (ok (search "different payload" line) "names the payload conflict: ~A" line))
    (check-equal before (node-children (kernel-state k) "root/f1")
                 "the refusal leaves both parents unchanged")))

(deftest "move-undo-refuses-a-reorder" "docs/SPEC-WORK.md:5379"
    "undo=conflict,position=never-guessed,restore=exact"
  ;; Without an intervening change the undo restores the exact before order.
  (let* ((seed '((:id "root"    :type :work-set :parent nil    :state :unknown)
                 (:id "root/f1" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f2" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f1/t2" :type :task :parent "root/f1" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line)
        (apply #'node-move k (move-request "root/f1/t1" "root/f1" "root/f2"
                                           :request "mv-ok"))
      (ok okp "move refused: ~A" line))
    (multiple-value-bind (okp line)
        (submit k (undo-verb-request "mv-ok" "undo-ok"))
      (ok okp "the undo refused: ~A" line))
    (check-equal '("root/f1/t1" "root/f1/t2") (node-children (kernel-state k) "root/f1")
                 "the exact before order is restored")
    (check-equal '() (node-children (kernel-state k) "root/f2")
                 "the destination returns to its before set"))
  ;; An intervening reparent makes the undo conflict and guess no position.
  (let* ((seed '((:id "root"    :type :work-set :parent nil    :state :unknown)
                 (:id "root/f1" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f2" :type :feature :parent "root"  :state :unknown)
                 (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing)
                 (:id "root/f1/t2" :type :task :parent "root/f1" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    (apply #'node-move k (move-request "root/f1/t1" "root/f1" "root/f2"
                                       :request "reorder-1"))
    (apply #'node-move k (move-request "root/f1/t2" "root/f1" "root/f2"
                                       :request "reorder-2"))
    (let ((after (node-children (kernel-state k) "root/f2")))
      (multiple-value-bind (okp line code)
          (submit k (undo-verb-request "reorder-1" "undo-reorder-1"))
        (ok (not okp) "the undo must refuse after a sibling reparent")
        (check-equal 1 code "the undo conflict exit")
        (ok (search "conflict" line) "names the conflict: ~A" line))
      (check-equal after (node-children (kernel-state k) "root/f2")
                   "the refused undo guessed no position"))))

;; ------------------------------------------------------------------
;; move-updates-every-roadmap-scope   docs/SPEC-WORK.md:5978
;; ------------------------------------------------------------------
;; move-updates-every-roadmap-scope now lives in
;; tests/acceptance/slice-09-replays-roadmap.lisp (nova-tools #362).
;; moving-source   docs/SPEC-WORK.md:6231
;; ------------------------------------------------------------------
;; moving-source is the real deftest in tests/replays-8646.lisp (nova-tools #362).

;;;; ------------------------------------------------------------------
;;;; Draft-25..27 replays promised by docs/SPEC-WORK.md. Each is a real
;;;; deftest: where a verb, feature or wire property is still outside the
;;;; kernel, the replay asserts the boundary refusal -- the feature refuses
;;;; cleanly rather than silently inventing a partial answer.
;;;; ------------------------------------------------------------------

(defun slice1-refuses-verb (verb &key (node "acme/work/f1/t1"))
  "Submit VERB, which the slice-1 kernel does not implement, and assert the
boundary refusal: exit 2, the line names it unsupported, and state is unmoved."
  (let* ((k (fresh))
         (before (root-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (list :verb verb :node node :by "rowan" :reason "r"
                        :request "req-unsup" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4"))
      (ok (not okp) "~A was applied" verb)
      (check-equal 2 code (format nil "~A exit code" verb))
      (ok (search "unsupported" line) "~A refusal does not say unsupported: ~A" verb line))
    (check-string= before (root-digest (kernel-state k))
                   (format nil "~A mutated state" verb))))

;; The six new verbs draft 26 added (SPEC-WORK.md:1014-1058): each writes an
;; event of its own kind, with every field of its ordered list written in order,
;; `:node` written `(:absent)`, and a retry of one request id answering with the
;; original OK line and applying nothing.
(defun new-verb-request (verb &key (request "nv-1") (by "rowan"))
  (append (list :verb verb :by by :request request
                :stamp "2026-09-14T12:00:00Z" :clock :tool
                :generation-owner "gen-4")
          (loop for f in (new-verb-fields verb)
                append (list f
                             (case f
                               (:change :set)
                               (:request-of "req-previous")
                               ((:parts :samples :usage) (list "p-1"))
                               (otherwise (format nil "~A-1"
                                                  (string-downcase (symbol-name f)))))))))

(deftest "new-verbs-have-a-kind-and-a-field-order" "docs/SPEC-WORK.md:5315"
    "expected=own-kind;field-order;:node(:absent);same-bytes"
  (dolist (verb '(:undo :redo :friend :model :observe :config))
    (let* ((k (fresh))
           (request (new-verb-request verb :request (format nil "kind-~A" verb))))
      (multiple-value-bind (okp line code envelope) (submit-new-verb k request)
        (ok okp "~A refused: ~A" verb line)
        (check-equal 0 code (format nil "~A exit code" verb))
        (let ((ev (first (getf envelope :events))))
          (ok ev "~A wrote no event" verb)
          (check-equal (new-verb-kind verb) (work-event-kind ev)
                       (format nil "~A did not write its own kind" verb))
          (check-equal (new-verb-fields verb) (kind-fields (work-event-kind ev))
                       (format nil "~A's kind does not carry its field order" verb))
          (ok (absentp (work-event-node ev)) "~A wrote a node" verb)
          (dolist (f (new-verb-fields verb))
            (check-equal (getf request f) (getf (work-event-fields ev) f)
                         (format nil "~A field ~A is not the request's" verb f))))))
    (let* ((k (fresh))
           (bare (list :verb verb :by "rowan" :request (format nil "bare-~A" verb)
                       :stamp "2026-09-14T12:00:00Z" :clock :tool
                       :generation-owner "gen-4")))
      (multiple-value-bind (okp line code envelope) (submit-new-verb k bare)
        (declare (ignore code))
        (ok okp "~A bare refused: ~A" verb line)
        (let ((ev (first (getf envelope :events))))
          (dolist (f (new-verb-fields verb))
            (ok (absentp (getf (work-event-fields ev) f))
                "~A's absent field ~A is not (:absent)" verb f)))))
    (let* ((a (fresh)) (b (fresh))
           (ra (new-verb-request verb :request (format nil "build-a-~A" verb)))
           (rb (new-verb-request verb :request (format nil "build-b-~A" verb))))
      (multiple-value-bind (oka la ca ea) (submit-new-verb a ra)
        (declare (ignore la ca))
        (multiple-value-bind (okb lb cb eb) (submit-new-verb b rb)
          (declare (ignore lb cb))
          (ok (and oka okb) "~A build refused" verb)
          (check-string= (nova-work::payload-digest (getf ea :events))
                         (nova-work::payload-digest (getf eb :events))
                         (format nil "~A two builds do not digest to the same bytes"
                                 verb)))))))

(deftest "new-verbs-retry-to-one-event" "docs/SPEC-WORK.md:5319"
    "expected=one-event;original-OK;changed-payload-refuses"
  (dolist (verb '(:undo :redo :friend :model :observe :config))
    (let* ((k (fresh))
           (request (new-verb-request verb :request (format nil "retry-~A" verb)))
           (subject (ecase (new-verb-subject verb)
                      (:nodes "nodes=")
                      (:friend "friend=")
                      (:model "model="))))
      (multiple-value-bind (okp line code envelope) (submit-new-verb k request)
        (declare (ignore code))
        (ok okp "~A first submission refused: ~A" verb line)
        (check-equal 1 (length (getf envelope :events))
                     (format nil "~A wrote one event" verb))
        (check-equal 1 (length (state-history (kernel-state k)))
                     (format nil "~A wrote one history record" verb))
        (ok (search subject line) "~A OK line lacks ~A: ~A" verb subject line)
        (ok (not (search "node= " line)) "~A OK line carries an empty node=: ~A"
            verb line)
        (multiple-value-bind (ok2 line2 code2 envelope2)
            (submit-new-verb k request)
          (ok ok2 "~A retry refused: ~A" verb line2)
          (check-equal 0 code2 (format nil "~A retry exit code" verb))
          (check-string= line line2
                         (format nil "~A retry did not return the original OK line" verb))
          (ok (getf envelope2 :replayed) "~A retry applied a second event" verb)
          (check-equal 1 (length (state-history (kernel-state k)))
                       (format nil "~A retry wrote a second event" verb)))
        (let ((changed (copy-list request)))
          (setf (getf changed :reason) "a different reason")
          (multiple-value-bind (ok3 line3 code3) (submit-new-verb k changed)
            (ok (not ok3) "~A accepted the same id with a changed payload" verb)
            (check-equal 1 code3 (format nil "~A changed-payload exit code" verb))
            (ok (search "reused with a different payload" line3)
                "~A refusal does not name the payload reuse: ~A" verb line3)))
        (let ((successor (make-kernel :state (kernel-state k)
                                      :journal (kernel-journal k))))
          (multiple-value-bind (ok4 line4) (submit-new-verb successor request)
            (ok ok4 "~A successor refused: ~A" verb line4)
            (check-string= line line4
                           (format nil "~A successor did not return the original line"
                                   verb))
            (check-equal 1 (length (state-history (kernel-state successor)))
                         (format nil "~A successor applied a second event" verb))))))))

;; no-dispatch-slips-past-a-hold now lives in slice-09-replays-holds.lisp, with
;; the pause/hold and dispatch gate it needed.

;; The no-effect receipt (`no-effect-mutation-is-journaled`) now lives with the
;; node-edit verbs that write it; see tests/acceptance.lisp.

(deftest "no-friend-name-in-the-tool" "docs/SPEC-WORK.md:5209"
    "expected=binary-and-fixtures-carry-no-friend-bench-repo-or-house-name"
  ;; The tool ships no identity: a bare kernel's seed is empty and its fleet has
  ;; no friends, so no friend, bench, repository or house name is a default of
  ;; this build; every identity arrives as configuration.
  (let ((bare (make-kernel)))
    (check-equal 0 (state-open-count (kernel-state bare))
                 "a bare kernel ships of its own")
    (check-equal 0 (state-closed-count (kernel-state bare))
                 "a bare kernel ships a closed fixture")
    (check-equal '() (state-history (kernel-state bare))
                 "a bare kernel ships a history record")
    (check-equal '() (fleet-friends (kernel-fleet bare))
                 "the fleet ships a friend identity"))
  ;; An identity appears only when it is configured, and it is configuration:
  ;; it writes no work state and no history.
  (let ((configured (make-kernel :friends '("configured-friend"))))
    (check-equal '("configured-friend") (fleet-friends (kernel-fleet configured))
                 "the configured identity did not arrive as configuration")
    (check-equal 0 (state-open-count (kernel-state configured))
                 "a configured friend wrote work state")
    (check-equal '() (state-history (kernel-state configured))
                 "a configured friend wrote a work event"))
  ;; The suite's own fixture carries only the placeholder house, never a real
  ;; friend, bench, repository or house name.
  (dolist (node *seed*)
    (let ((id (getf node :id)))
      (ok (and (stringp id) (eql 0 (search "acme/work" id)))
          "seed id ~A is not the placeholder house" id))
    (ok (null (getf node :by)) "seed node ~A ships an author" (getf node :id))))

;; Moved to tests/acceptance.lisp as real replays over src/assignment.lisp
;; (nova-tools #362): a second offer to another name while a holder is pending
;; or accepted is refused -- "no-shadow-lease-across-holders"; an admitted offer
;; writes :effect :dispatched, a pending-offer index entry and a reservation --
;; "offer-writes-intent-and-a-reservation".

;; archive export/reload is the real archive-completeness deftest in
;; tests/replays-8641.lisp, and old-history is the real deftest in this file
;; (nova-tools #362).

;; execution stop as a hold is the real stop-is-a-hold-not-a-cancel deftest in
;; tests/acceptance/slice-09-replays-holds.lisp (nova-tools #362).

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
    "expected=repo-at-root-unique;second-refused-repo-held-by;under-parent-refused;edit-cannot-change"
  ;; SPEC-WORK.md:5792 -- `--repo` under the open root and `--type work-set`
  ;; accepted and unique; the same `--repo` again refused `repo held by <id>`;
  ;; `--repo` under a parent refused `repo outside root`; `node edit` cannot
  ;; change it (node move has no repository field to change).
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
      (ok (search "repo outside root" line) "outside root named: ~A" line))
    (multiple-value-bind (okp line code)
        (node-edit k "r1" :changes (list :repo "acme/z") :request "edit-repo"
                   :reason "change" :stamp "2026-09-17T00:04:00Z")
      (ok (null okp) "a repo patch on node edit must refuse")
      (check-equal 1 code "repo patch exit")
      (ok (search "bad patch repo" line) "the repo patch is named: ~A" line))
    (check-string= "acme/x" (node-repo (kernel-state k) "r1")
                   "node edit left the repository unchanged")))

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

(deftest "offer-writes-intent-and-a-reservation" "docs/SPEC-WORK.md:5229"
    "expected=:effect-:dispatched;pending-offer;reservation-keyed-by-offer-attempt;nothing-else"
  (let* ((book (make-leasebook
                :nodes '(("n1" . :doing))
                :w '(("n1" . "alice"))
                :leases '(("n0" . (:holder "carol"
                                          :deadline "2026-09-20T00:00:00Z"
                                          :default "release")))
                :evidence '(("ev-1"))
                :completion '(("c-1"))))
         (before (list (leasebook-nodes book) (leasebook-w book) (leasebook-leases book)
                       (leasebook-evidence book) (leasebook-completion book))))
    (multiple-value-bind (okp line effect nbook)
        (leasebook-offer book :offer "off-1" :node "n1" :generation "gen-4"
                         :attempt "att-1" :to "alice" :profile "cap@1"
                         :reserve 2 :until "2026-09-14T12:30:00Z" :free-slots 4
                         :profile-ok t :payload-sha256 "sha-1" :staged-digest "sha-1"
                         :current-generation "gen-4")
      (ok okp "offer refused: ~A" line)
      (check-equal :dispatched effect "offer writes :effect :dispatched")
      (ok (alist-get "off-1" (leasebook-pending nbook)) "a pending-offer entry is written")
      (let ((r (alist-get (cons "off-1" "att-1") (leasebook-reservations nbook))))
        (ok r "a reservation keyed by (offer,attempt) is written")
        (check-equal :held (getf r :state) "the reservation is held")
        (check-equal 2 (getf r :reserve) "the declared slots are reserved"))
      (ok (equal (list "off-1") (alist-get "n1" (leasebook-index-node nbook)))
          "the pending offer is indexed under the node")
      (ok (equal (list "off-1") (alist-get "alice" (leasebook-index-friend nbook)))
          "the pending offer is indexed under the friend")
      (check-equal (first before) (leasebook-nodes nbook) "node state unchanged")
      (check-equal (second before) (leasebook-w nbook) "W unchanged")
      (check-equal (third before) (leasebook-leases nbook) "the lease index unchanged")
      (check-equal (fourth before) (leasebook-evidence nbook) "evidence unchanged")
      (check-equal (fifth before) (leasebook-completion nbook) "completion unchanged"))))

(deftest "no-shadow-lease-across-holders" "docs/SPEC-WORK.md:5238"
    "expected=cross-holder-offer-refused;no-shadow-lease;cross-holder-reply-creates-no-lease"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "the first offer to alice is admitted: ~A" line)
    (multiple-value-bind (okp2 line2 effect2 book2)
        (leasebook-offer book :offer "off-2" :node "n1" :generation "gen-4"
                         :attempt "att-2" :to "bob" :profile "cap@1" :reserve 1
                         :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
      (declare (ignore effect2))
      (ok (null okp2) "a cross-holder offer is refused")
      (ok (search "shadow" line2) "the refusal names the shadow lease: ~A" line2)
      (check-equal nil (alist-get (cons "off-2" "att-2") (leasebook-reservations book2))
                   "the refused offer writes no reservation")
      (check-equal (leasebook-leases book) (leasebook-leases book2) "no lease is created"))
    (let* ((held (make-leasebook
                  :nodes '(("n1" . :doing))
                  :leases '(("n1" . (:holder "alice"
                                             :deadline "2026-09-20T00:00:00Z"
                                             :default "release")))
                  :holders '(("n1" . "alice"))
                  :pending '(("off-9" :offer "off-9" :node "n1" :to "bob"
                              :attempt "att-9" :generation "gen-4" :reserve 1
                              :until "2026-09-14T12:30:00Z" :state :pending
                              :received t))
                  :reservations '((("off-9" . "att-9") :reserve 1 :state :held))))
           (nleases (length (leasebook-leases held))))
      (multiple-value-bind (okp3 line3 effect3 held2)
          (leasebook-accepted held :offer "off-9" :node "n1" :generation "gen-4"
                              :attempt "att-9" :by "bob" :default "release"
                              :deadline "2026-09-21T00:00:00Z")
        (ok okp3 "a cross-holder reply is retained, not applied: ~A" line3)
        (check-equal :late effect3 "a cross-holder accept is retained :late")
        (check-equal nleases (length (leasebook-leases held2))
                     "the cross-holder reply creates no lease")
        (check-equal 1 nleases "the lease index still holds only alice's one lease")))))

(deftest "accepted-creates-one-lease-or-binds" "docs/SPEC-WORK.md:5238"
    "expected=one-lease-or-bind-to-holders-own;deadline-and-default-unchanged-on-bind"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (multiple-value-bind (rok rline reffect rbook)
        (leasebook-received book :offer "off-1" :node "n1" :generation "gen-4"
                            :attempt "att-1" :receipt-digest "rd-1" :receipt-id "rc-1"
                            :request "q-recv" :verifier "op-verifier")
      (declare (ignore reffect))
      (ok rok "received: ~A" rline)
      (multiple-value-bind (aok aline aeffect abook)
          (leasebook-accepted rbook :offer "off-1" :node "n1" :generation "gen-4"
                              :attempt "att-1" :by "alice" :default "release"
                              :deadline "2026-09-14T12:30:00Z" :observed-model nil
                              :receipt-id "rc-2" :receipt-digest "rd-2" :request "q-acc")
        (ok aok "accepted: ~A" aline)
        (check-equal :accepted aeffect "the accepted envelope writes :effect :accepted")
        (check-equal 1 (length (leasebook-leases abook)) "exactly one lease is created")
        (check-equal 1 (length (leasebook-w abook)) "exactly one W entry is created")
        (check-equal :unknown (alist-get "n1" (leasebook-observed-models abook))
                     "an absent observed model is recorded unknown")
        (multiple-value-bind (o2ok o2line o2effect o2book)
            (leasebook-offer abook :offer "off-2" :node "n1" :generation "gen-4"
                             :attempt "att-2" :to "alice" :profile "cap@1" :reserve 1
                             :until "2026-09-14T13:00:00Z" :free-slots 4 :profile-ok t)
          (declare (ignore o2effect))
          (ok o2ok "a second offer to the same holder is admitted: ~A" o2line)
          (multiple-value-bind (r2ok r2line r2effect r2book)
              (leasebook-received o2book :offer "off-2" :node "n1" :generation "gen-4"
                                  :attempt "att-2" :receipt-digest "rd-3" :receipt-id "rc-3"
                                  :request "q-recv2" :verifier "op-verifier")
            (declare (ignore r2effect))
            (ok r2ok "second received: ~A" r2line)
            (multiple-value-bind (a2ok a2line a2effect a2book)
                (leasebook-accepted r2book :offer "off-2" :node "n1" :generation "gen-4"
                                    :attempt "att-2" :by "alice" :default "escalate:bob"
                                    :deadline "2026-09-30T00:00:00Z"
                                    :receipt-id "rc-4" :receipt-digest "rd-4" :request "q-acc2")
              (ok a2ok "second accepted binds: ~A" a2line)
              (check-equal :accepted a2effect "binding is still an :accepted")
              (check-equal 1 (length (leasebook-leases a2book))
                           "binding writes no second lease")
              (let ((lease (alist-get "n1" (leasebook-leases a2book))))
                (check-equal "2026-09-14T12:30:00Z" (getf lease :deadline)
                             "the held lease's deadline is unchanged")
                (check-equal "release" (getf lease :default)
                             "the held lease's default is unchanged")))))))))

(deftest "until-is-overdue-not-released" "docs/SPEC-WORK.md:5243"
    "expected=overdue-unreconciled;no-auto-launch;reservation-stands;lease-expiry-out-of-w"
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (multiple-value-bind (uok uline ueffect ubook)
        (lease-until book :offer "off-1" :now "2026-09-14T13:00:00Z")
      (ok uok "until: ~A" uline)
      (check-equal :overdue ueffect "at --until the offer is :overdue")
      (check-equal :overdue (getf (alist-get "off-1" (leasebook-pending ubook)) :state)
                   "the offer is overdue and unreconciled")
      (check-equal :held
                   (getf (alist-get (cons "off-1" "att-1")
                                    (leasebook-reservations ubook)) :state)
                   "the reservation stands")
      (check-equal nil (alist-get "n1" (leasebook-leases ubook))
                   "no lease is created or released")
      (multiple-value-bind (nok nline neffect nbook)
          (leasebook-offer ubook :offer "off-2" :node "n1" :generation "gen-4"
                           :attempt "att-2" :to "alice" :profile "cap@1" :reserve 1
                           :until "2026-09-14T14:00:00Z" :free-slots 4 :profile-ok t)
        (declare (ignore neffect))
        (ok (null nok) "no new offer is admitted for an overdue offer")
        (check-equal (leasebook-pending ubook) (leasebook-pending nbook)
                     "the refused offer changes nothing"))
      (let* ((accepted (accepted-book))
             (xok (nth-value 0 (leasebook-expire accepted :node "n1"
                                                :now "2026-09-15T00:00:00Z")))
             (xbook (nth-value 3 (leasebook-expire accepted :node "n1"
                                                   :now "2026-09-15T00:00:00Z"))))
        (ok xok "lease expiry is admitted")
        (check-equal 0 (length (leasebook-w xbook))
                     "lease expiry takes the task out of W")
        (ok (alist-get "n1" (leasebook-leases xbook))
            "lease expiry retains the friend's capacity and any uncertain execution")))))

(deftest "late-and-duplicate-receipts-are-retained" "docs/SPEC-WORK.md:5243"
    "expected=:effect-:late-retained;duplicate-consumes-no-capacity;conflicting-bytes-refused"
  (let ((book (declined-book)))
    (multiple-value-bind (okp line effect nbook)
        (leasebook-accepted book :offer "off-1" :node "n1" :generation "gen-4"
                            :attempt "att-1" :by "alice" :default "release"
                            :deadline "2026-09-14T13:00:00Z")
      (ok okp "a late accept is retained, not refused: ~A" line)
      (check-equal :late effect "the late accept carries :effect :late")
      (check-equal nil (alist-get "n1" (leasebook-leases nbook))
                   "a late accept revives no lease")))
  (multiple-value-bind (okp line effect book)
      (leasebook-offer (make-leasebook :nodes '(("n1" . :doing)))
                       :offer "off-1" :node "n1" :generation "gen-4" :attempt "att-1"
                       :to "alice" :profile "cap@1" :reserve 2
                       :until "2026-09-14T12:30:00Z" :free-slots 4 :profile-ok t)
    (declare (ignore effect))
    (ok okp "offer: ~A" line)
    (let ((before (leasebook-reservations book)))
      (multiple-value-bind (rok rline reffect rbook)
          (leasebook-receipt book :receipt-id "rc-1" :receipt-digest "rd-1"
                             :request "q1" :stage :received :offer "off-1" :node "n1"
                             :generation "gen-4" :attempt "att-1" :verifier "v")
        (declare (ignore reffect))
        (ok rok "first receipt: ~A" rline)
        (multiple-value-bind (dok dline deffect dbook)
            (leasebook-receipt rbook :receipt-id "rc-1" :receipt-digest "rd-1"
                               :request "q2" :stage :received :offer "off-1" :node "n1"
                               :generation "gen-4" :attempt "att-1" :verifier "v")
          (ok dok "duplicate receipt: ~A" dline)
          (check-equal :duplicate deffect "a duplicate receipt is :duplicate")
          (check-equal before (leasebook-reservations dbook)
                       "a duplicate consumes no capacity twice")
          (multiple-value-bind (cok cline ceffect cbook)
              (leasebook-receipt dbook :receipt-id "rc-1" :receipt-digest "other-bytes"
                                 :request "q3" :stage :received :offer "off-1" :node "n1"
                                 :generation "gen-4" :attempt "att-1" :verifier "v")
            (declare (ignore ceffect))
            (ok (null cok) "conflicting bytes for one receipt id are refused")
            (ok (search "conflicting" cline)
                "the refusal names the conflict: ~A" cline)))))))
