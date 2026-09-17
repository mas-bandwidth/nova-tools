;;;; closed-history.lisp --- the read side of the COW root: the partition
;;;; check, bounded closed-index paging with a pinned cursor, the ask's branch
;;;; and window flags, and the coverage-gap accounting.
;;;;
;;;; docs/SPEC-WORK.md:5492-5543, 5592. Nothing here loads the history: the
;;;; rows are the append-only closed index the write path maintains, so a page
;;;; visits no node, parses nothing and replays nothing.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The COW root partition (SPEC-WORK.md:5495-5497)
;;; ------------------------------------------------------------------

(defun cow-load-findings (state)
  "Rule 18 findings over a candidate root: every id named by a closed-index row
while its node still reads :o -- an id in both C and O."
  (let ((closed (make-hash-table :test #'equal))
        (findings '()))
    (dolist (row (wstate-rows state))
      (setf (gethash (getf row :node) closed) t))
    (dolist (id (wstate-order state))
      (let ((node (%node-quiet state id)))
        (when (and node (gethash id closed) (eq :o (wnode-branch node)))
          (push id findings))))
    (nreverse findings)))

(defun cow-partition-holds-p (state)
  "One id is in C or in O and never in both, and open= plus closed= equals the
scope's counted total on every ask (SPEC-WORK.md:5495-5497)."
  (and (null (cow-load-findings state))
       (= (+ (wstate-root-open state) (wstate-closed state))
          (length (wstate-order state)))
       (every (lambda (id)
                (member (wnode-branch (%node-quiet state id)) '(:o :c)))
              (wstate-order state))))

(defun hand-write-closed-row (state id rev)
  "A closed-index row written by hand for an id the node still reads :o: the
double membership rule 18 catches at load (SPEC-WORK.md:5496)."
  (push (list :key (format nil "~D:~A" rev id) :kind :settle :node id :rev rev
              :disposition :done :stamp "2026-09-14T12:00:00Z"
              :revived "-" :settles 1)
        (wstate-rows state))
  state)

(defun cow-candidate-gate (state)
  "Refuse a candidate whose root is not a partition. (values OK-P LINE CODE)."
  (let ((findings (cow-load-findings state)))
    (if findings
        (values nil (format nil "LOAD FAIL rule 18: ~{~A~^,~} in both C and O"
                            findings)
                1)
        (values t "LOAD OK" 0))))

;;; ------------------------------------------------------------------
;;; Bounded closed-index paging, cursor pinned to a revision
;;; (SPEC-WORK.md:5538-5540, 5592-5594)
;;; ------------------------------------------------------------------

(defstruct (closed-cursor
             (:constructor make-closed-cursor (&key rev after floor)))
  rev after floor)

(defun closed-rows-before (state rev)
  "The closed-index rows whose event revision is at or below REV, newest first.
Reads the index only: no node is visited, nothing is parsed and nothing is
replayed."
  (remove-if (lambda (row) (> (getf row :rev) rev)) (wstate-rows state)))

(defun closed-page (state &key (max 20) cursor floor)
  "One bounded page of the closed index. A CURSOR pins the revision it was
opened at, so a settle between two pages cannot add, drop or repeat a row in
the page sequence. Answers (values ROWS MORE-P NEXT-CURSOR LINE); a cursor
whose pinned revision is below FLOOR is refused `page expired`
(SPEC-WORK.md:5538-5540, 5592-5594)."
  (let* ((rev (if cursor (closed-cursor-rev cursor) (state-revision state)))
         (floor (or floor (if cursor (closed-cursor-floor cursor) 0)))
         (after (and cursor (closed-cursor-after cursor))))
    (when (< rev floor)
      (return-from closed-page
        (values nil nil nil
                (format nil "QUERY FAIL page expired rev=~D floor=~D" rev floor))))
    (let* ((rows (closed-rows-before state rev))
           (pos (if after
                    (position after rows :key (lambda (r) (getf r :key))
                              :test #'string=)
                    -1))
           (slice (subseq rows (1+ (or pos -1))))
           (page (subseq slice 0 (min max (length slice))))
           (more (> (length slice) (length page)))
           (next (and more (make-closed-cursor
                            :rev rev
                            :after (getf (car (last page)) :key)
                            :floor floor))))
      (values page more next
              (format nil "QUERY OK ask=closed shown=~D more=~A after=~A rev=~D parses=0 replays=0"
                      (length page) (if more "t" "f")
                      (if next (closed-cursor-after next) "-") rev)))))

;;; ------------------------------------------------------------------
;;; The closed ask with the retention archive file absent
;;; (SPEC-WORK.md:5541-5543)
;;; ------------------------------------------------------------------

(defun closed-ask (state &key archive reach-bodies)
  "The closed index answers its rows whatever the retention archive file's
state. ARCHIVE is a hash of closed-row key -> body, or NIL when the file is
absent. REACH-BODIES asks for the archived bodies: a missing body is counted
into gap= and named once by QUERY NOTE coverage-gap, and the row list is never
shortened (SPEC-WORK.md:5541-5543)."
  (let* ((rows (state-closed-rows state))
         (missing (if reach-bodies
                      (count-if (lambda (row)
                                  (not (and archive
                                            (gethash (getf row :key) archive))))
                                rows)
                      0)))
    (values rows
            (if (plusp missing)
                (format nil "QUERY OK ask=closed rows=~D gap=~D~%QUERY NOTE coverage-gap"
                        (length rows) missing)
                (format nil "QUERY OK ask=closed rows=~D gap=0" (length rows))))))

;;; ------------------------------------------------------------------
;;; The ask's branch and window flags (SPEC-WORK.md:5546-5549)
;;; ------------------------------------------------------------------

(defun validate-ask (&key ask branch from to)
  "A query --ask is refused at exit 2 unless it names --branch, a closed ask
carries --from/--to, an open ask carries no --from, and who/stale/handoffs are
not admitted under --branch closed or --branch root
(SPEC-WORK.md:5546-5549)."
  (cond
    ((null branch)
     (values nil (format nil "QUERY FAIL ask=~A: --branch is required"
                         (or ask :size))
             2))
    ((and (eq branch :closed) (null from) (null to))
     (values nil "QUERY FAIL branch=closed: --from or --to is required" 2))
    ((and (eq branch :open) from)
     (values nil "QUERY FAIL branch=open: --from is not admitted" 2))
    ((and (member ask '(:who :stale :handoffs)) (member branch '(:closed :root)))
     (values nil (format nil "QUERY FAIL ask=~A: not admitted under branch=~A"
                         ask branch)
             2))
    (t (values t "QUERY OK" 0))))
