;;;; s6-snapshot.lisp --- bounded paged indexes, the retention overlay, the
;;;; publication, and the retention boundary.
;;;;
;;;; docs/SPEC-WORK.md:509-535 — the two indexes that may not forget (the dedup
;;;; index and the closed index) are published beside the snapshot in bounded
;;;; pages and never loaded whole. Load cost is bounded by the retention window
;;;; and by `--index-cache`, never by the set's age.
;;;;
;;;; docs/SPEC-WORK.md:604-623 — a lookup is bounded indexed access; a page that
;;;; would pass its bound is split, a dedup page the predicate needs and cannot
;;;; read is a refusal and never evidence that a request is new; one revision
;;;; names the snapshot, the segments, the index roots and the manifests
;;;; together; and the replay of a journal's `:settle`/`:revive` builds a bounded
;;;; overlay over the published roots.

(in-package #:nova-work)

(defvar *pages-read* 0
  "Index pages a caller has opened. A lookup reads only its routed path, so this
bounds what *history-grows-startup-does-not* and *closed-paged-without-full-load*
observe.")

;;; ------------------------------------------------------------------
;;; Pages and the two indexes.
;;; ------------------------------------------------------------------

(defstruct (index-page (:conc-name page-))
  name kind records bytes entries available)

(defun make-page (&key name kind records bytes entries (available t))
  (make-index-page :name name :kind kind :records records :bytes bytes
                   :entries entries :available available))

(defun open-page (pages name)
  "Open NAME from PAGES. Answer (values page nil), or (values nil state) with
state :missing or :unavailable. This is the one place a page read is counted."
  (let ((p (find name pages :key #'page-name :test #'string=)))
    (cond ((null p) (values nil :missing))
          ((not (page-available p)) (values nil :unavailable))
          (t (incf *pages-read*) (values p nil)))))

(defstruct (closed-index (:conc-name ci-))
  pages)

(defun closed-row-lookup (index id)
  "A bounded lookup: read the root page, then exactly the one leaf page the key
routes to. The number of pages read is the depth of the routed path, never the
number of rows the team has finished."
  (let ((root-name "closed-root"))
    (multiple-value-bind (root err) (open-page (ci-pages index) root-name)
      (when err (return-from closed-row-lookup (values nil err)))
      (let ((leaf-name (cdr (assoc id (page-entries root) :test #'string=))))
        (unless leaf-name (return-from closed-row-lookup (values nil :missing)))
        (open-page (ci-pages index) leaf-name)))))

(defstruct (dedup-index (:conc-name di-))
  pages)

(defun dedup-lookup (index request digest)
  "The two-part test over a paged dedup index. Answer (values verdict nil page),
or (values nil status page). Verdict is :applied, :different-digest or :new;
status is :unavailable when the page the predicate needs cannot be read."
  (let ((name (format nil "dedup/pages/~A.sexp" request)))
    (multiple-value-bind (page err) (open-page (di-pages index) name)
      (cond
        ((eq err :unavailable) (values nil :unavailable name))
        ((or (eq err :missing) (null page)) (values :new nil name))
        (t (let ((rec (assoc request (page-entries page) :test #'string=)))
             (cond ((null rec) (values :new nil name))
                   ((string= digest (second rec)) (values :applied nil name))
                   (t (values :different-digest nil name)))))))))

(defun dedup-unavailable-line (word request page)
  "`<MUTATION> FAIL request=<id> page=<name>: dedup unavailable` — a retry the
predicate cannot place is a refusal, never evidence that the request is new."
  (format nil "~A FAIL request=~A page=~A: dedup unavailable" word request page))

;;; ------------------------------------------------------------------
;;; The page budget, --max and the continuation cursor.
;;; ------------------------------------------------------------------

(defun budget-limited-query (pages filter budget)
  "Read PAGES until BUDGET pages have been read; FILTER returns T to emit an
entry. Answer (values kind pages-read cursor) where kind is :rows (and the first
value is the emitted rows) or :more when the budget was met with no row able to
be emitted. `--max` caps the rows printed and bounds nothing else; the budget
caps the pages read whatever the filter rejects."
  (let ((emitted '()) (read 0) (cursor "-"))
    (dolist (p pages)
      (when (>= read budget) (return))
      (incf read)
      (dolist (e (page-entries p))
        (when (funcall filter e) (push e emitted)))
      (setf cursor (page-name p)))
    (if (and (null emitted) (>= read budget))
        (values :more read cursor)
        (values (nreverse emitted) read cursor))))

(defun query-more-line (rows shown pages after)
  "`QUERY MORE rows=<n> shown=<n> pages=<n> after=<cursor>` — the one cursor rule."
  (format nil "QUERY MORE rows=~D shown=~D pages=~D after=~A" rows shown pages after))

;;; ------------------------------------------------------------------
;;; The overlay: bounded, rebuilt from the journal, never per query.
;;; ------------------------------------------------------------------

(defstruct (overlay (:conc-name ov-))
  pages entries)

(defun replay-overlay (events index-cache page-records)
  "Rebuild the overlay from the durable journal's `:settle`/`:revive` events.
The overlay is what the next clip writes into the pages; until it does, a query
reads the pages and the overlay as one. Its page count is `ceil(entries / page-records)`,
held in bounded paged scratch under `--index-cache`; evicting an overlay page
erases nothing, because the journal is the truth."
  (let ((entries (loop for e in events
                       when (member (getf e :kind) '(:settle :revive))
                       collect e)))
    (make-overlay :pages (min (ceiling (length entries) (max 1 page-records)) index-cache)
                  :entries entries)))

;;; ------------------------------------------------------------------
;;; One revision publishes together.
;;; ------------------------------------------------------------------

(defstruct (publication (:conc-name pub-))
  revision snapshot-sha closed-sha dedup-sha manifests files)

(defun verify-publication (pub file-table)
  "Confirm every file the publication names is present with the hash its
manifest recorded, so a reader never sees a root pointing at a file that is not
there. FILE-TABLE is an alist path -> sha. Answer (values ok missing)."
  (let ((missing '()))
    (dolist (f (pub-files pub))
      (destructuring-bind (path . sha) f
        (unless (string= sha (cdr (assoc path file-table :test #'string=)))
          (push path missing))))
    (values (null missing) (nreverse missing))))
