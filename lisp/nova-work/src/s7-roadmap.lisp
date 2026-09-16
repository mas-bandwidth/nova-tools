;;;; s7-roadmap.lisp --- slice 7: the roadmap record, its verbs, counting and
;;;; the `percent` / `roadmap` queries.
;;;;
;;;; This slice is implemented as pure functions and records. Nothing here edits
;;;; kernel.lisp, session.lisp, state.lisp or event.lisp, and nothing calls the
;;;; command thread; a later wiring card connects these functions to it. The
;;;; spec read is docs/SPEC-WORK.md:890-1189 (the kinds), :1925-2000 (counting)
;;;; and :3048-3105 (the verbs and `ROADMAP OK`/`AXIS OK` grammar).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Records. A roadmap is a node plus a view record; here only the view record
;;; exists, because the node lives in O and this slice does not touch it.
;;; ------------------------------------------------------------------

(defstruct (s7-roadmap
             (:constructor make-s7-roadmap
                 (&key id title under row-kind aggregation completion-policy
                       axes members permitted-roots projections cells
                       scope-revision baseline-members since-baseline
                       closed retired archived)))
  id                          ; "<owner>/<name>"
  title
  under                       ; (:node "<id>")
  row-kind                    ; :feature (default), :epic or :work-set
  aggregation                 ; :required-members (default) | :all-members | :leaves
  completion-policy           ; :all-required-features (the only policy)
  axes                        ; ordered list of AXIS
  members                     ; ordered row ids (first-axis members, or axisless rows)
  permitted-roots             ; opaque root-id strings
  projections                 ; list of PROJECTION
  cells                       ; list of CELL-SLOT, keyed by (row . column)
  scope-revision              ; the count of scope events
  baseline-members            ; the membership its last :baseline recorded
  since-baseline              ; gross :discovery count since the last :baseline
  closed                      ; row ids whose feature work settled done
  retired                     ; row ids removed/cancelled/superseded (out of the set)
  archived)                   ; row ids clipped past the retention boundary

(defstruct (s7-axis (:conc-name s7-axis-)
                 (:constructor make-s7-axis (&key id members)))
  id                          ; axis name
  members)                    ; ordered member ids on this axis

(defstruct (s7-cell-slot (:conc-name s7-cell-)
                      (:constructor make-s7-cell-slot (&key row column ref
                                                          out-of-scope in-scope)))
  row                          ; row id (coordinate)
  column                       ; column id (coordinate)
  ref                          ; node id the cell references, or (:absent)
  out-of-scope                 ; t when a recorded :scope took it out
  in-scope)                    ; t when a recorded :scope put it back (default)

(defstruct (s7-projection (:conc-name s7-proj-)
                       (:constructor make-s7-projection
                           (&key id root repo path start end policy
                                 row-axis column-axis fixed)))
  id                           ; projection id
  root                         ; permitted-root id
  repo                         ; "<owner>/<name>"
  path                         ; relative path
  start                        ; opening marker
  end                          ; closing marker
  policy                       ; :markdown-table (the only policy)
  row-axis                     ; axis id or (:absent)
  column-axis                  ; axis id or (:absent)
  fixed)                       ; ((<s7-axis-id> <member-id>) ...)

;;; ------------------------------------------------------------------
;;; Small helpers.
;;; ------------------------------------------------------------------

(defun %s7-str (x)
  (and (stringp x) x))

(defun %s7-roadmap-fail (node reason)
  (format nil "ROADMAP FAIL node=~A: ~A" node reason))

(defun %s7-axis-fail (node reason)
  (format nil "AXIS FAIL node=~A: ~A" node reason))

(defun %s7-roadmap-ok-line (view change request changed)
  (format nil "ROADMAP OK id=- request=~A node=~A rev=~D pushed=- change=~A changed=~D"
          request (s7-roadmap-id view) (s7-roadmap-scope-revision view)
          (string-downcase (symbol-name change)) changed))

(defun %s7-axis-ok-line (view request change member cells)
  (format nil "AXIS OK id=- request=~A node=~A rev=~D pushed=- change=~A member=~A cells=~D"
          request (s7-roadmap-id view) (s7-roadmap-scope-revision view)
          (string-downcase (symbol-name change)) member cells))

(defun %s7-cell-find (view row column)
  (find-if (lambda (c) (and (equal (s7-cell-row c) row) (equal (s7-cell-column c) column)))
           (s7-roadmap-cells view)))

(defun %s7-live-members (view)
  "The required set: members of the first axis (or the axisless :members) whose
  node is live --- not removed, cancelled or superseded."
  (remove-if (lambda (m) (member m (s7-roadmap-retired view) :test #'string=))
             (s7-roadmap-members view)))

;;; ------------------------------------------------------------------
;;; Verbs. Each returns (values OK-P LINE VIEW) or (values NIL LINE NIL).
;;; ------------------------------------------------------------------

(defun s7-roadmap-create (request)
  "roadmap create. One parser, one record. Refuses unknown row-kind / aggregation
  and a missing id or title rather than guessing."
  (let* ((id (%s7-str (getf request :node)))
         (title (%s7-str (getf request :title)))
         (row-kind (or (getf request :row-kind) :feature))
         (aggregation (or (getf request :aggregation) :required-members))
         (axes (getf request :axes '()))
         (members (getf request :members '()))
         (permitted-roots (getf request :permitted-roots '()))
         (under (getf request :under))
         (request-id (getf request :request)))
    (cond
      ((not id) (values nil (%s7-roadmap-fail "-" "roadmap create needs a node id") nil))
      ((not (member row-kind '(:feature :epic :work-set)))
       (values nil (%s7-roadmap-fail id (format nil "unknown row-kind ~A" (string-downcase (symbol-name row-kind)))) nil))
      ((not (member aggregation '(:required-members :all-members :leaves)))
       (values nil (%s7-roadmap-fail id (format nil "unknown aggregation ~A" (string-downcase (symbol-name aggregation)))) nil))
      (t
       (let ((view (make-s7-roadmap
                    :id id :title (or title "") :under under
                    :row-kind row-kind :aggregation aggregation
                    :completion-policy :all-required-features
                    :axes (mapcar (lambda (a)
                                    (make-s7-axis :id (getf a :id) :members (getf a :members '())))
                                  axes)
                    :members members
                    :permitted-roots permitted-roots
                    :projections '() :cells '()
                    :scope-revision 1
                    :baseline-members members
                    :since-baseline 0
                    :closed '() :retired '() :archived '())))
         (values t (%s7-roadmap-ok-line view :create request-id 1) view))))))

(defun s7-roadmap-configure (view request)
  "roadmap configure. Patches are (:keep) or (:set V). Refuses layout changes on
  a populated roadmap and an all-keep request. Returns a fresh view."
  (let ((row-kind-patch (getf request :row-kind-patch '(:keep)))
        (aggregation-patch (getf request :aggregation-patch '(:keep)))
        (axes-patch (getf request :axes-patch '(:keep)))
        (roots-patch (getf request :permitted-roots-patch '(:keep)))
        (request (getf request :request))
        (changed 0))
    (flet ((apply-patch (patch current)
             (if (or (equal patch '(:keep)) (not patch) (null (second patch)))
                 current
                 (progn (incf changed) (second patch)))))
      (when (= changed 0)
        (return-from s7-roadmap-configure
          (values nil (%s7-roadmap-fail (s7-roadmap-id view) "all keep") nil)))
      (let* ((new-row-kind (apply-patch row-kind-patch (s7-roadmap-row-kind view)))
             (new-aggregation (apply-patch aggregation-patch (s7-roadmap-aggregation view)))
             (new-axes-spec (apply-patch axes-patch (s7-roadmap-axes view)))
             (new-roots (apply-patch roots-patch (s7-roadmap-permitted-roots view))))
        (when (and (not (equal (s7-roadmap-axes view) new-axes-spec))
                   (or (plusp (length (s7-roadmap-members view)))
                       (plusp (length (s7-roadmap-cells view)))))
          (return-from s7-roadmap-configure
            (values nil (%s7-roadmap-fail (s7-roadmap-id view) "layout populated") nil)))
        (let ((new-view (copy-s7-roadmap view)))
          (setf (s7-roadmap-row-kind new-view) new-row-kind
                (s7-roadmap-aggregation new-view) new-aggregation
                (s7-roadmap-permitted-roots new-view) new-roots
                (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
          (values t (%s7-roadmap-ok-line new-view :configure request changed) new-view))))))

(defun s7-roadmap-row-add (view request)
  "roadmap row --add. Axisless roadmaps only; appends at the bottom."
  (let ((member (%s7-str (getf request :member)))
        (request (getf request :request)))
    (cond
      ((plusp (length (s7-roadmap-axes view)))
       (values nil (%s7-roadmap-fail (s7-roadmap-id view) "has axes, naming axis") nil))
      ((not member) (values nil (%s7-roadmap-fail (s7-roadmap-id view) "unknown member") nil))
      (t
       (let ((new-view (copy-s7-roadmap view)))
         (setf (s7-roadmap-members new-view) (append (s7-roadmap-members new-view) (list member))
               (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view))
               (s7-roadmap-since-baseline new-view) (1+ (s7-roadmap-since-baseline view)))
         (values t (%s7-roadmap-ok-line new-view :row-add request 1) new-view))))))

(defun s7-roadmap-row-remove (view request)
  "roadmap row --remove. Retires the row from the view alone; the node stands."
  (let ((member (%s7-str (getf request :member)))
        (request (getf request :request)))
    (unless member
      (return-from s7-roadmap-row-remove (values nil (%s7-roadmap-fail (s7-roadmap-id view) "unknown member") nil)))
    (let ((new-view (copy-s7-roadmap view)))
      (setf (s7-roadmap-members new-view) (remove member (s7-roadmap-members new-view) :test #'string=)
            (s7-roadmap-retired new-view) (adjoin member (s7-roadmap-retired new-view) :test #'string=)
            (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
      (values t (%s7-roadmap-ok-line new-view :row-remove request 1) new-view))))

(defun s7-axis-add (view request)
  "axis --add. On the first axis a :discovery (moves the set); on another a column."
  (let ((axis-name (%s7-str (getf request :axis)))
        (member (%s7-str (getf request :add)))
        (request (getf request :request))
        (id (s7-roadmap-id view)))
    (cond
      ((not member) (values nil (%s7-axis-fail id "absent member") nil))
      (t
       (let* ((new-view (copy-s7-roadmap view))
              (target (or (find axis-name (s7-roadmap-axes new-view)
                                :key #'s7-axis-id :test #'string=)
                          (if axis-name
                              (progn (setf (s7-roadmap-axes new-view)
                                           (append (s7-roadmap-axes new-view)
                                                   (list (make-s7-axis :id axis-name :members '()))))
                                     (car (last (s7-roadmap-axes new-view))))
                              (car (s7-roadmap-axes new-view))))))
         (unless target
           (return-from s7-axis-add (values nil (%s7-axis-fail id "no axis named, and none declared") nil)))
         (setf (s7-axis-members target) (append (s7-axis-members target) (list member))
               (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
         (when (and (s7-roadmap-axes new-view)
                    (string= axis-name (s7-axis-id (first (s7-roadmap-axes new-view)))))
           (setf (s7-roadmap-members new-view) (append (s7-roadmap-members new-view) (list member))
                 (s7-roadmap-since-baseline new-view) (1+ (s7-roadmap-since-baseline new-view))))
         (values t (%s7-axis-ok-line new-view request :add member 0) new-view))))))

(defun s7-axis-remove (view request)
  "axis --remove. Retires the member from its axis and its cells; on the first
  axis retires the live row from the set."
  (let ((axis-name (%s7-str (getf request :axis)))
        (member (%s7-str (getf request :remove)))
        (request (getf request :request))
        (id (s7-roadmap-id view)))
    (unless member
      (return-from s7-axis-remove (values nil (%s7-axis-fail id "absent member") nil)))
    (let* ((new-view (copy-s7-roadmap view))
           (target (find axis-name (s7-roadmap-axes new-view) :key #'s7-axis-id :test #'string=))
           (first-axis-p (and target (equal target (first (s7-roadmap-axes new-view))))))
      (unless target
        (return-from s7-axis-remove (values nil (%s7-axis-fail id "no such axis") nil)))
      (setf (s7-axis-members target) (remove member (s7-axis-members target) :test #'string=))
      (when first-axis-p
        (setf (s7-roadmap-retired new-view) (adjoin member (s7-roadmap-retired new-view) :test #'string=)))
      (setf (s7-roadmap-cells new-view)
            (remove-if (lambda (c) (or (equal member (s7-cell-row c)) (equal member (s7-cell-column c))))
                       (s7-roadmap-cells new-view))
            (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
      (values t (%s7-axis-ok-line new-view request :remove member 0) new-view))))

(defun s7-roadmap-projection-add (view request)
  "roadmap projection --add. Stored target metadata; a duplicate id refuses."
  (let* ((pid (%s7-str (getf request :projection)))
         (root (%s7-str (getf request :root)))
         (repo (%s7-str (getf request :repo)))
         (path (%s7-str (getf request :path)))
         (start (%s7-str (getf request :start)))
         (end (%s7-str (getf request :end)))
         (row-axis (getf request :row-axis))
         (column-axis (getf request :column-axis))
         (fixed (getf request :fixed '()))
         (request (getf request :request))
         (id (s7-roadmap-id view)))
    (cond
      ((not (and pid root repo path start end))
       (values nil (%s7-roadmap-fail id "bad selection") nil))
      ((member pid (s7-roadmap-projections view) :key #'s7-proj-id :test #'string=)
       (values nil (%s7-roadmap-fail id "duplicate projection") nil))
      (t
       (let* ((proj (make-s7-projection :id pid :root root :repo repo :path path
                                     :start start :end end :policy :markdown-table
                                     :row-axis (or row-axis +absent+) :column-axis (or column-axis +absent+)
                                     :fixed fixed))
              (new-view (copy-s7-roadmap view)))
         (setf (s7-roadmap-projections new-view) (append (s7-roadmap-projections new-view) (list proj))
               (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
         (values t (%s7-roadmap-ok-line new-view :projection-add request 1) new-view))))))

(defun s7-roadmap-projection-remove (view request)
  "roadmap projection --remove."
  (let ((pid (%s7-str (getf request :projection-id)))
        (request (getf request :request))
        (id (s7-roadmap-id view)))
    (unless (member pid (s7-roadmap-projections view) :key #'s7-proj-id :test #'string=)
      (return-from s7-roadmap-projection-remove (values nil (%s7-roadmap-fail id "no such projection") nil)))
    (let ((new-view (copy-s7-roadmap view)))
      (setf (s7-roadmap-projections new-view)
            (remove-if (lambda (p) (string= pid (s7-proj-id p))) (s7-roadmap-projections new-view))
            (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
      (values t (%s7-roadmap-ok-line new-view :projection-remove request 1) new-view))))

(defun s7-cell-set (view request)
  "cell --ref|--out-of-scope|--in-scope. Re-points or re-scopes a coordinate;
  moves no required set."
  (let* ((coord (getf request :coord))
         (row (%s7-str (getf coord :row)))
         (column (%s7-str (getf coord :column)))
         (ref (getf request :ref))
         (out-of-scope (getf request :out-of-scope))
         (in-scope (getf request :in-scope))
         (request (getf request :request))
         (id (s7-roadmap-id view)))
    (unless (and row column)
      (return-from s7-cell-set (values nil (%s7-roadmap-fail id "bad selection") nil)))
    (let* ((new-view (copy-s7-roadmap view))
           (slot (or (%s7-cell-find new-view row column)
                     (progn (setf (s7-roadmap-cells new-view)
                                  (append (s7-roadmap-cells new-view)
                                          (list (make-s7-cell-slot :row row :column column
                                                                :ref +absent+ :out-of-scope nil :in-scope nil))))
                            (%s7-cell-find new-view row column)))))
      (when ref (setf (s7-cell-ref slot) ref))
      (when out-of-scope
        (setf (s7-cell-out-of-scope slot) t (s7-cell-in-scope slot) nil))
      (when in-scope
        (setf (s7-cell-in-scope slot) t (s7-cell-out-of-scope slot) nil))
      (setf (s7-roadmap-scope-revision new-view) (1+ (s7-roadmap-scope-revision view)))
      (values t (%s7-roadmap-ok-line new-view :configure request 1) new-view))))

;;; ------------------------------------------------------------------
;;; Counting. Green cells over applicable rows, baseline, since-baseline.
;;; ------------------------------------------------------------------

(defun %s7-row-kind-unit (view)
  (ecase (s7-roadmap-row-kind view)
    (:feature "features")
    (:epic "epics")
    (:work-set "work-sets")))

(defun %s7-row-closed-p (view row)
  (member row (s7-roadmap-closed view) :test #'string=))

(defun %s7-applicable-members (view &optional column)
  "Live rows less those with an out-of-scope cell for COLUMN. With no column the
  axisless rows (columns still apply where cells exist)."
  (let ((live (%s7-live-members view)))
    (if (null column)
        live
        (remove-if (lambda (r)
                     (let ((c (%s7-cell-find view r column)))
                       (and c (s7-cell-out-of-scope c))))
                   live))))

(defun %s7-green-members (view &optional column)
  "Rows whose cell for COLUMN (or the row itself, axisless) is verified done."
  (let ((applicable (%s7-applicable-members view column)))
    (remove-if-not (lambda (r)
                     (let ((c (and column (%s7-cell-find view r column))))
                       ;; An out-of-scope cell is not green; a cell with a ref
                       ;; to a closed row is green; axisless rows are green when
                       ;; their own feature work is closed.
                       (and (not (and c (s7-cell-out-of-scope c)))
                            (or (and c (not (absentp (s7-cell-ref c)))
                                     (%s7-row-closed-p view (s7-cell-ref c)))
                                (and (null column) (%s7-row-closed-p view r))))))
                   applicable)))

(defun s7-query-percent (view &key axis)
  "`percent --node R --axis <member>` (SPEC-WORK.md:2095). Returns (values OK-P LINE
  PERCENTAGE). --axis is required on a matrix and refused on a zero- or one-axis
  roadmap; over zero applicable rows it prints green=0 applicable=0 with no
  percentage and exits 0."
  (let* ((matrix-p (>= (length (s7-roadmap-axes view)) 2))
         (id (s7-roadmap-id view)))
    (cond
      ((and matrix-p (null axis))
       (values nil (format nil "PERCENT FAIL node=~A: --axis is required on a matrix" id) nil))
      ((and (not matrix-p) axis)
       (values nil (format nil "PERCENT FAIL node=~A: --axis is refused on a zero- or one-axis roadmap" id) nil))
      (t
       (let* ((green (length (%s7-green-members view axis)))
              (applicable (length (%s7-applicable-members view axis)))
              (rows (length (%s7-live-members view)))
              (baseline-rows (length (s7-roadmap-baseline-members view)))
              (since (s7-roadmap-since-baseline view))
              (pct (and (plusp applicable)
                        (format nil "pct=~,1F%%" (* 100 (/ green applicable))))))
         (values t
                 (format nil "QUERY OK ask=percent scope=~D unit=~A row-kind=~A green=~D applicable=~D rows=~D baseline-rows=~D since-baseline=~D~@[ ~A~]"
                         (s7-roadmap-scope-revision view) (%s7-row-kind-unit view)
                         (string-downcase (symbol-name (s7-roadmap-row-kind view)))
                         green applicable rows baseline-rows since pct)
                 (and (plusp applicable) (* 100 (/ green applicable)))))))))

;;; ------------------------------------------------------------------
;;; `roadmap --node R` (SPEC-WORK.md:2101) --- a named roadmap opened whole,
;;; from the retained view record, never a load of C and never narrowed by the
;;; default window.
;;; ------------------------------------------------------------------

(defun s7-query-roadmap (view &key node now archive-absent remaining)
  "Returns (values ROWS LINE). ROWS are the member ids shown, in order. A completed
  row outlives its work and stays in the listing; `remaining` is an explicit
  filter. A named open is never narrowed by `now`; an archived row whose retention
  archive is absent is reported as a coverage gap, never a silently shorter list."
  (declare (ignore node))
  (let* ((rows (if remaining
                   (remove-if (lambda (m)
                                (or (%s7-row-closed-p view m)
                                    (member m (s7-roadmap-retired view) :test #'string=)))
                              (s7-roadmap-members view))
                   (remove-if-not (lambda (m)
                                    (or (not archive-absent)
                                        (not (member m (s7-roadmap-archived view) :test #'string=))))
                                  (s7-roadmap-members view))))
         (gaps (if archive-absent (length (s7-roadmap-archived view)) 0))
         (line (with-output-to-string (s)
                 (format s "ROADMAP OK scope=~D rows=~D shown=~D gap=~D"
                         (s7-roadmap-scope-revision view) (length (%s7-live-members view))
                         (length rows) gaps)
                 (when remaining (format s " remaining only"))
                 (when (and archive-absent (plusp gaps))
                   (format s " note=coverage-gap")))))
    (values (mapcar (lambda (r) r) rows) line)))

;;; ------------------------------------------------------------------
;;; Settling a member (the roadmap outlives its work). Marks the row done while
;;; keeping it in `members`, exactly as a settle keeps the member in the set.
;;; ------------------------------------------------------------------

(defun s7-mark-member-closed (view member &key archived)
  "Record that a row's feature work settled done. The row stays on the axis and in
  the set; ARCHIVED marks it clipped past retention."
  (let ((new-view (copy-s7-roadmap view)))
    (setf (s7-roadmap-closed new-view) (adjoin member (s7-roadmap-closed new-view) :test #'string=))
    (when archived
      (setf (s7-roadmap-archived new-view) (adjoin member (s7-roadmap-archived new-view) :test #'string=)))
    new-view))
