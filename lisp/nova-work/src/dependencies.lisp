;;;; dependencies.lisp --- the needs/blocks edges (nova-tools #785).
;;;;
;;;; SPEC-WORK.md:850-886 -- `:deps` is a reference edge: needed, not owned,
;;;; not counted, and validated for existence and for dependency state. The
;;;; forward edge is a node's `:deps`; the reverse edge is its DEPENDENTS slot,
;;;; built once by rule 2 when the seed is published and updated by the same
;;;; write path that moves a need's branch. It is what `what does this block`
;;;; reads, so the answer is one bounded lookup and never a walk of O
;;;; (SPEC-WORK.md:709, the reverse-dependency index).
;;;;
;;;; SPEC-WORK.md:2110-2114 -- a node is ready only when every need is terminal
;;;; accepted, and a dependent whose need is open is not ready. The same gate
;;;; blocks the dependent's transition to doing (:1885) and the parent's
;;;; settlement to green (:1951, "A parent is green only when every required
;;;; child and every dependency gate is satisfied").

(in-package #:nova-work)

(defun node-dependents (state id)
  "The reverse-dependency index: the ids whose `:deps` name ID, in the order
they were seeded. One slot read -- SPEC-WORK.md:709, `what does this block` is
one bounded indexed access for the one id it needs, never a walk of the set."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (copy-list (wnode-dependents n))))

(defun %dependency-blocker (state id)
  "The first need of ID that is not terminal accepted, or NIL when every need is
settled. The dependency gate of SPEC-WORK.md:1885 and :2110: an unmet need
blocks the dependent, and the refusal names the blocker."
  (let ((n (%node-quiet state id)))
    (and n
         (find-if-not (lambda (dep) (%need-terminal-p state dep))
                      (wnode-deps n)))))

(defun %needs-settled-p (state id)
  "True when every need of ID is terminal accepted."
  (let ((n (%node-quiet state id)))
    (and n (every (lambda (dep) (%need-terminal-p state dep))
                  (wnode-deps n)))))
