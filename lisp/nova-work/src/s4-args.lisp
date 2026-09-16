;;;; s4-args.lisp --- S4 "the pool is the tree" (#466): the branch and window
;;;; rules every root-wide ask wears (SPEC-WORK.md:1780 and replay
;;;; branch-and-window-required).

(in-package #:nova-work)

(defun named-listing-ask-p (ask)
  "The listing asks over O alone: a live lease is a fact of O and never of C."
  (member ask '(:who :stale :handoffs)))

(defun check-query-ask (ask &key branch from to order)
  "Validate the flags a root-wide query lists. Returns NIL when admissible, or a
reason string naming the flag when it must refuse at exit 2. The rules, from
SPEC-WORK.md:1780 and the query's own grammar at :2296:
  - no --branch names --branch;
  - --branch closed or --branch root require --from and --to;
  - --branch open refuses --from/--to;
  - who, stale and handoffs refuse --branch closed and --branch root;
  - --order priority is exit 2 on every ask but ready."
  (cond
    ((null branch)
     "--branch is required; name open, closed or root")
    ((member branch '(:closed :root))
     (cond
       ((named-listing-ask-p ask)
        (format nil "~A is a fact of O alone; --branch ~A is refused" ask branch))
       ((or (null from) (null to))
        "--branch closed and --branch root require --from and --to")
       (t nil)))
    ((eq branch :open)
     (cond
       ((or from to) "--branch open refuses --from and --to")
       ((and order (eq order :priority) (not (eq ask :ready)))
        "--order priority requires the ready ask")
       (t nil)))
    (t (format nil "unknown --branch ~S" branch))))
