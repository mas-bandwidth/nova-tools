;;;; s4-ready.lisp --- S4 "the pool is the tree" (#466): the ready-to-assign
;;;; view (SPEC-WORK.md:2104). Derived from dependencies, agreed scope,
;;;; acceptance readiness, ownership and availability -- every row that cannot
;;;; proceed prints its exact reason and who resolves it, because waiting is not
;;;; execution (replay ready-names-the-blocker-and-the-resolver).

(in-package #:nova-work)

(defun dependency-done-p (pool dep)
  "A dependency is satisfied when the node it names has settled into C. A
cancelled, superseded or still-open dep blocks what needs it."
  (let ((n (pool-get pool dep)))
    (and n (eq :c (pool-branch n)))))

(defun s4-ready-p (pool id)
  "The ready predicate. Returns (values s4-ready-p reason resolver):
  - a node in C is out of scope and not ready (settled);
  - a live lease blocks it, refusable only by its holder;
  - a :blocked state blocks it, resolvable by its blocked-by or its owner;
  - an unsatisfied dependency blocks it, resolvable by that dep's owner;
  - a required task with no acceptance can never be done (rule 15) and is not
    ready until its owner adds a criterion.
  An in-scope, unleased, unblocked node whose deps are settled and whose
  acceptance (where required) is present is ready."
  (let ((n (pool-get pool id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (when (eq :c (pool-branch n))
      (return-from s4-ready-p (values nil "settled" nil)))
    (when (pool-holder n)
      (return-from s4-ready-p (values nil "leased" (pool-holder n))))
    (when (eq :blocked (pool-state n))
      (return-from s4-ready-p (values nil "blocked"
                                   (or (pool-blocked-by n) (pool-responsible n)))))
    (dolist (dep (pool-deps n))
      (unless (dependency-done-p pool dep)
        (return-from s4-ready-p
          (values nil "dependency not done"
                  (or (pool-responsible (pool-get pool dep)) "-")))))
    (when (and (pool-required n)
               (member (pool-type n) '(:task))
               (null (pool-acceptance n)))
      (return-from s4-ready-p (values nil "no acceptance" (or (pool-responsible n) "-"))))
    (values t "ready" (or (pool-responsible n) "-"))))

(defun s4-ready-row (pool id)
  "One ready row as a plist, in the grammar's field order (SPEC-WORK.md:5105)."
  (multiple-value-bind (ready reason resolver) (s4-ready-p pool id)
    (multiple-value-bind (rank source context) (s4-effective-priority pool id)
      (let ((n (pool-get pool id)))
        (list :id id
              :kind (string-downcase (symbol-name (pool-type n)))
              :state (string-downcase (symbol-name (pool-state n)))
              :ready ready
              :reason (if ready "-" reason)
              :resolver (or resolver "-")
              :priority (rank-label rank)
              :priority-source (or source "default")
              :priority-context (context-label context)
              :responsible (or (pool-responsible n) "-")
              :holder (or (pool-holder n) "unowned"))))))

(defun print-ready-row (pool id)
  (let ((r (s4-ready-row pool id)))
    (format nil "QUERY ROW ~A kind=~A state=~A ready=~A reason=~A resolver=~A priority=~A priority-source=~A priority-context=~A responsible=~A holder=~A"
            (getf r :id) (getf r :kind) (getf r :state)
            (if (getf r :ready) "true" "false")
            (getf r :reason) (getf r :resolver)
            (getf r :priority) (getf r :priority-source) (getf r :priority-context)
            (getf r :responsible) (getf r :holder))))

(defun ancestor-ids (pool node)
  "The containment ancestor ids of NODE, root-most. Bounded by depth."
  (labels ((walk (id acc)
             (let ((n (pool-get pool id)))
               (if (null (pool-parent n))
                   acc
                   (walk (pool-parent n) (cons (pool-parent n) acc))))))
    (walk (pool-id node) '())))

(defun ready-scope (pool scope)
  "The in-scope ids under SCOPE (a containment id), or the whole pool when SCOPE
is NIL, in discovery order. Only startable work -- :task nodes -- is a candidate:
a container has no work of its own (SPEC-WORK.md:1599) and is never a row of the
ready view. A dependency outside the scope that blocks a node is still reported
on the row (SPEC-WORK.md:1829)."
  (let ((root (and scope (pool-get pool scope))))
    (remove-if-not
     (lambda (id)
       (let ((n (pool-get pool id)))
         (and n
              (member (pool-type n) '(:task))
              (eq :o (pool-branch n))
              (or (null root)
                  (eq id scope)
                  (member scope (ancestor-ids pool n))))))
     (pool-order pool))))

(defun ready-query (pool &key scope order)
  "The ready ask. Returns a list of s4-ready-row plists. Under :priority, only the
eligible (ready) rows are sorted -- explicit rank ascending, then defaults, then
bytewise id -- and the blocked rows follow in discovery order with their reason
and resolver fields intact; under :discovery the default, every row prints in
discovery order (SPEC-WORK.md:3152)."
  (let ((ids (ready-scope pool scope)))
    (if (eq order :priority)
        (let ((eligible '()) (blocked '()))
          (dolist (id ids)
            (multiple-value-bind (rdy) (s4-ready-p pool id)
              (if rdy (push (s4-ready-row pool id) eligible)
                      (push (s4-ready-row pool id) blocked))))
          (append
           (sort (nreverse eligible)
                 (lambda (a b)
                   (multiple-value-bind (ra) (s4-effective-priority pool (getf a :id))
                     (multiple-value-bind (rb) (s4-effective-priority pool (getf b :id))
                       (let ((c (priority-rank-compare ra rb)))
                         (if (zerop c)
                             (string< (getf a :id) (getf b :id))
                             (minusp c)))))))
           (nreverse blocked)))
        (mapcar (lambda (id) (s4-ready-row pool id)) ids))))
