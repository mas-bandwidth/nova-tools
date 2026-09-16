;;;; replays-bugs.lisp --- the pure part of the six bug replays.
;;;;
;;;; docs/SPEC-WORK.md:1840-1923 adds one node kind (:bug), three fields
;;;; (:found-during, :evidence, :test), one validator rule (19), one count
;;;; (bugs=<open>/<fixed>) and seven replays. One replay,
;;;; bug-at-epic-level-is-legal, is already answered by the kernel's treatment
;;;; of :bug as a leaf beside :task. The six named here concern verbs slice 1
;;;; does not ship (node add, who/check/stale, decompose, roadmap parity), so
;;;; this file carries the pure functions and records those replays rest on,
;;;; tested in their own file, and lists the wiring owed in RESULT.md rather
;;;; than editing the kernel, session or state.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The bug record and a bug index.
;;; ------------------------------------------------------------------

(defstruct (nbug (:conc-name bg-))
  id title found-during test status source evidence acceptance)

(defstruct (bug-index (:conc-name bi-))
  (table (make-hash-table :test #'equal))
  (order ()))

(defun bug-list (index)
  "The bugs in insertion order."
  (bi-order index))

(defun bug-get (index id)
  (gethash id (bi-table index)))

(defun bug-put (index bug)
  "Add or replace BUG in INDEX, preserving insertion order. An added bug is
  open; a replace keeps the caller's status, which is how fixed/revived move."
  (unless (gethash (bg-id bug) (bi-table index))
    (setf (bi-order index) (append (bi-order index) (list (bg-id bug)))))
  (setf (gethash (bg-id bug) (bi-table index)) bug)
  index)

(defun bug-set-status (index id status)
  "Move the bug at ID to STATUS, answering a fresh index."
  (let* ((bug (bug-get index id))
         (copy (make-nbug
                :id (bg-id bug) :title (bg-title bug)
                :found-during (bg-found-during bug) :test (bg-test bug)
                :status status :source (bg-source bug)
                :evidence (bg-evidence bug) :acceptance (bg-acceptance bug))))
    (bug-put index copy)))

;;; ------------------------------------------------------------------
;;; `node add --type bug` -- rule 19 names a missing field, rule 2 a dangling.
;;; ------------------------------------------------------------------

(defun bug-add-refusal (request known-ids)
  "Validate a bug-add REQUEST (a plist with :id, :found-during, :test, :title,
  :source). Answer NIL when admissible, else (values exit-code line)."
  (let ((found (getf request :found-during)))
    (cond
      ((or (null found) (and (stringp found) (zerop (length found))))
       (values 1 "NODE FAIL: found-during is required for a bug"))
      ((not (member found known-ids :test #'string=))
       (values 1 (format nil "rule 2: ~A names no node" found)))
      (t (values nil nil)))))

(defun add-bug (index &key id found-during test title source known-ids)
  "Answer (values ok-p line exit-code index'). On refusal INDEX is returned
  unchanged (eq) and nothing is written; on admission a fresh index carries the
  open bug. known-ids is the set of existing node ids rule 2 checks against."
  (multiple-value-bind (code line) (bug-add-refusal
                                    (list :id id :found-during found-during
                                          :test test :title title :source source)
                                    known-ids)
    (if code
        (values nil line code index)
        (let ((bug (make-nbug :id id :title (or title "") :found-during found-during
                              :test test :status :open :source (or source ""))))
          (values t (format nil "NODE OK kind=bug found-during=~A" found-during)
                  0 (bug-put index bug))))))

;;; ------------------------------------------------------------------
;;; rule 19 -- a bug without its lock cannot reach :done. `correct` voids it.
;;; ------------------------------------------------------------------

(defun bug-close-verdict (bug verified-tests generation)
  "Answer NIL when BUG may reach :done, else a rule-19 reason string. The bug's
  :test must name a test in VERIFIED-TESTS (an alist of (name . generation))
  that is verified at GENERATION. A bug with another name's passing test, a test
  at an older generation, or no :test at all is refused."
  (let* ((test (bg-test bug))
         (v (and test (assoc test verified-tests :test #'string=))))
    (cond
      ((or (null test) (and (stringp test) (zerop (length test))))
       (format nil "rule 19: no ~S locks this fix" test))
      ((null v)
       (format nil "rule 19: the named test ~S is not among the verified tests" test))
      ((not (= generation (cdr v)))
       (format nil "rule 19: ~S is not verified at the current generation" test))
      (t nil))))

(defun correct-voids (verified-tests name)
  "A `correct` on the acceptance subject with this test leaves the lock empty:
  it removes NAME from VERIFIED-TESTS until it is re-verified."
  (remove-if (lambda (entry) (string= (car entry) name)) verified-tests))

;;; ------------------------------------------------------------------
;;; An open bug blocks its parent's done; it is no member of any required set.
;;; ------------------------------------------------------------------

(defstruct (scope-verdict (:conc-name sv-))
  state green percent scope-event revision-moved)

(defun feature-percent (required total)
  "The percentage from the required set only; a bug never enters it."
  (if (zerop total) 0 (round (* 100 required) total)))

(defun feature-verdict (&key required-done required-open open-bugs)
  "A feature's derived state. required-done/required-open read its own required
  leaves; open-bugs counts open bugs beneath it, directly or on a descendant.
  An open bug keeps the feature :unknown and not green, and raises no percent;
  its percent is the leaves-only denominator, unchanged by the bug."
  (let ((percent (feature-percent required-done (+ required-done required-open))))
    (if (plusp open-bugs)
        (make-scope-verdict :state :unknown :green nil :percent percent
                            :scope-event nil :revision-moved nil)
        (make-scope-verdict
         :state (if (> required-done 0) :done :unknown)
         :green (> required-done 0)
         :percent percent
         #| settling a bug writes no scope event and moves no revision |#
         :scope-event nil :revision-moved nil))))

;;; ------------------------------------------------------------------
;;; who/check/stale -- bugs=<open>/<fixed> on every line, beside the tree.
;;; ------------------------------------------------------------------

(defun bug-tally (index)
  "Answer (values open fixed) across INDEX. Only :open and :fixed count; a
  cancelled or superseded bug is neither."
  (let ((open 0) (fixed 0))
    (dolist (id (bi-order index))
      (let ((status (bg-status (bug-get index id))))
        (case status
          (:open (incf open))
          (:fixed (incf fixed)))))
    (values open fixed)))

(defun who-line (scope open fixed)
  "The counting row, which carries only the bug count beside its scope -- never
  done=, required=, rows= or since-baseline=."
  (format nil "~A bugs=~D/~D" scope open fixed))

;;; ------------------------------------------------------------------
;;; decompose --into bug:<id> -- a minted bug carries :found-during and its
;;; acceptance, enters no required set, and moves the scope revision by one.
;;; ------------------------------------------------------------------

(defstruct (feature-model (:conc-name fm-))
  id required scope-revision)

(defstruct (split (:conc-name sp-))
  children minted-bugs scope-revision-delta required-unchanged-p)

(defun %minted-bug-refusal (into)
  "NIL when the child spec is admissible, else a reason string. A bug child
  without --found-during refuses the whole decomposition."
  (let ((target (getf into :into)))
    (when (and (stringp target) (plusp (length target)) (char= #\b (char target 0)))
      (let ((found (getf into :found-during)))
        (when (or (null found) (and (stringp found) (zerop (length found))))
          (format nil "bug child ~S without --found-during" target))))))

(defun decompose (feature into)
  "Answer (values split-or-nil reason-or-nil). INTO is a list of child specs;
  a `bug:<id>` spec carries :found-during and :acceptance. One bug child without
  :found-during refuses the whole call and writes nothing."
  (dolist (spec into)
    (let ((refusal (%minted-bug-refusal spec)))
      (when refusal (return-from decompose (values nil refusal)))))
  (let ((minted '()))
    (dolist (spec into)
      (when (char= #\b (char (getf spec :into) 0))
        (push (make-nbug
               :id (subseq (getf spec :into) 4)
               :found-during (getf spec :found-during)
               :test (getf spec :test)
               :status :open
               :acceptance (getf spec :acceptance))
              minted)))
    (values (make-split
             :children (copy-list into)
             :minted-bugs (nreverse minted)
             :scope-revision-delta 1
             :required-unchanged-p t)
            nil)))

;;; ------------------------------------------------------------------
;;; The roadmap lisp -- :bugs at any level, parity, and the bare-symbol refusal.
;;; ------------------------------------------------------------------

(defun %utf8-bytes (text)
  (loop for ch across text summing
        (let ((code (char-code ch)))
          (cond ((<= code #x7f) 1)
                ((<= code #x7ff) 2)
                ((<= code #xffff) 3)
                (t 4)))))

(defun form-depth (x)
  (if (atom x) 0 (1+ (reduce #'max (mapcar #'form-depth x) :initial-value 0))))

(defun form-node-count (x)
  (if (atom x) 1 (1+ (reduce #'+ (mapcar #'form-node-count x) :initial-value 0))))

(defun parse-roadmap (text max-bytes max-depth max-nodes)
  "Read one roadmap under the reader's three bounds (SPEC-WORK.md:837). Answer
  (values exit-code form-or-reason). A reader refusal -- a bare `(bug ...)`
  symbol among them -- is exit 2 naming the byte offset; a bound past is exit 2
  naming the bound."
  (let ((form (handler-case (read-restricted text)
                (restricted-data-violation (c)
                  (return-from parse-roadmap (values 2 (princ-to-string c)))))))
    (let ((bytes (%utf8-bytes text))
          (depth (form-depth form))
          (nodes (form-node-count form)))
      (cond
        ((> bytes max-bytes) (values 2 (format nil "max-bytes: ~D > ~D" bytes max-bytes)))
        ((> depth max-depth) (values 2 (format nil "max-depth: ~D > ~D" depth max-depth)))
        ((> nodes max-nodes) (values 2 (format nil "max-nodes: ~D > ~D" nodes max-nodes)))
        (t (values 0 form))))))

(defun collect-bug-entries (form)
  "Every :bugs entry at any level (root, epic, feature, item). Containers are
  the plist keys :epics, :features and :items; each level carries an optional
  :bugs (...) list at its own level."
  (let ((acc '()))
    (labels ((bugs-at (subform)
               (let ((bs (getf subform :bugs)))
                 (when bs (setf acc (append acc (copy-list bs))))))
             (walk (subform)
               (when (listp subform)
                 (bugs-at subform)
                 (dolist (key '(:epics :features :items))
                   (dolist (child (getf subform key))
                     (walk child))))))
      (walk form))
    acc))

(defun roadmap-parity (form)
  "Answer (values equal-p line) where line prints bugs=<open>/<fixed> beside
  the feature and item counts. The bug count folds only :bugs entries -- never
  :current-features or :current-acceptance-items -- and is checked against the
  root's :current-bugs (open fixed)."
  (let* ((current (getf form :current-bugs))
         (c-open (first current)) (c-fixed (second current))
         (open 0) (fixed 0))
    (dolist (entry (collect-bug-entries form))
      (if (string= "open" (getf entry :status)) (incf open) (incf fixed)))
    (values
     (and (= c-open open) (= c-fixed fixed))
     (format nil "bugs=~D/~D features=~D items=~D"
             open fixed
             (getf form :current-features 0)
             (getf form :current-acceptance-items 0)))))
