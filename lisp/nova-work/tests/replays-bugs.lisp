;;;; replays-bugs.lisp --- the six bug replays, red first.
;;;;
;;;; Each names the line of docs/SPEC-WORK.md (the Required replays table at
;;;; 1913-1923) it comes from and asserts the Required outcome the table states.
;;;; The verbs these replays name (node add, who/check/stale, decompose, roadmap
;;;; parity) are not in slice 1's CLI, so the assertions drive the pure part
;;;; carried in src/replays-bugs.lisp.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; bug-needs-found-during                     SPEC-WORK.md:1918
;;; ------------------------------------------------------------------

(deftest "bug-needs-found-during" "docs/SPEC-WORK.md:1918"
    "expected=without--found-during-refused-exit-1-naming-the-field-and-nothing-else;missing-id-rule-2"
  (let ((idx (make-bug-index)))
    ;; Without --found-during: refused at exit 1 naming the field, index unchanged.
    (multiple-value-bind (ok line code idx2)
        (add-bug idx :id "b1" :known-ids '("root" "root/f"))
      (ok (null ok) "a bug without --found-during was admitted")
      (check-equal 1 code "exit code of a bug without --found-during")
      (ok (search "found-during" line) "refusal does not name found-during: ~A" line)
      (ok (eq idx idx2) "a refused add changed the index"))
    ;; One naming a missing id: refused by rule 2.
    (multiple-value-bind (ok line code idx2)
        (add-bug idx :id "b1" :found-during "root/nope" :known-ids '("root" "root/f"))
      (ok (null ok) "a bug naming a missing id was admitted")
      (check-equal 1 code "exit code naming a missing id")
      (ok (search "rule 2" line) "missing id refused by other than rule 2: ~A" line)
      (ok (eq idx idx2) "a rule-2 refusal changed the index"))))

;;; ------------------------------------------------------------------
;;; bug-cannot-close-without-test             SPEC-WORK.md:1919
;;; ------------------------------------------------------------------

(deftest "bug-cannot-close-without-test" "docs/SPEC-WORK.md:1919"
    "expected=rule-19-no-test;other-name;verified-at-current-generation-accepted;correct-voids"
  (let ((no-test (make-nbug :id "b1" :status :doing)))
    (ok (search "rule 19" (bug-close-verdict no-test nil 3))
        "a bug with no :test reached :done"))
  (let ((other (make-nbug :id "b2" :test "t2" :status :doing)))
    (ok (search "rule 19" (bug-close-verdict other '(("t1" . 3)) 3))
        "a bug whose :test is another name's verified test reached :done"))
  (let ((named (make-nbug :id "b3" :test "t3" :status :doing)))
    (check-equal nil (bug-close-verdict named '(("t3" . 3)) 3)
                 "the named test verified at the current generation was refused")
    (let ((after-correct (correct-voids '(("t3" . 3)) "t3")))
      (ok (search "rule 19" (bug-close-verdict named after-correct 3))
          "a correct did not void the test lock"))))

;;; ------------------------------------------------------------------
;;; bug-blocks-parent-done                    SPEC-WORK.md:1920
;;; ------------------------------------------------------------------

(deftest "bug-blocks-parent-done" "docs/SPEC-WORK.md:1920"
    "expected=open-bug-keeps-unknown-notch-green-percent-unchanged;bug-done-green-no-scope-event-no-revision"
  (let ((blocked (feature-verdict :required-done 3 :required-open 0 :open-bugs 1)))
    (check-equal :unknown (sv-state blocked) "a feature holding an open bug is not unknown")
    (ok (null (sv-green blocked)) "a feature holding an open bug is green")
    (check-equal 100 (sv-percent blocked) "percent moved over a bug")
    (ok (null (sv-scope-event blocked)) "a bug took a scope event"))
  (let ((settled (feature-verdict :required-done 3 :required-open 0 :open-bugs 0)))
    (check-equal :done (sv-state settled) "a feature with its bug settled stayed unknown")
    (ok (sv-green settled) "a feature with its bug settled is not green")
    (check-equal 100 (sv-percent settled) "percent moved when the bug settled")
    (ok (null (sv-scope-event settled)) "settling a bug wrote a scope event")
    (ok (null (sv-revision-moved settled)) "settling a bug moved the revision")))

;;; ------------------------------------------------------------------
;;; who-line-counts-bugs                      SPEC-WORK.md:1921
;;; ------------------------------------------------------------------

(deftest "who-line-counts-bugs" "docs/SPEC-WORK.md:1921"
    "expected=bugs=<open>/<fixed>-only;opened-fixed-revived-moves-both"
  (let ((idx (make-bug-index)))
    (bug-put idx (make-nbug :id "b1" :status :open))
    (multiple-value-bind (open fixed) (bug-tally idx)
      (let ((line (who-line "root" open fixed)))
        (ok (search "bugs=1/0" line) "open bug not counted 1/0: ~A" line)
        (ok (null (or (search "done=" line) (search "required=" line)
                      (search "rows=" line) (search "since-baseline=" line)))
            "a counting row leaked a tree field: ~A" line)))
    (bug-set-status idx "b1" :fixed)
    (multiple-value-bind (open fixed) (bug-tally idx)
      (ok (search "bugs=0/1" (who-line "root" open fixed)) "fixed bug not counted 0/1"))
    (bug-set-status idx "b1" :open)
    (multiple-value-bind (open fixed) (bug-tally idx)
      (ok (search "bugs=1/0" (who-line "root" open fixed)) "revived bug not back to 1/0"))))

;;; ------------------------------------------------------------------
;;; decompose-may-mint-bug                    SPEC-WORK.md:1922
;;; ------------------------------------------------------------------

(deftest "decompose-may-mint-bug" "docs/SPEC-WORK.md:1922"
    "expected=one-envelope;bug-carries-found-during-and-acceptance;required-unchanged;scope-plus-one;without--found-during-refused-whole"
  (let ((feature (make-feature-model :id "root/f" :required '("t1" "t2") :scope-revision 4)))
    (multiple-value-bind (split reason)
        (decompose feature
                   '((:into "task:t3")
                     (:into "bug:b9" :found-during "root/f/t1"
                            :acceptance (:kind :test :subject "test:a@1"))))
      (ok (null reason) "decompose refused: ~A" reason)
      (check-equal 1 (sp-scope-revision-delta split) "scope revision did not move by one")
      (ok (sp-required-unchanged-p split) "the required set changed")
      (let ((bug (find "b9" (sp-minted-bugs split) :key #'bg-id :test #'string=)))
        (ok bug "the minted bug b9 is missing")
        (check-equal "root/f/t1" (bg-found-during bug) "minted bug without :found-during")
        (ok (bg-acceptance bug) "minted bug without its acceptance"))))
  (let ((feature (make-feature-model :id "root/f" :required '("t1") :scope-revision 4)))
    (multiple-value-bind (split reason)
        (decompose feature '((:into "bug:b9" :acceptance (:kind :test))))
      (ok (null split) "a decompose without --found-during was not refused whole")
      (ok (search "found-during" reason) "whole refusal does not name found-during: ~A" reason))))

;;; ------------------------------------------------------------------
;;; roadmap-bug-form-parses                   SPEC-WORK.md:1923
;;; ------------------------------------------------------------------

(deftest "roadmap-bug-form-parses" "docs/SPEC-WORK.md:1923"
    "expected=:bugs-at-four-levels-parses-under-three-bounds;parity-equals-current-bugs;features-and-items-unchanged;bare-(bug)-refused-exit-2-byte-offset"
  (let* ((roadmap
          (list :current-bugs '(3 1)
                :current-features 3
                :current-acceptance-items 5
                :bugs '((:id "b0" :status "open"))
                :epics
                (list (list :id "e1"
                            :bugs '((:id "b1" :status "open"))
                            :features
                            (list (list :id "f1"
                                        :bugs '((:id "b2" :status "open"))
                                        :items
                                        (list (list :id "i1"
                                                    :bugs '((:id "b3" :status "fixed"))))))))))
         (text (canonical-string roadmap)))
    (multiple-value-bind (code form) (parse-roadmap text 100000 100 100000)
      (check-equal 0 code "the roadmap refused under the three bounds")
      (check-equal 4 (length (collect-bug-entries form)) ":bugs at four levels not all found"))
    (multiple-value-bind (equal-p line) (roadmap-parity roadmap)
      (ok equal-p "parity bugs != :current-bugs")
      (ok (search "bugs=3/1" line) "parity line wrong: ~A" line)
      (ok (search "features=3" line) "parity folded bugs into features: ~A" line)
      (ok (search "items=5" line) "parity folded bugs into items: ~A" line))
    (multiple-value-bind (code reason)
        (parse-roadmap "(bug \"b\" :found-during \"root\")" 1000 10 1000)
      (check-equal 2 code "a bare (bug ...) symbol form was not refused at exit 2")
      (ok (search "byte" reason) "the refusal names no byte offset: ~A" reason))))
