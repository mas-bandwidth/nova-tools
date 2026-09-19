;;;; dep-verb.lisp --- `dep --add` and `dep --remove` (nova-tools #785).
;;;;
;;;; Rule 6 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:4993-5031: "Moving an edge is a recorded act, and every
;;;; way past a need is a recorded act."
;;;;
;;;; The edge itself is `:deps`, the reference edge of *Containment and
;;;; reference* (SPEC-WORK.md:850-886), which this kernel could only seed until
;;;; now: `make-seed-state` read `:deps` from the seed form and nothing wrote one
;;;; afterwards. This is the verb.
;;;;
;;;; `dep` is NOT an admission verb: rule 3's list does not name it, and rule 6
;;;; says of the scope edits beside it that "none is gated, because gating scope
;;;; edits for the sake of some dependent would stop a team managing its own
;;;; scope". So there is no needs precondition here. `dep --remove` IS the
;;;; override of a direct need (:5012) -- the way to admit a node past a need is
;;;; to say on the record, with a name and a reason, that it does not need it --
;;;; and there is no other: no `--force`, and *Presence*'s `--anyway` answers a
;;;; presence reading and buys nothing past a need.

(in-package #:nova-work)

(defun node-structure-log (state id)
  "The node's append-only structure history, newest first. `dep` writes here,
as `roadmap-create` and `node move` do; it is not a work transition, so it is
not the replay path of seed and history."
  (let ((n (%node state id)))
    (unless n (error 'unsupported-input :what (format nil "rule 2: no such node ~A" id)))
    (remove-if-not (lambda (e) (eq :structure (getf e :kind)))
                   (copy-list (wnode-meta-log n)))))

(defun %dep-closes-a-cycle-p (state node need)
  "True when an edge NODE -> NEED would close a `:deps` cycle: NODE is already
reachable from NEED. Rule 3, \"a deadlock nobody can finish\"; a node that needs
itself is a cycle of length one, as *The coordination tree is one edge* says of
`:coordinator` (SPEC-WORK.md:4998). Bounded by the node count."
  (let ((seen (make-hash-table :test #'equal)))
    (labels ((reach (cur)
               (cond ((equal cur node) t)
                     ((gethash cur seen) nil)
                     (t (setf (gethash cur seen) t)
                        (let ((n (%node-quiet state cur)))
                          (and n (some #'reach (wnode-deps n))))))))
      (reach need))))

(defun %dep-line (kernel node change need rev &key (changed 1) view)
  "The `DEP OK` line of SPEC-WORK.md:6031, without its trailing `emitted=`,
which is the CLI's count of what it printed and there is no CLI in this kernel
-- the same omission every other OK line here makes.

`met=` is the named need's; `unmet=` and `needs-broken=` are the node's, read
AFTER the change (:4996)."
  (let ((state (kernel-state kernel)))
    (multiple-value-bind (met) (need-met-p state need :view view :dependent node)
      (multiple-value-bind (unmet) (node-needs-status state node :view view)
        (format nil "DEP OK id=~D request=~A node=~A rev=~D pushed=- change=~(~A~) need=~A met=~A unmet=~D needs-broken=~A changed=~D"
                rev (%dep-request kernel) node rev
                change need
                (if met "true" "false")
                unmet
                (if (node-needs-broken state node :view view) "true" "false")
                changed)))))

(defvar *dep-request* nil
  "The request id of the `dep` call in flight, so the line can name it without
threading it through every helper.")

(defun %dep-request (kernel) (declare (ignore kernel)) (or *dep-request* "-"))

(defun %dep-text-p (value) (and (stringp value) (plusp (length (string-trim " " value)))))

(defun dep-edit (kernel &key node change need as reason request stamp view)
  "`dep --add <id>` / `dep --remove <id>`: one recorded edit of one reference
edge. Answers (values OK-P LINE EXIT-CODE).

Refusals, in order. **Exit 2**, an invocation that cannot be read: a missing or
whitespace-only `--node`, `--need`, `--as`, `--reason` or `--request`, or a
`--change` outside `add` and `remove`. **Exit 1**, `DEP FAIL node=<id>: rule
<n>: <reason>`, nothing written: a node this session does not hold; on `--add`,
a need that names nothing in O or C (`rule 2: dangling`, and an id that lives in
another session's tree is that case and no other, since `:deps` holds ids of
this O), the node's own id or an id that would close a cycle (`rule 3`); on
`--remove`, an edge the node does not hold.

An edge added under work is admitted and flagged (:5008): onto a node that is
engaged or in C it is written anyway, because the dependency is true whether or
not it is convenient, and the line prints `needs-broken=true`. Nothing is
stopped and nothing is reopened, by rule 5.

`--as` is caller text and the tool authenticates nobody, as *The verbs* says of
every verb: what the tool cannot do is stop a named person removing an edge."
  (let ((state (kernel-state kernel)))
    (flet ((refuse2 (what)
             (return-from dep-edit
               (values nil (format nil "DEP FAIL node=~A: ~A; run: nova-work help"
                                   (if (%dep-text-p node) node "-") what)
                       2)))
           (refuse1 (what)
             (return-from dep-edit
               (values nil (format nil "DEP FAIL node=~A: ~A" node what) 1))))
      ;; exit 2: the invocation
      (unless (member change '(:add :remove))
        (refuse2 "--change is add or remove"))
      (unless (%dep-text-p node) (refuse2 "refusing to guess: --node"))
      (unless (%dep-text-p need) (refuse2 "refusing to guess: --need"))
      (unless (%dep-text-p as) (refuse2 "refusing to guess: --as"))
      (unless (%dep-text-p reason) (refuse2 "refusing to guess: --reason"))
      (unless (%dep-text-p request) (refuse2 "refusing to guess: --request"))
      ;; exit 1: the node
      (let ((n (%node-quiet state node)))
        (unless n (refuse1 (format nil "rule 2: no such node ~A" node)))
        (ecase change
          (:add
           (when (equal node need)
             (refuse1 (format nil "rule 3: ~A needs itself" node)))
           ;; rule 2 resolves a reference against O's nodes or C's closed index;
           ;; a removed node's row stays there, so a removed id is NOT dangling
           ;; and its edge is admitted, reading need-closed-unaccepted for good
           ;; (SPEC-WORK.md:4831).
           (unless (%node-quiet state need) (refuse1 "rule 2: dangling"))
           (when (member need (wnode-deps n) :test #'equal)
             (return-from dep-edit
               (values t (format nil "DEP NOTE node=~A need=~A: the edge is already there"
                                 node need)
                       0)))
           (when (%dep-closes-a-cycle-p state node need)
             (refuse1 (format nil "rule 3: :deps edges would contain a cycle through ~A" need))))
          (:remove
           (unless (member need (wnode-deps n) :test #'equal)
             (refuse1 (format nil "no such edge ~A -> ~A" node need)))))
        ;; apply
        (let ((rev (kernel-next-rev kernel))
              (target (%node-quiet state need)))
          (ecase change
            (:add
             (setf (wnode-deps n) (append (wnode-deps n) (list need)))
             (when target
               (setf (wnode-dependents target)
                     (append (wnode-dependents target) (list node)))))
            (:remove
             (setf (wnode-deps n) (remove need (wnode-deps n) :test #'equal))
             (when target
               (setf (wnode-dependents target)
                     (remove node (wnode-dependents target) :test #'equal)))))
          ;; the `:structure` event, with its author and its required reason
          (push (list :op :structure :kind :structure :verb :dep
                      :node node :change change :need need
                      :by as :reason reason :request request
                      :stamp (or stamp "-") :rev rev)
                (wnode-meta-log n))
          (setf (wstate-revision state) (max (wstate-revision state) rev))
          (setf (kernel-next-rev kernel) (1+ rev))
          (let ((*dep-request* request))
            (values t (%dep-line kernel node change need rev :view view) 0)))))))
