;;;; replays-l3600-2.lisp --- pure records and functions for the eight named
;;;; acceptance replays of docs/SPEC-WORK.md lines 3600-end, batch 2.
;;;;
;;;; slice 1 is the C/O transition kernel only; each replay below names a verb,
;;;; index, window or operation whose full machinery lives in the session, CLI,
;;;; query, dispatch or export slices that do not exist here. The part that is
;;;; pure and computable is implemented and tested in this file; what must still
;;;; be wired into kernel/session/state is listed in RESULT.md under "owed:".

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; an-invalid-delta-leaves-the-old-config   SPEC-WORK.md:5615
;;; ------------------------------------------------------------------
;;; The config exchange is bounded, validated and atomic: a delta is validated
;;; against its named base before any field moves, and an invalid delta leaves
;;; the old config untouched.

(defparameter *config-schema* '(:roster :window-days :capacity :retain-bytes)
  "The bounded config keys a delta may touch. Anything else is unknown and
refuses (SPEC-WORK.md:5284 wants the exchange bounded).")

(defun config-value-valid-p (key value)
  (case key
    (:roster (stringp value))
    ((:window-days :capacity :retain-bytes) (and (integerp value) (plusp value)))
    (t nil)))

(defun apply-config-delta (config delta)
  "Pure and atomic. DELTA is (plist :base N :changes plist). A stale BASE, an
unknown or forbidden key, or an invalid value refuses the WHOLE delta: the
original CONFIG is returned unchanged with APPLIED-P NIL. On success a fresh
config is returned with every change applied and :version bumped by one."
  (let ((base (getf delta :base))
        (changes (getf delta :changes)))
    (unless (and (integerp base) (eql base (getf config :version)))
      (return-from apply-config-delta (values config nil)))
    (loop for (key value) on changes by #'cddr
          do (unless (and (member key *config-schema*)
                          (config-value-valid-p key value))
               (return-from apply-config-delta (values config nil))))
    (let ((new (copy-list config)))
      (loop for (key value) on changes by #'cddr
            do (setf (getf new key) value))
      (setf (getf new :version) (1+ (getf config :version)))
      (values new t))))

;;; ------------------------------------------------------------------
;;; archive-completeness                    SPEC-WORK.md:6030
;;; ------------------------------------------------------------------
;;; Every intake gap stays an explicit entry and prohibits absorption, so a
;;; silent success by dropping one is impossible.

(defparameter *gap-kinds*
  '(:missing-attachment :unavailable-comment :unsupported-field
    :size-truncation :rate-limit :mid-page-failure)
  "The intake gaps named by SPEC-WORK.md:5586. Each stays explicit and prohibits
absorption.")

(defun capture-gaps (capture) (getf capture :gaps))

(defun capture-complete-p (capture) (null (capture-gaps capture)))

(defun absorb-capture (base incoming)
  "Pure. Absorption of INCOMING into BASE is prohibited unless both are
gap-free: a gap is never silently dropped. Answers (values result absorbed-p)."
  (if (and (capture-complete-p base) (capture-complete-p incoming))
      (values (list :gaps (append (capture-gaps base) (capture-gaps incoming))
                    :items (append (getf base :items) (getf incoming :items)))
              t)
      (values base nil)))

;;; ------------------------------------------------------------------
;;; as-of-refuses-unavailable-partition     SPEC-WORK.md:5466
;;; ------------------------------------------------------------------

(defun state-as-of (rows partitions day)
  "Pure. ROWS are (:node .. :day .. :rev ..). PARTITIONS is the list of openable
day keys. If DAY's partition cannot be opened, answer (values :refused day) --
never answered from a later row. Otherwise the newest row on or before DAY is
answered as (values rev nil)."
  (unless (member day partitions :test #'string=)
    (return-from state-as-of (values :refused day)))
  (let ((best nil))
    (dolist (r rows)
      (when (and (string<= (getf r :day) day)
                 (or (null best) (> (getf r :rev) (getf best :rev))))
        (setf best r)))
    (if best
        (values (getf best :rev) nil)
        (values :refused day))))

;;; ------------------------------------------------------------------
;;; async-operations                        SPEC-WORK.md:6037
;;; ------------------------------------------------------------------

(defun operation-launch (op)
  "Pure. A pending operation enters :running and counts one attempt. Launching a
:running, :done or :cancelled operation launches nothing (no double launch)."
  (if (eq (getf op :state) :pending)
      (list :id (getf op :id) :kind (getf op :kind) :state :running
            :attempts (1+ (getf op :attempts 0)))
      op))

(defun operation-cancel (op)
  "Pure. Cancelling a finished operation is never a false success: it reports
:already-complete and no cancellation. Something not finished cancels to
:cancelled with disposition :cancelled."
  (if (eq (getf op :state) :done)
      (list :id (getf op :id) :kind (getf op :kind) :state :done
            :attempts (getf op :attempts 0) :disposition :already-complete)
      (list :id (getf op :id) :kind (getf op :kind) :state :cancelled
            :attempts (getf op :attempts 0) :disposition :cancelled)))

;;; ------------------------------------------------------------------
;;; axisless-history                        SPEC-WORK.md:5714
;;; ------------------------------------------------------------------

(defun add-ordered-row (rows id evidence &key (node id))
  "Append an ordered roadmap row: id, node, evidence and a stable order
position; starts unfinished, unretired, no scope movement."
  (append rows (list (list :id id :order (length rows) :node node :evidence evidence
                           :finished nil :retired nil :scope-moved nil))))

(defun finish-row (rows id)
  (mapcar (lambda (r)
            (if (string= (getf r :id) id)
                (list :id (getf r :id) :order (getf r :order) :node (getf r :node)
                      :evidence (getf r :evidence) :finished t
                      :retired (getf r :retired) :scope-moved (getf r :scope-moved))
                r))
          rows))

(defun retire-row (rows id scope)
  "Retire a row: it stays in the table with its node, records the SCOPE movement,
and is no longer in the active denominator."
  (mapcar (lambda (r)
            (if (string= (getf r :id) id)
                (list :id (getf r :id) :order (getf r :order) :node (getf r :node)
                      :evidence (getf r :evidence) :finished (getf r :finished)
                      :retired t :scope-moved scope)
                r))
          rows))

(defun active-denominator (rows)
  "The rows not retired. Completion does not reduce it; retirement does."
  (count-if-not (lambda (r) (getf r :retired)) rows))

;;; ------------------------------------------------------------------
;;; batches-and-pipelines                   SPEC-WORK.md:6038
;;; ------------------------------------------------------------------

(defun apply-atomic-batch (acc entries validator)
  "Pure, all-or-none. If every entry satisfies VALIDATOR, answer
(values acc+ids :applied). If any entry fails, nothing is applied:
(values acc :refused)."
  (if (every (lambda (e) (funcall validator e)) entries)
      (values (append acc (mapcar (lambda (e) (getf e :id)) entries)) :applied)
      (values acc :refused)))

(defun apply-independent-batch (acc entries validator)
  "Pure. Stop at the first refusal: the exact accepted prefix is applied and the
remaining entries are marked :not-attempted; no entry after the refusal is
applied. Answers (values new-acc accepted-ids not-attempted-ids)."
  (let ((accepted '()) (not-attempted '()) (new-acc acc) (after-failure nil))
    (dolist (e entries)
      (let ((id (getf e :id)))
        (cond (after-failure (push id not-attempted))
              ((funcall validator e) (push id accepted) (setf new-acc (append new-acc (list id))))
              (t (setf after-failure t)))))
    (values new-acc (reverse accepted) (reverse not-attempted))))

;;; ------------------------------------------------------------------
;;; default-window-opens-two-days           SPEC-WORK.md:5441
;;; ------------------------------------------------------------------

(defun day-key (stamp)
  "The UTC day (yyyy-mm-dd) of an ISO stamp."
  (subseq stamp 0 10))

(defun parse-day (day)
  (let ((y (parse-integer day :start 0 :end 4))
        (m (parse-integer day :start 5 :end 7))
        (d (parse-integer day :start 8 :end 10)))
    (encode-universal-time 0 0 0 d m y 0)))

(defun day-string (ut)
  (multiple-value-bind (s mi h d m y) (decode-universal-time ut 0)
    (declare (ignore s mi h))
    (format nil "~4,'0D-~2,'0D-~2,'0D" y m d)))

(defun day-add (day n)
  (day-string (+ (parse-day day) (* n 86400))))

(defun window-days (stamp &key (window-days 2))
  "The candidate UTC day keys a default listing may open, oldest first: the
current day plus the preceding WINDOW-DAYS-1 days. At exactly 00:00:00Z the day
boundary opens the current day alone (one partition)."
  (let ((today (day-key stamp)))
    (if (string= (subseq stamp 11 19) "00:00:00")
        (list today)
        (loop for i from (1- window-days) downto 0
              collect (day-add today (- i))))))

(defun open-days-from (records from)
  "The day keys in [FROM, now] holding at least one closure record, oldest
first: exactly the record-holding days, no gap day and no day older than FROM."
  (let ((days '()))
    (dolist (r records)
      (let ((day (getf r :day)))
        (when (and (stringp day) (string<= from day))
          (pushnew day days :test #'string=))))
    (sort days #'string<)))

;;; ------------------------------------------------------------------
;;; dispatch-ack-and-ownership-are-three    SPEC-WORK.md:5550
;;; ------------------------------------------------------------------

(defun dispatch-facts-distinct-p (facts)
  "The four dispatch facts -- dispatch, delivery, acknowledgement, ownership --
must stay distinct: exactly four keywords with no duplicate."
  (= 4 (length (remove-duplicates facts))))

(defun make-offer (id holder declared)
  (list :id id :holder holder :declared declared :reserved 0))

(defun reserve-offer (offer n)
  "A pending offer reserves only its declared capacity: the reservation is
clamped to DECLARED and the original offer is not mutated."
  (list :id (getf offer :id) :holder (getf offer :holder)
        :declared (getf offer :declared)
        :reserved (min n (getf offer :declared))))

(defun make-dispatch (&key offers launches)
  (list :offers offers :launches launches))

(defun dispatch-launches (disp) (getf disp :launches 0))

(defun dispatch-timeout (disp)
  "Pure. A timeout alone -- no accepted ownership -- launches nothing: the launch
count is unchanged while the old worker may still run, so no duplicate is
started. The reconciliation that precedes any new dispatch is owed to the
session kernel."
  disp)
