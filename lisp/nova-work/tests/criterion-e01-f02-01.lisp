;;;; criterion-e01-f02-01.lisp --- proof that every file read requires
;;;; max-bytes, max-depth and max-nodes, enforced before the read.
;;;;
;;;; E01-F02-01: "Require max-bytes, max-depth and max-nodes on every file read."
;;;; SPEC-WORK.md "The data": every verb that reads a file takes the three
;;;; bounds, none defaulted; a file past any of them is refused before parsing
;;;; finishes, and a missing bound is `refusing to guess`.
;;;;
;;;; READ-BOUNDED (src/value.lisp) is the bounded gate for file text: all three
;;;; bounds are required and REFUSE-OVER-BOUNDS checks them lexically before the
;;;; reader runs. LOAD-STATE, STATE-LOAD and READ-LOADED-SNAPSHOT
;;;; (src/state-export.lisp) require the three bounds and check them before a
;;;; member is parsed; STATE-LOAD and READ-LOADED-SNAPSHOT also check a file's
;;;; size against max-bytes before its octets are read.

(in-package #:nova-work/tests)

(defun %e01-f02-01-refusal (thunk)
  "The restricted-data-violation text THUNK signals, or NIL when it returns."
  (handler-case (progn (funcall thunk) nil)
    (nova-work::restricted-data-violation (c)
      (or (nova-work::restricted-data-violation-value c) ""))))

(defun %e01-f02-01-nested (depth)
  (concatenate 'string (make-string depth :initial-element #\()
               (make-string depth :initial-element #\))))

(deftest "e01-f02-01-require-max-bytes-max" "docs/SPEC-WORK.md:E01-F02-01"
    "expected=read-bounded-refuses-excess-bytes+depth+nodes;missing-bound-refusing-to-guess"
  (let ((wide-bytes 1000000) (wide-depth 1000) (wide-nodes 1000000)
        (text "(:k1 :k2 :k3 :k4 :k5 :k6 :k7 :k8 :k9 :k10 :k11 :k12 :k13 :k14 :k15)"))
    ;; (a) max-bytes: input longer than the bound is refused
    (ok (> (length text) 50) "the fixture is longer than the bound")
    (let ((why (%e01-f02-01-refusal
                (lambda () (nova-work::read-bounded text :max-bytes 10 :max-depth wide-depth
                                                         :max-nodes wide-nodes)))))
      (ok (and why (search "max-bytes" why)) "refusal names max-bytes: ~A" why))
    ;; (b) max-bytes: input within the bound succeeds
    (check-equal '(:a) (nova-work::read-bounded "(:a)" :max-bytes 100 :max-depth wide-depth
                                                       :max-nodes wide-nodes)
                 "short input passes max-bytes")
    ;; (c) max-depth: a deeply nested form is refused
    (let ((why (%e01-f02-01-refusal
                (lambda () (nova-work::read-bounded (format nil "~A:k~A" (make-string 10 :initial-element #\()
                                                            (make-string 10 :initial-element #\)))
                                                    :max-bytes wide-bytes :max-depth 5
                                                    :max-nodes wide-nodes)))))
      (ok (and why (search "max-depth" why)) "refusal names max-depth: ~A" why))
    ;; (d) max-nodes: a form with too many atoms is refused, within it passes
    (ok (consp (nova-work::read-bounded text :max-bytes wide-bytes :max-depth wide-depth
                                             :max-nodes 100))
        "many-node form within bound passes")
    (let ((why (%e01-f02-01-refusal
                (lambda () (nova-work::read-bounded text :max-bytes wide-bytes :max-depth wide-depth
                                                         :max-nodes 5)))))
      (ok (and why (search "max-nodes" why)) "refusal names max-nodes: ~A" why))
    ;; (e) none defaulted: each missing bound is `refusing to guess`, named,
    ;; and nothing is parsed.
    (dolist (missing '(:max-bytes :max-depth :max-nodes))
      (let* ((args (loop for (k v) on (list :max-bytes wide-bytes :max-depth wide-depth
                                            :max-nodes wide-nodes)
                         by #'cddr unless (eq k missing) append (list k v)))
             (before nova-work::*parses*)
             (why (%e01-f02-01-refusal
                   (lambda () (apply #'nova-work::read-bounded "(:a (:b :c))" args)))))
        (ok (and why (search "refusing to guess" why)
                 (search (string-downcase (symbol-name missing)) why))
            "a read without ~A was not refused by name: ~A" missing why)
        (check-equal before nova-work::*parses*
                     (format nil "a read without ~A parsed" missing))))))

(deftest "e01-f02-01-load-state-forwards-bounds" "docs/SPEC-WORK.md:E01-F02-01"
    "expected=load-state-refuses-snapshot-over-max-depth+max-nodes+utf8-max-bytes-before-read;cr-comment-scanned"
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
      (ok (and line (search "max-nodes" line)) "the refusal names max-nodes: ~A" line))
    ;; none defaulted: a load missing a bound is refused by name, unparsed.
    (let ((before nova-work::*parses*))
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000 :max-nodes 1000000)
        (ok (null snap) "a load with no --max-depth loaded: ~A" line)
        (ok (and line (search "refusing to guess" line) (search "--max-depth" line))
            "the refusal does not name the missing --max-depth: ~A" line))
      (check-equal before nova-work::*parses* "a load with a missing bound parsed")))
  ;; (g) the depth bound is checked before the reader runs: 100000 nested
  ;; parens are refused by name, never read (no stack exhaustion).
  (let ((why (%e01-f02-01-refusal
              (lambda () (nova-work::read-bounded (%e01-f02-01-nested 100000)
                                                  :max-bytes 1000000 :max-depth 64
                                                  :max-nodes 1000000)))))
    (ok (and why (search "depth 65 exceeds max-depth 64" why))
        "the pre-read refusal names depth 65: ~A" why))
  ;; (h) max-bytes counts UTF-8 octets, not characters: 7 characters, 10 bytes.
  (let ((text (format nil "(\"~C~C~C\")" (code-char #xe9) (code-char #xe9) (code-char #xe9))))
    (ok (= 7 (length text)) "the fixture is 7 characters")
    (let ((why (%e01-f02-01-refusal
                (lambda () (nova-work::read-bounded text :max-bytes 8 :max-depth 10 :max-nodes 10)))))
      (ok (and why (search "10 bytes exceeds max-bytes 8" why))
          "the refusal counts 10 UTF-8 bytes: ~A" why)))
  ;; (i) a comment ends at CR as well as LF (as REFUSE-EVALUATION-SYNTAX lexes
  ;; it): the form after `;c<CR>` is scanned, so its depth is refused before
  ;; the reader runs rather than skipped as comment text.
  (let* ((text (format nil ";c~C~A" #\Return (%e01-f02-01-nested 100)))
         (before nova-work::*parses*)
         (why (%e01-f02-01-refusal
               (lambda () (nova-work::read-bounded text :max-bytes 1000000 :max-depth 64
                                                        :max-nodes 1000000)))))
    (ok (and why (search "depth 65 exceeds max-depth 64" why))
        "a form after a CR-terminated comment escaped the depth scan: ~A" why)
    (check-equal before nova-work::*parses* "the CR-comment input was parsed"))
  (let ((why (%e01-f02-01-refusal
              (lambda () (nova-work::read-bounded (format nil ";c~C(:a :b :c :d)" #\Return)
                                                  :max-bytes 1000000 :max-depth 64
                                                  :max-nodes 3)))))
    (ok (and why (search "exceeds max-nodes 3" why))
        "atoms after a CR-terminated comment escaped the node scan: ~A" why)))

(deftest "e01-f02-01-state-load-requires-bounds" "docs/SPEC-WORK.md:E01-F02-01"
    "expected=state-load+read-loaded-snapshot-refuse-missing-or-exceeded-bounds-before-read;no-destination"
  (let* ((base (namestring (test-temp-dir "e01-f02-01-state-load")))
         (k (fresh))
         (export-dir (concatenate 'string base "export/")))
    (write-state-export (kernel-state k) export-dir :id "exp-f02")
    ;; none defaulted: each missing bound refuses by name, parses nothing and
    ;; leaves no destination.
    (dolist (missing '(:max-bytes :max-depth :max-nodes))
      (let* ((dest (format nil "~Amissing-~(~A~)/" base missing))
             (args (loop for (k v) on (list :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
                         by #'cddr unless (eq k missing) append (list k v)))
             (before nova-work::*parses*))
        (multiple-value-bind (snap line) (apply #'state-load :from export-dir :into dest args)
          (ok (null snap) "a state load without ~A loaded: ~A" missing line)
          (ok (and line (search "refusing to guess" line)
                   (search (format nil "--~(~A~)" missing) line))
              "the refusal does not name --~(~A~): ~A" missing line)
          (check-equal before nova-work::*parses*
                       (format nil "a state load without ~A parsed" missing))
          (ok (null (probe-file dest)) "a refused state load left ~A" dest))))
    ;; Depth and nodes are checked on the member text before it is parsed.
    (let ((dest (concatenate 'string base "deep/"))
          (before nova-work::*parses*))
      (multiple-value-bind (snap line)
          (state-load :from export-dir :into dest :max-bytes 1000000 :max-depth 1 :max-nodes 1000000)
        (ok (null snap) "a state load past --max-depth 1 loaded: ~A" line)
        (ok (and line (search "max-depth" line)) "the refusal names max-depth: ~A" line)
        (check-equal before nova-work::*parses* "an over-deep file was parsed")
        (ok (null (probe-file dest)) "a refused state load left a destination")))
    (let ((dest (concatenate 'string base "wide/"))
          (before nova-work::*parses*))
      (multiple-value-bind (snap line)
          (state-load :from export-dir :into dest :max-bytes 1000000 :max-depth 1000 :max-nodes 3)
        (ok (null snap) "a state load past --max-nodes 3 loaded: ~A" line)
        (ok (and line (search "max-nodes" line)) "the refusal names max-nodes: ~A" line)
        (check-equal before nova-work::*parses* "an over-wide file was parsed")))
    ;; A file larger than --max-bytes is refused on its size, unread.
    (let ((dest (concatenate 'string base "big/"))
          (before nova-work::*parses*))
      (multiple-value-bind (snap line)
          (state-load :from export-dir :into dest :max-bytes 8 :max-depth 1000 :max-nodes 1000000)
        (ok (null snap) "a state load past --max-bytes 8 loaded: ~A" line)
        (ok (and line (search "exceeds --max-bytes 8" line)) "the refusal names --max-bytes: ~A" line)
        (check-equal before nova-work::*parses* "an over-size file was parsed")))
    ;; The loaded snapshot's reader takes the same three bounds.
    (let ((snap-dir (concatenate 'string base "snap/")))
      (multiple-value-bind (snap line)
          (state-load :from export-dir :into snap-dir :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
        (ok snap "the good export under wide bounds refused: ~A" line))
      (ok (read-loaded-snapshot snap-dir :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
          "the loaded snapshot did not read under wide bounds")
      (dolist (missing '(:max-bytes :max-depth :max-nodes))
        (let* ((args (loop for (k v) on (list :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
                           by #'cddr unless (eq k missing) append (list k v)))
               (why (%e01-f02-01-refusal (lambda () (apply #'read-loaded-snapshot snap-dir args)))))
          (ok (and why (search "refusing to guess" why)
                   (search (string-downcase (symbol-name missing)) why))
              "a snapshot read without ~A was not refused by name: ~A" missing why)))
      (let ((why (%e01-f02-01-refusal
                  (lambda () (read-loaded-snapshot snap-dir :max-bytes 1000000 :max-depth 1
                                                            :max-nodes 1000000)))))
        (ok (and why (search "max-depth" why)) "a snapshot read past max-depth 1: ~A" why)))))
