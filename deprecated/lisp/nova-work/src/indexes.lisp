;;;; indexes.lisp --- the reconstruction of the write-maintained counters and
;;;; indexes (SPEC-WORK.md:526, 5538-5568, 6363).
;;;;
;;;; `|O|`, `|C|`, the two separate counters, every container's own subtree
;;;; count and the holder (friend) index are moved on the write path and read,
;;;; never computed by a walk. A full independent reconstruction of the same
;;;; accepted revision is what proves the maintained values: the two agreeing
;;;; after every legal verb is the acceptance.
;;;;
;;;; `reconstruct-state-index` walks the node set and reads no maintained
;;;; counter; `state-index-mismatches` names every disagreement. Nothing here
;;;; mutates, parses or replays.

(in-package #:nova-work)

(defun reconstruct-state-index (state)
  "A full, independent walk of STATE computing what the write path maintains:
`|O|`, `|C|`, the open leaf-task and linked-issue counters, every container's
open subtree count INCLUDING itself, and the holder (friend) index. It reads no
maintained counter and is never the path a query takes."
  (let ((open 0) (closed 0) (leaf 0) (issue 0)
        (containers '()) (holders '()))
    (dolist (id (wstate-order state))
      (let ((node (gethash id (wstate-nodes state))))
        (if (eq :o (wnode-branch node))
            (progn
              (incf open)
              (when (member (wnode-type node) '(:task :bug))
                (incf leaf))
              (incf issue (length (%links-list (wnode-links node))))
              ;; The chain starts at the item itself: a container is in its
              ;; own count, the same rule the write path uses.
              (let ((cur id))
                (loop while cur
                      do (let ((cell (assoc cur containers :test #'equal)))
                           (if cell (incf (cdr cell)) (push (cons cur 1) containers)))
                         (let ((ancestor (gethash cur (wstate-nodes state))))
                           (setf cur (and ancestor (wnode-parent ancestor))))))
              (let ((holder (wnode-holder node)))
                (when holder
                  (let ((cell (assoc holder holders :test #'equal)))
                    (if cell (push id (cdr cell)) (push (cons holder (list id)) holders))))))
            (incf closed))))
    (list :open open :closed closed :leaf leaf :issue issue
          :containers containers :holders holders)))

(defun state-holder-index (state)
  "The holder (friend) index of the maintained snapshot: each live holder and
the open item ids it holds, in seed order. This is a read of the nodes' holder
field, not a maintained structure of its own."
  (let ((holders '()))
    (dolist (id (wstate-order state))
      (let ((node (gethash id (wstate-nodes state))))
        (when (eq :o (wnode-branch node))
          (let ((holder (wnode-holder node)))
            (when holder
              (let ((cell (assoc holder holders :test #'equal)))
                (if cell (push id (cdr cell)) (push (cons holder (list id)) holders))))))))
    holders))

(defun %normalize-holder-index (alist)
  "Holders and their held ids in a stable order, so two reconstructions that
name the same members compare equal."
  (sort (remove-if (lambda (cell) (null (cdr cell)))
                   (mapcar (lambda (cell)
                             (cons (car cell) (sort (copy-list (cdr cell)) #'string<)))
                           alist))
        #'string< :key #'car))

(defun state-index-mismatches (state)
  "Every way the maintained counters and indexes disagree with a full
independent reconstruction; () when they agree."
  (let ((reconstructed (reconstruct-state-index state)) (out '()))
    (unless (= (wstate-root-open state) (getf reconstructed :open))
      (push (format nil "|O| maintained=~A reconstructed=~A"
                    (wstate-root-open state) (getf reconstructed :open))
            out))
    (unless (= (wstate-closed state) (getf reconstructed :closed))
      (push (format nil "|C| maintained=~A reconstructed=~A"
                    (wstate-closed state) (getf reconstructed :closed))
            out))
    (unless (= (wstate-leaf-open state) (getf reconstructed :leaf))
      (push (format nil "leaf-open maintained=~A reconstructed=~A"
                    (wstate-leaf-open state) (getf reconstructed :leaf))
            out))
    (unless (= (wstate-issue-open state) (getf reconstructed :issue))
      (push (format nil "issue-open maintained=~A reconstructed=~A"
                    (wstate-issue-open state) (getf reconstructed :issue))
            out))
    (dolist (id (wstate-order state))
      (let* ((node (gethash id (wstate-nodes state)))
             (cell (assoc id (getf reconstructed :containers) :test #'equal))
             (expected (if cell (cdr cell) 0)))
        (unless (= (wnode-open-count node) expected)
          (push (format nil "container ~A maintained=~A reconstructed=~A"
                        id (wnode-open-count node) expected)
                out))))
    (unless (equal (%normalize-holder-index (state-holder-index state))
                   (%normalize-holder-index (getf reconstructed :holders)))
      (push "the holder index disagrees with reconstruction" out))
    out))
