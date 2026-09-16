;;;; replays-l2400-1.lisp --- the pure models behind the eight replays named in
;;;; docs/SPEC-WORK.md lines 2400-3600 (batch 1 of #362).
;;;;
;;;; None of this touches `kernel.lisp`, `state.lisp` or the session: every
;;;; record and function here is resident-model data and pure decision code, the
;;;; parts a replay can assert without a live session. What still needs wiring
;;;; into the session/kernel is listed one line each in RESULT.md under "owed".

(in-package #:nova-work)

;;; ==================================================================
;;; axisless-history  (SPEC-WORK.md:3080-3105, 5714)
;;; ==================================================================
;;; A zero- or one-axis roadmap: ordered rows, a denominator that completion
;;; does not reduce, and retirement that records a scope movement while the node
;;; stands. Each mutation snaps the full row export, so a prior view
;;; reconstructs at its captured revision.

(defstruct (rrow (:conc-name rrow-)
                 (:constructor make-rrow (node evidence finished retired order)))
  node evidence finished retired order)

(defstruct (roadmap (:conc-name roadmap-)
                    (:constructor %make-roadmap ()))
  id revision rows history)

(defun %rows-export (rows)
  (mapcar (lambda (r)
            (list :node (rrow-node r) :evidence (rrow-evidence r)
                  :finished (rrow-finished r) :retired (rrow-retired r)
                  :order (rrow-order r)))
          rows))

(defun %record (rm)
  (push (cons (roadmap-revision rm) (%rows-export (roadmap-rows rm)))
        (roadmap-history rm)))

(defun make-roadmap (id)
  (let ((rm (%make-roadmap)))
    (setf (roadmap-id rm) id
          (roadmap-revision rm) 0
          (roadmap-rows rm) '()
          (roadmap-history rm) '())
    (%record rm)
    rm))

(defun roadmap-row (rm node)
  (find node (roadmap-rows rm) :key #'rrow-node :test #'string=))

(defun roadmap-denominator (rm)
  "The view-required denominator is the total ordered row count; completion
  never reduces it (SPEC-WORK.md:5714)."
  (length (roadmap-rows rm)))

(defun roadmap-export (rm)
  (%rows-export (roadmap-rows rm)))

(defun roadmap-view-at (rm rev)
  "Reconstruct the row set as of REV from the captured snapshots (newest first)."
  (dolist (entry (roadmap-history rm))
    (when (<= (car entry) rev)
      (return-from roadmap-view-at (copy-tree (cdr entry)))))
  '())

(defun roadmap-load (id rows-export)
  (let ((rm (%make-roadmap)))
    (setf (roadmap-id rm) id
          (roadmap-revision rm) (length rows-export)
          (roadmap-rows rm)
          (mapcar (lambda (e)
                    (make-rrow (getf e :node) (getf e :evidence)
                               (getf e :finished) (getf e :retired)
                               (getf e :order)))
                  rows-export)
          (roadmap-history rm) '())
    (%record rm)
    rm))

(defun roadmap-add-row (rm node &optional (evidence '()))
  (incf (roadmap-revision rm))
  (setf (roadmap-rows rm)
        (append (roadmap-rows rm)
                (list (make-rrow node evidence nil nil (length (roadmap-rows rm))))))
  (%record rm)
  rm)

(defun roadmap-finish-row (rm node)
  (let ((row (roadmap-row rm node)))
    (unless row (error 'unsupported-input :what (format nil "no such row ~A" node)))
    (setf (rrow-finished row) t))
  (incf (roadmap-revision rm))
  (%record rm)
  rm)

(defun roadmap-retire-row (rm node)
  "Retirement moves the roadmap's view-required set and keeps the node: no
  `node remove`, no cancel (SPEC-WORK.md:3089, 5714)."
  (let ((row (roadmap-row rm node)))
    (unless row (error 'unsupported-input :what (format nil "no such row ~A" node)))
    (setf (rrow-retired row) t))
  (incf (roadmap-revision rm))
  (%record rm)
  rm)

;;; ==================================================================
;;; a-root-id-grants-nothing  (SPEC-WORK.md:3121-3132, 5733)
;;; ==================================================================
;;; A stored permitted root grants no filesystem access by itself: file mode
;;; needs both the stored permission and a --render-root mapping; chat needs no
;;; mapping. Escaping, symlink and identity escapes refuse; the renderer lock is
;;; cooperative.

(defstruct (render-root (:conc-name render-root-)
                        (:constructor %make-render-root ()))
  id repo path permitted)

(defun make-permitted-root (id repo)
  (let ((r (%make-render-root)))
    (setf (render-root-id r) id
          (render-root-repo r) repo
          (render-root-path r) nil
          (render-root-permitted r) t)
    r))

(defun map-render-root (root path &optional repo)
  (setf (render-root-path root) path)
  (when repo (setf (render-root-repo root) repo))
  root)

(defun render-chat-permitted-p (root)
  (render-root-permitted root))

(defun render-file-permitted-p (root)
  (and (render-root-permitted root)
       (not (null (render-root-path root)))))

(defun target-inside-root-p (target root)
  (let ((path (render-root-path root)))
    (and path
         (not (member "" target :test #'string=))
         (not (member "." target :test #'string=))
         (not (member ".." target :test #'string=))
         (let ((n (length path)))
           (and (<= n (length target))
                (every #'string= (subseq target 0 n) path))))))

(defun target-symlink-ok-p (target symlinks)
  (not (some (lambda (seg) (member seg symlinks :test #'string=)) target)))

(defun target-repo-matches-p (repo root)
  (string= repo (render-root-repo root)))

(defstruct (render-lock (:conc-name render-lock-)
                        (:constructor make-render-lock ()))
  holder)

(defun render-lock-acquire (lock holder)
  (if (render-lock-holder lock)
      nil
      (progn (setf (render-lock-holder lock) holder) t)))

(defun render-lock-release (lock holder)
  (when (string= (render-lock-holder lock) holder)
    (setf (render-lock-holder lock) nil))
  t)

(defun render-lock-advisory-p (lock)
  (declare (ignore lock))
  "The per-target lock is advisory among cooperating renderers; an external
  editor can still race the replace (SPEC-WORK.md:3124-3125)."
  t)

;;; ==================================================================
;;; state-export-refuses-a-gap  (SPEC-WORK.md:3229-3254, 5759)
;;; ==================================================================
;;; The manifest-checking predicates: a gap, a changed digest, a dangling
;;; reference, a path escape, a symlink, an overrun, a corrupt form, a gone
;;; resolver observation, and the half-open closed-history range's omissions.

(defstruct (export-member (:conc-name em-)
                          (:constructor make-export-member (segments kind bytes sha256)))
  segments kind bytes sha256)

(defun export-member-path (m)
  (format nil "~{~A~^/~}" (em-segments m)))

(defun export-member-digest-ok-p (m)
  (string= (sha256-hex (em-bytes m)) (em-sha256 m)))

(defstruct (export-bundle (:conc-name eb-)
                          (:constructor make-export-bundle
                           (&key members required-members refs required-observations)))
  members required-members refs required-observations)

(defun export-missing-mandatory (b)
  (loop for req in (eb-required-members b)
        unless (find req (eb-members b) :key #'export-member-path :test #'string=)
          collect req))

(defun export-dangling-refs (b)
  (loop for (src . target) in (eb-refs b)
        unless (find target (eb-members b) :key #'export-member-path :test #'string=)
          collect target))

(defun export-member-path-ok-p (segments)
  (not (or (member "" segments :test #'string=)
           (member "." segments :test #'string=)
           (member ".." segments :test #'string=))))

(defun export-member-symlink-ok-p (segments symlinks)
  (not (some (lambda (s) (member s symlinks :test #'string=)) segments)))

(defun export-output-within-p (members max-bytes)
  (<= (loop for m in members sum (length (em-bytes m))) max-bytes))

(defun export-member-corrupt-p (text)
  (handler-case (progn (read-restricted text) nil)
    (restricted-data-violation () t)))

(defun export-proof-gap-reason (b)
  (loop for (id . status) in (eb-required-observations b)
        when (eq status :gone)
          do (return (format nil "proof gap: resolver observation ~A" id))
        finally (return nil)))

(defun closed-history-omissions (mode from to members)
  (ecase mode
    (:range
     (loop for m in members
           when (or (string< m from) (string>= m to)) collect m))
    (:all '())
    (:none members)))

;;; ==================================================================
;;; fenced-export-can-finish  (SPEC-WORK.md:3183-3198, 5782)
;;; ==================================================================
;;; An export is one long operation with an id and a terminal line; a fenced
;;; session reaches status/wait/cancel only through its own export id.

(defstruct (export-operation (:conc-name eo-)
                             (:constructor make-export-operation (id status)))
  id status publication)

(defun export-operation-status (op) (eo-status op))
(defun export-operation-publication (op) (eo-publication op))

(defun cancel-export (op)
  (setf (eo-status op) :cancelled
        (eo-publication op) :reconciled)
  op)

(defun fenced-operation-status (own-id op asked-id)
  (if (and (string= own-id asked-id)
           (string= (eo-id op) asked-id))
      (eo-status op)
      nil))

(defun fenced-allow-p (own-id kind arg)
  (case kind
    ((:operation-status :operation-wait :operation-cancel)
     (and (stringp arg) (string= own-id arg)))
    ((:operation-list :canonical-write) nil)
    (t nil)))

;;; ==================================================================
;;; four-capability-groups-and-three-fields  (SPEC-WORK.md:3368-3377, 5611)
;;; ==================================================================
;;; Four execution capability groups, each with a stable id, source,
;;; last-verified stamp, availability and constraints; declared support,
;;; verified runtime and free capacity kept as three fields, never one.

(defstruct (capability (:conc-name capability-)
                       (:constructor make-capability
                        (id group source
                         &key (last-verified "2026-09-14T00:00:00Z")
                              (availability :available)
                              (constraints '())
                              declared-support verified-runtime (free-capacity 0))))
  id group source last-verified availability constraints
  declared-support verified-runtime free-capacity)

;;; ==================================================================
;;; dispatch-ack-and-ownership-are-three  (SPEC-WORK.md:3379-3391, 5550)
;;; ==================================================================
;;; Dispatch intent, delivery, acknowledgement and accepted ownership are four
;;; distinct facts. An offer reserves only declared capacity; a timeout launches
;;; no duplicate while a worker runs; a return reconciles before new dispatch.

(defstruct (dispatch-state (:conc-name ds-)
                           (:constructor make-dispatch-state ()))
  requests deliveries acknowledgements accepted live-workers reconciled)

(defun dispatch-intent (ds request node)
  (push (cons request node) (ds-requests ds)))
(defun dispatch-deliver (ds request) (push request (ds-deliveries ds)))
(defun dispatch-ack (ds request) (push request (ds-acknowledgements ds)))
(defun dispatch-accept (ds request) (push request (ds-accepted ds)))
(defun dispatch-intent-p (ds request) (not (null (assoc request (ds-requests ds) :test #'string=))))
(defun dispatch-delivered-p (ds request) (not (null (member request (ds-deliveries ds) :test #'string=))))
(defun dispatch-acknowledged-p (ds request) (not (null (member request (ds-acknowledgements ds) :test #'string=))))
(defun dispatch-accepted-p (ds request) (not (null (member request (ds-accepted ds) :test #'string=))))
(defun dispatch-worker-live (ds node) (push node (ds-live-workers ds)))
(defun timeout-may-launch-p (ds node)
  (not (member node (ds-live-workers ds) :test #'string=)))
(defun dispatch-gate-open-p (ds) (ds-reconciled ds))
(defun dispatch-reconcile (ds) (setf (ds-reconciled ds) t))

(defun offer-reserve (declared requested)
  (min declared requested))

;;; ==================================================================
;;; a-retry-does-not-overwrite-its-attempt  (SPEC-WORK.md:3385-3391, 5553)
;;; ==================================================================
;;; A retry mints a NEW attempt; the old attempt keeps its own observed model
;;; and usage, the new one's observed model stays unknown, and a friend's usual
;;; model never stands as proof of the delegated executor.

(defstruct (attempt (:conc-name attempt-)
                    (:constructor make-attempt (id requested-model observed-model usage)))
  id requested-model observed-model usage)

(defstruct (attempt-log (:conc-name attempt-log-)
                        (:constructor make-attempt-log ()))
  attempts)

(defun attempt-log-add (log a)
  (push a (attempt-log-attempts log))
  a)

(defun attempt-log-count (log)
  (length (attempt-log-attempts log)))

(defun retry (log prev requested-model)
  (declare (ignore prev))
  (let ((a (make-attempt (format nil "at-~D" (1+ (attempt-log-count log)))
                         requested-model +absent+ 0)))
    (push a (attempt-log-attempts log))
    a))

;;; ==================================================================
;;; explicit-rest-is-not-pinged  (SPEC-WORK.md:3393-3407, 5556)
;;; ==================================================================
;;; A configured silence threshold triggers one bounded ping, never to a resting
;;; or wakeup-reserved friend; no answer marks `unconfirmed` (not sleep, not
;;; exhausted credit); a failed probe is unresolved delivery, not a failed
;;; friend.

(defstruct (availability (:conc-name availability-)
                         (:constructor %make-availability ()))
  (status :unknown) (reason +absent+) reserved-from-wakeups (ping-count 0))

(defun make-availability (status &key (reserved-from-wakeups nil))
  (let ((a (%make-availability)))
    (setf (availability-status a) status
          (availability-reason a) +absent+
          (availability-reserved-from-wakeups a) reserved-from-wakeups
          (availability-ping-count a) 0)
    a))

(defun ping-eligible-p (a)
  (and (not (eq (availability-status a) :resting))
       (not (availability-reserved-from-wakeups a))))

(defun ping-once (a)
  (unless (plusp (availability-ping-count a))
    (incf (availability-ping-count a)))
  (availability-ping-count a))

(defun record-no-answer (a)
  (setf (availability-status a) :unavailable
        (availability-reason a) :unconfirmed)
  a)

(defun probe-failure-verdict ()
  :unresolved-delivery)
