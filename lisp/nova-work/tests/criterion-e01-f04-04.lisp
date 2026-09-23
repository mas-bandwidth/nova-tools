(in-package #:nova-work/tests)

;;; criterion-e01-f04-04.lisp --- E01-F04-04: Represent project and stream groupings as work-set categories
;;;
;;; without inferring kind from title or position.

(deftest "e01-f04-04-represent-project-stream-groupings"
    "E01-F04-04: Represent project and stream groupings as work-set categories without inferring kind from title or position"
    "expected=project-and-stream-are-container-types;types-are-explicit-not-inferred"
  ;; Project and stream are valid node types that are treated as containers,
  ;; and their type is explicitly stored, not inferred from title or position.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                (:id "p1" :type :project :parent "root" :state :unknown)
                (:id "s1" :type :stream :parent "p1" :state :unknown)
                (:id "t1" :type :task :parent "s1" :state :doing)))
         (state (make-seed-state seed)))
    ;; Project and stream nodes are created with their explicit types
    (check-equal :project (node-type state "p1")
                 "project node has explicit :project type, not inferred")
    (check-equal :stream (node-type state "s1")
                 "stream node has explicit :stream type, not inferred")
    ;; Project and stream are container types (like work-set, epic, feature, roadmap)
    (ok (nova-work::%need-container-p (nova-work::%node-quiet state "p1"))
        "project node is recognized as a container type")
    (ok (nova-work::%need-container-p (nova-work::%node-quiet state "s1"))
        "stream node is recognized as a container type")
    ;; The task node is not a container
    (ok (not (nova-work::%need-container-p (nova-work::%node-quiet state "t1")))
        "task node is not a container type")
    ;; The type is stored explicitly and can be read back
    (check-equal :task (node-type state "t1")
                 "task node has explicit :task type")))
