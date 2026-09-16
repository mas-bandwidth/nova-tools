;;;; s5.lisp --- the S5 acceptance cases: evidence, attempts, generation,
;;;; models and a done that costs something.
;;;;
;;;; Each case names its line of docs/SPEC-WORK.md and is implemented against
;;;; the pure functions and records of src/s5-evidence.lisp and
;;;; src/s5-model.lisp, which a later wiring card connects to the command
;;;; thread. No kernel, session, state or event file is edited here.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green        SPEC-WORK.md:4874
;;; ------------------------------------------------------------------

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:4874"
    "a reference into C whose partition cannot be read reports rule 2 unavailable, never dangling, never green"
  (let ((resolve (lambda (id branch stamp)
                   (declare (ignore id branch stamp))
                   :unavailable)))
    (check-equal :unavailable (resolve-reference resolve "acme/work/f1/t1" :closed "2026-09-10")
                 "a closed-index lookup that cannot read its page")
    ;; The distinction the hurt paid for: unavailable is its own answer,
    ;; not the dangling reading and not the green reading.
    (check-equal :unavailable (resolve-reference resolve "acme/work/f1/t1" :closed "2026-09-10")
                 "the same lookup reported the same way")
    (let ((dangling (lambda (id branch stamp) (declare (ignore id branch stamp)) nil)))
      (check-equal :dangling (resolve-reference dangling "ghost" :closed "2026-09-10")
                   "a lookup that reads and finds nothing is dangling, not unavailable"))))

;;; ------------------------------------------------------------------
;;; settle-keeps-id-and-evidence           SPEC-WORK.md:5378
;;; ------------------------------------------------------------------

(deftest "settle-keeps-id-and-evidence" "docs/SPEC-WORK.md:5378"
    "a state --to done settles its task and keeps :id, :children, :acceptance and every evidence event's five fields"
  (let* ((crit (make-criterion "c1" :test "test:internal/lockfile/TestLockRule1@r1" :passes))
         (ev (make-evidence "test:internal/lockfile/TestLockRule1@r1" "c1" "r1" 3 "a1"))
         (node (list :id "acme/work/f1/t1"
                     :children '("t1.1" "t1.2")
                     :acceptance (list crit)
                     :evidence (list ev)
                     :generation 3
                     :state :doing)))
    (let ((done (settle-to-done node)))
      (check-equal "acme/work/f1/t1" (getf done :id) "the id is unchanged")
      (check-equal '("t1.1" "t1.2") (getf done :children) "the children are unchanged")
      (check-equal (list crit) (getf done :acceptance) "the acceptance is unchanged")
      (check-equal 3 (getf done :generation) "the generation is unchanged")
      (check-equal :done (getf done :state) "the state moved to done")
      (check-equal (evidence-five-fields ev)
                   (evidence-five-fields (car (getf done :evidence)))
                   "the evidence event's five fields read the same from the closed row"))))

;;; ------------------------------------------------------------------
;;; accept-add-on-a-done-node-refuses      SPEC-WORK.md:2359
;;; ------------------------------------------------------------------

(deftest "accept-add-on-a-done-node-refuses" "docs/SPEC-WORK.md:2359"
    "an accept --add on a node derived :done is refused by rule 5 until correct reopens the acceptance"
  (let* ((crit (make-criterion "c1" :test "test:x" :passes))
         (ev (make-evidence "test:x" "c1" "r1" 1 nil))
         (c2 (make-criterion "c2" :job "job:deploy" :succeeds)))
    ;; The done claim stands on criteria covered by existing evidence.
    (let ((standing-done (done-finding (list ev) (list crit) 1)))
      (ok (null standing-done) "the standing :to :done is valid: ~A"
          (and standing-done (finding-reason standing-done))))
    ;; accept --add of an uncovered criterion is refused by rule 5.
    (let ((f (accept-add-finding t (list crit) c2 (list ev))))
      (ok f "an accept --add on a done node refuses")
      (check-equal 5 (finding-rule f) "the refusal is rule 5"))
    ;; after correct bumps the generation, the add is admitted as an open
    ;; acceptance change, and fresh evidence of the new generation closes it.
    (let ((g2 (correct-generation 1)))
      (check-equal 2 g2 "correct bumps the task generation")
      (ok (null (accept-add-finding nil (list crit) c2 (list ev)))
          "once the node is off its done claim, the add is admitted")
      (ok (null (done-finding (list (make-evidence "test:x" "c1" "r1" 2 nil)
                                    (make-evidence "run:x#y@r2" "c2" "r2" 2 nil))
                              (list crit c2) 2))
          "fresh evidence of the new generation re-covers the acceptance"))))

;;; ------------------------------------------------------------------
;;; older-generation-evidence-cannot-close  SPEC-WORK.md:1231
;;; ------------------------------------------------------------------

(deftest "older-generation-evidence-cannot-close" "docs/SPEC-WORK.md:1231"
    "evidence of a generation older than the task's current generation cannot close the task"
  (let* ((crit (make-criterion "c1" :test "test:x" :passes))
         (old (make-evidence "test:x" "c1" "r1" 1 nil)))
    (ok (older-generation-evidence-p (evidence-generation old) 2)
        "generation 1 evidence is older than a generation 2 task")
    (ok (null (older-generation-evidence-p (evidence-generation old) 1))
        "generation 1 evidence is not older than a generation 1 task")
    (let ((f (done-finding (list old) (list crit) 2)))
      (ok f "a :to :done naming older-generation evidence refuses")
      (check-equal 5 (finding-rule f) "the refusal is rule 5"))))

;;; ------------------------------------------------------------------
;;; done-unverified-is-its-own-count       SPEC-WORK.md:2045
;;; ------------------------------------------------------------------

(deftest "done-unverified-is-its-own-count" "docs/SPEC-WORK.md:2045"
    "done, done-unverified and unknown are three counts, kept apart and never added"
  (let* ((nodes '((:disposition :done :verified t)
                  (:disposition :done :verified nil)
                  (:disposition :unknown :verified nil))))
    (let ((counts (count-dispositions nodes)))
      (check-equal 1 (getf counts :done) "one verified done")
      (check-equal 1 (getf counts :done-unverified) "one recorded done on unverified evidence")
      (check-equal 2 (getf counts :unknown)
                   "unknown is the unverified done plus the unknown node, kept apart from done")
      (ok (= (getf counts :done) 1) "done-unverified never rides the done count"))))

;;; ------------------------------------------------------------------
;;; four-capability-groups-and-three-fields  SPEC-WORK.md:3363 (model half)
;;; ------------------------------------------------------------------

(deftest "four-capability-groups-and-three-fields" "docs/SPEC-WORK.md:4507"
    "declared capability, assessment and measured result stay three fields; unknown evidence never hardens into a strength"
  (let* ((idx (make-model-index))
         (idx (model-register idx "gpt-x" "openai" "route-a" :metered))
         (idx (model-rate idx "gpt-x"
                          (make-rate-record :pricing-id "p1" :pricing :per-mtoken
                                            :effective "2026-09-01T00:00:00Z" :source "price:openai#p1@r1")))
         (idx (model-evidence idx "gpt-x"
                              (make-measurement-record :task-class "code-review"
                                                       :result "run:x#42@r2"
                                                       :samples 3 :source "bench:studio"))))
    (let* ((m (index-get idx "gpt-x"))
           (fields (model-three-fields m)))
      (check-equal "gpt-x" (model-id m) "the model identity is its id")
      (check-equal '() (getf fields :capabilities) "the declared capability field is separate and empty here")
      (check-equal 1 (length (getf fields :assessments)) "one assessment (pricing) in its own field")
      (check-equal 1 (length (getf fields :measurements)) "one measured result in its own field")
      (ok (and (listp (getf fields :capabilities))
               (listp (getf fields :assessments))
               (listp (getf fields :measurements)))
          "the three fields are three lists and never collapse into one")
      ;; Unknown or outdated evidence is a measurement, never a strength.
      (let ((idx2 (model-evidence idx "gpt-x"
                                  (make-measurement-record :task-class "unknown"
                                                           :result "unresolved"
                                                           :samples 0 :source "bench:studio"))))
        (check-equal 2 (length (getf (model-three-fields (index-get idx2 "gpt-x")) :measurements))
                     "an unresolved result stays a measurement")
        (check-equal '() (getf (model-three-fields (index-get idx2 "gpt-x")) :capabilities)
                     "and never hardens into a declared capability")))))
