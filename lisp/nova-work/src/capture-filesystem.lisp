;;;; capture-filesystem.lisp --- source capture and import staging with the
;;;; staged bytes on disk (AUDIT row E02.3, SPEC-WORK.md:2752-2754, :2759-2760).
;;;;
;;;; "**Slow I/O stages its inputs and results outside the mutation loop** and
;;;; only the owning engine admits a validated result at an expected revision,
;;;; so a concurrent capture never becomes a second writer" (:2752-2754), and
;;;; "Queues, jobs, staged bytes and retained results are bounded by explicit
;;;; limits, accepted work stays recoverable, and **recovery reconciles
;;;; interrupted operation ids and their external outcomes before anything is
;;;; retried**" (:2759-2760).
;;;;
;;;; src/capture.lisp is the pure model of that: an input is a struct carrying a
;;;; byte COUNT and no bytes, and nothing is ever read from or written to
;;;; anything. Here the staged bytes are a file under a staging root, the
;;;; running sum is the sum of what is actually on disk, and the length and
;;;; SHA-256 of every stage are recorded on the same recovery journal the accept
;;;; record rides -- so a restart can check each staged file against what it was
;;;; told, and report a missing or torn one rather than admitting it.
;;;;
;;;; THE PROVIDER IS NOT DECIDED HERE. The spec does not say what a capture
;;;; reads FROM, so this file takes the narrowest reading it can: the provider
;;;; is out of process and the engine's contract begins at "here are the bytes".
;;;; STAGE-SOURCE-BYTES takes the content; nothing here opens a network
;;;; connection or runs a provider. That default is written up in the lane's
;;;; HANDOFF for Stella; if she chooses a resident provider, it is a producer in
;;;; front of this function and none of what is below changes.
;;;;
;;;; A staged file is NOT written by rename. A stage is trusted only when its
;;;; recorded length and digest match the bytes on disk, which catches a torn
;;;; write at the moment it matters -- the admission -- and is a stronger
;;;; statement than an atomic publish would be, because it also catches a stage
;;;; that something else truncated afterwards.

(in-package #:nova-work)

(defstruct (filesystem-capture-stage
            (:include capture-stage)
            (:constructor %make-filesystem-capture-stage))
  (root "")
  ;; input-id -> (:operation <id> :bytes <n> :sha <hex>)
  (staged '())
  ;; input ids the restart could not verify, with the reason
  (unverified '()))

;;; ------------------------------------------------------------------
;;; Where a staged input lives, and what the journal records about it
;;; ------------------------------------------------------------------

(defun staged-input-path (stage operation id)
  "The staged bytes for input ID of operation OPERATION. One directory per
operation, so an interrupted operation's staged inputs are found together."
  (format nil "~A/~A/~A.stage" (filesystem-capture-stage-root stage) operation id))

(defun stage-record-p (record)
  "True when RECORD is a staging record and not an accept or a cancel."
  (and record (eq :stage (getf record :record))))

(defun make-stage-record (&key request operation input kind revision bytes sha
                               author stamp)
  "The durable record of one staged input. Its :id is the stage's own request
id; :bytes and :sha are what the restart checks the file against."
  (list :id request :record :stage :operation operation :input input
        :kind kind :revision revision :bytes bytes :sha sha
        :author author :stamp stamp))

;;; ------------------------------------------------------------------
;;; Opening the staging area
;;; ------------------------------------------------------------------

(defun open-filesystem-capture-stage (root &key registry (limits *capture-stage-limits*))
  "The staging area rooted at ROOT, over REGISTRY's durable recovery journal,
reconciled against every stage record that journal already holds
(SPEC-WORK.md:2752-2754, :2759-2760)."
  (ensure-directories-exist (concatenate 'string root "/"))
  (let ((stage (%make-filesystem-capture-stage
                :registry (or registry (make-operation-registry))
                :inputs '() :results '() :limits limits
                :root (string-right-trim "/" root))))
    (reconcile-capture-stage stage)
    stage))

;;; ------------------------------------------------------------------
;;; Staging one input outside the mutation loop
;;; ------------------------------------------------------------------

(defun write-staged-file (path content)
  "Write CONTENT to PATH and make it durable: the file is fsynced and so is its
directory, because a staged input nobody can find after a crash is not staged."
  (let ((directory (subseq path 0 (position #\/ path :from-end t))))
    (ensure-directories-exist (concatenate 'string directory "/"))
    (with-open-file (out path :direction :output :if-exists :supersede
                              :if-does-not-exist :create
                              :element-type 'character :external-format :utf-8)
      (write-string content out)
      (sync-stream out :path path))
    (sync-directory directory)
    path))

(defun read-staged-file (path)
  "The staged bytes, or NIL when the file is gone."
  (when (probe-file path)
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8)
      (let ((text (make-string (file-length in))))
        (subseq text 0 (read-sequence text in))))))

(defun stage-source-bytes (stage &key operation id kind (expected-revision 0)
                                      content source-pin request author stamp)
  "Stage one source record outside the mutation loop. The bound is checked
BEFORE anything is written, so a breach refuses and stages nothing; then the
bytes are written and fsynced, and their length and digest are recorded durably
on the recovery journal before the stage is acknowledged
(SPEC-WORK.md:2752-2754, :2759).

Answers (values T LINE 0) or (values NIL LINE 2)."
  (let* ((text (or content ""))
         (bytes (length (utf8-octets text)))
         (sha (sha256-hex text)))
    (multiple-value-bind (staged line)
        (capture-stage-input stage :id id :kind kind
                                   :expected-revision expected-revision
                                   :bytes bytes :records 1
                                   :source-pin source-pin)
      (if (not staged)
          ;; The bound refused: nothing is written and nothing is recorded.
          (values nil line 2)
          (handler-case
              (let ((path (staged-input-path stage operation id)))
                (write-staged-file path text)
                (durable-accept-record
                 (operation-registry-journal (capture-stage-registry stage))
                 (make-stage-record :request (or request id) :operation operation
                                    :input id :kind kind
                                    :revision expected-revision
                                    :bytes bytes :sha sha
                                    :author author :stamp stamp))
                (push (cons id (list :operation operation :bytes bytes :sha sha))
                      (filesystem-capture-stage-staged stage))
                (values t line 0))
            (error (c)
              ;; A stage that could not be made durable is not a stage: the
              ;; input comes back off the set and the bytes come off the disk.
              (setf (capture-stage-inputs stage)
                    (remove id (capture-stage-inputs stage)
                            :key #'capture-input-id :test #'equal))
              (ignore-errors (delete-file (staged-input-path stage operation id)))
              (values nil (format nil "STAGE FAIL id=~A: ~A" id c) 2)))))))

;;; ------------------------------------------------------------------
;;; Reading a stage back, verified
;;; ------------------------------------------------------------------

(defun staged-content (stage id)
  "The staged bytes for ID, verified against the length and digest the journal
recorded. A stage whose file is gone, short or altered answers
(values NIL LINE 2) and is never admitted (SPEC-WORK.md:2752-2754)."
  (let ((entry (cdr (assoc id (filesystem-capture-stage-staged stage) :test #'equal))))
    (if (null entry)
        (values nil (format nil "STAGE FAIL id=~A: no staged input" id) 2)
        (let* ((path (staged-input-path stage (getf entry :operation) id))
               (text (read-staged-file path)))
          (cond
            ((null text)
             (values nil (format nil "STAGE FAIL id=~A: the staged file is missing" id) 2))
            ((/= (length (utf8-octets text)) (getf entry :bytes))
             (values nil
                     (format nil "STAGE FAIL id=~A: staged ~D bytes, recorded ~D"
                             id (length (utf8-octets text)) (getf entry :bytes))
                     2))
            ((not (equal (sha256-hex text) (getf entry :sha)))
             (values nil (format nil "STAGE FAIL id=~A: the staged digest moved" id) 2))
            (t (values text nil 0)))))))

(defun staged-bytes-on-disk (stage)
  "The running sum of what is actually on disk, which is the number the bound is
about (SPEC-WORK.md:2759)."
  (let ((total 0))
    (dolist (row (filesystem-capture-stage-staged stage) total)
      (let ((text (read-staged-file (staged-input-path stage
                                                       (getf (cdr row) :operation)
                                                       (car row)))))
        (when text (incf total (length (utf8-octets text))))))))

;;; ------------------------------------------------------------------
;;; The restart: accepted work stays recoverable (:2759-2760)
;;; ------------------------------------------------------------------

(defun reconcile-capture-stage (stage)
  "Walk every stage record the recovery journal holds and check its file. A
verified stage is re-registered so the engine can still admit it; one whose file
is missing or torn is reported on UNVERIFIED and NOT registered, because
`accepted work stays recoverable` is a promise about work that is still there,
not a licence to admit what is not. Answers (values VERIFIED UNVERIFIED)."
  (let* ((registry (capture-stage-registry stage))
         (journal (operation-registry-journal registry))
         (verified '())
         (unverified '()))
    (dolist (id (operation-journal-ids journal))
      (let ((record (accept-record-of journal id)))
        (when (stage-record-p record)
          (let* ((input (getf record :input))
                 (operation (getf record :operation))
                 (path (staged-input-path stage operation input))
                 (text (read-staged-file path)))
            (cond
              ((null text)
               (push (cons input "the staged file is missing") unverified))
              ((/= (length (utf8-octets text)) (getf record :bytes))
               (push (cons input "the staged file is short or long") unverified))
              ((not (equal (sha256-hex text) (getf record :sha)))
               (push (cons input "the staged digest moved") unverified))
              (t
               (unless (find input (capture-stage-inputs stage)
                             :key #'capture-input-id :test #'equal)
                 (push (make-capture-input :id input :kind (getf record :kind)
                                           :expected-revision (getf record :revision)
                                           :bytes (getf record :bytes) :records 1
                                           :source-pin nil)
                       (capture-stage-inputs stage))
                 (push (cons input (list :operation operation
                                         :bytes (getf record :bytes)
                                         :sha (getf record :sha)))
                       (filesystem-capture-stage-staged stage)))
               (push input verified)))))))
    (setf (filesystem-capture-stage-unverified stage) (nreverse unverified))
    (values (nreverse verified) (filesystem-capture-stage-unverified stage))))

;;; ------------------------------------------------------------------
;;; Admission: the owning engine, at the expected revision (:2752-2754)
;;; ------------------------------------------------------------------

(defun admit-staged-result (stage current-revision &key id result)
  "Admit a staged input's result at CURRENT-REVISION. The staged bytes are
verified first, so a torn or vanished stage is never admitted; then the pure
model's revision and retention rules decide (SPEC-WORK.md:2752-2754, :2759)."
  (multiple-value-bind (text line code) (staged-content stage id)
    (if (null text)
        (values nil line code)
        (multiple-value-bind (admitted admit-line)
            (capture-admit-result stage current-revision :id id
                                  :result (or result (list :bytes (length (utf8-octets text))
                                                           :sha (sha256-hex text))))
          (values admitted admit-line (if admitted 0 2))))))
