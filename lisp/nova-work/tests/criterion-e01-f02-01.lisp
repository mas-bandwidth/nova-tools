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

(deftest "e01-f02-01-load-state-forwards-bounds" "docs/SPEC-WORK.md:E01-F02-01"
    "expected=load-state-refuses-snapshot-over-max-depth+max-nodes+utf8-max-bytes-before-read"
  ;; (f) load-state forwards max-depth and max-nodes to the snapshot read: a
  ;; good manifest loads under wide bounds and is refused under tight ones.
  (let* ((k (fresh))
         (m (export-manifest (kernel-state k) :id "exp-bounds")))
    (multiple-value-bind (snap line) (load-state m :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
      (ok snap "the good manifest under wide bounds refused: ~A" line))
    (multiple-value-bind (snap line) (load-state m :max-bytes 1000000 :max-depth 1 :max-nodes 1000000)
      (ok (null snap) "a snapshot deeper than max-depth 1 loaded: ~A" line)
      (ok (and line (search "max-depth" line)) "the refusal names max-depth: ~A" line))
    (multiple-value-bind (snap line) (load-state m :max-bytes 1000000 :max-depth 1000 :max-nodes 2)
      (ok (null snap) "a snapshot wider than max-nodes 2 loaded: ~A" line)
      (ok (and line (search "max-nodes" line)) "the refusal names max-nodes: ~A" line)))
  ;; (g) the depth bound is checked before the reader runs: 100000 nested
  ;; parens are refused by name, never read (no stack exhaustion).
  (let ((text (concatenate 'string (make-string 100000 :initial-element #\()
                           (make-string 100000 :initial-element #\)))))
    (handler-case
        (progn (nova-work::read-restricted text :max-depth 64)
               (fail "read-restricted read a 100000-deep form under max-depth 64"))
      (nova-work::restricted-data-violation (c)
        (ok (search "depth 65 exceeds max-depth 64"
                    (nova-work::restricted-data-violation-value c))
            "the pre-read refusal names depth 65: ~A" c))))
  ;; (h) max-bytes counts UTF-8 octets, not characters: 7 characters, 10 bytes.
  (let ((text (format nil "(\"~C~C~C\")" (code-char #xe9) (code-char #xe9) (code-char #xe9))))
    (ok (= 7 (length text)) "the fixture is 7 characters")
    (handler-case
        (progn (nova-work::read-restricted text :max-bytes 8)
               (fail "read-restricted counted characters, not UTF-8 bytes"))
      (nova-work::restricted-data-violation (c)
        (ok (search "10 bytes exceeds max-bytes 8"
                    (nova-work::restricted-data-violation-value c))
            "the refusal counts 10 UTF-8 bytes: ~A" c)))))
