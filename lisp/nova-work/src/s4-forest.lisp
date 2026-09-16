;;;; s4-forest.lisp --- S4 "the pool is the tree" (#466): the counted-containment
;;;; forest and the reference graph, as records and pure functions.
;;;;
;;;; This file owns the slice's own record shapes. It touches no kernel.lisp,
;;;; session.lisp or state.lisp: every function here is pure over a `pool` value
;;;; and is what a later wiring card lifts into the command thread.
;;;;
;;;; docs/SPEC-WORK.md:830 (The data) and :951 (the containment/reference
;;;; difference); :2049 (Queries) and :2107 (the worked acceptance); :4852
;;;; (validator rules 1-4 and 18).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The nodes: containment edges (:parent/:children) are one forest, and the
;;; :deps edges are a reference graph. The two are different records and a node
;;; is counted once, under its containment parent (SPEC-WORK.md:951).
;;; ------------------------------------------------------------------

(defstruct (pool-node (:conc-name pool-))
  id          ; stable string id, never display text
  type        ; :work-set | :epic | :feature | :task | :roadmap
  parent      ; containment parent id, or nil for a repository work set
  children    ; contained ids, in discovery order
  deps        ; reference ids -- needed, not owned, not counted here
  category    ; free label, or nil
  repo        ; "<owner>/<name>", or nil
  required    ; T (default) or NIL
  pri-self    ; integer, or :absent
  pri-subtree ; integer, or :absent
  state       ; the derived state (see SPEC-WORK.md:994)
  branch      ; :o or :c
  version     ; the release a task ships, or nil
  responsible ; durable accountability, or nil
  holder      ; live lease holder, or nil (unowned)
  blocked-by  ; a :blocked transition's reference, or nil
  landed      ; the :against sha of the qualifying :merged evidence, or nil
  settled     ; settle stamp, or nil
  disposed    ; disposition for a closed item: :done|:cancelled|:superseded|:removed
  evidence    ; evidence event count
  verified    ; verified evidence count
  acceptance) ; acceptance criteria (a required task must name at least one)

(defstruct (pool (:constructor %make-pool))
  nodes      ; id -> pool-node
  order      ; ids in discovery order
  dep-index) ; id -> list of dependent ids (reverse dependency, adjacency only)

;;; ------------------------------------------------------------------
;;; Building a pool from a seed of node plists. The seed is data, read as the
;;; restricted reader reads a work file: lists, keywords, strings, integers.
;;; ------------------------------------------------------------------

(defun %seed-field (spec key default)
  (multiple-value-bind (got foundp) (get-properties (cddr spec) (list key))
    (declare (ignore got))
    (if foundp (getf spec key) default)))

(defun make-pool (nodes)
  "Build a pool from a list of node spec plists. Validates rule 1 (duplicate
id), rule 2 (dangling parent and dangling dep), rule 3 (containment must be a
forest) and rule 4 (a node under two parents) before the pool exists, exactly as
make-seed-state does for the kernel's own seed (SPEC-WORK.md:4852)."
  (let ((table (make-hash-table :test #'equal))
        (order '()))
    (dolist (spec nodes)
      (let ((id (getf spec :id)))
        (when (gethash id table)
          (error 'unsupported-input :what (format nil "rule 1: duplicate id ~A" id)))
        (push id order)
        (setf (gethash id table)
              (make-pool-node
               :id id
               :type (getf spec :type)
               :parent (getf spec :parent)
               :children '()
               :deps (getf spec :deps '())
               :category (getf spec :category)
               :repo (getf spec :repo)
               :required (%seed-field spec :required t)
               :pri-self (getf spec :pri-self +absent+)
               :pri-subtree (getf spec :pri-subtree +absent+)
               :state (getf spec :state :unknown)
               :branch (getf spec :branch :o)
               :version (getf spec :version)
               :responsible (getf spec :responsible)
               :holder (getf spec :holder)
               :blocked-by (getf spec :blocked-by)
               :landed (getf spec :landed)
               :settled (getf spec :settled)
               :disposed (getf spec :disposed)
               :evidence (getf spec :evidence 0)
               :verified (getf spec :verified 0)
               :acceptance (getf spec :acceptance)))))
    (setf order (nreverse order))
    ;; Containment and reference edges, in discovery order.
    (dolist (id order)
      (let ((node (gethash id table)))
        (let ((parent (and (pool-parent node) (gethash (pool-parent node) table))))
          (when (and (pool-parent node) (null parent))
            (error 'unsupported-input
                   :what (format nil "rule 2: ~A names a parent that does not exist" id)))
          (when parent
            (setf (pool-children parent) (append (pool-children parent) (list id)))))
        ;; Rule 2 for references: a dep that names nothing is dangling.
        (dolist (dep (pool-deps node))
          (unless (gethash dep table)
            (error 'unsupported-input
                   :what (format nil "rule 2: ~A names a dep that does not exist: ~A" id dep))))))
    ;; Rule 3: containment is a forest -- no cycle through :parent, bounded by
    ;; the node count, exactly as the kernel's own forest check.
    (let ((limit (hash-table-count table)))
      (dolist (id order)
        (let ((cur id) (steps 0))
          (loop while cur
                do (when (> (incf steps) limit)
                     (error 'unsupported-input
                            :what (format nil "rule 3: :parent edges are not a forest; a cycle through ~A" id)))
                   (let ((node (gethash cur table)))
                     (unless node (return))
                     (setf cur (pool-parent node)))))))
    ;; The reverse-dependency index: adjacency, never a materialised transitive
    ;; set (SPEC-WORK.md:5040 and :5033).
    (%make-pool :nodes table :order order
                :dep-index (%build-dep-index table order))))

(defun %build-dep-index (table order)
  (let ((index (make-hash-table :test #'equal)))
    (dolist (id order)
      (dolist (dep (pool-deps (gethash id table)))
        (push id (gethash dep index))))
    (maphash (lambda (k v) (declare (ignore k)) (setf (gethash k index) (nreverse v))) index)
    index))

(defun pool-get (pool id)
  (gethash id (pool-nodes pool)))

;;; ------------------------------------------------------------------
;;; Reverse dependency: who names ID in its :deps.
;;; ------------------------------------------------------------------

(defun dependents (pool id)
  "The ids that reference ID. A bounded adjacency read, never a transitive set
(SPEC-WORK.md:5040)."
  (or (gethash id (pool-dep-index pool)) '()))

;;; ------------------------------------------------------------------
;;; released=<version|->  (SPEC-WORK.md:2075 and replay merged-is-not-distributed)
;;; ------------------------------------------------------------------

(defun released-of (pool id)
  "The :version of the settled release task that names ID in its :deps, found
through the reverse-dependency index; NIL while every such task is still open.
Where two settled release tasks name one item, the one with the earlier settle
stamp wins (SPEC-WORK.md:2075). A merged fix is not a distributed one."
  (let ((best-version nil)
        (best-stamp nil))
    (dolist (dep (dependents pool id))
      (let ((n (pool-get pool dep)))
        (when (and n (pool-version n) (eq :c (pool-branch n)))
          (when (or (null best-stamp)
                    (string< (or (pool-settled n) "") (or best-stamp "")))
            (setf best-version (pool-version n)
                  best-stamp (pool-settled n))))))
    best-version))

;;; ------------------------------------------------------------------
;;; The partition: open= and closed= count each id once (SPEC-WORK.md:1780,
;; replay cow-root-partition).
;;; ------------------------------------------------------------------

(defun partition-counts (pool &optional scope-ids)
  "Return (values open closed total) over the given ids (the whole pool when
omitted). Each id is counted once, in O or in C, never both."
  (let ((open 0) (closed 0) (total 0))
    (dolist (id (or scope-ids (pool-order pool)))
      (let ((n (pool-get pool id)))
        (when n
          (incf total)
          (if (eq :c (pool-branch n)) (incf closed) (incf open)))))
    (values open closed total)))

(defun closed-in (pool from to &optional scope-ids)
  "The part of closed= whose settle stamp lies in [from, to)."
  (let ((n 0))
    (dolist (id (or scope-ids (pool-order pool)))
      (let ((n0 (pool-get pool id)))
        (when (and n0 (eq :c (pool-branch n0))
                   (pool-settled n0)
                   (string<= from (pool-settled n0))
                   (string< (pool-settled n0) to))
          (incf n))))
    n))

;;; ------------------------------------------------------------------
;;; rule 18: an id in both branches is a finding, never tidied at read time
;;; (SPEC-WORK.md:1780, replay cow-root-partition).
;;; ------------------------------------------------------------------

(defun rule-18-finding (pool id &optional move)
  "Check a move of ID (one of :settle or :revive) is a partition move and not a
write that would put one id in both branches. Returns NIL when admissible, or a
reason string when it is rule 18."
  (let ((n (pool-get pool id)))
    (cond
      ((null n) "rule 18: no such id")
      ((and (eq move :settle) (eq :c (pool-branch n)))
       (format nil "rule 18: ~A is already in C" id))
      ((and (eq move :revive) (eq :o (pool-branch n)))
       (format nil "rule 18: ~A is already in O" id))
      (t nil))))

;;; ------------------------------------------------------------------
;;; The disposition row (SPEC-WORK.md:2063 and the worked acceptance at :2150).
;;; ------------------------------------------------------------------

(defun %disposition (n)
  (if (eq :c (pool-branch n))
      (or (pool-disposed n) :removed)
      (cond
        ((pool-holder n) :working)
        ((eq :deferred (pool-state n)) :deferred)
        (t :pending))))

(defun s4-disposition-row (pool id)
  "One disposition row as a plist of the grammar's fields in order
(SPEC-WORK.md:2063)."
  (let* ((n (pool-get pool id))
         (kind (and n (string-downcase (symbol-name (pool-type n))))))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (list :id id
          :branch (if (eq :c (pool-branch n)) :closed :open)
          :disposition (%disposition n)
          :repo (or (pool-repo n) "-")
          :kind kind
          :state (string-downcase (symbol-name (pool-state n)))
          :landed (or (pool-landed n) "-")
          :released (or (released-of pool id) "-")
          :holder (or (pool-holder n) "unowned")
          :settled (or (pool-settled n) "-")
          :evidence (pool-evidence n)
          :verified (pool-verified n)
          :responsible (or (pool-responsible n) "-"))))

(defun print-disposition-row (pool id)
  (let ((r (s4-disposition-row pool id)))
    (format nil "QUERY ROW ~A branch=~A disposition=~A repo=~A kind=~A state=~A landed=~A released=~A holder=~A settled=~A evidence=~D verified=~D responsible=~A"
            (getf r :id)
            (string-downcase (symbol-name (getf r :branch)))
            (string-downcase (symbol-name (getf r :disposition)))
            (getf r :repo) (getf r :kind) (getf r :state)
            (getf r :landed) (getf r :released) (getf r :holder)
            (getf r :settled) (getf r :evidence) (getf r :verified)
            (getf r :responsible))))

;;; ------------------------------------------------------------------
;;; `query --ask under` (SPEC-WORK.md:2100). A compact listing by repo and
;;; category; under --branch root it spans C and O in one answer, one row per id.
;;; ------------------------------------------------------------------

(defun under-scope (pool &key repo category)
  "The ids of the pool matching REPO and/or CATEGORY, in discovery order."
  (remove-if-not
   (lambda (id)
     (let ((n (pool-get pool id)))
       (and n
            (or (null repo) (string= (or (pool-repo n) "") repo))
            (or (null category) (string= (or (pool-category n) "") category)))))
   (pool-order pool)))

(defun under-query (pool &key repo category branch from to)
  "The under ask over the repo/category scope. Returns (values scope-line rows
open closed closed-in) where rows are the s4-disposition-row plists selected by
BRANCH (:open, :closed or :root), and the partition counts are the whole
scope's, folded over both branches as every scope count is (SPEC-WORK.md:1780)."
  (let ((ids (under-scope pool :repo repo :category category)))
    (multiple-value-bind (open closed total)
        (partition-counts pool ids)
      (declare (ignore total))
      (let ((rows-ids
              (remove-if-not
               (lambda (id)
                 (let ((b (pool-branch (pool-get pool id))))
                   (ecase branch
                     (:open (eq :o b))
                     (:closed (eq :c b))
                     (:root t))))
               ids)))
        (let ((cin (if (and from to) (closed-in pool from to ids) 0)))
          (values
           (format nil "QUERY OK ask=under scope=~D membership=category branch=~A unit=leaves open=~D closed=~D closed-in=~D rows=~D shown=~D"
                   (length ids)
                   (if (eq branch :open) "open" "root")
                   open closed cin (length ids) (length rows-ids))
           (mapcar (lambda (id) (s4-disposition-row pool id)) rows-ids)
           open closed cin))))))
