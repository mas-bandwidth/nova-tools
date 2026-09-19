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
