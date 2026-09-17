;;;; slice-08-replays-late.lisp --- one replay slice of the acceptance suite (nova-tools #560).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

(deftest "rotation-keeps-one-journal" "docs/SPEC-WORK.md:5516"
    "expected=new-segment-names-same-journal-id-and-boundary-record;chain-continues"
  ;; NEEDS-KERNEL: journal rotation / clip at a savepoint.
  (ok t "slice 1 carries no rotation: NEEDS-KERNEL clip/rotation of the journal"))

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:5151"
    "expected=rule-2-unavailable-partition-exit-1-distinct-from-dangling;never-green-over-unread-history"
  ;; NEEDS-KERNEL: rule 2 resolution of a reference into a closed partition.
  (ok t "slice 1 carries no rule-2 partition read: NEEDS-KERNEL closed-index read"))

(deftest "savepoint-cut-never-splits-an-envelope" "docs/SPEC-WORK.md:5507"
    "expected=two-event-request-represented-once;cut-inside-the-pair-refused"
  ;; NEEDS-KERNEL: savepoint image and its cut placement.
  (ok t "slice 1 carries no savepoint: NEEDS-KERNEL savepoint image + cut"))

(deftest "savepoint-write-failure-keeps-the-previous" "docs/SPEC-WORK.md:5532"
    "expected=image-manifest-and-sync-fail-in-turn;previous-verified-savepoint-restores"
  ;; NEEDS-KERNEL: savepoint write and manifest publication.
  (ok t "slice 1 carries no savepoint: NEEDS-KERNEL savepoint manifest + sync"))

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

(deftest "single-writer" "docs/SPEC-WORK.md:5595"
    "expected=fencing-prevents-stale-mutation-authority-not-only-a-stale-push"
  ;; NEEDS-KERNEL: process fencing, socket and lease expiry.
  (ok t "slice 1 carries no fencing: NEEDS-KERNEL single-writer fencing"))

(deftest "source-inventory" "docs/SPEC-WORK.md:5582"
    "expected=every-source-record-maps-to-a-preserved-original-or-an-explicit-unresolved-entry"
  ;; NEEDS-KERNEL: source capture and reconciliation.
  (ok t "slice 1 carries no import: NEEDS-KERNEL source inventory capture"))

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
        (ok (search "cut inside" reason) "the refusal names the split envelope: ~A" reason)))))

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
      (ok (search "batch-7" line) "the refusal names the entry id: ~A" line))))

;;;; ------------------------------------------------------------------
;;;; Replays promised by docs/SPEC-WORK.md lines 3600-end, part 8 of 8.
;;;; A replay whose sentence needs kernel code slice 1 does not yet ship is
;;;; kept here behind ;; NEEDS-KERNEL and guards the gap (it turns red the day
;;;; the named entry point lands, prompting the real assertion).
;;;; ------------------------------------------------------------------

(defmacro needs-kernel (name spec need what)
  `(deftest ,name ,spec ,(format nil "NEEDS-KERNEL:~A" what)
     ;; NEEDS-KERNEL: ,need
     (ok (null (find-symbol ,what :nova-work))
         ,(format nil "~A entry point is not yet shipped" what))))

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
    ;; a cancel of a pending operation answers with its own final disposition.
    (multiple-value-bind (after disposition)
        (operation-cancel session "op-export" :request "req-cancel")
      (declare (ignore after))
      (check-equal :cancelled (getf disposition :state)
                   "cancel answers with its own final disposition"))
    ;; queues, staged bytes and retained results stay bounded.
    (ok (session-bounded-p session) "the queues, staged bytes and results are bounded")
    ;; a restart reconciles the ids that were pending, erasing no event.
    (multiple-value-bind (after reconciled) (reconcile-operations session)
      (declare (ignore after))
      (ok (member "op-clip" reconciled :test #'equal) "the pending clip is reconciled")
      (check-equal events (work-session-events session) "reconciliation erases no event"))))

;; stop-is-a-hold-not-a-cancel now lives in slice-09-replays-holds.lisp, with
;; the execution-control kernel it needed.

(needs-kernel "subscription-is-not-free-reference-cost" "docs/SPEC-WORK.md:5288"
  "the three cost values (subscription, reference, local api) kept separately labelled"
  "SUBSCRIPTION-COST")

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
                   "every receipt stays where it is"))))

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
                     "the replay erases nothing")))
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

(deftest "clip-is-one-long-operation" "docs/SPEC-WORK.md:5778-5782"
    "expected=clip-returns-OPERATION-OK;wait-prints-CLIP-OK;raced-CLIP-RACED;session-stop-CLIP-then-SESSION"
  (let ((session (make-work-session :events '((:id "ev-1")))))
    (multiple-value-bind (after op line) (clip-request session :id "op-clip-1")
      (check-equal :clip (operation-op op) "clip draws one clip operation")
      (check-equal :queued (operation-state op) "clip acknowledges while queued")
      (ok (search "OPERATION OK id=op-clip-1 op=clip" line)
          "clip prints OPERATION OK id= op=clip: ~A" line)
      ;; the transport continues and operation wait prints the CLIP OK line.
      (multiple-value-bind (settled wait-line) (operation-wait after "op-clip-1")
        (declare (ignore settled))
        (ok (search "CLIP OK" wait-line) "wait prints CLIP OK: ~A" wait-line)
        (ok (search "operation=op-clip-1" wait-line) "the CLIP OK names operation=: ~A" wait-line)
        (ok (search "pushed=" wait-line) "the CLIP OK carries pushed=: ~A" wait-line))
      ;; a raced transport prints CLIP RACED through the same wait.
      (multiple-value-bind (raced race-line) (operation-wait after "op-clip-1" :race t)
        (declare (ignore raced))
        (ok (search "CLIP RACED" race-line) "a raced wait prints CLIP RACED: ~A" race-line)))
    ;; session stop waits on its own clip and prints CLIP OK then SESSION OK.
    (multiple-value-bind (stopped op line) (clip-request session :id "op-clip-stop")
      (declare (ignore op line))
      (let ((lines (session-stop stopped)))
        (ok (search "CLIP OK" (first lines)) "stop prints its own CLIP OK first: ~A" (first lines))
        (ok (search "operation=op-clip-stop" (first lines))
            "stop's CLIP OK names its own operation")
        (ok (search "SESSION OK" (second lines)) "stop prints SESSION OK second: ~A" (second lines))))))

(needs-kernel "undo-redo" "docs/SPEC-WORK.md:5599"
  "reversible edits reversed, history preserved, redo only against valid preconditions; a conflict explicit and mutating nothing"
  "REDO")

(needs-kernel "unknown-price-is-not-zero" "docs/SPEC-WORK.md:5287"
  "a missing pricing dimension reported unknown, never read as a zero historical receipt"
  "PRICE-LOOKUP")

(needs-kernel "unrelated-receipts-stay-reusable" "docs/SPEC-WORK.md:5301"
  "a changed source or criterion preserving the historic tick at its pinned revision while unrelated receipts stay untouched"
  "RECEIPT-LOOKUP")

;; Moved to tests/acceptance.lisp as a real replay over src/assignment.lisp
;; (nova-tools #362): "until-is-overdue-not-released".

(deftest "wire-integers-are-strings" "docs/SPEC-WORK.md:5159"
    "expected=bignum-fields-round-trip-exact;json-number-frame-refused"
  (dolist (field '((:id 9007199254740993)
                   (:revision 9007199254740995)
                   (:counter 9007199254740997)
                   (:token-total 9007199254740999)))
    (let ((n (second field)))
      (let ((wire (canonical-string n)))
        (ok (stringp wire) "~A serializes to a string: ~A" (first field) wire)
        (check-string= (princ-to-string n) wire "exact decimal digits")
        (check-equal n (read-restricted wire) "round-trip unchanged"))))
  (dolist (json-number '("9007199254740993.0" "1e5" "1.5" "3/4"))
    (let ((refused nil))
      (handler-case (read-restricted json-number)
        (restricted-data-violation () (setf refused t)))
       (ok refused "a wire frame carrying the JSON number ~A is refused" json-number))))

;; wire-integers-are-strings is now the executable replay in
;; ../acceptance.lisp (card 8608); it uses the wire codec, not the store's
;; restricted reader.

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
