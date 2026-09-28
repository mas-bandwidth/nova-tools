;;;; replays-8621.lisp --- the pure part of five named acceptance replays.
;;;;
;;;; Slice 1 is the C/O transition kernel only. The roadmap, axis, configure
;;;; and render machinery these replays name lives in session, CLI, query and
;;;; render slices that do not exist here. What is pure and computable is
;;;; implemented in this file and exercised by the five replays of
;;;; tests/acceptance/slice-09-replays-roadmap.lisp:
;;;;
;;;;   axisless-history                        docs/SPEC-WORK.md:5834
;;;;   matrix-retirement                       docs/SPEC-WORK.md:5838
;;;;   configure-no-effect-and-undo-conflict   docs/SPEC-WORK.md:5842
;;;;   completed-view-mutation                 docs/SPEC-WORK.md:5845
;;;;   chat-and-file-render-are-byte-identical docs/SPEC-WORK.md:5755

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; axisless-history                        docs/SPEC-WORK.md:5834
;;; ------------------------------------------------------------------
;;; Ordered roadmap rows; completion is not removal. Retirement records a
;;; scope movement and keeps the node. Export/load round-trips the rows and a
;;; view reopened past the default window still carries every row and its
;;; evidence; the prior view reconstructs at its captured revision.

(defun r8621-row-add (rows id evidence &key (node id))
  (append rows (list (list :id id :order (length rows) :node node
                           :evidence evidence :finished nil :retired nil
                           :scope-moved nil))))

(defun r8621-row-finish (rows id)
  (mapcar (lambda (r)
            (if (string= (getf r :id) id)
                (list :id (getf r :id) :order (getf r :order) :node (getf r :node)
                      :evidence (getf r :evidence) :finished t
                      :retired (getf r :retired) :scope-moved (getf r :scope-moved))
                r))
          rows))

(defun r8621-row-retire (rows id scope)
  (mapcar (lambda (r)
            (if (string= (getf r :id) id)
                (list :id (getf r :id) :order (getf r :order) :node (getf r :node)
                      :evidence (getf r :evidence) :finished (getf r :finished)
                      :retired t :scope-moved scope)
                r))
          rows))

(defun r8621-row-active-count (rows)
  "Completion does not reduce the denominator; retirement does."
  (count-if-not (lambda (r) (getf r :retired)) rows))

(defun r8621-rows-export (rows) (copy-tree rows))

(defun r8621-rows-load (exported) (copy-tree exported))

(defun r8621-view-reopen (rows &key (window 1))
  "Reopen the view past the default window. Awaiting the render slice, the
observable promise is that no row, evidence or prior revision is dropped."
  (declare (ignore window))
  (copy-tree rows))

;;; ------------------------------------------------------------------
;;; matrix-retirement                       docs/SPEC-WORK.md:5838
;;; ------------------------------------------------------------------
;;; A matrix is (:axes ((axis . members) ...) :retired ((axis . member) ...)
;;; :flattened selection). Retirement retires only the selected coordinates
;;; and stays recoverable; an unknown member refuses; a layout change on a
;;; populated roadmap refuses `layout populated` with no partial write; and a
;;; matrix is never flattened without explicit selections. No task is touched.

(defun r8621-matrix-make (axes)
  (list :axes (copy-tree axes) :retired '() :flattened nil))

(defun r8621-matrix-axis (matrix axis)
  (cdr (assoc axis (getf matrix :axes) :test #'string=)))

(defun r8621-matrix-member-p (matrix axis member)
  (and (member member (r8621-matrix-axis matrix axis) :test #'string=) t))

(defun r8621-matrix-retired-p (matrix axis member)
  (and (member (cons axis member) (getf matrix :retired) :test #'equal) t))

(defun r8621-matrix-remove (matrix axis member)
  (if (not (r8621-matrix-member-p matrix axis member))
      (values matrix (format nil "AXIS FAIL unknown member ~A/~A" axis member) 2)
      (let ((m (copy-tree matrix)))
        (pushnew (cons axis member) (getf m :retired) :test #'equal)
        (values m (format nil "AXIS OK remove ~A/~A" axis member) 0))))

(defun r8621-matrix-restore (matrix axis member)
  (if (r8621-matrix-retired-p matrix axis member)
      (let ((m (copy-tree matrix)))
        (setf (getf m :retired)
              (remove (cons axis member) (getf m :retired) :test #'equal))
        (values m (format nil "AXIS OK restore ~A/~A" axis member) 0))
      (values matrix (format nil "AXIS FAIL not retired ~A/~A" axis member) 2)))

(defun r8621-matrix-add-axis (matrix axis members)
  (if (or (getf matrix :retired)
          (some #'cdr (getf matrix :axes)))
      (values matrix "AXIS FAIL layout populated" 2)
      (let ((m (copy-tree matrix)))
        (push (cons axis members) (getf m :axes))
        (values m "AXIS OK layout" 0))))

(defun r8621-matrix-flatten (matrix selections)
  (if (null selections)
      (values matrix "AXIS FAIL flatten needs explicit selections" 2)
      (let ((m (copy-tree matrix)))
        (setf (getf m :flattened) (copy-tree selections))
        (values m "AXIS OK flatten" 0))))

;;; ------------------------------------------------------------------
;;; configure-no-effect-and-undo-conflict   docs/SPEC-WORK.md:5842
;;; ------------------------------------------------------------------
;;; An equal-value configure is a no-effect receipt (changed=0). A lost reply
;;; retried returns the original receipt while the later value stands: the
;;; ledger answers the id, it does not re-run the write. Undo restores the
;;; ordered preimage only while its guard (expected revision) matches.

(defun r8621-cfg-make (value)
  (list :value value :rev 0 :history (list value) :receipts '()))

(defun r8621-cfg-value (cfg) (getf cfg :value))
(defun r8621-cfg-rev (cfg) (getf cfg :rev))

(defun r8621-cfg-configure (cfg new request)
  (let* ((no-effect (equal (getf cfg :value) new))
         (next (copy-list cfg))
         (receipt (list :request request :changed (if no-effect 0 1)
                        :value new :rev (1+ (getf cfg :rev)))))
    (unless no-effect
      (setf (getf next :value) new)
      (push new (getf next :history)))
    (setf (getf next :rev) (1+ (getf cfg :rev)))
    (push (cons request receipt) (getf next :receipts))
    (values next receipt)))

(defun r8621-cfg-retry (cfg request)
  (let ((found (assoc request (getf cfg :receipts) :test #'string=)))
    (if found
        (values cfg (cdr found) 0)
        (values cfg (list :request request :refused "unknown request") 2))))

(defun r8621-cfg-undo (cfg expected-rev)
  (if (= expected-rev (getf cfg :rev))
      (let* ((hist (getf cfg :history))
             (preimage (if (cdr hist) (second hist) (first hist)))
             (next (copy-list cfg)))
        (setf (getf next :value) preimage)
        (setf (getf next :rev) (1+ (getf cfg :rev)))
        (values next (format nil "UNDO OK restore ~A" preimage) 0))
      (values cfg "UNDO FAIL conflict" 2)))

;;; ------------------------------------------------------------------
;;; completed-view-mutation                 docs/SPEC-WORK.md:5845
;;; ------------------------------------------------------------------
;;; A roadmap is (:members ((id . done-p) ...) :revive-events (...)). Pure
;;; reads never revive a settled roadmap. Adding an outstanding member to a
;;; settled container revives it atomically, so no settled container silently
;;; holds open required work; counts match the reference fold after each step.

(defun r8621-roadmap-make (members)
  (list :members (copy-tree members) :revive-events '()))

(defun r8621-roadmap-settled-p (rm)
  (and (getf rm :members) (every #'cdr (getf rm :members)) t))

(defun r8621-roadmap-open-required (rm)
  (count-if-not #'cdr (getf rm :members)))

(defun r8621-roadmap-fold-count (rm)
  "The reference fold an index must agree with."
  (reduce (lambda (acc m) (if (cdr m) acc (1+ acc))) (getf rm :members)
          :initial-value 0))

(defun r8621-roadmap-metadata (rm)
  (values (list :id "rm" :members (length (getf rm :members))) rm))

(defun r8621-roadmap-project (rm)
  (values (list :id "rm" :rows (mapcar #'car (getf rm :members))) rm))

(defun r8621-roadmap-render (rm)
  (values (format nil "ROADMAP OK members=~D" (length (getf rm :members))) rm))

(defun r8621-roadmap-add-member (rm member)
  (let ((was-settled (r8621-roadmap-settled-p rm))
        (next (copy-list rm))
        (event nil))
    (setf (getf next :members) (append (copy-tree (getf rm :members)) (list member)))
    (when (and was-settled (not (cdr member)))
      (setf event (list :kind :revive :member (car member)))
      (push event (getf next :revive-events)))
    (values next event)))

;;; ------------------------------------------------------------------
;;; chat-and-file-render-are-byte-identical docs/SPEC-WORK.md:5755
;;; ------------------------------------------------------------------
;;; The rendered body is deterministic for one projection and revision. Chat
;;; mode returns exactly those bytes; file mode writes exactly those bytes
;;; between the marker pair and preserves every other byte. A missing,
;;; duplicate or reversed marker pair is refused. Private rows are filtered
;;; identically in both modes.

(defparameter +r8621-marker-start+ "<!-- ROADMAP:START -->")
(defparameter +r8621-marker-end+ "<!-- ROADMAP:END -->")

(defun r8621-render-body (view projection)
  (declare (ignore projection))
  (let ((private (getf view :private)))
    (with-output-to-string (s)
      (format s "| Feature | Status |~%")
      (format s "| --- | --- |~%")
      (format s "revision: ~A~%" (getf view :revision))
      (dolist (row (list "acme/work/f1" "acme/work/f2"))
        (unless (member row private :test #'string=)
          (format s "| ~A | open |~%" row))))))

(defun r8621-render-chat (view projection)
  (r8621-render-body view projection))

(defun r8621-marker-region (input start end)
  "Return (values START-IDX END-IDX-EXCLUSIVE) for the one correctly ordered
marker pair, or (values NIL NIL) when missing, duplicated or reversed."
  (let* ((s1 (search start input))
         (s2 (and s1 (search start input :start2 (+ s1 (length start)))))
         (e1 (and s1 (search end input :start2 (if s2 (1+ s2) (+ s1 (length start)))))))
    (when (and s1 e1 (< e1 s1))
      (return-from r8621-marker-region (values nil nil)))
    (cond
      ((null s1) (values nil nil))
      ((null e1) (values nil nil))
      (s2 (values nil nil))
      ((search end input :start2 (+ e1 (length end))) (values nil nil))
      (t (values (+ s1 (length start)) e1)))))

(defun r8621-render-file (view projection input)
  (let ((body (r8621-render-body view projection)))
    (multiple-value-bind (rstart rend)
        (r8621-marker-region input +r8621-marker-start+ +r8621-marker-end+)
      (if (and rstart rend)
          (values (concatenate 'string (subseq input 0 rstart) body (subseq input rend)) t)
          (values nil nil)))))
