;;;; decide.lisp (tests) --- the slice-2 red test of docs/SPEC-DECIDE.md.
;;;;
;;;; The named case is the Lisp-kernel red test of SPEC-DECIDE.md:185-187:
;;;; a decision journaled with the in-process fake present replays with the
;;;; fake ABSENT, reads the same answer back from the event's question hash,
;;;; and never makes a fresh provider call. The kernel's decisive order is
;;;; bit-for-bit identical before and after: a decision advises, the machinery
;;;; decides (rules 5, 6 and 9).

(in-package #:nova-work/tests)

(defun decide-question ()
  "One bounded choice question (SPEC-DECIDE rule 1)."
  (list :kind :choice
        :question "Which ready node should the kernel take first?"
        :options '("ship" "wait")
        :subject "acme/work/f1"
        :evidence "acme/work/f1/1"))

(deftest "a-journaled-decision-replays-without-the-provider"
    "docs/SPEC-DECIDE.md:185-187"
    "expected=journal-carries-the-question-hash;replay-answers-from-the-journal;no-fresh-provider-call;kernel-order-bit-for-bit-identical"
  (let* ((journal (make-ordering-journal))
         (kernel (make-kernel :state (make-seed-state *seed*) :journal journal
                              :rev-base 1))
         (fake (make-fake-decider :answer "ship" :confidence 0.9))
         (question (decide-question))
         (eligible (list (list :id "acme/work/f1/t1" :priority 5)
                         (list :id "acme/work/f1/t2" :priority 9)))
         (order-before (ready-order eligible nil :order :priority))
         (revision-before (state-revision (kernel-state kernel))))
    ;; A fresh decision consults the fake once and journals the question hash.
    (let ((first (decide kernel question 0.5 :provider fake)))
      (check-equal "ship" (decision-answer first)
                   "the fake's above-floor answer is decisive")
      (check-equal 1 (fake-decider-calls fake) "the fake is consulted once")
      (check-equal (question-hash question) (decision-question-hash first)
                   "the decision names the question hash")
      (let ((event (journaled-decision-event journal question)))
        (ok event "the decision is journaled as an event")
        (check-equal :decide (work-event-kind event)
                     "the journaled event has the :decide kind")
        (check-equal (question-hash question)
                     (getf (work-event-fields event) :question-hash)
                     "the journaled event carries the question hash")))
    ;; Replay with the provider ABSENT: the journal answers, and the provider
    ;; is never consulted again.
    (let ((replay (decide kernel question 0.5 :provider nil)))
      (check-equal "ship" (decision-answer replay)
                   "the replay reproduces the journaled answer")
      (check-equal :journal (decision-source replay)
                   "the replay answers from the journal")
      (check-equal 1 (fake-decider-calls fake)
                   "the replay made no fresh provider call"))
    ;; The kernel's decisive order is bit-for-bit identical before and after.
    (check-equal order-before (ready-order eligible nil :order :priority)
                 "the kernel's decisive order is bit-for-bit identical")
    (check-equal revision-before (state-revision (kernel-state kernel))
                 "the decision moved no revision")))

(deftest "a-below-floor-decision-keeps-the-kernels-own-answer"
    "docs/SPEC-DECIDE.md:177-187"
    "expected=below-the-floor-the-kernel-keeps-its-own-abstain;the-provider-answer-is-a-suggestion;the-confidence-and-floor-are-carried"
  (let* ((kernel (make-kernel :state (make-seed-state *seed*)
                              :journal (make-ordering-journal) :rev-base 1))
         (question (decide-question))
         (fake (make-fake-decider :answer "ship" :confidence 0.5))
         (decision (decide kernel question 0.9 :provider fake)))
    (check-equal :abstain (decision-answer decision)
                 "below the floor the kernel keeps its own abstain")
    (check-equal "ship" (decision-proposed decision)
                 "the provider's answer is still recorded")
    (ok (decision-suggestion-p decision)
        "the below-floor answer is a suggestion")
    (check-equal 500 (decision-confidence decision) "the provider confidence is carried")
    (check-equal 900 (decision-floor decision) "the floor is carried")))

;;; ------------------------------------------------------------------
;;; E10-F01 "Structured refusal and operation diagnostics" (ROADMAP.md:976):
;;; Provide bounded inspect/diagnose drill-down without secrets or private
;;; bodies.
;;;
;;; The two halves of the criterion, each by its own SPEC-WORK line:
;;;   docs/SPEC-WORK.md:2737    -- `operation status|list|wait|cancel` are
;;;                               "each bounded and capped like every other
;;;                               listing" (the bounded drill-down).
;;;   docs/SPEC-WORK.md:3027-3029 -- the privacy floor: a private node "leaves
;;;                               every public render and `private=<n>` is all
;;;                               that is printed of them; a refusal names the
;;;                               field and never prints a private value."
;;; ------------------------------------------------------------------

(deftest "TestE10F01ProvideBoundedInspectDiagnoseDrill"
    "docs/SPEC-WORK.md:2737;3027-3029"
    "expected=bounded-drill-down-summary-not-body;listing-capped;private-node-prints-marker-never-its-body"
  ;; Part 1: the inspect/diagnose drill-down is bounded. `operation status`
  ;; answers with the named operation's own summary (kind and state) and never
  ;; a whole-queue or whole-body dump; the listing is capped by --max.
  (let* ((events '((:id "ev-1" :kind :state-to-done :request "req-1")))
         (session (make-work-session :events events))
         (session (session-add-operation
                   session (make-operation :id "op-a" :op :capture
                                           :request "req-a" :state :running
                                           :staged-bytes 256)))
         (session (session-add-operation
                   session (make-operation :id "op-b" :op :export
                                           :request "req-b" :state :queued)) )
         (session (session-add-operation
                   session (make-operation :id "op-c" :op :clip
                                           :request "req-c" :state :queued))))
    (let ((status (operation-status session "op-b")))
      (check-equal :export (getf status :op) "the drill-down names the operation kind")
      (check-equal :queued (getf status :state) "the drill-down reads the operation state")
      (ok (null (getf status :operations)) "no whole-queue dump in the drill-down")
      (ok (null (getf status :events)) "no event-body dump in the drill-down")
      (ok (null (getf status :spec)) "no internal spec/body in the drill-down"))
    (let ((rows (session-operation-list session :max 2)))
      (check-equal 2 (length rows) "the operation listing is capped by --max")
      (dolist (r rows)
        (ok (and (getf r :id) (getf r :op) (getf r :state))
            "a listing row is a summary of id/kind/state, not a body")))
    (ok (session-bounded-p session) "the queues, staged bytes and results stay bounded"))
  ;; Part 2: the drill-down discloses no private body. A private node's render
  ;; prints `private=1` and never its title or links (which would otherwise be
  ;; its whole public body).
  (let* ((k (make-kernel :state (make-seed-state
                                 '((:id "root" :type :work-set :parent nil :state :unknown)
                                   (:id "root/p" :type :task :parent "root" :state :doing
                                         :title "secret body" :links ("acct-token-123")
                                         :private t)))))
         (rendered (render-node k "root/p")))
    (check-string= "private=1" rendered "a private node's drill-down prints the marker only")
    (ok (null (search "secret body" rendered)) "the private body is not disclosed: ~A" rendered)
    (ok (null (search "acct-token-123" rendered)) "no link/token value is disclosed: ~A" rendered)))

;;; ------------------------------------------------------------------
;;; E04-F01-03 "Label features, leaves and member grains with revision"
;;; (ROADMAP.md:460).
;;;
;;; docs/SPEC-WORK.md:1989 -- "Units are labelled on every line: unit=features
;;;   or unit=leaves; a comparison never changes unit silently"; unit= names the
;;;   grain of the line's state counts, and never a spelling the spec nowhere
;;;   names.
;;; docs/SPEC-WORK.md:2104 -- the `size` ask answers "total required leaves",
;;;   so its count is at the leaves grain.
;;; docs/SPEC-WORK.md:2044-2046 -- the count's unit and revision are printed
;;;   with it on one line: `open=<n>`, `unit=<unit>` and `scope=<rev>`.
;;; ------------------------------------------------------------------

(deftest "TestE04F01LabelFeaturesLeavesAndMember"
    "docs/SPEC-WORK.md:1989;2104;2044-2046"
    "expected=size-ask-labels-its-count-with-the-leaves-grain;never-a-unit-the-spec-names-nowhere;revision-printed-beside-the-count"
  (let ((kernel (make-kernel :state (make-seed-state *seed*) :rev-base 1)))
    (multiple-value-bind (open unit scope line) (ask-size kernel)
      (declare (ignore open))
      ;; The size ask counts leaves (SPEC-WORK.md:2104), so its unit= must name
      ;; the leaves grain, not a unit the spec nowhere names.
      (check-equal "leaves" unit
                   "the size ask labels its count with the leaves grain, not a unit the spec never names")
      (ok (search "unit=leaves" line)
          "the QUERY OK line carries unit=leaves: ~A" line)
      (ok (not (search "unit=items" line))
          "the QUERY OK line never spells unit=items, a unit the spec does not name: ~A" line)
      ;; The count's revision is printed with it, on the same named line.
      (check-equal (state-revision (kernel-state kernel)) scope
                   "the size ask returns the state's revision as scope=")
      (ok (search (format nil "scope=~D" scope) line)
          "the revision is printed beside the count on the one QUERY OK line: ~A" line))))

;;; ------------------------------------------------------------------
;;; E08-F02-04 "Use bounded typed JSON over a local Unix socket, exact
;;; integer/time encoding and durable asynchronous operation IDs; reconcile
;;; cross-platform endpoint requirements before lock" (ROADMAP.md:819).
;;;
;;; The bounded typed JSON wire, the integer-as-string and RFC 3339 time
;;; encoding, and the durable asynchronous operation IDs are each already
;;; carried and replayed by slice-08; the half this case pins is the LAST
;;; sentence: the endpoint lock is reconciled across platform spellings NAME
;;; before it is taken, never assumed. Two SPEC-WORK lines state it:
;;;   docs/SPEC-WORK.md:184-185 -- "<session>.lock is a file only where
;;;                               --session is a filesystem path: it is that
;;;                               socket's canonical spelling with .lock
;;;                               appended, in the socket's own directory."
;;;   docs/SPEC-WORK.md:186-190 -- "On Windows the endpoint --session names is a
;;;                               named pipe (..), which is not exclusive by
;;;                               default .. that first-instance creation IS
;;;                               the endpoint lock .. there is no
;;;                               <session>.lock file on Windows."
;;; ------------------------------------------------------------------

(deftest "TestE08F02UseBoundedTypedJSONOver"
    "docs/SPEC-WORK.md:184-190;2647-2648"
    "expected=filesystem-socket-lock-is-canonical-dot-lock-in-its-own-directory;named-pipe-endpoint-has-no-lock-file;platform-spelling-named-not-assumed"
  ;; The endpoint is one transport whose spelling is the platform's: a
  ;; Unix-domain socket on a filesystem path, or the Windows named pipe
  ;; \\.\pipe\<name>. Reconcile the endpoint lock for each BEFORE it is taken.
  (let ((socket "/tmp/nova-work-e08f02/work.sock")
        (pipe "\\\\.\\pipe\\nova-work-e08f02"))
    ;; A filesystem socket's endpoint lock is a file: the socket's canonical
    ;; spelling with .lock appended, in the socket's own directory (:184-185).
    (check-string= "/tmp/nova-work-e08f02/work.sock.lock"
                   (session-endpoint-lock-path socket)
                   "a filesystem socket's endpoint lock is canonical-spelling.lock")
    ;; The named pipe is the SAME endpoint under the Windows spelling (:2647-2648).
    (ok (named-pipe-endpoint-p pipe) "the \\\\.\\pipe\\<name> spelling is recognised")
    (ok (not (named-pipe-endpoint-p socket)) "a filesystem path is not a named pipe")
    ;; A named-pipe endpoint carries no <session>.lock file at all: the
    ;; first-instance create IS the lock, so no caller ever writes a .lock under
    ;; \\.\pipe\ (:186-190).
    (ok (null (session-endpoint-lock-path pipe))
        "a named-pipe endpoint has no <session>.lock file: ~S"
        (session-endpoint-lock-path pipe)))
  ;; An existing socket reached through a symlinked --session is the SAME
  ;; endpoint: both spellings key one <session>.lock, in the real socket's own
  ;; directory, never a second lock beside the alias (:184-185).
  (let* ((real-dir (namestring (truename (test-temp-dir "e08f02-real"))))
         (alias-dir (namestring (truename (test-temp-dir "e08f02-alias"))))
         (real (format nil "~Awork.sock" real-dir))
         (alias (format nil "~Aalias.sock" alias-dir)))
    (with-open-file (out real :direction :output :if-exists :supersede
                              :if-does-not-exist :create))
    (sb-posix:symlink real alias)
    (check-string= (concatenate 'string real ".lock")
                   (session-endpoint-lock-path alias)
                   "a symlinked --session keys the real socket's .lock")
    (check-string= (session-endpoint-lock-path real)
                   (session-endpoint-lock-path alias)
                   "an alias and its socket share one endpoint lock")))
