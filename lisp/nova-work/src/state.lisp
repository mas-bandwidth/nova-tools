;;;; state.lisp --- the root, its two branches, and the counters kept on write.
;;;;
;;;; docs/SPEC-WORK.md:1169 — "The root is COW: closed, open, working. The root
;;;; is `(root C O)`". C and O hold the same nodes told apart by one derived
;;;; fact; this is a partition and not a second ledger (SPEC-WORK.md:1187-1190).
;;;;
;;;; SPEC-WORK.md:1562 — "|O| is a counter carried by every accepted mutation
;;;; envelope and is read, never computed ... The root's open-item count, and
;;;; the per-repository and per-container counts beneath it, are updated by the
;;;; same envelope that moves an item ... before its OK line is printed".
;;;; Every counter here moves on the write path, along the item's containment
;;;; ancestors only, and never by a walk of O on a read.

(in-package #:nova-work)

(defstruct (wnode (:conc-name wnode-))
  id type parent coordinator children required required-count required-open state branch
  open-count
  ;; The five permitted metadata fields of `node edit` (SPEC-WORK.md:5798). An
  ;; absent field is +ABSENT+ and an empty or false value is kept as itself, so
  ;; keep, clear, set-empty and set-false are four distinguishable states.
  (links +absent+) (title +absent+) (category +absent+)
  (private +absent+) (version +absent+)
  ;; SPEC-WORK.md:850-886 -- `:deps` is a reference edge to another node:
  ;; needed, not owned, not counted. DEPENDENTS is the reverse edge, kept so a
  ;; revert reaches the dependents it breaks in one bounded walk.
  deps dependents
  ;; The flag a revert of a need raises on its dependents (SPEC-WORK.md:2110).
  needs-broken
  ;; SPEC-WORK.md:3001 -- `node edit` owns exactly these five metadata fields.
  ;; They live on the node like every other value and move on write. :repo is
  ;; the `--repo` a root work-set may hold (SPEC-WORK.md:2846); :view is the
  ;; roadmap's view record, written with its node by the one creator. :meta-log
  ;; is the engine's append-only edit history, newest first, the :before/after
  ;; pair `node edit` keeps outside the payload digest for its undo.
  repo view meta-log
  ;; SPEC-WORK.md:1222 -- the newest row of an id carries revived=<rev|-> and
  ;; settles=<n>. Both are kept on the node and moved on write, like every
  ;; other counter here, so a row is written and never computed by a scan.
  settles revived
  ;; SPEC-WORK.md:4651 -- every node carries an estimate (units, hours, tokens,
  ;; usd; who estimated, when). It is read, never derived, and a node without
  ;; one holds +ABSENT+ so an estimate of zero stays a value.
  estimate
  ;; SPEC-WORK.md:1674-1689 -- the live lease's holder, or NIL for
  ;; `holder=unowned`. W is the view of O nodes whose holder is live, never a
  ;; field of its own.
  holder)

(defstruct (wstate (:conc-name wstate-))
  seed       ; the seed forest, verbatim, so a reconstruction starts where this did
  nodes      ; id -> wnode
  order      ; ids in seed order
  root-open  ; |O|
  closed     ; |C|
  leaf-open  ; the open leaf-task counter -- separate, and never labelled |O|
  issue-open ; the open linked-issue counter -- separate, and never labelled |O|
  history    ; envelope records, newest first
  rows       ; closed-index rows, newest first
  revision
  ;; SPEC-WORK.md:1674-1680 -- the lease log, newest first, "kept whole for
  ;; handoffs". A settle of a live lease appends a :release here.
  lease-log)

(defun %node (state id)
  "Every node access goes through here so *VISITS* is honest."
  (incf *visits*)
  (gethash id (wstate-nodes state)))

(defun %node-quiet (state id)
  "Node access on a path that is not a read of the work set: the write path's
own ancestor walk, and serialization of the whole state."
  (gethash id (wstate-nodes state)))

;;; The five permitted metadata fields and their tagged patches
;;; (SPEC-WORK.md:5798). A patch is (:keep), (:clear) or (:set V).

(defparameter *metadata-fields* '(:title :category :links :private :version))
(defparameter *metadata-patch-keys*
  '((:title . :title-patch) (:category . :category-patch) (:links . :links-patch)
    (:private . :private-patch) (:version . :version-patch)))

(defun %links-list (value)
  (if (absentp value) '() value))

(defun wnode-field (node field)
  (ecase field
    (:title (wnode-title node))
    (:category (wnode-category node))
    (:links (wnode-links node))
    (:private (wnode-private node))
    (:version (wnode-version node))))

(defun (setf wnode-field) (value node field)
  (ecase field
    (:title (setf (wnode-title node) value))
    (:category (setf (wnode-category node) value))
    (:links (setf (wnode-links node) value))
    (:private (setf (wnode-private node) value))
    (:version (setf (wnode-version node) value))))

(defun %apply-patch (current patch)
  "The postimage of one tagged patch over CURRENT."
  (case (car patch)
    (:keep current)
    (:clear +absent+)
    (:set (second patch))
    (t (error 'unsupported-input
              :what (format nil "malformed patch ~S" patch)))))

(defun %field-expected-type (field)
  (ecase field
    (:title "a text")
    (:category "a text")
    (:links "a list of text")
    (:private "a boolean")
    (:version "a text")))

(defun %field-type-ok-p (field value)
  (ecase field
    ((:title :category :version) (stringp value))
    (:links (and (listp value) (every #'stringp value)))
    ;; A boolean is a value of the restricted-data grammar, which carries no T:
    ;; :true and :false are the two spellings and both are values, never absent.
    (:private (member value '(:true :false)))))

(defun node-metadata-reader (reader state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (funcall reader n)))

(defun node-title (state id) (node-metadata-reader #'wnode-title state id))
(defun node-category (state id) (node-metadata-reader #'wnode-category state id))
(defun node-links (state id) (node-metadata-reader #'wnode-links state id))
(defun node-private (state id) (node-metadata-reader #'wnode-private state id))
(defun node-version (state id) (node-metadata-reader #'wnode-version state id))

(defun metadata-digest (state)
  "A digest of the five metadata fields of every node, in id order. A no-effect
edit leaves it unchanged; a real edit moves it (SPEC-WORK.md:5627)."
  (sha256-hex
   (canonical-string
    (loop for id in (sort (copy-list (wstate-order state)) #'string<)
          collect (let ((n (%node-quiet state id)))
                    (list id (wnode-title n) (wnode-category n) (wnode-links n)
                          (wnode-private n) (wnode-version n)))))))

(defun %seed-required (spec)
  "Read the internal seed's boolean without accepting truthy lookalikes. An
absent field defaults to T; an explicitly supplied value is exactly T or NIL."
  (let* ((missing (gensym "MISSING-REQUIRED-"))
         (value (getf spec :required missing)))
    (cond ((eq value missing) t)
          ((eq value t) t)
          ((null value) nil)
          (t (error 'unsupported-input
                    :what "required must be the internal boolean T or NIL")))))

(defun make-seed-state (nodes)
  (let ((table (make-hash-table :test #'equal))
        (order '()))
    (dolist (spec nodes)
      (let ((id (getf spec :id)))
        (when (gethash id table)
          (error 'unsupported-input :what (format nil "rule 1: duplicate id ~A" id)))
        (push id order)
        (setf (gethash id table)
              (make-wnode :id id
                          :type (getf spec :type)
                          :parent (getf spec :parent)
                          ;; SPEC-WORK.md:4256 -- the coordination tree is a
                          ;; distinct structure from the work containment tree:
                          ;; a node's :coordinator is its one direct coordinating
                          ;; parent, independent of its containment :parent.
                          :coordinator (getf spec :coordinator)
                          :children '()
                          ;; The approved data model defaults :required to true.
                          ;; NIL is the restricted-data spelling used by this
                          ;; static seed subset for an explicitly optional node.
                          :required (%seed-required spec)
                          :required-count 0
                          :required-open 0
                          :state (getf spec :state :unknown)
                          :branch :o
                          :open-count 0
                          :deps (copy-list (getf spec :deps))
                          :dependents '()
                          :needs-broken nil
                          :links (getf spec :links +absent+)
                          :title (getf spec :title +absent+)
                          :category (getf spec :category +absent+)
                          :private (getf spec :private +absent+)
                          :version (getf spec :version +absent+)
                          :repo (getf spec :repo)
                          :view nil
                          :meta-log '()
                          :settles 0
                          :revived "-"
                          :estimate (getf spec :estimate +absent+)
                          :holder nil))))
    (setf order (nreverse order))
    ;; Containment edges, in seed order.
    (dolist (id order)
      (let* ((node (gethash id table))
             (parent (and (wnode-parent node) (gethash (wnode-parent node) table))))
        (when (and (wnode-parent node) (null parent))
          (error 'unsupported-input
                 :what (format nil "rule 2: ~A names a parent that does not exist" id)))
        (when parent
          (setf (wnode-children parent)
                (append (wnode-children parent) (list id)))
          (when (wnode-required node)
            (incf (wnode-required-count parent))))))
    ;; SPEC-WORK.md:3347 referential-integrity -- "duplicate ids, dangling
    ;; references, cycles, conflicting parents ... all fail BEFORE publication".
    ;; Rule 1 is above and rule 2 is in the edge walk; rule 3 is the forest
    ;; check, and it is here rather than left to the ancestor walk, which would
    ;; spin on a cycle instead of refusing. It is bounded by the node count.
    (let ((limit (hash-table-count table)))
      (dolist (id order)
        (let ((cur id) (steps 0))
          (loop while cur
                do (when (> (incf steps) limit)
                     (error 'unsupported-input
                            :what (format nil "rule 3: :children edges are not a forest; a cycle of :parent through ~A"
                                           id)))
                   (let ((node (gethash cur table)))
                     (unless node (return))
                     (setf cur (wnode-parent node)))))))
    ;; SPEC-WORK.md:850-886 -- `:deps` is a reference edge, not containment:
    ;; needed, not owned, not counted. Rule 2 checks every need resolves (against
    ;; O's nodes, as the seed holds only O) and builds the reverse edge; rule 3
    ;; refuses a dependency cycle, "a deadlock nobody can finish" (:5041).
    (dolist (id order)
      (let ((deps (wnode-deps (gethash id table))))
        (unless (or (null deps) (listp deps))
          (error 'unsupported-input
                 :what (format nil "deps of ~A is not a list" id)))
        (dolist (dep deps)
          (unless (and (stringp dep) (plusp (length dep)))
            (error 'unsupported-input
                   :what (format nil "rule 2: ~A names a dependency that is not a non-empty id" id)))
          (let ((target (gethash dep table)))
            (unless target
              (error 'unsupported-input
                     :what (format nil "rule 2: ~A needs ~A which does not exist" id dep)))
            (push id (wnode-dependents target))))))
    (let ((color (make-hash-table :test #'equal)))
      (labels ((visit (id)
                 (let ((c (gethash id color)))
                   (cond ((eq c :grey)
                          (error 'unsupported-input
                                 :what (format nil "rule 3: :deps edges contain a cycle through ~A" id)))
                         ((eq c :black) nil)
                         (t (setf (gethash id color) :grey)
                            (let ((node (gethash id table)))
                              (when node
                                (dolist (dep (wnode-deps node)) (visit dep))))
                            (setf (gethash id color) :black))))))
        (dolist (id order) (visit id))))
    ;; SPEC-WORK.md:4256 the coordination tree -- a node has at most one direct
    ;; coordinating parent (the single :coordinator slot), that parent must
    ;; exist, and the :coordinator edges must be a forest. A reference or
    ;; sibling edge (:links) is not a coordinating edge and is not consulted
    ;; here. Rule 2 refuses a dangling parent; the bounded walk refuses a cycle.
    (dolist (id order)
      (let ((coord (wnode-coordinator (gethash id table))))
        (when (and coord (null (gethash coord table)))
          (error 'unsupported-input
                 :what (format nil "rule 2: ~A names a coordinating parent that does not exist" id)))))
    (let ((limit (hash-table-count table)))
      (dolist (id order)
        (let ((cur id) (steps 0))
          (loop while cur
                do (when (> (incf steps) limit)
                     (error 'unsupported-input
                            :what (format nil "rule 3: :coordinator edges are not a forest; a cycle of :coordinator through ~A"
                                          id)))
                   (let ((node (gethash cur table)))
                     (unless node (return))
                     (setf cur (wnode-coordinator node)))))))
    (let ((state (make-wstate :seed (copy-tree nodes) :nodes table :order order
                              :root-open 0 :closed 0 :leaf-open 0 :issue-open 0
                              :history '() :rows '() :revision 0 :lease-log '())))
      ;; Seed the counters once, on the write path that builds the set.
      (dolist (id order)
        (%adjust-counters state id 1))
      ;; The seed is entirely open; |C| starts at zero rather than at the
      ;; mirror of the seeding walk.
      (setf (wstate-closed state) 0)
      state)))

(defun %adjust-counters (state id delta)
  "Move the root counter, every containment ancestor's counter, and the two
separate counters, by DELTA. The walk is the item's ancestor chain, which is
bounded by depth and is acyclic by rule 3 above; it is never a walk of O.

A CONTAINER IS IN ITS OWN COUNT -- the chain starts at the item itself, so a
container's counter is the open canonical item ids in its subtree INCLUDING
itself. SPEC-WORK.md:1568 says only \"the per-repository and per-container
counts beneath it\", where \"beneath it\" is beneath the root; it does not
settle self-inclusion. The root's |O| counts containers as items -- five at the
suite's seed, of which three are containers -- and :1573 says the counters
\"count canonical item ids once\", so a per-container count that excluded its
own id would not be the same counting rule one level down. Decision for review."
  (let ((node (%node-quiet state id)))
    (let ((cur id))
      (loop while cur
            do (let ((n (%node-quiet state cur)))
                 (unless n (return))
                 (incf (wnode-open-count n) delta)
                 (setf cur (wnode-parent n)))))
    ;; A required set contains direct members, so only the containment parent
    ;; moves here. This counter makes a cascade decision proportional to depth
    ;; rather than to the number of siblings.
    (let ((parent (and (wnode-parent node)
                       (%node-quiet state (wnode-parent node)))))
      (when (and parent (wnode-required node))
        (incf (wnode-required-open parent) delta)))
    (incf (wstate-root-open state) delta)
    (decf (wstate-closed state) delta)
    (when (member (wnode-type node) '(:task :bug))
      (incf (wstate-leaf-open state) delta))
    (incf (wstate-issue-open state) (* delta (length (%links-list (wnode-links node)))))))

;;; Reads that are the counters themselves. No node is visited here.

(defun state-open-count (state) (wstate-root-open state))
(defun state-closed-count (state) (wstate-closed state))
(defun state-revision (state) (wstate-revision state))
(defun state-history (state) (reverse (wstate-history state)))
(defun state-closed-rows (state) (reverse (wstate-rows state)))

;;; Reads of one node. These do visit.

(defun node-open-count (state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-open-count n)))

(defun node-branch (state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-branch n)))

(defun node-state (state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-state n)))

(defun node-coordinator (state id)
  "The node's one direct coordinating parent, or NIL for the root. This is the
coordination tree's edge (SPEC-WORK.md:4256), independent of the containment
:parent and never created by a :links reference."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-coordinator n)))

(defun node-required-count (state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-required-count n)))

(defun node-required-open (state id)
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-required-open n)))

(defun node-deps (state id)
  "The ids this node needs, in the order the seed gave them. A reference edge:
it carries no count and is not a containment."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (copy-list (wnode-deps n))))

(defun node-needs-broken (state id)
  "True when a need of this node was reverted after this node landed
(SPEC-WORK.md:2110, the `needs-broken` flag)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-needs-broken n)))

(defun %need-terminal-p (state id)
  "A need is terminal accepted when its node has settled into C."
  (let ((n (%node-quiet state id)))
    (and n (eq :c (wnode-branch n)))))

(defun ready-p (state id)
  "Open leaf work whose every need is terminal accepted and which no reverted
need has left needs-broken (SPEC-WORK.md:2110, `query ready`)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (and (eq :o (wnode-branch n))
         (member (wnode-type n) '(:task :bug))
         (not (wnode-needs-broken n))
         (every (lambda (dep) (%need-terminal-p state dep)) (wnode-deps n)))))

(defun ready-nodes (state)
  "`query ready`: the ready items, in seed order. This is a read -- it visits
nodes and mutates none."
  (loop for id in (wstate-order state)
        when (ready-p state id) collect id))

(defun %recheck-needs-broken (state id settled-p)
  "Re-evaluate the dependents of ID after it settled (SETTLED-P true, clear the
flag where every need is terminal again) or was reverted (false, raise it)."
  (dolist (dependent (wnode-dependents (%node-quiet state id)))
    (let ((d (%node-quiet state dependent)))
      (when d
        (setf (wnode-needs-broken d)
              (if settled-p
                  (not (every (lambda (dep) (%need-terminal-p state dep))
                              (wnode-deps d)))
                  t))))))
(defun node-estimate (state id)
  "The node's estimate record, or +ABSENT+ when it carries none. A node with an
estimate of zero is not a node without one."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-estimate n)))

(defun forecast (state)
  "The remaining-work forecast (SPEC-WORK.md:4651): a read over the open set
that sums the estimates every open node carries and derives the per-unit and
per-million-token rates from those sums. It computes nothing from the evidence
records and keeps no history; a zero denominator leaves the rate undefined
rather than zero. A view: it never writes, and a closed node is not in it."
  (let ((nodes 0) (units 0) (hours 0) (tokens 0) (usd 0))
    (dolist (id (wstate-order state))
      (let* ((n (%node-quiet state id))
             (e (and n (wnode-estimate n))))
        (when (and n (eq :o (wnode-branch n)) (not (absentp e)))
          (incf nodes)
          (incf units (getf e :units 0))
          (incf hours (getf e :hours 0))
          (incf tokens (getf e :tokens 0))
          (incf usd (getf e :usd 0)))))
    (list :nodes nodes :units units :hours hours :tokens tokens :usd usd
          :usd-per-unit (if (plusp units) (/ usd units) +absent+)
          :hours-per-unit (if (plusp units) (/ hours units) +absent+)
          :usd-per-mtok (if (plusp tokens) (/ usd (/ tokens 1000000)) +absent+))))

;;; The root, as bytes.

(defun root-form (state)
  (list :root
        (list :open (wstate-root-open state))
        (list :closed (wstate-closed state))
        (list :revision (wstate-revision state))
        (list :leaf-open (wstate-leaf-open state))
        (list :issue-open (wstate-issue-open state))
        (cons :nodes
              (loop for id in (sort (copy-list (wstate-order state)) #'string<)
                    collect (let ((n (%node-quiet state id)))
                              (list (wnode-id n) (wnode-type n) (wnode-state n)
                                    (wnode-branch n) (wnode-open-count n)
                                    (if (wnode-required n) :required :optional)
                                    (wnode-required-count n)
                                    (wnode-required-open n)))))))

(defun root-digest (state)
  (sha256-hex (canonical-string (root-form state))))

(defun state-canonical-form (state)
  "The durable bytes: the seed the set began at, and the append-only history."
  (list :seed (wstate-seed state)
        :history (state-history state)))

;;; Copy-on-write, so an envelope is applied to a candidate and the candidate is
;;; installed only when the whole of it succeeded.

(defun copy-state (state)
  (let ((table (make-hash-table :test #'equal :size (hash-table-count (wstate-nodes state)))))
    (maphash (lambda (id node) (setf (gethash id table) (copy-wnode node)))
             (wstate-nodes state))
    (make-wstate :seed (wstate-seed state)
                 :nodes table
                 :order (wstate-order state)
                 :root-open (wstate-root-open state)
                 :closed (wstate-closed state)
                 :leaf-open (wstate-leaf-open state)
                 :issue-open (wstate-issue-open state)
                 :history (wstate-history state)
                 :rows (wstate-rows state)
                 :revision (wstate-revision state)
                 :lease-log (wstate-lease-log state))))

;;; Applying one event. The live path and the replay path share it, which is
;;; what makes the reconstruction independent of the live counters.

(defun apply-event (state event)
  (let ((kind (work-event-kind event)))
    ;; A recorded external effect is an outcome, not a verb of the state
    ;; grammar: it advances the revision and changes nothing else.
    (when (eq kind :external)
      (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
      (return-from apply-event state))
    ;; The six new verbs draft 26 added write the `friends`, `models` and
    ;; `undo`/`redo` indexes, which are CONFIG and not work: their events name
    ;; no containment node, so they advance the revision and move no node, no
    ;; count, no roadmap and no required set (SPEC-WORK.md:1054-1058, replay
    ;; new-verbs-have-a-kind-and-a-field-order).
    (when (member kind '(:undo :redo :friend :model :observe :config))
      (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
      (return-from apply-event state)))
  (let* ((id (work-event-node event))
         (node (%node-quiet state id)))
    (unless node
      (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (ecase (work-event-kind event)
      (:edit
       ;; The five permitted metadata fields, each a tagged patch.
       (dolist (field *metadata-fields*)
         (let* ((key (cdr (assoc field *metadata-patch-keys*)))
                (patch (getf (work-event-fields event) key +absent+)))
           (unless (absentp patch)
             (setf (wnode-field node field)
                   (%apply-patch (wnode-field node field) patch))))))
      (:terminal
       (setf (wnode-state node) (getf (work-event-fields event) :disposition)))
      (:transition
       (setf (wnode-state node) (getf (work-event-fields event) :to)))
      (:reopen
       (setf (wnode-state node) :todo))
      (:settle
       (unless (eq :o (wnode-branch node))
         (error 'unsupported-input :what (format nil "rule 18: ~A is already in C" id)))
       (setf (wnode-branch node) :c)
       (%adjust-counters state id -1)
       ;; SPEC-WORK.md:1674-1680 -- "the settle envelope carries a :release for
       ;; a live lease on the node, written by the settling author and naming
       ;; the holder it ended ... no item of C holds a live lease". The release
       ;; is appended to the lease log and the node reads holder=unowned.
       (when (wnode-holder node)
         (push (list :kind :release :node id
                     :by (work-event-by event)
                     :holder (wnode-holder node)
                     :stamp (work-event-stamp event)
                     :rev (work-event-rev event))
               (wstate-lease-log state))
         (setf (wnode-holder node) nil))
       (incf (wnode-settles node))
       (setf (wnode-revived node) "-")
       (push (list :key (closed-row-key event) :kind :settle :node id
                   :rev (work-event-rev event)
                   :disposition (getf (work-event-fields event) :disposition)
                   :reason (getf (work-event-fields event) :reason +absent+)
                   :already-closed (getf (work-event-fields event) :already-closed +absent+)
                   :stamp (work-event-stamp event)
                    :revived (wnode-revived node)
                    :settles (wnode-settles node))
             (wstate-rows state))
       ;; A settle is a terminal accepted need: re-evaluate the dependents it
       ;; unblocks, clearing needs-broken where their needs are all terminal.
       (%recheck-needs-broken state id t))
      (:revive
       (unless (eq :c (wnode-branch node))
         (error 'unsupported-input :what (format nil "rule 18: ~A is not in C" id)))
       (setf (wnode-branch node) :o)
       (%adjust-counters state id 1)
       ;; A revert breaks the dependents that had counted on this need: flag
       ;; them needs-broken so they are re-evaluated, never silently launched.
       (%recheck-needs-broken state id nil)
       (setf (wnode-revived node) (work-event-rev event))
       (push (list :key (closed-row-key event) :kind :revive :node id
                   :rev (work-event-rev event)
                   :disposition (getf (work-event-fields event) :disposition +absent+)
                   :stamp (work-event-stamp event)
                   :revived (wnode-revived node)
                   :settles (wnode-settles node))
             (wstate-rows state))))
    (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
    state))

(defun apply-envelope (state envelope)
  "Pure: STATE is never touched. The candidate is built whole and returned, so
the caller installs all of it or none of it."
  (let ((candidate (copy-state state)))
    (dolist (event (getf envelope :events))
      (apply-event candidate event))
    (push (list :request (getf envelope :request)
                :digest (getf envelope :digest)
                :events (mapcar #'event-record-form (getf envelope :events)))
          (wstate-history candidate))
    candidate))

(defun reconstruct-state (text)
  "A full independent reconstruction: parse the canonical bytes, seed a fresh
set, and replay the history over it."
  (let* ((form (read-restricted text))
         (seed (getf form :seed))
         (history (getf form :history))
         (state (make-seed-state seed)))
    (dolist (record history)
      (incf *replays*)
      (let ((events (loop for e in (getf record :events)
                          collect (record-form->event
                                   e :session-written-p
                                   (member (getf e :kind) '(:settle :revive))))))
        (setf state (apply-envelope state (list :request (getf record :request)
                                                :digest (getf record :digest)
                                                :events events)))))
    state))
