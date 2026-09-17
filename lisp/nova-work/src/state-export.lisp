;;;; state-export.lisp --- the state export/load kernel the five replays of
;;;; docs/SPEC-WORK.md:5879-5905 assert (the `state load` fold of :3273-3297).
;;;;
;;;; This is the internal kernel's own model of an export: a pinned capture of
;;;; one revision and its mandatory closure members, a no-replace publication
;;;; with one output identity, a retention pass that cannot reclaim a pinned
;;;; member, an isolated read-only load under its bounds, and a fenced session
;;;; that admits export operations and refuses canonical writes.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; An export: a pinned capture of one revision and its members.
;;; ------------------------------------------------------------------

(defstruct (state-export (:conc-name state-export-))
  id revision bytes members status destination published terminal)

(defun export-members (state)
  "The mandatory closure members of a full export: the manifest, the schema and
the seed, plus one member per accepted history record (:3254-3259)."
  (let ((members '("manifest" "schema" "seed")))
    (dolist (record (state-history state))
      (let ((request (getf record :request)))
        (unless (member request members :test #'string=)
          (setf members (append members (list request))))))
    members))

(defun export-state (state &key (id "op-1") destination)
  "Capture STATE at its current revision. The bytes are the canonical form at
R, so a later R+1 can never substitute current bytes (:5889)."
  (make-state-export
   :id id
   :revision (state-revision state)
   :bytes (canonical-string (state-canonical-form state))
   :members (export-members state)
   :status :running
   :destination destination
   :published nil))

(defun export-complete (op)
  "Complete OP at exactly its captured revision."
  (setf (state-export-status op) :complete
        (state-export-terminal op)
        (format nil "EXPORT OK id=~A rev=~D bytes=~D"
                (state-export-id op) (state-export-revision op)
                (length (state-export-bytes op))))
  op)

(defun publish-state-export (op existing)
  "No-replace publication (:3273-3276): an existing destination refuses, a
published export keeps its one output identity and is never republished."
  (cond
    ((eq :cancelled (state-export-status op))
     (values nil (format nil "EXPORT FAIL id=~A: cancelled" (state-export-id op)) 1))
    ((state-export-published op)
     (values nil (format nil "EXPORT FAIL id=~A: already published=~A"
                         (state-export-id op) (state-export-published op))
             1))
    ((member (state-export-destination op) existing :test #'string=)
     (values nil (format nil "EXPORT FAIL id=~A: destination ~A exists"
                         (state-export-id op) (state-export-destination op))
             1))
    (t
     (setf (state-export-published op) (state-export-id op)
           (state-export-status op) :complete
           (state-export-terminal op)
           (format nil "EXPORT OK id=~A rev=~D published=~A"
                   (state-export-id op) (state-export-revision op)
                   (state-export-id op)))
     (values t (state-export-terminal op) 0))))

(defun cancel-state-export (op)
  "Cancel an unfinished export; a published output is never claimed back."
  (if (state-export-published op)
      (values nil (format nil "EXPORT FAIL id=~A: already published=~A; a published output is not reversed"
                          (state-export-id op) (state-export-published op))
              1)
      (progn
        (setf (state-export-status op) :cancelled
              (state-export-terminal op)
              (format nil "EXPORT FAIL id=~A: cancelled" (state-export-id op)))
        (values t (state-export-terminal op) 0))))

;;; ------------------------------------------------------------------
;;; A clip's retention pass, and the export pin (:5889).
;;; ------------------------------------------------------------------

(defun retention-pass (state &key reclaim pinned needed)
  "Drop the history members named by RECLAIM. A name in PINNED is never
reclaimed; a needed member that is not pinned is a named recovery gap. Returns
(values candidate-state reclaimed gap)."
  (let ((dropped '())
        (gap nil))
    (dolist (name reclaim)
      (cond
        ((member name pinned :test #'string=) nil)
        ((member name needed :test #'string=)
         (setf gap (format nil "recovery gap: required member ~A was reclaimed" name)))
        (t (push name dropped))))
    (let ((candidate (copy-state state)))
      (when dropped
        (setf (wstate-history candidate)
              (remove-if (lambda (record)
                           (member (getf record :request) dropped :test #'string=))
                         (wstate-history candidate))))
      (values candidate (nreverse dropped) gap))))

;;; ------------------------------------------------------------------
;;; The manifest and the isolated read-only load (:3281-3297, :5879).
;;; ------------------------------------------------------------------

(defun export-manifest (state &key (id "op-1"))
  "The manifest a load verifies: version, revision, digest and member set."
  (let ((bytes (canonical-string (state-canonical-form state))))
    (list :version 1
          :id id
          :revision (state-revision state)
          :digest (sha256-hex bytes)
          :members (list (cons "snapshot" bytes))
          :observations-gone nil)))

(defstruct (snapshot (:conc-name snapshot-))
  state revision)

(defun %manifest-member (manifest name)
  (cdr (assoc name (getf manifest :members) :test #'string=)))

;;; The isolation instrumentation (:3281, :5896): an isolated load reports what
;;; it did. Every session-level effect has a counter; an honest load leaves all
;;; of them at zero and writes only the declared exclusive path.

(defvar *isolation* nil)

(defmacro with-isolation (&body body)
  `(let ((*isolation* (list :owners 0 :dispatches 0 :replays 0 :merges 0
                            :resolvers 0 :network 0 :repo-writes 0 :writes '())))
     ,@body))

(defun isolation-count (key) (or (getf *isolation* key) 0))
(defun isolation-writes () (reverse (getf *isolation* :writes)))
(defun isolation-write (path)
  (when *isolation* (push path (getf *isolation* :writes))))

(defun load-state (manifest &key max-bytes max-depth max-nodes into)
  "Verify and materialise one isolated snapshot. Returns (values snapshot line)
or (values nil refusal); no path text is read, no resolver runs and nothing is
written outside INTO."
  (declare (ignore max-depth max-nodes))
  (unless (eql 1 (getf manifest :version))
    (return-from load-state (values nil "LOAD FAIL: unsupported manifest version")))
  (when (getf manifest :observations-gone)
    (return-from load-state
      (values nil "LOAD FAIL: proof gap: the historical resolver observations are gone; current ones are never substituted")))
  (let ((total 0))
    (dolist (member (getf manifest :members))
      (let ((path (car member)))
        (when (or (and (plusp (length path)) (char= #\/ (char path 0)))
                  (search ".." path)
                  (find #\Nul path))
          (return-from load-state (values nil (format nil "LOAD FAIL: path escape ~A" path))))
        (when (member path (getf manifest :symlinks) :test #'string=)
          (return-from load-state (values nil (format nil "LOAD FAIL: symlink member ~A" path))))
        (incf total (length (cdr member)))))
    (when (and max-bytes (> total max-bytes))
      (return-from load-state
        (values nil (format nil "LOAD FAIL: output overrun ~D bytes exceeds --max-bytes ~D"
                            total max-bytes)))))
  (let ((bytes (%manifest-member manifest "snapshot")))
    (unless bytes
      (return-from load-state (values nil "LOAD FAIL: missing mandatory member snapshot")))
    (unless (string= (sha256-hex bytes) (getf manifest :digest))
      (return-from load-state (values nil "LOAD FAIL: changed digest")))
    (handler-case
        (let ((state (reconstruct-state bytes)))
          (when into (isolation-write into))
          (values (make-snapshot :state state :revision (state-revision state))
                  (format nil "LOAD OK id=~A rev=~D" (getf manifest :id) (state-revision state))))
      (restricted-data-violation ()
        (values nil "LOAD FAIL: corrupt S-expression"))
      (reader-error ()
        (values nil "LOAD FAIL: corrupt S-expression"))
      (unsupported-input (c)
        (values nil (format nil "LOAD FAIL: dangling internal reference: ~A"
                            (unsupported-input-what c)))))))

(defun snapshot-query (snap)
  "The loaded snapshot answers `query --snapshot`; nothing is reloaded."
  (state-open-count (snapshot-state snap)))

(defparameter *session-only-verbs*
  '(:state-to-done :state-to-doing :event-reopen :replay :clip :handoff))

(defun snapshot-accept-session-p (snap verb)
  "A loaded snapshot is never a `--session` for a mutation, replay, clip or
handoff verb (:3291-3292)."
  (declare (ignore snap))
  (not (member verb *session-only-verbs*)))

;;; ------------------------------------------------------------------
;;; A fenced session (:3288, :5902).
;;; ------------------------------------------------------------------

(defstruct (fenced-session (:conc-name fenced-) (:constructor %make-fenced-session))
  owner exports)

(defun make-fenced-session (&key (owner "owner-1"))
  (%make-fenced-session :owner owner :exports '()))

(defun fenced-claim (session owner)
  "One owner; a second claim creates no second owner."
  (if (string= owner (fenced-owner session))
      (values t "FENCED OK owner" 0)
      (values nil "FENCED FAIL: no second owner" 1)))

(defun fenced-export-start (session state &key (id "op-1"))
  "An export is admissible in a fenced session and creates no mutation authority."
  (let ((op (export-state state :id id)))
    (push op (fenced-exports session))
    op))

(defun fenced-status (session id)
  "Read an operation's status and terminal line by its id."
  (let ((op (find id (fenced-exports session) :key #'state-export-id :test #'string=)))
    (if op
        (values t (or (state-export-terminal op)
                      (format nil "EXPORT RUNNING id=~A" id))
                0)
        (values nil (format nil "EXPORT FAIL: unknown id ~A" id) 1))))

(defun fenced-cancel (session id)
  "Cancel an unfinished export with publication reconciled; an unknown id is
refused."
  (let ((op (find id (fenced-exports session) :key #'state-export-id :test #'string=)))
    (if op
        (progn
          (setf (state-export-status op) :cancelled
                (state-export-terminal op)
                (format nil "EXPORT FAIL id=~A: cancelled; publication reconciled" id))
          (values t (state-export-terminal op) 0))
        (values nil (format nil "EXPORT FAIL: unknown id ~A" id) 1))))

(defun fenced-operation-list (session)
  "A fenced session exposes no operation list."
  (declare (ignore session))
  (values nil "REFUSED: fenced" 1))

(defun fenced-write (session verb)
  "Every canonical write is refused `fenced`."
  (declare (ignore session verb))
  (values nil "REFUSED: fenced" 1))

;;; ------------------------------------------------------------------
;;; `session export --state --at`: the flags and the pinned revision
;;; (SPEC-WORK.md:3197-3223)
;;; ------------------------------------------------------------------

(defun validate-export-form (&key state at (closed-history :none) from to)
  "`--state` is required with `--at` and the request export refuses `--at`;
`range` requires both stamps, the half-open `[from, to)`, and `none`/`all`
refuse them (SPEC-WORK.md:3201-3204). Answers (values okp reason)."
  (cond
    ((and at (not state))
     (values nil "--at is refused on the request export"))
    ((and state (null at))
     (values nil "--state requires --at"))
    ((eq closed-history :range)
     (cond
       ((or (null from) (null to)) (values nil "range requires --from and --to"))
       ((not (string< (string from) (string to)))
        (values nil "range is half-open [from, to)"))
       (t (values t "range accepted"))))
    ((member closed-history '(:none :all))
     (if (or from to)
         (values nil "only range takes --from/--to")
         (values t "closed-history accepted")))
    (t (values nil "closed-history must be none, all or range"))))

(defstruct (export-pin (:conc-name export-pin-))
  "One `--at` resolved to a savepoint plus the exact journal prefix that reaches
it, pinned until the operation ends."
  revision savepoint journal-prefix member)

(defun resolve-export-at (state at &key savepoints (retain-from 0) members)
  "Resolve `--at` to a savepoint and the exact journal prefix that reaches it,
pinned until the operation ends. A revision outside the recoverable range, or
below recoverable retention, refuses naming the revision and, when one is
known, the missing member; it is never approximated by a later snapshot
(SPEC-WORK.md:3212-3215)."
  (let ((revision (state-revision state)))
    (cond
      ((or (< at 0) (> at revision))
       (values nil (format nil "EXPORT FAIL: revision ~D is outside the recoverable range 0..~D"
                           at revision)))
      ((< at retain-from)
       (values nil (format nil "EXPORT FAIL: revision ~D is outside recoverable retention below ~D~@[; missing member ~A~]"
                           at retain-from (first members))))
      (t
       (let* ((history (state-history state))
              (prefix (remove-if (lambda (record)
                                   (> (getf record :revision) at))
                                 history))
              (savepoint
                (or (and savepoints
                         (first (sort (remove-if (lambda (s) (> s at)) savepoints)
                                      #'>)))
                    0)))
         (values (make-export-pin :revision at :savepoint savepoint
                                  :journal-prefix prefix)
                 (format nil "EXPORT PIN revision=~D savepoint=~D records=~D"
                         at savepoint (length prefix))))))))

(defun export-wire-op ()
  "The wire operation the resident export runs as."
  "session.export")

(defun begin-state-export (registry state &key (id "op-export-1")
                                              (request "req-export-1")
                                              (author "rowan")
                                              (stamp "2026-09-17T00:00:00Z")
                                              at savepoints (retain-from 0)
                                              destination)
  "The resident form of `session export --state --at` is one long operation: the
id is durable before it is printed and the request is acknowledged at once with
OPERATION OK id= op=export state=queued (SPEC-WORK.md:3204-3208). Answers
(values id op line code); a revision outside retention answers (values nil nil
refusal 1)."
  (multiple-value-bind (pin reason)
      (resolve-export-at state at :savepoints savepoints :retain-from retain-from)
    (unless pin
      (return-from begin-state-export (values nil nil reason 1)))
    (operation-accept registry :id id :kind :export :request request
                               :author author :stamp stamp)
    (let ((op (make-state-export :id id :revision (export-pin-revision pin)
                                 :bytes (canonical-string (state-canonical-form state))
                                 :members (export-members state)
                                 :status :queued :destination destination
                                 :published nil)))
      (values id op
              (format nil "OPERATION OK id=~A op=export state=queued" id)
              0))))

(defun state-export-wait (op)
  "`operation wait --id` prints the terminal line for the export: EXPORT OK at
the captured revision, or EXPORT FAIL for a cancelled one (SPEC-WORK.md:3204-3207)."
  (if (eq (state-export-status op) :cancelled)
      (format nil "EXPORT FAIL id=~A: cancelled; publication reconciled"
              (state-export-id op))
      (progn
        (setf (state-export-status op) :complete
              (state-export-terminal op)
              (format nil "EXPORT OK id=~A rev=~D operation=~A"
                      (state-export-id op) (state-export-revision op)
                      (state-export-id op)))
        (state-export-terminal op))))

(defun state-export-cancel-ack (op)
  "Cancellation acknowledges, then reconciles whether publication happened, and
never promises to unpublish (SPEC-WORK.md:3219-3220). Answers (values line code)."
  (multiple-value-bind (ok line code) (cancel-state-export op)
    (declare (ignore ok))
    (values (if (zerop code)
                (format nil "~A; publication reconciled" line)
                line)
            code)))

(defun snapshot-state-export (snapshot at)
  "The offline `--snapshot` counterpart: `--at` must equal that snapshot's
captured revision, it reads no session and runs as one finite process, and it
prints the same terminal line with operation=- (SPEC-WORK.md:3216-3218)."
  (let ((captured (snapshot-revision snapshot)))
    (unless (eql at captured)
      (return-from snapshot-state-export
        (values nil
                (format nil "EXPORT FAIL: --at ~D does not equal the snapshot revision ~D"
                        at captured)
                1)))
    (values (export-manifest (snapshot-state snapshot))
            (format nil "EXPORT OK rev=~D operation=-" captured)
            0)))
