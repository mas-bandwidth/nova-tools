;;;; s4-priority.lisp --- S4 "the pool is the tree" (#466): priority is a noun
;;;; and a verb, and it steers ordering and nothing else (SPEC-WORK.md:3134).
;;;;
;;;; :priority (:self <rank|absent> :subtree <rank|absent>) is the one fixed
;;;; field; effective rank is derived, never stored -- the nearest context, the
;;;; deepest :subtree on the containment path, else the default. Rank 2 precedes
;;;; 10: ranks compare as unsigned integers.

(in-package #:nova-work)

(defun rank-value (slot)
  "An explicit rank, or NIL for an absent slot. A rank is an unsigned integer of
at most eighteen digits (bounded before any conversion)."
  (if (absentp slot) nil slot))

(defun set-priority (node rank context)
  "Set the :self or :subtree slot to RANK (an integer) or clear it when RANK is
NIL. A same-value set is the no-effect case: the caller checks its own
changed= against the before/after pair (replay priority-grants-nothing)."
  (let ((n (copy-pool-node node)))
    (ecase context
      (:self (setf (pool-pri-self n) (or rank +absent+)))
      (:subtree (setf (pool-pri-subtree n) (or rank +absent+))))
    n))

(defun container-path (pool node)
  "The containment ancestors of NODE from the root to NODE's parent, inclusive of
every container on the way. Bounded by depth; the forest is acyclic by rule 3."
  (labels ((walk (id acc)
             (let ((n (pool-get pool id)))
               (if (null (pool-parent n))
                   acc
                   (walk (pool-parent n) (cons (pool-get pool (pool-parent n)) acc))))))
    (walk (pool-id node) '())))

(defun s4-effective-priority (pool id)
  "Return (values rank source context): the node's own :self, else the deepest
:subtree on its containment path, else the default. RANK is an integer or NIL
for the default; SOURCE is an id or NIL; CONTEXT is :self, :subtree or :default
(SPEC-WORK.md:3134)."
  (let ((node (pool-get pool id)))
    (unless node (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (let ((self (rank-value (pool-pri-self node))))
      (if self
          (values self (pool-id node) :self)
          (let ((best nil) (best-src nil))
            (dolist (anc (container-path pool node))
              (let ((sub (rank-value (pool-pri-subtree anc))))
                (when sub
                  (setf best sub best-src (pool-id anc)))))
            (if best
                (values best best-src :subtree)
                (values nil nil :default)))))))

(defun priority-rank-compare (a b)
  "Compare two effective ranks for `--order priority`: explicit ascending, the
default after every explicit rank. Ties share the returned 0 and fall to the
stable id compare."
  (cond
    ((and a b)
     (cond ((< a b) -1) ((> a b) 1) (t 0)))
    ((and (null a) (null b)) 0)
    (a -1)                 ; explicit before default
    (t 1)))

(defun rank-label (rank)
  (if rank (princ-to-string rank) "default"))

(defun context-label (context)
  (ecase context (:self "self") (:subtree "subtree") (:default "default")))
