;;;; issue-1989.lisp --- reproduction test for nova-tools#1989.
;;;;
;;;; tests/acceptance.lisp loads three slices more than once, so 18 acceptance
;;;; cases are registered twice or three times. This test verifies
;;;; *acceptance-slices* has no duplicates.

(in-package #:nova-work/tests)

(deftest "issue-1989" "nova-tools#1989"
    "expected=no-duplicate-acceptance-slices"
  (let ((seen (make-hash-table :test #'equal))
        (dups '()))
    (dolist (slice *acceptance-slices*)
      (if (gethash slice seen)
          (push slice dups)
          (setf (gethash slice seen) t)))
    (ok (null dups)
        "tests/acceptance.lisp loads three slices more than once: ~{~A~^, ~}" dups)))