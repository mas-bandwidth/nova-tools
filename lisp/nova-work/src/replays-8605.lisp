;;;; replays-8605.lisp --- leases, the working view and the retained roadmap.
;;;;
;;;; SPEC-WORK.md:1674-1689 -- "Settling ends the claim, because the work has
;;;; ended. The settle envelope carries a `:release` for a live lease on the
;;;; node ... a settled item reads `holder=unowned` in every answer while its
;;;; lease log stays whole for `handoffs`". And "`(working O)` is the items of O
;;;; that hold a live lease ... `take` and `release` are the only things that
;;;; change W ... `|W| <= |O|`".
;;;;
;;;; SPEC-WORK.md:1648-1664 -- "a roadmap is a durable named view ... Opening a
;;;; named roadmap is therefore an explicit scoped query, answered from that
;;;; retained record plus bounded indexed reads of its members' closed rows --
;;;; never a load of C, and never narrowed by the default `[now - 24h, now)`
;;;; window". The record is the node's own children; it survives the settle
;;;; cascade because the containment forest spans both branches.

(in-package #:nova-work)

;;; ---- leases --------------------------------------------------------------

(defun node-holder (state id)
  "The live lease's holder, or NIL when the item reads `holder=unowned`."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-holder n)))

(defun state-lease-log (state)
  "The lease log, oldest first, so a `handoffs --since` read sees the whole
transition log (SPEC-WORK.md:5055)."
  (reverse (wstate-lease-log state)))

(defun take-lease (kernel id by)
  "`take`: one live lease per node; a second `take` is refused and names the
holder (SPEC-WORK.md:135)."
  (let* ((state (kernel-state kernel))
         (n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (unless (eq :o (wnode-branch n))
      (error 'unsupported-input :what (format nil "~A is in C and takes no lease" id)))
    (when (wnode-holder n)
      (error 'unsupported-input
             :what (format nil "LEASE FAIL node=~A holder=~A: held" id (wnode-holder n))))
    (setf (wnode-holder n) by)
    by))

(defun release-lease (kernel id by)
  "`release`: a claim is ended by the one who made it, never a third name
reaching in (SPEC-WORK.md:1354-1357)."
  (let* ((state (kernel-state kernel))
         (n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (unless (equal by (wnode-holder n))
      (error 'unsupported-input
             :what (format nil "LEASE FAIL node=~A holder=~A live: held" id
                           (or (wnode-holder n) "unowned"))))
    (setf (wnode-holder n) nil)
    by))

(defun working-count (kernel)
  "|W|: the O items that hold a live lease. W is a view of O and never a third
branch, so this can never exceed `state-open-count`."
  (let ((state (kernel-state kernel))
        (n 0))
    (dolist (id (wstate-order state) n)
      (let ((node (%node-quiet state id)))
        (when (and (eq :o (wnode-branch node)) (wnode-holder node))
          (incf n))))))

(defun node-disposition (state id)
  "Read-time disposition: settled is `:done`, a leased open item is `:working`,
and an open item with no live lease is `:pending` (SPEC-WORK.md:5082)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (cond ((eq :c (wnode-branch n)) :done)
          ((wnode-holder n) :working)
          (t :pending))))

;;; ---- the retained roadmap view -------------------------------------------

(defun roadmap-members (state id)
  "A roadmap's rows: its direct members, whichever branch it and they are in.
The view record stays in the live snapshot whatever branch the roadmap is in
and however old its work is (SPEC-WORK.md:1648-1664)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (unless (eq :roadmap (wnode-type n))
      (error 'unsupported-input :what (format nil "~A is not a roadmap" id)))
    (copy-list (wnode-children n))))

(defun %subtree-ids (state id)
  "The retained containment subtree rooted at ID, by the forest edges that
survive the settle (settling detaches nothing). Bounded by the rows' own work,
never by O (SPEC-WORK.md:1653-1656)."
  (let ((out '()))
    (labels ((walk (cur)
               (push cur out)
               (dolist (child (wnode-children (%node-quiet state cur)))
                 (walk child))))
      (walk id))
    out))

(defun roadmap-open (kernel id &key window)
  "Open a named roadmap: its retained row set plus the closed rows of its
members and the work beneath them. The answer is never narrowed by WINDOW and
never walks O, because the default `[now - 24h, now)` window bounds a
closed-activity listing and not a named view (SPEC-WORK.md:1648-1664)."
  (declare (ignore window))
  (let* ((state (kernel-state kernel))
         (members (roadmap-members state id))
         (reachable (loop for member in members
                          append (%subtree-ids state member)))
         (rows (remove-if-not (lambda (r) (member (getf r :node) reachable :test #'equal))
                              (state-closed-rows state))))
    (list :id id :members members :rows rows)))
