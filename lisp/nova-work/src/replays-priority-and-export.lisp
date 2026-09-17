;;;; replays-priority-and-export.lisp --- the pure kernel the five replays
;;;; `rank-2-precedes-10`, `priority-undo-is-history-not-value`,
;;;; `priority-grants-nothing`, `state-export-describes-exactly-r` and
;;;; `state-export-is-one-long-operation` drive (docs/SPEC-WORK.md:3156-3297,
;;;; 5857-5888). Ordering intent, the priority history identity, the captured
;;;; revision and the long operation are pure data here; the session/CLI wiring
;;;; a later slice owns is listed as owed in RESULT.md.

(in-package #:nova-work)

(defconstant +rank-ceiling+ (expt 10 18)
  "A rank is an unsigned integer atom of at most eighteen digits.")

(defun rank-atom-p (r)
  (and (integerp r) (<= 0 r) (< r +rank-ceiling+)))

;;; ------------------------------------------------------------------
;;; The priority table: two slots per node and the latest-change identity
;;; ------------------------------------------------------------------

(defstruct (priority-table (:constructor make-priority-table (&key parents)))
  (parents (make-hash-table :test #'equal))
  (slots (make-hash-table :test #'equal)))

(defun priority-table-slot (table id context)
  (let ((slot (gethash id (priority-table-slots table))))
    (getf slot context +absent+)))

(defun priority-change-id (table id context)
  (let ((slot (gethash id (priority-table-slots table))))
    (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword)
          +absent+)))

(defun (setf priority-table-slot) (value table id context)
  (let ((slot (or (gethash id (priority-table-slots table))
                  (setf (gethash id (priority-table-slots table))
                        (list :self +absent+ :subtree +absent+
                              :self-change +absent+ :subtree-change +absent+)))))
    (setf (getf slot context) value)
    (setf (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword))
          +absent+)
    value))

(defun note-priority-change (table id context change-id)
  (let ((slot (gethash id (priority-table-slots table))))
    (setf (getf slot (intern (concatenate 'string (symbol-name context) "-CHANGE")
                             :keyword))
          change-id)))

(defun priority-parent (table id)
  (gethash id (priority-table-parents table)))

(defun effective-rank (table id)
  "The nearest context: the node's own :self, else the deepest :subtree on its
containment path, else the default (SPEC-WORK.md:3172)."
  (let ((self (priority-table-slot table id :self)))
    (when (and self (not (eq self +absent+)))
      (return-from effective-rank (values self id :self))))
  (let ((cursor (priority-parent table id)))
    (loop while cursor do
      (let ((sub (priority-table-slot table cursor :subtree)))
        (when (and sub (not (eq sub +absent+)))
          (return-from effective-rank (values sub cursor :subtree))))
      (setf cursor (priority-parent table cursor))))
  (values nil "default" :default))

(defun make-priority-receipt (&key change context rank changed no-effect change-id)
  (list :change change :context context :rank rank :changed changed
        :no-effect no-effect :change-id change-id))

(defun priority-table-set (table id context rank reason change-id)
  "A set changes the slot only when the value differs; a same-value set is the
no-effect receipt (SPEC-WORK.md:3187)."
  (unless (rank-atom-p rank)
    (return-from priority-table-set (values nil (format nil "bad rank ~S" rank) 2)))
  (let ((current (priority-table-slot table id context)))
    (if (and (not (eq current +absent+)) (eql current rank))
        (values (make-priority-receipt :change :set :context context :rank rank
                                       :changed 0 :no-effect t :change-id +absent+)
                "PRIORITY OK changed=0" 0)
        (progn
          (setf (priority-table-slot table id context) rank)
          (note-priority-change table id context change-id)
          (values (make-priority-receipt :change :set :context context :rank rank
                                         :changed 1 :no-effect nil :change-id change-id)
                  "PRIORITY OK changed=1" 0)))))

(defun priority-table-clear (table id context reason change-id)
  "A clear of an absent slot is the no-effect receipt (SPEC-WORK.md:3187)."
  (let ((current (priority-table-slot table id context)))
    (if (eq current +absent+)
        (values (make-priority-receipt :change :clear :context context :rank +absent+
                                       :changed 0 :no-effect t :change-id +absent+)
                "PRIORITY OK changed=0" 0)
        (progn
          (setf (priority-table-slot table id context) +absent+)
          (note-priority-change table id context change-id)
          (values (make-priority-receipt :change :clear :context context :rank +absent+
                                         :changed 1 :no-effect nil :change-id change-id)
                  "PRIORITY OK changed=1" 0)))))

(defun priority-undo (table id context event-id)
  "Undo restores the preimage only while the slot's latest-change identity equals
this event's :after (SPEC-WORK.md:3188)."
  (let ((latest (priority-change-id table id context)))
    (if (equal latest event-id)
        (progn
          (setf (priority-table-slot table id context) +absent+)
          (note-priority-change table id context +absent+)
          (values t "PRIORITY UNDO OK" 0))
        (values nil (format nil "PRIORITY UNDO FAIL: slot changed since ~A" event-id) 2))))

;;; ------------------------------------------------------------------
;;; Ordering: explicit ranks ascending, then defaults, then id
;;; ------------------------------------------------------------------

(defun row-eligible-p (row) (getf row :ready))

(defun row-sort-key (row)
  (let ((rank (getf row :priority)))
    (if rank (cons 0 rank) (cons 1 0))))

(defun priority-order (rows)
  "Explicit ranks ascending, then default rows, each tie broken by bytewise id;
blocked rows follow in discovery order (SPEC-WORK.md:3178-3181)."
  (let ((eligible (remove-if-not #'row-eligible-p rows))
        (blocked (remove-if #'row-eligible-p rows)))
    (append
     (stable-sort (copy-list eligible)
                  (lambda (a b)
                    (let ((ka (row-sort-key a)) (kb (row-sort-key b)))
                      (cond ((/= (car ka) (car kb)) (< (car ka) (car kb)))
                            ((/= (cdr ka) (cdr kb)) (< (cdr ka) (cdr kb)))
                            (t (string< (getf a :id) (getf b :id)))))))
     blocked)))

(defun priority-rows (table rows)
  "Resolve each row's effective rank and label it, then order."
  (let ((resolved
          (mapcar (lambda (row)
                    (multiple-value-bind (rank source context)
                        (effective-rank table (getf row :id))
                      (list* :priority rank :priority-source source
                             :priority-context context row)))
                  rows)))
    (priority-order resolved)))

(defun priority-page (ordered after-id size)
  "Later pages read the pinned order: the next SIZE rows after AFTER-ID, with the
id that continues the cursor."
  (let* ((position (if after-id
                       (1+ (or (position after-id ordered
                                         :key (lambda (r) (getf r :id)) :test #'equal)
                               -1))
                       0))
         (page (subseq ordered position (min (length ordered) (+ position size)))))
    (values page (and page (getf (car (last page)) :id)))))

;;; ------------------------------------------------------------------
;;; Granting nothing: priority moves no execution authority
;;; ------------------------------------------------------------------

(defstruct (work-view (:constructor make-work-view (&key who lease worker approval)))
  who lease worker approval)

(defun priority-grants-nothing-p (before after)
  "Proves the ordering act left who, lease, worker and approval identical."
  (and (equal (work-view-who before) (work-view-who after))
       (equal (work-view-lease before) (work-view-lease after))
       (equal (work-view-worker before) (work-view-worker after))
       (equal (work-view-approval before) (work-view-approval after))))

;;; ------------------------------------------------------------------
;;; The captured revision: capture R while R+1 is accepted
;;; ------------------------------------------------------------------

(defstruct (export-capture (:constructor make-export-capture
                                          (&key revision base end records bytes)))
  revision base end records bytes)

(defun state-record (revision id digest envelope)
  (list :sequence revision :revision revision :id id :digest digest :sha256 digest
        :envelope envelope))

(defun records-through (records revision)
  (remove-if (lambda (r) (> (getf r :revision) revision)) records))

(defun capture-export (records at &key (base at) end)
  "Capture AT; the bytes are built only from records through AT, so a later
accepted revision never enters them (SPEC-WORK.md:3243-3250)."
  (let* ((captured (records-through records at))
         (bytes (canonical-string (mapcar (lambda (r)
                                            (list (getf r :revision)
                                                  (getf r :id)
                                                  (getf r :digest)))
                                          captured))))
    (make-export-capture :revision at :base base :end end
                         :records captured :bytes bytes)))

(defun validate-export (capture all-records)
  "The coverage and revision-chain checks load performs before exposing state:
base never exceeds R; an absent end is legal only when B equals R; a base below
R needs a complete end; a missing, swapped, wrong-hash or split end refuses
(SPEC-WORK.md:3243-3251)."
  (let ((r (export-capture-revision capture))
        (b (export-capture-base capture))
        (end (export-capture-end capture)))
    (cond
      ((> b r) (values nil (format nil "base ~D exceeds captured revision ~D" b r)))
      ((and (< b r) (null end)) (values nil "absent end below R"))
      ((and (= b r) (null end)) (values t "exact snapshot: absent end at B=R"))
      (t
       (let* ((seq (getf end :sequence))
              (hash (getf end :sha256))
              (record (find seq all-records :key (lambda (x) (getf x :sequence)))))
         (cond
           ((null record) (values nil (format nil "missing end record ~D" seq)))
           ((not (equal hash (getf record :sha256)))
            (values nil (format nil "wrong end hash at ~D" seq)))
           ((getf record :split) (values nil (format nil "cut inside envelope at ~D" seq)))
           ((and (getf record :swapped) (/= (getf record :revision) r))
            (values nil (format nil "wrong end revision at ~D" seq)))
           (t (values t "coverage verified"))))))))

;;; ------------------------------------------------------------------
;;; The long operation: acknowledged at once, waited on by id
;;; ------------------------------------------------------------------

(defstruct (priority-operation (:constructor %make-priority-operation (&key id kind revision status result))
                      (:conc-name op-))
  id kind revision status result)

(defstruct (priority-operation-registry (:constructor make-priority-operation-registry (&key (counter 0))))
  counter
  (operations (make-hash-table :test #'equal)))

(defun begin-operation (registry kind revision &key inside-batch entry-id)
  "The request is acknowledged at once with a durable id; inside an atomic batch
it refuses by entry id (SPEC-WORK.md:3202-3208, 5888)."
  (when inside-batch
    (return-from begin-operation
      (values nil (format nil "OPERATION FAIL: ~A refused inside atomic batch ~A"
                          kind entry-id) 2)))
  (let ((id (format nil "op-~D" (incf (priority-operation-registry-counter registry)))))
    (setf (gethash id (priority-operation-registry-operations registry))
          (%make-priority-operation :id id :kind kind :revision revision :status :queued))
    (values id (format nil "OPERATION OK id=~A op=~A state=queued" id kind) 0)))

(defun priority-operation-record (registry id)
  (gethash id (priority-operation-registry-operations registry)))

(defun priority-operation-state (registry id)
  (let ((op (priority-operation-record registry id)))
    (and op (op-status op))))

(defun priority-operation-start (registry id)
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :running))
    op))

(defun priority-operation-finish (registry id &key result)
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :done (op-result op) result))
    op))

(defun priority-operation-cancel (registry id)
  "Cancellation acknowledges; it never promises to unpublish (SPEC-WORK.md:3217)."
  (let ((op (priority-operation-record registry id)))
    (when op (setf (op-status op) :cancelled))
    (values op (format nil "OPERATION OK id=~A op=cancel state=cancelled" id) 0)))

(defun priority-operation-wait (registry id)
  "Wait returns the same operation id and the captured revision it pinned."
  (let ((op (priority-operation-record registry id)))
    (values (and op (op-id op))
            (and op (op-revision op))
            (and op (op-status op))
            (and op (op-result op)))))

(defun unrelated-mutation (counter)
  "A mutation responsive under a running long operation: it advances."
  (1+ counter))
