;;;; journal.lisp --- the acceptance interface, and nothing more.
;;;;
;;;; docs/SPEC-WORK.md:307 — the session "appends it with its request id to the
;;;; local recovery journal, acknowledges only after the journal is durable,
;;;; then applies it". The kernel therefore asks the journal to accept an
;;;; envelope BEFORE any of it is applied, and a refusal applies nothing.
;;;;
;;;; What is here is the interface and a fake. THE FAKE TESTS ORDERING ONLY,
;;;; NOT DURABILITY: it holds no file, does no fsync, survives no restart, and
;;;; the `atomic-mutation` and `recovery` suites (SPEC-WORK.md:3348, :3357) are
;;;; out of this slice and are not claimed green by anything here.
;;;;
;;;; The dedup lookup lives behind this interface on purpose. SPEC-WORK.md:2117
;;;; forbids "an unbounded resident map of request ids" as production dedup; the
;;;; kernel keeps no such map, and the fake's own store is bounded and answers
;;;; `dedup unavailable` past its capacity, which is the shape of
;;;; SPEC-WORK.md:517 rather than a production implementation of it.

(in-package #:nova-work)

(defgeneric journal-accept (journal envelope)
  (:documentation "Answer (values T NIL) to accept, or (values NIL reason) to
refuse. Called before any part of ENVELOPE is applied."))

(defgeneric journal-record (journal request digest line rev)
  (:documentation "Record the accepted request's id, the digest of its payload
and the OK line it was answered with."))

(defgeneric journal-lookup (journal request)
  (:documentation "Answer (values FOUND-P digest line) for REQUEST, or
(values :unavailable page NIL) where the bounded store cannot say."))

(defclass ordering-journal ()
  ((records :initform (make-hash-table :test #'equal) :reader journal-records)
   (order :initform '() :accessor journal-order-slot)
   (capacity :initarg :capacity :initform 64 :reader journal-capacity)
   (evicted :initform nil :accessor journal-evicted-p)))

(defun make-ordering-journal (&key (capacity 64))
  "A bounded fake. Past CAPACITY the oldest record is evicted and EVERY id the
store no longer holds -- including one it has never seen -- answers `dedup
unavailable` from then on. That is deliberate: SPEC-WORK.md:517 refuses rather
than assuming a request outside the bound is new, and a fake that cannot tell
the two apart must take the refusal. A production store answers from the
retention section's bounded indexed pages instead; this one is not that."
  (make-instance 'ordering-journal :capacity capacity))

(defun journal-order (journal)
  "The accepted request ids in the order they were accepted. Ordering is the
only property this fake carries."
  (reverse (journal-order-slot journal)))

(defmethod journal-accept ((journal ordering-journal) envelope)
  (declare (ignore envelope))
  (values t nil))

(defmethod journal-record ((journal ordering-journal) request digest line rev)
  (setf (gethash request (journal-records journal)) (list digest line rev))
  (push request (journal-order-slot journal))
  ;; Bounded: the oldest record leaves rather than the store growing without
  ;; end. A lookup of a request that has left answers :unavailable.
  (when (> (length (journal-order-slot journal)) (journal-capacity journal))
    (let ((evicted (car (last (journal-order-slot journal)))))
      (remhash evicted (journal-records journal))
      (setf (journal-evicted-p journal) t)
      (setf (journal-order-slot journal)
            (butlast (journal-order-slot journal)))))
  request)

(defmethod journal-lookup ((journal ordering-journal) request)
  (multiple-value-bind (record found) (gethash request (journal-records journal))
    (cond (found (values t (first record) (second record)))
          ;; Past the bound the answer is a refusal and never "this is new"
          ;; (SPEC-WORK.md:517): a request whose record has left the store
          ;; cannot be shown to be a first arrival.
          ((journal-evicted-p journal) (values :unavailable "journal-page-0" nil))
          (t (values nil nil nil)))))

(defclass rejecting-journal (ordering-journal)
  ((reject-on :initarg :reject-on :initform nil :accessor journal-reject-on)))

(defun make-rejecting-journal (&key (capacity 64) reject-on)
  (make-instance 'rejecting-journal :capacity capacity :reject-on reject-on))

(defmethod journal-accept ((journal rejecting-journal) envelope)
  (let ((request (getf envelope :request))
        (reject (journal-reject-on journal)))
    (if (and reject (equal request reject))
        (values nil "injected acceptance failure")
        (values t nil))))

;;;; ------------------------------------------------------------------
;;;; Real Bounded Filesystem Persistence Adapter
;;;; ------------------------------------------------------------------

#+sbcl
(eval-when (:compile-toplevel :load-toplevel :execute)
  (require :sb-posix))

(defun sync-stream (stream &key (path "unknown"))
  "Flush internal buffers and perform POSIX durable sync (F_FULLFSYNC on Darwin, fsync on other POSIX)
on the underlying file descriptor. Signals JOURNAL-SYNC-FAILED if unsupported or if synchronization fails."
  (finish-output stream)
  #+sbcl
  (let ((fd (ignore-errors (sb-sys:fd-stream-fd stream))))
    (unless (and fd (integerp fd) (>= fd 0))
      (error 'journal-sync-failed :path path :reason "cannot obtain valid file descriptor for stream"))
    (let ((ret (handler-case
                   #+(and sbcl darwin) (sb-posix:fcntl fd 51 0)
                   #+(and sbcl (not darwin)) (sb-posix:fsync fd)
                 (error (c)
                   (error 'journal-sync-failed :path path :reason (format nil "system sync error: ~A" c))))))
      (unless (eql ret 0)
        (error 'journal-sync-failed :path path :reason (format nil "sync returned ~D" ret)))
      t))
  #-sbcl
  (error 'journal-sync-failed :path path
         :reason "unsupported platform: durable file synchronization requires SBCL with POSIX support"))

(defun sync-directory (dir-path)
  "Synchronize parent directory DIR-PATH to non-volatile storage.
Uses standard POSIX fsync(dirfd), which is supported across POSIX platforms
(including Darwin APFS and Linux). Does not issue F_FULLFSYNC on directory
descriptors (which is reserved for regular file data barriers).
Signals JOURNAL-SYNC-FAILED if unsupported or if synchronization fails."
  #+sbcl
  (let ((dir-str (namestring (merge-pathnames dir-path))))
    (let ((fd nil))
      (unwind-protect
           (progn
             (setf fd (handler-case (sb-posix:open dir-str sb-posix:o-rdonly 0)
                        (error (c)
                          (error 'journal-sync-failed
                                 :path dir-str
                                 :reason (format nil "cannot open directory for sync: ~A" c)))))
             (when (or (null fd) (< fd 0))
               (error 'journal-sync-failed :path dir-str :reason "invalid directory fd"))
             (let ((ret (handler-case
                            (sb-posix:fsync fd)
                          (error (c)
                            (error 'journal-sync-failed :path dir-str :reason (format nil "directory fsync error: ~A" c))))))
               (unless (eql ret 0)
                 (error 'journal-sync-failed :path dir-str :reason (format nil "directory fsync returned ~D" ret)))
               t))
        (when (and fd (>= fd 0))
          (ignore-errors (sb-posix:close fd))))))
  #-sbcl
  (error 'journal-sync-failed :path (namestring dir-path)
         :reason "unsupported platform: durable directory synchronization requires SBCL with POSIX support"))

(defclass file-journal ()
  ((path :initarg :path :reader journal-path)
   (stream :initform nil :accessor journal-stream)
   (capacity :initarg :capacity :initform 64 :reader journal-capacity)
   (records :initform (make-hash-table :test #'equal) :reader journal-records)
   (order :initform '() :accessor journal-order-slot)
   (initial-state-hash :initarg :initial-state-hash :initform nil :accessor journal-initial-state-hash)
   (pending-envelope :initform nil :accessor journal-pending-envelope)
   (reject-on :initarg :reject-on :initform nil :accessor journal-reject-on)
   (fail-sync-on :initarg :fail-sync-on :initform nil :accessor journal-fail-sync-on)
   (fail-pre-write-on :initarg :fail-pre-write-on :initform nil :accessor journal-fail-pre-write-on)
   (fail-partial-write-on :initarg :fail-partial-write-on :initform nil :accessor journal-fail-partial-write-on)
   (fail-creation-sync-on :initarg :fail-creation-sync-on :initform nil :accessor journal-fail-creation-sync-on)
   (seq :initform 0 :accessor journal-seq)
   (uncertain-p :initform nil :accessor journal-uncertain-p)))

(defun make-file-journal (path &key (capacity 64) initial-state-hash reject-on fail-sync-on
                                fail-pre-write-on fail-partial-write-on fail-creation-sync-on)
  (make-instance 'file-journal
                 :path (namestring (merge-pathnames path))
                 :capacity capacity
                 :initial-state-hash initial-state-hash
                 :reject-on reject-on
                 :fail-sync-on fail-sync-on
                 :fail-pre-write-on fail-pre-write-on
                 :fail-partial-write-on fail-partial-write-on
                 :fail-creation-sync-on fail-creation-sync-on))

(defun write-header (stream initial-state-hash capacity stamp path)
  (let* ((header (list :journal-header
                       :magic "nova-work/journal"
                       :version 1
                       :initial-state (or initial-state-hash "")
                       :capacity capacity
                       :created-at (or stamp "2026-09-14T00:00:00Z")))
         (header-str (canonical-string header)))
    (write-string header-str stream)
    (write-char #\Newline stream)
    (sync-stream stream :path path)))

(defun read-header (stream path expected-initial-state)
  (let ((line (read-line stream nil :eof)))
    (when (eq line :eof)
      (error 'journal-corrupt-data :path path :reason "unexpected EOF reading header"))
    (let ((form (handler-case (read-restricted line)
                  (error (c)
                    (error 'journal-corrupt-data :path path :reason (format nil "header parse error: ~A" c))))))
      (unless (and (listp form) (eq (first form) :journal-header))
        (error 'journal-corrupt-data :path path :reason "missing :journal-header tag"))
      (let ((plist (rest form)))
        (unless (equal (getf plist :magic) "nova-work/journal")
          (error 'journal-corrupt-data :path path :reason "bad journal magic"))
        (unless (eql (getf plist :version) 1)
          (error 'journal-corrupt-data :path path :reason "unsupported journal version"))
        (let ((initial-state (getf plist :initial-state)))
          (when (and expected-initial-state
                     (plusp (length expected-initial-state))
                     (not (string= expected-initial-state initial-state)))
            (error 'journal-mismatch :path path
                   :what (format nil "initial-state mismatch: expected ~A, recorded ~A"
                                 expected-initial-state initial-state))))
        form))))

(defun read-record-frame (stream path expected-seq)
  (let ((line (read-line stream nil :eof)))
    (cond
      ((eq line :eof) nil)
      ((zerop (length line)) nil)
      (t
       (let ((frame (handler-case (read-restricted line)
                      (error (c)
                        (error 'journal-corrupt-data :path path
                               :reason (format nil "frame parse error: ~A" c))))))
         (unless (and (listp frame) (eq (first frame) :frame))
           (error 'journal-corrupt-data :path path :reason "missing :frame tag"))
         (let* ((plist (rest frame))
                (seq (getf plist :seq))
                (len (getf plist :len))
                (checksum (getf plist :checksum))
                (record (getf plist :record)))
           (unless (eql seq expected-seq)
             (error 'journal-corrupt-data :path path
                    :reason (format nil "sequence mismatch: expected ~D, got ~D"
                                    expected-seq seq)))
           (unless (and record (listp record))
             (error 'journal-corrupt-data :path path :reason "frame missing record"))
           (let* ((record-canon (canonical-string record))
                  (actual-len (length record-canon))
                  (actual-checksum (sha256-hex record-canon)))
             (unless (eql len actual-len)
               (error 'journal-corrupt-data :path path
                      :reason (format nil "frame length mismatch: header ~D, actual ~D"
                                      len actual-len)))
             (unless (string= checksum actual-checksum)
               (error 'journal-corrupt-data :path path
                      :reason (format nil "frame checksum mismatch: header ~A, actual ~A"
                                      checksum actual-checksum)))
             frame)))))))

(defun open-file-journal (path &key (capacity 64) initial-state-hash reject-on fail-sync-on
                                fail-pre-write-on fail-partial-write-on fail-creation-sync-on stamp)
  (let* ((journal (make-instance 'file-journal
                                 :path (namestring (merge-pathnames path))
                                 :capacity capacity
                                 :initial-state-hash initial-state-hash
                                 :reject-on reject-on
                                 :fail-sync-on fail-sync-on
                                 :fail-pre-write-on fail-pre-write-on
                                 :fail-partial-write-on fail-partial-write-on
                                 :fail-creation-sync-on fail-creation-sync-on))
         (full-path (journal-path journal))
         (exists (probe-file full-path)))
    (if exists
        (let ((seq 0))
          ;; 1. Read and validate entire file read-only. Failure leaves file untouched.
          (with-open-file (in full-path :direction :input :element-type 'character :external-format :utf-8)
            (let* ((header (read-header in full-path initial-state-hash))
                   (hplist (rest header)))
              (setf (journal-initial-state-hash journal) (getf hplist :initial-state))
              (when (getf hplist :capacity)
                (setf (slot-value journal 'capacity) (getf hplist :capacity))))
            (loop
              (let ((frame (read-record-frame in full-path (1+ seq))))
                (unless frame (return))
                (incf seq)
                (let* ((record (getf (rest frame) :record))
                       (req (getf record :request))
                       (digest (getf record :digest))
                       (line (getf record :line))
                       (rev (getf record :rev)))
                  (when (and (>= (hash-table-count (journal-records journal)) (journal-capacity journal))
                             (null (gethash req (journal-records journal))))
                    (error 'journal-corrupt-data :path full-path
                           :reason (format nil "journal contains ~D records, exceeding capacity ~D"
                                           (1+ (hash-table-count (journal-records journal)))
                                           (journal-capacity journal))))
                  (setf (gethash req (journal-records journal)) (list digest line rev))
                  (push req (journal-order-slot journal))))))
          (setf (journal-seq journal) seq)
          ;; 2. Reopen for append once validated.
          (let ((out (open full-path :direction :output
                                     :if-exists :append
                                     :if-does-not-exist :error
                                     :element-type 'character
                                     :external-format :utf-8)))
            (setf (journal-stream journal) out)))
        ;; File does not exist: create fresh journal, sync directory, and write header
        (let ((out (open full-path :direction :output
                                   :if-exists :error
                                   :if-does-not-exist :create
                                   :element-type 'character
                                   :external-format :utf-8)))
          (setf (journal-stream journal) out)
          (handler-case
              (progn
                ;; Optional injected creation directory sync failure
                (when (journal-fail-creation-sync-on journal)
                  (error 'journal-sync-failed :path full-path :reason "injected creation directory sync failure"))
                ;; Synchronize parent directory to guarantee the new directory entry is durable
                (let ((parent-dir (directory-namestring (merge-pathnames full-path))))
                  (sync-directory parent-dir))
                (write-header out initial-state-hash capacity stamp full-path))
            (error (c)
              ;; CRITICAL: Close the already-open stream so file descriptor is not leaked,
              ;; while preserving the created file on disk for explicit recovery.
              (ignore-errors (close out))
              (setf (journal-stream journal) nil)
              (error c)))))
    journal))

(defun close-file-journal (journal)
  (when (journal-stream journal)
    (unwind-protect
         (sync-stream (journal-stream journal) :path (journal-path journal))
      (close (journal-stream journal))
      (setf (journal-stream journal) nil)))
  t)

(defmacro with-file-journal ((var path &rest args) &body body)
  `(let ((,var (open-file-journal ,path ,@args)))
     (unwind-protect
          (progn ,@body)
       (close-file-journal ,var))))

(defmethod journal-accept ((journal file-journal) envelope)
  (let ((request (getf envelope :request))
        (reject (journal-reject-on journal)))
    (cond
      ((journal-uncertain-p journal)
       (values nil "journal in uncertain-write state; recovery required"))
      ((and reject (equal request reject))
       (values nil "injected acceptance failure"))
      ((null (journal-stream journal))
       (values nil "journal is closed"))
      ((null request)
       (values nil "missing request id"))
      ((null (getf envelope :digest))
       (values nil "missing payload digest"))
      ((and (>= (hash-table-count (journal-records journal)) (journal-capacity journal))
            (null (gethash request (journal-records journal))))
       (values nil "journal capacity exceeded"))
      (t
       (setf (journal-pending-envelope journal) envelope)
       (values t nil)))))

(defmethod journal-record ((journal file-journal) request digest line rev)
  (when (journal-uncertain-p journal)
    (error 'journal-uncertain-write :path (journal-path journal)
                                    :reason "prior append or sync failed; recovery required"))
  (let ((envelope (journal-pending-envelope journal)))
    (unless (and envelope
                 (equal (getf envelope :request) request)
                 (equal (getf envelope :digest) digest))
      (error 'unsupported-input
             :what (format nil "journal-record mismatch: expected pending envelope for ~A" request)))
    (let* ((stream (journal-stream journal))
           (path (journal-path journal)))
      (unless stream
        (error 'nova-work-error :what "cannot record to closed journal"))
      (let* ((next-seq (1+ (journal-seq journal)))
             (events (mapcar #'event-record-form (getf envelope :events)))
             (record (list :request request
                           :digest digest
                           :line line
                           :rev rev
                           :events events))
             (record-canon (canonical-string record))
             (checksum (sha256-hex record-canon))
             (len (length record-canon))
             (frame (list :frame
                          :seq next-seq
                          :len len
                          :checksum checksum
                          :record record))
             (frame-str (canonical-string frame)))
        (handler-case
            (progn
              (when (and (journal-fail-pre-write-on journal)
                         (equal request (journal-fail-pre-write-on journal)))
                (error "injected pre-write failure"))
              (cond
                ((and (journal-fail-partial-write-on journal)
                      (equal request (journal-fail-partial-write-on journal)))
                 ;; Partial (torn) write: write half the frame bytes and flush before failing
                 (let ((partial-len (max 1 (floor (length frame-str) 2))))
                   (write-string (subseq frame-str 0 partial-len) stream)
                   (finish-output stream)
                   (error "injected partial-write failure")))
                (t
                 (write-string frame-str stream)
                 (write-char #\Newline stream)
                 (finish-output stream)
                 (when (and (journal-fail-sync-on journal)
                            (equal request (journal-fail-sync-on journal)))
                   (error "injected sync failure after write and flush"))
                 (sync-stream stream :path path))))
          (error (c)
            (setf (journal-uncertain-p journal) t)
            (error 'journal-uncertain-write :path path
                                            :reason (format nil "write or sync failed: ~A" c))))
        ;; Sequence is committed ONLY after successful publication and sync
        (setf (journal-seq journal) next-seq)
        ;; Update in-memory dedup store
        (setf (gethash request (journal-records journal)) (list digest line rev))
        (push request (journal-order-slot journal))
        (setf (journal-pending-envelope journal) nil)
        request))))

(defmethod journal-lookup ((journal file-journal) request)
  (multiple-value-bind (record found) (gethash request (journal-records journal))
    (if found
        (values t (first record) (second record))
        (values nil nil nil))))

(defun replay-journal (journal target-kernel &key (stop-at-seq nil))
  "Replay entries from JOURNAL into TARGET-KERNEL from its current revision.
TARGET-KERNEL must start at the seed state matching the journal's initial state hash.
Replays into private working state and installs into TARGET-KERNEL only after the
requested replay cut completes without error.
Returns (values TARGET-KERNEL total-replayed-events total-replayed-records)."
  (let* ((path (journal-path journal))
         (expected-initial (root-digest (kernel-state target-kernel)))
         (working-state (kernel-state target-kernel))
         (working-next-rev (kernel-next-rev target-kernel))
         (seq 0)
         (record-count 0)
         (event-count 0))
    (with-open-file (in path :direction :input :element-type 'character :external-format :utf-8)
      (let ((header (read-header in path expected-initial)))
        (declare (ignore header)))
      (loop
        (when (and stop-at-seq (>= seq stop-at-seq))
          (return))
        (let ((frame (read-record-frame in path (1+ seq))))
          (unless frame (return))
          (incf seq)
          (incf record-count)
          (let* ((record (getf (rest frame) :record))
                 (req (getf record :request))
                 (digest (getf record :digest))
                 (rev (getf record :rev))
                 (events (loop for e in (getf record :events)
                               collect (record-form->event
                                        e :session-written-p
                                        (member (getf e :kind) '(:settle :revive)))))
                 (envelope (list :request req :digest digest :events events)))
            (incf *replays*)
            (incf event-count (length events))
            (let ((candidate (apply-envelope working-state envelope)))
              (setf working-state candidate)
              (setf working-next-rev (max working-next-rev (1+ rev))))))))
    (setf (kernel-state target-kernel) working-state)
    (setf (kernel-next-rev target-kernel) working-next-rev)
    (values target-kernel event-count record-count)))
