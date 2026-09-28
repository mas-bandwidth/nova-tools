;;;; criterion-e07-f04-01.lisp --- nova-work criterion E07-F04-01.
;;;;
;;;; E07-F04 (Fixed Tables imported baseline inventory), criterion 01:
;;;; "Preserve source revision, audit IDs, reports and aliases".
;;;;
;;;; docs/SPEC-WORK.md:7785-7786: "Pin the baseline membership, source revision
;;;; and completion unit before starting the stream. Append discoveries visibly;
;;;; retain their discovery event and the original baseline."
;;;; docs/SPEC-WORK.md:7826-7827: "Original audit IDs, historical reports and
;;;; superseding aliases remain retrievable. They are provenance, not current
;;;; completion assertions."
;;;;
;;;; nova-tools #2056 proved this criterion UNMET (no imported-baseline surface
;;;; carried any of the four); src/imported-baseline.lisp is the production
;;;; change this test pins.

(in-package #:nova-work/tests)

(deftest "criterion-e07-f04-01" "docs/SPEC-WORK.md:7785-7786,7826-7827"
    "expected=revision+unit+membership-pinned;audit-ids,reports,aliases-retrievable;provenance-not-completion"
  (let* ((rev "8ea5ed8e4656875088250f564e88a965a7135e7c")
         (rows (list (list :id "schema/fixed-tables/versioning/cpp"
                           :audit-id "FT-AUDIT-07"
                           :reports '((:report "audit-2026-09-13.md" :result :pass)))
                     (list :id "schema/fixed-tables/versioning/struct"
                           :audit-id "FT-AUDIT-11"
                           :aliases '("schema/ft/struct-evolution")
                           :reports '((:report "audit-2026-09-13.md" :result :partial)))))
         (b (import-baseline :source-revision rev :completion-unit :required-leaf
                             :rows rows)))
    ;; 1. An import without a pinned source revision or completion unit refuses.
    (ok (handler-case (progn (import-baseline :completion-unit :required-leaf
                                              :rows rows)
                             nil)
          (nova-work-error () t))
        "an import without a source revision was accepted")
    (ok (handler-case (progn (import-baseline :source-revision rev :rows rows) nil)
          (nova-work-error () t))
        "an import without a completion unit was accepted")
    ;; 2. Revision, unit and membership are pinned: mutating the caller's input
    ;;    after the import changes nothing retained.
    (setf (getf (first rows) :audit-id) "FT-AUDIT-XX")
    (check-string= rev (imported-baseline-source-revision b) "pinned source revision")
    (check-equal :required-leaf (imported-baseline-completion-unit b) "pinned completion unit")
    (check-equal '("schema/fixed-tables/versioning/cpp" "schema/fixed-tables/versioning/struct")
                 (imported-baseline-members b) "pinned baseline membership")
    ;; 3. The original audit ID is retrievable by row and resolves back to it.
    (check-string= "FT-AUDIT-07"
                   (getf (baseline-provenance b "schema/fixed-tables/versioning/cpp") :audit-id)
                   "the original audit ID was not preserved")
    (check-string= "schema/fixed-tables/versioning/struct"
                   (getf (baseline-provenance b "FT-AUDIT-11") :id)
                   "the original audit ID does not resolve to its row")
    ;; 4. An imported alias and a later superseding rename both stay retrievable.
    (check-string= "schema/fixed-tables/versioning/struct"
                   (getf (baseline-provenance b "schema/ft/struct-evolution") :id)
                   "an imported alias does not resolve")
    (baseline-supersede b "schema/fixed-tables/versioning/cpp"
                        "schema/fixed-tables/versioning/cpp-backend"
                        :by "stella" :reason "cell renamed by the survey")
    (let ((p (baseline-provenance b "schema/fixed-tables/versioning/cpp")))
      (ok p "the superseded id is no longer retrievable")
      (check-string= "schema/fixed-tables/versioning/cpp-backend" (getf p :id)
                     "the superseded id does not resolve to its successor")
      (check-string= "FT-AUDIT-07" (getf p :audit-id)
                     "the audit ID was lost to the rename")
      (ok (member "schema/fixed-tables/versioning/cpp" (getf p :aliases) :test #'string=)
          "the superseded id is not listed as an alias: ~S" (getf p :aliases)))
    (check-equal '("schema/fixed-tables/versioning/cpp" "schema/fixed-tables/versioning/struct")
                 (imported-baseline-members b)
                 "a rename rewrote the original baseline membership")
    ;; 5. Historical reports are appended, never replaced, oldest first.
    (baseline-add-report b "schema/fixed-tables/versioning/cpp-backend"
                         '(:report "survey-2026-09-18.md" :result :fail))
    (let ((reports (getf (baseline-provenance b "FT-AUDIT-07") :reports)))
      (check-equal 2 (length reports) "historical report count")
      (check-string= "audit-2026-09-13.md" (getf (first reports) :report)
                     "the original report was not preserved")
      (check-string= "survey-2026-09-18.md" (getf (second reports) :report)
                     "the later report was not appended"))
    ;; 6. Provenance is not a current completion assertion: a historical :pass
    ;;    report completes nothing, and the denominator is the pinned baseline.
    (check-equal :provenance
                 (getf (baseline-provenance b "FT-AUDIT-07") :assertion)
                 "provenance was presented as a completion assertion")
    (let ((report (inventory-report (baseline-accounting b))))
      (check-equal 0 (getf report :completed)
                   "a historical report was counted as completed work")
      (check-equal 2 (getf report :baseline) "accounting baseline count"))
    ;; 7. An unknown id is refused rather than guessed.
    (ok (null (baseline-provenance b "FT-AUDIT-99"))
        "an unknown audit ID resolved to a row")))
