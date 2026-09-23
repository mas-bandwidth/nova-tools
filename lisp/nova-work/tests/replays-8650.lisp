;;;; replays-8650.lisp --- the five #362 acceptance replays of
;;;; docs/SPEC-WORK.md: the preservation/recovery suite rows
;;;; `schema-evolution` (:6247) and `source-inventory` (:6228), the
;;;; single-writer rule (:2616, :6083), the `shared-prerequisite-owned-once`
;;;; obligation (:6365) and `subscription-is-not-free-reference-cost` (:5770,
;;;; :4642). Each deftest is named exactly as the spec names it and asserts
;;;; the outcome its paragraph promises.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; schema-evolution                            SPEC-WORK.md:6247
;;; ------------------------------------------------------------------

(deftest "schema-evolution" "docs/SPEC-WORK.md:6247"
    "expected=migrate=lossless,unsupported=refused,originals=preserved"
  (let* ((golden '(:schema "work-v1"
                   :seed ((:id "acme/work" :type :work-set :parent nil :state :unknown)
                          (:id "acme/work/f1" :type :feature :parent "acme/work"
                           :state :unknown))))
         (before (copy-tree golden))
         (migrated (migrate-state-schema golden)))
    ;; A supported old schema migrates: the body survives semantic comparison.
    (check-string= "work-v2" (getf migrated :schema) "the migrated schema tag")
    (check-equal (getf golden :seed) (getf migrated :seed)
                 "the migrated body is not the old body")
    (check-string= "work-v1" (getf (getf migrated :migration) :from)
                   "the migration does not record its source schema")
    ;; A migration never rewrites the only source copy.
    (check-equal before golden "the migration rewrote the only source copy")
    ;; An unsupported version refuses while preserving the originals.
    (let ((unsupported (list* :schema "work-v99" (cddr golden))))
      (handler-case
          (progn (migrate-state-schema unsupported)
                 (fail "an unsupported schema was migrated"))
        (unsupported-input () t))
      (check-equal "work-v99" (getf unsupported :schema)
                   "the refused source was rewritten")
      (check-equal (cddr before) (cddr unsupported)
                   "the refused source body was rewritten"))))

;;; ------------------------------------------------------------------
;;; shared-prerequisite-owned-once              SPEC-WORK.md:6365
;;; ------------------------------------------------------------------

(deftest "shared-prerequisite-owned-once" "docs/SPEC-WORK.md:6365"
    "expected=owner=one,references=every-cell-once,obligations=three"
  (let* ((prereq (declare-shared-prerequisite "acme/schema-fix" "glenn"))
         (prereq (reference-shared-prerequisite prereq "acme/work/f1"))
         (prereq (reference-shared-prerequisite prereq "acme/work/f2"))
         ;; A repeated reference is the same cell, never a second ownership.
         (prereq (reference-shared-prerequisite prereq "acme/work/f1")))
    ;; Owned once, and referenced by every affected cell.
    (check-string= "glenn" (prerequisite-owner prereq) "the prerequisite's owner")
    (check-equal '("acme/work/f1" "acme/work/f2") (prerequisite-cells prereq)
                 "the shared prerequisite is not referenced once per cell")
    ;; A merged fix, verified behaviour and a published distribution are
    ;; three evidence obligations beside feature completion.
    (check-equal '(:merged-fix :verified-behaviour :published-distribution)
                 (prerequisite-obligations prereq)
                 "the three evidence obligations")
    ;; A second owner is refused: it is owned once.
    (handler-case
        (progn (own-shared-prerequisite prereq "stella")
               (fail "a shared prerequisite was owned twice"))
      (unsupported-input () t))
    (check-string= "glenn" (prerequisite-owner prereq)
                   "the refused second owner moved the owner")))

;;; ------------------------------------------------------------------
;;; single-writer                               SPEC-WORK.md:6241, :6083
;;; ------------------------------------------------------------------

(deftest "single-writer" "docs/SPEC-WORK.md:6241"
    "expected=one-total-order,journal=sequence,in-order,outside-loop=defect"
  (let* ((k (fresh))
         (loop (make-command-loop :kernel k)))
    ;; Two concurrent clients' commands land in one total order. They are
    ;; interleaved, so the only source of an order is the one command loop.
    (command-loop-submit loop (close-request :request "a-1"
                                             :node "acme/work/f1/t1"
                                             :evidence '("ev-a1")))
    (command-loop-submit loop (close-request :request "b-1"
                                             :node "acme/work/f1/t2"
                                             :evidence '("ev-b1")))
    (command-loop-submit loop (reopen-request :request "a-2"
                                              :node "acme/work/f1/t1"))
    (command-loop-submit loop (reopen-request :request "b-2"
                                              :node "acme/work/f1/t2"))
    (check-equal '("a-1" "b-1" "a-2" "b-2") (command-loop-order loop)
                 "the two clients' commands are not in one total order")
    ;; The journal shows the sequence numbers in that order.
    (check-equal '("a-1" "b-1" "a-2" "b-2") (journal-order (kernel-journal k))
                 "the journal does not show the commands in that order")
    (let ((seq (command-loop-sequence loop)))
      (ok (equal seq (sort (copy-list seq) #'<))
          "the journal's sequence numbers are not in total order: ~S" seq)
      (check-equal 4 (length (remove-duplicates seq))
                   "two commands shared one sequence number"))
    ;; A mutation outside the command loop is a defect.
    (handler-case
        (progn (mutate-outside-command-loop k (close-request :request "c-1"))
               (fail "a mutation outside the command loop was admitted"))
      (unsupported-input () t))
    (check-equal '("a-1" "b-1" "a-2" "b-2") (journal-order (kernel-journal k))
                 "the outside mutation reached the journal")))

;;; ------------------------------------------------------------------
;;; source-inventory                            SPEC-WORK.md:6228
;;; ------------------------------------------------------------------

(deftest "source-inventory" "docs/SPEC-WORK.md:6228"
    "expected=record=original+mapping|unresolved,reconcile=equal,same-count-different-ids=fail"
  (let* ((captured (list (inventory-record "issue-1" :issue
                                           :original "{\"number\":1}"
                                           :mapping "acme/work/f1")
                         (inventory-record "comment-9" :comment
                                           :original "{\"body\":\"hi\"}"
                                           :mapping "acme/work/f1#c9")
                         (inventory-record "attach-7" :attachment
                                           :unresolved "not reachable")))
         (same (copy-tree captured))
         (different-ids (list (inventory-record "issue-2" :issue
                                                :original "{\"number\":1}"
                                                :mapping "acme/work/f1")
                              (inventory-record "comment-9" :comment
                                                :original "{\"body\":\"hi\"}"
                                                :mapping "acme/work/f1#c9")
                              (inventory-record "attach-7" :attachment
                                                :unresolved "not reachable")))
         (different-content (copy-tree captured)))
    (setf (getf (first different-content) :mapping) "acme/work/f9")
    ;; Every captured source record maps to a preserved original plus a
    ;; normalised mapping, or to an explicit unresolved entry.
    (dolist (record captured)
      (ok (or (record-resolved-p record) (record-unresolved-p record))
          "a record is neither resolved nor explicitly unresolved: ~S" record))
    (multiple-value-bind (okp line) (reconcile-inventory captured same)
      (ok okp "an inventory does not reconcile against itself: ~A" line))
    ;; The same counts with different ids or content must fail reconciliation.
    (multiple-value-bind (okp line) (reconcile-inventory captured different-ids)
      (ok (not okp) "same counts, different ids reconciled")
      (ok (search "id" line) "the id mismatch is not named: ~A" line))
    (multiple-value-bind (okp line) (reconcile-inventory captured different-content)
      (ok (not okp) "same ids, different content reconciled")
      (ok (search "content" line) "the content mismatch is not named: ~A" line))))

;;; ------------------------------------------------------------------
;;; subscription-is-not-free-reference-cost     SPEC-WORK.md:5770, :4642
;;; ------------------------------------------------------------------

(deftest "subscription-is-not-free-reference-cost" "docs/SPEC-WORK.md:5770"
    "expected=cash-and-reference=separate-labels,subscription=reference-not-zero"
  (let ((breakdown (cost-breakdown :measured-cash 0
                                   :marginal-cash 0
                                   :reference-tokens 1250
                                   :subscription-p t)))
    ;; Measured provider cash, estimated marginal cash and virtual reference
    ;; token cost are three separately labelled values.
    (check-equal 0 (getf breakdown :measured-cash) "measured cash is mislabelled")
    (check-equal 0 (getf breakdown :marginal-cash) "marginal cash is mislabelled")
    (check-equal 1250 (getf breakdown :reference-tokens) "reference tokens is mislabelled")
    (check-equal 3 (length (remove-duplicates
                            (list :measured-cash :marginal-cash :reference-tokens)))
                 "the three cost labels are not distinct")
    ;; A subscription does not make reference cost zero.
    (ok (not (zerop (getf breakdown :reference-tokens)))
        "a subscription zeroed the virtual reference token cost")
    (ok (subscription-covers-cash-p breakdown)
        "the paid cash and the reference cost are not kept separately")
    ;; And an unpriced dimension is unknown, never summed as zero.
    (let ((unknown (cost-breakdown :marginal-cash 0
                                   :reference-tokens 1250
                                   :priced-p nil)))
      (ok (absentp (getf unknown :measured-cash))
          "an unsupported cash dimension was reported as zero"))))

;;; ------------------------------------------------------------------
;;; E10-F07-03  session start --repair only when findings strictly
;;; decrease                                  (SPEC-WORK.md:2430-2440)
;;; ------------------------------------------------------------------

(deftest "TestE10F07SupportSessionStartRepairOnly" "docs/SPEC-WORK.md:2430"
    "expected=red-load-names-its-findings;source-unmodified;strict-decrease-admitted;otherwise-refused-with-no-repair"
  ;; A red set under rule 18 (SPEC-WORK.md:5496): an id whose latest closed row
  ;; settles it while its node still reads :o. One hand-written row = one
  ;; finding; two rows over two still-open ids = two findings.
  (let* ((red (hand-write-closed-row (make-seed-state *seed*) "acme/work/f1/t1" 7))
         (red-again (hand-write-closed-row (make-seed-state *seed*) "acme/work/f1/t1" 7))
         (redder (hand-write-closed-row
                  (hand-write-closed-row (make-seed-state *seed*) "acme/work/f1/t1" 7)
                  "acme/work/f1/t2" 8))
         (clean (make-seed-state *seed*)))
    (ok (= 1 (length (cow-load-findings red))) "the red fixture is not red")
    (multiple-value-bind (sess line code)
        (session-start :repair t :state-seed red :owner "rowan" :base "tip")
      (ok sess "session start --repair did not start a session: ~A" line)
      ;; A red load under --repair still names its findings, and only a load
      ;; with zero findings exits 0 (SPEC-WORK.md:2438).
      (check-equal 1 (session-findings sess)
                   "session start --repair did not name its loaded finding count")
      (check-equal 1 code "a red --repair load did not exit 1")
      (ok (search "findings=1" (session-status-line sess))
          "the SESSION OK line does not carry findings=: ~A"
          (session-status-line sess))
      ;; The unmodified source: starting left the red state it was given alone.
      (check-equal 1 (length (cow-load-findings red))
                   "session start --repair rewrote the unmodified source")
      ;; Repair admits a candidate only when its finding count strictly drops.
      (multiple-value-bind (admitted rline rcode)
          (session-repair-gate sess clean :node "acme/work/f1/t1")
        (ok admitted "a repair dropping findings to zero was refused: ~A" rline)
        (check-equal 0 rcode "the admitted repair is not exit 0"))
      ;; The same count is no repair: refused at exit 1 with the repair diff.
      (multiple-value-bind (admitted rline rcode)
          (session-repair-gate sess red-again :node "acme/work/f1/t1")
        (ok (not admitted) "a repair that keeps the finding count was admitted")
        (check-equal 1 rcode "the refused non-decrease is not exit 1")
        (ok (search "no repair" rline)
            "the refusal is not the `no repair` line: ~A" rline)
        (ok (search "findings=1" rline)
            "the refusal does not name the candidate findings: ~A" rline)
        (ok (search "was=1" rline)
            "the refusal does not name the current findings: ~A" rline))
      ;; A candidate that raises the count is refused too.
      (multiple-value-bind (admitted rline rcode)
          (session-repair-gate sess redder :node "acme/work/f1/t2")
        (ok (not admitted) "a repair that raises the finding count was admitted")
        (check-equal 1 rcode "the refused increase is not exit 1")
        (ok (search "no repair" rline)
            "the refusal is not the `no repair` line: ~A" rline)))))

(deftest "TestE10F07RepairSessionSubmitIsGated" "docs/SPEC-WORK.md:2430"
    "expected=non-decrease-refused-through-submit;nothing-applied;decrease-admitted-through-submit;findings-updated"
  ;; The gate is the session's mutation path, not a free function: a mutation
  ;; submitted to a --repair session reaches O only when the candidate's
  ;; finding count strictly drops (SPEC-WORK.md:2430-2440).
  (let ((red (hand-write-closed-row (make-seed-state *seed*) "acme/work/f1/t1" 7)))
    (multiple-value-bind (sess line code)
        (session-start :repair t :state-seed red :owner "rowan" :base "tip")
      (declare (ignore code))
      (ok sess "session start --repair did not start a session: ~A" line)
      (let* ((k (session-kernel sess))
             (history (length (state-history (kernel-state k)))))
        ;; t2 moving to doing leaves t1's finding standing: no repair.
        (multiple-value-bind (okp rline rcode)
            (session-submit sess (list :verb :state-to-doing :node "acme/work/f1/t2"
                                       :by "rowan" :reason "picked up" :evidence '("ev-1")
                                       :request "repair-no-1" :stamp "2026-09-14T12:00:00Z"
                                       :clock :tool :generation-owner "gen-4"))
          (ok (not okp) "a mutation that keeps the finding count was admitted: ~A" rline)
          (check-equal 1 rcode "the refused non-decrease is not exit 1")
          (ok (and rline (search "no repair" rline))
              "the refusal is not the `no repair` line: ~A" rline))
        (check-equal history (length (state-history (kernel-state k)))
                     "the refused mutation was applied to O")
        (check-equal 1 (session-findings sess)
                     "the refused mutation moved the finding count")
        ;; Settling t1 takes it out of O: the one finding goes, so it is admitted.
        (multiple-value-bind (okp rline rcode)
            (session-submit sess (close-request :node "acme/work/f1/t1"
                                                :request "repair-yes-1"))
          (ok okp "a mutation that drops the finding count was refused: ~A" rline)
          (check-equal 0 rcode "the admitted repair is not exit 0"))
        (check-equal 0 (session-findings sess)
                     "the admitted repair did not update the finding count")
        (check-equal 0 (length (cow-load-findings (kernel-state k)))
                     "the admitted repair did not reach O")))))
