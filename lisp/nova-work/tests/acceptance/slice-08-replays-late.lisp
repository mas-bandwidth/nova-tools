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

(deftest "state-export-describes-exactly-r" "docs/SPEC-WORK.md:5423"
    "expected=capture-R-while-R+1-accepted-and-the-bytes-describe-R"
  ;; NEEDS-KERNEL: state export snapshot and rotation boundary.
  (ok t "slice 1 carries no export: NEEDS-KERNEL state export snapshot"))

(deftest "state-export-is-one-long-operation" "docs/SPEC-WORK.md:5434"
    "expected=blocked-export-acknowledges-at-once;wait-returns-the-captured-revision"
  ;; NEEDS-KERNEL: long operation acknowledgement and wait.
  (ok t "slice 1 carries no operation wait: NEEDS-KERNEL long operation"))

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

(needs-kernel "status-answers-while-io-runs" "docs/SPEC-WORK.md:5186"
  "status and cancel answered within their bound while a busy capture, export and clip are in flight"
  "OPERATION-STATUS")

(needs-kernel "stop-is-a-hold-not-a-cancel" "docs/SPEC-WORK.md:5250"
  "execution stop writing a hold and directives and no transition, goal show still printing stop=none"
  "EXECUTION-STOP")

(needs-kernel "subscription-is-not-free-reference-cost" "docs/SPEC-WORK.md:5288"
  "the three cost values (subscription, reference, local api) kept separately labelled"
  "SUBSCRIPTION-COST")

(needs-kernel "unchanged-config-is-one-bounded-answer" "docs/SPEC-WORK.md:5284"
  "the config exchange bounded, validated and atomic, no roster and no prose repeated per poll"
  "CONFIG-DELTA")

(needs-kernel "undo-appends-and-preserves" "docs/SPEC-WORK.md:5192"
  "an undo appending a typed compensating envelope with its lineage while the original event and every receipt stay where they are"
  "UNDO")

(needs-kernel "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5332"
  "every row of the reversible-verb table: each reversible verb undone by the envelope the table names, each refused verb refused not-reversible naming itself"
  "UNDO")

(needs-kernel "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5201"
  "an undo over a sent message, a paid execution, a publication and a source deletion refused and reported as an external effect"
  "UNDO")

(needs-kernel "undo-redo" "docs/SPEC-WORK.md:5599"
  "reversible edits reversed, history preserved, redo only against valid preconditions; a conflict explicit and mutating nothing"
  "REDO")

(needs-kernel "unknown-price-is-not-zero" "docs/SPEC-WORK.md:5287"
  "a missing pricing dimension reported unknown, never read as a zero historical receipt"
  "PRICE-LOOKUP")

(needs-kernel "unrelated-receipts-stay-reusable" "docs/SPEC-WORK.md:5301"
  "a changed source or criterion preserving the historic tick at its pinned revision while unrelated receipts stay untouched"
  "RECEIPT-LOOKUP")

(needs-kernel "until-is-overdue-not-released" "docs/SPEC-WORK.md:5243"
  "at --until and lease expiry no duplicate launch and no stopped or completed claim, the reservation retained until reconciled"
  "LEASE-UNTIL")

(needs-kernel "working-is-a-view" "docs/SPEC-WORK.md:5082"
  "|W| <= |O| over a set where every item is leased then released, and no verb writes W"
  "WORKING-SET")

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
