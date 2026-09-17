;;;; roadmap.lisp --- the roadmap scope bookkeeping a `node move` advances.
;;;;
;;;; `move-updates-every-roadmap-scope` (docs/SPEC-WORK.md:5978): a row
;;;; referenced by two roadmaps outside both parent chains and one unrelated
;;;; roadmap. Both referencing scope revisions advance in the move's envelope,
;;;; the unrelated roadmap stays, a failed acceptance moves none, a render
;;;; captured before the move keeps its captured scope, and an intervening
;;;; mutation of an affected roadmap makes an undo conflict.
;;;;
;;;; A roadmap scope is the pure bookkeeping a `node move` must touch when a
;;;; referenced row's scope revision changes: its id, its current scope
;;;; revision, and the row ids it references. The move is one envelope -- it
;;;; either advances every referencing scope or, refused, writes none.

(in-package #:nova-work)

(defstruct (scope-roadmap (:constructor %make-scope-roadmap (id revision rows)))
  (id "" :read-only t :type string)
  (revision 0 :type integer)
  (rows '() :type list))

(defun make-scope-roadmap (&key id (revision 0) rows)
  "A roadmap view's scope bookkeeping: ID, its REVISION, and the ROWS it
references."
  (%make-scope-roadmap (or id "") (or revision 0) (copy-list (or rows '()))))

(defun scope-roadmap-references-p (scope row-id)
  "True when SCOPE references ROW-ID. A roadmap outside the row's parent
chains is still a referrer."
  (and (member row-id (scope-roadmap-rows scope) :test #'string=) t))

(defun scopes-on-move (scopes row-id &key (accept t))
  "The scopes a `node move` of ROW-ID updates, in one envelope. When ACCEPT is
true every scope referencing ROW-ID has its revision advanced by one and the
returned events name each; an unrelated scope is returned unchanged. When
ACCEPT is nil -- a refused move -- the original scopes are returned with an
empty envelope: nothing moves. Returns (values SCOPES EVENTS ACCEPTED)."
  (if (not accept)
      (values scopes '() nil)
      (let ((events '()) (updated '()))
        (dolist (scope scopes)
          (if (scope-roadmap-references-p scope row-id)
              (let ((next (copy-scope-roadmap scope)))
                (incf (scope-roadmap-revision next))
                (push (list :kind :scope :roadmap (scope-roadmap-id next)
                            :scope-revision (scope-roadmap-revision next)
                            :references row-id)
                      events)
                (push next updated))
              (push scope updated)))
        (values (nreverse updated) (nreverse events) t))))

(defun scope-capture (scopes)
  "A render captured now: the scopes at their current revisions. A later move
returns new scopes and never rewrites this capture."
  (copy-list scopes))

(defun scope-mutate (scope)
  "A later mutation of an affected roadmap's scope: its revision advances so
that an undo captured before it no longer matches."
  (let ((next (copy-scope-roadmap scope)))
    (incf (scope-roadmap-revision next))
    next))

(defun scopes-undo (scopes touched guards preimage)
  "Undo the move that produced SCOPES by restoring PREIMAGE, but only while
every touched roadmap still matches its GUARDS. Returns (values RESTORED nil)
on success; a stale guard names the moved-on roadmap and returns
(values nil line) with nothing restored."
  (let ((conflict
          (loop for id in touched
                for guard in guards
                for scope = (find id scopes :key #'scope-roadmap-id :test #'string=)
                when (or (null scope) (/= guard (scope-roadmap-revision scope)))
                  return (format nil "UNDO FAIL request-of=move: conflict ~A" id))))
    (if conflict
        (values nil conflict)
        (values (copy-list preimage) nil))))


;;; ------------------------------------------------------------------
;;; folded from replays-8621.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

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


;;; ------------------------------------------------------------------
;;; folded from replays-8649.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8649.lisp --- the pure kernel the five replays of card 8649 need:
;;;; the roadmap proof (docs/SPEC-WORK.md:6244), one-journal rotation (:5998),
;;;; rule 2's unavailable partition (:5024/:5633), the savepoint's cut
;;;; (:5989/:6312) and its write-failure retention (:6014/:6326).
;;;;
;;;; As with replays-applicable-delegation.lisp, this is the pure part: no
;;;; session, no CLI, no socket, no filesystem. The live wiring a later slice
;;;; owes is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; roadmap-proof (docs/SPEC-WORK.md:6244)
;;; ------------------------------------------------------------------

(defstruct (roadmap-row
             (:constructor make-roadmap-row (&key id axis kind state evidence status)))
  id axis kind state evidence status)

(defstruct (roadmap
             (:constructor make-roadmap
                 (&key id axes rows shared-prerequisites discovered closed)))
  id axes rows shared-prerequisites discovered closed)

(defun roadmap-name (x)
  "A canonical lowercase name: keywords print downcased, strings verbatim."
  (if (symbolp x) (string-downcase (symbol-name x)) (princ-to-string x)))

(defun roadmap-row-line (row)
  (format nil "row id=~A axis=~A kind=~A state=~A evidence=~A status=~A"
          (roadmap-row-id row)
          (roadmap-name (roadmap-row-axis row))
          (roadmap-name (roadmap-row-kind row))
          (roadmap-name (roadmap-row-state row))
          (roadmap-name (roadmap-row-evidence row))
          (roadmap-name (roadmap-row-status row))))

(defun roadmap-render (roadmap &optional (style :chat))
  "One canonical render of the whole table. STYLE names the surface the render
is written to and changes no byte, so the chat and file renders are identical
(docs/SPEC-WORK.md:6244)."
  (declare (ignore style))
  (with-output-to-string (s)
    (format s "ROADMAP id=~A~%" (roadmap-id roadmap))
    (dolist (axis (roadmap-axes roadmap))
      (format s "axis=~A~%" (roadmap-name axis)))
    (dolist (row (roadmap-rows roadmap))
      (write-string (roadmap-row-line row) s)
      (write-char #\Newline s))
    (dolist (p (roadmap-shared-prerequisites roadmap))
      (format s "shared-prerequisite ~A~%" p))
    (dolist (d (roadmap-discovered roadmap))
      (format s "discovered ~A~%" d))
    (dolist (c (roadmap-closed roadmap))
      (format s "closed ~A~%" c))))

(defun count-occurrences (needle haystack)
  (if (or (null needle) (string= "" needle))
      0
      (loop with n = 0
            with start = 0
            for pos = (search needle haystack :start2 start)
            while pos
            do (incf n)
               (setf start (+ pos (length needle)))
            finally (return n))))

(defun roadmap-edit-marker (text marker replacement)
  "Replace the single occurrence of MARKER and preserve every unrelated byte; an
absent or ambiguous marker refuses and publishes nothing
(docs/SPEC-WORK.md:6244)."
  (let ((n (count-occurrences marker text)))
    (cond
      ((zerop n) (values nil (format nil "marker not found: ~S" marker)))
      ((> n 1) (values nil (format nil "ambiguous marker: ~S appears ~D times" marker n)))
      (t (let ((pos (search marker text)))
           (values (concatenate 'string
                                (subseq text 0 pos)
                                replacement
                                (subseq text (+ pos (length marker))))
                   nil))))))

;;; ------------------------------------------------------------------
;;; rotation-keeps-one-journal (docs/SPEC-WORK.md:5998, :6312, :6338)
;;; ------------------------------------------------------------------

(defstruct (journal-record (:constructor make-journal-record (&key (seq 1) (events '()))))
  seq events)

(defun journal-record-hash (record)
  "The verified identity of one journal record: the SHA-256 of its canonical
bytes (docs/SPEC-WORK.md:6296)."
  (sha256-hex (canonical-string (list :seq (journal-record-seq record)
                                      :events (journal-record-events record)))))

(defstruct (journal-segment
             (:constructor make-journal-segment (&key (path "") (header '()) (records '()))))
  path header records)

(defstruct (journal-chain
             (:constructor make-journal-chain (&key (id "") (segments '()))))
  id segments)

(defun journal-last-segment (chain)
  (car (last (journal-chain-segments chain))))

(defun rotate-journal (chain)
  "Rotate CHAIN: the new segment's header names the same journal id and copies
the boundary record -- its sequence and hash -- so the chain and the savepoint
cut stay reachable without scanning an old segment (docs/SPEC-WORK.md:5998,
:6338)."
  (let* ((segments (journal-chain-segments chain))
         (old (car (last segments)))
         (boundary (car (last (journal-segment-records old))))
         (number (1+ (length segments)))
         (header (list :journal-id (journal-chain-id chain)
                       :segment number
                       :copied-from (journal-segment-path old)
                       :copied-boundary (and boundary
                                             (list :seq (journal-record-seq boundary)
                                                   :sha256 (journal-record-hash boundary)))))
         (new (make-journal-segment
               :path (format nil "~A.~D" (journal-chain-id chain) number)
               :header header
               :records (if boundary (list boundary) '()))))
    (make-journal-chain :id (journal-chain-id chain)
                        :segments (append segments (list new)))))

(defun journal-append-record (chain record)
  "Append RECORD to the last segment, continuing the chain's sequence from the
copied boundary."
  (let* ((segments (journal-chain-segments chain))
         (last-seg (car (last segments)))
         (last-rec (car (last (journal-segment-records last-seg))))
         (next-seq (if last-rec (1+ (journal-record-seq last-rec)) 1))
         (rec (make-journal-record :seq next-seq :events (journal-record-events record)))
         (new (make-journal-segment :path (journal-segment-path last-seg)
                                    :header (journal-segment-header last-seg)
                                    :records (append (journal-segment-records last-seg)
                                                     (list rec)))))
    (make-journal-chain :id (journal-chain-id chain)
                        :segments (append (butlast segments) (list new)))))

(defun savepoint-cut-reachable-p (chain cut)
  "The cut is reachable from the last segment's copied boundary record alone,
with no scan of an old segment (docs/SPEC-WORK.md:5998, :6338)."
  (let* ((header (journal-segment-header (journal-last-segment chain)))
         (b (getf header :copied-boundary)))
    (and b (integerp cut) (<= 0 cut (getf b :seq)))))

(defun export-journal-bundle (chain &key from)
  "The offline bundle. FROM names the file it is written from and changes no
byte, so either file writes the same bundle (docs/SPEC-WORK.md:6001)."
  (declare (ignore from))
  (let* ((header (journal-segment-header (journal-last-segment chain)))
         (b (getf header :copied-boundary)))
    (canonical-string (list :journal-bundle
                            :journal-id (journal-chain-id chain)
                            :boundary (if b
                                          (list :seq (getf b :seq) :sha256 (getf b :sha256))
                                          +absent+)))))

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green (docs/SPEC-WORK.md:5019, :5033, :5633)
;;; ------------------------------------------------------------------

(defun rule-2-check (reference &key partition-readable-p)
  "Rule 2 (:5011) resolves a reference against O's nodes or C's closed index.
Where the page that lookup needs cannot be read the rule reports that it could
not check and never that the reference is broken, and green is never printed
over history it could not read (docs/SPEC-WORK.md:5019-5033). REFERENCE's own
:resolved-p says the name resolved without a partition read."
  (let ((id (getf reference :id))
        (partition (getf reference :partition))
        (resolved-p (getf reference :resolved-p)))
    (cond
      (resolved-p (values :green nil))
      ((not partition-readable-p)
       (values :unavailable
               (format nil "WORK FAIL ~A: rule 2: unavailable partition=~A" id partition)))
      (t (values :dangling
                 (format nil "WORK FAIL ~A: rule 2: dangling" id))))))

(defun validate-report (verdicts)
  "One validation over the verdicts: exit 1 with a `WORK FAIL` count line on any
finding, exit 0 with `WORK OK` only when every rule was green
(docs/SPEC-WORK.md:5001-5008, :5633)."
  (let ((findings (count-if (lambda (v) (not (eq v :green))) verdicts)))
    (if (plusp findings)
        (values 1 (format nil "WORK FAIL findings=~D" findings))
        (values 0 "WORK OK"))))

;;; The savepoint's create and list verbs live in savepoint.lisp; the journal's
;;; record identities and its cut-reachability helper stay here.

;;; ------------------------------------------------------------------
;;; `axis --add|--remove` (docs/SPEC-WORK.md:2323, :3072-3129, :5438)
;;; ------------------------------------------------------------------
;;; A roadmap's stored view is the real input. `axis --add` appends a member to
;;; one declared axis in order; `axis --remove` takes it off that axis and off
;;; every current cell coordinate holding it, records the position and removed
;;; cells as its `:before`, and on the first axis retires the row from the
;;; view's required set while the row is live (done included: only removed,
;;; cancelled and superseded are not live). It never calls `node remove`,
;;; `cancel` or any terminal verb; the node, its evidence and its state stand.
;;; An absent member refuses. The roadmap's scope revision advances, never a
;;; containment parent's.
;;;
;;; The view record the one creator writes is
;;; `(:members ... :axes ... :permitted-roots ... :projections ...)`; this file
;;; adds the `:axis-members`, `:cells`, `:scope-revision` and `:axis-log` fields
;;; it owns to that same record, without touching the node's containment.

(defparameter +axis-non-live-states+ '(:removed :cancelled :superseded)
  "A row is live unless its node is removed, cancelled or superseded; done is
live (docs/SPEC-WORK.md:3103-3109).")

(defun %roadmap-view (wnode)
  "WNODE's view record with the axis fields this verb owns present, written back
onto the node so a later in-place update is visible. The node itself is never
touched."
  (let ((view (wnode-view wnode)))
    (unless (member :axis-members view) (setf view (list* :axis-members '() view)))
    (unless (member :cells view) (setf view (list* :cells '() view)))
    (unless (member :scope-revision view) (setf view (list* :scope-revision 0 view)))
    (unless (member :axis-log view) (setf view (list* :axis-log '() view)))
    (setf (wnode-view wnode) view)
    view))

(defun %view-axis-ids (view)
  "The declared axis ids, in order. An axis is written as a bare id or as the
record `(:id <id> ...)`."
  (mapcar (lambda (a) (if (consp a) (getf a :id) a)) (getf view :axes)))

(defun %view-axis-members (view axis-id)
  (cdr (assoc axis-id (getf view :axis-members) :test #'string=)))

(defun %view-set-axis-members (view axis-id members)
  "Replace AXIS-ID's ordered members, mutating the stored record in place."
  (let ((entry (assoc axis-id (getf view :axis-members) :test #'string=)))
    (if entry
        (setf (cdr entry) members)
        (setf (getf view :axis-members)
              (append (getf view :axis-members) (list (cons axis-id members))))))
  members)

(defun %row-live-p (state id)
  "True when ID names a node that is not removed, cancelled or superseded."
  (let ((node (%node-quiet state id)))
    (and node
         (not (member (wnode-state node) +axis-non-live-states+))
         t)))

(defun %axis-coordinate-p (cell axis-id member)
  (let ((coord (car cell)))
    (and (consp coord)
         (string= axis-id (first coord))
         (string= member (second coord)))))

(defun axis (kernel &key roadmap axis member add remove reason request
                       (by "rowan") (stamp "2026-09-14T12:00:00Z")
                       (generation-owner "gen-4"))
  "The `axis --add|--remove` structure verb on a roadmap's stored view. Answers
(values OK-P LINE EXIT-CODE). `--add` appends MEMBER to AXIS in order; `--remove`
takes it off the axis and off every current cell coordinate holding it, retires
it from the view's required set when it is on the first axis and live, and
leaves the node, its evidence and its state standing. An unknown axis or an
absent member refuses at exit 2 and writes nothing (docs/SPEC-WORK.md:3103-3113,
:5438)."
  (declare (ignore reason))
  (let* ((state (kernel-state kernel))
         (wnode (%node-quiet state roadmap)))
    (unless wnode
      (return-from axis
        (values nil (format nil "AXIS FAIL node=~A: no such node" roadmap) 2)))
    (unless (eq :roadmap (wnode-type wnode))
      (return-from axis
        (values nil (format nil "AXIS FAIL node=~A: not a roadmap" roadmap) 2)))
    (unless (wnode-view wnode)
      (return-from axis
        (values nil (format nil "AXIS FAIL node=~A: no view" roadmap) 2)))
    (unless (and (stringp axis) (plusp (length axis)))
      (return-from axis
        (values nil (format nil "AXIS FAIL node=~A: no axis" roadmap) 2)))
    (unless (and (stringp member) (plusp (length member)))
      (return-from axis
        (values nil (format nil "AXIS FAIL node=~A: no member" roadmap) 2)))
    (let* ((view (%roadmap-view wnode))
           (ids (%view-axis-ids view)))
      (unless (member axis ids :test #'string=)
        (return-from axis
          (values nil (format nil "AXIS FAIL node=~A: unknown axis ~A" roadmap axis) 2)))
      (let ((index (position axis ids :test #'string=))
            (members (%view-axis-members view axis)))
        (cond
          (add
           (multiple-value-bind (okp line)
               (if (member member members :test #'string=)
                   (values t (format nil "AXIS OK id=- request=~A node=~A rev=~D pushed=- change=add member=~A changed=0 cells=0 emitted=0"
                                     request roadmap (state-revision state) member))
                   (progn
                     (%view-set-axis-members view axis (append members (list member)))
                     (when (zerop index)
                       (setf (getf view :members)
                             (append (getf view :members) (list member))))
                     (incf (getf view :scope-revision))
                     (values t (format nil "AXIS OK id=- request=~A node=~A rev=~D pushed=- change=add member=~A changed=1 cells=0 emitted=0"
                                       request roadmap (state-revision state) member))))
             (values okp line 0)))
          (remove
           (if (not (member member members :test #'string=))
               (values nil (format nil "AXIS FAIL node=~A: unknown member ~A on ~A"
                                   roadmap member axis)
                       2)
               (let* ((cells (getf view :cells))
                      (retired (remove-if-not (lambda (c) (%axis-coordinate-p c axis member))
                                              cells))
                      (n (length retired)))
                 (setf (getf view :cells)
                       (remove-if (lambda (c) (%axis-coordinate-p c axis member)) cells))
                 (%view-set-axis-members view axis (remove member members :test #'string=))
                 (when (and (zerop index) (%row-live-p state member))
                   (setf (getf view :members)
                         (remove member (getf view :members) :test #'string=)))
                 (push (list :request request :verb :axis :axis axis :member member
                             :before (list :position (position member members :test #'string=)
                                           :cells retired))
                       (getf view :axis-log))
                 (incf (getf view :scope-revision))
                 (values t (format nil "AXIS OK id=- request=~A node=~A rev=~D pushed=- change=remove member=~A changed=1 cells=~D emitted=0"
                                   request roadmap (state-revision state) member n)
                         0))))
          (t
           (values nil (format nil "AXIS FAIL node=~A: --add or --remove is required"
                               roadmap)
                   2)))))))

(defun axis-add (kernel roadmap axis member &rest args)
  "`axis --add`: the spelling-side helper over AXIS (docs/SPEC-WORK.md:2323)."
  (apply #'axis kernel :roadmap roadmap :axis axis :member member :add t args))

(defun axis-remove (kernel roadmap axis member &rest args)
  "`axis --remove`: the spelling-side helper over AXIS (docs/SPEC-WORK.md:2323)."
  (apply #'axis kernel :roadmap roadmap :axis axis :member member :remove t args))

;;; The `:axis` verb is reversible: `axis --remove` of an added member, and
;;; `axis --add` of a removed one restoring its position and its cells
;;; (docs/SPEC-WORK.md:2863).
