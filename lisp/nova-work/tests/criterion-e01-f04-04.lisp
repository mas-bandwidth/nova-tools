(in-package #:nova-work/tests)

;;; criterion-e01-f04-04.lisp --- E01-F04-04: Represent project and stream groupings as work-set categories
;;;
;;; without inferring kind from title or position.  The contract is
;;; SPEC-WORK.md "Recursive structure within a repository": project and stream
;;; containers use `:type :work-set` with the explicit `:category` label
;;; ("project" or "stream"); this adds no new kind, and a node's role is never
;;; inferred from its title or its position.

(deftest "e01-f04-04-represent-project-stream-groupings"
    "E01-F04-04: Represent project and stream groupings as work-set categories without inferring kind from title or position"
    "expected=project-and-stream-are-container-types;types-are-explicit-not-inferred"
  ;; monorepo -> project -> stream -> task.  The stream is titled "nova-work"
  ;; and the task is titled "project": neither title decides anything.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "p1" :type :work-set :category "project" :title "monorepo"
                  :parent "root" :state :unknown)
                 (:id "s1" :type :work-set :category "stream" :title "nova-work"
                  :parent "p1" :state :unknown)
                 (:id "t1" :type :task :title "project" :parent "s1" :state :doing)))
         (state (make-seed-state seed)))
    ;; Project and stream are work-sets: no new kind (SPEC-WORK.md, "This adds
    ;; no new kind").
    (check-equal :work-set (node-type state "p1")
                 "project grouping is a :work-set, not a new kind")
    (check-equal :work-set (node-type state "s1")
                 "stream grouping is a :work-set, not a new kind")
    (ok (not (member :project nova-work::*need-container-types*))
        "no :project kind is added to the container set")
    (ok (not (member :stream nova-work::*need-container-types*))
        "no :stream kind is added to the container set")
    ;; The grouping is the explicit :category label, read back as stored.
    (check-equal "project" (node-category state "p1")
                 "project grouping carries the explicit category \"project\"")
    (check-equal "stream" (node-category state "s1")
                 "stream grouping carries the explicit category \"stream\"")
    ;; Both are containers by rule 2's container clause, at any depth.
    (ok (nova-work::%need-container-p (nova-work::%node-quiet state "p1"))
        "project node is recognized as a container type")
    (ok (nova-work::%need-container-p (nova-work::%node-quiet state "s1"))
        "stream node is recognized as a container type")
    ;; A task titled "project" under a stream stays a task: kind is never
    ;; inferred from title or position.
    (check-equal :task (node-type state "t1")
                 "task titled \"project\" keeps its explicit :task type")
    (ok (not (nova-work::%need-container-p (nova-work::%node-quiet state "t1")))
        "task node is not a container type")))
