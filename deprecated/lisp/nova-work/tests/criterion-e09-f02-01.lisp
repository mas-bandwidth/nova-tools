;;;; criterion-e09-f02-01.lisp --- E09-F02-01: Map one issue to many nodes and repeated updates without duplicates

(in-package #:nova-work/tests)

(deftest "e09-f02-01-map-issue-many-nodes" "docs/SPEC-WORK.md:E09-F02-01"
    "Map one issue to many nodes and repeated updates without duplicates"
  (let* ((prereq (declare-shared-prerequisite "acme/issue-1" "owner1"))
         (prereq (own-shared-prerequisite prereq "owner1")))
    ;; Reference the prerequisite from multiple cells
    (setf prereq (reference-shared-prerequisite prereq "acme/work/f1"))
    (setf prereq (reference-shared-prerequisite prereq "acme/work/f2"))
    (setf prereq (reference-shared-prerequisite prereq "acme/work/f3"))
    
    ;; Check that all three cells are referenced
    (check-equal '("acme/work/f1" "acme/work/f2" "acme/work/f3")
                (prerequisite-cells prereq)
                "three different cells should be referenced")
    
    ;; Reference the same cell again - should not create a duplicate
    (setf prereq (reference-shared-prerequisite prereq "acme/work/f1"))
    (check-equal '("acme/work/f1" "acme/work/f2" "acme/work/f3")
                (prerequisite-cells prereq)
                "repeated reference should not create duplicates")
    
    ;; Reference from another cell
    (setf prereq (reference-shared-prerequisite prereq "acme/work/f4"))
    (check-equal '("acme/work/f1" "acme/work/f2" "acme/work/f3" "acme/work/f4")
                (prerequisite-cells prereq)
                "fourth cell should be added")
    
    ;; Verify owner is still correct
    (check-string= "owner1" (prerequisite-owner prereq) "owner should be preserved")
    
    ;; Verify obligations are present
    (check-equal '(:merged-fix :verified-behaviour :published-distribution)
                (prerequisite-obligations prereq)
                "obligations should be present")
    
    ;; Verify that non-string cells are refused
    (handler-case
        (progn (reference-shared-prerequisite prereq 123)
               (fail "non-string cell was accepted"))
      (unsupported-input () t))))
