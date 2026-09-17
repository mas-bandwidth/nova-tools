;;;; replays-8664.lisp --- pure models for the three named acceptance replays
;;;; of docs/SPEC-WORK.md at origin/dev:
;;;;
;;;;   preparation-interrupted-before-launch   SPEC-WORK.md:6153-6159
;;;;   concurrent-slots-refuse-third-job       SPEC-WORK.md:6160-6164
;;;;   indexes-and-counters                    SPEC-WORK.md:6242
;;;;
;;;; The first two paragraphs name CLI lines on a live session; what they turn
;;;; on is one admission rule -- slots come from declared concurrency, never
;;;; from cores, and a slot returns only on a confirmed termination. That rule
;;;; is proven here purely. The third paragraph is the preservation suite's
;;;; counters-and-indexes row; the write-maintained |O|, friend index and
;;;; per-container counts are proven against an independent reconstruction.
;;;; The live journal, session and CLI wiring is out of this slice and is
;;;; listed in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; fleet allocation: slots come from declared concurrency, not cores
;;; ------------------------------------------------------------------

(defstruct (replay-allocation (:conc-name alloc-)
                              (:constructor %make-alloc))
  id slot node holder state)

(defstruct (replay-machine (:conc-name replay-machine-)
                           (:constructor %make-replay-machine))
  id cores concurrent allocations)

(defun make-replay-machine (id &key cores concurrent)
  (%make-replay-machine :id id :cores cores :concurrent concurrent :allocations '()))

(defun %live-allocations (machine)
  "An allocation holds its slot until a confirmed termination releases it; an
interrupted preparation and a suspect expiry are both still live."
  (remove-if (lambda (a) (eq :released (alloc-state a)))
             (replay-machine-allocations machine)))

(defun machine-live-slots (machine)
  (reduce #'+ (%live-allocations machine) :key #'alloc-slot :initial-value 0))

(defun find-allocation (machine allocation-id)
  (find allocation-id (replay-machine-allocations machine)
        :key #'alloc-id :test #'equal))

(defun take-slot (machine node &key (slots 1) holder id)
  "Take SLOTS of the machine's declared concurrency for NODE. Grant every
requested slot or none: a request that would pass :concurrent is refused with
the blocking holder and writes no allocation (SPEC-WORK.md:3659-3662,
6153-6164). Cores are a separate constraint and never the slot count."
  (if (> (+ (machine-live-slots machine) slots) (replay-machine-concurrent machine))
      (let* ((blocking (first (%live-allocations machine)))
             (named (or (and blocking (alloc-holder blocking)) holder)))
        (values nil
                (format nil "ALLOC FAIL machine=~A slots=~A holder=~A: capacity"
                        (replay-machine-id machine) slots named)
                machine))
      (let* ((allocation (%make-alloc
                          :id (or id (format nil "alloc-~A"
                                             (length (replay-machine-allocations machine))))
                          :slot slots :node node :holder holder :state :active)))
        (values t
                (format nil "ALLOC OK id=~A machine=~A allocation=~A slot=~A node=~A"
                        (alloc-id allocation) (replay-machine-id machine)
                        (alloc-id allocation) slots node)
                (%make-replay-machine :id (replay-machine-id machine)
                              :cores (replay-machine-cores machine)
                              :concurrent (replay-machine-concurrent machine)
                              :allocations (append (replay-machine-allocations machine)
                                                   (list allocation)))))))

(defun preparation-interrupted (machine allocation-id)
  "Preparation (clone/scp) was interrupted before the model launched: the
allocation keeps holding its slot (SPEC-WORK.md:6153-6156)."
  (let ((allocation (find-allocation machine allocation-id)))
    (when allocation (setf (alloc-state allocation) :interrupted))
    machine))

(defparameter *termination-evidence* '(:verified-stop :not-started :machine-fencing)
  "Capacity returns only on confirmed process termination: a verified stop
observation, a not-started bound to the launch authority, or machine-side
fencing (SPEC-WORK.md:3682-3687).")

(defun reconcile-allocation (machine allocation-id evidence)
  "Free ALLOCATION-ID's slot only on confirmed termination EVIDENCE; an expiry,
a silence or an elapsed estimate frees nothing (SPEC-WORK.md:6156-6159)."
  (let ((allocation (find-allocation machine allocation-id)))
    (cond ((null allocation)
           (values nil
                   (format nil "ALLOC FAIL machine=~A allocation=~A: stale token"
                           (replay-machine-id machine) allocation-id)
                   machine))
          ((not (member evidence *termination-evidence*))
           (values nil
                   (format nil "ALLOC FAIL machine=~A: not fenced" (replay-machine-id machine))
                   machine))
          (t
           (setf (alloc-state allocation) :released)
           (values t
                   (format nil "ALLOC RELEASE OK allocation=~A machine=~A slot=~A freed=true"
                           allocation-id (replay-machine-id machine) (alloc-slot allocation))
                   machine)))))

;;; ------------------------------------------------------------------
;;; indexes-and-counters                            SPEC-WORK.md:6242
;;; ------------------------------------------------------------------
;;; The write-maintained |O|, |C|, the friend (holder) index and the
;;; per-container counts. Each verb moves them on the write path along the
;;; item's containment ancestors only; the reconstruction walks everything and
;;; consults no maintained value, so the two agreeing is the acceptance.

(defstruct (rnode (:conc-name rnode-))
  id parent state holder links)

(defstruct (replay-index (:conc-name rindex-)
                         (:constructor %make-rindex))
  nodes order open-count closed-count holder-index container-index)

(defun make-replay-index (specs)
  (let ((table (make-hash-table :test #'equal)) (order '()))
    (dolist (spec specs)
      (push (getf spec :id) order)
      (setf (gethash (getf spec :id) table)
            (make-rnode :id (getf spec :id) :parent (getf spec :parent)
                        :state (getf spec :state :open)
                        :holder (getf spec :holder)
                        :links (getf spec :links))))
    (%seed-maintained
     (%make-rindex :nodes table :order (nreverse order)
                  :open-count 0 :closed-count 0
                  :holder-index '() :container-index '()))))

(defun %node-of (index id) (gethash id (rindex-nodes index)))

(defun %ancestors (index id)
  "ID and its parent chain, nearest first."
  (let ((out '()) (cur id))
    (loop while cur
          do (push cur out)
             (let ((n (%node-of index cur))) (setf cur (and n (rnode-parent n)))))
    out))

(defun %holder-cell (index holder)
  (assoc holder (rindex-holder-index index) :test #'equal))

(defun %seed-maintained (index)
  "Seed the maintained counters and indexes once, on the write path that builds
the set."
  (let ((open 0) (closed 0) (holders '()) (containers '()))
    (dolist (id (rindex-order index))
      (if (eq :open (rnode-state (%node-of index id)))
          (progn (incf open)
                 (let ((h (rnode-holder (%node-of index id))))
                   (when h
                     (let ((cell (assoc h holders :test #'equal)))
                       (if cell (push id (cdr cell))
                           (push (cons h (list id)) holders))))))
          (incf closed)))
    (dolist (id (rindex-order index))
      (when (eq :open (rnode-state (%node-of index id)))
        (dolist (anc (%ancestors index id))
          (let ((cell (assoc anc containers :test #'equal)))
            (if cell (incf (cdr cell)) (push (cons anc 1) containers))))))
    (setf (rindex-open-count index) open
          (rindex-closed-count index) closed
          (rindex-holder-index index) holders
          (rindex-container-index index) containers)
    index))

(defun %holder-remove (index id)
  (let* ((n (%node-of index id)) (h (and n (rnode-holder n))))
    (when h
      (let ((cell (%holder-cell index h)))
        (when cell (setf (cdr cell) (remove id (cdr cell) :test #'equal)))))))

(defun %holder-add (index id)
  (let* ((n (%node-of index id)) (h (and n (rnode-holder n))))
    (when h
      (let ((cell (%holder-cell index h)))
        (if cell
            (unless (member id (cdr cell) :test #'equal) (push id (cdr cell)))
            (push (cons h (list id)) (rindex-holder-index index)))))))

(defun %container-adjust (index ids delta)
  (dolist (id ids)
    (let ((cell (assoc id (rindex-container-index index) :test #'equal)))
      (if cell (incf (cdr cell) delta)
          (push (cons id delta) (rindex-container-index index))))))

(defun replay-index-open-p (index id)
  (let ((n (%node-of index id))) (and n (eq :open (rnode-state n)))))

(defun replay-index-closed-p (index id)
  (let ((n (%node-of index id))) (and n (eq :closed (rnode-state n)))))

(defun replay-index-ancestor-p (index id ancestor)
  "T when ANCESTOR is ID or lies on ID's parent chain."
  (and id (member ancestor (%ancestors index id) :test #'equal) t))

(defun replay-index-close (index id)
  (let ((n (%node-of index id)))
    (when (and n (eq :open (rnode-state n)))
      (setf (rnode-state n) :closed)
      (decf (rindex-open-count index))
      (incf (rindex-closed-count index))
      (%holder-remove index id)
      (%container-adjust index (%ancestors index id) -1))
    index))

(defun replay-index-reopen (index id)
  (let ((n (%node-of index id)))
    (when (and n (eq :closed (rnode-state n)))
      (setf (rnode-state n) :open)
      (incf (rindex-open-count index))
      (decf (rindex-closed-count index))
      (%holder-add index id)
      (%container-adjust index (%ancestors index id) 1))
    index))

(defun replay-index-reparent (index id new-parent)
  "Move ID under NEW-PARENT. |O| and the friend index are untouched; the
per-container counts move along the old and new ancestor chains and are never
double-counted (SPEC-WORK.md:6242)."
  (let ((n (%node-of index id)))
    (when (and n (%node-of index new-parent))
      (let ((openp (eq :open (rnode-state n))))
        (when openp (%container-adjust index (%ancestors index id) -1))
        (setf (rnode-parent n) new-parent)
        (when openp (%container-adjust index (%ancestors index id) 1))))
    index))

(defun replay-index-assign (index id holder)
  (let ((n (%node-of index id)))
    (when n
      (when (eq :open (rnode-state n)) (%holder-remove index id))
      (setf (rnode-holder n) holder)
      (when (eq :open (rnode-state n)) (%holder-add index id)))
    index))

(defun holder-open-ids (index holder)
  (copy-list (cdr (%holder-cell index holder))))

;;; The independent reconstruction: a full walk of the current item set that
;;; reads no maintained counter and no maintained index.

(defun %subtree-open-count (index id)
  (+ (if (eq :open (rnode-state (%node-of index id))) 1 0)
     (loop for child in (rindex-order index)
           when (equal (rnode-parent (%node-of index child)) id)
             sum (%subtree-open-count index child))))

(defun independent-reconstruction (index)
  (let ((open 0) (closed 0) (holders '()) (containers '()))
    (dolist (id (rindex-order index))
      (if (eq :open (rnode-state (%node-of index id)))
          (progn
            (incf open)
            (let ((h (rnode-holder (%node-of index id))))
              (when h
                (let ((cell (assoc h holders :test #'equal)))
                  (if cell (push id (cdr cell))
                      (push (cons h (list id)) holders))))))
          (incf closed)))
    (dolist (id (rindex-order index))
      (let ((count (%subtree-open-count index id)))
        (when (plusp count) (push (cons id count) containers))))
    (list :open open :closed closed :holder-index holders :container-index containers)))

(defun %normalize-holder (alist)
  (sort (remove-if (lambda (cell) (null (cdr cell)))
                   (mapcar (lambda (cell) (cons (car cell) (sort (copy-list (cdr cell)) #'string<)))
                           alist))
        #'string< :key #'car))

(defun %normalize-container (alist)
  (sort (remove-if (lambda (cell) (zerop (cdr cell))) (copy-list alist))
        #'string< :key #'car))

(defun index-mismatches (index)
  "Every way the maintained counters and indexes disagree with an independent
full reconstruction; () when they agree."
  (let ((r (independent-reconstruction index)) (out '()))
    (unless (= (rindex-open-count index) (getf r :open))
      (push (format nil "|O| maintained=~A reconstructed=~A"
                    (rindex-open-count index) (getf r :open)) out))
    (unless (= (rindex-closed-count index) (getf r :closed))
      (push (format nil "|C| maintained=~A reconstructed=~A"
                    (rindex-closed-count index) (getf r :closed)) out))
    (unless (equal (%normalize-holder (rindex-holder-index index))
                   (%normalize-holder (getf r :holder-index)))
      (push "friend index disagrees with reconstruction" out))
    (unless (equal (%normalize-container (rindex-container-index index))
                   (%normalize-container (getf r :container-index)))
      (push "container counters disagree with reconstruction" out))
    out))
