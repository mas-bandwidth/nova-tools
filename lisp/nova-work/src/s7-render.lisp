;;;; s7-render.lisp --- slice 7: render to chat and to a file's marker region.
;;;;
;;;; `render --view <id> --projection <id>` reads the stored projection, not a
;;;; remembered command line (SPEC-WORK.md:3107-3132). Chat and file mode are
;;;; byte-identical for one projection and revision; file mode changes only the
;;;; selected marker region and preserves every other byte; a missing, duplicate
;;;; or reversed marker pair is refused, as is a target outside the configured
;;;; permitted roots. Pure functions; no filesystem write happens here (a
;;;; later wiring card owns the atomic replace and the hash recheck).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The rendered body: a deterministic markdown table for the stored selection.
;;; ------------------------------------------------------------------

(defun %s7-projection-of (view projection-id)
  (find projection-id (s7-roadmap-projections view) :key #'s7-proj-id :test #'string=))

(defun s7-render-body (view projection)
  "The bytes one projection and revision render. Chat mode returns exactly this;
  file mode writes exactly this between the markers."
  (let* ((row-axis (s7-proj-row-axis projection))
         (column-axis (s7-proj-column-axis projection))
         (rows (cond
                 ((and row-axis (not (absentp row-axis)))
                  (s7-axis-members (or (find row-axis (s7-roadmap-axes view)
                                          :key #'s7-axis-id :test #'string=)
                                    (make-s7-axis :members '()))))
                 (t (s7-roadmap-members view))))
         (columns (cond
                    ((and column-axis (not (absentp column-axis)))
                     (s7-axis-members (or (find column-axis (s7-roadmap-axes view)
                                             :key #'s7-axis-id :test #'string=)
                                       (make-s7-axis :members '()))))
                    (t '()))))
    (with-output-to-string (s)
      (format s "| Feature | Status |~%")
      (format s "| --- | --- |~%")
      (dolist (r rows)
        (let* ((state (cond
                        ((member r (s7-roadmap-closed view) :test #'string=) "done")
                        ((member r (s7-roadmap-retired view) :test #'string=) "retired")
                        (t "open")))
               (marks (loop for c in columns
                            for slot = (%s7-cell-find view r c)
                            when (and slot (not (absentp (s7-cell-ref slot))))
                              collect (format nil "~A=~A" c
                                              (if (member (s7-cell-ref slot) (s7-roadmap-closed view)
                                                          :test #'string=)
                                                  "done" "open")))))
          (format s "| ~A | ~A~@[ (~{~A~^, ~})~] |~%"
                  r state marks))))))

(defun s7-render-chat (view &key projection row-axis column-axis fixed)
  "The chat bytes. With a stored projection the projection's display selection is
  read; without one a matrix names its selection. Returns the bytes or NIL."
  (declare (ignore fixed))
  (let ((proj (and projection (%s7-projection-of view projection))))
    (cond
      (proj (s7-render-body view proj))
      (row-axis
       (s7-render-body view (make-s7-projection :row-axis row-axis :column-axis column-axis
                                          :fixed '())))
      (t
       (s7-render-body view (make-s7-projection :row-axis +absent+ :column-axis +absent+
                                          :fixed '()))))))

;;; ------------------------------------------------------------------
;;; Marker-region surgery. Every other byte is preserved.
;;; ------------------------------------------------------------------

(defun %s7-marker-region (input start end)
  "Return (values START-IDX END-IDX-EXCLUSIVE) for the single, correctly ordered
  marker pair, or (values NIL NIL) when the pair is missing, duplicated or
  reversed."
  (let* ((s1 (search start input))
         (s2 (and s1 (search start input :start2 (+ s1 (length start)))))
         (e1 (and s1 (search end input :start2 (if s2 (1+ s2) (+ s1 (length start))))))
         (e2 (and e1 (search end input :start2 (+ e1 (length end))))))
    ;; Reversed: the first END appears before the first START.
    (when (and s1 e1 (< e1 s1))
      (return-from %s7-marker-region (values nil nil)))
    (cond
      ((null s1) (values nil nil))
      ((null e1) (values nil nil))
      (s2 (values nil nil))
      (e2 (values nil nil))
      (t (values (+ s1 (length start)) e1)))))

(defun %s7-path-escapes-p (path roots)
  "T when PATH escapes every permitted ROOT, or names a root that is not among
  them. Roots are opaque ids; a `..` that climbs above the root refuses."
  (let ((comps (remove "" (uiop:split-string path :separator "/") :test #'string=)))
    (or (null comps)
        (let ((root (first comps)))
          (or (not (member root roots :test #'string=))
              (let ((depth 0))
                (block out
                  (dolist (c comps)
                    (cond
                      ((string= c "..")
                       (if (zerop depth) (return-from out t) (decf depth)))
                      ((string= c ".") nil)
                      (t (incf depth))))
                  nil)))))))

(defun %s7-resolve-path (view projection path)
  "The effective path under the roots check. A stored projection's :path is
  relative to its :root; a `path` override names the whole effective path.
  Returns NIL when outside the permitted roots or escaping them."
  (let* ((roots (s7-roadmap-permitted-roots view))
         (root (s7-proj-root projection))
         (rel (s7-proj-path projection))
         (full (or path
                   (if (and (stringp root) (plusp (length root)))
                       (format nil "~A/~A" root rel)
                       rel))))
    (when (%s7-path-escapes-p full roots)
      (return-from %s7-resolve-path nil))
    full))

(defun s7-render-file (view &key projection input path)
  "File mode: replace the marker region with the render bytes, preserving every
  other byte. Returns the new contents, or NIL on a missing, duplicate or reversed
  marker pair or a target outside the permitted roots."
  (let ((proj (and projection (%s7-projection-of view projection))))
    (unless proj (return-from s7-render-file nil))
    (unless (%s7-resolve-path view proj path) (return-from s7-render-file nil))
    (multiple-value-bind (rstart rend) (%s7-marker-region input (s7-proj-start proj) (s7-proj-end proj))
      (unless (and rstart rend) (return-from s7-render-file nil))
      (let ((body (s7-render-body view proj)))
        (concatenate 'string
                     (subseq input 0 rstart)
                     body
                     (subseq input rend))))))

(defun s7-render-check (view &key projection path)
  "`render --check`: verify the target is inside the permitted roots without
  writing. Returns (values OK-P LINE)."
  (let* ((proj (and projection (%s7-projection-of view projection)))
         (id (s7-roadmap-id view)))
    (unless proj
      (return-from s7-render-check (values nil (format nil "RENDER FAIL view=~A projection=~A: no such projection" id projection))))
    (unless (%s7-resolve-path view proj path)
      (return-from s7-render-check
        (values nil (format nil "RENDER FAIL view=~A projection=~A: target outside the permitted roots refused" id projection))))
    (values t (format nil "RENDER OK view=~A projection=~A target=~A:~A"
                      id projection (s7-proj-repo proj) (s7-proj-path proj)))))
