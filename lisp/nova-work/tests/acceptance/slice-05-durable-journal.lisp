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
;;; to the prose line where it is first named. This slice is the
;;; internal C/O transition kernel only (no CLI, no socket, no provider,
;;; no clip, no undo, no retention window — see README.md), so each of
;;; these asserts a sentence the kernel here cannot yet satisfy. They
;;; are kept in the file's deftest shape, marked NEEDS-KERNEL, and
;;; counted, exactly as the card asks.
;;; ------------------------------------------------------------------

;;; endpoint-is-local-and-private  SPEC-WORK.md prose :2646-2653 / table :5727
(deftest "endpoint-is-local-and-private" "docs/SPEC-WORK.md:5727"
    "session-dir=0700;socket=0600;wider-mode-refused;no-network-bind"
  ;; the session's directory created 0700 and its socket 0600, both owned
  ;; by the running account; a pre-existing directory or socket with wider
  ;; modes refused rather than reused; no listener on any network address.
  (let* ((base (concatenate 'string (namestring (uiop:temporary-directory))
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

;;; wire-integers-are-strings  SPEC-WORK.md prose :2510 / table :5159
;; (deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
;;     "int>2^53-round-trips-as-string;json-number-frame-refused;null-and-absent-alike"
;;   ;; an id, a revision, a counter and a token total each above 2^53 crossing
;;   ;; the wire and returning unchanged; a frame carrying a JSON number refused;
;;   ;; null and an absent key reading alike, an empty string and empty array as
;;   ;; values.)
;; NEEDS-KERNEL: the wire codec/transport (no JSON frame or wire exists here).

;;; protocol-version-negotiated-or-refused  SPEC-WORK.md table :5162
;; (deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
;;     "unsupported-version-refused-with-list;no-request-before-handshake;oversized-frame-refused"
;;   ;; a client offering an unsupported version refused with the supported list
;;   ;; named and the connection closed; no request admitted before the handshake;
;;   ;; an oversized frame refused with one framed error before the close.)
;; NEEDS-KERNEL: the protocol handshake and connection admission (no transport).

;;; pipeline-replies-are-correlated  SPEC-WORK.md prose :2529 / table :5165
;; (deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
;;     "every-response-reaches-only-its-request;operation-id-distinct;unknown-id-closes"
;;   ;; pipeline two queries, a mutation and a long-operation acceptance, deliver
;;   ;; response frames out of order and in fragments: every response matches only
;;   ;; its request; unknown/duplicate/absent response ids close without falsely
;;   ;; settling an outstanding request.)
;; NEEDS-KERNEL: the pipelined transport with response-id correlation (no socket).

;;; disconnect-is-not-a-rollback  SPEC-WORK.md prose :2540 / table :5173
;; (deftest "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
;;     "event-stands-after-kill;same-id-returns-recorded-disposition;different-args-refused;rev/pushed-distinct"
;;   ;; a client killed after its mutation was journaled: the event stands, the same
;;   ;; request id and body returns the recorded disposition, the same id with
;;   ;; different arguments is refused, and rev= and pushed= are distinct.)
;; NEEDS-KERNEL: socket disconnect + a pushed= counter (slice-1 receipt has pushed=-).

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

;;; undo-appends-and-preserves  SPEC-WORK.md prose :2665 / table :5192
;; (deftest "undo-appends-and-preserves" "docs/SPEC-WORK.md:5192"
;;     "compensating-envelope-with-lineage;original-event-and-receipts-untouched"
;;   ;; an undo of a named request appending a typed compensating envelope with its
;;   ;; lineage while the original event and every receipt stay exactly where they are.)
;; NEEDS-KERNEL: the undo verb and its reversible-verb table (no undo in slice).

;;; redo-refuses-a-stale-plan  SPEC-WORK.md prose :2666 / table :5194
;; (deftest "redo-refuses-a-stale-plan" "docs/SPEC-WORK.md:5194"
;;     "stale-precondition-refused-atomically;names-what-changed;writes-nothing;undo-not-deleted"
;;   ;; a redo whose preconditions moved refused atomically, naming what changed,
;;   ;; writing nothing, and never reached by deleting the undo.)
;; NEEDS-KERNEL: the redo verb (no undo/redo stack in slice 1).

;;; undo-refuses-an-external-effect  SPEC-WORK.md prose :2666 / table :5201
;; (deftest "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5201"
;;     "sent/paid/published/deleted-refused-as-external;history-never-reset"
;;   ;; an undo over a sent message, a paid execution, a publication and a source
;;   ;; deletion refused and reported as an external effect; shared Git history
;;   ;; never reset as the undo path.)
;; NEEDS-KERNEL: undo plus external-effect awareness (no such model here).

;;; undo-names-its-reversible-set  SPEC-WORK.md prose :2721 / table :5332
;; (deftest "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5332"
;;     "each-reversible-verb-undone-by-table;refused-verb-refused-named;terminal-dispositions-refused"
;;   ;; every row of the reversible-verb table exercised; each refused verb refused
;;   ;; `not reversible here` naming itself; an undo over cancel and node remove
;;   ;; refused because both dispositions are terminal.)
;; NEEDS-KERNEL: the reversible-verb table and undo (does not exist in slice 1).

;;; clip-is-one-long-operation  SPEC-WORK.md prose :2568 / table :5327
;;; The real replay lives in tests/acceptance/slice-08-replays-late.lisp:335
;;; (nova-tools #362): clip prints OPERATION OK id= op=clip and exits, wait
;;; prints CLIP OK, a raced transport prints CLIP RACED, and session stop waits
;;; on its own clip within --git-timeout.

;;; repo-only-at-the-root  SPEC-WORK.md prose :2846 / table :5341
;;; The real replay now lives in tests/acceptance.lisp over the node verbs of
;;; src/node-verbs.lisp (nova-tools #362).

;;; roadmap-has-one-creator  SPEC-WORK.md prose :2846 / table :5344
;; (deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
;;     "add-roadmap-exits-2-naming-create;create-writes-node+view-in-one-envelope;crash-all-or-none"
;;   ;; `node add --type roadmap` exit 2 naming `roadmap create`; `roadmap create`
;;   ;; writing one node and one view in one envelope, a crash between them
;;   ;; replaying all-or-none; no second alias.)
;; NEEDS-KERNEL: the roadmap verbs (no roadmap in slice 1).

;;; metadata-patches-preserve-intent  SPEC-WORK.md prose :2844 / table :5347
;;; The real replay now lives in tests/acceptance.lisp over the node-edit verbs
;;; of src/node-verbs.lisp (nova-tools #362).

;;; edit-is-atomic-and-replayable  SPEC-WORK.md prose :2845 / table :5351
;;; The real replay now lives in tests/acceptance.lisp over the node-edit verbs
;;; of src/node-verbs.lisp (nova-tools #362).

;;; edit-undo-preserves-later-work  SPEC-WORK.md prose :2845 / table :5355
;;; The real replay now lives in tests/acceptance.lisp over the node-edit and
;;; node-undo verbs of src/node-verbs.lisp (nova-tools #362).

;;; edit-never-fetches-a-link  SPEC-WORK.md prose :2845 / table :5357
;;; The real replay now lives in tests/acceptance.lisp over the node-edit link
;;; validation and renderer of src/node-verbs.lisp (nova-tools #362).

;;; The node-move replays (move-keeps-every-count, move-keeps-the-lease,
;;; move-refuses-by-name, move-same-parent-is-a-receipt and
;;; move-undo-refuses-a-reorder) are real deftests in the move section at the
;;; end of this file; move-keeps-every-count also stands in tests/acceptance.lisp.
;;; Replays promised by docs/SPEC-WORK.md:2400-3600 but whose verb/kernel
;;; machinery (move, roadmap/axis, render, priority, state-export) does
;;; not yet exist in this slice-1 kernel. Each is kept, marked
;;; NEEDS-KERNEL, and counted but not yet run.
;;; ------------------------------------------------------------------

;;; NEEDS-KERNEL: move-updates-every-roadmap-scope (SPEC-WORK.md:2891) — the
;;;   `move` verb: a row referenced by two roadmaps outside both parent chains
;;;   and one unrelated roadmap advances both referencing scope revisions in
;;;   the envelope, leaves the unrelated one, a failed acceptance moves none,
;;;   historical renders keep the old captured scope, and an intervening
;;;   affected-roadmap mutation makes undo conflict.

;;; NEEDS-KERNEL: axisless-history (SPEC-WORK.md:2948) — `roadmap row`/axis:
;;;   two ordered rows added, one finished, the state exported and loaded, the
;;;   view reopened past the default window: both rows and their evidence
;;;   present, the denominator not reduced by completion; a row retired records
;;;   a scope movement, keeps its node, and the prior view reconstructs at its
;;;   captured revision.

;;; NEEDS-KERNEL: matrix-retirement (SPEC-WORK.md:2948) — `axis --remove`: of a
;;;   first-axis row then of another axis's member, only the selected
;;;   coordinates retired and recoverable, no task cancelled, an unknown member
;;;   refused, a layout change on a populated roadmap refused "layout
;;;   populated" with no partial write, and a matrix never flattened without
;;;   explicit selections.

;;; NEEDS-KERNEL: configure-no-effect-and-undo-conflict (SPEC-WORK.md:2949) —
;;;   `roadmap configure`: an equal-value configure, its reply lost, a later
;;;   edit, then the retry: the original receipt returned and the later value
;;;   kept; undo restores an ordered preimage only while its guards match.

;;; NEEDS-KERNEL: completed-view-mutation (SPEC-WORK.md:2949) — metadata,
;;;   projection and render on a settled roadmap revive nothing; an outstanding
;;;   member added applies the atomic revival rule so no settled container
;;;   silently holds open required work; counts and indexes are checked by the
;;;   reference fold after each step.

;;; NEEDS-KERNEL: render-refuses-a-target-outside-its-roots (SPEC-WORK.md:2950)
;;;   — `render`: a projection target is resolved only within explicitly
;;;   configured permitted roots; a missing mapping or a conflicting change is
;;;   an explicit refusal and never a guessed destination.

;;; NEEDS-KERNEL: render-artifact-is-bounded (SPEC-WORK.md:2977) — `render`:
;;;   ordinary replies and a --chat artifact interleaved in one correlated
;;;   batch, request ids, byte length and hash verified; a corrupt or oversized
;;;   artifact is a bounded refusal and never partial Markdown; --check creates
;;;   no target, no receipt claiming a write, no commit and no push.

;;; NEEDS-KERNEL: priority-orders-only-the-eligible (SPEC-WORK.md:3015) — a
;;;   blocked rank-0 task stays blocked with its reason and resolver while a
;;;   rank-9 ready sibling is first among the eligible; --order priority under
;;;   done exits 2; capacity loss, approval withdrawal, a dependency change or
;;;   a hold is rechecked before ranking and starts or interrupts nothing.

;;; NEEDS-KERNEL: priority-inherits-and-clears (SPEC-WORK.md:3015) — a root
;;;   :subtree rank changes ready order with no lease, attempt, state, O, C, W,
;;;   counter, baseline or roadmap moved; a child's :self overrides it; a clear
;;;   reveals the parent; settle and reopen keep the slots; a move re-reads
;;;   inheritance with no cloned event.

;;; NEEDS-KERNEL: rank-2-precedes-10 (SPEC-WORK.md:3015) — ranks are compared
;;;   as integers, and equal and default rows are ordered by id across a
;;;   restart, a handoff, a cursor continuation and skewed clocks; a first
;;;   unseen filter is O(k log k), later pages come from the pinned order, and
;;;   a subtree invalidation touches no unrelated scope and no C.

;;; NEEDS-KERNEL: priority-undo-is-history-not-value (SPEC-WORK.md:3016) — a
;;;   same-value set and a clear of an absent slot are each the no-effect
;;;   receipt; set 2, set 9, set 2, then undo of the first is refused although
;;;   the value matches.

;;; NEEDS-KERNEL: priority-grants-nothing (SPEC-WORK.md:3016) — with priority
;;;   set on every node, `who` is unchanged, no lease is written, no worker is
;;;   selected, and no approval is bypassed.
;;; render, priority and rank replays now run in
;;; tests/acceptance/slice-06-replays-early.lisp and slice-07-replays-mid.lisp
;;; (nova-tools #362).

;;; NEEDS-KERNEL: state-export-describes-exactly-r (SPEC-WORK.md:3117) —
;;;   `session export --state --at <revision>`: capturing R while R+1 is
;;;   accepted yields bytes that describe R; an exact-snapshot export with B
;;;   equal to R and an absent end, and one with B below R over a multi-record
;;;   prefix across a rotation; an absent end below R, a missing or swapped
;;;   record, a wrong end hash or revision and a cut inside an envelope are each
;;;   refused, and a present later tail is never replayed.

;;; NEEDS-KERNEL: state-export-is-one-long-operation (SPEC-WORK.md:3117) — an
;;;   export blocked on archive I/O acknowledges its operation at once, status,
;;;   cancel and an unrelated mutation stay responsive under it, and wait
;;;   returns the same operation and captured revision after publication or
;;;   refusal; an export inside an atomic batch is refused by entry id.

;;; NEEDS-KERNEL: state-export-pin-survives-clip (SPEC-WORK.md:3118) — capturing
;;;   R, a clip and a retention pass at R+1 during the copy, then exactly R
;;;   completes or a recovery gap is named; no pinned member is reclaimed and
;;;   no current bytes are substituted.

;;; NEEDS-KERNEL: state-export-disconnect-and-cancel (SPEC-WORK.md:3118) — a
;;;   lost client, a restart and a cancellation around the no-replace
;;;   publication keep one operation and one output identity, no duplicate
;;;   directory and no claim to reverse a published one; an existing destination
;;;   is refused; a staged manifest before the commit is not published.

;;; NEEDS-KERNEL: state-export-refuses-a-gap (SPEC-WORK.md:3119) — a missing
;;;   mandatory member, a changed digest, a dangling internal reference, a path
;;;   escape, a symlink, an output overrun and a corrupt S-expression are each
;;;   refused with no valid load; a historical export whose resolver observations
;;;   are gone is refused with a named proof gap and never given current ones;
;;;   --closed-history range over [from,to) declares its omissions while keeping
;;;   closure, all reaches C past the resident window and a fresh load reproduces
;;;   its proof.
;;; ------------------------------------------------------------------
;;; 50-71. replays promised by docs/SPEC-WORK.md:2400-3600 (part 3 of 4)
;;;
;;; Every one of these names a line of SPEC-WORK.md; the three the slice-1
;;; kernel can already execute (an export via state-canonical-form and a load
;;; via reconstruct-state) are green deftest forms. The rest describe CONFIG and
;;; ACTIVE roles, the fleet, dispatch/ack/offers, model attribution, silence
;;; pinging and the bounded config exchange -- none of which exists in slice 1
;;; yet -- and are kept, marked NEEDS-KERNEL with the missing piece.
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
;; NEEDS-KERNEL: a fenced session and the operation-id read/cancel of an export.
;;   In a fenced session an export started, its status and terminal line read by
;;   id, an unfinished one cancelled, an unknown/non-export id and every
;;   canonical write refused `fenced`.

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

;;; 57. four-capability-groups-and-three-fields   docs/SPEC-WORK.md:5280
;;;
;; NEEDS-KERNEL: child-agents, swarms, local-models and one-shots as capability
;;   groups, each with stable id/source/stamp/availability/constraints, and
;;   declared support, verified runtime and free capacity as three fields.

;;; 58. dispatch-ack-and-ownership-are-three (SPEC-WORK.md:5219) now runs as
;;; the real deftest below, over the four-fact dispatch records.
;;; 58. dispatch-ack-and-ownership-are-three   docs/SPEC-WORK.md:5219
;;;
;; NEEDS-KERNEL: dispatch/delivery/acknowledgement and accepted-ownership as
;;   distinct facts; a pending offer reserving only declared capacity; a timeout
;;   alone launching no duplicate.

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

;;; 62. explicit-rest-is-not-pinged   docs/SPEC-WORK.md:5225
;;;
;; NEEDS-KERNEL: observed explicit-rest state and ping gating on it.
;;   Explicit rest is respected: a resting friend is not pinged by a silence
;;   threshold.

;;; 63. return-reconciles-before-dispatch (SPEC-WORK.md:5226) now runs as the
;;; real deftest in slice-07-replays-mid.lisp.
;;; 63. return-reconciles-before-dispatch   docs/SPEC-WORK.md:5226
;;;
;; NEEDS-KERNEL: return reconciling outstanding assignments and capacity.
;;   A return reconciles outstanding assignments and observed capacity before
;;   any new dispatch.

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

;; NEEDS-KERNEL: fleet `query --for` writes no lease and leaves `who` unchanged.
;; (deftest "fleet-for-is-a-recommendation-not-a-lease" "docs/SPEC-WORK.md:3377"
;;     "expected=for-writes-no-lease;who-unchanged")

;; NEEDS-KERNEL: fleet `query --for` over an excluding member is a refusal, not an empty answer.
;; (deftest "an-excluded-choice-is-refused-not-empty" "docs/SPEC-WORK.md:3378"
;;     "expected=excluded-kind-refused;never-empty-rows")

;; four-facts-four-verbs (SPEC-WORK.md:3398) now runs as the real deftest in
;; tests/acceptance/slice-09-fleet-assignment.lisp.
;; NEEDS-KERNEL: offer/acknowledge/decline verbs keep the four facts apart, infer none, launch none.
;; (deftest "four-facts-four-verbs" "docs/SPEC-WORK.md:3398"
;;     "expected=dispatch-delivery-accepted-ownership-stay-apart;nothing-inferred;nothing-launched")

;; NEEDS-KERNEL: acknowledge/decline admit only behind an operator-configured verifier.
;; (deftest "a-receipt-needs-a-verifier" "docs/SPEC-WORK.md:3413"
;;     "expected=unverified-provenance-refused;state-unchanged;no-bus-body-authority")

;; NEEDS-KERNEL: staged verifier/provenance/payload inputs run outside the mutation loop; stale/failed stage writes nothing.
;; (deftest "staged-admission-refuses" "docs/SPEC-WORK.md:3414"
;;     "expected=stale-or-failed-stage-writes-nothing")

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

;; NEEDS-KERNEL: a clip that publishes during a live capture carries the pin forward; the capture never reads a newer scope.
;; (deftest "capture-survives-clip" "docs/SPEC-WORK.md:3517"
;;     "expected=clip-carries-pin-forward;no-reconstruct-from-newer-scope")

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

;; NEEDS-KERNEL: verifier/payload reader and staged admission; no verify verb exists yet.
;; a-receipt-needs-a-verifier (SPEC-WORK.md:5234) --- a copied note and an --as <recipient>
;; with no verifier result refused with no canonical write; a verifier returning after a
;; conflicting revision or failing validation writes no reservation, receipt, lease or W change.

;; NEEDS-KERNEL: attempt/usage attribution records; no attempt model exists yet.
;; a-retry-does-not-overwrite-its-attempt (SPEC-WORK.md:5222) --- unknown staying unknown,
;; concurrent attempts keeping separate model and usage attribution, a friend's usual model
;; never standing as proof of a delegated task's executor.

;; Now asserted by tests/acceptance/slice-07-replays-mid.lisp.
;; a-root-id-grants-nothing (SPEC-WORK.md:5402) --- a stored permitted root with no
;; --render-root mapping refusing file mode while --chat renders; escaping/symlink/target-identity
;; refusals; the cooperative lock and external-editor limit retained.

;; NEEDS-KERNEL: savepoint/checkpoint distinction; no savepoint exists yet.
;; a-savepoint-is-not-a-shared-backup (SPEC-WORK.md:5308) --- restore takes no ownership,
;; reanimates no assignment, replays no message; savepoint age, local and shared revisions,
;; unshared work and failed backups readable; a savepoint never printed where a checkpoint asked.

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

;; NEEDS-KERNEL: roadmap row ordering and completion; no roadmap verb exists yet.
;; axisless-history (SPEC-WORK.md:5383) --- two ordered rows, one finished, exported/loaded and
;; reopened past the window: both rows and evidence present, denominator not reduced by completion.

;; bare-correct-refuses-under-execution (SPEC-WORK.md:5272) now runs as the
;; real deftest in tests/replays-8640.lisp.

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

;; (deftest "busy-day-many-segments" "docs/SPEC-WORK.md:5115"
;;     "expected=many-segments-read-in-bounded-pages;max-caps-rows;more-names-after;day-never-read-whole"
;;   ;; NEEDS-KERNEL: closed-history index with day segments and paged reads
;;   )
;; (deftest "branch-and-window-required" "docs/SPEC-WORK.md:5095"
;;     "expected=query-ask-without-branch-refused-exit-2;closed-without-from-to-refused;from-under-open-refused;who-stale-handoffs-refused-under-closed-and-root"
;;   ;; NEEDS-KERNEL: query command and --branch/--window flag validation at exit 2
;;   )

;; busy-day-many-segments now lives in tests/acceptance.lisp over the
;; closed-history model of src/replays-closed-history.lisp (nova-tools #362).

;; cache-aware-context-choice now runs in
;; lisp/nova-work/tests/replays-8642.lisp (nova-tools #362).

;;; cancel-is-a-request-not-an-erasure: the real replay lives in
;;; tests/acceptance/slice-08-replays-late.lisp:298 (nova-tools #362); this
;;; duplicate parked copy is removed.

;; (deftest "chat-and-file-render-are-byte-identical" "docs/SPEC-WORK.md:5304"
;;     "expected=chat-and-marker-region-bytes-identical;other-bytes-preserved;missing-duplicate-reversed-marker-refused;target-outside-roots-refused"
;;   ;; NEEDS-KERNEL: render with marker regions and permitted-roots boundary
;;   )

;;; clip-is-one-long-operation: the real replay lives in
;;; tests/acceptance/slice-08-replays-late.lisp:335 (nova-tools #362); this
;;; duplicate parked copy is removed.
;; (deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5327"
;;     "expected=clip-operation-ok-op-pushed;wait-prints-clip-ok-operation;raced-prints-clip-raced;session-stop-prints-clip-ok-then-session-ok"
;;   ;; NEEDS-KERNEL: clip long-operation protocol and operation wait id
;;   )

;;; clip-names-the-index-that-overflowed: the real replay lives in
;;; tests/acceptance/slice-09-replays-8603.lisp:81 (nova-tools #362); this
;;; duplicate parked copy is removed.

;; closed-paged-without-full-load now lives in tests/acceptance.lisp over the
;; closed-index paging of src/replays-closed-history.lisp (nova-tools #362).

;; closed-row-with-archive-absent now lives in tests/acceptance.lisp over the
;; closed-history index of src/replays-closed-history.lisp (nova-tools #362).

;; (deftest "compaction-keeps-the-last-copy" "docs/SPEC-WORK.md:5309"
;;     "expected=compaction-never-removes-the-only-recoverable-copy"
;;   ;; NEEDS-KERNEL: compaction over savepoints preserves the last copy
;;   )

;; complete-cost-lineage now runs in
;; lisp/nova-work/tests/replays-8643.lisp (nova-tools #362).

;; (deftest "completed-view-mutation" "docs/SPEC-WORK.md:5394"
;;     "expected=metadata-projection-render-on-settled-roadmap-revive-nothing;member-add-applies-atomic-revival;counts-indexes-checked"
;;   ;; NEEDS-KERNEL: roadmap view mutation and atomic revival rule
;;   )

;; (deftest "configure-no-effect-and-undo-conflict" "docs/SPEC-WORK.md:5391"
;;     "expected=equal-configure-original-receipt-and-later-value-kept;undo-restores-preimage-only-while-guards-match"
;;   ;; NEEDS-KERNEL: configure + undo retry/guard machinery
;;   )

;; (deftest "copied-journal-grants-nothing" "docs/SPEC-WORK.md:5535"
;;     "expected=restore-inspects-in-isolation-takes-no-ownership-dispatches-nothing;session-start-over-copy-refused-by-fencing"
;;   ;; NEEDS-KERNEL: savepoint restore fencing and bench identity rules
;;   )

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
;;; the efficiency/roadmap tables. Slice 1 is the C/O transition kernel
;;; only; these all describe session, CLI, goal, dispatch, export or
;;; endpoint slices whose kernel code does not exist here, so each is kept
;;; and counted rather than run. The pending convention is a repeated
;;; ;; NEEDS-KERNEL: line naming exactly what is missing.
;;; ------------------------------------------------------------------

(defvar *needs-kernel* '()
  "Replays written from SPEC-WORK.md whose kernel code does not exist yet.
Each is (name spec-line expected need). Counted, not run.")

(defmacro deftest-pending (name spec-line expected need)
  "Keep a replay NAME asserting EXPECTED (from SPEC-LINE), blocked on NEED.
It is registered and counted, but never added to *tests*: the kernel it
asserts does not exist in slice 1."
  `(push (list ,name ,spec-line ,expected ,need) *needs-kernel*))

;; default-window-opens-two-days is implemented in ../acceptance.lisp (card 8601).

;; disconnect-is-not-a-rollback: a client killed after its mutation was
;; journaled -- the event stands, the same request id and body returns the
;; recorded disposition, the same id with different arguments is refused, and
;; rev= and pushed= are distinct in every response.
;; NEEDS-KERNEL: a session reply layer carrying rev= and pushed= per response.
(deftest-pending "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
    "expected=event-stands;same-id-same-disposition;changed-args-refused;rev!=pushed"
  "kernel dedup disposes but the session reply packaging does not exist")

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

;; dry-run-writes-nothing: a dry run validates and projects without mutating;
;; after a green preview at revision R the --request id is still new to the
;; dedup index and events=/pending=/pushed= are unchanged; an accepted mutation
;; moves to R+1; apply --expect R is refused stale; apply at R+1 newly validates.
;; NEEDS-KERNEL: a --dry-run projection path and SESSION OK counters.
(deftest-pending "dry-run-writes-nothing" "docs/SPEC-WORK.md:5196"
    "expected=preview-mutates-nothing;dedup-still-new;events-pending-pushed-unchanged"
  "no dry-run projection exists in slice 1")

;; edit-is-atomic-and-replayable now lives in tests/acceptance.lisp over the
;; node-edit verb of src/node-verbs.lisp (nova-tools #362).
;; edit-is-atomic-and-replayable: a bad one-of-five patch writes nothing; an
;; accepted mixed edit moves only its named fields and the category index; the
;; same request id retried answers NODE OK; a changed payload refuses; an
;; equal-value edit is the no-effect receipt, changed=0, rev up by one.
;; NEEDS-KERNEL: an edit patch verb and category index.
(deftest-pending "edit-is-atomic-and-replayable" "docs/SPEC-WORK.md:5351"
    "expected=one-of-five-writes-nothing;changed-payload-refused;equal-value=changed-0"
  "no edit verb in slice 1")

;; efficiency-lessons-gate (SPEC-WORK.md:4916) now runs as the real deftest in
;; tests/replays-8644.lisp, with the dispatch/effort/ceiling gates it named.
;; edit-never-fetches-a-link: a live URL to a counting endpoint added, edited
;; and rendered with zero requests; a link holding NUL refused `bad link`; a
;; refusal on a private node prints no value.
;; NEEDS-KERNEL: a render path and link validation with a counting endpoint.
(deftest-pending "edit-never-fetches-a-link" "docs/SPEC-WORK.md:5357"
    "expected=fetches=0;nul=bad-link;private-node-prints-no-value"
  "no link render/fetch in slice 1")
;; efficiency-lessons-gate: prime projection respects --max-bytes; unpoured
;; checklist items never count in |O|; tripped nodes require --reason; delegate
;; mode refuses edits below the model; packets lacking :effort are refused;
;; dispatches crossing the configured daily fleet spend ceiling are refused.
;; NEEDS-KERNEL: model dispatch, delegate mode, :effort and spend-ceiling gates.
(deftest-pending "efficiency-lessons-gate" "docs/SPEC-WORK.md:4439"
    "expected=max-bytes-respected;unpoured!=O;tripped=reason;no-effort=refused;ceiling=refused"
  "dispatch/effort/ceiling enforcement is not in slice 1")
;; edit-never-fetches-a-link now lives in tests/acceptance.lisp over the
;; node-edit link validation and renderer of src/node-verbs.lisp (nova-tools #362).

;; edit-undo-preserves-later-work now lives in tests/acceptance.lisp over the
;; node-edit and node-undo verbs of src/node-verbs.lisp (nova-tools #362).

;; endpoint-is-local-and-private: the session's directory created 0700 and its
;; socket 0600, both owned by the running account; a pre-existing directory or
;; socket with wider modes refused rather than reused; the Windows named pipe
;; created with FILE_FLAG_FIRST_PIPE_INSTANCE; no listener bound to a network
;; address.
;; NEEDS-KERNEL: a session socket/directory bootstrap and permission check.
(deftest-pending "endpoint-is-local-and-private" "docs/SPEC-WORK.md:5276"
    "expected=dir-0700;socket-0600;wider-modes-refused;no-network-listener"
  "no session endpoint exists in slice 1")

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

;; fenced-export-can-finish: in a fenced session an export started, its status
;; and terminal line read by id; an unfinished one cancelled with publication
;; reconciled; an unknown id, a non-export id, operation list and every
;; canonical write refused `fenced`; no second owner and no mutation authority.
;; NEEDS-KERNEL: export/operation ids and a fenced (single-owner) session.
(deftest-pending "fenced-export-can-finish" "docs/SPEC-WORK.md:5451"
    "expected=terminal-read-by-id;cancel-reconciles;canonical-write=fenced;one-owner"
  "export and fencing are not in slice 1")

;; findings-across-c-and-o: the worked acceptance asserted line by line -- four
;; ids, four rows, open=1 closed=3 closed-in=3, no id twice, the dispositions
;; distinct, and the release task listed as pending by the second ask.
;; NEEDS-KERNEL: a `query --ask` findings listing across O and C.

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

;; full-round-trip: export a captured revision, load it in a fresh isolated
;; engine, export again, and compare every semantic field, stable ids, Unicode
;; and literal text, order where meaningful, links, evidence, roles, CONFIG,
;; ACTIVE observations, model and rate records, O and C history, roadmaps and
;; accounting provenance; derived caches rebuild to equivalent values.
;; NEEDS-KERNEL: an export/import path over a durable captured revision.

;;; ------------------------------------------------------------------
;;; SPEC-WORK.md lines 3600-end, part 4 of 8: session/CLI-level replays.
;;; Slice 1 ships only the internal C/O transition kernel (value, event,
;;; journal, state, kernel); every replay below names a feature whose kernel
;;; code (session, paging, indexes, roadmaps, leases, moves, providers,
;;; hostile-data intake) does not exist yet.  Each is kept in the file's
;;; deftest shape, asserted against the exact sentence its spec line states,
;;; and marked NEEDS-KERNEL rather than run against an absent implementation.
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
;; matrix-retirement   docs/SPEC-WORK.md:5387
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap axes (only selected coordinates retired and
;; recoverable; no task cancelled; layout change on populated roadmap refused).
;;(deftest "matrix-retirement" "docs/SPEC-WORK.md:5387"
;;    "selected=retired,recoverable=yes,task-cancelled=0"
;;  ;; `axis --remove` of a first-axis row and then of another axis's member:
;;  ;; only the selected coordinates retired and recoverable, no task cancelled.)

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
;; move-updates-every-roadmap-scope   docs/SPEC-WORK.md:5375
;; ------------------------------------------------------------------
;; NEEDS-KERNEL: roadmap scope revisions (a row referenced by two roadmaps
;; advances both referencing scopes, unrelated scope stays).
;;(deftest "move-updates-every-roadmap-scope" "docs/SPEC-WORK.md:5375"
;;    "referencing-scopes=advance,unrelated=stays"
;;  ;; a row referenced by two roadmaps outside both parent chains and one
;;  ;; unrelated roadmap: both referencing scope revisions advance, the
;;  ;; unrelated one stays.)

;;;; ------------------------------------------------------------------
;;;; Draft-25..27 replays promised by docs/SPEC-WORK.md and absent here.
;;;;
;;;; Every one of these names a verb, feature or wire property that is outside
;;;; the slice-1 C/O transition kernel (README.md "What is out"). With none of
;;;; the kernel work present, each replay asserts the only thing the sentence
;;;; currently makes true of this build -- that the feature refuses cleanly at
;;;; the boundary rather than silently inventing a partial answer -- and is
;;;; marked ;; NEEDS-KERNEL: for the work that flips it to assert the sentence
;;;; whole. They are kept, and counted, so the promised name is never lost.
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

;; NEEDS-KERNEL: archive export/reload and a recent-only export never labelled full.

;; NEEDS-KERNEL: execution stop as a hold plus an evidence-set custom cancel.
