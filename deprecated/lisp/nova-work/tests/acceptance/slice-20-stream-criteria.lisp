;;;; slice-20-stream-criteria.lisp --- two criterion tests (E01-F05-03 and
;;;; E07-F06-01) that came in through stream/nova-work, moved out of
;;;; acceptance.lisp so that file keeps at most one deftest (nova-tools
;;;; #560: one file per slice, TestOneFilePerSliceAndSection560).

(in-package #:nova-work/tests)

;;; E01-F05-03 "Compare exports with an independent semantic comparator"
;;; (docs/roadmaps/nova-work.sexp, subfeature 3 of E01-F05 "Canonical
;;; encoding and semantic round trip"). The contract line is
;;; docs/SPEC-WORK.md:7053: comparison "uses immutable source captures and an
;;; independently implemented semantic comparator, never the production
;;; serializer checking itself"; the executable shape is docs/SPEC-WORK.md:7072
;;; (`full-round-trip`): export, load in a fresh isolated engine, export again,
;;; and compare every semantic field.

(defun independent-semantic-equal (bytes-a bytes-b)
  "An independent semantic comparator over two exports: each is parsed through
the restricted reader (which refuses dispatch macros, quotes and evaluation), a
separate implementation from the canonical printer; the two resulting forms are
then compared as values. This is never the production serializer re-serializing
and checking its own bytes (docs/SPEC-WORK.md:7053)."
  (equal (read-restricted bytes-a) (read-restricted bytes-b)))

(deftest "TestE01F05CompareExportsWithAnIndependent" "docs/SPEC-WORK.md:7053,7072"
    "expected=independent-reader-compares-exports-semantically;distinguishes-different-exports"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1" :evidence '("ev-1")))
        "close refused")
    (ok (submit k (reopen-request :request "req-2")) "reopen refused")
    (let* ((state (kernel-state k))
           (exported (state-canonical-form state))
           (bytes (canonical-string exported)))
      ;; The independent reader recovers exactly the semantic form captured.
      (check-equal exported (read-restricted bytes)
                   "an independent reader recovered a different semantic form")
      ;; A fresh isolated engine rebuilds the state from the bytes, and its
      ;; re-export is compared against the original export semantically, through
      ;; the independent reader, never through the production serializer.
      (let ((rebuilt (reconstruct-state bytes)))
        (check-equal (state-history state) (state-history rebuilt)
                     "the fresh engine rebuilt a different history")
        (check-equal (root-digest state) (root-digest rebuilt)
                     "the fresh engine rebuilt a different root digest")
        (ok (independent-semantic-equal
             bytes (canonical-string (state-canonical-form rebuilt)))
            "two exports of one state compared semantically different"))
      ;; The comparator is not vacuous: two different exports compare unequal.
      (let ((k2 (fresh)))
        (ok (submit k2 (close-request :request "req-x" :evidence '("ev-x")))
            "second close refused")
        (ok (not (independent-semantic-equal
                  bytes (canonical-string
                         (state-canonical-form (kernel-state k2)))))
            "two different exports compared equal through the independent reader")))))

;;; E07-F06-01 (ROADMAP.md:778): "Omit private nodes and descendants from
;;; rendered output files." docs/SPEC-WORK.md:947-948 — "render never writes a
;;; private node or its descendants into an output file".

(deftest "TestE07F06OmitPrivateNodesAndDescendants" "docs/SPEC-WORK.md:947-948"
    "expected=render-omits-a-private-node-and-every-containment-descendant-from-the-output-body"
  ;; Marking a feature private must omit the feature and every task beneath it,
  ;; while an unmarked sibling feature still renders.
  (let* ((state (make-seed-state
                 '((:id "root" :type :work-set :parent nil :state :unknown)
                   (:id "root/f1" :type :feature :parent "root" :state :unknown :private t)
                   (:id "root/f1/t1" :type :task :parent "root/f1" :state :doing)
                   (:id "root/f1/t2" :type :task :parent "root/f1" :state :doing)
                   (:id "root/f2" :type :feature :parent "root" :state :unknown))))
         (view (list :axes '() :revision 1 :projections '()
                     :members (list "root/f1" "root/f1/t1" "root/f1/t2" "root/f2")
                     :private '())))
    (multiple-value-bind (body line code) (render-view view :state state)
      (ok (eql 0 code) "the render of a view carrying a private node was refused: ~A" line)
      (ok (search "row=root/f2" body) "a public sibling was dropped from the rendered output: ~A" body)
      (ok (null (search "root/f1" body)) "a private node leaked into the rendered output: ~A" body)
      (ok (null (search "root/f1/t1" body)) "a private node's descendant leaked into the rendered output: ~A" body)
      (ok (null (search "root/f1/t2" body)) "a private node's descendant leaked into the rendered output: ~A" body))))
