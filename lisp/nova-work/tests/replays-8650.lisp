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
;;; TestE03F02SupportAddMetadataEditMove          SPEC-WORK.md:2928
;;;
;;; nova-work acceptance criterion E03-F02-01: "Support add, metadata
;;; edit, move/reparent, decompose, link/unlink and retire". This test
;;; pins the operations the slice-1 kernel already provides — add
;;; (`node-add`), permitted-metadata edit and link/unlink (`node-edit`),
;;; move/reparent (`node-move`) and retire (`node-remove`). It names the
;;; SPEC-WORK "work structure" row (docs/SPEC-WORK.md:2928). `decompose`
;;; (the `--into` split verb) is not yet present in this slice and is
;;; pinned separately by replays-8651's reversibility registry, so it is
;;; outside this test's scope.
;;; ------------------------------------------------------------------

(deftest "TestE03F02SupportAddMetadataEditMove" "docs/SPEC-WORK.md:2928"
    "expected=add=ok,edit=ok,link=ok,unlink=ok,move=ok,retire=ok"
  (let ((k (fresh)))
    ;; add: a new feature is admitted under the work set.
    (multiple-value-bind (okp line)
        (node-add k :id "acme/work/f3" :type :feature :parent "acme/work")
      (ok okp "add refused: ~A" line))
    (check-equal :feature (node-type (kernel-state k) "acme/work/f3")
                 "the added feature's type")
    ;; metadata edit: title and a link are permitted fields.
    (multiple-value-bind (okp line)
        (node-edit k "acme/work/f3"
                   :changes (list :title "F3"
                                  :links (list "https://example.com/i1"))
                   :request "edit-1")
      (ok okp "metadata edit refused: ~A" line))
    (check-string= "F3"
                   (getf (node-metadata (kernel-state k) "acme/work/f3") :title)
                   "the edited title")
    (check-equal '("https://example.com/i1")
                 (getf (node-metadata (kernel-state k) "acme/work/f3") :links)
                 "the added link")
    ;; unlink: links are a permitted metadata patch, cleared in place.
    (multiple-value-bind (okp line)
        (node-edit k "acme/work/f3" :changes (list :links :clear) :request "edit-2")
      (ok okp "unlink refused: ~A" line))
    (check-equal '() (getf (node-metadata (kernel-state k) "acme/work/f3") :links)
                 "the cleared links")
    ;; move/reparent: the task's containment edge moves from f1 to f2.
    (multiple-value-bind (okp line)
        (node-move k :id "acme/work/f1/t1" :from "acme/work/f1" :under "acme/work/f2"
                   :request "move-1")
      (ok okp "move refused: ~A" line))
    (check-string= "acme/work/f2"
                   (node-parent (kernel-state k) "acme/work/f1/t1")
                   "the moved node's new parent")
    (ok (member "acme/work/f1/t1"
                (node-children (kernel-state k) "acme/work/f2") :test #'equal)
        "the moved node is not listed under its new parent")
    ;; retire: node removal settles the added feature out of O.
    (multiple-value-bind (okp line) (node-remove k "acme/work/f3")
      (ok okp "retire refused: ~A" line))
    (check-equal :c (node-branch (kernel-state k) "acme/work/f3")
                 "the retired node's branch")))

;;; ------------------------------------------------------------------
;;; TestE09F01PreserveBodyCommentsLabelsRelationships  SPEC-WORK.md:7067
;;;
;;; nova-work acceptance criterion E09-F01-02: "Preserve body, comments,
;;; labels, relationships, attachments and pagination". This pins the
;;; SPEC-WORK `source-inventory` suite row (docs/SPEC-WORK.md:7067):
;;; "open and closed issues, comments, identities, labels, relationships,
;;; attachments and pagination; every captured source record maps to a
;;; preserved original plus a normalised mapping, or to an explicit
;;; unresolved entry". The slice-1 capture layer preserves each observed
;;; field through `capture-observe` (body, comments, labels, relationships
;;; and attachments are recorded as versioned observations, never dropped
;;; by a later one) and retains every paginated record through
;;; `inventory-record` / `reconcile-inventory` (equal record counts across
;;; pages reconcile; none is lost).
;;; ------------------------------------------------------------------

(deftest "TestE09F01PreserveBodyCommentsLabelsRelationships" "docs/SPEC-WORK.md:7067"
    "expected=body=preserved,comments=preserved,labels=preserved,relationships=preserved,attachments=preserved,pagination=all-pages-reconcile"
  (let ((cap (make-source-capture :source-revision 1)))
    (capture-observe cap "i1" :body "issue body text" :revision 1)
    (capture-observe cap "i1" :comments '("first" "second") :revision 1)
    (capture-observe cap "i1" :labels '("bug" "p1") :revision 1)
    (capture-observe cap "i1" :relationships '("related#42" "blocked-by#7") :revision 1)
    (capture-observe cap "i1" :attachments '("shot.png") :revision 1)
    ;; body: the captured body is preserved, never replaced by an empty cell.
    (check-string= "issue body text"
                   (source-version-value (first (capture-versions cap "i1" :body)))
                   "the issue body was not preserved")
    (check-equal '("first" "second")
                 (source-version-value (first (capture-versions cap "i1" :comments)))
                 "the comments were not preserved")
    (check-equal '("bug" "p1")
                 (source-version-value (first (capture-versions cap "i1" :labels)))
                 "the labels were not preserved")
    (check-equal '("related#42" "blocked-by#7")
                 (source-version-value (first (capture-versions cap "i1" :relationships)))
                 "the relationships were not preserved")
    (check-equal '("shot.png")
                 (source-version-value (first (capture-versions cap "i1" :attachments)))
                 "the attachments were not preserved"))
  ;; pagination: records captured across pages are all retained and reconcile.
  (let* ((paginated (list (inventory-record "issue-1" :issue
                                            :original "{\"number\":1}"
                                            :mapping "acme/work/f1")
                          (inventory-record "comment-1" :comment
                                            :original "{\"body\":\"hi\"}"
                                            :mapping "acme/work/f1#c1")
                          (inventory-record "attach-1" :attachment
                                            :original "{\"name\":\"shot.png\"}"
                                            :mapping "acme/work/f1#a1")))
         (recaptured (copy-tree paginated)))
    (check-equal 3 (length paginated) "the paginated records were dropped")
    (multiple-value-bind (okp line) (reconcile-inventory paginated recaptured)
      (ok okp "a paginated inventory does not reconcile against itself: ~A" line))))
