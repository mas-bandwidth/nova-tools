;;;; criterion-e01-f02-01.lisp --- proof that every file read requires
;;;; max-bytes, max-depth and max-nodes.
;;;;
;;;; E01-F02-01: "Require max-bytes, max-depth and max-nodes on every file read."
;;;; The read-restricted entry point (src/value.lisp) is the single gate that
;;;; reads s-expressions from text; it now accepts max-bytes, max-depth and
;;;; max-nodes keyword arguments and refuses inputs that exceed them.

(in-package #:nova-work/tests)

(deftest "e01-f02-01-require-max-bytes-max" "docs/SPEC-WORK.md:E01-F02-01"
    "expected=read-restricted-refuses-excess-bytes+depth+nodes;no-bounds-unchanged"
  ;; (a) max-bytes: input longer than the bound is refused
  (let ((text "(:k1 :k2 :k3 :k4 :k5 :k6 :k7 :k8 :k9 :k10 :k11 :k12 :k13 :k14 :k15)"))
    (ok (> (length text) 50) "the fixture is longer than the bound")
    (handler-case
        (progn (nova-work::read-restricted text :max-bytes 10)
               (fail "read-restricted should have refused max-bytes"))
      (nova-work::restricted-data-violation (c)
        (ok (search "max-bytes"
                     (nova-work::restricted-data-violation-value c))
            "refusal names max-bytes: ~A" c))))
  ;; (b) max-bytes: input within the bound succeeds
  (let ((text "(:a)"))
    (let ((result (nova-work::read-restricted text :max-bytes 100)))
      (ok (equal '(:a) result) "short input passes max-bytes"))
    (handler-case
        (progn (nova-work::read-restricted text :max-bytes 1)
               (fail "read-restricted should have refused max-bytes"))
      (nova-work::restricted-data-violation (c)
        (ok t "long input refused by max-bytes: ~A" c))))
  ;; (c) max-depth: a deeply nested form is refused
  (let ((text (format nil "~A:k~A" (make-string 10 :initial-element #\() (make-string 10 :initial-element #\) ))))
    (handler-case
        (progn (nova-work::read-restricted text :max-depth 5)
               (fail "read-restricted should have refused max-depth"))
      (nova-work::restricted-data-violation (c)
        (ok (search "max-depth"
                     (nova-work::restricted-data-violation-value c))
            "refusal names max-depth: ~A" c))))
  ;; (d) max-nodes: a form with too many atoms is refused
  (let ((text "(:k1 :k2 :k3 :k4 :k5 :k6 :k7 :k8 :k9 :k10 :k11 :k12 :k13 :k14 :k15)"))
    (let ((result (nova-work::read-restricted text :max-nodes 100)))
      (ok (consp result) "many-node form within bound passes"))
    (handler-case
        (progn (nova-work::read-restricted text :max-nodes 5)
               (fail "read-restricted should have refused max-nodes"))
      (nova-work::restricted-data-violation (c)
        (ok (search "max-nodes"
                     (nova-work::restricted-data-violation-value c))
            "refusal names max-nodes: ~A" c))))
  ;; (e) no bounds: the original behaviour is unchanged
  (let ((text "(:a (:b :c))"))
    (let ((result (nova-work::read-restricted text)))
      (ok (equal '(:a (:b :c)) result) "no-bounds read still works"))))
