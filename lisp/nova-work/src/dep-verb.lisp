;;;; dep-verb.lisp --- `dep --add` and `dep --remove` (nova-tools#1673, #785).
;;;;
;;;; v1's own body promises the verb in four places outside the SPEC-AHEAD
;;;; section: the `:structure` event's per-verb field order (SPEC-WORK.md:987,
;;;; `dep` -- `:verb`, `:add`, `:remove`, `:reason`), the list of structure
;;;; verbs (:2362), the scope-revision rule (:1506) and the undo table (:2868).
;;;; The wire schema agrees (docs/schemas/nova-work-wire-v1.json, `"verb":
;;;; "dep"`, `"event_kind": ":structure"`, `"mutating": true`). The schema was
;;;; right and the kernel had no verb, so a `:deps` edge could only be seeded
;;;; and never edited.
;;;;
;;;; `dep` is NOT an admission verb: rule 3's list does not name it, and the
;;;; scope edits beside it are ungated, "because gating scope edits for the sake
;;;; of some dependent would stop a team managing its own scope". There is no
;;;; needs precondition here. `dep --remove` IS the recorded way past a direct
;;;; need: the way to admit a node past a need is to say on the record, with a
;;;; name and a reason, that it does not need it.

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
itself is a cycle of length one. Bounded by the node count."
  (let ((seen (make-hash-table :test #'equal)))
    (labels ((reach (cur)
               (cond ((equal cur node) t)
                     ((gethash cur seen) nil)
                     (t (setf (gethash cur seen) t)
                        (let ((n (%node-quiet state cur)))
                          (and n (some #'reach (wnode-deps n))))))))
      (reach need))))

(defun %dep-text-p (value)
  (and (stringp value) (plusp (length (string-trim " " value)))))

(defun %dep-request-refusal (request)
  "An invocation `dep` cannot read, or NIL. Exit 2 is the code. A required
`--reason`, a required author and a required request id are asked HERE, before
any mutation, so a duplicate add cannot bypass them."
  (let ((node (getf request :node))
        (add (getf request :add))
        (remove (getf request :remove))
        (by (getf request :by))
        (reason (getf request :reason))
        (rid (getf request :request)))
    (cond
      ((not (%dep-text-p node)) "refusing to guess: --node")
      ((and (not (%dep-text-p add)) (not (%dep-text-p remove)))
       "one of --add or --remove is required")
      ((and (%dep-text-p add) (%dep-text-p remove))
       "--add and --remove are mutually exclusive")
      ((not (%dep-text-p by)) "refusing to guess: --as")
      ((not (%dep-text-p reason)) "refusing to guess: --reason")
      ((not (%dep-text-p rid)) "refusing to guess: --request")
      (t nil))))

(defun %dep-line (rid node change need rev changed)
  (format nil "DEP OK id=~D request=~A node=~A rev=~D pushed=- change=~(~A~) need=~A changed=~D"
          rev rid node rev change need changed))

(defun %dep-submit (kernel request)
  "`dep --add` / `dep --remove`, run on the kernel's one command thread.

It is a `:structure` EVENT through the same writer path every other mutation
takes -- `%oneshot-submit` -> `%install-envelope`: dedup asked of the journal
BEFORE any mutable precondition, the record written before the apply, the
candidate installed all-or-none, and the event in the history that
`reconstruct-state` replays. Scope edits stay UNGATED (SPEC-WORK.md:5018):
durability does not make `dep` an admission verb."
  (let ((refusal (%dep-request-refusal request)))
    (when refusal
      (return-from %dep-submit
        (values nil (format nil "DEP FAIL node=~A: ~A; run: nova-work help"
                            (or (getf request :node) "-") refusal)
                2 nil))))
  (let* ((state (kernel-state kernel))
         (node (getf request :node))
         (add (and (%dep-text-p (getf request :add)) (getf request :add)))
         (remove (and (%dep-text-p (getf request :remove)) (getf request :remove)))
         (change (if add :add :remove))
         (need (or add remove))
         (rid (getf request :request))
         (by (getf request :by))
         (reason (getf request :reason))
         (n (%node-quiet state node)))
    (flet ((refuse1 (what)
             (return-from %dep-submit
               (values nil (format nil "DEP FAIL node=~A: ~A" node what) 1 nil))))
      (let* ((event (make-work-event
                     :kind :structure :node node :by by
                     :fields (list :verb :dep
                                   :add (or add +absent+)
                                   :remove (or remove +absent+)
                                   :reason reason)
                     :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
                     :clock (or (getf request :clock) :tool)
                     :request rid
                     :generation-owner (or (getf request :generation-owner) by)
                     :rev (kernel-next-rev kernel)
                     :session-written-p nil))
             (digest (payload-digest (list event))))
        ;; The replay answer comes BEFORE every mutable-state precondition: an
        ;; identical recorded payload answers its original receipt whatever the
        ;; set has done since, and the same id with a changed payload is
        ;; refused rather than applied at the next revision.
        (multiple-value-bind (verdict recorded) (%dedup-verdict kernel rid digest)
          (cond
            ((eq verdict :unavailable)
             (return-from %dep-submit
               (values nil (format nil "DEP FAIL node=~A page=~A: dedup unavailable"
                                   node recorded)
                       1 nil)))
            ((eq verdict :replay)
             (return-from %dep-submit
               (values t recorded 0 (list :request rid :digest digest :events '() :replayed t))))
            ((eq verdict :conflict)
             (return-from %dep-submit
               (values nil (%dedup-refusal "DEP" rid verdict nil) 1 nil)))))
        ;; and only now the mutable state
        (unless n (refuse1 (format nil "rule 2: no such node ~A" node)))
        (ecase change
          (:add
           (when (equal node need)
             (refuse1 (format nil "rule 3: ~A needs itself" node)))
           ;; A removed node's row stays in O, so a removed id is not dangling.
           (unless (%node-quiet state need) (refuse1 "rule 2: dangling"))
           (when (%dep-closes-a-cycle-p state node need)
             (refuse1 (format nil "rule 3: :deps edges would contain a cycle through ~A" need)))
           ;; A FRESH request naming an edge that is already there is a visible,
           ;; successful NO-EFFECT: no event, changed=0. It is reached only after
           ;; request-id validation and the dedup answer above.
           (when (member need (wnode-deps n) :test #'equal)
             (return-from %dep-submit
               (values t (format nil "DEP NOTE node=~A request=~A change=add need=~A changed=0: the edge is already there"
                                 node rid need)
                       0 nil))))
          (:remove
           (unless (member need (wnode-deps n) :test #'equal)
             (refuse1 (format nil "no such edge ~A -> ~A" node need)))))
        ;; ONE DURABLE RECORD PER MUTATION, on the one record-then-apply path.
        (%oneshot-submit kernel rid digest (%dep-line rid node change need
                                                      (work-event-rev event) 1)
                         event "DEP"
                         (list :verb :dep :node node :change change :need need
                               :request request))))))

(defun dep-edit (kernel &key node add remove as reason request stamp)
  "`dep --add <id>` / `dep --remove <id>`: one recorded edit of one reference
edge, submitted through the kernel's one command thread. A convenience wrapper;
`%dep-submit` is the verb."
  (submit kernel (list :verb :dep :node node :add add :remove remove
                       :by as :reason reason :request request :stamp stamp
                       :clock :tool)))
