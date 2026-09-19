;;;; report-verb.lisp --- the hand report (nova-tools #854 item 7).
;;;;
;;;; Rules 7 and 8 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:5033-5112, with its replays at :6981 and :6989.
;;;;
;;;; Rule 7 draws the line this verb exists on: a hand act whose effect is TREE
;;;; STATE is done by that field's typed verb, and where no verb owns the field
;;;; it is a *missing verb* and is never reported, "because a report that set
;;;; tree state would be the generic set-field escape hatch". A hand act whose
;;;; effect lies OUTSIDE the tree -- a process started or killed, a service
;;;; restarted, a queue entry dropped, a merge made through a forge's own page
;;;; -- the tree cannot do and cannot undo, and what it can hold is a record
;;;; that it happened. That is `report`, one verb, and it changes no state.
;;;;
;;;; So: the typed verbs change state and the one report verb changes none.
;;;; `apply-event` skips `:report` with the six CONFIG kinds -- the revision
;;;; advances and no node, count, roadmap, required set, lease or W entry moves.

(in-package #:nova-work)

(defparameter *report-acts* '(:launched :stopped :other)
  "`:act` is the class of the act, three values and no others (SPEC-WORK.md:5060):
`:launched`, a worker started; `:stopped`, a worker or a process ended;
`:other`.")

(defparameter *report-subject-kinds*
  '(:node :machine :friend :route :offer :external)
  "`:subject` is what the hand acted on, a typed selector as
`:execution-control`'s `:scope` is (SPEC-WORK.md:5063). `(:external \"<text>\")`
is for a thing the tree has no identity for.")

(defun %report-text-p (value)
  "A written, non-whitespace field. The same refusal this document already makes
of whitespace-only evidence (SPEC-WORK.md:5098)."
  (and (stringp value)
       (plusp (length (string-trim '(#\Space #\Tab #\Newline #\Return) value)))))

(defun %report-rfc3339-p (stamp)
  "An RFC 3339 UTC stamp with a trailing Z. A stamp that is not one is exit 2."
  (and (stringp stamp)
       (= (length stamp) 20)
       (char= (char stamp 10) #\T)
       (char= (char stamp 19) #\Z)
       (ignore-errors (parse-rfc3339 stamp) t)
       t))

(defun parse-report-subject (text)
  "Split a `--subject` at its FIRST `:` (SPEC-WORK.md:5066) and answer
(values KIND VALUE), or NIL when it has no `:` or names a kind outside the six."
  (let ((colon (and (stringp text) (position #\: text))))
    (when colon
      (let* ((kind-text (subseq text 0 colon))
             (value (subseq text (1+ colon)))
             (kind (find kind-text *report-subject-kinds*
                         :key (lambda (k) (string-downcase (symbol-name k)))
                         :test #'string=)))
        (when kind (values kind value))))))

(defun report-subject-selector (kind value)
  "The printed selector, `<kind>:<text>`."
  (format nil "~(~A~):~A" kind value))

(defun %report-duration (seconds)
  "A duration in the spelling `parse-duration` reads back: `90s`, `5m`, `1h`."
  (cond ((null seconds) "-")
        ((and (plusp seconds) (zerop (mod seconds 3600))) (format nil "~Dh" (floor seconds 3600)))
        ((and (plusp seconds) (zerop (mod seconds 60))) (format nil "~Dm" (floor seconds 60)))
        (t (format nil "~Ds" seconds))))

(defun %report-resolve-subject (kernel kind value)
  "Whether the session holds the thing the selector names. Answers T, or NIL
with the `no such <kind>` refusal rule 8 requires (SPEC-WORK.md:5090).

`external:` names a thing the tree has no identity for, so it resolves to
itself and is never `no such`. A node resolves by validator rule 2's
resolution, C included -- `%node` holds both branches."
  (let ((state (kernel-state kernel)))
    (ecase kind
      (:node (and (%node-quiet state value) t))
      (:machine (and (kernel-fleet kernel)
                     (fleet-member (kernel-fleet kernel) value)
                     t))
      (:friend (and (kernel-fleet kernel)
                    (member value (fleet-friends (kernel-fleet kernel)) :test #'string=)
                    t))
      (:route (and (kernel-routes kernel)
                   (route-member (kernel-routes kernel) value)
                   t))
      (:offer (and (kernel-controls kernel)
                   (find value (ctl-offers (kernel-controls kernel))
                         :key (lambda (o) (getf o :id))
                         :test #'equal)
                   t))
      (:external t))))

(defun %report-submit (kernel request)
  "`report`, run on the kernel's one command thread.

STELLA'S [P1] ON 12236baa: the first cut prepared the event, took the revision
from `kernel-next-rev` and then asked the journal for acceptance -- all on the
CALLER's thread. With the prepared report paused before journal acceptance, a
normal `submit` committed revisions 1 and 2; the report then resumed, wrote
revision 1 again and reset `next-rev` to 2. Its `:unmet` and `:need` were read
at the same stale moment.

The whole mutation is now one command: validation, the dedup answer, the
revision, the node diagnostics, the journal record and the apply all happen
here, inside the writer, in the one total order -- the same `%oneshot-submit`
path `take --node` and `dep` take.

Request is the kernel command plist; `report-submit` is the caller's wrapper."
  (apply (function %report-submit-1) kernel
         (loop for (key value) on request by (function cddr)
               unless (eq key :verb) append (list key value))))

(defun %report-submit-1 (kernel &key subject act what acted-at instead-of reason
                                  as request stamp (clock :tool) expect skew view)
  "`report`: one `:report` event recording a hand act, changing nothing.

    nova-work report --session <path> <write flags> --act <launched|stopped|other>
      --subject <node|machine|friend|route|offer|external>:<text> --what <text>
      --acted-at <stamp> --instead-of <text|-> --reason <text>

Answers (values OK-P LINE EXIT-CODE ENVELOPE). The lines (SPEC-WORK.md:5085):

    REPORT OK id=<event-id> request=<id> subject=<selector> act=<...>
      instead-of=<text|-> acted-at=<stamp> lag=<duration> unmet=<n|->
      need=<id|-> holder=<name|unowned|-> rev=<n> pushed=<rev|->
    REPORT FAIL subject=<selector|->: <reason>
    REPORT FAIL subject=<selector> expect=<rev> current=<rev>: stale

`emitted=` is omitted, as every other OK line in this kernel omits it.

No field restates the envelope (SPEC-WORK.md:5057): the reporter is the event's
`:by`, the moment of the report is its `:stamp` and `:clock`, and the six fields
carry only what the envelope cannot. `:acted-at` is when the hand acted, which
is not when it was reported, and the two are kept apart as `:given` and `:tool`
clocks are; `lag=` is the event's `:stamp` less `:acted-at`.

`:unmet` and `:need` are the session's own half, outside the payload digest:
the count and the first unmet need of a `(:node ...)` subject at the revision
the report is applied at, `(:absent)` for every other subject.

It is NEVER reversible (SPEC-WORK.md:5083): a `:report` records that something
happened, and a mistaken report is answered by another report whose `--what`
names the first event's id.

A report carries no secret: a report about a key names where the key is and
never its value, which is the reporter's duty and no check this tool can make
on free text."
  (let* ((state (kernel-state kernel))
         (view (or view (kernel-needs-view kernel)))
         (selector-text (if (%report-text-p subject) subject "-")))
    (labels ((refuse2 (what)
               (return-from %report-submit-1
                 (values nil (format nil "REPORT FAIL subject=~A: ~A; run: nova-work help"
                                     selector-text what)
                         2 nil)))
             (refuse1 (sel what)
               (return-from %report-submit-1
                 (values nil (format nil "REPORT FAIL subject=~A: ~A" sel what) 1 nil))))
      ;; ---- exit 2: an invocation it cannot read (SPEC-WORK.md:5095) --------
      (unless (%report-text-p subject) (refuse2 "refusing to guess: --subject"))
      (unless act (refuse2 "refusing to guess: --act"))
      (unless (member act *report-acts*)
        (refuse2 "--act is launched, stopped or other"))
      (unless (%report-text-p what) (refuse2 "refusing to guess: --what"))
      (unless (%report-text-p reason) (refuse2 "refusing to guess: --reason"))
      (unless (%report-text-p instead-of) (refuse2 "refusing to guess: --instead-of"))
      (unless (%report-text-p acted-at) (refuse2 "refusing to guess: --acted-at"))
      (unless (%report-text-p request) (refuse2 "refusing to guess: --request"))
      (unless (%report-rfc3339-p acted-at)
        (refuse2 "--acted-at is not an RFC 3339 UTC stamp"))
      (multiple-value-bind (kind value) (parse-report-subject subject)
        (unless kind
          (refuse2 "--subject is <node|machine|friend|route|offer|external>:<text>"))
        (when (and (eq kind :external) (not (%report-text-p value)))
          (refuse2 "an external: subject carries text"))
        (unless (%report-text-p value)
          (refuse2 "--subject names nothing after its colon"))
        (let ((selector (report-subject-selector kind value))
              (now (or stamp "2026-09-14T12:00:00Z")))
          (unless (%report-rfc3339-p now)
            (refuse2 "--now is not an RFC 3339 UTC stamp"))
          ;; ---- exit 1: the session's own refusals (SPEC-WORK.md:5090) ------
          ;; ---- the durable replay answer, before any mutable state --------
          ;;
          ;; Stella's [P2] on 1a11652d, applied to EVERY command on this path:
          ;; an identical recorded payload answers its original receipt whatever
          ;; the set has done since, and a different payload under the same id
          ;; is refused. The digest covers the SIX payload fields only --
          ;; `:unmet` and `:need` are the session's own half and are outside it
          ;; (SPEC-WORK.md:5071) -- so it can be taken before the state is read
          ;; at all, which is what makes this ordering possible here.
          (unless (%report-text-p as) (refuse2 "refusing to guess: --as"))
          (let ((digest (payload-digest
                         (list (make-work-event
                                :kind :report :node +absent+ :by as
                                :fields (list :act act :subject (list kind value)
                                              :what what :acted-at acted-at
                                              :instead-of instead-of :reason reason)
                                :stamp now :clock clock :request request
                                :generation-owner (or as "rowan") :rev 0)))))
            (multiple-value-bind (verdict recorded)
                (%request-replay-verdict kernel request digest)
              (case verdict
                (:unavailable
                 (return-from %report-submit-1
                   (values nil (format nil "REPORT FAIL subject=~A page=~A: dedup unavailable"
                                       selector recorded)
                           1 nil)))
                (:replay
                 (return-from %report-submit-1
                   (values t recorded 0
                           (list :request request :digest digest
                                 :events (list) :replayed t))))
                (:conflict
                 (return-from %report-submit-1
                   (values nil (format nil "REPORT FAIL subject=~A: reused with a different payload"
                                       selector)
                           1 nil))))))
          ;; ---- and only now the mutable state -----------------------------
          (when (and expect (/= expect (state-revision state)))
            (return-from %report-submit-1
              (values nil (format nil "REPORT FAIL subject=~A expect=~D current=~D: stale"
                                  selector expect (state-revision state))
                      1 nil)))
          (unless (%report-resolve-subject kernel kind value)
            (refuse1 selector (format nil "no such ~(~A~)" kind)))
          ;; `--as` is caller text and the tool authenticates nobody, but it
          ;; must name a registered friend of `friends` (SPEC-WORK.md:5079).
          (let ((friends (and (kernel-fleet kernel) (fleet-friends (kernel-fleet kernel)))))
            (unless (and friends (member as friends :test #'string=))
              (refuse1 selector "unknown reporter")))
          (let ((lag (- (parse-rfc3339 now) (parse-rfc3339 acted-at))))
            (when (< lag (- (if skew (parse-duration skew) 0)))
              (refuse1 selector "acted-at ahead of the clock"))
            ;; ---- the event ------------------------------------------------
            (let* ((node-subject-p (eq kind :node))
                   (unmet (when node-subject-p
                            (node-needs-status state value :view view)))
                   (need (when node-subject-p
                           (nth-value 1 (node-needs-status state value :view view))))
                   (holder (cond ((not node-subject-p) "-")
                                 ((wnode-holder (%node-quiet state value)))
                                 (t "unowned")))
                   (event (make-work-event
                           :kind :report
                           ;; its subject is a hand act and not a node
                           :node +absent+
                           :by as
                           :fields (list :act act
                                         :subject (list kind value)
                                         :what what
                                         :acted-at acted-at
                                         :instead-of instead-of
                                         :reason reason
                                         ;; outside the payload digest
                                         :unmet (if node-subject-p unmet +absent+)
                                         :need (if node-subject-p need +absent+))
                           :stamp now
                           :clock clock
                           :request request
                           :generation-owner (or as "rowan")
                           :rev (kernel-next-rev kernel)))
                   (digest (payload-digest (list event)))
                   (line (format nil "REPORT OK id=~A request=~A subject=~A act=~(~A~) instead-of=~A acted-at=~A lag=~A unmet=~A need=~A holder=~A rev=~D pushed=-"
                                 (event-id event) request selector act
                                 instead-of acted-at (%report-duration lag)
                                 (if node-subject-p (format nil "~D" unmet) "-")
                                 (if node-subject-p need "-")
                                 holder
                                 (work-event-rev event))))
              ;; The dedup answer was given above, before any state was read.
              (let ((envelope (list :request request :digest digest :events (list event))))
                (multiple-value-bind (accepted refusal)
                    (journal-accept (kernel-journal kernel) envelope)
                  (unless accepted
                    (return-from %report-submit-1
                      (values nil (format nil "REPORT FAIL subject=~A: journal refused acceptance: ~A"
                                          selector refusal)
                              1 nil))))
                (journal-record (kernel-journal kernel) request digest line
                                (work-event-rev event))
                ;; It is never reversible (SPEC-WORK.md:5083): the applied entry
                ;; is terminal, so an `undo` of it is refused `not reversible
                ;; here` rather than answered `no such request`. A mistaken
                ;; report is answered by another report whose `--what` names
                ;; the first event's id.
                (setf (gethash request (kernel-applied kernel))
                      (list :verb :report :node selector :terminal t
                            :request request))
                (setf (kernel-state kernel)
                      (apply-envelope (kernel-state kernel) envelope))
                (setf (kernel-next-rev kernel) (1+ (work-event-rev event)))
                (values t line 0 envelope)))))))))

(defun report-submit (kernel &key subject act what acted-at instead-of reason
                                  as request stamp (clock :tool) expect skew view)
  "`report`: the caller's wrapper. It submits one `:report` command, so the
whole mutation -- validation, dedup, the revision, the node diagnostics, the
journal record and the apply -- happens on the kernel's one command thread, in
the one total order (Stella's [P1] on 12236baa)."
  (submit kernel (list :verb :report
                       :subject subject :act act :what what :acted-at acted-at
                       :instead-of instead-of :reason reason :as as
                       :request request :stamp stamp :clock clock
                       :expect expect :skew skew :view view)))

;;; ------------------------------------------------------------------
;;; the one reports ask                          SPEC-WORK.md:5142-5163
;;; ------------------------------------------------------------------
;;;
;;; Rule 10: "Reports are read by one ask, and the counts are information."
;;; `query --ask reports --branch open --since <revision>`. No rule, gate or
;;; exit in the document reads these counts, and none of them has a value that
;;; is good or bad by itself.

(defun %report-private-p (state event)
  "Whether the report's subject is a `:private` node. The privacy floor holds,
the id included: such a report prints `subject=node:-`, `need=-`, `what=-` and
`reason=-` on every read but the owning session's, is counted in `reports=` and
in the line's `private=`, and names nothing of the node, as every other view of
a private node does (the approved follow-up #1580 at 191b9ab7, its rule 10)."
  (let ((node (report-subject-node event)))
    (and node
         (let ((n (%node-quiet state node)))
           (and n (eq :true (wnode-private n)))))))

(defun %report-row (state event &key owning)
  "One `QUERY ROW` of rule 10 (SPEC-WORK.md:5148). The row prints the why beside
the what, `reason=` last, so the one read of reports is not a log without its
reasons."
  (let* ((hidden (and (%report-private-p state event) (not owning)))
         (subject (getf event :subject))
         (kind (and (consp subject) (first subject)))
         (value (and (consp subject) (second subject)))
         (lag (ignore-errors
               (- (parse-rfc3339 (getf event :stamp))
                  (parse-rfc3339 (getf event :acted-at))))))
    (format nil "QUERY ROW ~A kind=report act=~(~A~) subject=~A instead-of=~A acted-at=~A at=~A clock=~(~A~) lag=~A by=~A unmet=~A need=~A what=~A reason=~A"
            (getf event :rev)
            (getf event :act)
            (if hidden "node:-" (report-subject-selector kind value))
            (getf event :instead-of)
            (getf event :acted-at)
            (getf event :stamp)
            (getf event :clock)
            (%report-duration lag)
            (getf event :by)
            (let ((u (getf event :unmet))) (if (absentp u) "-" (format nil "~D" u)))
            (let ((n (getf event :need)))
              (if (or hidden (absentp n) (null n)) "-" n))
            (if hidden "-" (getf event :what))
            (if hidden "-" (getf event :reason)))))

(defparameter *reports-default-max* 20
  "The default page size of the reports ask. SPEC-WORK.md bounds every listing
the same way: a listing is capped at 20 unless `--max` says otherwise, `--max 0`
means ALL, and a negative `--max` is refused. Stella's [P2] on 3967655e's
sibling: the first cut defaulted to 21 and printed 21 rows with no `MORE` line,
and `--max 0` printed zero rows WITH a `MORE` line -- exactly backwards.")

(defun query-reports (state &key since node (max *reports-default-max*)
                                 (branch :open) floor owning)
  "`query --ask reports --branch open --since <revision>` (SPEC-WORK.md:5142).

Answers (values COUNT-LINE ROWS EXIT-CODE MORE-LINE). Rows are one per
`:report` event since that revision, NEWEST LAST, capped by `--max` with a
`MORE` line, and `--node <id>` narrows it to reports whose subject is that node.

`--since` is required and is refused before the retention boundary exactly as
`handoffs --since` is; `--branch closed` and `--branch root` are exit 2 as they
are for `handoffs`.

The three counts: `reports=` is the events in the range, `no-verb=` those whose
`:instead-of` is `-`, and `launched-unmet=` the `:launched` reports whose
`:unmet` was above zero. **No rule, gate or exit in this document reads these
counts**, and none of them has a value that is good or bad by itself.

The range is counted in REVISIONS, which the session assigns, so a backdated or
postdated `--acted-at` moves no report into or out of any answer."
  (when (member branch '(:closed :root))
    (return-from query-reports
      (values (format nil "QUERY FAIL ask=reports: not admitted under branch=~(~A~)" branch)
              '() 2 nil)))
  (unless (integerp since)
    (return-from query-reports
      (values "QUERY FAIL ask=reports: --since is required" (list) 2 nil)))
  (unless (and (integerp max) (>= max 0))
    (return-from query-reports
      (values (format nil "QUERY FAIL ask=reports max=~A: --max is zero (all) or a positive count"
                      max)
              (list) 2 nil)))
  (when (and floor (< since floor))
    (return-from query-reports
      (values (format nil "QUERY FAIL ask=reports since=~D floor=~D: before the retention boundary"
                      since floor)
              '() 2 nil)))
  (let* ((all (remove-if-not
               (lambda (e)
                 (and (> (getf e :rev) since)
                      (or (null node) (equal node (report-subject-node e)))))
               (state-reports state)))
         (reports (length all))
         (no-verb (count-if (lambda (e) (equal "-" (getf e :instead-of))) all))
         (launched-unmet (count-if (lambda (e)
                                     (and (eq :launched (getf e :act))
                                          (let ((u (getf e :unmet)))
                                            (and (integerp u) (plusp u)))))
                                   all))
         (private (count-if (lambda (e) (%report-private-p state e)) all))
         ;; `--max 0` means all; a positive max caps; a negative one is refused
         ;; above.
         (shown (if (and max (plusp max) (< max reports)) (subseq all 0 max) all))
         (more-p (< (length shown) reports)))
    (values (format nil "QUERY OK ask=reports branch=~(~A~) since=~D shown=~D [reports=~D no-verb=~D launched-unmet=~D] private=~D"
                    branch since (length shown) reports no-verb launched-unmet private)
            (mapcar (lambda (e) (%report-row state e :owning owning)) shown)
            0
            (when more-p
              (format nil "QUERY MORE ask=reports shown=~D of=~D"
                      (length shown) reports)))))
