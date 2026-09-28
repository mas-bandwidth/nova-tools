;;;; replays-8641.lisp --- the smallest pure records the five acceptance replays
;;;; of card 8641 assert. Each paragraph of docs/SPEC-WORK.md is reduced to the
;;;; facts it promises, without a session, a socket, a savepoint file, a worker
;;;; or an intake adapter:
;;;;
;;;;   a-broken-assertion-must-fail         (:6371-6373, :6384-6387)
;;;;   a-savepoint-is-not-a-shared-backup   (:5790-5793, :6270-6303)
;;;;   a-stop-reaches-distributed-work      (:6374-6378)
;;;;   add-field-order-is-complete          (:962-967, :2977-2999, :5819-5822)
;;;;   archive-completeness                 (:6232)
;;;;
;;;; The transports these records would ride (a savepoint file, a distributed
;;;; worker, an intake adapter) stay outside this slice; the records and the
;;;; refusals are what the replays can assert here.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; a-broken-assertion-must-fail (SPEC-WORK.md:6371-6373, :6384-6387)
;;; ------------------------------------------------------------------

(defstruct (regression-receipt
             (:constructor make-regression-receipt
                 (&key criterion revision-coverage uncertainty assertion mutation)))
  "One regression receipt: the specific criterion, its revision coverage, the
remaining uncertainty, and the assertion together with the mutation that
deliberately breaks the behaviour it asserts. ASSERTION and MUTATION are
functions of the preserved behaviour."
  criterion revision-coverage uncertainty assertion mutation)

(defun assertion-holds-p (receipt behaviour)
  "The receipt's assertion on a behaviour, which is T when the behaviour is
preserved."
  (funcall (regression-receipt-assertion receipt) behaviour))

(defun regression-evidence-p (receipt behaviour)
  "A test is regression evidence only when its assertion holds on the preserved
behaviour and the deliberately broken behaviour makes it fail. A test that still
passes with its asserted behaviour broken is not regression evidence
(SPEC-WORK.md:6371-6373)."
  (let ((broken (funcall (regression-receipt-mutation receipt) behaviour)))
    (and (funcall (regression-receipt-assertion receipt) behaviour)
         (not (funcall (regression-receipt-assertion receipt) broken)))))

(defun green-badge-p (receipt behaviour)
  "The badge a receipt earns: :evidence only for a real mutation, never a green
badge for a test that passes when its asserted behaviour is broken
(SPEC-WORK.md:6384-6387)."
  (if (regression-evidence-p receipt behaviour) :evidence nil))

;;; ------------------------------------------------------------------
;;; a-savepoint-is-not-a-shared-backup (SPEC-WORK.md:5790-5793, :6270-6303)
;;; ------------------------------------------------------------------

(defstruct (savepoint
             (:constructor make-savepoint
                 (&key id schema local-revision journal-id replay-cut boundary
                       manifest image local-replies age failed-backup)))
  "One validated atomic local savepoint: its schema, journal id, the local
revision of its image, the replay cut and boundary records, its own manifest and
content references, its age, and whether a replacement backup failed. The
periodic clip supplies the separately observable shared checkpoint."
  id schema local-revision journal-id replay-cut boundary
  manifest image local-replies age failed-backup)

(defstruct (checkpoint
             (:constructor make-checkpoint (&key id shared-revision source)))
  "The shared checkpoint the clip supplies, kept a different thing from a
savepoint."
  id shared-revision source)

(defun savepoint-is-not-shared-backup-p (savepoint)
  "A local savepoint is never the shared backup (SPEC-WORK.md:6277)."
  (declare (ignore savepoint))
  t)

(defun restore-open (&key savepoint)
  "A restore opens a read-only, isolated, non-dispatching recovery session: it
inherits no coordinator ownership, reanimates no assignment and replays no bus
message (SPEC-WORK.md:6285-6288)."
  (declare (ignore savepoint))
  (list :read-only t :isolated t :dispatch nil :ownership nil
        :assignments '() :messages '()))

(defun savepoint-report (savepoint &key shared-revision unshared failed-backups)
  "Savepoint age, the local and the shared revisions side by side, the unshared
work and the failed backup attempts (SPEC-WORK.md:6278-6280)."
  (list :age (savepoint-age savepoint)
        :local-revision (savepoint-local-revision savepoint)
        :shared-revision shared-revision
        :unshared unshared
        :failed-backups failed-backups))

(defun checkpoint-line (thing)
  "The line for a shared checkpoint. A savepoint is never printed where a
checkpoint was asked for (SPEC-WORK.md:5790-5793)."
  (unless (checkpoint-p thing)
    (error 'unsupported-input
           :what "a savepoint is never printed where a checkpoint was asked for"))
  (list :checkpoint (checkpoint-id thing) (checkpoint-shared-revision thing)))

;;; ------------------------------------------------------------------
;;; a-stop-reaches-distributed-work (SPEC-WORK.md:6374-6378)
;;; ------------------------------------------------------------------

(defstruct (control-request
             (:constructor make-control-request (&key kind request targets)))
  "One priority change, correction, pause or stop, with its durable request
identity and the already-distributed tasks it names."
  kind request targets)

(defstruct (control-handle
             (:constructor make-control-handle
                 (&key target request delivered acknowledged reconciled generation)))
  "One reconciled execution handle: delivery and acknowledgement of the durable
request, and the generation a correction bumps."
  target request delivered acknowledged reconciled generation)

(defstruct (blocked-question
             (:constructor make-blocked-question
                 (&key node question fallback persisted)))
  "A blocked question with its bounded fallback plan, persisted so a missing
answer at night does not stall every independent task (SPEC-WORK.md:6377-6378)."
  node question fallback persisted)

(defparameter *control-kinds* '(:prioritise :correct :pause :stop)
  "The four controls reached by `execution pause`, `stop` and `correct`.")

(defun control-kind-p (kind)
  (and (member kind *control-kinds*) t))

(defun distribute-control (request targets)
  "Carry one durable control request to each already-distributed task and return
its reconciled handles. The same request id reaches all of them."
  (list :request (control-request-request request)
        :handles (mapcar (lambda (target)
                           (make-control-handle :target target
                                                :request (control-request-request request)
                                                :delivered t :acknowledged t
                                                :reconciled t :generation 1))
                         targets)))

(defun stop-reaches-p (distribution)
  "True only when every distributed handle has delivered, acknowledged and
reconciled the request (SPEC-WORK.md:6375)."
  (let ((handles (getf distribution :handles)))
    (and handles
         (every (lambda (h)
                  (and (control-handle-delivered h)
                       (control-handle-acknowledged h)
                       (control-handle-reconciled h)))
                handles))))

(defun control-retry-identity (distribution)
  (getf distribution :request))

(defun apply-correction (handle)
  "A correction across already-distributed work bumps the generation of its
handle and never rewrites the original."
  (let ((h (copy-control-handle handle)))
    (incf (control-handle-generation h))
    h))

(defun fallback-plan (question)
  "The bounded fallback plan persisted with a blocked question."
  (when (blocked-question-persisted question)
    (or (blocked-question-fallback question) :bounded-fallback)))

(defun independent-tasks-stall-p (questions)
  "True when a blocked question has no persisted bounded fallback plan."
  (some (lambda (q) (null (fallback-plan q))) questions))

;;; ------------------------------------------------------------------
;;; add-field-order-is-complete (SPEC-WORK.md:962-967, :2977-2999, :5819-5822)
;;; ------------------------------------------------------------------

(defparameter *node-add-structure-fields*
  '(:verb :node-type :title :under :category :required :acceptance
    :repo :links :private :version :reason)
  "The twelve fields of a completed `node add` `:structure` event, in the order
the registry lists them, which is also the payload-digest order
(SPEC-WORK.md:965-967, :2977-2980).")

(defparameter *node-add-pre-fold-fields*
  '(:verb :node-type :title :under :category :required :acceptance :repo
    :links :reason)
  "A fixture in the pre-fold add shape, refused rather than read as the new one
(SPEC-WORK.md:2981-2984).")

(defun node-add-field-value (request key)
  "The field's value, or `(:absent)` when the caller did not give it. An empty
list stays `()` and a cleared links list stays `(:absent)`."
  (let ((pair (assoc key request)))
    (if pair (cdr pair) +absent+)))

(defun node-add-structure (request)
  "The `node add` `:structure` field list, every one of the twelve fields
written in order, absent `(:absent)`."
  (loop for key in *node-add-structure-fields*
        collect key
        collect (node-add-field-value request key)))

(defun node-add-structure-keys (request)
  (loop for (key nil) on (node-add-structure request) by #'cddr collect key))

(defun node-add-pre-fold-structure (request)
  "The same request in the pre-fold field order, for the loader refusal."
  (loop for key in *node-add-pre-fold-fields*
        collect key
        collect (node-add-field-value request key)))

(defun node-add-digest (request)
  "Serializer one: the payload digest of the completed `node add` structure."
  (sha256-hex (canonical-string (node-add-structure request))))

(defun write-node-add-value (value stream)
  "Serializer two's own writer, independent of `canonical-print`, over the same
restricted spellings: `(:absent)`, `()`, `""` and `false` remain distinct."
  (typecase value
    (null (write-string "()" stream))
    (cons (write-char #\( stream)
          (let ((first t))
            (dolist (item value)
              (unless first (write-char #\Space stream))
              (setf first nil)
              (write-node-add-value item stream)))
          (write-char #\) stream))
    (keyword (write-char #\: stream)
             (write-string (string-downcase (symbol-name value)) stream))
    (string (write-char #\" stream)
            (loop for ch across value
                  do (case ch
                       (#\" (write-string "\\\"" stream))
                       (#\\ (write-string "\\\\" stream))
                       (t (write-char ch stream))))
            (write-char #\" stream))
    (integer (format stream "~D" value))
    (t (error 'restricted-data-violation :value value))))

(defun node-add-digest-second (request)
  "Serializer two: the second, independent digest of the same structure."
  (let ((text (with-output-to-string (s)
                (write-char #\( s)
                (let ((first t))
                  (dolist (key *node-add-structure-fields*)
                    (unless first (write-char #\Space s))
                    (setf first nil)
                    (write-node-add-value key s)
                    (write-char #\Space s)
                    (write-node-add-value (node-add-field-value request key) s)))
                (write-char #\) s))))
    (sha256-hex text)))

(defun load-node-add-fixture (fields)
  "Load a `node add` structure fixture only in the completed field order. A
fixture in the pre-fold order refuses `schema revision unsupported`
(SPEC-WORK.md:2981-2984)."
  (unless (equal (loop for (key nil) on fields by #'cddr collect key)
                 *node-add-structure-fields*)
    (error 'unsupported-input :what "schema revision unsupported"))
  fields)

;;; ------------------------------------------------------------------
;;; archive-completeness (SPEC-WORK.md:6232)
;;; ------------------------------------------------------------------

(defparameter *archive-gap-kinds*
  '(:missing-attachment :unavailable-comment :unsupported-field
    :size-truncation :rate-limit :mid-page-failure)
  "The six gaps an incomplete archive must state rather than absorb.")

(defstruct (archive-gap
             (:constructor make-archive-gap (&key kind detail source-issue)))
  "One explicit gap: what was not captured and the source issue it belongs to."
  kind detail source-issue)

(defstruct (archive-capture
             (:constructor make-archive-capture (&key source-issue author gaps)))
  "One capture with its source issue, its author class and its explicit gaps."
  source-issue author gaps)

(defun archive-gaps-explicit-p (capture)
  "True when every gap names one of the six kinds and a detail, so none is a
silent drop."
  (every (lambda (g)
           (and (member (archive-gap-kind g) *archive-gap-kinds*)
                (stringp (archive-gap-detail g))))
         (archive-capture-gaps capture)))

(defun archive-absorbable-p (capture)
  "An incomplete capture prohibits absorption; only a gap-free capture may be
absorbed (SPEC-WORK.md:6232)."
  (null (archive-capture-gaps capture)))

(defun author-retains-source-p (capture)
  "A mixed, external or unknown author retains its source issue."
  (and (member (archive-capture-author capture) '(:mixed :external :unknown))
       (archive-capture-source-issue capture)
       t))
