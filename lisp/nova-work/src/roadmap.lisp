;;;; roadmap.lisp --- the roadmap scope bookkeeping a `node move` advances.
;;;;
;;;; `move-updates-every-roadmap-scope` (docs/SPEC-WORK.md:5978): a row
;;;; referenced by two roadmaps outside both parent chains and one unrelated
;;;; roadmap. Both referencing scope revisions advance in the move's envelope,
;;;; the unrelated roadmap stays, a failed acceptance moves none, a render
;;;; captured before the move keeps its captured scope, and an intervening
;;;; mutation of an affected roadmap makes an undo conflict.
;;;;
;;;; A roadmap scope is the pure bookkeeping a `node move` must touch when a
;;;; referenced row's scope revision changes: its id, its current scope
;;;; revision, and the row ids it references. The move is one envelope -- it
;;;; either advances every referencing scope or, refused, writes none.

(in-package #:nova-work)

(defstruct (scope-roadmap (:constructor %make-scope-roadmap (id revision rows)))
  (id "" :read-only t :type string)
  (revision 0 :type integer)
  (rows '() :type list))

(defun make-scope-roadmap (&key id (revision 0) rows)
  "A roadmap view's scope bookkeeping: ID, its REVISION, and the ROWS it
references."
  (%make-scope-roadmap (or id "") (or revision 0) (copy-list (or rows '()))))

(defun scope-roadmap-references-p (scope row-id)
  "True when SCOPE references ROW-ID. A roadmap outside the row's parent
chains is still a referrer."
  (and (member row-id (scope-roadmap-rows scope) :test #'string=) t))

(defun scopes-on-move (scopes row-id &key (accept t))
  "The scopes a `node move` of ROW-ID updates, in one envelope. When ACCEPT is
true every scope referencing ROW-ID has its revision advanced by one and the
returned events name each; an unrelated scope is returned unchanged. When
ACCEPT is nil -- a refused move -- the original scopes are returned with an
empty envelope: nothing moves. Returns (values SCOPES EVENTS ACCEPTED)."
  (if (not accept)
      (values scopes '() nil)
      (let ((events '()) (updated '()))
        (dolist (scope scopes)
          (if (scope-roadmap-references-p scope row-id)
              (let ((next (copy-scope-roadmap scope)))
                (incf (scope-roadmap-revision next))
                (push (list :kind :scope :roadmap (scope-roadmap-id next)
                            :scope-revision (scope-roadmap-revision next)
                            :references row-id)
                      events)
                (push next updated))
              (push scope updated)))
        (values (nreverse updated) (nreverse events) t))))

(defun scope-capture (scopes)
  "A render captured now: the scopes at their current revisions. A later move
returns new scopes and never rewrites this capture."
  (copy-list scopes))

(defun scope-mutate (scope)
  "A later mutation of an affected roadmap's scope: its revision advances so
that an undo captured before it no longer matches."
  (let ((next (copy-scope-roadmap scope)))
    (incf (scope-roadmap-revision next))
    next))

(defun scopes-undo (scopes touched guards preimage)
  "Undo the move that produced SCOPES by restoring PREIMAGE, but only while
every touched roadmap still matches its GUARDS. Returns (values RESTORED nil)
on success; a stale guard names the moved-on roadmap and returns
(values nil line) with nothing restored."
  (let ((conflict
          (loop for id in touched
                for guard in guards
                for scope = (find id scopes :key #'scope-roadmap-id :test #'string=)
                when (or (null scope) (/= guard (scope-roadmap-revision scope)))
                  return (format nil "UNDO FAIL request-of=move: conflict ~A" id))))
    (if conflict
        (values nil conflict)
        (values (copy-list preimage) nil))))
