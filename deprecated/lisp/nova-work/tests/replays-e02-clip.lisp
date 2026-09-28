;;;; replays-e02-clip.lisp --- the clip transport as one long operation over a
;;;; real git remote (AUDIT row E02.6, SPEC-WORK.md:2744-2750, :5955-5957,
;;;; :6091-6095).
;;;;
;;;;   clip-is-one-long-operation-over-a-real-remote     :2744-2750, :5955
;;;;   a-clip-whose-base-moved-is-raced-and-pushes-nothing  :2746-2748, :5956
;;;;   session-stop-is-the-one-caller-that-waits-for-its-own-clip  :2748-2750, :6091-6095
;;;;   the-tip-of-an-empty-remote-is-genesis             :2746-2748
;;;;   a-clip-transport-that-cannot-push-is-clip-fail    :5957
;;;;
;;;; The remote is a real bare repository made with `git init --bare`. Every
;;;; assertion about what was published reads that repository back with git,
;;;; not through the engine, and the raced case has a SECOND writer move the ref
;;;; first, which is the only way to show that the base predicate is the
;;;; remote's compare-and-swap and not a check this process made and then acted
;;;; on.

(in-package #:nova-work/tests)

(defun clip-fixture (name)
  "A bare remote, a durable operation registry and one accepted clip operation.
Answers (values REGISTRY REMOTE ROOT)."
  (let* ((root (test-session-root name))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         (remote (open-git-clip-remote (format nil "~A/remote.git" root))))
    (operation-accept registry :id "op-clip" :kind "clip" :request "req-clip"
                               :author "rowan" :stamp "2026-09-19T12:00:00Z")
    (values registry remote root)))

;;; ------------------------------------------------------------------
;;; the-tip-of-an-empty-remote-is-genesis                :2746-2748
;;; ------------------------------------------------------------------

(deftest "the-tip-of-an-empty-remote-is-genesis" "docs/SPEC-WORK.md:2746-2748"
    "expected=an-empty-bare-repository-answers-genesis;the-first-push-creates-the-ref"
  (multiple-value-bind (registry remote) (clip-fixture "clip-genesis")
    (unwind-protect
         (progn
           (check-string= "genesis" (clip-remote-tip remote)
                          "an empty remote's tip is the word a first clip's base predicate reads")
           (check-equal '() (git-clip-remote-history remote) "and it has no history")
           (let ((sha (clip-remote-push remote "genesis" "digest-1")))
             (ok sha "the first push lands")
             (check-string= sha (clip-remote-tip remote) "and becomes the tip")
             (check-equal (list sha) (git-clip-remote-history remote)
                          "the ref the remote clips to now holds exactly that commit")
             (check-string= "nova-work clip digest-1" (git-clip-commit-message remote sha)
                            "and the commit names the clip it published")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; clip-is-one-long-operation-over-a-real-remote        :2744-2750, :5955
;;; ------------------------------------------------------------------

(deftest "clip-is-one-long-operation-over-a-real-remote" "docs/SPEC-WORK.md:2744-2750,5955"
    "expected=clip-exits-at-once;the-CLIP-OK-line-arrives-through-operation-wait;the-real-ref-moved"
  (multiple-value-bind (registry remote root) (clip-fixture "clip-long")
    (declare (ignore root))
    (unwind-protect
         (let ((before (clip-remote-tip remote))
               (start (get-internal-real-time)))
           ;; `clip` has already printed OPERATION OK and exited. Nothing is
           ;; pushed yet: the transport has not run.
           (check-string= "genesis" before "the transport has not run yet")
           (let ((transport (sb-thread:make-thread
                             (lambda ()
                               (sleep 0.20)
                               (run-clip-transport registry remote
                                                   :id "op-clip" :session "/s/one"
                                                   :boundary "req-boundary-1"
                                                   :events 3 :base "genesis"
                                                   :commit "digest-1" :revision 12
                                                   :stamp "2026-09-19T12:00:01Z"))
                             :name "nova-work-test-clip")))
             (multiple-value-bind (rows state cursor line code)
                 (durable-operation-wait registry "op-clip" :timeout "30s" :after 0)
               (declare (ignore rows cursor))
               (let ((took (elapsed-seconds start)))
                 (sb-thread:join-thread transport)
                 (check-equal nil line "the wait is answered, not refused")
                 (check-equal 0 code "and exits 0")
                 (check-equal :done state "the transport settled")
                 (ok (>= took 0.19)
                     "the caller learned of it by waiting, not synchronously (took ~,3Fs)" took)
                 (ok (< took 10) "and woke on the settlement, not the timeout (took ~,3Fs)" took))))
           ;; The CLIP OK line, carrying operation=<id>, is what the wait prints.
           (let ((line (registry-operation-result registry "op-clip")))
             (check-string= "CLIP OK session=/s/one operation=op-clip boundary=req-boundary-1 events=3 base=genesis commit=digest-1 pushed=12 attempts=1 emitted=0"
                            line "the settled line is the one the output grammar fixes"))
           ;; And the real repository moved.
           (let ((history (git-clip-remote-history remote)))
             (check-equal 1 (length history) "the bare repository holds one commit")
             (check-string= (first history) (clip-remote-tip remote)
                            "which is now the tip")
             (check-string= "nova-work clip digest-1"
                            (git-clip-commit-message remote (first history))
                            "and it names the clip that published it")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-clip-whose-base-moved-is-raced-and-pushes-nothing  :2746-2748, :5956
;;; ------------------------------------------------------------------

(deftest "a-clip-whose-base-moved-is-raced-and-pushes-nothing" "docs/SPEC-WORK.md:2746-2748,5956"
    "expected=CLIP-RACED-through-the-wait;the-other-writers-commit-is-still-the-tip;nothing-of-ours-published"
  (multiple-value-bind (registry remote) (clip-fixture "clip-raced")
    (unwind-protect
         (let ((base (clip-remote-tip remote)))
           ;; A second writer moves the ref under us, between the clip being
           ;; accepted and its transport running.
           (let ((theirs (clip-remote-push remote base "digest-theirs")))
             (ok theirs "the other writer's push lands first")
             (let ((line (run-clip-transport registry remote
                                             :id "op-clip" :session "/s/two"
                                             :boundary "req-boundary-1"
                                             :events 3 :base base
                                             :commit "digest-ours" :revision 12)))
               (check-string= (format nil "CLIP RACED session=/s/two operation=op-clip boundary=req-boundary-1 generation=1 expected=~A found=~A"
                                      (clip-sha12 base) (clip-sha12 theirs))
                              line "the race is the line the output grammar fixes")
               ;; It arrives the same way: through the wait.
               (multiple-value-bind (rows state) (durable-operation-wait
                                                  registry "op-clip"
                                                  :timeout "5s" :after 0)
                 (declare (ignore rows))
                 (check-equal :failed state "a raced clip is settled, not left running")
                 (check-string= line (registry-operation-result registry "op-clip")
                                "and the wait answers the same line"))
               ;; Nothing of ours was published.
               (check-string= theirs (clip-remote-tip remote)
                              "the other writer's commit is still the tip")
               (check-equal 1 (length (git-clip-remote-history remote))
                            "and the ref holds only their commit")
               (dolist (sha (git-clip-remote-history remote))
                 (ok (null (search "digest-ours" (git-clip-commit-message remote sha)))
                     "no commit of ours reached the clipped ref")))))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; session-stop-is-the-one-caller-that-waits-for-its-own-clip
;;;                                        :2748-2750, :6091-6095
;;; ------------------------------------------------------------------

(deftest "session-stop-is-the-one-caller-that-waits-for-its-own-clip" "docs/SPEC-WORK.md:2748-2750,6091-6095"
    "expected=stop-reaches-CLIP-OK-by-waiting-on-its-operation-id;a-git-timeout-leaves-the-transport-running"
  (multiple-value-bind (registry remote) (clip-fixture "clip-stop")
    (unwind-protect
         (progn
           ;; A stop whose git-timeout passes before the transport settles gets
           ;; the wait's note and leaves the clip running: there is no second
           ;; synchronous clip path for it to fall back on.
           (multiple-value-bind (line state code)
               (session-stop-wait-for-clip registry "op-clip" :git-timeout "200ms")
             (check-string= "OPERATION NOTE waiting id=op-clip timeout=200ms after=0" line
                            "the stop's own --git-timeout is the wait's bound")
             (check-equal :timeout state "and it did not settle inside it")
             (check-equal 0 code "a git timeout is not a refusal"))
           (check-equal :running
                        (getf (cdr (assoc "op-clip" (operation-registry-operations registry)
                                          :test #'equal))
                              :state)
                        "the transport is still running after the stop's timeout")
           ;; Now the transport settles and a second stop reaches the CLIP OK
           ;; line by waiting on the same operation id.
           (let ((transport (sb-thread:make-thread
                             (lambda ()
                               (sleep 0.20)
                               (run-clip-transport registry remote
                                                   :id "op-clip" :session "/s/three"
                                                   :boundary "req-boundary-1"
                                                   :events 1 :base "genesis"
                                                   :commit "digest-stop" :revision 4))
                             :name "nova-work-test-clip-stop")))
             (multiple-value-bind (line state code)
                 (session-stop-wait-for-clip registry "op-clip" :git-timeout "30s")
               (sb-thread:join-thread transport)
               (check-equal :done state "the stop waited until its own clip settled")
               (check-equal 0 code "and exits 0")
               (check-string= "CLIP OK session=/s/three operation=op-clip boundary=req-boundary-1 events=1 base=genesis commit=digest-stop pushed=4 attempts=1 emitted=0"
                              line "stop prints its clip's CLIP OK line"))))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; a-clip-transport-that-cannot-push-is-clip-fail       :5957
;;; ------------------------------------------------------------------

(deftest "a-clip-transport-that-cannot-push-is-clip-fail" "docs/SPEC-WORK.md:5957"
    "expected=a-transport-failure-that-is-not-a-race-is-CLIP-FAIL;nothing-is-published"
  (let* ((root (test-session-root "clip-fail"))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         ;; A ref name git refuses: the push cannot land and the tip cannot
         ;; move, so this is a failure and not a race.
         (remote (open-git-clip-remote (format nil "~A/remote.git" root)
                                       :ref "refs/heads/bad name")))
    (unwind-protect
         (progn
           (operation-accept registry :id "op-clip" :kind "clip" :request "req-clip"
                                      :author "rowan" :stamp "2026-09-19T12:00:00Z")
           (let ((line (run-clip-transport registry remote
                                           :id "op-clip" :session "/s/four"
                                           :boundary "req-boundary-1"
                                           :events 2 :base "genesis"
                                           :commit "digest-1" :revision 5)))
             (ok (search "CLIP FAIL session=/s/four operation=op-clip boundary=req-boundary-1 events=2 base=genesis pushed=- attempts=1: " line)
                 "the failure is the line the output grammar fixes: ~A" line)
             (ok (null (search "CLIP RACED" line))
                 "a failure that moved no tip is not reported as a race"))
           (check-equal '() (git-clip-remote-history remote) "nothing is published")
           (multiple-value-bind (rows state) (durable-operation-wait registry "op-clip"
                                                                     :timeout "5s" :after 0)
             (declare (ignore rows))
             (check-equal :failed state "the failure arrives through the wait like the others")))
      (close-durable-operation-registry registry))))

;;; ------------------------------------------------------------------
;;; The clip operation path end to end: `clip` (CLIP-REQUEST) accepts the
;;; operation on the scheduler and launches the git transport on its own
;;; worker; `operation wait` (OPERATION-WAIT) and `session stop` (SESSION-STOP,
;;; SESSION-STOP-LIFECYCLE) learn the outcome only by waiting on it.
;;;
;;; The remote is a real bare repository whose push is held behind a gate the
;;; test opens, so "clip returned before anything was pushed" is observed, not
;;; inferred from timing.
;;; ------------------------------------------------------------------

(defstruct (gated-git-clip-remote (:include git-clip-remote)
                                  (:constructor %make-gated-git-clip-remote))
  (gate (sb-thread:make-semaphore :count 0)))

(defmethod clip-remote-push :before ((remote gated-git-clip-remote) base commit)
  (declare (ignore base commit))
  (sb-thread:wait-on-semaphore (gated-git-clip-remote-gate remote)))

(defun gated-clip-remote (root)
  (let ((plain (open-git-clip-remote (format nil "~A/remote.git" root))))
    (%make-gated-git-clip-remote :directory (git-clip-remote-directory plain)
                                 :ref (git-clip-remote-ref plain))))

(defun open-clip-gate (remote)
  (sb-thread:signal-semaphore (gated-git-clip-remote-gate remote)))

(defparameter *clip-path-events*
  '((:request "req-e1" :id "e1") (:request "req-e2" :id "e2")))

(deftest "clip-request-launches-the-git-transport-and-only-the-wait-learns-it" "docs/SPEC-WORK.md:2744-2750,5955"
    "expected=clip-returns-OPERATION-OK-before-any-push;a-short-wait-is-a-NOTE-and-leaves-it-running;the-wait-prints-CLIP-OK;the-real-ref-moved"
  (let* ((root (test-session-root "clip-path"))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         (remote (gated-clip-remote root))
         (session (make-work-session :path "/s/path" :events *clip-path-events*))
         (commit (clip-commit *clip-path-events* 2)))
    (unwind-protect
         (multiple-value-bind (after op ack)
             (clip-request session :id "op-clip-path" :request "req-clip-path"
                                   :remote remote :registry registry
                                   :author "rowan" :stamp "2026-09-23T16:00:00Z")
           (check-string= "OPERATION OK id=op-clip-path op=clip state=queued" ack
                          "clip prints OPERATION OK at once")
           (check-equal :running (registry-operation-state registry "op-clip-path")
                        "the operation is accepted on the scheduler and running")
           (check-equal '() (git-clip-remote-history remote)
                        "and clip returned without pushing anything")
           ;; A wait shorter than the transport is a NOTE, and changes nothing.
           (check-string= "OPERATION NOTE waiting id=op-clip-path timeout=200ms after=0"
                          (nth-value 1 (operation-wait after "op-clip-path" :timeout "200ms"))
                          "a wait that times out answers the NOTE line")
           (check-equal :running (registry-operation-state registry "op-clip-path")
                        "and leaves the transport running")
           (check-equal '() (git-clip-remote-history remote) "still nothing pushed")
           ;; The transport pushes on its own worker; the wait learns of it.
           (open-clip-gate remote)
           (let ((line (nth-value 1 (operation-wait after "op-clip-path" :timeout "30s"))))
             (check-string= (format nil "CLIP OK session=/s/path operation=op-clip-path boundary=req-e2 events=2 base=genesis commit=~A pushed=2 attempts=1 emitted=0"
                                    commit)
                            line "the wait prints the CLIP OK line the transport settled")
             (check-equal :done (operation-state op) "the operation is done")
             (check-equal :done (getf (operation-status after "op-clip-path") :state)
                          "and status reads it done")
             (check-string= line (nth-value 1 (operation-wait after "op-clip-path"))
                            "a settled wait answers the recorded line")
             (let ((history (git-clip-remote-history remote)))
               (check-equal 1 (length history) "the bare repository holds one commit")
               (check-string= (format nil "nova-work clip ~A" commit)
                              (git-clip-commit-message remote (first history))
                              "and it is this clip's commit"))))
      (close-durable-operation-registry registry))))

(deftest "clip-request-over-a-moved-git-tip-is-raced-through-the-wait" "docs/SPEC-WORK.md:2746-2748,5956"
    "expected=the-launched-transport-refuses-by-CAS;the-wait-prints-CLIP-RACED;the-other-writer-keeps-the-tip"
  (let* ((root (test-session-root "clip-path-raced"))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         (remote (open-git-clip-remote (format nil "~A/remote.git" root)))
         (session (make-work-session :path "/s/raced" :events *clip-path-events*)))
    (unwind-protect
         (let ((theirs (clip-remote-push remote "genesis" "digest-theirs")))
           (multiple-value-bind (after op)
               (clip-request session :id "op-clip-raced" :remote remote
                                     :registry registry :base "genesis")
             (let ((line (nth-value 1 (operation-wait after "op-clip-raced" :timeout "30s"))))
               (check-string= (format nil "CLIP RACED session=/s/raced operation=op-clip-raced boundary=req-e2 generation=1 expected=genesis found=~A"
                                      (clip-sha12 theirs))
                              line "the wait prints CLIP RACED")
               (check-equal :raced (operation-state op) "the operation is raced")
               (check-equal (list theirs) (git-clip-remote-history remote)
                            "only the other writer's commit is on the ref"))))
      (close-durable-operation-registry registry))))

(deftest "session-stop-waits-for-the-clip-its-session-launched" "docs/SPEC-WORK.md:2748-2750,6091-6095"
    "expected=stop-inside-a-short-git-timeout-prints-the-NOTE-and-leaves-the-transport-running;a-later-stop-prints-CLIP-OK-then-SESSION-OK"
  (let* ((root (test-session-root "clip-path-stop"))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         (remote (gated-clip-remote root))
         (session (make-work-session :path "/s/stop" :events *clip-path-events*)))
    (unwind-protect
         (let ((after (clip-request session :id "op-clip-stop" :remote remote
                                            :registry registry)))
           (let ((lines (session-stop after :git-timeout "200ms")))
             (check-string= "OPERATION NOTE waiting id=op-clip-stop timeout=200ms after=0"
                            (first lines) "a stop inside its git timeout prints the wait's NOTE")
             (ok (search "SESSION OK session=/s/stop" (second lines))
                 "and then its SESSION OK: ~A" (second lines))
             (check-equal :running (registry-operation-state registry "op-clip-stop")
                          "the transport is left running")
             (check-equal '() (git-clip-remote-history remote) "and nothing was pushed by the stop"))
           (open-clip-gate remote)
           (let ((lines (session-stop after :git-timeout "30s")))
             (ok (search "CLIP OK session=/s/stop operation=op-clip-stop" (first lines))
                 "the stop prints its own clip's CLIP OK first: ~A" (first lines))
             (ok (search "SESSION OK" (second lines)) "then SESSION OK")
             (check-string= (first lines) (nth-value 1 (operation-wait after "op-clip-stop"))
                            "the stop's line is the line operation wait answers")
             (check-equal 1 (length (git-clip-remote-history remote))
                          "and the real ref moved once")))
      (close-durable-operation-registry registry))))

(deftest "session-stop-lifecycle-waits-on-its-clip-operation" "docs/SPEC-WORK.md:824-826,2748-2750,6091-6095"
    "expected=the-resident-stop-prints-the-settled-CLIP-OK-of-its-launched-clip;then-releases-the-owner"
  (let* ((root (test-session-root "clip-path-lifecycle"))
         (registry (open-durable-operation-registry
                    (format nil "~A/operations.journal" root)))
         (remote (open-git-clip-remote (format nil "~A/remote.git" root)))
         (work (make-work-session :path "/s/life" :events *clip-path-events*))
         (seed '((:id "acme/work" :type :work-set :state :unknown)))
         (sess (session-start :owner "rowan" :state-seed seed :base "abc123")))
    (unwind-protect
         (progn
           (clip-request work :id "op-clip-life" :remote remote :registry registry)
           (multiple-value-bind (okp lines record)
               (session-stop-lifecycle sess :clip-registry registry
                                            :clip-operation "op-clip-life"
                                            :git-timeout "30s"
                                            :now "2026-09-23T16:05:00Z")
             (ok okp "the stop is admitted")
             (ok (search "CLIP OK session=/s/life operation=op-clip-life" (first lines))
                 "the resident stop prints its launched clip's settled line: ~A" (first lines))
             (check-string= (registry-operation-result registry "op-clip-life") (first lines)
                            "which is the scheduler's settled result")
             (ok (search "SESSION OK" (second lines)) "then its SESSION OK")
             (check-string= "2026-09-23T16:05:00Z" (owner-until record)
                            "and the owner is released at the stop's stamp")
             (check-equal 1 (length (git-clip-remote-history remote))
                          "the real ref moved once")))
      (close-durable-operation-registry registry))))
