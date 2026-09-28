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

(defun %dep-line (state node change need rev &key (changed 1) view request)
  "The `DEP OK` line of SPEC-WORK.md:6056 (SPEC-AHEAD #785, rule 6 at :5004),
without its trailing `emitted=`, which is the CLI's count of what it printed
and there is no CLI in this kernel -- the same omission every other OK line
here makes. `met=` is the named need's; `unmet=` and `needs-broken=` are the
node's, read AFTER the change, so STATE is the candidate the event produces."
  (let ((met (need-met-p state need :view view :dependent node))
        (unmet (node-needs-status state node :view view)))
    (format nil "DEP OK id=~D request=~A node=~A rev=~D pushed=- change=~(~A~) need=~A met=~A unmet=~D needs-broken=~A changed=~D"
            rev (or request "-") node rev change need
            (if met "true" "false")
            unmet
            (if (node-needs-broken state node :view view) "true" "false")
            changed)))

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
         (changed 1)
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
           ;; A FRESH request naming an edge that is already there is THE
           ;; NO-EFFECT RECEIPT (SPEC-WORK.md:2982-2986): an accepted typed
           ;; `:structure` event with a real id, `changed=0`, the event revision
           ;; advanced and no edge, count or index moved (the apply is
           ;; idempotent on a present edge). It is reached only after
           ;; request-id validation and the dedup answer above, and it takes
           ;; the one record-then-apply path below like every other edit.
           (when (member need (wnode-deps n) :test #'equal)
             (setf changed 0)))
          (:remove
           (unless (member need (wnode-deps n) :test #'equal)
             (refuse1 (format nil "no such edge ~A -> ~A" node need)))))
        ;; ONE DURABLE RECORD PER MUTATION, on the one record-then-apply path.
        ;; The line is read AFTER the change, so it is computed from a PRIVATE
        ;; CANDIDATE -- `apply-envelope` never touches the live state -- and the
        ;; finished receipt goes into the one record (a second `journal-record`
        ;; has no pending envelope on a real file journal).
        (let* ((candidate (apply-envelope state (list :request rid :digest digest
                                                      :events (list event))))
               (line (%dep-line candidate node change need (work-event-rev event)
                                :changed changed :view (kernel-needs-view kernel)
                                :request rid)))
          (%oneshot-submit kernel rid digest line event "DEP"
                           (list :verb :dep :node node :change change :need need
                                 :request request)))))))

(defun dep-edit (kernel &key node add remove change need as reason request stamp view)
  "`dep --add <id>` / `dep --remove <id>`: one recorded edit of one reference
edge, submitted through the kernel's one command thread. A convenience wrapper;
`%dep-submit` is the verb. CHANGE (:add or :remove) with NEED is the same call
spelled the way rule 6's line spells it. VIEW, when given, is installed as the
kernel's for this call, because the counts on the line are read after the
change and a caller that built a view should see its own."
  (when need
    (ecase change
      (:add (setf add need))
      (:remove (setf remove need))))
  (let ((previous (kernel-needs-view kernel)))
    (when view (setf (kernel-needs-view kernel) view))
    (unwind-protect
         (submit kernel (list :verb :dep :node node :add add :remove remove
                              :by as :reason reason :request request :stamp stamp
                              :clock :tool))
      (when view (setf (kernel-needs-view kernel) previous)))))
