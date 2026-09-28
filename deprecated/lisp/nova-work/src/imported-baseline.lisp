;;;; imported-baseline.lisp --- the pinned imported starting point of a roadmap.
;;;;
;;;; nova-work criterion E07-F04-01, "Preserve source revision, audit IDs,
;;;; reports and aliases" (Fixed Tables imported baseline inventory).
;;;;
;;;; docs/SPEC-WORK.md:7785-7786: pin the baseline membership, source revision
;;;; and completion unit before starting the stream; retain the original
;;;; baseline. docs/SPEC-WORK.md:7826-7827: original audit IDs, historical
;;;; reports and superseding aliases remain retrievable; they are provenance,
;;;; not current completion assertions.
;;;;
;;;; An imported baseline is a pure record. Its revision, completion unit and
;;;; membership are read-only copies taken at import; each row keeps its
;;;; original audit ID, an append-only list of historical reports and every
;;;; alias it has been known by. A rename (supersede) moves the row to its new
;;;; id and keeps the old id as an alias; nothing is ever dropped. The only
;;;; accounting it feeds is the pinned denominator: a historical report never
;;;; counts as completed work.

(in-package #:nova-work)

(define-condition baseline-import-refused (nova-work-error)
  ((what :initarg :what :reader baseline-import-refused-what))
  (:report (lambda (c s)
             (format s "IMPORT FAIL: ~A" (baseline-import-refused-what c)))))

(defstruct (baseline-row (:constructor %make-baseline-row (id audit-id reports aliases)))
  ;; the row's current id; a supersede moves it and keeps the old one as an alias
  id
  ;; the original audit ID, never rewritten
  (audit-id nil :read-only t)
  ;; historical reports, oldest first, append-only
  reports
  ;; every id and alias this row has been known by, oldest first
  aliases)

(defstruct (imported-baseline
            (:constructor %make-imported-baseline
                (source-revision completion-unit members)))
  (source-revision nil :read-only t)
  (completion-unit nil :read-only t)
  ;; the original baseline membership, in import order, never rewritten
  (members '() :read-only t)
  ;; current id, alias or audit ID -> baseline-row
  (index (make-hash-table :test #'equal))
  ;; the supersede events, oldest first
  (supersessions '()))

(defun %baseline-refuse (fmt &rest args)
  (error 'baseline-import-refused :what (apply #'format nil fmt args)))

(defun %baseline-index (baseline key row)
  (let ((held (gethash key (imported-baseline-index baseline))))
    (when (and held (not (eq held row)))
      (%baseline-refuse "~S names two rows" key))
    (setf (gethash key (imported-baseline-index baseline)) row)))

(defun import-baseline (&key source-revision completion-unit rows)
  "Pin an imported baseline. SOURCE-REVISION and COMPLETION-UNIT are required;
ROWS is a list of plists (:id :audit-id :reports :aliases). Every value is
copied, so a later change to the caller's input changes nothing retained. A
duplicate id, alias or audit ID refuses the whole import."
  (unless (and (stringp source-revision) (plusp (length source-revision)))
    (%baseline-refuse "no pinned source revision"))
  (unless completion-unit
    (%baseline-refuse "no pinned completion unit"))
  (let* ((members (mapcar (lambda (r) (copy-seq (getf r :id))) rows))
         (b (%make-imported-baseline (copy-seq source-revision) completion-unit
                                     members)))
    (dolist (r rows b)
      (let* ((id (getf r :id))
             (audit (getf r :audit-id))
             (row (%make-baseline-row (copy-seq id)
                                      (and audit (copy-seq audit))
                                      (copy-tree (getf r :reports))
                                      (mapcar #'copy-seq (getf r :aliases)))))
        (unless (and (stringp id) (plusp (length id)))
          (%baseline-refuse "a row without an id"))
        (%baseline-index b id row)
        (when audit (%baseline-index b audit row))
        (dolist (alias (baseline-row-aliases row))
          (%baseline-index b alias row))))))

(defun %baseline-row (baseline key)
  (gethash key (imported-baseline-index baseline)))

(defun baseline-provenance (baseline key)
  "The provenance of the row KEY names (its current id, any alias or its
original audit ID), or nil when KEY names no row. The plist is provenance, not
a current completion assertion: :assertion is always :provenance."
  (let ((row (%baseline-row baseline key)))
    (when row
      (list :id (baseline-row-id row)
            :audit-id (baseline-row-audit-id row)
            :reports (copy-tree (baseline-row-reports row))
            :aliases (copy-list (baseline-row-aliases row))
            :source-revision (imported-baseline-source-revision baseline)
            :assertion :provenance))))

(defun baseline-supersede (baseline old new &key by reason)
  "Rename the row OLD names to NEW. OLD stays retrievable as an alias of NEW;
the audit ID, reports and original membership are untouched. BY and REASON are
recorded with the supersession."
  (let ((row (%baseline-row baseline old)))
    (unless row (%baseline-refuse "~S names no row" old))
    (let ((was (baseline-row-id row)))
      (%baseline-index baseline new row)
      (unless (member was (baseline-row-aliases row) :test #'equal)
        (setf (baseline-row-aliases row)
              (append (baseline-row-aliases row) (list was))))
      (setf (baseline-row-id row) new)
      (setf (imported-baseline-supersessions baseline)
            (append (imported-baseline-supersessions baseline)
                    (list (list :from was :to new :by by :reason reason))))
      baseline)))

(defun baseline-add-report (baseline key report)
  "Append REPORT to the historical reports of the row KEY names. Earlier
reports are never replaced."
  (let ((row (%baseline-row baseline key)))
    (unless row (%baseline-refuse "~S names no row" key))
    (setf (baseline-row-reports row)
          (append (baseline-row-reports row) (list (copy-tree report))))
    baseline))

(defun baseline-accounting (baseline)
  "An inventory accounting whose baseline is the pinned membership. No
historical report is carried in as completed work."
  (make-inventory-accounting :baseline (copy-list (imported-baseline-members baseline))))
