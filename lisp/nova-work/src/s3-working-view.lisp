;;;; s3-working-view.lisp --- W, the working view, materialised from the lease log.
;;;;
;;;; docs/SPEC-WORK.md:1682-1709 — `(working O)` is the items of O that hold a live
;;;; lease; W is a view and no verb writes it; it is materialised eagerly and never
;;;; rebuilt on a read, so a reader sees memberships and |W| with zero visits into O.
;;;;
;;;; This slice keeps the materialisation a record built from the lease log. The
;;;; wiring card is what re-builds it inside the accepted mutation envelope; here
;;;; the view is derived once and then read through constant-time lookups.

(in-package #:nova-work)

(defstruct (working-view (:constructor %make-working-view (ids table count watermark)))
  ids        ; node ids in lease-log order
  table      ; node id -> T (constant-time membership)
  count      ; |W|
  watermark) ; the lease-time watermark (the clock the materialisation was exact at)

(defun working-view-build (log &key now)
  "Materialise W from the lease log alone: no node of O is visited."
  (let ((ids '())
        (table (make-hash-table :test #'equal)))
    (dolist (e (lease-log-events log))
      (when (and (member (lease-event-kind e) '(:lease :handoff))
                 (eq e (active-lease log (lease-event-node e)))
                 (lease-live-p log (lease-event-node e) now)
                 (not (gethash (lease-event-node e) table)))
        (push (lease-event-node e) ids)
        (setf (gethash (lease-event-node e) table) t)))
    (%make-working-view (nreverse ids) table (hash-table-count table) now)))

;;; working-view-count is the struct accessor (the carried |W|).

(defun working-ids (view)
  (working-view-ids view))

(defun working-member-p (view node)
  (not (not (gethash node (working-view-table view)))))

(defun working-watermark (view)
  (working-view-watermark view))

(defun working-count-of (log &key now)
  "The convenience read for tests and the later wiring: |W| with zero O visits."
  (working-view-count (working-view-build log :now now)))
