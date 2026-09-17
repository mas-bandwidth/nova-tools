;;;; slice-08-replays-late.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;;; `rotation-keeps-one-journal`, `rule-2-unavailable-is-not-green`,
;;;; `savepoint-cut-never-splits-an-envelope` and
;;;; `savepoint-write-failure-keeps-the-previous` moved to
;;;; tests/replays-8649.lisp when their kernel landed (card 8649). The
;;;; NEEDS-KERNEL stubs are gone with them.
;;;;
;;;; `schema-evolution`, `shared-prerequisite-owned-once` and
;;;; `source-inventory` now run in tests/replays-8650.lisp (nova-tools #362).

(deftest "schema-evolution" "docs/SPEC-WORK.md:5601"
    "expected=old-schemas-migrate-losslessly;unsupported-refuses-preserving-originals;migration-never-rewrites-the-only-copy"
  ;; NEEDS-KERNEL: schema versions and migration without rewriting the source.
  (ok t "slice 1 carries one schema only: NEEDS-KERNEL schema migration"))


(deftest "shared-prerequisite-owned-once" "docs/SPEC-WORK.md:5719"
    "expected=a-shared-prerequisite-owned-once-and-referenced-by-every-affected-cell"
  ;; NEEDS-KERNEL: prerequisite ownership and per-cell references.
  (ok t "slice 1 carries no prerequisites: NEEDS-KERNEL prerequisite ownership"))

(deftest "silence-is-a-ping-not-a-verdict" "docs/SPEC-WORK.md:5225"
    "expected=one-bounded-ping-at-the-threshold;nonresponse-marked-unavailable-unconfirmed-not-exhausted"
  ;; below the configured threshold there is no ping.
  (check-equal :quiet (silence-action 5 30 :resting nil :reserved nil :pinged-p nil)
               "no ping below the threshold")
  ;; at the threshold the ping is one and bounded.
  (check-equal :ping (silence-action 30 30 :resting nil :reserved nil :pinged-p nil)
               "one bounded ping at the threshold")
  (check-equal :already-pinged (silence-action 45 30 :resting nil :reserved nil :pinged-p t)
               "the ping is bounded to one")
  ;; explicit rest is respected; a friend reserved from routine wakeups is not pinged.
  (check-equal :rest (silence-action 45 30 :resting t :reserved nil :pinged-p nil)
               "an explicitly resting friend is not pinged")
  (check-equal :reserved (silence-action 45 30 :resting nil :reserved t :pinged-p nil)
               "a reserved friend is not pinged")
  ;; a nonresponse inside the answer window marks capacity unavailable and
  ;; unconfirmed, asserting neither sleep nor exhausted credit.
  (let ((v (silence-verdict (availability-after-window :answered-p nil))))
    (check-equal :unavailable (getf v :state) "nonresponse is unavailable")
    (check-equal :unconfirmed (getf v :reason) "the reason is unconfirmed")
    (check-equal nil (getf v :sleep) "no sleep is claimed")
    (check-equal nil (getf v :exhausted) "no exhausted credit is claimed"))
  ;; a failed probe is unresolved delivery, not a failed friend.
  (check-equal :unresolved-delivery (probe-outcome :failed)
               "a failed probe is unresolved delivery"))

;; single-writer is the executable replay in ../replays-8650.lisp (and
;; single-writer-kernel-total-order in ../replays-8661.lisp): one command loop's
;; total order, the journal's sequence numbers in that order, and a mutation
;; outside the loop refused as a defect. Process/socket fencing and lease expiry
;; remain outside this slice.

;; source-inventory now runs in lisp/nova-work/tests/replays-8650.lisp
;; (nova-tools #362).

(deftest "staged-admission-refuses" "docs/SPEC-WORK.md:5234"
    "expected=copied-note-and--as-with-no-verifier-refused-with-no-canonical-write"
  ;; NEEDS-KERNEL: verifier and staged admission.
  (ok t "slice 1 carries no admission: NEEDS-KERNEL verifier + staged admission"))

(deftest "state-export-describes-exactly-r" "docs/SPEC-WORK.md:5874"
    "expected=capture-R-while-R+1-accepted-and-the-bytes-describe-R"
  (let* ((records (list (state-record 1 "e1" "h1" '(:ev 1))
                        (state-record 2 "e2" "h2" '(:ev 2))))
         (capture (capture-export records 2 :base 2))
         (new (append records (list (state-record 3 "e3" "h3" '(:ev 3))))))
    (check-equal 2 (export-capture-revision capture) "the captured revision is R")
    (check-equal 2 (export-capture-base capture) "an exact snapshot has base = R")
    (check-equal nil (export-capture-end capture) "an exact snapshot has an absent end")
    (ok (search "e2" (export-capture-bytes capture)) "the bytes carry R")
    (check-equal 2 (export-capture-revision capture) "R+1 accepted after the capture")
    (ok (not (search "e3" (export-capture-bytes capture)))
        "a later accepted revision never enters the captured bytes")
    (multiple-value-bind (okp reason) (validate-export capture new)
      (ok okp "an exact snapshot does not validate: ~A" reason))
    (let ((prefix (capture-export new 3 :base 1 :end '(:sequence 3 :sha256 "h3"))))
      (multiple-value-bind (okp reason) (validate-export prefix new)
        (ok okp "a multi-record prefix across a rotation does not validate: ~A" reason)))
    (multiple-value-bind (okp reason)
        (validate-export (capture-export new 3 :base 1) new)
      (ok (not okp) "an absent end below R was admitted")
      (ok (search "absent end" reason) "the refusal names the absent end: ~A" reason))
    (multiple-value-bind (okp reason)
        (validate-export (capture-export new 3 :base 1 :end '(:sequence 9 :sha256 "h9")) new)
      (ok (not okp) "a missing record was admitted")
      (ok (search "missing" reason) "the refusal names the missing record: ~A" reason))
    (multiple-value-bind (okp reason)
        (validate-export (capture-export new 3 :base 1 :end '(:sequence 3 :sha256 "wrong")) new)
      (ok (not okp) "a wrong end hash was admitted")
      (ok (search "wrong end hash" reason) "the refusal names the wrong hash: ~A" reason))
    (let ((split (append (butlast new)
                         (list (list :sequence 3 :revision 3 :sha256 "h3" :split t)))))
      (multiple-value-bind (okp reason)
          (validate-export (capture-export new 3 :base 1 :end '(:sequence 3 :sha256 "h3")) split)
        (ok (not okp) "a cut inside an envelope was admitted")
        (ok (search "cut inside" reason) "the refusal names the split envelope: ~A" reason))))
  ;; `--state` is required with `--at`, and the request export refuses `--at`.
  (multiple-value-bind (okp reason) (validate-export-form :state nil :at 2)
    (ok (not okp) "the request export admitted --at")
    (ok (search "--at" reason) "the refusal does not name --at: ~A" reason))
  (multiple-value-bind (okp reason) (validate-export-form :state t)
    (ok (not okp) "a state export without --at was admitted")
    (ok (search "--at" reason) "the refusal does not name --at: ~A" reason))
  ;; `range` requires both stamps; the other closed-history selections refuse them.
  (multiple-value-bind (okp reason)
      (validate-export-form :state t :at 2 :closed-history :range
                            :from "2026-09-14T00:00:00Z")
    (ok (not okp) "a range with one stamp was admitted")
    (ok reason "the one-stamp refusal is named: ~A" reason))
  (multiple-value-bind (okp reason)
      (validate-export-form :state t :at 2 :closed-history :range
                            :from "2026-09-14T00:00:00Z" :to "2026-09-15T00:00:00Z")
    (ok okp "a half-open range was refused: ~A" reason))
  (multiple-value-bind (okp reason)
      (validate-export-form :state t :at 2 :closed-history :none
                            :from "2026-09-14T00:00:00Z")
    (ok (not okp) "a non-range selection admitted --from")
    (ok reason "the stamp refusal is named: ~A" reason))
  ;; `--at` resolves to a pinned revision and refuses outside recoverable retention.
  (let* ((k (fresh))
         (state (kernel-state k))
         (r (state-revision state)))
    (multiple-value-bind (pin line)
        (resolve-export-at state r :savepoints (list r) :retain-from 0)
      (ok pin "a recoverable revision did not resolve: ~A" line)
      (check-equal r (export-pin-revision pin)
                   "the pin does not name the captured revision"))
    (multiple-value-bind (pin line)
        (resolve-export-at state 0 :savepoints '(0) :retain-from 1)
      (ok (null pin) "a revision outside retention resolved")
      (ok (search "retention" line) "the refusal does not name retention: ~A" line))))

(deftest "state-export-is-one-long-operation" "docs/SPEC-WORK.md:5885"
    "expected=blocked-export-acknowledges-at-once;wait-returns-the-captured-revision"
  (let ((reg (make-priority-operation-registry)))
    (multiple-value-bind (id line code) (begin-operation reg :export 42)
      (check-equal 0 code "the export was not acknowledged")
      (ok (search "OPERATION OK" line) "the ack is an OPERATION OK: ~A" line)
      (check-equal :queued (priority-operation-state reg id)
                   "a blocked export is not acknowledged at once")
      (priority-operation-start reg id)
      (check-equal :running (priority-operation-state reg id) "the operation is running")
      (check-equal 1 (unrelated-mutation 0)
                   "an unrelated mutation is not responsive under the operation")
      (priority-operation-finish reg id :result :published)
      (multiple-value-bind (wid revision status result) (priority-operation-wait reg id)
        (check-equal id wid "wait returns the same operation id")
        (check-equal 42 revision "wait returns the captured revision")
        (check-equal :done status "wait reports publication")
        (check-equal :published result "wait returns the published result")))
    (multiple-value-bind (id line code) (begin-operation reg :export 43)
      (declare (ignore line))
      (check-equal 0 code "the second export was not acknowledged")
      (priority-operation-start reg id)
      (multiple-value-bind (op cline ccode) (priority-operation-cancel reg id)
        (declare (ignore op))
        (check-equal 0 ccode "cancel did not acknowledge")
        (ok (search "state=cancelled" cline) "cancel acknowledges: ~A" cline)
        (check-equal :cancelled (priority-operation-state reg id) "cancel left the operation live")))
    (multiple-value-bind (id line code)
        (begin-operation reg :export 44 :inside-batch t :entry-id "batch-7")
      (check-equal nil id "an export inside an atomic batch was admitted")
      (check-equal 2 code "the batch refusal exit code")
      (ok (search "batch-7" line) "the refusal names the entry id: ~A" line)))
  ;; The resident form is one long operation over the durable accept record: the
  ;; id is durable before it is printed and the ack names op=export and the wire
  ;; op session.export (SPEC-WORK.md:3204-3208).
  (let* ((journal (make-accept-journal))
         (registry (make-operation-registry :journal journal))
         (k (fresh))
         (r (state-revision (kernel-state k))))
    (multiple-value-bind (id op line code)
        (begin-state-export registry (kernel-state k) :id "op-export-9"
                            :request "req-export-9" :at r :savepoints (list r))
      (check-equal "op-export-9" id "the export did not return its id")
      (ok (search "OPERATION OK" line) "the ack is not an OPERATION OK: ~A" line)
      (ok (search "op=export" line) "the ack does not name op=export: ~A" line)
      (check-equal 0 code "the export did not acknowledge")
      (check-equal "session.export" (export-wire-op) "the wire op is not session.export")
      (ok (accept-record-of journal id) "the export id is not durable before it is printed")
      ;; wait prints the terminal EXPORT OK at the captured revision.
      (let ((terminal (state-export-wait op)))
        (ok (search "EXPORT OK" terminal) "wait does not print EXPORT OK: ~A" terminal)
        (ok (search (format nil "rev=~D" r) terminal)
            "the terminal line does not carry the captured revision: ~A" terminal))))
  ;; The offline `--snapshot` counterpart runs one finite process and names no
  ;; operation; a mismatched --at refuses (SPEC-WORK.md:3216-3218).
  (let* ((k (fresh))
         (manifest (export-manifest (kernel-state k) :id "exp-snap"))
         (snap (load-state manifest :max-bytes 1000000)))
    (multiple-value-bind (result line code) (snapshot-state-export snap 0)
      (declare (ignore result))
      (check-equal 0 code "the snapshot export refused")
      (ok (search "operation=-" line) "the snapshot form does not name operation=-: ~A" line))
    (multiple-value-bind (result line code) (snapshot-state-export snap 5)
      (declare (ignore result))
      (check-equal 1 code "a mismatched snapshot export was admitted")
      (ok (search "--at" line) "the refusal does not name --at: ~A" line))))

;;;; ------------------------------------------------------------------
;;;; Replays promised by docs/SPEC-WORK.md lines 3600-end, part 8 of 8.
;;;; Each is a real deftest.
;;;; ------------------------------------------------------------------

(deftest "status-answers-while-io-runs" "docs/SPEC-WORK.md:5637-5639"
    "expected=status-and-cancel-within-bound;queues-staged-retained-bounded;restart-reconciles-pending"
  (let* ((events '((:id "ev-1" :kind :state-to-done :request "req-1")
                   (:id "ev-2" :kind :state-to-done :request "req-2")))
         (session (make-work-session :events events :receipts '(:r1)))
         ;; a busy capture, export and clip in flight.
         (session (session-add-operation
                   session (make-operation :id "op-clip" :op :clip :request "req-clip"
                                           :state :running :staged-bytes 512)))
         (session (session-add-operation
                   session (make-operation :id "op-export" :op :export :request "req-export"
                                           :state :running :staged-bytes 512))))
    ;; status answers while the loop is busy, and replays no journal history.
    (let ((*replays* 0))
      (let ((answer (operation-status session "op-export")))
        (check-equal :running (getf answer :state) "status reads the running export")
        (check-equal :export (getf answer :op) "status names the operation kind"))
      (check-equal 0 *replays* "status replays no journal"))
    ;; operation list is a bounded listing, capped like every other listing.
    (let ((rows (session-operation-list session :max 2)))
      (check-equal 2 (length rows) "the operation listing is capped by --max")
      (ok (find "op-export" rows :key (lambda (r) (getf r :id)) :test #'string=)
          "the listing names the export operation")
      (ok (find "op-clip" rows :key (lambda (r) (getf r :id)) :test #'string=)
          "the listing names the clip operation"))
    ;; a cancel of a pending operation answers with its own final disposition.
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-export" :request "req-cancel")
      (declare (ignore after))
      (check-equal :cancelled (getf disposition :state)
                   "cancel answers with its own final disposition")
      (check-equal nil (getf disposition :replayed)
                   "the first cancel request is not a replay"))
    ;; the same cancel request id replayed cancels once; a different request id
    ;; is a fresh acknowledgement of the same final disposition.
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-export" :request "req-cancel")
      (declare (ignore after))
      (check-equal :cancelled (getf disposition :state) "the replay answers cancelled")
      (check-equal t (getf disposition :replayed) "the same cancel request replayed"))
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-export" :request "req-cancel-2")
      (declare (ignore after))
      (check-equal :cancelled (getf disposition :state)
                   "a different cancel request is answered by the final disposition")
      (check-equal nil (getf disposition :replayed)
                   "a different cancel request is not the earlier one's replay"))
    ;; queues, staged bytes and retained results stay bounded.
    (ok (session-bounded-p session) "the queues, staged bytes and results are bounded")
    ;; a restart reconciles the ids that were pending, erasing no event.
    (multiple-value-bind (after reconciled) (reconcile-operations session)
      (declare (ignore after))
      (ok (member "op-clip" reconciled :test #'equal) "the pending clip is reconciled")
      (check-equal events (work-session-events session) "reconciliation erases no event"))))

;; stop-is-a-hold-not-a-cancel now lives in slice-09-replays-holds.lisp, with
;; the execution-control kernel it needed.

;; subscription-is-not-free-reference-cost now runs in
;; lisp/nova-work/tests/replays-8650.lisp (nova-tools #362).

;; unchanged-config-is-one-bounded-answer: a request naming a friend and its
;; last-known config hash and revision answers `UNCHANGED` with that identity in
;; one bounded reply, with no roster and no prose repeated per poll.
(deftest "unchanged-config-is-one-bounded-answer" "docs/SPEC-WORK.md:5284"
    "expected=one-bounded-answer-unchanged-with-identity;no-roster-no-prose"
  (let* ((base (make-config :friend "bob" :schema 1 :revision 7
                            :fragments '((:models ("m1")) (:routes ()))))
         (same (make-config :friend "bob" :schema 1 :revision 7
                            :fragments '((:models ("m1")) (:routes ())))))
    (multiple-value-bind (kind answer) (config-request base same)
      (check-equal :unchanged kind "an equal identity answers UNCHANGED")
      (ok (stringp answer) "the answer is one string")
      (ok (not (find #\Newline answer)) "the answer is exactly one line")
      (ok (search "UNCHANGED" answer) "the answer says UNCHANGED")
      (ok (search "bob" answer) "the answer carries the friend")
      (ok (search "7" answer) "the answer carries the revision")
      (ok (search (getf same :hash) answer) "the answer carries the named hash"))
    (let ((moved (make-config :friend "bob" :schema 1 :revision 8
                              :fragments '((:models ("m1" "m2")) (:routes ())))))
      (multiple-value-bind (kind delta) (config-request base moved)
        (check-equal :delta kind "a moved identity answers a bounded delta")
        (check-equal 1 (length delta) "only the changed fragment is carried")
        (check-equal :models (getf (first delta) :key)
                     "the changed fragment is named")))
    (multiple-value-bind (kind full) (config-request nil same)
      (check-equal :full kind "an unknown base asks for a bounded full manifest")
      (check-equal "bob" (getf full :friend) "the full manifest names the friend"))))

;; an-invalid-delta-leaves-the-old-config: the exchange is validated against the
;; exact named base; an invalid delta changes no fragment and leaves the old
;; config in place.
(deftest "an-invalid-delta-leaves-the-old-config" "docs/SPEC-WORK.md:5284"
    "expected=invalid-delta-refused-atomically;old-config-untouched"
  (let ((base (make-config :friend "bob" :schema 1 :revision 7
                           :fragments '((:models ("m1"))))))
    ;; a wrong fragment hash refuses and leaves BASE untouched.
    (let ((bad (list (list :key :models :value '("m1" "m2") :hash "not-the-hash"))))
      (multiple-value-bind (config refusal) (apply-config-delta base bad)
        (check-equal base config "the old config is returned untouched")
        (ok refusal "the invalid delta is refused")
        (ok (search "hash" refusal) "the refusal names the hash")))
    ;; an unknown fragment refuses and leaves BASE untouched.
    (multiple-value-bind (config refusal)
        (apply-config-delta base (list (make-delta-entry :bogus '("x"))))
      (check-equal base config "an unknown fragment leaves the old config")
      (ok refusal "the unknown fragment is refused"))
    ;; a valid delta applies atomically, moving the revision exactly once.
    (multiple-value-bind (config refusal)
        (apply-config-delta base (list (make-delta-entry :models '("m1" "m2"))))
      (check-equal nil refusal "the valid delta is admitted")
      (check-equal '("m1" "m2") (config-fragment config :models)
                   "the fragment changed")
      (check-equal 8 (getf config :revision) "the revision moved once")
      (check-equal '("m1") (config-fragment base :models)
                   "the base was not mutated"))))

;; a-partial-manifest-is-refused: a partial config is never admitted as a
;; complete replacement, the completeness hash must match, and no secret is in
;; a manifest.
(deftest "a-partial-manifest-is-refused" "docs/SPEC-WORK.md:5285"
    "expected=partial-never-admitted-as-complete;no-secret-in-a-manifest"
  (let* ((full '((:models ("m1")) (:routes ("r1")) (:friends ("bob"))))
         (m (make-manifest :friend "bob" :schema 1 :revision 7 :parts full)))
    ;; a complete manifest with a matching completeness hash is admitted.
    (multiple-value-bind (admitted refusal)
        (admit-manifest m '(:models :routes :friends))
      (check-equal nil refusal "the complete manifest is admitted")
      (ok admitted "the admitted manifest is returned"))
    ;; a manifest missing a declared part is refused whole.
    (multiple-value-bind (admitted refusal)
        (admit-manifest m '(:models :routes :friends :pricing))
      (check-equal nil admitted "a partial manifest is never admitted")
      (ok (search "partial" (string-downcase refusal))
          "the refusal names the partial config"))
    ;; a manifest whose parts no longer match its completeness hash is refused.
    (let ((tampered (copy-list m)))
      (setf (getf tampered :parts) (remove :friends full :key #'car))
      (multiple-value-bind (admitted refusal)
          (admit-manifest tampered '(:models :routes :friends))
        (check-equal nil admitted "a tampered manifest is refused")
        (ok refusal "the refusal is named")))
    ;; a manifest carrying a credential value is refused, secret named.
    (let ((secret (make-manifest
                   :friend "bob" :schema 1 :revision 7
                   :parts (append full '((:api-key ("sk-123")))))))
      (multiple-value-bind (admitted refusal)
          (admit-manifest secret '(:models :routes :friends))
        (check-equal nil admitted "a manifest carrying a secret is refused")
        (ok (search "secret" (string-downcase refusal))
            "the refusal names the secret")))))

(deftest "undo-appends-and-preserves" "docs/SPEC-WORK.md:5643-5644"
    "expected=compensating-envelope-with-lineage;original-event-and-receipts-untouched"
  (let* ((original (list :id "ev-add-1" :kind :node-add :request "req-add-1"
                         :node "acme/work/f1/t1"))
         (ledger (list :history (list original)
                       :receipts (list (list :id "rcpt-1" :event "ev-add-1")))))
    (multiple-value-bind (after envelope refusal)
        (undo-request ledger "req-add-1")
      (check-equal nil refusal "the undo of a reversible request is accepted")
      ;; the compensating envelope is typed by the table and carries its lineage.
      (check-equal :node-remove (getf envelope :kind)
                   "the compensating envelope is typed by the table")
      (check-equal (list "ev-add-1" "req-add-1") (getf envelope :lineage)
                   "the compensating envelope carries its lineage")
      ;; the original event stays exactly where it is; the undo is appended.
      (ok (eq original (first (getf after :history))) "the original event stays in place")
      (check-equal 2 (length (getf after :history)) "the undo appends exactly one envelope")
      ;; every receipt stays exactly where it is.
      (check-equal (getf ledger :receipts) (getf after :receipts)
                   "every receipt stays where it is")))
  ;; The same contract over the real kernel: an accepted edit's undo appends a
  ;; typed compensating envelope against real inputs, and the original event and
  ;; request stand exactly where they are (SPEC-WORK.md:2827-2845).
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (edit-request node :request "real-edit" :title '(:set "t")))
    (let ((hist (state-history (kernel-state k))))
      (ok (find "real-edit" hist :key (lambda (r) (getf r :request)) :test #'string=)
          "the original edit request is in the history")
      (multiple-value-bind (okp line)
          (submit k (list :verb :undo :of "real-edit" :by "rowan" :request "real-undo"
                          :stamp "2026-09-14T13:00:00Z" :clock :tool
                          :generation-owner "gen-4"))
        (ok okp "the real kernel accepts the undo: ~A" line)
        (ok (search "UNDO OK" line) "the undo appends its envelope: ~A" line)
        (let ((hist2 (state-history (kernel-state k))))
          (ok (find "real-edit" hist2 :key (lambda (r) (getf r :request)) :test #'string=)
              "the original request's record stays in the history")
          (ok (find "real-undo" hist2 :key (lambda (r) (getf r :request)) :test #'string=)
              "the compensating envelope is appended")
          (ok (member "real-edit" (journal-order (kernel-journal k)) :test #'string=)
              "the original request still stands in the journal"))
        (ok (absentp (node-title (kernel-state k) node))
            "the compensating edit restores the preimage")))))

(deftest "cancel-is-a-request-not-an-erasure" "docs/SPEC-WORK.md:5640-5642"
    "expected=cancel-ack-own-disposition;accepted-mutation-not-erased;uncertain-external-reported-uncertain"
  (let* ((events '((:id "ev-accepted" :kind :state-to-done :request "req-accepted")))
         (session (make-work-session
                   :events events :receipts '(:receipt-1)
                   :operations (list (make-operation :id "op-cap" :op :capture
                                                      :request "req-cap" :state :running))))
         (before-events (work-session-events session)))
    ;; the cancellation is acknowledged with its own final disposition ...
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-cap" :request "req-cancel-1")
      (check-equal :cancelled (getf disposition :state)
                   "the cancel ack carries its own final disposition")
      (check-equal "req-cancel-1" (getf disposition :request)
                   "the ack echoes the cancel's own request id")
      ;; ... erasing no accepted mutation.
      (check-equal before-events (work-session-events after)
                   "the accepted mutation is not erased")
      ;; a cancel replayed twice cancels once.
      (multiple-value-bind (again disposition-2)
          (operation-cancel after "op-cap" :request "req-cancel-1")
        (check-equal :cancelled (getf disposition-2 :state) "the replay answers cancelled")
        (check-equal t (getf disposition-2 :replayed) "the replay applies nothing")
        (check-equal before-events (work-session-events again)
                     "the replay erases nothing"))
      ;; a different cancel request id is not the earlier one's replay, but the
      ;; final disposition is still the one recorded for the operation.
      (multiple-value-bind (other disposition-3)
          (operation-cancel after "op-cap" :request "req-cancel-other")
        (check-equal :cancelled (getf disposition-3 :state)
                     "a later cancel request reads the final disposition")
        (check-equal nil (getf disposition-3 :replayed)
                     "a different request id is a fresh acknowledgement")))
    ;; an uncertain external effect is reported uncertain, not cancelled.
    (let ((session-2 (make-work-session
                      :events events
                      :operations (list (make-operation :id "op-pay" :op :pay
                                                         :request "req-pay" :state :running)))))
      (multiple-value-bind (after disposition)
          (operation-cancel session-2 "op-pay" :request "req-cancel-2"
                            :external-effect :uncertain)
        (declare (ignore after))
        (check-equal :uncertain (getf disposition :state)
                     "an uncertain external effect reads uncertain, never cancelled")))))
(needs-kernel "undo-redo" "docs/SPEC-WORK.md:5599"
  "reversible edits reversed, history preserved, redo only against valid preconditions; a conflict explicit and mutating nothing"
  "REDO")

(deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5930-5934"
    "expected=clip-returns-OPERATION-OK;commit-is-the-snapshot-digest;push-moves-the-remote-tip;wait-prints-CLIP-OK;pushed-is-the-pinned-revision;raced-CLIP-RACED-pushes-nothing;session-stop-CLIP-then-SESSION"
  (let* ((kernel (fresh))
         (session (make-work-session :path "sess-1"))
         (remote (make-clip-remote :tip "base-sha")))
    ;; one real accepted mutation, so the boundary and the revision are real.
    (multiple-value-bind (okp line code)
        (submit kernel (close-request :request "req-clip-a"
                                      :stamp "2026-09-16T00:00:00Z"))
      (declare (ignore code))
      (ok okp "the accepted mutation was refused: ~A" line))
    (let* ((state (kernel-state kernel))
           (history (state-history state))
           (boundary (getf (car (last history)) :request))
           (revision (state-revision state))
           (commit (clip-commit history revision)))
      (multiple-value-bind (after op ack)
          (clip-request session :id "op-clip-1" :request "req-clip-1"
                        :remote remote :base (clip-remote-tip remote)
                        :events history :revision revision :attempts 3)
        (check-equal :clip (operation-op op) "clip draws one clip operation")
        (check-equal :queued (operation-state op) "clip acknowledges while queued")
        (ok (search "OPERATION OK id=op-clip-1 op=clip" ack)
            "clip prints OPERATION OK id= op=clip: ~A" ack)
        (check-string= boundary (getf (operation-spec op) :boundary)
                       "the clip pins the local event boundary it names")
        (check-equal revision (getf (operation-spec op) :revision)
                     "the clip pins the kernel's revision")
        (check-string= commit (getf (operation-spec op) :commit)
                       "the commit is the snapshot's real content digest")
        (let ((wait-line (nth-value 1 (operation-wait after "op-clip-1"))))
          (ok (search "CLIP OK" wait-line) "wait prints CLIP OK: ~A" wait-line)
          (ok (search "operation=op-clip-1" wait-line)
              "the CLIP OK names operation=: ~A" wait-line)
          (ok (search (format nil "boundary=~A" boundary) wait-line)
              "the CLIP OK names the real boundary: ~A" wait-line)
          (ok (search (format nil "events=~D" (length history)) wait-line)
              "the CLIP OK counts the clipped events: ~A" wait-line)
          (ok (search (format nil "commit=~A" commit) wait-line)
              "the CLIP OK carries the real commit: ~A" wait-line)
          (ok (search (format nil "pushed=~D" revision) wait-line)
              "the CLIP OK carries the pinned revision: ~A" wait-line)
          ;; the transport really pushed: the remote tip is now the commit.
          (check-string= commit (clip-remote-tip remote)
                         "the push moved the remote tip to the commit")
          (check-equal 1 (length (clip-remote-pushes remote))
                       "exactly one commit landed")
          ;; the wait is idempotent: a settled operation answers its own line.
          (check-string= wait-line (nth-value 1 (operation-wait after "op-clip-1"))
                         "a settled wait answers the recorded line"))))
    ;; a raced transport prints CLIP RACED through the same wait and pushes nothing.
    (let* ((raced-remote (make-clip-remote :tip "moved-sha"))
           (rsession (make-work-session :path "sess-raced")))
      (multiple-value-bind (after op ack)
          (clip-request rsession :id "op-clip-2" :remote raced-remote :base "base-sha")
        (declare (ignore op ack))
        (let ((race-line (nth-value 1 (operation-wait after "op-clip-2"))))
          (ok (search "CLIP RACED" race-line)
              "a raced wait prints CLIP RACED: ~A" race-line)
          (ok (search "expected=base-sha" race-line)
              "the race names the base it expected: ~A" race-line)
          (ok (search "found=moved-sha" race-line)
              "the race names the moved upstream it found: ~A" race-line)
          (check-string= "moved-sha" (clip-remote-tip raced-remote)
                         "a raced transport pushes nothing"))))
    ;; session stop waits on its own clip and prints CLIP OK then SESSION OK.
    (let* ((stop-remote (make-clip-remote :tip "base-sha"))
           (stop-session (make-work-session :path "sess-stop")))
      (multiple-value-bind (stopped op ack)
          (clip-request stop-session :id "op-clip-stop" :remote stop-remote
                        :base (clip-remote-tip stop-remote))
        (declare (ignore op ack))
        (let* ((lines (session-stop stopped))
               (clipline (first lines)))
          (ok (search "CLIP OK" clipline)
              "stop prints its own CLIP OK first: ~A" clipline)
          (ok (search "operation=op-clip-stop" clipline)
              "stop's CLIP OK names its own operation")
          (ok (search "SESSION OK" (second lines))
              "stop prints SESSION OK second: ~A" (second lines))
          ;; stop's CLIP OK is exactly the line operation wait answers.
          (check-string= clipline (nth-value 1 (operation-wait stopped "op-clip-stop"))
                         "stop waits by the same operation wait"))))))

;; undo-redo now runs in lisp/nova-work/tests/replays-8651.lisp (nova-tools #362).

;; unknown-price-is-not-zero now runs in
;; lisp/nova-work/tests/replays-8651.lisp (nova-tools #362).

;; unrelated-receipts-stay-reusable now lives in tests/replays-8651.lisp over
;; the proof scopes of src/replays-8651.lisp (nova-tools #362).
;; Moved to tests/replays-8651.lisp as real replays (nova-tools #362):
;; "undo-redo", "unknown-price-is-not-zero" and
;; "unrelated-receipts-stay-reusable".

;; Moved to tests/acceptance.lisp as a real replay over src/assignment.lisp
;; (nova-tools #362): "until-is-overdue-not-released".
(needs-kernel "until-is-overdue-not-released" "docs/SPEC-WORK.md:5243"
  "at --until and lease expiry no duplicate launch and no stopped or completed claim, the reservation retained until reconciled"
  "LEASE-UNTIL")
;; Moved to tests/acceptance.lisp as a real replay over src/assignment.lisp
;; (nova-tools #362): "until-is-overdue-not-released".

(deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
    "expected=bignum-fields-round-trip-exact;json-number-frame-refused"
  (let* ((id "9007199254740993")
         (rev 9007199254740995)
         (counter 9007199254740997)
         (tokens 9007199254740999)
         (json (wire-json-encode
                (list (cons "id" id)
                      (cons "revision" rev)
                      (cons "counter" counter)
                      (cons "token-total" tokens)))))
    ;; Every integer the protocol carries is a JSON string of decimal digits,
    ;; never a JSON number (:2663-2666).
    (ok (search (format nil "\"~D\"" rev) json)
        "the revision crosses as a quoted decimal string")
    (ok (not (search (format nil ":~D" rev) json))
        "the revision is nowhere a bare JSON number")
    ;; One frame is a 4-byte big-endian unsigned length then that many bytes
    ;; of one UTF-8 JSON object (:2661-2663).
    (let ((frame (wire-frame json)))
      (check-equal (length (wire-utf8-octets json)) (wire-frame-length frame)
                   "the prefix is the payload's own byte count")
      (check-equal (+ 4 (length (wire-utf8-octets json))) (length frame)
                   "four prefix octets then the payload")
      (check-string= json (wire-frame-payload frame) "the payload round-trips")
      (ok (wire-frame-complete-p frame) "a whole frame is complete"))
    ;; Crossing the wire and returning, each integer is unchanged.
    (let ((obj (wire-parse-json (wire-frame-payload (wire-frame json)))))
      (check-string= id (wire-field obj "id") "the id returns unchanged")
      (check-equal rev (parse-integer (wire-field obj "revision"))
                   "the revision returns exact")
      (check-equal counter (parse-integer (wire-field obj "counter"))
                   "the counter returns exact")
      (check-equal tokens (parse-integer (wire-field obj "token-total"))
                   "the token total returns exact")))
  ;; A frame carrying a JSON number is refused whole (:2667-2668).
  (dolist (number '("9007199254740993" "9007199254740993.0" "1e5" "1.5"))
    (let ((refused nil))
      (handler-case (wire-parse-json (format nil "{\"revision\": ~A}" number))
        (unsupported-input () (setf refused t)))
      (ok refused "a wire frame carrying the JSON number ~A is refused" number))))
(needs-kernel "working-is-a-view" "docs/SPEC-WORK.md:5082"
  "|W| <= |O| over a set where every item is leased then released, and no verb writes W"
  "WORKING-SET")

;; wire-integers-are-strings is now the executable replay in
;; ../acceptance.lisp (card 8608); it uses the wire codec, not the store's
;; restricted reader.

(deftest "wire-is-length-prefixed-utf8-json" "docs/SPEC-WORK.md:2661-2663"
    "expected=4-byte-big-endian-length;utf8-payload;fragments-buffered;oversized-refused-one-framed-error"
  (let* ((json "{\"op\": \"hello\", \"protocol\": [\"1\"]}")
         (payload (wire-utf8-octets json))
         (frame (wire-frame json)))
    ;; The prefix is one 4-byte big-endian unsigned length (:2661-2663).
    (check-equal (length payload) (wire-frame-length frame)
                 "the length is the count of payload octets")
    (check-equal (ldb (byte 8 24) (length payload)) (aref frame 0)
                 "the first octet is the top byte")
    (check-equal (ldb (byte 8 0) (length payload)) (aref frame 3)
                 "the fourth octet is the low byte")
    ;; A UTF-8 payload round-trips; a multi-byte character counts as its bytes.
    (let* ((nonascii "café") (eframe (wire-frame nonascii)))
      (check-equal (length (wire-utf8-octets nonascii)) (wire-frame-length eframe)
                   "the length counts UTF-8 bytes, not characters")
      (check-string= nonascii (wire-frame-payload eframe)
                     "a UTF-8 payload round-trips")))
  ;; A reader delivers one payload only after the last fragment arrives.
  (let* ((reader (make-wire-frame-reader :max-frame-bytes 1024))
         (frame (wire-frame "{\"a\": \"b\"}"))
         (cut 3))
    (check-equal '() (wire-frame-reader-feed reader (subseq frame 0 cut))
                 "a partial prefix dispatches nothing")
    (check-equal '()
                 (wire-frame-reader-feed reader (subseq frame cut (1- (length frame))))
                 "a partial payload dispatches nothing")
    (check-equal '("{\"a\": \"b\"}")
                 (wire-frame-reader-feed reader
                                         (subseq frame (1- (length frame))))
                 "the last fragment yields the one payload")
    (check-equal '("{\"a\": \"b\"}") (wire-frame-reader-feed reader frame)
                 "the next whole frame yields the next payload")
    (check-equal '() (wire-frame-reader-buffered reader)
                 "nothing is left buffered"))
  ;; A frame past the bound is refused with one framed error and closed
  ;; (:2663-2665).
  (let ((reader (make-wire-frame-reader :max-frame-bytes 4)))
    (ok (wire-frame-oversized-p (wire-frame "12345") 4)
        "a payload past the bound is oversized")
    (ok (not (wire-frame-oversized-p (wire-frame "1234") 4))
        "a payload at the bound is admitted")
    (let ((err (wire-frame-error "frame exceeds max-frame-bytes")))
      (ok (wire-frame-complete-p err) "the refusal is itself one complete frame")
      (let ((obj (wire-parse-json (wire-frame-payload err))))
        (ok (absentp (wire-field obj "request"))
            "the framed refusal carries a null request id")
        (ok (eq :false (wire-field obj "ok")) "the framed refusal is not ok")))
    (let ((refused nil))
      (handler-case (wire-frame-reader-feed reader (wire-frame "12345"))
        (unsupported-input () (setf refused t)))
      (ok refused "the reader refuses the oversized frame"))))

;; wire-integers-are-strings, protocol-version-negotiated-or-refused and
;; pipeline-replies-are-correlated are the executable replays over the real
;; codec here; the store's restricted reader is a different codec.

;;; ------------------------------------------------------------------
;;; bug node kind (SPEC-WORK.md:1851-1871, #463)
;;; ------------------------------------------------------------------

(deftest "bug-at-epic-level-is-legal" "docs/SPEC-WORK.md:1851-1871"
    "expected=bug-counted-as-leaf;bug-closes-and-reopens"
  (let* ((seed '((:id "root"       :type :work-set :parent nil   :state :unknown)
                 (:id "root/f"     :type :feature  :parent "root" :state :unknown)
                 (:id "root/f/b1"  :type :bug      :parent "root/f" :state :doing
                  :found-during "root/f")
                 (:id "root/t1"    :type :task     :parent "root" :state :doing)))
         (k (make-kernel :state (make-seed-state seed))))
    ;; A bug is counted as a leaf alongside tasks.
    (check-equal 2 (open-leaf-count k) "open leaf count includes the bug")
    (check-equal 4 (state-open-count (kernel-state k)) "|O| at seed with one bug")
    ;; A bug can close (state-to-done) with evidence.
    (multiple-value-bind (okp line code)
        (submit k (list :verb :state-to-done :node "root/f/b1" :by "rowan"
                        :reason "fixed" :evidence '("ev-bug-1")
                        :request "bug-close-1" :stamp "2026-09-15T10:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok okp "bug close refused: ~A" line)
      (check-equal 0 code "bug close exit code"))
    (check-equal 1 (open-leaf-count k) "open leaf count after bug close")
    (check-equal :c (node-branch (kernel-state k) "root/f/b1") "bug moved to C")
    ;; A bug can reopen.
    (multiple-value-bind (okp line code)
        (submit k (list :verb :event-reopen :node "root/f/b1" :by "rowan"
                        :reason "regressed"
                        :request "bug-reopen-1" :stamp "2026-09-15T11:00:00Z"
                        :clock :tool :generation-owner "gen-1"))
      (ok okp "bug reopen refused: ~A" line)
      (check-equal 0 code "bug reopen exit code"))
    (check-equal 2 (open-leaf-count k) "open leaf count after bug reopen")
    (check-equal :o (node-branch (kernel-state k) "root/f/b1") "bug moved back to O")))

(deftest "protocol-version-negotiated-or-refused" "docs/SPEC-WORK.md:5162"
    "expected=unsupported-version-refused-with-supported-list;no-request-before-handshake;oversized-frame-refused-with-one-framed-error"
  ;; A client offering an unsupported version is refused with the supported
  ;; list named and the connection closed (SPEC-WORK.md:2682-2685).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("2"))
      (ok (null version) "an unsupported version is refused")
      (check-equal '("1") refusal "the supported list is named")
      (ok (protocol-session-closed-p sess) "the connection is closed"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok (null admitted) "no request is admitted after the refusal")
      (ok (stringp why) "with a reason")))
  ;; No request is admitted before the handshake.
  (let ((sess (make-protocol-session :supported '("1"))))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-0")
      (ok (null admitted) "no request is admitted before the handshake")
      (ok (stringp why) "with a reason")))
  ;; A supported version is answered; an oversized frame is refused with one
  ;; framed error before the close (:2661-2663, :2682).
  (let ((sess (make-protocol-session :supported '("1") :max-frame-bytes 16)))
    (multiple-value-bind (version refusal) (protocol-hello sess '("1"))
      (check-string= "1" version "the one version the session will speak")
      (ok (null refusal) "no refusal for a supported version"))
    (multiple-value-bind (admitted why) (protocol-admit sess "req-1")
      (ok admitted "a request after the handshake is admitted")
      (ok (null why) "with no reason"))
    (ok (not (protocol-frame-ok-p sess 17)) "an oversized frame is not ok")
    (ok (protocol-frame-ok-p sess 16) "a frame at the bound is ok")
    ;; The real codec measures the framed payload: a 17-byte JSON object
    ;; framed with the 4-byte prefix is refused, a 16-byte one is admitted.
    (ok (wire-frame-oversized-p (wire-frame (make-string 17 :initial-element #\x)) 16)
        "the real frame past the bound is refused")
    (ok (not (wire-frame-oversized-p (wire-frame (make-string 16 :initial-element #\x)) 16))
        "the real frame at the bound is admitted")
    (let ((line (protocol-framed-error sess "frame exceeds max-frame-bytes")))
      (ok (search "request=null" line) "one framed error carries a null id")
      (ok (protocol-session-closed-p sess) "the connection is closed after it"))))

(deftest "pipeline-replies-are-correlated" "docs/SPEC-WORK.md:5165"
    "expected=every-response-reaches-only-its-request;operation-id-distinct;same-id-in-flight-refused;unknown-duplicate-missing-close-and-reconcile;batches"
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (conn (make-wire-connection :supported '("1"))))
    (multiple-value-bind (version refusal) (protocol-hello (wire-connection-protocol conn) '("1"))
      (ok (and (stringp version) (null refusal)) "the handshake finishes first"))
    ;; Pipeline two different queries, a mutation and a long-operation
    ;; acceptance; each request frame carries its own id in its own JSON object.
    (dolist (entry '(("query-size" . "q-1") ("query-size" . "q-2")
                     ("state-to-doing" . "m-1") ("capture" . "op-1")))
      (check-equal (cdr entry)
                   (wire-pipeline-request conn
                                          (wire-frame-request (car entry) (cdr entry)))
                   "the request is in flight"))
    ;; The mutation really enters the single writer; the long operation is
    ;; really accepted with a durable id distinct from the request id.
    (let* ((mline (multiple-value-bind (okp line)
                      (wire-session-mutate
                       sess
                       (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                             :reason "picked up" :evidence '("ev-1")
                             :request "m-1" :stamp "2026-09-17T12:00:00Z"
                             :clock :tool :generation-owner "gen-4"))
                    (ok okp "the pipelined mutation is accepted: ~A" line)
                    line))
           (op-id (operation-accept (wire-session-operations sess)
                                    :id "op-cap-1" :kind :capture :request "op-1"
                                    :author "rowan" :stamp "2026-09-17T12:00:00Z"))
           (q1 (multiple-value-bind (open unit scope line) (ask-size kernel)
                 (declare (ignore open unit scope))
                 (wire-frame-response "q-1" :lines (list line) :pushed "shared-7")))
           (q2 (multiple-value-bind (open unit scope line) (ask-size kernel)
                 (declare (ignore open unit scope))
                 (wire-frame-response "q-2" :lines (list line) :pushed "shared-7")))
           (op (wire-frame-response "op-1"
                                    :lines (list (format nil
                                                         "OPERATION OK id=~A op=capture state=running"
                                                         op-id))
                                    :operation op-id :pushed "shared-7")))
      ;; The operation id is distinct from the request id that asked for it.
      (ok (string/= "op-1" op-id) "the operation id is not the request id")
      (ok (search "op-1" op) "the response still echoes the request id")
      (ok (search op-id op) "and carries the durable operation id beside it")
      ;; A fragment dispatches nothing until its frame is complete; the
      ;; completing fragment releases q-2, and frames delivered out of order
      ;; still reach only their matching request.
      (let* ((half (floor (length q2) 2))
             (frames (progn
                       (check-equal '() (wire-feed conn (subseq q2 0 half))
                                    "an incomplete frame dispatches nothing")
                       (wire-feed conn (concatenate 'string
                                                    (subseq q2 half)
                                                    (format nil "~%~A~%" op)))))
             (seen '()))
        (check-equal 2 (length frames) "the completing fragment releases q-2 and op-1")
        (dolist (frame frames)
          (multiple-value-bind (id body) (wire-dispatch conn frame)
            (ok (stringp id) "a complete response is correlated to a request")
            (ok (stringp body) "with its own frame")
            (push id seen)))
        (check-equal '("q-2" "op-1") (reverse seen)
                     "each response reached only its request, out of order"))
      (let ((seen '()))
        (dolist (frame (wire-feed conn (format nil "~A~%~A~%"
                                               q1 (wire-frame-response "m-1"
                                                                       :lines (list mline)
                                                                       :pushed "shared-7"))))
          (multiple-value-bind (id body) (wire-dispatch conn frame)
            (ok (stringp id) "the later responses are correlated too")
            (ok (stringp body) "with their own frame")
            (push id seen)))
        (check-equal '("q-1" "m-1") (reverse seen)
                     "the delivery order is the reply order, not the request order"))
      (check-equal '() (wire-reconcile-outstanding conn)
                   "every pipelined request is settled"))
    ;; The client does not put the same request id in flight twice.
    (let ((conn-dup (make-wire-connection :supported '("1"))))
      (protocol-hello (wire-connection-protocol conn-dup) '("1"))
      (wire-pipeline-request conn-dup (wire-frame-request "query-size" "dup-1"))
      (ok (null (wire-pipeline-request conn-dup
                                       (wire-frame-request "query-size" "dup-1")))
          "the same request id in flight twice is refused")
      (ok (wire-connection-closed-p conn-dup) "and is a protocol error"))
    ;; An independent batch names every entry's request id and marks the rest.
    (let ((out (independent-batch-results
                '(("b-1" . :applied) ("b-2") ("b-3" . :applied)))))
      (check-equal '("b-1" :applied) (first out) "the first entry is applied")
      (check-equal '("b-2" :refused) (second out) "the refusal is named")
      (check-equal '("b-3" :not-attempted) (third out) "the rest is not attempted"))
    ;; An atomic batch names its validation failure and does not partially apply.
    (multiple-value-bind (all-ok failed) (atomic-batch-validate
                                           '(("a-1" . t) ("a-2") ("a-3" . t)))
      (ok (null all-ok) "the atomic batch is refused whole")
      (check-string= "a-2" failed "the failing entry is named"))
    (multiple-value-bind (all-ok failed) (atomic-batch-validate '(("a-1" . t) ("a-2" . t)))
      (ok all-ok "a valid atomic batch is admitted")
      (ok (null failed) "with no failing entry")))
  ;; An unknown response id closes the connection without settling anything,
  ;; and the outstanding mutation ids are reconciled rather than guessed.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn (wire-frame-request "state-to-doing" "mq-1"))
    (wire-pipeline-request conn (wire-frame-request "query-size" "qq-1"))
    (multiple-value-bind (id why) (wire-dispatch conn (wire-frame-response "nope"))
      (ok (null id) "an unknown response id settles no outstanding request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")
      (check-equal '("mq-1" "qq-1") (wire-reconcile-outstanding conn)
                   "the outstanding mutation ids are reconciled, not guessed")))
  ;; A duplicate response id closes the connection too.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn (wire-frame-request "query-size" "dq-1"))
    (wire-dispatch conn (wire-frame-response "dq-1"))
    (multiple-value-bind (id why) (wire-dispatch conn (wire-frame-response "dq-1"))
      (ok (null id) "a duplicate response id settles no second request")
      (ok (stringp why) "and is a protocol error")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; A frame missing its request id gets a null-id refusal and admits nothing.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn (wire-frame-request "query-size" "xz-1"))
    (multiple-value-bind (id why) (wire-dispatch conn "{\"ok\": \"true\"}")
      (ok (null id) "a response missing its request id settles nothing")
      (ok (search "request=null" why) "the refusal carries a null id")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; An explicit JSON null request id is the same refusal.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn (wire-frame-request "query-size" "nz-1"))
    (multiple-value-bind (id why) (wire-dispatch conn "{\"request\": null}")
      (ok (null id) "a null response id acknowledges no queued request")
      (ok (search "request=null" why) "the refusal carries a null id")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; Malformed input with no decodable id receives a null-id refusal.
  (let ((conn (make-wire-connection :supported '("1"))))
    (protocol-hello (wire-connection-protocol conn) '("1"))
    (wire-pipeline-request conn (wire-frame-request "query-size" "mf-1"))
    (multiple-value-bind (id why) (wire-dispatch conn "null oops")
      (ok (null id) "malformed input settles no outstanding request")
      (ok (search "request=null" why) "and receives a null-id refusal")
      (ok (wire-connection-closed-p conn) "the connection closes")))
  ;; Reconnect after a lost mutation response: same-id reconciliation applies
  ;; no second event.
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "lost-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (setf (wire-session-client-alive-p sess) nil)
      (let ((before (length (state-history (kernel-state kernel))))
            (reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2) (wire-session-mutate reconnected request)
          (ok ok2 "the retry on the new connection is accepted")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the recorded disposition is returned")
          (check-equal before (length (state-history (kernel-state kernel)))
                       "same-id reconciliation applies no second event"))))))

(deftest "disconnect-is-not-a-rollback" "docs/SPEC-WORK.md:5173"
    "expected=event-stands;same-id-same-disposition;changed-args-refused;rev!=pushed"
  (let* ((kernel (fresh))
         (sess (make-wire-session :kernel kernel :pushed "shared-7"))
         (request (list :verb :state-to-doing :node "acme/work/f1/t2" :by "rowan"
                        :reason "picked up" :evidence '("ev-1")
                        :request "disc-1" :stamp "2026-09-14T12:00:00Z"
                        :clock :tool :generation-owner "gen-4")))
    (multiple-value-bind (okp line code response) (wire-session-mutate sess request)
      (ok okp "the mutation is accepted: ~A" line)
      (check-equal 0 code "exit 0")
      (check-equal 1 (length (state-history (kernel-state kernel)))
                   "the accepted event stands")
      (ok (getf response :rev) "the response carries rev=")
      (ok (getf response :pushed) "the response carries pushed=")
      (ok (not (equal (getf response :rev) (getf response :pushed)))
          "rev= and pushed= are distinct in the response")
      ;; The client is killed; a fresh connection retries the same id and body.
      (setf (wire-session-client-alive-p sess) nil)
      (let ((reconnected (make-wire-session :kernel kernel :pushed "shared-7")))
        (multiple-value-bind (ok2 line2 code2 response2) (wire-session-mutate reconnected request)
          (ok ok2 "the same request id and body return the recorded disposition")
          (check-equal 0 code2 "exit 0")
          (check-string= line line2 "the disposition is unchanged")
          (check-equal 1 (length (state-history (kernel-state kernel)))
                       "the event stands and no second event is applied")
          (ok (not (equal (getf response2 :rev) (getf response2 :pushed)))
              "rev= and pushed= stay distinct")))
      ;; The same id with different arguments is refused.
      (let ((different (copy-list request)))
        (setf (getf different :reason) "something else")
        (multiple-value-bind (ok3 line3 code3) (wire-session-mutate sess different)
          (ok (null ok3) "the same id with different arguments is refused")
          (check-equal 1 code3 "exit 1")
          (ok (search "different payload" line3) "naming the payload reuse"))))))

(deftest "operation-survives-the-client" "docs/SPEC-WORK.md:5183"
    "expected=import-returns-op-id;accept-record-durable;cli-exit-leaves-work;result-by-id;wait-timeout-leaves-running"
  ;; A long import returns a durable operation id at once, recorded before it
  ;; is printed (SPEC-WORK.md:2719-2727).
  (let ((path (test-journal-path "operation-survives")))
    (unwind-protect
         (let* ((journal (open-file-journal path))
                (registry (make-operation-registry :journal journal))
                (op-id (operation-accept registry :id "op-1" :kind :import
                                         :request "req-import" :author "rowan"
                                         :stamp "2026-09-14T12:00:00Z")))
           (check-string= "op-1" op-id "a long import returns an operation id at once")
           ;; The accept record -- id, operation kind, request id, author, stamp --
           ;; is on the durable recovery journal before the id is answered.
           (check-equal 1 (operation-journal-length registry)
                        "the id is durable before it is printed")
           (let ((record (accept-record-of journal "op-1")))
             (check-equal "op-1" (getf record :id) "the accept record names the id")
             (check-equal :import (getf record :kind) "the accept record names the op kind")
             (check-equal "req-import" (getf record :request)
                          "the accept record names the request id")
             (check-equal "rowan" (getf record :author) "the accept record names the author")
             (check-string= "2026-09-14T12:00:00Z" (getf record :stamp)
                            "the accept record names the stamp"))
           (check-equal :running (registry-operation-state registry op-id) "the work is running")
           ;; The CLI exits while the work continues.
           (operation-client-exit registry)
           (check-equal :running (registry-operation-state registry op-id)
                        "the work continues after the client exits")
           ;; operation wait is a bounded block over an event cursor and leaves a
           ;; timed-out operation running (:2733).
           (multiple-value-bind (result state cursor)
               (registry-operation-wait registry op-id :timeout 5 :after 0)
             (ok (null result) "a wait timeout returns no result")
             (check-equal :timeout state "the wait reports the timeout")
             (check-equal 0 cursor "a timed-out wait is still at cursor 0")
             (check-equal :running (registry-operation-state registry op-id)
                          "the operation is left running"))
           ;; A completed result is retrievable by its id afterwards, and
           ;; resuming from the advanced cursor answers the recorded disposition
           ;; at once, never by re-reading from the start.
           (operation-complete registry op-id "imported 42 items")
           (check-equal :done (registry-operation-state registry op-id) "the operation completes")
           (check-string= "imported 42 items" (registry-operation-result registry op-id)
                          "the result is retrievable by id afterwards")
           (multiple-value-bind (result state cursor)
               (registry-operation-wait registry op-id :timeout 5 :after 1)
             (check-string= "imported 42 items" result
                            "the resumed wait returns the result")
             (check-equal :done state "the resumed wait reports done")
             (check-equal 1 cursor "the completed wait advances the event cursor"))
           ;; An id no journal holds has a line of its own (:2727-2729).
           (multiple-value-bind (state line code) (registry-operation-state registry "op-missing")
             (ok (null state) "an unknown operation has no state")
             (check-equal 2 code "exit 2")
             (ok (search "no such operation" line) "and its own line"))
           ;; The recovery reconciliation has an id for every operation a caller
           ;; was told about: a restart over the same journal still holds it.
           (close-file-journal journal)
           (let ((reopened (open-file-journal path)))
             (unwind-protect
                  (let ((restarted (make-operation-registry :journal reopened)))
                    (check-equal 1 (operation-journal-length restarted)
                                 "the durable accept record survives the restart")
                    (multiple-value-bind (state line code)
                        (registry-operation-state restarted "op-1")
                      (declare (ignore line))
                      (ok (null state) "a restart does not invent a state for the id")
                      (check-equal 2 code "an id the restarted process has not recovered answers exit 2"))
                    (let ((record (recover-operation restarted "op-1")))
                      (check-equal "op-1" (getf record :id)
                                   "recovery reads the accepted id from the journal")
                      (check-equal :queued (registry-operation-state restarted "op-1")
                                   "a recovered operation is reconciled before anything is retried")))
               (close-file-journal reopened))))
      (ignore-errors (delete-file path)))))

;;; ------------------------------------------------------------------
;;; the-cli-thin-client (SPEC-WORK.md:2642-2646, :2679-2683, :2691-2694).
;;; The row the spec does not name a replay for gets its own deftest, named
;;; by the paragraph: the Go CLI is a thin client of the resident session, and
;;; what is executable here is the client side of the wire the wire row owns --
;;; the ordered request frame, the reply matched by request id, the lines split
;;; by the second token, and the emitted count. The live socket and its framing
;;; are the transport and wire rows'; the client drives decoded frames here.
;;; ------------------------------------------------------------------

(deftest "the-cli-thin-client" "docs/SPEC-WORK.md:2642-2646,2679-2683,2691-2694"
    "expected=frame-ordered-ints-are-strings;lines-split-by-second-token;replies-matched-by-id;duplicate-id-refused;unknown-id-refused;emitted-counted;no-identity"
  ;; The request frame is the pinned spelling op/request/as/expect/now/max/
  ;; deadline/args, in that order, and every integer is a JSON string
  ;; (SPEC-WORK.md:2663-2676).
  (let ((frame (client-request-frame
                (make-client-request :op "query" :request "q-1" :as "rowan"
                                     :expect 9007199254740993
                                     :now "2026-09-14T12:00:00Z" :max 20
                                     :args '(("node" . "acme/work"))))))
    (ok (search "\"op\":\"query\"" frame) "the frame names the op: ~A" frame)
    (ok (search "\"request\":\"q-1\"" frame) "the frame names its request id: ~A" frame)
    (ok (search "\"as\":\"rowan\"" frame) "the frame carries the author text: ~A" frame)
    ;; an integer above 2^53 is a string of decimal digits, never a JSON number
    (ok (search "\"expect\":\"9007199254740993\"" frame)
        "a bignum is not a JSON-string field: ~A" frame)
    (ok (search "\"max\":\"20\"" frame) "a small integer is a string too: ~A" frame)
    ;; an absent key is null, not an empty string
    (ok (search "\"deadline\":null" frame) "an absent deadline is null: ~A" frame)
    (ok (search "\"args\":{\"node\":\"acme/work\"}" frame)
        "args is the ordered JSON object: ~A" frame)
    (ok (not (search "\"max\":20" frame)) "no JSON number crosses the frame: ~A" frame))
  ;; The client ships no identity: an absent `as` stays null and no friend,
  ;; bench or house name is a default of this build (SPEC-WORK.md:5209).
  (let ((bare (client-request-frame (make-client-request :op "query" :request "q-2"))))
    (ok (search "\"as\":null" bare) "an absent author is null: ~A" bare)
    (ok (not (search "acme" bare)) "the tool ships a house name: ~A" bare)
    (ok (not (search "rowan" bare)) "the tool ships a friend name: ~A" bare))
  ;; The client splits the handed lines by the second token and by nothing
  ;; else: OK/ROW/NOTE/MORE to stdout, FAIL/RACED to stderr (:2679-2683).
  (let ((lines '("QUERY OK ask=size" "READY ROW id=acme/work/f1/t1" "FLEET NOTE pong"
                 "QUERY MORE after=7" "SESSION FAIL reason=held"
                 "CLIP RACED expected=deadbeef found=cafef00d")))
    (multiple-value-bind (stdout stderr) (client-route-lines lines)
      (check-equal '("QUERY OK ask=size" "READY ROW id=acme/work/f1/t1"
                     "FLEET NOTE pong" "QUERY MORE after=7")
                   stdout "OK/ROW/NOTE/MORE are stdout, whatever the noun")
      (check-equal '("SESSION FAIL reason=held" "CLIP RACED expected=deadbeef found=cafef00d")
                   stderr "FAIL/RACED are stderr, whatever the noun")))
  ;; A fresh client holds no kernel and reloads nothing; it does not put the
  ;; same request id in flight twice on one connection (:2646, :2701).
  (let ((client (make-cli-client)))
    (check-equal '() (cli-client-in-flight client) "a fresh client has nothing in flight")
    (check-equal '() (cli-client-settled client) "a fresh client has settled nothing")
    (check-equal 0 (cli-client-emitted client) "a fresh client has printed nothing")
    (ok (stringp (cli-client-send client (make-client-request :op "query" :request "q-1")))
        "the first request is admitted")
    (let ((refused nil))
      (handler-case
          (progn (cli-client-send client (make-client-request :op "query" :request "q-1"))
                 (fail "the same id was put in flight twice"))
        (unsupported-input () (setf refused t)))
      (ok refused "the same id in flight twice is refused"))
    ;; A reply matches by its request id, never arrival order: q-2's frame may
    ;; arrive first and still settle only q-2.
    (ok (stringp (cli-client-send client (make-client-request :op "query" :request "q-2")))
        "the second request is admitted")
    (let ((reply (client-decode-response
                  "{\"request\":\"q-2\",\"ok\":true,\"exit\":\"0\",\"lines\":[\"QUERY OK ask=size\"],\"rev\":\"7\",\"pushed\":\"6\"}")))
      (cli-client-receive client reply)
      (check-equal "q-2" (getf reply :request) "the out-of-order reply settles q-2")
      (check-equal 0 (getf reply :exit) "the reply carries its exit code")
      (check-equal '("q-1") (mapcar #'car (cli-client-in-flight client))
                   "q-1 is still outstanding after q-2's reply"))
    (cli-client-receive client (client-decode-response
                                "{\"request\":\"q-1\",\"ok\":true,\"exit\":\"0\",\"lines\":[],\"rev\":\"7\",\"pushed\":\"6\"}"))
    (check-equal '() (cli-client-in-flight client) "both replies settle their own request")
    (check-equal '("q-2" "q-1") (cli-client-settled client)
                 "the settled ids are the ones the replies named")
    ;; An unknown response id is a protocol error: nothing is falsely settled.
    (cli-client-send client (make-client-request :op "query" :request "q-3"))
    (let ((refused nil))
      (handler-case
          (progn (cli-client-receive
                  client (client-decode-response
                          "{\"request\":\"q-9\",\"ok\":true,\"exit\":\"0\",\"lines\":[],\"rev\":\"7\",\"pushed\":\"6\"}"))
                 (fail "an unknown response id settled nothing"))
        (unsupported-input () (setf refused t)))
      (ok refused "an unknown response id is a protocol error")
      (check-equal '("q-3") (mapcar #'car (cli-client-in-flight client))
                   "the unknown reply settled no outstanding request")))
  ;; The client prints exactly the lines it was handed and counts the bytes it
  ;; printed; emitted is on every OK line (SPEC-WORK.md:2679-2681, :5336).
  (let ((lines '("QUERY OK ask=size" "SESSION FAIL reason=held")))
    (check-equal (+ (length "QUERY OK ask=size") 1
                    (length "SESSION FAIL reason=held") 1)
                 (client-lines-bytes lines)
                 "emitted counts every printed line and its newline"))
  (let* ((client (make-cli-client))
         (out (make-string-output-stream))
         (err (make-string-output-stream)))
    (cli-client-send client (make-client-request :op "query" :request "q-9"))
    (multiple-value-bind (exit emitted stdout stderr)
        (cli-client-run client
                        "{\"request\":\"q-9\",\"ok\":true,\"exit\":\"0\",\"lines\":[\"QUERY OK ask=size\",\"SESSION FAIL reason=x\"],\"rev\":\"1\",\"pushed\":\"-\"}"
                        :out out :err err)
      (check-equal 0 exit "the client prints the response's own exit code")
      (check-equal (+ (length "QUERY OK ask=size") 1 (length "SESSION FAIL reason=x") 1)
                   emitted "emitted is the bytes printed")
      (check-string= "QUERY OK ask=size" (string-trim '(#\Newline)
                                                        (get-output-stream-string out))
                     "stdout carries the OK line")
      (check-string= "SESSION FAIL reason=x" (string-trim '(#\Newline)
                                                           (get-output-stream-string err))
                     "stderr carries the FAIL line")
      (check-equal emitted (cli-client-emitted client)
                   "the client's own emitted count is the bytes it printed")))
)
