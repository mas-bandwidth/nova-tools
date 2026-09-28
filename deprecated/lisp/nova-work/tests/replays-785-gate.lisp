;;;; replays-785-gate.lisp --- the needs-met predicate (nova-tools #785).
;;;;
;;;; The replays of rules 1 and 2 of *The dependency gate and the hand report*,
;;;; docs/SPEC-WORK.md:4780-4867, each cut from the replay text at
;;;; docs/SPEC-WORK.md:6905-6956. Nothing is wired to a verb here: this file
;;;; asserts the one predicate and its five reason tokens, and rule 3's gate at
;;;; the admission verbs is the next slice.
;;;;
;;;; The needs view (src/dependencies.lisp) is the read-time seam rule 1 needs:
;;;; `wstate` carries no evidence store and no verification cache, so the
;;;; session hands the gate the evidence records a node's standing `:to :done`
;;;; names, the verification session whose cache already holds the raw facts,
;;;; and the node's current generation. The gate fetches nothing.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; fixtures
;;; ------------------------------------------------------------------

(defparameter *gate-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n"))
    (:id "acme/work/n" :type :task     :parent "acme/work" :state :doing))
  "D, a dependent task, whose one need N is open.")

(defparameter *gate-two-needs-seed*
  '((:id "acme/work"    :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d"  :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/n1" "acme/work/n2"))
    (:id "acme/work/n1" :type :task     :parent "acme/work" :state :doing)
    (:id "acme/work/n2" :type :task     :parent "acme/work" :state :doing))
  "D with two needs, so a row names the first in `:deps` order and counts both.")

(defparameter *gate-container-seed*
  '((:id "acme/work"      :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/d"    :type :task     :parent "acme/work"    :state :todo
     :deps ("acme/work/f"))
    (:id "acme/work/f"    :type :feature  :parent "acme/work"    :state :todo)
    (:id "acme/work/f/m1" :type :task     :parent "acme/work/f"  :state :todo)
    (:id "acme/work/f/m2" :type :task     :parent "acme/work/f"  :state :todo))
  "A need that is a container of two required members (rule 2's container rule).")

(defparameter *gate-empty-container-seed*
  '((:id "acme/work"   :type :work-set :parent nil         :state :unknown)
    (:id "acme/work/d" :type :task     :parent "acme/work" :state :todo
     :deps ("acme/work/f"))
    (:id "acme/work/f" :type :feature  :parent "acme/work" :state :todo))
  "An empty container never settles, so it is `need-open` for as long as it is empty.")

(defun gate-kernel (&optional (seed *gate-seed*))
  (make-kernel :state (make-seed-state seed)
               :journal (make-ordering-journal) :rev-base 1))

(defun gate-doing (k node &key (request (format nil "doing-~A" node))
                                (stamp "2026-09-16T11:00:00Z"))
  "A node reaches `:done` only from `:doing` or `:review` (src/kernel.lisp:127),
so a fixture that settles a `:todo` node walks the edge first."
  (multiple-value-bind (okp line)
      (submit k (list :verb :state-to-doing :node node :by "rowan" :reason "started"
                      :request request :stamp stamp :clock :tool
                      :generation-owner "gen-1"))
    (ok okp "~A moves to doing: ~A" node line)
    line))

(defvar *gate-step* 0)

(defun gate-done (k node &key (request (format nil "done-~A" node))
                              (stamp "2026-09-16T12:00:00Z")
                              (evidence (list "ev-1")))
  (unless (member (node-state (kernel-state k) node) (list :doing :review))
    (gate-doing k node :request (format nil "doing-~A-~D" node (incf *gate-step*))))
  (multiple-value-bind (okp line)
      (submit k (list :verb :state-to-done :node node :by "rowan" :reason "merged"
                      :evidence evidence :request request :stamp stamp
                      :clock :tool :generation-owner "gen-1"))
    (ok okp "~A settles: ~A" node line)
    line))


(defun gate-reopen (k node &key (request (format nil "reopen-~A" node))
                                (stamp "2026-09-16T13:00:00Z"))
  (multiple-value-bind (okp line)
      (submit k (list :verb :event-reopen :node node :by "rowan" :reason "reverted"
                      :request request :stamp stamp :clock :tool
                      :generation-owner "gen-1"))
    (ok okp "~A reopens: ~A" node line)
    line))

(defun gate-cancel (k node &key (request (format nil "cancel-~A" node))
                                (stamp "2026-09-16T14:00:00Z"))
  (multiple-value-bind (okp line)
      (submit k (list :verb :event-cancel :node node :by "rowan" :reason "not wanted"
                      :request request :stamp stamp :clock :tool
                      :generation-owner "gen-1"))
    (ok okp "~A cancels: ~A" node line)
    line))

;;; A verification session whose cache holds exactly the facts the fixture
;;; names. `verify` fetches; the gate never does, so every fact here is stored
;;; by hand, as `verify` would have left it.

(defun gate-resolver (&optional (scheme "run") (command "run-reader"))
  (make-verification-resolver scheme command))

(defun gate-session (&key (resolvers (list (gate-resolver))) source-revision)
  (make-verification-session
   :cache (make-verification-cache)
   :resolvers resolvers
   :source-revision source-revision))

(defun gate-cache-holds (session pointer subject &key (resolver "run-reader")
                                                      (fact :holds)
                                                      (stamp "2026-09-16T12:00:00Z"))
  (verification-cache-store (verification-session-cache session)
                            pointer subject resolver fact stamp)
  session)

(defun gate-job-evidence (node &key (event-id "ev-1") (sha "abc123") (generation 1))
  "One `:job` evidence event: a `run:` pointer at the revision it was taken
against, which `verify-qualifies-p` admits only when the two agree."
  (make-verify-evidence event-id
                        :pointer (format nil "run:ci/9~A@~A" node sha)
                        :criterion :job
                        :subject node
                        :against sha
                        :generation generation
                        :node node))

;;; ------------------------------------------------------------------
;;; every-unmet-need-has-one-reason              SPEC-WORK.md:4820, :6915
;;; ------------------------------------------------------------------

(deftest "every-unmet-need-has-one-reason" "docs/SPEC-WORK.md:4820"
    "expected=five-tokens-one-per-row-of-rule-2"
  ;; row 1: in O, no :revive in its closed-index rows
  (let ((k (gate-kernel)))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
      (ok (not met) "an open need is not met")
      (check-equal :need-open reason "an open need reads need-open")))
  ;; row 2: in O, and its closed-index rows hold a :revive
  (let ((k (gate-kernel)))
    (gate-done k "acme/work/n")
    (gate-reopen k "acme/work/n")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
      (ok (not met) "a reopened need is not met")
      (check-equal :need-reverted reason "a settled-then-reopened need reads need-reverted")))
  ;; row 4: in C with disposition cancelled
  (let ((k (gate-kernel)))
    (gate-cancel k "acme/work/n")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
      (ok (not met) "a cancelled need is not met")
      (check-equal :need-closed-unaccepted reason
                   "a cancelled need reads need-closed-unaccepted")))
  ;; row 4: in C with disposition removed
  (let ((k (gate-kernel)))
    (node-remove k "acme/work/n")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
      (ok (not met) "a removed need is not met")
      (check-equal :need-closed-unaccepted reason
                   "a removed need reads need-closed-unaccepted")))
  ;; row 5: in C with disposition done, and short of rule 1
  (let* ((k (gate-kernel))
         (session (gate-session))
         (view (make-needs-view
                :session session
                :evidence (list (list "acme/work/n"
                                      (gate-job-evidence "acme/work/n"))))))
    (gate-done k "acme/work/n")
    (multiple-value-bind (met reason)
        (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "a done need on evidence no resolver answered is not met")
      (check-equal :need-unverified reason "it reads need-unverified")))
  ;; row 3: in C, and the closed-index page that says how cannot be read
  (let ((k (gate-kernel)))
    (gate-done k "acme/work/n")
    ;; the one row that says how this need settled is gone; incomplete and
    ;; invalid are two answers and neither is green (SPEC-WORK.md:4839).
    (let ((state (kernel-state k)))
      (setf (nova-work::wstate-rows state)
            (remove "acme/work/n" (nova-work::wstate-rows state)
                    :key (lambda (r) (getf r :node)) :test #'equal))
      (multiple-value-bind (met reason) (need-met-p state "acme/work/n")
        (ok (not met) "a need whose closed-index row cannot be read is not met")
        (check-equal :need-unavailable reason "it reads need-unavailable"))))
  ;; the row names the first unmet need in :deps order and counts them all
  (let ((k (gate-kernel *gate-two-needs-seed*)))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 2 unmet "both needs are counted")
      (check-string= "acme/work/n1" need "the row names the first in :deps order")
      (check-equal :need-open reason "and that need's one reason"))
    ;; a node's state= is untouched by any of this
    (check-equal :todo (node-state (kernel-state k) "acme/work/d")
                 "an unmet need is no state of the transition table"))
  ;; a node with no :deps is needs-met
  (let ((k (gate-kernel)))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/n")
      (check-equal 0 unmet "a node with no :deps has no unmet need")
      (check-string= "-" need "and names none")
      (ok (null reason) "and carries no reason token"))))

;;; ------------------------------------------------------------------
;;; the resolver each row names                  SPEC-WORK.md:4820
;;; ------------------------------------------------------------------

(deftest "an-unmet-need-names-the-resolver-its-row-names" "docs/SPEC-WORK.md:4820"
    "expected=open-names-the-need-holder-closed-unaccepted-names-the-dependent"
  ;; need-open: the need's live holder, else its responsible, else -
  (let ((k (gate-kernel)))
    (take-lease k "acme/work/n" "emma")
    (multiple-value-bind (unmet need reason resolver)
        (node-needs-status (kernel-state k) "acme/work/d")
      (declare (ignore unmet need))
      (check-equal :need-open reason "the need is open")
      (check-string= "emma" resolver "need-open names the need's live holder")))
  ;; need-closed-unaccepted: the dependent's responsible, else -
  (let* ((k (gate-kernel))
         (view (make-needs-view :responsible '(("acme/work/d" . "sam")
                                               ("acme/work/n" . "emma")))))
    (gate-cancel k "acme/work/n")
    (multiple-value-bind (unmet need reason resolver)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (declare (ignore unmet need))
      (check-equal :need-closed-unaccepted reason "the need is cancelled")
      (check-string= "sam" resolver
                     "need-closed-unaccepted names the dependent's responsible")))
  ;; need-unavailable: -
  (let ((k (gate-kernel)))
    (gate-done k "acme/work/n")
    (let ((state (kernel-state k)))
      (setf (nova-work::wstate-rows state)
            (remove "acme/work/n" (nova-work::wstate-rows state)
                    :key (lambda (r) (getf r :node)) :test #'equal))
      (multiple-value-bind (unmet need reason resolver)
          (node-needs-status state "acme/work/d")
        (declare (ignore unmet need))
        (check-equal :need-unavailable reason "the page cannot be read")
        (check-string= "-" resolver "need-unavailable names nobody")))))

;;; ------------------------------------------------------------------
;;; a-done-need-on-unverified-evidence-admits-nothing
;;;                                              SPEC-WORK.md:4780, :6905
;;; ------------------------------------------------------------------

(deftest "a-done-need-on-unverified-evidence-admits-nothing" "docs/SPEC-WORK.md:4780"
    "expected=recorded-is-not-verified-and-the-gate-never-fetches"
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n"))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "a done need whose resolver has not answered admits nothing")
      (check-equal :need-unverified reason "its reason is need-unverified"))
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 1 unmet "D counts one unmet need")
      (check-string= "acme/work/n" need "and names N")
      (check-equal :need-unverified reason "with the one token"))
    ;; the cache answers `holds`, as `verify --node N` would have left it, and
    ;; with no other command the same read is met.
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok met "the need is met once the cache holds the raw fact: ~A" reason))
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "and D reads unmet=0 with no command of its own")))
  ;; the same run with the resolver answering `absent` leaves the refusal standing
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n"))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n")
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n" :fact :absent)
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "a cached `absent` is not a qualifying fact")
      (check-equal :need-unverified reason "it reads need-unverified"))))

;;; ------------------------------------------------------------------
;;; stale-evidence-does-not-unmeet-a-need        SPEC-WORK.md:4790, :6912
;;; ------------------------------------------------------------------

(deftest "stale-evidence-does-not-unmeet-a-need" "docs/SPEC-WORK.md:4790"
    "expected=the-gate-leaves-out-the-against-versus-source-comparison"
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n" :sha "abc123"))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n")
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n")
    (ok (need-met-p (kernel-state k) "acme/work/n" :view view) "N is met")
    ;; `source --to <sha>` on N's repository work set: the evidence reads stale
    ;; and `verify` counts it so, while the gate asks the historical-delivery
    ;; question and is unmoved.
    (setf (verification-session-source-revision session) "def456")
    (ok (nova-work::verify-evidence-stale-p session evidence) "the evidence now reads stale")
    (ok (need-met-p (kernel-state k) "acme/work/n" :view view)
        "stale does not unmeet a need")
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "and D still reads unmet=0"))))

;;; ------------------------------------------------------------------
;;; an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less
;;;                                              SPEC-WORK.md:4799, :6952
;;; ------------------------------------------------------------------

(defun gate-attested-evidence (node &key (event-id "ev-att") (sha "abc123")
                                         (reviewer "emma") (generation 1))
  (let ((pointer (format nil "attest:~A@~A" node sha)))
    (make-verify-evidence event-id
                          :pointer pointer
                          :criterion :attested
                          :subject node
                          :against sha
                          :generation generation
                          :attestation (list :reviewer reviewer
                                             :result-pointer pointer
                                             :revision sha)
                          :node node)))

(deftest "an-attested-only-need-is-met-by-its-attestation-and-by-nothing-less"
    "docs/SPEC-WORK.md:4799"
    "expected=attested-needs-a-cached-result-a-current-generation-and-no-typed-name"
  (let* ((k (gate-kernel))
         (evidence (gate-attested-evidence "acme/work/n"))
         (session (gate-session :resolvers (list (gate-resolver "attest" "attest-reader"))))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence))
                                :generations '(("acme/work/n" . 1)))))
    ;; the standing done must NAME the attestation event, or the correspondence
    ;; rule 1 asks for cannot hold (Stella's HOLD on df714493).
    (gate-done k "acme/work/n" :evidence (list "ev-att"))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "an attestation whose result pointer is unanswered meets nothing")
      (check-equal :need-unverified reason "it reads need-unverified"))
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n"
                      :resolver "attest-reader")
    (ok (need-met-p (kernel-state k) "acme/work/n" :view view)
        "the verified result meets the attested-only need")
    ;; after a `correct`, the generation moves and the older attestation
    ;; qualifies nothing.
    (setf (needs-view-generations view) '(("acme/work/n" . 2)))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "an attestation of an older generation meets nothing")
      (check-equal :need-unverified reason "it reads need-unverified")))
  ;; the same attest typed under another --as meets it just the same: --as is
  ;; caller text and the tool authenticates nobody (SPEC-WORK.md:4802).
  (let* ((k (gate-kernel))
         (evidence (gate-attested-evidence "acme/work/n" :reviewer "somebody-else"))
         (session (gate-session :resolvers (list (gate-resolver "attest" "attest-reader"))))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence)))))
    (gate-done k "acme/work/n" :evidence (list "ev-att"))
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n"
                      :resolver "attest-reader")
    (ok (need-met-p (kernel-state k) "acme/work/n" :view view)
        "a reviewer's typed name changes nothing")))

;;; ------------------------------------------------------------------
;;; a-container-need-is-met-with-its-members     SPEC-WORK.md:4849, :6920
;;; ------------------------------------------------------------------

(defun gate-container-view (session &rest members)
  (make-needs-view
   :session session
   :evidence (loop for (id . evidence) in members collect (list id evidence))))

(deftest "a-container-need-is-met-with-its-members" "docs/SPEC-WORK.md:4849"
    "expected=a-container-has-no-evidence-of-its-own-and-is-met-with-its-members"
  ;; one member done and verified, one open: need-open
  (let* ((k (gate-kernel *gate-container-seed*))
         (m1-ev (gate-job-evidence "acme/work/f/m1"))
         (session (gate-session))
         (view (gate-container-view session (cons "acme/work/f/m1" m1-ev))))
    (gate-done k "acme/work/f/m1")
    (gate-cache-holds session (verify-evidence-pointer m1-ev) "acme/work/f/m1")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/f" :view view)
      (ok (not met) "a container with an open member is not met")
      (check-equal :need-open reason "and reads its member's one reason")))
  ;; both members done, one on unverified evidence: need-unverified
  (let* ((k (gate-kernel *gate-container-seed*))
         (m1-ev (gate-job-evidence "acme/work/f/m1"))
         (m2-ev (gate-job-evidence "acme/work/f/m2" :event-id "ev-2"))
         (session (gate-session))
         (view (gate-container-view session
                                    (cons "acme/work/f/m1" m1-ev)
                                    (cons "acme/work/f/m2" m2-ev))))
    (gate-done k "acme/work/f/m1" :evidence (list "ev-1"))
    (gate-done k "acme/work/f/m2" :evidence (list "ev-2"))
    (gate-cache-holds session (verify-evidence-pointer m1-ev) "acme/work/f/m1")
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/f" :view view)
      (ok (not met) "a done container with a member short of rule 1 is not met")
      (check-equal :need-unverified reason "and reads need-unverified"))
    ;; both verified: met, and the dependent reads unmet=0
    (gate-cache-holds session (verify-evidence-pointer m2-ev) "acme/work/f/m2")
    (ok (need-met-p (kernel-state k) "acme/work/f" :view view)
        "a container whose every required member is met is met")
    (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
      (check-equal 0 unmet "and its dependent reads unmet=0")))
  ;; an empty container never settles, so it is need-open for as long as it is empty
  (let ((k (gate-kernel *gate-empty-container-seed*)))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/f")
      (ok (not met) "an empty container is never met")
      (check-equal :need-open reason "and reads need-open"))))

;;; ------------------------------------------------------------------
;;; a-removed-or-corrected-need-is-unmet         SPEC-WORK.md:4831, :6947
;;; ------------------------------------------------------------------

(deftest "a-removed-or-corrected-need-is-unmet" "docs/SPEC-WORK.md:4831"
    "expected=removed-is-closed-unaccepted-for-good-and-corrected-is-unverified"
  ;; a removed need is a closed need like the other two, and never becomes met
  (let ((k (gate-kernel)))
    (node-remove k "acme/work/n")
    (multiple-value-bind (unmet need reason)
        (node-needs-status (kernel-state k) "acme/work/d")
      (check-equal 1 unmet "the removed need is counted")
      (check-string= "acme/work/n" need "and named")
      (check-equal :need-closed-unaccepted reason "and reads need-closed-unaccepted"))
    ;; and never becomes met: a second read of the same closed row says the same
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
      (ok (not met) "a removed need is never met")
      (check-equal :need-closed-unaccepted reason "for good")))
  ;; a done need that is then corrected is unmet until it is done again at the
  ;; new generation
  (let* ((k (gate-kernel))
         (evidence (gate-job-evidence "acme/work/n" :generation 1))
         (session (gate-session))
         (view (make-needs-view :session session
                                :evidence (list (list "acme/work/n" evidence))
                                :generations '(("acme/work/n" . 1)))))
    (gate-done k "acme/work/n")
    (gate-cache-holds session (verify-evidence-pointer evidence) "acme/work/n")
    (ok (need-met-p (kernel-state k) "acme/work/n" :view view) "N is met")
    ;; `correct --node N` bumps the generation; the evidence the standing done
    ;; names is of the older one and qualifies nothing.
    (setf (needs-view-generations view) '(("acme/work/n" . 2)))
    (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
      (ok (not met) "a corrected need is unmet")
      (check-equal :need-unverified reason "and reads need-unverified"))
    ;; reopened, evidenced and done again at the new generation: met
    (gate-reopen k "acme/work/n")
    (let ((fresh (gate-job-evidence "acme/work/n" :event-id "ev-2" :sha "def456"
                                                  :generation 2)))
      (setf (needs-view-evidence view) (list (list "acme/work/n" fresh)))
      (gate-done k "acme/work/n" :request "done-n-2" :stamp "2026-09-16T15:00:00Z"
                                 :evidence '("ev-2"))
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok (not met) "still unmet until the new evidence is verified: ~A" reason))
      (gate-cache-holds session (verify-evidence-pointer fresh) "acme/work/n")
      (ok (need-met-p (kernel-state k) "acme/work/n" :view view)
          "met again at the new generation")
      (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
        (check-equal 0 unmet "and D reads unmet=0")))))

;;; ------------------------------------------------------------------
;;; the session-built view, used by every slice above this one
;;; ------------------------------------------------------------------

(defun session-needs-view (k &key generations)
  "A needs view built the way a session builds one, and never by hand.

For every node whose standing `:to :done` names evidence, one VERIFY-EVIDENCE
record PER NAMED ID, bound to that node and its generation, with the raw fact
already in the cache -- which is what `verify` leaves behind. Nothing is
invented: the ids come from the tree, by `standing-done-evidence`.

Stella's read of #1584 asks that the admission and restart tests exercise the
session-built view rather than a complete one supplied by hand, because a view
supplied by hand is the very thing that hid the defect."
  (let* ((state (kernel-state k))
         (session (gate-session))
         (evidence '()))
    (dolist (id (state-node-ids state))
      (multiple-value-bind (ids standing) (standing-done-evidence state id)
        (when (and standing ids)
          (let ((records
                  (loop for event-id in ids
                        collect (let* ((generation
                                         (or (cdr (assoc id generations :test #'equal))
                                             *need-default-generation*))
                                       (sha (format nil "sha-~A-~A" id event-id))
                                       (record (make-verify-evidence
                                                event-id
                                                :pointer (format nil "run:ci/~A@~A" id sha)
                                                :criterion :job
                                                :subject id
                                                :against sha
                                                :generation generation
                                                :node id)))
                                  (gate-cache-holds session
                                                    (verify-evidence-pointer record) id)
                                  record))))
            (push (cons id records) evidence)))))
    (make-needs-view :session session :evidence evidence :generations generations)))

;;; ------------------------------------------------------------------
;;; a-done-need-is-unverified-without-complete-proof
;;;    Stella's HOLD on #1584 at df714493: `%need-evidence-verified-p` ignored
;;;    STATE and quantified only over the records the view supplied, so
;;;    `(every ... NIL)` was vacuously true. Four cases returned met.
;;;                                              SPEC-WORK.md:4780
;;; ------------------------------------------------------------------

(deftest "a-done-need-is-unverified-without-complete-proof" "docs/SPEC-WORK.md:4780"
    "expected=every-id-the-standing-done-names-is-bound-and-verified-or-the-need-is-unverified"
  ;; N settles naming TWO evidence events.
  (flet ((settled-n ()
           (let ((k (gate-kernel)))
             (gate-done k "acme/work/n" :evidence '("ev-1" "ev-2"))
             k)))
    ;; 1. no view at all: nothing is resolved, so nothing is proved
    (let ((k (settled-n)))
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n")
        (ok (not met) "a missing view proves nothing")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 2. an empty view: the same
    (let ((k (settled-n)))
      (multiple-value-bind (met reason)
          (need-met-p (kernel-state k) "acme/work/n" :view (make-needs-view))
        (ok (not met) "an empty view proves nothing")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 3. PARTIAL: ev-1 verified, ev-2 named by the done and absent from the view
    (let* ((k (settled-n))
           (session (gate-session))
           (one (gate-job-evidence "acme/work/n" :event-id "ev-1"))
           (view (make-needs-view :session session
                                  :evidence (list (list "acme/work/n" one)))))
      (gate-cache-holds session (verify-evidence-pointer one) "acme/work/n")
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok (not met) "one verified id of two proves nothing: this is not a nonempty check")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 4. MISMATCHED: a verified record whose id the standing done never named
    (let* ((k (settled-n))
           (session (gate-session))
           (wrong (gate-job-evidence "acme/work/n" :event-id "not-named-by-done"))
           (view (make-needs-view :session session
                                  :evidence (list (list "acme/work/n" wrong)))))
      (gate-cache-holds session (verify-evidence-pointer wrong) "acme/work/n")
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok (not met) "a verified id the standing done never named proves nothing")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 5. a record bound to ANOTHER node, under this node's id
    (let* ((k (settled-n))
           (session (gate-session))
           (other (make-verify-evidence "ev-1"
                                        :pointer "run:ci/elsewhere@sha-x"
                                        :criterion :job :subject "acme/work/d"
                                        :against "sha-x" :generation 1
                                        :node "acme/work/d"))
           (view (make-needs-view :session session
                                  :evidence (list (list "acme/work/n" other)))))
      (gate-cache-holds session (verify-evidence-pointer other) "acme/work/d")
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok (not met) "a record bound to another node is no proof for this one")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 6. COMPLETE, and built the way a session builds it: met
    (let* ((k (settled-n))
           (view (session-needs-view k)))
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok met "every named id bound and verified: the need is met (~A)" reason))
      (multiple-value-bind (unmet) (node-needs-status (kernel-state k) "acme/work/d" :view view)
        (check-equal 0 unmet "and its dependent reads unmet=0")))
    ;; 7. complete, then the node's generation moves: unverified again
    (let* ((k (settled-n))
           (view (session-needs-view k)))
      (ok (need-met-p (kernel-state k) "acme/work/n" :view view) "met at generation 1")
      (setf (needs-view-generations view) '(("acme/work/n" . 2)))
      (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
        (ok (not met) "a corrected node's older evidence proves nothing")
        (check-equal :need-unverified reason "and the need reads need-unverified")))
    ;; 8. a node with NO standing done cannot be proved by this route
    (let* ((k (gate-kernel))
           (view (session-needs-view k)))
       (multiple-value-bind (met reason) (need-met-p (kernel-state k) "acme/work/n" :view view)
         (ok (not met) "an open node is not met")
         (check-equal :need-open reason "by rule 2 row 1, before evidence is read at all")))))

;;; ------------------------------------------------------------------
;;; TestE01F04RepresentWorkSetFeatureRoadmap     SPEC-WORK.md:888
;;; ------------------------------------------------------------------

(defparameter *kinds-seed*
  '((:id "ws"     :type :work-set :parent nil    :state :unknown)
    (:id "ws/f"   :type :feature  :parent "ws"   :state :unknown)
    (:id "ws/f/t" :type :task     :parent "ws/f" :state :todo))
  "One work-set with a feature and a leaf task beneath it, to exercise the
three node kinds the criterion names beside the roadmap, the lease and the
event.")

(deftest "TestE01F04RepresentWorkSetFeatureRoadmap" "docs/SPEC-WORK.md:888"
    "expected=work-set-feature-and-task-are-node-types;roadmap-is-a-node-type-made-by-its-one-creator;a-lease-names-a-holder-and-is-a-lease-event-kind;an-event-carries-a-kind"
  (let ((k (gate-kernel *kinds-seed*)))
    ;; work-set, feature and task are node types (SPEC-WORK.md:891, :894, :930).
    (check-equal :work-set (node-type (kernel-state k) "ws") "a work-set is a node type")
    (check-equal :feature (node-type (kernel-state k) "ws/f") "a feature is a node type")
    (check-equal :task (node-type (kernel-state k) "ws/f/t") "a task is a node type")
    ;; roadmap is a node type, and only its one creator makes one (:891-903).
    (multiple-value-bind (okp line)
        (roadmap-create k :id "ws/r" :parent "ws" :title "plan")
      (ok okp "roadmap create refused: ~A" line)
      (check-equal :roadmap (node-type (kernel-state k) "ws/r") "a roadmap is a node type"))
    ;; lease: one live holder named by the take, and a :lease event kind (:955).
    (check-equal "emma" (take-lease k "ws/f/t" "emma") "a lease names its holder")
    (check-equal "emma" (node-holder (kernel-state k) "ws/f/t") "the node is held")
    (check-equal '(:change :holder) (kind-fields :lease)
                 "lease is an event kind with its own field list")
    ;; event: the log, and every event carries a :kind (:956). The take above
    ;; is one :lease event in the history; read the newest record's events.
    (let ((record (first (state-history (kernel-state k)))))
      (ok (getf record :events) "the take wrote no event record")
      (check-equal :lease (getf (first (getf record :events)) :kind)
                   "an event carries its kind"))))

;;; ------------------------------------------------------------------
;;; TestE02F02VerifyGenerationFencingUnderPartitions
;;;
;;; Criterion E02-F02-04 (docs/roadmaps/nova-work.sexp): "Verify generation
;;; fencing under partitions and clock skew; reject unsafe takeover rather than
;;; relying on PID locks alone".
;;;   docs/SPEC-WORK.md:244-246 — the partitioned owner fences at `until` with
;;;   no network at all, and a takeover is refused until `until` plus `--skew`
;;;   has passed on the taker's clock;
;;;   docs/SPEC-WORK.md:7671 — a local PID/file lock alone only protects one
;;;   host, so the fence is the lease's generation and clock, never a pid.
;;; ------------------------------------------------------------------

(deftest "TestE02F02VerifyGenerationFencingUnderPartitions"
    "docs/SPEC-WORK.md:244-246"
    "expected=live-lease-refuses-a-competing-taker-naming-holder-generation-and-until;takeover-still-refused-within-until-plus-skew;an-expired-lease-is-taken-at-the-next-generation-with-a-fresh-token"
  ;; A live lease -- the old owner partitioned away, its process unreachable and
  ;; its PID useless as any signal -- still refuses a competing taker: the
  ;; claim fences and names holder, generation and `until`. The fence is the
  ;; lease's generation and clock, not a PID lock two processes could both hold.
  (let ((live (make-ownership-record :owner "emma" :generation 3 :token "tok-emma"
                                     :stamp "2026-09-14T11:59:00Z"
                                     :until "2026-09-14T12:01:00Z" :bench "bench-a")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim live "stella" :now "2026-09-14T12:00:30Z"
                                  :every "30s" :skew "5s" :token "tok-stella"
                                  :my-bench "bench-b")
      (declare (ignore record))
      (check-equal :fenced action "a live lease was taken by a competing taker")
      (check-equal 1 exit-code "the competing take did not refuse")
      (ok (search "SESSION FAIL" line) "the refusal is not a SESSION FAIL: ~A" line)
      (ok (search "owner=emma" line) "the refusal does not name the holder: ~A" line)
      (ok (search "generation=3" line) "the refusal does not name the held generation: ~A" line)
      (ok (search "held" line) "the refusal does not say held: ~A" line)))
  ;; Clock skew: `until` has passed but `until + --skew` has not, so the takeover
  ;; stays refused on the taker's clock (SPEC-WORK.md:245-246).
  (let ((near (make-ownership-record :owner "emma" :generation 3 :token "tok-emma"
                                     :stamp "2026-09-14T11:59:00Z"
                                     :until "2026-09-14T12:01:00Z" :bench "bench-a")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim near "stella" :now "2026-09-14T12:01:03Z"
                                  :every "30s" :skew "5s" :token "tok-stella"
                                  :my-bench "bench-b")
      (declare (ignore record))
      (check-equal :fenced action "a takeover within --skew was admitted")
      (check-equal 1 exit-code "the within-skew takeover did not refuse")
      (ok (search "held" line) "the within-skew refusal does not say held: ~A" line)))
  ;; Partition with no network at all: once `until + --skew` is in the past on
  ;; the taker's clock the take succeeds, and the generation is fenced forward
  ;; to the next one with a fresh token -- a take, never a resume (SPEC-WORK.md:244-246).
  (let ((expired (make-ownership-record :owner "emma" :generation 3 :token "tok-emma"
                                        :stamp "2026-09-14T11:59:00Z"
                                        :until "2026-09-14T12:01:00Z" :bench "bench-a")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim expired "stella" :now "2026-09-14T12:01:30Z"
                                  :every "30s" :skew "5s" :token "tok-stella"
                                  :my-bench "bench-b")
      (check-equal :take action "an expired lease was not taken after until+skew")
      (check-equal 0 exit-code "the take is not green")
      (check-equal 4 (owner-generation record) "the generation was not fenced to the next one")
      (check-string= "stella" (owner-owner record) "the taker does not own the new generation")
      (check-string= "tok-stella" (owner-token record) "a take did not mint a fresh token")
      (check-string= "bench-b" (owner-bench record) "the taker's bench was not recorded")
      (ok (search "generation=4" line) "SESSION OK does not name the new generation: ~A" line))))
