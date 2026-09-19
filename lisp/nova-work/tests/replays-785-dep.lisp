;;;; replays-785-dep.lisp --- `dep --add` and `dep --remove` (nova-tools #785).
;;;;
;;;; Rule 6 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:4993-5031, with its replay at :6972 and its line at
;;;; :6031:
;;;;
;;;;   DEP OK id=<event-id> request=<id> node=<id> rev=<n> pushed=<rev|->
;;;;   change=<add|remove> need=<id> met=<true|false> unmet=<n>
;;;;   needs-broken=<true|false> changed=<n> emitted=<bytes>
;;;;
;;;; `met=` is the named need's; `unmet=` and `needs-broken=` are the node's,
;;;; read AFTER the change.

(in-package #:nova-work/tests)

(defparameter *dep-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/m" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/t" :type :task     :parent "acme/work" :state :todo)
    ;; a second repository work set of the same O
    (:id "other/work"   :type :work-set :parent nil          :state :unknown)
    (:id "other/work/x" :type :task     :parent "other/work" :state :doing))
  "D needs the open N. M and T are unattached; other/work/x is a need under
another repository work set of the same O.")

(defun dep-kernel (&optional (seed *dep-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1))

(defun dep-add (k node need &key (as "rowan") (reason "the new order")
                                 (request (format nil "dep-add-~A-~A" node need)))
  (dep-edit k :node node :change :add :need need :as as :reason reason
              :request request :stamp "2026-09-16T14:00:00Z"))

(defun dep-remove (k node need &key (as "rowan") (reason "it does not need it")
                                    (request (format nil "dep-rm-~A-~A" node need)))
  (dep-edit k :node node :change :remove :need need :as as :reason reason
              :request request :stamp "2026-09-16T15:00:00Z"))

;;; ------------------------------------------------------------------
;;; an-edge-added-under-work-flags-and-every-way-past-a-need-is-recorded
;;;                                              SPEC-WORK.md:4993, :6972
;;; ------------------------------------------------------------------

(deftest "an-edge-added-under-work-flags-and-every-way-past-a-need-is-recorded"
    "docs/SPEC-WORK.md:4993"
    "expected=the-edge-is-true-whether-or-not-it-is-convenient-and-there-is-no-force"
  ;; `dep --add M` of an open M onto a leased D: written, flagged, stops nothing
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (submit k (list :verb :state-to-done
                                                    :node "acme/work/n" :by "rowan"
                                                    :reason "merged" :evidence '("ev-1")
                                                    :request "done-n"
                                                    :stamp "2026-09-16T12:00:00Z"
                                                    :clock :tool
                                                    :generation-owner "gen-1"))
      (ok okp "N settles first so D can be leased: ~A" line))
    (refresh-needs-view k)
    (take-lease k "acme/work/d" "emma")
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/m")
      (ok okp "the edge is admitted under work: ~A" line)
      (check-equal 0 code "at exit 0")
      (ok (search "change=add" line) "change=add: ~A" line)
      (ok (search "need=acme/work/m" line) "need= names it: ~A" line)
      (ok (search "met=false" line) "met= is the need's own: ~A" line)
      (ok (search "unmet=1" line) "unmet= is the node's, after the change: ~A" line)
      (ok (search "needs-broken=true" line) "and an engaged node is flagged: ~A" line))
    ;; it stops nothing
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "the lease stands")
    (check-equal :todo (node-state (kernel-state k) "acme/work/d")
                 "and nothing is reopened or moved")
    (check-equal '("acme/work/n" "acme/work/m")
                 (node-deps (kernel-state k) "acme/work/d")
                 "the edge is on the node, in the order it was added"))
  ;; onto an unengaged :todo node it prints needs-broken=false
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-add k "acme/work/t" "acme/work/m")
      (ok okp "admitted: ~A" line)
      (ok (search "needs-broken=false" line)
          "an unengaged todo node is simply not needs-met: ~A" line)))
  ;; remove, admit, add back: three events with their authors, the lease
  ;; standing, and needs-broken=true from the third
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-remove k "acme/work/d" "acme/work/n")
      (ok okp "the override is a recorded edit of the edge: ~A" line)
      (ok (search "change=remove" line) "change=remove: ~A" line)
      (ok (search "unmet=0" line) "and it takes effect at once: ~A" line))
    (ok (null (handler-case (progn (take-lease k "acme/work/d" "emma") nil)
                (unsupported-input (c) (unsupported-input-what c))))
        "the node is admitted past the need it no longer has")
    (multiple-value-bind (okp line) (dep-add k "acme/work/d" "acme/work/n")
      (ok okp "and the edge goes back: ~A" line)
      (ok (search "needs-broken=true" line)
          "from the third event the node reads needs-broken=true: ~A" line))
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "the lease stands through all three")
    ;; Three recorded acts: the remove, the take, the add back. Two are `dep`
    ;; `:structure` events and the third is the lease -- "Remove, admit, add
    ;; back is therefore not silent: it is three events with their authors"
    ;; (SPEC-WORK.md:5023).
    (check-equal 2 (count-if (lambda (e) (eq :dep (getf e :verb)))
                             (node-structure-log (kernel-state k) "acme/work/d"))
                 "the two edge edits are on the record")
    (check-string= "emma" (node-holder (kernel-state k) "acme/work/d")
                   "and the take between them is the third act")
    (dolist (e (node-structure-log (kernel-state k) "acme/work/d"))
      (check-string= "rowan" (getf e :by) "each names its author")
      (ok (stringp (getf e :reason)) "and each carries its required --reason")))
  ;; the three refusals: each exit 1, nothing written
  (let* ((k (dep-kernel))
         (before (node-deps (kernel-state k) "acme/work/d")))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/d")
      (ok (not okp) "a node that needs itself is a cycle of length one")
      (check-equal 1 code "exit 1")
      (check-string= "DEP FAIL node=acme/work/d: rule 3: acme/work/d needs itself" line
                     "refused rule 3"))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/n" "acme/work/d")
      (ok (not okp) "an edge closing a two-node cycle is refused")
      (check-equal 1 code "exit 1")
      (ok (search "rule 3" line) "refused rule 3: ~A" line))
    (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/nowhere")
      (ok (not okp) "an id naming nothing is refused")
      (check-equal 1 code "exit 1")
      (check-string= "DEP FAIL node=acme/work/d: rule 2: dangling" line
                     "refused rule 2: dangling"))
    (check-equal before (node-deps (kernel-state k) "acme/work/d")
                 "and nothing was written by any of the three"))
  ;; a need under another repository work set of the same O is admitted
  (let ((k (dep-kernel)))
    (multiple-value-bind (okp line) (dep-add k "acme/work/d" "other/work/x")
      (ok okp "rule 1 asks nothing about where a need lives: ~A" line)
      (ok (search "need=other/work/x" line) "and the edge names it: ~A" line))))

;;; ------------------------------------------------------------------
;;; what there is not: an unrecorded road or a flag
;;;                                              SPEC-WORK.md:5021
;;; ------------------------------------------------------------------

(deftest "a-dep-edit-refuses-to-be-unrecorded" "docs/SPEC-WORK.md:5021"
    "expected=a-required-reason-an-author-and-no-force"
  (let ((k (dep-kernel)))
    ;; --reason is required: exit 2, nothing written
    (multiple-value-bind (okp line code)
        (dep-edit k :node "acme/work/d" :change :add :need "acme/work/m"
                    :as "rowan" :reason nil :request "r1"
                    :stamp "2026-09-16T14:00:00Z")
      (ok (not okp) "a dep edit with no reason is refused")
      (check-equal 2 code "at exit 2, an invocation that cannot be read")
      (ok (search "--reason" line) "and the line names the field: ~A" line))
    ;; so is one with no author
    (multiple-value-bind (okp line code)
        (dep-edit k :node "acme/work/d" :change :add :need "acme/work/m"
                    :as nil :reason "because" :request "r2"
                    :stamp "2026-09-16T14:00:00Z")
      (ok (not okp) "a dep edit with no author is refused")
      (check-equal 2 code "at exit 2")
      (ok (search "--as" line) "and the line names the field: ~A" line))
    (check-equal '("acme/work/n") (node-deps (kernel-state k) "acme/work/d")
                 "neither refusal wrote anything")
    ;; removing an edge that is not there is refused rather than silently ok
    (multiple-value-bind (okp line code) (dep-remove k "acme/work/d" "acme/work/m")
      (ok (not okp) "removing an edge the node does not hold is refused")
      (check-equal 1 code "at exit 1")
      (ok (search "no such edge" line) "naming what is missing: ~A" line))))

;;; ------------------------------------------------------------------
;;; a dep edit is durable, deduplicated and replayed
;;;    Stella's [P1] on 5d1f9dfa          SPEC-WORK.md:4993
;;; ------------------------------------------------------------------

(deftest "a-dep-edit-is-journaled-and-survives-a-restart" "docs/SPEC-WORK.md:4993"
    "expected=the-edge-the-verb-acknowledged-is-the-edge-a-restart-reads"
  ;; The first cut mutated `wnode-deps`, the reverse edge and the structure log
  ;; in place and appended nothing to the history: `DEP OK changed=1` came back
  ;; while the history length stayed zero, and a canonical reconstruction
  ;; produced D with no dependencies and no structure log. A restart erased the
  ;; edge that had just been acknowledged.
  (flet ((restart-of (k)
           (reconstruct-state
            (canonical-string (state-canonical-form (kernel-state k))))))
    ;; --add survives, forward edge, reverse edge and audit record
    (let* ((k (dep-kernel))
           (before (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line) (dep-add k "acme/work/d" "acme/work/m")
        (ok okp "the edge is admitted: ~A" line)
        (ok (search "changed=1" line) "and says it changed one thing: ~A" line))
      (ok (> (length (state-history (kernel-state k))) before)
          "the history grew: the edit is a journaled envelope and not a poke")
      (let ((fresh (restart-of k)))
        (check-equal '("acme/work/n" "acme/work/m") (node-deps fresh "acme/work/d")
                     "the forward edge survives a canonical reconstruction")
        (check-equal '("acme/work/d") (node-dependents fresh "acme/work/m")
                     "and so does the reverse edge")
        (let ((log (remove-if-not (lambda (e) (eq :dep (getf e :verb)))
                                  (node-structure-log fresh "acme/work/d"))))
          (check-equal 1 (length log) "and the audit record")
          (check-string= "rowan" (getf (first log) :by) "with its author")
          (ok (stringp (getf (first log) :reason)) "and its reason"))))
    ;; --remove survives too
    (let ((k (dep-kernel)))
      (dep-remove k "acme/work/d" "acme/work/n")
      (let ((fresh (restart-of k)))
        (check-equal '() (node-deps fresh "acme/work/d")
                     "a removed edge stays removed across a restart")
        (check-equal '() (node-dependents fresh "acme/work/n")
                     "and the reverse edge with it")))
    ;; an identical request replays to the ORIGINAL receipt and writes once
    (let ((k (dep-kernel)))
      (multiple-value-bind (okp first-line) (dep-add k "acme/work/d" "acme/work/m"
                                                     :request "review-add")
        (ok okp "the first add: ~A" first-line)
        (let ((history (length (state-history (kernel-state k)))))
          (multiple-value-bind (okp again) (dep-add k "acme/work/d" "acme/work/m"
                                                    :request "review-add")
            (ok okp "the retransmission is admitted")
            (check-string= first-line again
                           "and answers the original deduplicated receipt"))
          (check-equal history (length (state-history (kernel-state k)))
                       "and writes no second envelope"))))
    ;; the SAME request id with a DIFFERENT payload is refused
    (let ((k (dep-kernel)))
      (dep-add k "acme/work/d" "acme/work/m" :request "review-add")
      (let ((deps (node-deps (kernel-state k) "acme/work/d"))
            (history (length (state-history (kernel-state k)))))
        (multiple-value-bind (okp line code)
            (dep-remove k "acme/work/d" "acme/work/n" :request "review-add")
          (ok (not okp) "one request id with a different payload is refused")
          (check-equal 1 code "at exit 1")
          (ok (search "reused with a different payload" line) "by name: ~A" line))
        (check-equal deps (node-deps (kernel-state k) "acme/work/d")
                     "and nothing was mutated")
        (check-equal history (length (state-history (kernel-state k)))
                     "and nothing was written")))
    ;; a REFUSED edit writes nothing at all -- no partial mutation
    (let* ((k (dep-kernel))
           (deps (node-deps (kernel-state k) "acme/work/d"))
           (history (length (state-history (kernel-state k)))))
      (dolist (bad '(("acme/work/d" "acme/work/d") ("acme/work/d" "acme/work/nowhere")))
        (multiple-value-bind (okp line code) (dep-add k (first bad) (second bad))
          (declare (ignore line))
          (ok (not okp) "~S is refused" bad)
          (check-equal 1 code "at exit 1")))
      (check-equal deps (node-deps (kernel-state k) "acme/work/d") "no edge moved")
      (check-equal history (length (state-history (kernel-state k)))
                   "and no envelope was written")
      (check-equal '() (remove-if-not (lambda (e) (eq :dep (getf e :verb)))
                                      (node-structure-log (kernel-state k) "acme/work/d"))
                   "and no audit record was left behind"))
    ;; a FRESH request naming an edge that is already there: a visible,
    ;; successful no-effect (Stella's ruling on SPEC-QUESTION 2)
    (let* ((k (dep-kernel))
           (history (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line code)
          (dep-add k "acme/work/d" "acme/work/n" :request "fresh-duplicate")
        (ok okp "a duplicate add under a fresh request is admitted")
        (check-equal 0 code "at exit 0")
        (ok (search "DEP NOTE" line) "as a visible no-effect: ~A" line)
        (ok (search "changed=0" line) "with changed=0: ~A" line))
      (check-equal history (length (state-history (kernel-state k)))
                   "and it writes no new event")
      (check-equal '("acme/work/n") (node-deps (kernel-state k) "acme/work/d")
                   "and does not double the edge")
      ;; and it does NOT bypass request-id validation
      (multiple-value-bind (okp line code)
          (dep-edit k :node "acme/work/d" :change :add :need "acme/work/n"
                      :as "rowan" :reason "no request id" :request nil
                      :stamp "2026-09-16T14:00:00Z")
        (ok (not okp) "a duplicate add with no request id is still refused")
        (check-equal 2 code "at exit 2")
        (ok (search "--request" line) "naming the field: ~A" line)))))

;;; ------------------------------------------------------------------
;;; the same, against the REAL file journal
;;;    Stella's [P1] on 1a558076          SPEC-WORK.md:307, :315
;;; ------------------------------------------------------------------
;;;
;;; The `ordering-journal` fake permits an overwrite of a recorded receipt. The
;;; real `file-journal` does not: it expects exactly one record per pending
;;; envelope. The first cut recorded a PLACEHOLDER and then recorded again, so
;;; an ordinary add returned `MUTATION FAIL ... journal-record mismatch:
;;; expected pending envelope` at exit 2 WITH THE EDGE ALREADY APPLIED, and the
;;; retry answered a bare `DEP OK`. No failure was injected. Every case below is
;;; the fake's case run again against the real thing.

(defun dep-file-kernel (path &key (seed *dep-seed*))
  (let ((state (make-seed-state seed)))
    (make-kernel :state state
                 :journal (open-file-journal
                           path :initial-state-hash (root-digest state))
                 :rev-base 1)))

(deftest "a-dep-edit-is-one-record-in-the-real-file-journal" "docs/SPEC-WORK.md:307"
    "expected=one-durable-append-per-mutation-and-the-complete-receipt-on-it"
  (let* ((path (test-journal-path "dep-file-journal"))
         (k (dep-file-kernel path)))
    (unwind-protect
         (progn
           ;; an ordinary --add: exit 0, and the COMPLETE receipt, not a bare
           ;; `DEP OK` and not a MUTATION FAIL after the edge was applied
           (multiple-value-bind (okp line code) (dep-add k "acme/work/d" "acme/work/m"
                                                         :request "file-add")
             (ok okp "an ordinary add succeeds against a real journal: ~A" line)
             (check-equal 0 code "at exit 0")
             (ok (search "change=add" line) "with the complete receipt: ~A" line)
             (ok (search "need=acme/work/m" line) "naming the need: ~A" line)
             (ok (search "unmet=" line) "and the counts read after the change: ~A" line)
             (ok (not (search "MUTATION FAIL" line)) "and no mutation failure: ~A" line))
           (check-equal '("acme/work/n" "acme/work/m")
                        (node-deps (kernel-state k) "acme/work/d")
                        "and the edge is installed")
           ;; the identical request replays to that same complete receipt
           (multiple-value-bind (okp again code) (dep-add k "acme/work/d" "acme/work/m"
                                                          :request "file-add")
             (ok okp "the identical request replays")
             (check-equal 0 code "at exit 0")
             (ok (search "change=add" again)
                 "to the COMPLETE receipt the file journal holds, not a bare DEP OK: ~A"
                 again))
           ;; --remove, on the real journal
           (multiple-value-bind (okp line code) (dep-remove k "acme/work/d" "acme/work/n"
                                                            :request "file-remove")
             (ok okp "an ordinary remove succeeds: ~A" line)
             (check-equal 0 code "at exit 0")
             (ok (search "change=remove" line) "with its own receipt: ~A" line))
           ;; a conflicting payload under one id is refused by the real journal
           (multiple-value-bind (okp line code) (dep-remove k "acme/work/d" "acme/work/m"
                                                            :request "file-add")
             (ok (not okp) "a different payload under the same id is refused")
             (check-equal 1 code "at exit 1")
             (ok (search "reused with a different payload" line) "by name: ~A" line))
           ;; close and reopen: the edges replay from the durable record
           (close-file-journal (kernel-journal k))
           (let* ((state (make-seed-state *dep-seed*))
                  (j2 (open-file-journal path :initial-state-hash (root-digest state))))
             (unwind-protect
                  (let* ((k2 (make-kernel :state state :journal j2 :rev-base 1))
                         (replayed (progn (replay-journal j2 k2) (kernel-state k2))))
                    (check-equal '("acme/work/m") (node-deps replayed "acme/work/d")
                                 "a journal replay rebuilds the forward edges exactly")
                    (check-equal '("acme/work/d") (node-dependents replayed "acme/work/m")
                                 "and the reverse edge")
                    (check-equal '() (node-dependents replayed "acme/work/n")
                                 "and the removed one stays removed")
                    (check-equal 2 (length (remove-if-not
                                            (lambda (e) (eq :dep (getf e :verb)))
                                            (node-structure-log replayed "acme/work/d")))
                                 "and both audit records"))
               (close-file-journal j2))))
      (ignore-errors (close-file-journal (kernel-journal k))))))

(deftest "a-failed-durable-append-leaves-the-edge-alone" "docs/SPEC-WORK.md:307"
    "expected=refusal-before-change-and-nothing-written"
  ;; "the session appends it with its request id to the local recovery journal,
  ;; acknowledges only after the journal is durable, THEN applies it": a failed
  ;; append must leave the edge exactly where it was.
  (let* ((path (test-journal-path "dep-file-append-fail"))
         (state (make-seed-state *dep-seed*))
         (k (make-kernel :state state :rev-base 1
                         :journal (open-file-journal
                                   path :initial-state-hash (root-digest state)
                                        :fail-pre-write-on "boom-add"))))
    (unwind-protect
         (let ((deps (node-deps (kernel-state k) "acme/work/d")))
           ;; a pre-write failure signals rather than answering a line; either
           ;; way the contract is the same: nothing is applied.
           (handler-case
               (multiple-value-bind (okp line code)
                   (dep-add k "acme/work/d" "acme/work/m" :request "boom-add")
                 (declare (ignore line))
                 (ok (not okp) "a failed durable append refuses")
                 (ok (member code (list 1 2)) "at a refusing exit code, got ~A" code))
             (error (c)
               (ok (search "injected pre-write failure" (princ-to-string c))
                   "or signals the injected failure: ~A" c)))
           (check-equal deps (node-deps (kernel-state k) "acme/work/d")
                        "and the edge is exactly where it was")
           (check-equal '() (remove-if-not (lambda (e) (eq :dep (getf e :verb)))
                                           (node-structure-log (kernel-state k) "acme/work/d"))
                        "with no audit record left behind"))
      (ignore-errors (close-file-journal (kernel-journal k))))))
