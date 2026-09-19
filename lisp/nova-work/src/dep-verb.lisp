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

(defun %dep-line-for (state node change need rev &key (changed 1) view request)
  "The `DEP OK` line of SPEC-WORK.md:6031, without its trailing `emitted=`,
which is the CLI's count of what it printed and there is no CLI in this kernel
-- the same omission every other OK line here makes.

`met=` is the named need's; `unmet=` and `needs-broken=` are the node's, read
AFTER the change (:4996)."
  (progn
    (multiple-value-bind (met) (need-met-p state need :view view :dependent node)
      (multiple-value-bind (unmet) (node-needs-status state node :view view)
        (format nil "DEP OK id=~D request=~A node=~A rev=~D pushed=- change=~(~A~) need=~A met=~A unmet=~D needs-broken=~A changed=~D"
                rev (or request "-") node rev
                change need
                (if met "true" "false")
                unmet
                (if (node-needs-broken state node :view view) "true" "false")
                changed)))))

(defun %dep-text-p (value) (and (stringp value) (plusp (length (string-trim " " value)))))

(defun %dep-request-refusal (request)
  "An invocation `dep` cannot read, or NIL. Exit 2 is the code. Request-id
validation happens HERE, before the no-effect case, so a duplicate add cannot
bypass it (Stella's read of 5d1f9dfa)."
  (let ((change (getf request :change)))
    (cond
      ((not (member change '(:add :remove))) "--change is add or remove")
      ((not (%dep-text-p (getf request :node))) "refusing to guess: --node")
      ((not (%dep-text-p (getf request :need))) "refusing to guess: --need")
      ((not (%dep-text-p (getf request :by))) "refusing to guess: --as")
      ((not (%dep-text-p (getf request :reason))) "refusing to guess: --reason")
      ((not (%dep-text-p (getf request :request))) "refusing to guess: --request")
      (t nil))))

(defun %dep-submit (kernel request)
  "`dep --add` / `dep --remove`, run on the kernel's one command thread.

STELLA'S [P1] ON 5d1f9dfa. The first cut mutated `wnode-deps`, the reverse edge
and `wnode-meta-log` in place and appended nothing to the history. `DEP OK
changed=1` came back while the history length stayed zero, so a canonical
reconstruction produced D with no dependencies and no structure log: a restart
erased the edge that had just been acknowledged. The same bypass also accepted
one request id twice with different payloads.

It is now a `:dep` EVENT through the same writer path every other mutation
takes -- `%oneshot-submit` -> `%install-envelope`: dedup asked of the journal,
the record written before the apply, the candidate installed all-or-none, and
the event in the history that `reconstruct-state` replays.

Scope edits stay UNGATED (SPEC-WORK.md:5018): durability does not make `dep` an
admission verb, and there is no needs precondition here."
  (let ((refusal (%dep-request-refusal request)))
    (when refusal
      (return-from %dep-submit
        (values nil (format nil "DEP FAIL node=~A: ~A; run: nova-work help"
                            (let ((n (getf request :node)))
                              (if (%dep-text-p n) n "-"))
                            refusal)
                2 nil))))
  (let* ((state (kernel-state kernel))
         (node (getf request :node))
         (need (getf request :need))
         (change (getf request :change))
         (rid (getf request :request))
         (by (getf request :by))
         (n (%node-quiet state node)))
    (flet ((refuse1 (what)
             (return-from %dep-submit
               (values nil (format nil "DEP FAIL node=~A: ~A" node what) 1 nil))))
      ;; The replay answer comes BEFORE every mutable-state precondition, as
      ;; Stella's [P2] on 1a11652d requires of every command on this path: an
      ;; identical recorded payload answers its original receipt whatever the
      ;; set has done since.
      (let* ((event (make-work-event
                     :kind :dep :node node :by by
                     :fields (list :change change :need need
                                   :reason (getf request :reason))
                     :stamp (or (getf request :stamp) "2026-09-14T12:00:00Z")
                     :clock (or (getf request :clock) :tool)
                     :request rid
                     :generation-owner (or (getf request :generation-owner) by)
                     :rev (kernel-next-rev kernel)))
             (digest (payload-digest (list event))))
        ;; The two-part dedup test, asked of the journal FIRST: a retransmitted
        ;; request answers its original receipt, and the same id with a changed
        ;; payload is refused rather than applied at the next revision.
        (multiple-value-bind (found recorded-digest recorded-line)
            (journal-lookup (kernel-journal kernel) rid)
          (cond
            ((eq found :unavailable)
             (return-from %dep-submit
               (values nil (format nil "DEP FAIL node=~A page=~A: dedup unavailable"
                                   node recorded-digest)
                       1 nil)))
            (found
             (return-from %dep-submit
               (if (string= digest recorded-digest)
                   (values t recorded-line 0
                           (list :request rid :digest digest :events (list) :replayed t))
                   (values nil (format nil "DEP FAIL node=~A request=~A: reused with a different payload"
                                       node rid)
                           1 nil))))))
        ;; and only now the mutable state
        (unless n (refuse1 (format nil "rule 2: no such node ~A" node)))
        (ecase change
          (:add
           (when (equal node need)
             (refuse1 (format nil "rule 3: ~A needs itself" node)))
           ;; rule 2 resolves against O's nodes or C's closed index; a removed
           ;; node's row stays there, so a removed id is NOT dangling.
           (unless (%node-quiet state need) (refuse1 "rule 2: dangling"))
           ;; A FRESH request naming an edge that is already there is a visible,
           ;; successful NO-EFFECT: no event, changed=0 (Stella's ruling on
           ;; SPEC-QUESTION 2). It is reached only after request-id validation
           ;; and the dedup answer above, so it never bypasses either.
           (when (member need (wnode-deps n) :test #'equal)
             (return-from %dep-submit
               (values t (format nil "DEP NOTE node=~A request=~A change=add need=~A met=~A unmet=~D needs-broken=~A changed=0: the edge is already there"
                                 node rid need
                                 (if (need-met-p state need :view (kernel-needs-view kernel)
                                                            :dependent node)
                                     "true" "false")
                                 (node-needs-status state node :view (kernel-needs-view kernel))
                                 (if (node-needs-broken state node
                                                        :view (kernel-needs-view kernel))
                                     "true" "false"))
                       0 nil)))
           (when (%dep-closes-a-cycle-p state node need)
             (refuse1 (format nil "rule 3: :deps edges would contain a cycle through ~A" need))))
          (:remove
           (unless (member need (wnode-deps n) :test #'equal)
             (refuse1 (format nil "no such edge ~A -> ~A" node need)))))
        ;; ONE DURABLE RECORD PER MUTATION.
        ;;
        ;; STELLA'S [P1] ON 1a558076: the first cut handed `%oneshot-submit` a
        ;; PLACEHOLDER "DEP OK". That recorded and applied; the detailed receipt
        ;; was then written with a SECOND `journal-record`, which has no pending
        ;; envelope left. Against a real `file-journal` an ordinary add returned
        ;; `MUTATION FAIL ... journal-record mismatch: expected pending envelope`
        ;; at exit 2 WITH THE EDGE ALREADY APPLIED, and the retry answered a
        ;; bare `DEP OK`. The `ordering-journal` fake permits the overwrite and
        ;; hid it.
        ;;
        ;; The line is read AFTER the change, so it is computed from a PRIVATE
        ;; CANDIDATE -- `apply-envelope` never touches the live state -- and the
        ;; finished receipt goes into the one record-then-apply.
        (let* ((candidate (apply-envelope state (list :request rid :digest digest
                                                      :events (list event))))
               (line (%dep-line-for candidate node change need (work-event-rev event)
                                    :view (kernel-needs-view kernel) :request rid)))
          (%oneshot-submit kernel rid digest line event "DEP"
                           (list :verb :dep :node node :need need :change change
                                 :request rid)))))))

(defun dep-edit (kernel &key node change need as reason request stamp view)
  "`dep --add <id>` / `dep --remove <id>`: one recorded edit of one reference
edge, submitted through the kernel's one command thread.

A convenience wrapper: `%dep-submit` is the verb. VIEW, when given, is installed
as the kernel's for this call, because the counts on the line are read after the
change and a caller that built a view should see its own."
  (let ((previous (kernel-needs-view kernel)))
    (when view (setf (kernel-needs-view kernel) view))
    (unwind-protect
         (submit kernel (list :verb :dep :node node :change change :need need
                              :by as :reason reason :request request
                              :stamp stamp :clock :tool))
      (when view (setf (kernel-needs-view kernel) previous)))))
