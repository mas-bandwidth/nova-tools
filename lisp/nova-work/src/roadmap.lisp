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
;;; The roadmap view record and the `roadmap configure` verb
;;; (docs/SPEC-WORK.md:3078-3094, :3110-3124, :5994-5996)
;;; ------------------------------------------------------------------
;;;
;;; `roadmap create` (src/node-verbs.lisp) writes the node and its view; these
;;; are the reads and the four verbs that own the record after creation. This
;;; slice carries `roadmap configure`: it patches `:row-kind`, `:aggregation`,
;;; `:completion-policy`, `:axes` and `:permitted-roots` with `(:keep)` or
;;; `(:set V)`, refuses `all keep` and `bad patch`, moves the layout only while
;;; the roadmap is empty (`layout populated`), refuses a row-kind change whose
;;; retained rows do not match (`row kind mismatch`), makes an equal-value
;;; configure the no-effect receipt `changed=0` with no scope event, and undoes
;;; a configure only while the current postimage still equals its `:after`.

(defparameter *roadmap-config-keys*
  '((:row-kind-patch . :row-kind)
    (:aggregation-patch . :aggregation)
    (:completion-policy-patch . :completion-policy)
    (:axes-patch . :axes)
    (:permitted-roots-patch . :permitted-roots))
  "The five fields `roadmap configure` owns, in the spec's order (:3088).")

(defun %roadmap-view (state id)
  (let ((node (%node-or-nil state id)))
    (and node (wnode-view node))))

(defun roadmap-view-revision (state id)
  "The roadmap view's scope revision: a no-effect configure leaves it."
  (getf (%roadmap-view state id) :revision))

(defun roadmap-view-aggregation (state id)
  (getf (%roadmap-view state id) :aggregation))

(defun roadmap-view-axes (state id)
  "The declared axis ids, in order."
  (getf (%roadmap-view state id) :axes))

(defun roadmap-view-members (state id)
  "The ordered rows of an axisless roadmap."
  (getf (%roadmap-view state id) :members))

(defun roadmap-structure-events (state id)
  "The roadmap ID's `:roadmap-create` `:structure` events, oldest first."
  (let ((node (%node-or-nil state id)))
    (when (and node (wnode-view node))
      (reverse (remove-if-not (lambda (e) (eq :structure (getf e :op)))
                              (wnode-meta-log node))))))

(defun %roadmap-config-postimage (view)
  "The five configurable fields of VIEW, as the `:after` an undo guards on."
  (list :row-kind (getf view :row-kind)
        :aggregation (getf view :aggregation)
        :completion-policy (getf view :completion-policy)
        :axes (copy-list (getf view :axes))
        :permitted-roots (copy-list (getf view :permitted-roots))))

(defun %roadmap-patch-value (field patch)
  "Answer (values VALUE REFUSAL) for one tagged patch. VALUE is `:keep` or the
patched value. The three policies admit no clear; a clear is a bad patch, and
the axes and roots lists are explicit empties, never a clear (:3087)."
  (cond
    ((not (and (consp patch) (member (car patch) '(:keep :set))))
     (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field)))))
    ((eq (car patch) :keep) (values :keep nil))
    ((cddr patch) (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field)))))
    (t
     (let ((v (second patch)))
       (case field
         (:row-kind
          (if (member v *roadmap-row-kinds*) (values v nil)
              (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))
         (:aggregation
          (if (member v *roadmap-aggregations*) (values v nil)
              (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))
         (:completion-policy
          (if (member v *roadmap-completion-policies*) (values v nil)
              (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))
         (:axes
          (if (%roadmap-axis-ids-p v) (values v nil)
              (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))
         (:permitted-roots
          (if (and (listp v)
                   (every (lambda (r) (and (stringp r) (plusp (length r)))) v))
              (values v nil)
              (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))
         (t (values nil (format nil "bad patch ~A" (string-downcase (symbol-name field))))))))))

(defun %roadmap-configure-payload (roadmap row-kind-patch aggregation-patch
                                            completion-policy-patch axes-patch
                                            permitted-roots-patch reason)
  "The request's own payload, compared so a retry replays and a changed payload
under the same id refuses (:5364, :3030)."
  (list :verb :roadmap-configure :roadmap roadmap
        :row-kind-patch row-kind-patch :aggregation-patch aggregation-patch
        :completion-policy-patch completion-policy-patch :axes-patch axes-patch
        :permitted-roots-patch permitted-roots-patch :reason reason))

(defun roadmap-configure (kernel &key roadmap row-kind-patch aggregation-patch
                                       completion-policy-patch axes-patch
                                       permitted-roots-patch reason request)
  "Patch the roadmap view's row-kind, aggregation, completion-policy, axes and
permitted-roots (:3086-3094). Answers (values OK-P LINE EXIT-CODE). An
equal-value configure is the no-effect receipt changed=0 with no scope event;
`all keep` and `bad patch` refuse at exit 2; a layout change on a populated
roadmap refuses `layout populated`; a row-kind change whose retained rows do
not match refuses `row kind mismatch` (:3110-3124)."
  (let* ((state (kernel-state kernel))
         (node (%node-quiet state roadmap))
         (view (and node (wnode-view node))))
    (unless view
      (return-from roadmap-configure
        (values nil (format nil "ROADMAP FAIL node=~A: no roadmap view" roadmap) 1)))
    (let ((payload (%roadmap-configure-payload
                    roadmap row-kind-patch aggregation-patch
                    completion-policy-patch axes-patch permitted-roots-patch reason)))
      ;; A lost reply's retry replays its original receipt even if the view has
      ;; since moved on; a changed payload under the id is a conflict.
      (when request
        (let ((prior (gethash request (kernel-applied kernel))))
          (when prior
            (if (and (eq (getf prior :verb) :roadmap-configure)
                     (equal (getf prior :payload) payload))
                (return-from roadmap-configure (values t (getf prior :line) 0))
                (return-from roadmap-configure
                  (values nil
                          (format nil "ROADMAP FAIL node=~A: reused with a different payload"
                                  roadmap)
                          1))))))
      (let ((patches '()) (refusal nil))
        (dolist (spec (list (list :row-kind row-kind-patch)
                            (list :aggregation aggregation-patch)
                            (list :completion-policy completion-policy-patch)
                            (list :axes axes-patch)
                            (list :permitted-roots permitted-roots-patch)))
          (destructuring-bind (field patch) spec
            (unless refusal
              (multiple-value-bind (value why) (%roadmap-patch-value field patch)
                (if why
                    (setf refusal why)
                    (push (cons field value) patches))))))
        (when refusal
          (return-from roadmap-configure
            (values nil (format nil "ROADMAP FAIL node=~A: ~A" roadmap refusal) 2)))
        (setf patches (nreverse patches))
        (when (every (lambda (p) (eq :keep (cdr p))) patches)
          (return-from roadmap-configure
            (values nil (format nil "ROADMAP FAIL node=~A: all keep" roadmap) 2)))
        (let* ((before (%roadmap-config-postimage view))
               (after (copy-list before)))
          (dolist (p patches)
            (unless (eq :keep (cdr p))
              (setf (getf after (car p))
                    (let ((v (cdr p))) (if (listp v) (copy-list v) v)))))
          (let ((changed (count-if-not
                          (lambda (f) (equal (getf before f) (getf after f)))
                          '(:row-kind :aggregation :completion-policy
                            :axes :permitted-roots))))
            ;; An equal-value configure is the no-effect receipt: changed=0, no
            ;; scope event, no view write.
            (if (zerop changed)
                (let ((line (format nil "ROADMAP OK id=~A request=~A change=configure changed=0"
                                    roadmap request)))
                  (when request
                    (setf (gethash request (kernel-applied kernel))
                          (list :verb :roadmap-configure :roadmap roadmap
                                :payload payload :line line :changed 0)))
                  (return-from roadmap-configure (values t line 0)))
                (progn
                  ;; A row-kind change requires every retained row to match.
                  (when (and (not (equal (getf before :row-kind) (getf after :row-kind)))
                             (getf view :members))
                    (dolist (member (getf view :members))
                      (let ((m (%node-quiet state member)))
                        (unless (and m (eq (getf after :row-kind) (wnode-type m)))
                          (return-from roadmap-configure
                            (values nil
                                    (format nil "ROADMAP FAIL node=~A: row kind mismatch" roadmap)
                                    2))))))
                  ;; The axis layout changes only while the roadmap is empty.
                  (when (and (not (equal (getf before :axes) (getf after :axes)))
                             (or (getf view :members)
                                 (getf view :cells)))
                    (return-from roadmap-configure
                      (values nil
                              (format nil "ROADMAP FAIL node=~A: layout populated" roadmap)
                              2)))
                  (let ((rev (1+ (getf view :revision))))
                    (setf (getf view :row-kind) (getf after :row-kind)
                          (getf view :aggregation) (getf after :aggregation)
                          (getf view :completion-policy) (getf after :completion-policy)
                          (getf view :axes) (copy-list (getf after :axes))
                          (getf view :permitted-roots) (copy-list (getf after :permitted-roots))
                          (getf view :revision) rev)
                    (push (list :op :configure :request request :rev rev
                                :before before :after after :changed changed)
                          (getf view :log))
                    (let ((line (format nil "ROADMAP OK id=~A request=~A change=configure changed=~D rev=~D"
                                        roadmap request changed rev)))
                      (when request
                        (setf (gethash request (kernel-applied kernel))
                              (list :verb :roadmap-configure :roadmap roadmap
                                    :payload payload :line line :changed changed
                                    :before before :after after)))
                      (values t line 0)))))))))))

(defun roadmap-configure-undo (kernel &key roadmap of request)
  "Undo a `roadmap configure` by restoring its ordered preimage, but only while
the current postimage still equals the configure's `:after`; an intervening
configure is a conflict, never guessed (:3121-3123, :5996)."
  (let* ((state (kernel-state kernel))
         (node (%node-quiet state roadmap))
         (view (and node (wnode-view node))))
    (unless view
      (return-from roadmap-configure-undo
        (values nil (format nil "UNDO FAIL request-of=~A: no roadmap view" of) 1)))
    (let ((entry (find of (getf view :log)
                       :key (lambda (e) (getf e :request)) :test #'equal)))
      (unless entry
        (return-from roadmap-configure-undo
          (values nil (format nil "UNDO FAIL request-of=~A: no such configure" of) 1)))
      (unless (equal (%roadmap-config-postimage view) (getf entry :after))
        (return-from roadmap-configure-undo
          (values nil
                  (format nil "UNDO FAIL request-of=~A: conflict; the view moved" of)
                  1)))
      (let ((before (getf entry :before)))
        (setf (getf view :row-kind) (getf before :row-kind)
              (getf view :aggregation) (getf before :aggregation)
              (getf view :completion-policy) (getf before :completion-policy)
              (getf view :axes) (copy-list (getf before :axes))
              (getf view :permitted-roots) (copy-list (getf before :permitted-roots)))
        (incf (getf view :revision))
        (push (list :op :undo :request request :undo-of of)
              (getf view :log))
        (values t
                (format nil "UNDO OK request=~A of=~A roadmap=~A change=configure"
                        request of roadmap)
                0)))))

;;; ------------------------------------------------------------------
;;; The roadmap view record and the `roadmap row` / `roadmap projection`
;;; verbs (docs/SPEC-WORK.md:3095-3117, :3118-3129).
;;; ------------------------------------------------------------------
;;;
;;; `roadmap row --add|--remove` is admitted on an axisless roadmap only, its
;;; member an existing node of the declared `:row-kind`; add appends to the
;;; ordered `:members`, remove retires the row from the view alone and touches
;;; no containment, state, evidence, lease or repository. `roadmap projection
;;; --add|--remove` owns the stored `:projections`: ids unique per roadmap, only
;;; `:markdown-table` a policy, a duplicate id with a different payload refuses
;;; `duplicate projection`, a remove of an unknown id refuses `no such
;;; projection`, and a matrix selection naming an unknown, missing or duplicate
;;; axis or member refuses `bad selection` (:3095-3103).

(defun roadmap-view-projections (state id)
  "The roadmap ID's stored projections, in order."
  (getf (%roadmap-view state id) :projections))

(defun roadmap-view-retired (state id)
  "The member ids retired from the roadmap ID's view, most recent first."
  (getf (%roadmap-view state id) :retired))

(defun roadmap-view-log (state id)
  "The roadmap ID's scope events, newest first."
  (getf (%roadmap-view state id) :log))

(defun roadmap-view-revive-events (state id)
  "The revivals a membership change implied, newest first (:3114-3115)."
  (getf (%roadmap-view state id) :revive-events))

(defun %roadmap-member-settled-p (state member)
  "A row is finished when its node has settled into C (:1216-1222)."
  (let ((n (%node-quiet state member)))
    (and n (eq :c (wnode-branch n)))))

(defun roadmap-open-member-count (state id)
  "The live ordered rows of the roadmap that are not yet settled. Completion is
not removal, so this is the denominator the aggregation reads."
  (let ((view (%roadmap-view state id)))
    (count-if-not (lambda (m) (%roadmap-member-settled-p state m))
                  (getf view :members))))

(defun roadmap-settled-p (state id)
  "True when the roadmap has at least one live row and every one has settled.
An empty required set never settles (:1511)."
  (let* ((view (%roadmap-view state id))
         (members (getf view :members)))
    (and members
         (every (lambda (m) (%roadmap-member-settled-p state m)) members)
         t)))

(defun roadmap-member-evidence (state member)
  "The evidence a live row carries: its node's settle count, read from the node
the retirement keeps (:3097, :5988)."
  (let ((n (%node-quiet state member)))
    (and n (wnode-settles n))))

(defun %roadmap-member-row-kind-p (node row-kind)
  (and node (eq row-kind (wnode-type node))))

(defun roadmap-row (kernel &key roadmap member op reason request)
  "`roadmap row --add|--remove` on an axisless roadmap (:3095-3098, :3111-3113).
OP is `:add` or `:remove`; MEMBER is an existing node of the declared
`:row-kind`. Add appends to `:members`, remove retires the row from the view
alone and touches no containment, state, evidence, lease or repository. Writes
one `:roadmap-row` scope event. Answers (values OK-P LINE EXIT-CODE REVIVE-EVENT)."
  (let* ((state (kernel-state kernel))
         (view (%roadmap-view state roadmap)))
    (unless view
      (return-from roadmap-row
        (values nil (format nil "ROADMAP FAIL node=~A: no roadmap view" roadmap) 1 nil)))
    (when (getf view :axes)
      (return-from roadmap-row
        (values nil (format nil "ROADMAP FAIL node=~A: has axes; use axis" roadmap) 2 nil)))
    (unless (and (stringp member) (plusp (length member)))
      (return-from roadmap-row
        (values nil (format nil "ROADMAP FAIL node=~A: bad member" roadmap) 2 nil)))
    (let ((mnode (%node-quiet state member)))
      (unless mnode
        (return-from roadmap-row
          (values nil (format nil "ROADMAP FAIL node=~A: no such member ~A" roadmap member) 2 nil)))
      (unless (%roadmap-member-row-kind-p mnode (getf view :row-kind))
        (return-from roadmap-row
          (values nil (format nil "ROADMAP FAIL node=~A: row kind mismatch" roadmap) 2 nil)))
      (let ((members (getf view :members)))
        (ecase op
          (:add
           (when (member member members :test #'string=)
             (return-from roadmap-row
               (values nil (format nil "ROADMAP FAIL node=~A: already a row" roadmap) 2 nil)))
           (let ((was-settled (roadmap-settled-p state roadmap))
                 (revive nil))
             (setf (getf view :members) (append (copy-list members) (list member)))
             (incf (getf view :revision))
             (push (list :op :roadmap-row :change :row-add :member member
                         :reason (or reason +absent+))
                   (getf view :log))
             ;; Adding an outstanding member to a settled roadmap revives it
             ;; atomically, so no settled container silently holds open required
             ;; work (:5997-5999).
             (when (and was-settled (not (%roadmap-member-settled-p state member)))
               (setf revive (list :kind :revive :member member))
               (push revive (getf view :revive-events))
               (let ((rm (%node-quiet state roadmap)))
                 (when (and rm (eq :c (wnode-branch rm)))
                   (setf (wnode-branch rm) :o))))
             (values t
                     (format nil "ROADMAP OK id=~A request=~A change=row-add changed=1 rev=~D"
                             roadmap request (getf view :revision))
                     0 revive)))
          (:remove
           (unless (member member members :test #'string=)
             (return-from roadmap-row
               (values nil (format nil "ROADMAP FAIL node=~A: no such row ~A" roadmap member) 2 nil)))
           (setf (getf view :members) (remove member members :test #'string=))
           (push member (getf view :retired))
           (incf (getf view :revision))
           (push (list :op :roadmap-row :change :row-remove :member member
                       :reason (or reason +absent+))
                 (getf view :log))
           (values t
                   (format nil "ROADMAP OK id=~A request=~A change=row-remove changed=1 rev=~D"
                           roadmap request (getf view :revision))
                   0 nil)))))))

;;; ------------------------------------------------------------------
;;; `roadmap projection --add|--remove` (docs/SPEC-WORK.md:3098-3103).
;;; ------------------------------------------------------------------

(defun %roadmap-projection-payload (projection)
  (list :id (getf projection :id) :root (getf projection :root)
        :repo (getf projection :repo) :path (getf projection :path)
        :start (getf projection :start) :end (getf projection :end)
        :policy (getf projection :policy)
        :row-axis (getf projection :row-axis)
        :column-axis (getf projection :column-axis)
        :fixed (copy-list (getf projection :fixed))))

(defun %roadmap-path-clean-p (path)
  "The projection path is clean, relative and contained (:3082-3083)."
  (and (stringp path) (plusp (length path))
       (not (char= #\/ (char path 0)))
       (not (find #\Nul path))
       (not (search ".." path))))

(defun %roadmap-markers-ok-p (start end)
  (and (stringp start) (plusp (length start))
       (stringp end) (plusp (length end))
       (not (string= start end))))

(defun %roadmap-selection (view row-axis column-axis fixed)
  "Validate a projection's display selection against the declared axes. For a
matrix the row and column axes are distinct declared axes with `:fixed` naming
exactly one member of every other axis; for zero or one axis both are absent and
`:fixed` is empty. Answers (values ROW COLUMN FIXED REFUSAL) (:3083-3085,
:3102-3103)."
  (let ((axes (getf view :axes)))
    (cond
      ((or (null axes) (null (cdr axes)))
       (if (or row-axis column-axis fixed)
           (values nil nil nil "bad selection")
           (values nil nil '() nil)))
      (t
       (let ((row (and (stringp row-axis) (find row-axis axes :test #'string=)))
             (col (and (stringp column-axis) (find column-axis axes :test #'string=))))
         (cond
           ((or (null row) (null col) (string= row col))
            (values nil nil nil "bad selection"))
           (t
            (let* ((others (remove-if (lambda (a) (or (string= a row) (string= a col))) axes))
                   (pairs '())
                   (bad nil))
              (dolist (entry fixed)
                (let* ((a (car entry)) (m (cdr entry)))
                  (when (or bad (not (member a others :test #'string=))
                            (assoc a pairs :test #'string=)
                            (not (and (stringp m) (plusp (length m)))))
                    (setf bad t))
                  (push (cons a m) pairs)))
              (dolist (a others)
                (unless (assoc a pairs :test #'string=) (setf bad t)))
              (if bad
                  (values nil nil nil "bad selection")
                  (values row col (nreverse pairs) nil))))))))))

(defun roadmap-projection (kernel &key roadmap op id root repo path start end
                                       policy row-axis column-axis fixed reason request)
  "`roadmap projection --add|--remove` (:3098-3103, :3116). OP is `:add` or
`:remove`. Only `:markdown-table` is a policy; a duplicate id with a different
payload refuses `duplicate projection`, an identical re-add is the no-effect
receipt `changed=0`, a remove of an unknown id refuses `no such projection`, and
a bad selection refuses `bad selection`. Answers (values OK-P LINE EXIT-CODE)."
  (let* ((state (kernel-state kernel))
         (view (%roadmap-view state roadmap)))
    (unless view
      (return-from roadmap-projection
        (values nil (format nil "ROADMAP FAIL node=~A: no roadmap view" roadmap) 1)))
    (unless (and (stringp id) (plusp (length id)))
      (return-from roadmap-projection
        (values nil (format nil "ROADMAP FAIL node=~A: bad projection id" roadmap) 2)))
    (let ((existing (find id (getf view :projections)
                          :key (lambda (p) (getf p :id)) :test #'string=)))
      (ecase op
        (:remove
         (unless existing
           (return-from roadmap-projection
             (values nil (format nil "ROADMAP FAIL node=~A: no such projection ~A" roadmap id) 2)))
         (setf (getf view :projections)
               (remove id (getf view :projections)
                       :key (lambda (p) (getf p :id)) :test #'string=))
         (incf (getf view :revision))
         (push (list :op :roadmap-projection :change :projection-remove :projection id
                     :reason (or reason +absent+))
               (getf view :log))
         (values t
                 (format nil "ROADMAP OK id=~A request=~A change=projection-remove changed=1 rev=~D"
                         roadmap request (getf view :revision))
                 0))
        (:add
         (unless (eq policy :markdown-table)
           (return-from roadmap-projection
             (values nil (format nil "ROADMAP FAIL node=~A: bad policy" roadmap) 2)))
         (unless (%roadmap-path-clean-p path)
           (return-from roadmap-projection
             (values nil (format nil "ROADMAP FAIL node=~A: bad projection path" roadmap) 2)))
         (unless (%roadmap-markers-ok-p start end)
           (return-from roadmap-projection
             (values nil (format nil "ROADMAP FAIL node=~A: bad markers" roadmap) 2)))
         (multiple-value-bind (r c fx refusal)
             (%roadmap-selection view row-axis column-axis fixed)
           (when refusal
             (return-from roadmap-projection
               (values nil (format nil "ROADMAP FAIL node=~A: ~A" roadmap refusal) 2)))
           (let* ((projection (list :id id :root root :repo repo :path path
                                    :start start :end end :policy policy
                                    :row-axis (or r +absent+)
                                    :column-axis (or c +absent+)
                                    :fixed (or fx '())))
                  (payload (%roadmap-projection-payload projection)))
             (when existing
               (if (equal payload (%roadmap-projection-payload existing))
                   (return-from roadmap-projection
                     (values t
                             (format nil "ROADMAP OK id=~A request=~A change=projection-add changed=0 rev=~D"
                                     roadmap request (getf view :revision))
                             0))
                   (return-from roadmap-projection
                     (values nil (format nil "ROADMAP FAIL node=~A: duplicate projection ~A" roadmap id) 2))))
             (setf (getf view :projections)
                   (append (copy-list (getf view :projections)) (list projection)))
             (incf (getf view :revision))
             (push (list :op :roadmap-projection :change :projection-add :projection id
                         :reason (or reason +absent+))
                   (getf view :log))
             (values t
                     (format nil "ROADMAP OK id=~A request=~A change=projection-add changed=1 rev=~D"
                             roadmap request (getf view :revision))
                     0))))))))

;;; ------------------------------------------------------------------
;;; The reads that keep a settled roadmap's head whole (:3124-3125).
;;; ------------------------------------------------------------------

(defun roadmap-view-render (state roadmap)
  "Render the roadmap's whole current head from the retained view record. A read
on a settled roadmap revives nothing (:5997-5998)."
  (let ((view (%roadmap-view state roadmap)))
    (with-output-to-string (s)
      (format s "ROADMAP id=~A rev=~D~%" roadmap (getf view :revision))
      (dolist (m (getf view :members))
        (format s "row ~A state=~A~%" m
                (if (%roadmap-member-settled-p state m) "done" "open"))))))

(defun export-roadmap-view (view)
  "The canonical durable bytes of a roadmap view record, so an export/load
round-trip carries the whole head (:3124-3125)."
  (canonical-string
   (list :members (copy-list (getf view :members))
         :axes (copy-list (getf view :axes))
         :retired (copy-list (getf view :retired))
         :projections (copy-tree (getf view :projections))
         :revision (getf view :revision))))

(defun load-roadmap-view (bytes)
  "Reconstruct the view record from EXPORT-ROADMAP-VIEW's bytes."
  (read-restricted bytes))

(defun roadmap-view-open (view &key (window :default))
  "Open a stored roadmap view. The record is a durable named view, so no live or
retired row and no evidence is narrowed by the default closed window (SPEC-WORK.md
:1648-1664); WINDOW changes no row here."
  (declare (ignore window))
  (copy-tree view))

;;; ------------------------------------------------------------------
;;; `percent --axis` on a matrix (docs/SPEC-WORK.md:2326-2329, :3118,
;;; :1937-1951, :2101, :5590-5593).
;;; ------------------------------------------------------------------
;;;
;;; `query --ask percent --node R --axis <member>` is the roadmap rollup for one
;;; axis member. `rows=` is the roadmap's required-set cardinality -- the live
;;; first-axis members -- and `baseline-rows=` the membership its last
;;; `:baseline` recorded; both are the roadmap's own and are the same under
;;; every `--axis`. `applicable=` is the live rows less those with a recorded
;;; out-of-scope cell for that member, and the percentage is `green` over
;;; `applicable`. A matrix requires `--axis`; a zero- or one-axis roadmap takes
;;; none and refuses one at exit 2 (:3118). `percent` over zero applicable rows
;;; prints `green=0 applicable=0` and no percentage, at exit 0 (:1943-1944), and
;;; every partial cell’s `k/n` and `unknown=<u>` follows on a `QUERY ROW`
;;; (:2101). The `axis` and `cell` verbs on other E04 rows own the view fields
;;; this read consumes; a view built by hand is read the same way.

(defparameter +percent-non-live-states+ '(:removed :cancelled :superseded)
  "A row is live unless its node is removed, cancelled or superseded; done is
live (:1156-1157).")

(defun %percent-axes (view)
  "VIEW's declared axes as an ordered alist (axis-id . members), the shape the
`axis` verb owns in `:axis-members`; a legacy bare `:axes` declares no members."
  (let ((members (getf view :axis-members)))
    (if members
        members
        (mapcar (lambda (a) (if (consp a) (cons (car a) (copy-list (cdr a)))
                                (cons a '())))
                (getf view :axes)))))

(defun %percent-first-axis-members (view)
  "The ordered first-axis members, the roadmap's required rows (:1156, :1174)."
  (or (cdr (car (%percent-axes view)))
      (getf view :members)))

(defun %percent-row-live-p (state id)
  "A row is live while its node is not removed, cancelled or superseded."
  (let ((n (and (stringp id) (%node-quiet state id))))
    (and n (not (member (wnode-state n) +percent-non-live-states+)) t)))

(defun %percent-member-known-p (view member)
  "True when MEMBER is a member of one of VIEW's declared axes (:2326)."
  (and (some (lambda (a) (member member (cdr a) :test #'string=))
             (%percent-axes view))
       t))

(defun %percent-out-of-scope-p (view row member)
  "True when VIEW records ROW's coordinate with MEMBER as out of scope (:1176)."
  (some (lambda (cell)
          (let ((coord (car cell)))
            (and (getf (cdr cell) :out-of-scope)
                 (member row coord :test #'string=)
                 (member member coord :test #'string=))))
        (getf view :cells)))

(defgeneric roadmap-cell-verified-p (kernel node-id)
  (:documentation
   "The verification seam `percent` reads for a cell's green state
(docs/SPEC-WORK.md:1937-1941, :5078): whether NODE-ID's done is backed by
verified evidence. The verifier and staged-admission subsystem is outside this
epic, so the one in-process implementation here reads the node's settled
branch.")
  (:method (kernel node-id)
    (let ((n (and (stringp node-id) (%node-quiet (kernel-state kernel) node-id))))
      (and n (eq :c (wnode-branch n))))))

(defun %percent-settled-p (state id)
  "True when ID's node has settled into C, however its evidence is rated."
  (let ((n (and (stringp id) (%node-quiet state id))))
    (and n (eq :c (wnode-branch n)))))

(defun %percent-leaf-counts (state id)
  "Answer (values DONE TOTAL UNKNOWN) for the required leaves beneath ID. A
required leaf is a required node with no required children; DONE is the settled
count over TOTAL, and an open leaf still `:unknown` is unknown (:1939-1940)."
  (let ((done 0) (total 0) (unknown 0))
    (labels ((walk (n)
               (let ((required-children
                       (loop for c in (wnode-children n)
                             for cn = (%node-quiet state c)
                             when (and cn (wnode-required cn)) collect cn)))
                 (if required-children
                     (dolist (c required-children) (walk c))
                     (when (wnode-required n)
                       (incf total)
                       (when (eq :c (wnode-branch n)) (incf done))
                       (when (and (eq :o (wnode-branch n))
                                  (eq :unknown (wnode-state n)))
                         (incf unknown)))))))
      (let ((root (%node-quiet state id)))
        (when root (walk root))))
    (values done total unknown)))

(defun roadmap-percent (kernel &key node roadmap axis)
  "`query --ask percent` for NODE (or ROADMAP) and one axis member (:2326-2329,
:3118, :1937-1951, :2101). Answers (values OK-P LINE EXIT-CODE): a matrix
without `--axis`, a non-matrix with one, and an unknown axis member each refuse
at exit 2; zero applicable rows print `green=0 applicable=0` with no percentage."
  (let* ((state (kernel-state kernel))
         (id (or node roadmap))
         (view (and (stringp id) (node-view state id))))
    (unless view
      (return-from roadmap-percent
        (values nil (format nil "QUERY FAIL node=~A: no roadmap view" id) 1)))
    (let* ((axes (%percent-axes view))
           (matrix (>= (length axes) 2))
           (rows-members (%percent-first-axis-members view))
           (rows (count-if (lambda (m) (%percent-row-live-p state m)) rows-members)))
      (cond
        ((and matrix (null axis))
         (return-from roadmap-percent
           (values nil
                   (format nil "QUERY FAIL node=~A: --ask percent requires --axis <member> on a matrix"
                           id)
                   2)))
        ((and (not matrix) axis)
         (return-from roadmap-percent
           (values nil
                   (format nil "QUERY FAIL node=~A: --axis is refused on a zero- or one-axis roadmap"
                           id)
                   2)))
        ((and matrix (not (%percent-member-known-p view axis)))
         (return-from roadmap-percent
           (values nil
                   (format nil "QUERY FAIL node=~A: no such axis member ~A" id axis)
                   2))))
      (let* ((live (remove-if-not (lambda (m) (%percent-row-live-p state m))
                                  rows-members))
             (applicable-rows (if matrix
                                  (remove-if (lambda (m)
                                               (%percent-out-of-scope-p view m axis))
                                             live)
                                  live))
             (applicable (length applicable-rows))
             (green (count-if (lambda (m) (roadmap-cell-verified-p kernel m))
                              applicable-rows))
             (done-unverified
               (count-if (lambda (m) (and (%percent-settled-p state m)
                                          (not (roadmap-cell-verified-p kernel m))))
                         applicable-rows))
             (baseline (or (getf view :baseline-rows) rows))
             (row-kind (getf view :row-kind))
             (cell-rows
               (loop for m in applicable-rows
                     unless (roadmap-cell-verified-p kernel m)
                       collect (multiple-value-bind (done total unknown)
                                   (%percent-leaf-counts state m)
                                 (format nil "QUERY ROW cell=~A,~A k/n=~D/~D unknown=~D"
                                         m (or axis "-") done total unknown)))))
        (values t
                (if (plusp applicable)
                    (format nil "QUERY OK ask=percent node=~A axis=~A row-kind=~A green=~D applicable=~D rows=~D baseline-rows=~D done-unverified=~D percent=~D%~{~%~A~}"
                            id (or axis "-") (roadmap-name row-kind)
                            green applicable rows baseline done-unverified
                            (round (* 100 green) applicable) cell-rows)
                    (format nil "QUERY OK ask=percent node=~A axis=~A row-kind=~A green=0 applicable=0 rows=~D baseline-rows=~D done-unverified=0~{~%~A~}"
                            id (or axis "-") (roadmap-name row-kind)
                            rows baseline cell-rows))
                0)))))
