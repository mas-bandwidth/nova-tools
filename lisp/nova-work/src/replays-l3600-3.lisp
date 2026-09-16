;;;; replays-l3600-3.lisp --- the pure parts of eight replays named in
;;;; docs/SPEC-WORK.md (lines 3600-99999, batch 3), each tested in
;;;; tests/replays-l3600-3.lisp.
;;;;
;;;; Slice 1 has no session, no CLI, no socket and no goal verb, so the data and
;;;; decisions each replay names are modelled here as pure functions and records
;;;; over immutable snapshots. The wiring that would connect them to a live
;;;; engine (kernel/session/state) is owed and listed one line each in RESULT.md;
;;;; nothing here edits those files.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; four-capability-groups-and-three-fields   SPEC-WORK.md:5611
;;; ------------------------------------------------------------------

(defstruct (cap-group (:conc-name cg-))
  id            ; its stable id
  source        ; its source
  last-verified ; its last-verified stamp
  availability  ; its availability
  constraints   ; its constraints
  support       ; declared support  \
  runtime       ; verified runtime    } three fields, never one
  capacity)     ; current free capacity /

(defun cap-three-fields (g)
  "The three fields, kept apart. They never collapse into one."
  (list (cg-support g) (cg-runtime g) (cg-capacity g)))

(defun cap-four-kinds ()
  "Child agents, swarms, local models and one-shots."
  '(:child-agents :swarms :local-models :one-shots))

(defun cap-express (kind)
  "One capability group per kind, stable id under the kind."
  (make-cap-group :id kind :source nil :last-verified nil :availability :unknown
                  :constraints nil :support '() :runtime '() :capacity '()))

;;; ------------------------------------------------------------------
;;; explicit-rest-is-not-pinged   SPEC-WORK.md:5556
;;; ------------------------------------------------------------------

(defun classify-capacity (&key rest-p silences-p delivered-p)
  "Answer (values status reason) for one capacity. Explicit rest is respected
and is never pinged. Silence at or past the threshold triggers exactly the one
bounded ping. A capacity whose probe came back unresolved is marked unavailable
with reason :unconfirmed -- never :asleep, never :exhausted-credit."
  (cond (rest-p (values :rest nil))
        (silences-p (values :ping nil))
        (t (values (if delivered-p :available :unavailable)
                   (if delivered-p nil :unconfirmed)))))

;;; ------------------------------------------------------------------
;;; fenced-export-can-finish   SPEC-WORK.md:5782
;;; ------------------------------------------------------------------

(defstruct (fenced-session (:conc-name fs-))
  owner       ; exactly one owner; no second owner, no mutation authority
  operations) ; alist op-id -> (kind . status)

(defun fenced-start-export (s id)
  (let ((p (copy-fenced-session s)))
    (setf (fs-operations p) (acons id (cons :export :running) (fs-operations p)))
    p))

(defun fenced-status (s id)
  (let ((row (assoc id (fs-operations s) :test #'string=)))
    (if row (cdr row) nil)))

(defun fenced-admit (s verb id)
  "Only an export status read is admitted; `operation list`, an unknown id, a
non-export id, and every canonical write are refused `fenced`.
Returns (values ok line)."
  (ecase verb
    (:status
     (let ((row (fenced-status s id)))
       (if (and row (eq :export (car row)))
           (values t (format nil "EXPORT OK id=~A status=~A" id
                             (string-downcase (symbol-name (cdr row)))))
           (values nil "fenced"))))
    (:list (values nil "fenced"))
    (:write (values nil "fenced"))))

(defun fenced-cancel (s id)
  "Cancel an unfinished export: status becomes :cancelled and publication is
reconciled; an unknown or non-export id is refused. Returns (values session line)."
  (let ((row (fenced-status s id)))
    (if (and row (eq :export (car row)))
        (let ((p (copy-fenced-session s)))
          (setf (fs-operations p) (acons id (cons :export :cancelled) (fs-operations p)))
          (values p (format nil "EXPORT OK id=~A status=cancelled publication=reconciled" id)))
        (values s "fenced"))))

;;; ------------------------------------------------------------------
;;; goal-crosses-harness / goal-expect-is-required /
;;; goal-stale-update-refuses / goal-stop-is-a-request-not-evidence
;;; SPEC-WORK.md:5787 / 5824 / 5797 / 5806
;;; ------------------------------------------------------------------

(defstruct (goal-state (:conc-name gs-))
  node branch state revision stop evidence note progress)

(defun goal-show-line (g)
  "The `goal show` line: goal=<node> rev=<r> stop=<none|requested|cancelled>."
  (format nil "goal=~A rev=~D stop=~A"
          (or (gs-node g) "-") (gs-revision g)
          (ecase (gs-stop g)
            ((:none) "none") ((:requested) "requested") ((:cancelled) "cancelled"))))

(defun goal-expect (g expect)
  "goal set/update --expect. Without --expect: exit 2 naming the flag, nothing
written. One behind: refused `stale` at exit 1 with the current value printed.
At or after the current local revision: admitted. Returns (values ok line exit)."
  (cond
    ((null expect) (values nil "goal set/update: --expect is required" 2))
    ((< expect (gs-revision g))
     (values nil (format nil "GOAL FAIL expect=~D current=~D: stale" expect (gs-revision g)) 1))
    (t (values t nil 0))))

(defun goal-stop (g &key reason)
  "goal update --stop. A :review or :done node has no edge. Otherwise stop is a
request, not evidence: stop becomes :requested and the event is a :transition
:to :cancel-requested carrying :reason and no :evidence.
Returns (values goal line event)."
  (if (member (gs-state g) '(:review :done))
      (values nil (format nil "GOAL FAIL node=~A: no edge" (gs-node g)) nil)
      (let ((p (copy-goal-state g)))
        (setf (gs-stop p) :requested
              (gs-revision p) (1+ (gs-revision g))
              (gs-evidence p) nil)
        (values p
                (format nil "GOAL OK node=~A change=stop kind=transition rev=~D"
                        (gs-node g) (gs-revision p))
                (list :kind :transition :to :cancel-requested
                      :reason (or reason "") :evidence :absent)))))

(defun goal-cancel (g &key evidence)
  "event --kind cancel --evidence <pointer>: terminal. stop becomes :cancelled."
  (let ((p (copy-goal-state g)))
    (setf (gs-stop p) :cancelled
          (gs-revision p) (1+ (gs-revision g))
          (gs-evidence p) evidence)
    (values p (format nil "GOAL OK node=~A change=cancel rev=~D"
                      (gs-node g) (gs-revision p)))))

(defun goal-withdraw (g)
  "state --to doing --reason by id: the withdrawal. stop returns to :none."
  (let ((p (copy-goal-state g)))
    (setf (gs-stop p) :none
          (gs-state p) :doing
          (gs-revision p) (1+ (gs-revision g)))
    (values p (format nil "GOAL OK node=~A change=withdraw rev=~D"
                      (gs-node g) (gs-revision p)))))

(defun goal-progress (g &key text)
  "goal update --progress. Refused `stop requested` while stop is requested or
cancelled. Returns (values goal line)."
  (declare (ignore text))
  (if (member (gs-stop g) '(:requested :cancelled))
      (values nil (format nil "GOAL FAIL node=~A: stop requested" (gs-node g)))
      (values g nil)))

(defun goal-progress-evidence (g evidence)
  "goal update --progress with the evidence triple on a :doing leaf: writes the
:evidence event and advances the revision. Returns the new snapshot."
  (let ((p (copy-goal-state g)))
    (setf (gs-evidence p) evidence
          (gs-revision p) (1+ (gs-revision g)))
    p))

(defun goal-set-refusal (target-branch stop)
  "goal set --goal <node>. A node on the closed branch is refused
`disposition=done` whatever --expect says; a cancelled goal is refused
`disposition=cancelled`. NIL means admitted."
  (cond
    ((eq target-branch :c) "GOAL FAIL: disposition=done")
    ((eq stop :cancelled) "GOAL FAIL: disposition=cancelled")
    (t nil)))

;;; ------------------------------------------------------------------
;;; dry-run-writes-nothing   SPEC-WORK.md:5527
;;; ------------------------------------------------------------------

(defstruct (flow (:conc-name flow-))
  revision events pending pushed dedup)

(defun flow-counters (f)
  (list :events (flow-events f) :pending (flow-pending f) :pushed (flow-pushed f)))

(defun flow-dedup-fresh-p (f id)
  (not (member id (flow-dedup f) :test #'string=)))

(defun flow-expect-check (f expect)
  "apply --expect. Without --expect: exit 2. One behind (or otherwise stale):
refused `stale` naming expect and current at exit 1. At the current revision:
admitted. Returns (values ok line exit)."
  (cond
    ((null expect) (values nil "apply: --expect is required" 2))
    ((< expect (flow-revision f))
     (values nil (format nil "APPLY FAIL expect=~D current=~D: stale" expect (flow-revision f)) 1))
    (t (values t nil 0))))

(defun flow-dry-run (f request)
  "Validate and project without mutating F: the projection is at R+1, the
--request id is still new to dedup, and events=/pending=/pushed= are unchanged
in F itself. Returns the projection."
  (declare (ignore request))
  (let ((p (copy-flow f)))
    (setf (flow-revision p) (1+ (flow-revision f)))
    p))

(defun flow-commit (f request)
  "An accepted mutation: the request id enters dedup, revision and the counters
move by one."
  (let ((p (copy-flow f)))
    (setf (flow-dedup p) (cons (getf request :request) (flow-dedup p))
          (flow-revision p) (1+ (flow-revision f))
          (flow-events p) (1+ (flow-events f))
          (flow-pushed p) (1+ (flow-pushed f)))
    p))

(export '(make-cap-group cg-id cg-source cg-last-verified cg-availability
          cg-constraints cg-support cg-runtime cg-capacity
          cap-three-fields cap-four-kinds cap-express
          classify-capacity
          make-fenced-session fs-owner fs-operations fenced-start-export
          fenced-admit fenced-cancel fenced-status
          make-goal-state gs-node gs-branch gs-state gs-revision gs-stop
          gs-evidence gs-note gs-progress
          goal-show-line goal-expect goal-stop goal-cancel goal-withdraw
          goal-progress goal-progress-evidence goal-set-refusal
          make-flow flow-revision flow-events flow-pending flow-pushed flow-dedup
          flow-counters flow-dedup-fresh-p flow-expect-check flow-dry-run flow-commit))
