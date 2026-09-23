;;;; operation-records.lisp --- the durable accept record of a long operation,
;;;; its status, its cancellation and the recovery reconciliation.
;;;;
;;;; docs/SPEC-WORK.md:2725-2760. "A long operation -- a source capture, an
;;;; import staging, an export, a clip's transport -- returns an operation id
;;;; and a state at once" (:2726-2728), and "The id is durable before it is
;;;; printed": "the engine draws it, appends the accept record -- the id, the
;;;; operation kind, the request id, the author and the stamp -- to the local
;;;; recovery journal, waits for that journal to be durable, and only then
;;;; replies, so a crash between accepting the work and acknowledging it can
;;;; never leave a caller holding an id the restart never heard of, and the
;;;; recovery reconciliation below has an id for every operation a caller was
;;;; ever told about" (:2728-2733). "An id no journal holds has a line of its
;;;; own: `OPERATION FAIL id=<id> op=- state=-: no such operation`, exit 2,
;;;; never an invented `queued`" (:2733-2735). A cancellation "is a request with
;;;; its own acknowledgement and its own final disposition -- so it takes
;;;; <write flags> like every other request, carrying its --as and its own
;;;; --request id, deduplicated by the same predicate, and a cancel replayed
;;;; twice cancels once -- and it can neither erase an accepted mutation nor
;;;; undo an external effect that may already have happened" (:2740-2744). And
;;;; "recovery reconciles interrupted operation ids and their external outcomes
;;;; before anything is retried" (:2759-2760).
;;;;
;;;; The vocabulary is not invented here: the kinds, the states and the three
;;;; external outcomes are the output grammar's at :5978-5982 --
;;;; `op=<capture|stage|export|clip|execution>`,
;;;; `state=<queued|running|done|cancelling|cancelled|failed>` and
;;;; `external=<known|uncertain|none>`.
;;;;
;;;; What is new here over src/operations.lisp:1081 `reconcile-operations` --
;;;; the pure model that walks a session struct a replay built and sets its
;;;; pending operations to `:reconciled` -- is that every record is the kernel
;;;; journal's. The accept record is written and made durable before the verb
;;;; answers, the status reads the journal and answers `no such operation` for
;;;; an id it does not hold, and the reconciliation is what a brand-new kernel
;;;; over the same journal reports. Nothing here is resident: an image that
;;;; never saw the accept still answers for it.
;;;;
;;;; One reading, stated rather than buried: a reconciliation records that the
;;;; restart accounted for the id, and does not move its state. The operation's
;;;; own state is what the engine last wrote; the restart cannot know whether
;;;; the work finished, so it marks the external outcome `uncertain` unless the
;;;; owning engine had already recorded a `known` one, and claims nothing else.
;;;; `none` is only ever the owning engine's word, never a restart's: the accept
;;;; record is written before the work runs, so its `none` says "nothing
;;;; external yet", which a crash turns into "cannot say". A second restart does
;;;; not report it again, which is what makes "before anything is retried"
;;;; checkable.

(in-package #:nova-work)

(defparameter *operation-kinds* '(:capture :stage :export :clip :execution)
  "The operation kinds of the output grammar at SPEC-WORK.md:5978.")

(defparameter *operation-states*
  '(:queued :running :done :cancelling :cancelled :failed)
  "The operation states of the output grammar at SPEC-WORK.md:5978.")

(defparameter *operation-externals* '(:known :uncertain :none)
  "The three external outcomes of the `OPERATION ROW` line at
SPEC-WORK.md:5979. `none` is an operation with no external effect to lose;
`uncertain` is one a restart cannot speak for.")

(defparameter *operation-terminal-states* '(:done :cancelled :failed)
  "The states a restart does not call interrupted.")

;;; ------------------------------------------------------------------
;;; The durable record.
;;; ------------------------------------------------------------------

(defun %operation-line (&key id kind request author stamp state external
                             (reconciled nil) (result nil))
  "One journal line per operation record. `result=` is last and carries the
canonical s-expression, so the fielded head stays readable by the same `%field`
reader src/control.lisp:115 uses."
  (format nil "OPERATION id=~A op=~A request=~A by=~A at=~A state=~A external=~A reconciled=~A result=~A"
          id (string-downcase (symbol-name kind)) request author stamp
          (string-downcase (symbol-name state))
          (string-downcase (symbol-name external))
          (if reconciled "yes" "no")
          (if result (canonical-string result) "-")))

(defun %parse-operation-line (line)
  "The plist an `OPERATION` journal line carries, or NIL when LINE is not one."
  (let ((head-marker " result="))
    (when (and (stringp line)
               (>= (length line) 10)
               (string= "OPERATION " line :end2 10))
      (let ((marker (search head-marker line)))
        (when marker
          (let ((head (subseq line 0 marker))
                (body (subseq line (+ marker (length head-marker)))))
            (list :id (%field head "id")
                  :kind (intern (string-upcase (%field head "op")) :keyword)
                  :request (%field head "request")
                  :author (%field head "by")
                  :stamp (%field head "at")
                  :state (intern (string-upcase (%field head "state")) :keyword)
                  :external (intern (string-upcase (%field head "external")) :keyword)
                  :reconciled (equal "yes" (%field head "reconciled"))
                  :result (if (equal "-" body) nil (read-restricted body)))))))))

;;; ------------------------------------------------------------------
;;; The journal-backed index. SPEC-WORK.md:2735-2737, :2761.
;;; ------------------------------------------------------------------
;;;
;;; "No unbounded scan ... may hold the mutation loop: it is paginated"
;;; (:2761), and `operation list` is "bounded and capped like every other
;;; listing" (:2735-2737). A status, a list page or a cancel therefore never
;;; walks the journal: they read an index of the journal's own records.
;;;
;;; The index holds no record. It holds, per operation id, the journal request
;;; keys of that id's records, and the ids in the order they were accepted; every
;;; answer is still read from the journal by `journal-lookup` on one of those
;;; keys, so an id the journal no longer holds answers `no such operation` as
;;; before. The index is the journal's, not the image's: it is keyed by the
;;; journal handle, built from that journal's own accepted order, and folded
;;; forward one entry per new journal record (each record is visited once, ever).
;;; A brand-new handle over the same file builds it from the file's order, which
;;; `open-file-journal` has already read frame by frame. It is bounded by what the
;;; journal holds, which is bounded by the journal's capacity.

(defstruct (operation-index (:constructor %make-operation-index))
  (head nil)
  (ids (make-array 16 :adjustable t :fill-pointer 0))
  (keys (make-hash-table :test #'equal)))

(defvar *operation-indexes*
  (make-hash-table :test #'eq #+sbcl :weakness #+sbcl :key
                              #+sbcl :synchronized #+sbcl t)
  "Journal handle -> its `operation-index`. Weak on the handle: the index lives
exactly as long as the journal it indexes.")

(defun %lookup-operation-record (journal key)
  "The operation record the journal holds under request KEY, or NIL."
  (multiple-value-bind (found digest line) (journal-lookup journal key)
    (declare (ignore digest))
    (and (eq found t) (%parse-operation-line line))))

(defun %operation-index (kernel)
  "The kernel journal's operation index, folded forward over every record the
journal accepted since the last fold -- those records alone. When the journal's
accepted order no longer contains the last folded entry (the bounded fake
evicted and rewrote its order), the index is rebuilt from the order it holds.
An entry the journal cannot read right now commits nothing: the next call folds
it again."
  (let* ((journal (kernel-journal kernel))
         (index (or (gethash journal *operation-indexes*)
                    (setf (gethash journal *operation-indexes*) (%make-operation-index))))
         (order (journal-order-slot journal))
         (mark (operation-index-head index)))
    (when (eq order mark)
      (return-from %operation-index index))
    ;; The order is newest first: walk it back to the last folded entry.
    (let ((new '()) (tail order))
      (loop until (or (null tail) (eq tail mark))
            do (push (car tail) new)
               (setf tail (cdr tail)))
      (when (and mark (null tail))
        ;; The mark is gone: NEW is the journal's whole order. Rebuild.
        (setf index (%make-operation-index)))
      (let ((folded '()))
        (dolist (key new)
          (multiple-value-bind (found digest line) (journal-lookup journal key)
            (declare (ignore digest))
            (unless (eq found t)
              (return-from %operation-index
                (or (gethash journal *operation-indexes*) index)))
            (let ((record (%parse-operation-line line)))
              (when record (push (cons (getf record :id) key) folded)))))
        (dolist (entry (nreverse folded))
          (destructuring-bind (id . key) entry
            (unless (nth-value 1 (gethash id (operation-index-keys index)))
              (vector-push-extend id (operation-index-ids index)))
            (push key (gethash id (operation-index-keys index)))))
        (setf (operation-index-head index) order
              (gethash journal *operation-indexes*) index)
        index))))

(defun kernel-operation-records (kernel &optional id)
  "Every operation record the kernel's journal holds, oldest first; those of ID
alone when ID is given. This is the whole store: nothing about an operation
lives in the image (SPEC-WORK.md:2728-2733). With ID it reads that id's records
only, through the index; without ID it is the whole-journal audit walk, which no
verb calls."
  (if id
      (let ((journal (kernel-journal kernel))
            (out '()))
        (dolist (key (gethash id (operation-index-keys (%operation-index kernel))) out)
          (let ((record (%lookup-operation-record journal key)))
            (when (and record (equal id (getf record :id)))
              (push record out)))))
      (let ((out '()))
        (dolist (request (journal-order (kernel-journal kernel)))
          (let ((record (%lookup-operation-record (kernel-journal kernel) request)))
            (when record (push record out))))
        (nreverse out))))

(defun kernel-operation-record (kernel id)
  "The latest record for ID, or NIL when the journal holds no id like it. One
journal lookup: the index names the key of ID's latest record."
  (let ((key (first (gethash id (operation-index-keys (%operation-index kernel))))))
    (and key
         (let ((record (%lookup-operation-record (kernel-journal kernel) key)))
           (and record (equal id (getf record :id)) record)))))

(defun kernel-operation-ids (kernel)
  "Every operation id the journal holds, in the order they were accepted."
  (coerce (operation-index-ids (%operation-index kernel)) 'list))

(defun kernel-operation-result (kernel id)
  "The result a completed operation retained, retrievable by its id afterwards
(SPEC-WORK.md:2739-2740)."
  (let ((record (find-if (lambda (r) (getf r :result))
                         (reverse (kernel-operation-records kernel id)))))
    (and record (getf record :result))))

;;; ------------------------------------------------------------------
;;; Writing one record, durably, before the verb answers.
;;; ------------------------------------------------------------------

(defun %operation-write (kernel rid line what)
  "Accept then record LINE under the journal request id RID. Answer (values T
NIL) or (values NIL REFUSAL-LINE). WHAT names the verb in a refusal."
  (let ((digest (sha256-hex line)))
    (multiple-value-bind (accepted reason)
        (journal-accept (kernel-journal kernel)
                        (list :request rid :digest digest :line line :rev 0 :events '()))
      (unless accepted
        (return-from %operation-write
          (values nil (format nil "OPERATION FAIL ~A: ~A" what reason)))))
    (journal-record (kernel-journal kernel) rid digest line 0)
    (values t nil)))

(defun %no-such-operation (id)
  "The line SPEC-WORK.md:2734 fixes for an id no journal holds. It names no
kind and no state, because the engine has neither."
  (format nil "OPERATION FAIL id=~A op=- state=-: no such operation" id))

;;; ------------------------------------------------------------------
;;; The verbs.
;;; ------------------------------------------------------------------

(defun kernel-operation-accept (kernel &key id kind request author stamp)
  "Draw an operation id and make it durable before answering. Answer (values
OK-P LINE EXIT-CODE RECORD).

The accept record carries exactly what SPEC-WORK.md:2729-2731 names -- the id,
the kind, the request id, the author and the stamp -- and the reply comes after
the journal has taken it, never before. A retry of the same request answers the
original reply and draws no second id; the same request id under a different
payload is a conflict."
  (flet ((fail (fmt &rest args)
           (return-from kernel-operation-accept
             (values nil (format nil "OPERATION FAIL id=~A op=~A state=-: ~A"
                                 (or id "-")
                                 (if kind (string-downcase (princ-to-string kind)) "-")
                                 (apply #'format nil fmt args))
                     2 nil))))
    (unless id (fail "no operation id"))
    (unless request (fail "no request id"))
    (unless (member kind *operation-kinds*)
      (fail "op=~A is not an operation kind"
            (if kind (string-downcase (princ-to-string kind)) "-")))
    (let* ((author (or author "rowan"))
           (stamp (or stamp "2026-09-19T00:00:00Z"))
           (line (%operation-line :id id :kind kind :request request :author author
                                  :stamp stamp :state :queued :external :none))
           (digest (sha256-hex line)))
      ;; Dedup on the caller's request id, before anything is written.
      (multiple-value-bind (found recorded-digest) (journal-lookup (kernel-journal kernel) request)
        (cond
          ((eq found :unavailable)
           (fail "dedup unavailable"))
          (found
           (return-from kernel-operation-accept
             (if (equal digest recorded-digest)
                 (values t (format nil "OPERATION OK id=~A op=~A state=queued"
                                   id (string-downcase (symbol-name kind)))
                         0 (kernel-operation-record kernel id))
                 (values nil (format nil "OPERATION FAIL id=~A op=~A state=-: request=~A reused with a different payload"
                                     id (string-downcase (symbol-name kind)) request)
                         2 nil))))))
      ;; Durable before it is printed.
      (multiple-value-bind (written refusal)
          (%operation-write kernel request line
                            (format nil "id=~A op=~A state=-" id
                                    (string-downcase (symbol-name kind))))
        (unless written
          (return-from kernel-operation-accept (values nil refusal 2 nil))))
      (values t (format nil "OPERATION OK id=~A op=~A state=queued"
                        id (string-downcase (symbol-name kind)))
              0 (kernel-operation-record kernel id)))))

(defun kernel-operation-status (kernel &key id)
  "`operation status --id <id>`. An id the journal does not hold answers the
line of SPEC-WORK.md:2734 at exit 2, never an invented state."
  (let ((record (kernel-operation-record kernel id)))
    (unless record
      (return-from kernel-operation-status (values nil (%no-such-operation id) 2 nil)))
    (values t
            (format nil "OPERATION OK id=~A op=~A state=~A started=~A external=~A"
                    id
                    (string-downcase (symbol-name (getf record :kind)))
                    (string-downcase (symbol-name (getf record :state)))
                    (getf record :stamp)
                    (string-downcase (symbol-name (getf record :external))))
            0 record)))

(defun kernel-operation-list (kernel &key (max 20))
  "`operation list`, bounded and capped like every other listing
(SPEC-WORK.md:2735-2737). One `OPERATION ROW` per id (grammar :5979). It visits
the first MAX ids of the index and reads one journal record for each: the work
is bounded by --max, never by the journal's length (:2761)."
  (let* ((ids (operation-index-ids (%operation-index kernel)))
         (rows (loop for i below (min (max 0 max) (length ids))
                     for id = (aref ids i)
                     for record = (kernel-operation-record kernel id)
                     when record
                       collect (format nil "OPERATION ROW id=~A op=~A state=~A started=~A external=~A"
                                       id
                                       (string-downcase (symbol-name (getf record :kind)))
                                       (string-downcase (symbol-name (getf record :state)))
                                       (getf record :stamp)
                                       (string-downcase (symbol-name (getf record :external)))))))
    (values t
            (format nil "OPERATION OK op=- state=- shown=~D" (length rows))
            0
            rows)))

(defun kernel-operation-complete (kernel &key id result (external :known))
  "The owning engine admits a validated result for ID. The record is durable;
the result is retrievable by its id afterwards (SPEC-WORK.md:2739-2740)."
  (let ((record (kernel-operation-record kernel id)))
    (unless record
      (return-from kernel-operation-complete (values nil (%no-such-operation id) 2 nil)))
    (let ((kind (getf record :kind)))
      (multiple-value-bind (written refusal)
          (%operation-write kernel (format nil "~A/done" id)
                            (%operation-line :id id :kind kind
                                             :request (getf record :request)
                                             :author (getf record :author)
                                             :stamp (getf record :stamp)
                                             :state :done :external external
                                             :result result)
                            (format nil "id=~A op=~A state=done" id
                                    (string-downcase (symbol-name kind))))
        (unless written
          (return-from kernel-operation-complete (values nil refusal 2 nil))))
      (values t (format nil "OPERATION OK id=~A op=~A state=done"
                        id (string-downcase (symbol-name kind)))
              0 (kernel-operation-record kernel id)))))


(defun kernel-operation-cancel (kernel &key id request author stamp)
  "`operation cancel --id <id>`: a request with its own acknowledgement and its
own final disposition, deduplicated by the same predicate as every other
request, so a cancel replayed twice cancels once. It erases no accepted
mutation and undoes no external effect (SPEC-WORK.md:2740-2744): it writes one
operation record and touches neither the work tree, its counters nor its
revision."
  (let ((record (kernel-operation-record kernel id)))
    (unless record
      (return-from kernel-operation-cancel (values nil (%no-such-operation id) 2 nil)))
    (unless request
      (return-from kernel-operation-cancel
        (values nil (format nil "OPERATION FAIL id=~A op=~A state=~A: no request id"
                            id (string-downcase (symbol-name (getf record :kind)))
                            (string-downcase (symbol-name (getf record :state))))
                2 nil)))
    (let* ((kind (getf record :kind))
           (line (%operation-line :id id :kind kind :request request
                                  :author (or author (getf record :author))
                                  :stamp (or stamp (getf record :stamp))
                                  :state :cancelling
                                  ;; A cancellation stays a request until its
                                  ;; outcome is known (SPEC-WORK.md:2846-2847).
                                  :external :uncertain))
           (digest (sha256-hex line)))
      (multiple-value-bind (found recorded-digest) (journal-lookup (kernel-journal kernel) request)
        (cond
          ((eq found :unavailable)
           (return-from kernel-operation-cancel
             (values nil (format nil "OPERATION FAIL id=~A op=~A state=~A: dedup unavailable"
                                 id (string-downcase (symbol-name kind))
                                 (string-downcase (symbol-name (getf record :state))))
                     2 nil)))
          (found
           (return-from kernel-operation-cancel
             (if (equal digest recorded-digest)
                 (values t (format nil "OPERATION OK id=~A op=~A state=cancelling"
                                   id (string-downcase (symbol-name kind)))
                         0 (kernel-operation-record kernel id))
                 (values nil (format nil "OPERATION FAIL id=~A op=~A state=~A: request=~A reused with a different payload"
                                     id (string-downcase (symbol-name kind))
                                     (string-downcase (symbol-name (getf record :state)))
                                     request)
                         2 nil))))))
      (multiple-value-bind (written refusal)
          (%operation-write kernel request line
                            (format nil "id=~A op=~A state=~A" id
                                    (string-downcase (symbol-name kind))
                                    (string-downcase (symbol-name (getf record :state)))))
        (unless written
          (return-from kernel-operation-cancel (values nil refusal 2 nil))))
      (values t (format nil "OPERATION OK id=~A op=~A state=cancelling"
                        id (string-downcase (symbol-name kind)))
              0 (kernel-operation-record kernel id)))))

;;; ------------------------------------------------------------------
;;; The recovery reconciliation.
;;; ------------------------------------------------------------------

(defun kernel-operation-interrupted (kernel)
  "The operation ids a restart must reconcile: every id whose latest record is
neither terminal nor already reconciled (SPEC-WORK.md:2759-2760). The answer is
the journal's, so a brand-new kernel over the same journal gives it."
  (loop for id in (kernel-operation-ids kernel)
        for record = (kernel-operation-record kernel id)
        unless (or (member (getf record :state) *operation-terminal-states*)
                   (getf record :reconciled))
          collect id))

(defun kernel-operation-reconcile (kernel &key author stamp)
  "Reconcile the interrupted operation ids and their external outcomes before
anything is retried (SPEC-WORK.md:2759-2760). Answer (values OK-P LINE
EXIT-CODE IDS).

It moves no operation's state: the restart cannot know whether the work
finished. It records that the id was accounted for, and marks the external
outcome `uncertain` unless the owning engine had already recorded a known one:
a restart cannot speak for an accept record written before the work ran, so
`external=none` at accept becomes `uncertain` and never stays `none`. It erases no
accepted mutation: no work event is written, and the revision does not move. A
second call answers the empty list."
  (let ((ids (kernel-operation-interrupted kernel)))
    (dolist (id ids)
      (let* ((record (kernel-operation-record kernel id))
             (kind (getf record :kind))
             (state (getf record :state))
             (external (if (eq (getf record :external) :known) :known :uncertain)))
        (multiple-value-bind (written refusal)
            (%operation-write kernel (format nil "~A/reconciled" id)
                              (%operation-line :id id :kind kind
                                               :request (getf record :request)
                                               :author (or author (getf record :author))
                                               :stamp (or stamp (getf record :stamp))
                                               :state state
                                               :external external
                                               :reconciled t
                                               :result (getf record :result))
                              (format nil "id=~A op=~A state=~A" id
                                      (string-downcase (symbol-name kind))
                                      (string-downcase (symbol-name state))))
          (unless written
            (return-from kernel-operation-reconcile (values nil refusal 2 nil))))))
    (values t (format nil "OPERATION OK op=- state=- reconciled=~D" (length ids)) 0 ids)))
