;;;; s4.lisp --- the S4 acceptance cases, one per named replay.
;;;;
;;;; Each case exercises the pure records and functions in src/s4-*.lisp against
;;;; the spec text the slice names; the command-thread wiring is a later card.

(in-package #:nova-work/tests)

(defun rowf (row key) (getf row key))

(defparameter *alex-pool*
  (make-pool
   '((:id "nova-tools" :type :work-set :parent nil :repo "mas-bandwidth/nova-tools")
     (:id "nova-tools/shared" :type :work-set :parent "nova-tools"
      :repo "mas-bandwidth/nova-tools" :required nil)
     (:id "nova-tools/sec/alex-1" :type :task :parent "nova-tools"
      :category "security-finding" :repo "mas-bandwidth/nova-tools"
      :state :done :branch :c :disposed :done :landed "9f2c1a7e"
      :settled "2026-09-12T18:22:41Z" :responsible "emma" :evidence 2 :verified 2)
     (:id "nova-tools/sec/alex-2" :type :task :parent "nova-tools"
      :category "security-finding" :repo "mas-bandwidth/nova-tools"
      :state :done :branch :c :disposed :done :landed "4b81c0d5"
      :settled "2026-09-13T09:14:02Z" :responsible "emma" :evidence 2 :verified 2)
     (:id "nova-tools/sec/alex-3" :type :task :parent "nova-tools"
      :category "security-finding" :repo "mas-bandwidth/nova-tools"
      :state :doing :branch :o :holder "freddy" :responsible "freddy")
     (:id "nova-tools/sec/alex-4" :type :task :parent "nova-tools"
      :category "security-finding" :repo "mas-bandwidth/nova-tools"
      :state :cancelled :branch :c :disposed :cancelled
      :settled "2026-09-11T22:03:55Z" :responsible "rowan" :evidence 1 :verified 0)
     (:id "nova-tools/release/v0.4.2" :type :task :parent "nova-tools"
      :category "release" :repo "mas-bandwidth/nova-tools" :version "v0.4.2"
      :deps ("nova-tools/sec/alex-1") :branch :c :disposed :done
      :settled "2026-09-12T19:00:00Z" :responsible "glenn")
     (:id "nova-tools/release/v0.4.3" :type :task :parent "nova-tools"
      :category "release" :repo "mas-bandwidth/nova-tools" :version "v0.4.3"
      :deps ("nova-tools/sec/alex-2") :branch :o :state :todo :responsible "glenn")))
   "The worked-acceptance fixture of docs/SPEC-WORK.md:2107: four findings under
one repository, category security-finding, with two release tasks of which v0.4.2
settled and v0.4.3 is still open.")

;;; ------------------------------------------------------------------
;;; ready-names-the-blocker-and-the-resolver    SPEC-WORK.md:6160
;;; ------------------------------------------------------------------

(deftest "ready-names-the-blocker-and-the-resolver" "docs/SPEC-WORK.md:6160"
    "expected=every-non-ready-row-carries-its-exact-reason-and-resolver"
  (let ((pool
          (make-pool
           '((:id "r" :type :work-set :parent nil :repo "o/r")
             (:id "r/base" :type :task :parent "r" :branch :c :disposed :done
              :responsible "emma")
             (:id "r/open" :type :task :parent "r" :deps ("r/base") :acceptance ("c1")
              :state :todo :responsible "emma")
             (:id "r/lease" :type :task :parent "r" :acceptance ("c1")
              :holder "freddy" :responsible "emma")
             (:id "r/block" :type :task :parent "r" :acceptance ("c1")
              :state :blocked :blocked-by "r/base" :responsible "emma")
             (:id "r/noacc" :type :task :parent "r" :state :todo :responsible "emma")
             (:id "r/hole" :type :task :parent "r" :deps ("r/missing-open")
              :acceptance ("c1") :state :todo :responsible "emma")
             (:id "r/missing-open" :type :task :parent "r" :state :doing
              :responsible "jane")))))
    (multiple-value-bind (a ar aw) (s4-ready-p pool "r/open")
      (ok (and a (string= "ready" ar) (string= "emma" aw))
          "open ready: ~S ~S ~S" a ar aw))
    (multiple-value-bind (a ar aw) (s4-ready-p pool "r/lease")
      (ok (and (not a) (string= "leased" ar) (string= "freddy" aw))
          "leased names its resolver: ~S ~S" ar aw))
    (multiple-value-bind (a ar aw) (s4-ready-p pool "r/block")
      (ok (and (not a) (string= "blocked" ar) (string= "r/base" aw))
          "blocked names its resolver: ~S ~S" ar aw))
    (multiple-value-bind (a ar aw) (s4-ready-p pool "r/noacc")
      (ok (and (not a) (string= "no acceptance" ar) (string= "emma" aw))
          "no acceptance names its resolver: ~S ~S" ar aw))
    (multiple-value-bind (a ar aw) (s4-ready-p pool "r/hole")
      (ok (and (not a) (string= "dependency not done" ar) (string= "jane" aw))
          "an unsatisfied dep names the dep owner: ~S ~S" ar aw))
    ;; The ready ask: exactly one eligible row, the blockers following it.
    (let ((rows (ready-query pool :scope "r" :order :priority)))
      (check-equal 1 (count-if (lambda (r) (rowf r :ready)) rows)
                   "one eligible row")
      (check-equal "r/open" (rowf (car rows) :id) "the eligible row is first")
      (ok (> (length rows) 1) "blocked rows follow, not dropped"))))

;;; ------------------------------------------------------------------
;;; cow-root-partition                         SPEC-WORK.md:5375
;;; ------------------------------------------------------------------

(deftest "cow-root-partition" "docs/SPEC-WORK.md:5375"
    "expected=one-id-in-C-or-O-never-both,open-plus-closed-equals-total"
  (let ((pool
          (make-pool
           '((:id "p" :type :work-set :parent nil)
             (:id "p/a" :type :task :parent "p" :branch :c :disposed :done)
             (:id "p/b" :type :task :parent "p" :branch :o :state :todo)
             (:id "p/c" :type :task :parent "p" :branch :o :state :doing)))))
    (multiple-value-bind (open closed total) (partition-counts pool)
      (check-equal 3 open "open count")
      (check-equal 1 closed "closed count")
      (check-equal 4 total "total count")
      (check-equal total (+ open closed) "open + closed == total"))
    ;; A settle of an already-C id, or a revive of an already-O id, is rule 18.
    (ok (search "rule 18" (rule-18-finding pool "p/a" :settle))
        "settling an already-C id is a rule 18 finding")
    (ok (search "rule 18" (rule-18-finding pool "p/b" :revive))
        "reviving an already-O id is a rule 18 finding")
    (ok (null (rule-18-finding pool "p/b" :settle))
        "a real partition move is not a finding")
    (ok (null (rule-18-finding pool "p/a" :revive))
        "a real revive is not a finding")))

;;; ------------------------------------------------------------------
;;; branch-and-window-required                 SPEC-WORK.md:5426
;;; ------------------------------------------------------------------

(deftest "branch-and-window-required" "docs/SPEC-WORK.md:5426"
    "expected=each-missing-flag-named-at-exit-2"
  (ok (search "--branch" (check-query-ask :done)) "no --branch names --branch")
  (ok (search "--from" (check-query-ask :done :branch :closed))
      "--branch closed with no window names --from/--to")
  (ok (search "--from" (check-query-ask :done :branch :root))
      "--branch root with no window names --from/--to")
  (ok (search "--from" (check-query-ask :done :branch :open :from "2026-09-01T00:00:00Z"))
      "--from under --branch open is refused")
  (ok (search "--branch" (check-query-ask :who :branch :closed))
      "who refuses --branch closed")
  (ok (search "--branch" (check-query-ask :stale :branch :root))
      "stale refuses --branch root")
  (ok (search "--order priority" (check-query-ask :done :branch :open :order :priority))
      "--order priority on a non-ready ask is exit 2")
  (ok (null (check-query-ask :ready :branch :open :order :priority))
      "--order priority is legal on the ready ask")
  (ok (null (check-query-ask :done :branch :closed
                             :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z"))
      "a complete closed ask is admissible"))

;;; ------------------------------------------------------------------
;;; merged-is-not-distributed                  SPEC-WORK.md:5424
;;; ------------------------------------------------------------------

(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:5424"
    "expected=released=-while-the-release-task-is-open,then-the-version"
  (let ((pool
          (make-pool
           '((:id "fix" :type :task :parent nil :branch :c :disposed :done
              :landed "4b81c0d5")
             (:id "rel" :type :task :parent nil :version "v0.4.3" :deps ("fix")
              :branch :o :state :todo)))))
    ;; A merged fix is not a distributed one: its release task is still open.
    (check-equal nil (released-of pool "fix") "released is unset while open")
    (check-equal "-" (rowf (s4-disposition-row pool "fix") :released)
                 "the disposition row prints released=-")
    (check-equal "4b81c0d5" (rowf (s4-disposition-row pool "fix") :landed)
                 "landed= carries the merged sha"))
  (let ((pool
          (make-pool
           '((:id "fix" :type :task :parent nil :branch :c :disposed :done
              :landed "4b81c0d5")
             (:id "rel" :type :task :parent nil :version "v0.4.3" :deps ("fix")
              :branch :c :disposed :done :settled "2026-09-14T09:00:00Z")
             (:id "rel-old" :type :task :parent nil :version "v0.4.2" :deps ("fix")
              :branch :c :disposed :done :settled "2026-09-13T09:00:00Z")))))
    ;; Where two settled release tasks name one item, the earlier settle stamp
    ;; wins: the release that first carried the fix is the one that distributed it.
    (check-equal "v0.4.2" (released-of pool "fix")
                 "the earlier-settled release task's version wins")))

;;; ------------------------------------------------------------------
;;; findings-across-c-and-o                    SPEC-WORK.md:5430
;;; ------------------------------------------------------------------

(deftest "findings-across-c-and-o" "docs/SPEC-WORK.md:5430"
    "expected=four-ids-four-rows,open=1-closed=3-closed-in=3,dispositions-distinct"
  (multiple-value-bind (line rows open closed cin)
      (under-query *alex-pool*
                   :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :root
                   :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
    (declare (ignore line))
    (check-equal 4 (length rows) "four ids, four rows")
    (check-equal 1 open "open=1")
    (check-equal 3 closed "closed=3")
    (check-equal 3 cin "closed-in=3")
    ;; No id twice.
    (check-equal 4 (length (remove-duplicates (mapcar (lambda (r) (rowf r :id)) rows)
                                              :test #'equal))
                 "no id printed twice")
    ;; The dispositions are the four distinct ones of the fixture.
    (let ((disps (sort (mapcar (lambda (r) (rowf r :disposition)) rows)
                       (lambda (a b) (string< (symbol-name a) (symbol-name b))))))
      (check-equal '(:cancelled :done :done :working) disps "dispositions distinct"))
    ;; released= reads the reverse dependency: alex-1 shipped in v0.4.2, alex-2
    ;; is merged but not distributed.
    (let ((alex-1 (find "nova-tools/sec/alex-1" rows :key (lambda (r) (rowf r :id)) :test #'equal))
          (alex-2 (find "nova-tools/sec/alex-2" rows :key (lambda (r) (rowf r :id)) :test #'equal)))
      (check-equal "v0.4.2" (rowf alex-1 :released) "alex-1 released=v0.4.2")
      (check-equal "-" (rowf alex-2 :released) "alex-2 released=- (release task open)")))
  ;; The second ask: the release task is listed as pending, one set read twice.
  (multiple-value-bind (line rows) 
      (under-query *alex-pool*
                   :repo "mas-bandwidth/nova-tools" :category "release"
                   :branch :open)
    (declare (ignore line))
    (let ((rel (find "nova-tools/release/v0.4.3" rows
                     :key (lambda (r) (rowf r :id)) :test #'equal)))
      (ok (and rel (eq :pending (rowf rel :disposition)))
          "the open release task is listed as pending"))))
