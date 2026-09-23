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
  lease-log
  ;; The CONFIG/ACTIVE halves the six journaled verbs write: the fleet's
  ;; `:kind :machine` members, the `:kind :route` model routes, and the one
  ;; allocator per physical machine with its allocations and observations
  ;; (nova-tools#1695). They live ON THE STATE, not on the kernel, so the
  ;; half a `replay-journal` reconstruction rebuilds from the journaled
  ;; events (apply-event below) is the half the kernel reads after a
  ;; restart; the kernel reaches them through KERNEL-FLEET,
  ;; KERNEL-ROUTES and KERNEL-ALLOCATIONS (src/kernel.lisp). A work envelope
  ;; never touches them, so `copy-state` carries the very objects and no
  ;; candidate install can orphan a live CONFIG member.
  fleet routes allocations)

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
                          :holder (getf spec :holder)))))
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
                              :history '() :rows '() :revision 0 :lease-log '()
                              ;; The CONFIG/ACTIVE halves start empty beside
                              ;; the seed (nova-tools#1695): the fleet's
                              ;; friends are the session's own CONFIG
                              ;; (make-kernel sets them), and the members,
                              ;; routes and allocations arrive through the
                              ;; journaled verbs and through replay only.
                              :fleet (make-fleet)
                              :routes (make-route-registry)
                              :allocations (make-fleet-registry))))
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
(defun state-node-ids (state)
  "Every id the set holds, in seed order. A read."
  (copy-list (wstate-order state)))
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

;;; ------------------------------------------------------------------
;;; the recorded half of rule 1 (SPEC-WORK.md:4780-4867, nova-tools #785)
;;; ------------------------------------------------------------------
;;;
;;; src/needs.lisp holds the one predicate, `need-met-p`, which adds rule 1's
;;; verified half over a NEEDS-VIEW. It cannot live here: it reads the
;;; verification cache, and src/verifier.lisp loads after this file. What lives
;;; here is what the write path itself needs -- the closed-index row that says
;;; how a need settled, the `:revive` that says it was reopened, and the
;;; container clause -- so that `ready-p` and the settle/revive recheck read the
;;; same recorded facts `need-met-p` does.

(defun %need-settle-row (state id)
  "The newest `:settle` row of ID in the closed index, or NIL when the page that
says how it settled cannot be read. Rows are newest first."
  (find-if (lambda (row)
             (and (eq :settle (getf row :kind))
                  (equal id (getf row :node))))
           (wstate-rows state)))

(defun %need-revived-p (state id)
  "True when ID's closed-index rows hold a `:revive`: it settled and was
reopened (rule 2, row 2, SPEC-WORK.md:4820)."
  (and (find-if (lambda (row)
                  (and (eq :revive (getf row :kind))
                       (equal id (getf row :node))))
                (wstate-rows state))
       t))

(defparameter *need-container-types* '(:work-set :epic :feature :roadmap)
  "The container kinds of rule 2's container clause (SPEC-WORK.md:4849). A
container has no evidence of its own and is met with its direct required
members.")

(defun %need-container-p (node)
  (and node (member (wnode-type node) *need-container-types*) t))

(defparameter *need-unaccepted-dispositions* '(:cancelled :superseded :removed)
  "The three closed-and-not-accepted dispositions of rule 2, row 4
(SPEC-WORK.md:4820). A need carrying one is never met and never becomes met.")

(defun %need-closed-unaccepted-p (state id)
  "True when ID carries one of the three unaccepted dispositions, whichever
branch this kernel left it in.

A COMPATIBILITY READING, stated so nobody has to infer it. Rule 2 row 4
(SPEC-WORK.md:4820) says such a need is \"in C\"; this kernel puts only one of
the three there. `node remove` settles into C with disposition `removed`
(src/kernel.lisp:480), while `event --kind cancel` writes a `:terminal` event
that sets the disposition and leaves the node in O (src/edit-undo.lisp:248-253),
and `superseded` has no verb at all. So the DISPOSITION and not the branch is
what is read here, which preserves the safety property :4841 names -- \"never
met and never becomes met\" -- under both spellings.

This predicate deliberately does NOT change the cancel or undo lifecycle: no
new settle transition is introduced, no branch is moved, and nothing else in
this kernel reads a cancelled node differently than it did before. The O/C
discrepancy itself is tracked as its own issue, on Stella's read of #1584."
  (let ((n (%node-quiet state id)))
    (and n
         (or (member (wnode-state n) *need-unaccepted-dispositions*)
             (let ((row (%need-settle-row state id)))
               (and row (member (getf row :disposition)
                                *need-unaccepted-dispositions*))))
         t)))

(defun %need-required-members (state node)
  "The direct required members of a container, in seed order."
  (remove-if-not (lambda (child)
                   (let ((c (%node-quiet state child)))
                     (and c (wnode-required c))))
                 (wnode-children node)))

(defun %need-terminal-p (state id)
  "The recorded half of rule 1: a need is terminal accepted when it is in C with
disposition `done` -- never merely in C.

A `cancelled`, `superseded` or `removed` need is never met and never becomes
met (SPEC-WORK.md:4841); a need in C whose closed-index row cannot be read is
*unavailable*, and incomplete is not green (:4839); a container is met with its
direct required members, and an empty required set never settles (:4849).

Rule 1's other half -- that every evidence event the standing `:to :done` names
is verified from the session's cache -- is `need-met-p` in src/needs.lisp, which
this predicate does not call: the write path holds no verification cache, so a
settle's own recheck reads the recorded facts and the candidate gate reads
both."
  (let ((n (%node-quiet state id)))
    (and n
         (eq :c (wnode-branch n))
         (let ((row (%need-settle-row state id)))
           (and row
                (eq :done (getf row :disposition))
                (if (%need-container-p n)
                    (let ((members (%need-required-members state n)))
                      (and members
                           (every (lambda (m) (%need-terminal-p state m)) members)))
                    t))))))

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
  ;; The CONFIG/ACTIVE registries are carried, never copied: a work envelope's
  ;; candidate install must not orphan a live CONFIG member or an ACTIVE
  ;; allocation the journaled verbs wrote (nova-tools#1695), and only the six
  ;; verbs' own events -- which the live path never routes through here --
  ;; mutate them, on the replay's single lineage.
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
                 :lease-log (wstate-lease-log state)
                 :fleet (wstate-fleet state)
                 :routes (wstate-routes state)
                 :allocations (wstate-allocations state))))

;;; ------------------------------------------------------------------
;;; The six CONFIG/ACTIVE verbs' events, and the replay half that rebuilds
;;; what they wrote (nova-tools#1695)
;;; ------------------------------------------------------------------
;;;
;;; `machine`, `route`, `take`, `heartbeat`, `release` and `probe` journal one
;;; event of their own kind (src/kernel.lisp, %submit-config), and a restart
;;; rebuilds the CONFIG members, the model routes and the one allocator per
;;; machine with its allocations and observations by APPLYING those events
;;; here -- exactly the way the work tree's own envelopes replay. The kinds'
;;; ordered field lists are named here (beside the code that reconstructs
;;; them) rather than in src/event.lisp: %submit-config reads them through
;;; KIND-FIELDS, the journal's record and digest through EVENT-RECORD-FORM,
;;; and this file's apply-event through the replay below.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (dolist (row '((:machine   ; the `machine` verb (SPEC-WORK.md:3541)
                  :change :machine :owner :name :connect :roles :permits
                  :excludes :limits :facts :aliases :generation :workload
                  :key :value :declared-by)
                 (:route      ; the `route` verb (SPEC-WORK.md:2289)
                  :change :route :provider :endpoint :key-location :plan
                  :cost-per-mtok :capabilities :owner :card :pass :wall :usd
                  :benched-until :source)
                 (:allocation ; `take`, `heartbeat`, `release`
                  ; (SPEC-WORK.md:3646-3687)
                  :change :machine :allocation :allocation-id :node :slots
                  :offer :attempt :generation :allocation-generation
                  :request-ref :batch :holder :parent :now :deadline :fenced
                  :stop-observed :not-started)
                 (:probe      ; the fleet's `probe` verb (SPEC-WORK.md:3691)
                  :machine :slot :source :fact :at)))
    (pushnew row *kind-field-order* :test #'equal)))

(defun %config-field (fields key)
  "The event field's value with the absent spelling read as empty. NIL is a
value and survives as itself: an absent field and an empty one are two
spellings, and reconstruction treats both as empty."
  (let ((value (getf fields key)))
    (if (absentp value) nil value)))

(defun %apply-machine-event (state event)
  "Replay half of the `machine` CONFIG verb: rebuild the fleet member and open
its one allocator exactly as src/fleet.lisp's register, retire and change
paths wrote them live, from the journaled event alone."
  (let* ((fields (work-event-fields event))
         (fleet (wstate-fleet state))
         (registry (wstate-allocations state))
         (change (getf fields :change))
         (id (%config-field fields :machine)))
    (case change
      (:register
       (let* ((limits (%config-field fields :limits))
              (roles (%config-field fields :roles))
              (facts (%config-field fields :facts))
              (member (%make-machine
                       :id id
                       :name (%config-field fields :name)
                       :owner (%config-field fields :owner)
                       :connect (%config-field fields :connect)
                       :roles (copy-list roles)
                       :permits (copy-list (%config-field fields :permits))
                       :excludes (copy-list (%config-field fields :excludes))
                       :limits (copy-list limits)
                       :facts (copy-list facts))))
         (setf (gethash id (fleet-machines fleet)) member)
         (push id (fleet-order fleet))
         ;; The machine is CONFIG; opening its one ACTIVE allocator is what a
         ;; slot later becomes takeable with (SPEC-WORK.md:3707-3715).
         (fleet-register-machine registry
                                 :machine-id id
                                 :aliases (%config-field fields :aliases)
                                 :name (%config-field fields :name)
                                 :owner (%config-field fields :owner)
                                 :concurrent (or (getf limits :concurrent) 1)
                                 :cores (getf limits :cores)
                                 :generation (or (%config-field fields :generation) 1)
                                 :facts (copy-list facts)
                                 :limits (copy-list limits)
                                 :connect (%config-field fields :connect)
                                 :roles (copy-list roles))))
      (:retire
       (remhash id (fleet-machines fleet))
       (setf (fleet-order fleet) (remove id (fleet-order fleet) :test #'equal))
       (remhash id (fleet-registry-allocators registry)))
      ((:permit :exclude :limit :fact)
       (let ((member (gethash id (fleet-machines fleet))))
         (when member
           (case change
             (:permit (pushnew (%config-field fields :workload)
                               (machine-permits member) :test #'equal))
             (:exclude (pushnew (%config-field fields :workload)
                                (machine-excludes member) :test #'equal))
             (:limit (setf (machine-limits member)
                           (let ((l (copy-list (machine-limits member))))
                             (setf (getf l (%config-field fields :key))
                                   (%config-field fields :value))
                             l)))
             (:fact
              (setf (machine-facts member)
                    (list* (list :key (%config-field fields :key)
                                 :value (%config-field fields :value)
                                 :declared-by (%config-field fields :declared-by)
                                 :declared-at (work-event-stamp event))
                           (remove (%config-field fields :key) (machine-facts member)
                                   :key (lambda (f) (getf f :key)) :test #'equal)))))
           ;; A meaningful machine CONFIG change moves the live allocator's
           ;; declared numbers and bumps the machine generation
           ;; (SPEC-WORK.md:3724-3730), as %sync-machine-allocator does live.
           (let ((allocator (fleet-allocator-of registry id)))
             (when allocator
               (setf (fleet-allocator-limits allocator)
                     (copy-list (machine-limits member)))
               (setf (fleet-allocator-facts allocator)
                     (copy-list (machine-facts member)))
               (when (eq change :limit)
                 (let ((concurrent (getf (machine-limits member) :concurrent)))
                   (when concurrent
                     (setf (fleet-allocator-concurrent allocator) concurrent))))
               (incf (fleet-allocator-generation allocator))))))))))

(defun %apply-route-event (state event)
  "Replay half of the `route` CONFIG verb: rebuild the `:kind :route` member,
its retirement and its dated probes exactly as src/routes.lisp wrote them
live."
  (let* ((fields (work-event-fields event))
         (registry (wstate-routes state))
         (change (getf fields :change))
         (id (%config-field fields :route)))
    (case change
      (:register
       (setf (gethash id (route-registry-routes registry))
             (make-route :id id
                         :provider (%config-field fields :provider)
                         :endpoint (%config-field fields :endpoint)
                         :key-location (%config-field fields :key-location)
                         :plan (%config-field fields :plan)
                         :cost-per-mtok (%config-field fields :cost-per-mtok)
                         :capabilities (%config-field fields :capabilities)
                         :owner (%config-field fields :owner)))
       (push id (route-registry-order registry)))
      (:retire
       (remhash id (route-registry-routes registry))
       (setf (route-registry-order registry)
             (remove id (route-registry-order registry) :test #'equal)))
      (:probe
       (route-probe registry id
                    :card (%config-field fields :card)
                    :pass (%config-field fields :pass)
                    :wall (%config-field fields :wall)
                    :usd (%config-field fields :usd)
                    :source (%config-field fields :source)
                    :benched-until (%config-field fields :benched-until))))))

(defun %apply-allocation-event (state event)
  "Replay half of the fleet's ACTIVE verbs: rebuild the allocation `take`
  wrote, the renewal a `heartbeat` made and the release a `release` confirmed,
  exactly as src/fleet.lisp wrote them live."
  (let* ((fields (work-event-fields event))
         (registry (wstate-allocations state))
         (change (getf fields :change)))
    (case change
      (:take
       (let* ((machine (%config-field fields :machine))
              (canonical (fleet-resolve registry machine))
              (allocator (gethash canonical (fleet-registry-allocators registry))))
         (when allocator
           (let* ((generation (or (%config-field fields :generation)
                                  (fleet-allocator-generation allocator)))
                  (parent (%config-field fields :parent))
                  (parent-record (and parent (fleet-find-allocation registry parent)))
                  (nested-p (not (null parent-record)))
                  (slots (or (%config-field fields :slots) 1))
                  (holder (or (%config-field fields :holder) (work-event-by event)))
                  (ev (incf (fleet-registry-events registry)))
                  (aid (or (%config-field fields :allocation-id)
                           (format nil "alloc-~A" ev)))
                  (ag (or (%config-field fields :allocation-generation)
                          (format nil "ag-~A" ev)))
                  (slot-no (if nested-p (allocation-record-slot parent-record) 1))
                  (record (make-allocation-record
                           :allocation-id aid :machine canonical :alias machine
                           :slot slot-no :slots slots
                           :node (%config-field fields :node)
                           :batch (%config-field fields :batch)
                           :offer (%config-field fields :offer)
                           :attempt (%config-field fields :attempt)
                           :machine-generation generation :allocation-generation ag
                           :request (work-event-request event)
                           :request-ref (%config-field fields :request-ref)
                           :holder holder
                           :parent (and parent-record
                                        (allocation-record-allocation-id parent-record))
                           :deadline (%config-field fields :deadline)
                           :state :active :admission-phase :before-preparation
                           :core-pin nil
                           :created (or (%config-field fields :now) 0))))
             (push record (fleet-allocator-allocations allocator))
             ;; The take's own retry table, rebuilt with the line the take
             ;; answered -- the format kept identical to fleet-take's
             ;; (src/fleet.lisp) so the reconstructed record is the live one.
             (setf (gethash (work-event-request event)
                            (fleet-allocator-requests allocator))
                   (list :payload (list machine
                                        (%config-field fields :node)
                                        slots
                                        (%config-field fields :offer)
                                        (%config-field fields :attempt)
                                        generation
                                        (%config-field fields :request-ref)
                                        (%config-field fields :batch))
                         :line (format nil
                                       "ALLOC OK id=ev-~4,'0D request=~A machine=~A allocation=~A slot=~A slots=~A node=~A batch=~A offer=~A attempt=~A machine-generation=~A allocation-generation=~A rev=~A pushed=- changed=1 emitted=300"
                                       ev (work-event-request event) canonical aid
                                       slot-no slots
                                       (%config-field fields :node)
                                       (%config-field fields :batch)
                                       (%config-field fields :offer)
                                       (%config-field fields :attempt)
                                       generation ag ev)))))))
       (:heartbeat
       (multiple-value-bind (record allocator)
           (fleet-find-allocation registry (%config-field fields :allocation))
         (declare (ignore allocator))
         (when (and record (allocation-live-p record))
           ;; The renewal a passing heartbeat made (SPEC-WORK.md:3663).
           (let ((now (or (%config-field fields :now) 0)))
             (when (and now (allocation-record-deadline record))
               (setf (allocation-record-deadline record) (+ now 1000)))))))
      (:release
       (multiple-value-bind (record allocator)
           (fleet-find-allocation registry (%config-field fields :allocation))
         (declare (ignore allocator))
         (when (and record (allocation-live-p record))
           ;; Free exactly that allocation's slot after both generations
           ;; validated (SPEC-WORK.md:3667-3687).
           (setf (allocation-record-released record) t
                 (allocation-record-fenced record)
                 (and (%config-field fields :fenced) t))))))))

(defun %apply-probe-event (state event)
  "Replay half of the fleet's `probe` verb: the dated observed ACTIVE evidence
a probe wrote, never a declared CONFIG field (SPEC-WORK.md:3691-3702)."
  (let* ((fields (work-event-fields event))
         (registry (wstate-allocations state))
         (machine (%config-field fields :machine))
         (allocator (fleet-allocator-of registry machine)))
    (when allocator
      (push (list :machine (fleet-resolve registry machine)
                  :slot (%config-field fields :slot)
                  :fact (or (%config-field fields :fact) :observed)
                  :at (or (%config-field fields :at) 0)
                  :source (%config-field fields :source))
            (fleet-allocator-observations allocator)))))

(defun %apply-config-event (state event)
  "Dispatch one CONFIG/ACTIVE event to the half it rebuilds. Only the six
journaled verbs write these kinds (src/kernel.lisp, %submit-config), so this
runs on the replay and reconstruction paths, never between the live verb and
its own mutation."
  (case (work-event-kind event)
    (:machine (%apply-machine-event state event))
    (:route (%apply-route-event state event))
    (:allocation (%apply-allocation-event state event))
    (:probe (%apply-probe-event state event))))


;;; Applying one event. The live path and the replay path share it, which is
;;; what makes the reconstruction independent of the live counters.

(defun apply-event (state event)
  (let ((kind (work-event-kind event)))
    ;; A recorded external effect is an outcome, not a verb of the state
    ;; grammar: it advances the revision and changes nothing else.
    (when (eq kind :external)
      (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
      (return-from apply-event state))
    ;; THE SIX CONFIG/ACTIVE VERBS' events (nova-tools#1695): they name no
    ;; containment node and move no work revision -- their :rev is the state's
    ;; own revision at submit -- but they DO rebuild the CONFIG and ACTIVE
    ;; halves a restart has to find again: the fleet members, the model routes
    ;; and the one allocator per machine with its allocations and observations.
    (when (member kind '(:machine :route :allocation :probe))
      (%apply-config-event state event)
      (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
      (return-from apply-event state))
    ;; The six new verbs draft 26 added write the `friends`, `models` and
    ;; `undo`/`redo` indexes, which are CONFIG and not work: their events name
    ;; no containment node, so they advance the revision and move no node, no
    ;; count, no roadmap and no required set (SPEC-WORK.md:1054-1058, replay
    ;; new-verbs-have-a-kind-and-a-field-order).
    (when (member kind '(:undo :redo :friend :model :observe :config
                         ;; A receipt and its seal are CONFIG-shaped too: they
                         ;; name no containment node (SPEC-WORK.md:3857-3862).
                         :receipt :receipt-seal))
      (setf (wstate-revision state) (max (wstate-revision state) (work-event-rev event)))
      (return-from apply-event state)))
  (let* ((id (work-event-node event))
         (node (%node-quiet state id)))
    (unless node
      (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (ecase (work-event-kind event)
      (:structure
       ;; SPEC-WORK.md:2362 -- a structure verb appends one structure event; its
       ;; `:verb` names the verb (`dep`) and v1's body fixes the field order at
       ;; :987. `dep` edits the `:deps` reference edge, so the forward edge, the
       ;; reverse edge and the append-only structure log are all rebuilt here
       ;; and a canonical replay reconstructs the same edge the live path did
       ;; (nova-tools#1673, #785).
       (let* ((fields (work-event-fields event))
              (verb (getf fields :verb))
              (add (getf fields :add +absent+))
              (remove (getf fields :remove +absent+))
              (reason (getf fields :reason +absent+)))
         (unless (eq verb :dep)
           (error 'unsupported-input
                  :what (format nil "unsupported: structure verb ~A is not in slice 1"
                                (if verb (string-downcase (princ-to-string verb)) "-"))))
         (when (and (stringp add) (plusp (length add)))
           (unless (member add (wnode-deps node) :test #'equal)
             (setf (wnode-deps node) (append (wnode-deps node) (list add)))
             (let ((target (%node-quiet state add)))
               (when target
                 (setf (wnode-dependents target)
                       (append (wnode-dependents target) (list id)))))))
         (when (and (stringp remove) (plusp (length remove)))
           (setf (wnode-deps node) (remove remove (wnode-deps node) :test #'equal))
           (let ((target (%node-quiet state remove)))
             (when target
               (setf (wnode-dependents target)
                     (remove id (wnode-dependents target) :test #'equal)))))
         ;; Removing the need that raised a `needs-broken` clears it again when
         ;; every remaining need is terminal. Adding one never raises it: a
         ;; fresh unmet need is simply not ready (`ready-p`), not broken.
         (when (and (eq verb :dep) (wnode-needs-broken node))
           (setf (wnode-needs-broken node)
                 (not (every (lambda (dep) (%need-terminal-p state dep))
                             (wnode-deps node)))))
         (push (list :op :structure :kind :structure :verb verb
                     :node id :add add :remove remove
                     :by (work-event-by event) :reason reason
                     :request (work-event-request event)
                     :stamp (work-event-stamp event)
                     :rev (work-event-rev event))
               (wnode-meta-log node))))
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
      ;; `take` and `release` on a node (nova-tools #1612 lane). Applied HERE,
      ;; inside the single writer, in the same total order as every other
      ;; event -- which is what makes a lease survive `replay-journal` and what
      ;; a raw `(setf (wnode-holder n) by)` on the caller's thread could not do.
      ;; The log row carries the CHANGE as its kind, so it reads `:take` and
      ;; `:release` exactly as the settle path already writes `:release`.
      (:lease
       (let ((change (getf (work-event-fields event) :change))
             (holder (getf (work-event-fields event) :holder)))
         (setf (wnode-holder node) (when (eq change :take) holder))
         (push (list :kind change :node id
                     :by (work-event-by event)
                     :holder holder
                     :stamp (work-event-stamp event)
                     :rev (work-event-rev event))
               (wstate-lease-log state))))
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


;;; ------------------------------------------------------------------
;;; folded from replays-8605.lisp (nova-tools #1102)
;;; ------------------------------------------------------------------

;;;; replays-8605.lisp --- leases, the working view and the retained roadmap.
;;;;
;;;; SPEC-WORK.md:1674-1689 -- "Settling ends the claim, because the work has
;;;; ended. The settle envelope carries a `:release` for a live lease on the
;;;; node ... a settled item reads `holder=unowned` in every answer while its
;;;; lease log stays whole for `handoffs`". And "`(working O)` is the items of O
;;;; that hold a live lease ... `take` and `release` are the only things that
;;;; change W ... `|W| <= |O|`".
;;;;
;;;; SPEC-WORK.md:1648-1664 -- "a roadmap is a durable named view ... Opening a
;;;; named roadmap is therefore an explicit scoped query, answered from that
;;;; retained record plus bounded indexed reads of its members' closed rows --
;;;; never a load of C, and never narrowed by the default `[now - 24h, now)`
;;;; window". The record is the node's own children; it survives the settle
;;;; cascade because the containment forest spans both branches.

(in-package #:nova-work)

;;; ---- leases --------------------------------------------------------------

(defun node-holder (state id)
  "The live lease's holder, or NIL when the item reads `holder=unowned`."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (wnode-holder n)))

(defun state-lease-log (state)
  "The lease log, oldest first, so a `handoffs --since` read sees the whole
transition log (SPEC-WORK.md:5055)."
  (reverse (wstate-lease-log state)))

;;; `take-lease` and `release-lease` MOVED to src/take-verb.lisp (nova-tools
;;; #1612 lane). They lived here and did `(setf (wnode-holder n) by)` on the
;;; live kernel state, on the CALLER's thread, with no event, no journal record
;;; and no request id -- exactly the mutation outside the command loop that
;;; SPEC-WORK.md:2603-2616 rule 6 calls a defect. A lease so taken was invisible
;;; to the journal's total order and did not survive `replay-journal`. They are
;;; `:lease` events on the single writer now; the callers' contract is unchanged.

(defun working-count (kernel)
  "|W|: the O items that hold a live lease. W is a view of O and never a third
branch, so this can never exceed `state-open-count`."
  (let ((state (kernel-state kernel))
        (n 0))
    (dolist (id (wstate-order state) n)
      (let ((node (%node-quiet state id)))
        (when (and (eq :o (wnode-branch node)) (wnode-holder node))
          (incf n))))))

(defun node-disposition (state id)
  "Read-time disposition: settled is `:done`, a leased open item is `:working`,
and an open item with no live lease is `:pending` (SPEC-WORK.md:5082)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (cond ((eq :c (wnode-branch n)) :done)
          ((wnode-holder n) :working)
          (t :pending))))

;;; ---- the retained roadmap view -------------------------------------------

(defun roadmap-members (state id)
  "A roadmap's rows: its direct members, whichever branch it and they are in.
The view record stays in the live snapshot whatever branch the roadmap is in
and however old its work is (SPEC-WORK.md:1648-1664)."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (unless (eq :roadmap (wnode-type n))
      (error 'unsupported-input :what (format nil "~A is not a roadmap" id)))
    (copy-list (wnode-children n))))

(defun %subtree-ids (state id)
  "The retained containment subtree rooted at ID, by the forest edges that
survive the settle (settling detaches nothing). Bounded by the rows' own work,
never by O (SPEC-WORK.md:1653-1656)."
  (let ((out '()))
    (labels ((walk (cur)
               (push cur out)
               (dolist (child (wnode-children (%node-quiet state cur)))
                 (walk child))))
      (walk id))
    out))

(defun roadmap-open (kernel id &key window)
  "Open a named roadmap: its retained row set plus the closed rows of its
members and the work beneath them. The answer is never narrowed by WINDOW and
never walks O, because the default `[now - 24h, now)` window bounds a
closed-activity listing and not a named view (SPEC-WORK.md:1648-1664)."
  (declare (ignore window))
  (let* ((state (kernel-state kernel))
         (members (roadmap-members state id))
         (reachable (loop for member in members
                          append (%subtree-ids state member)))
         (rows (remove-if-not (lambda (r) (member (getf r :node) reachable :test #'equal))
                              (state-closed-rows state))))
    (list :id id :members members :rows rows)))
