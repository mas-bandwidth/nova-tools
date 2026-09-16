;;;; s4.lisp --- the slice-4 acceptance cases (red first).
;;;;
;;;; S4 — The pool is the tree (#466). The ready-to-assign view and the
;;;; disposition row, the counted-containment forest with references as a graph.
;;;;
;;;; The contract this file leaves for the implementation cards:
;;;;
;;;;   * `submit' gains the structure verbs `:dep' (change :add|:remove), and
;;;;     `dep', `node require' and `prioritise' are reachable as `:verb :dep',
;;;;     `:verb :node-require', `:verb :prioritise'.
;;;;
;;;;   * `ask-ready'  (kernel &key node order)        an O-side listing of the
;;;;     walkable work; `order' is :discovery or :priority (default
;;;;     :discovery); `--order priority' on any other ask refuses at exit 2.
;;;;
;;;;   * `ask-under'  (kernel &key repo category branch from to max)
;;;;     `ask-remaining' (kernel &key repo category branch)
;;;;
;;;;   Each ask answers (values ok-p line exit-code rows): ok-p is T on a QUERY
;;;;   OK, line is the `QUERY OK' scope line (or a `QUERY FAIL' line on
;;;;   refusal), exit-code 0 on success and 2 on a flag refusal, and rows is a
;;;;   list of `QUERY ROW' line strings in printed order.

(in-package #:nova-work/tests)

(defun find-row (rows id)
  "The printed row whose id token is ID, or NIL."
  (find-if (lambda (r) (search id r)) rows))

;;; ------------------------------------------------------------------
;;; ready — the pool is the tree: every row that cannot proceed prints its
;;; exact reason and who can resolve it (SPEC-WORK.md:2104,6160).
;;; ------------------------------------------------------------------

(deftest "ready-names-the-blocker-and-the-resolver" "docs/SPEC-WORK.md:2104"
    "expected=ready-row:ready=true,reason=-,resolver=-;blocked-row:ready=false,reason=names-dep,resolver=names-owner"
  (let* ((seed '((:id "root"   :type :work-set :parent nil   :state :unknown)
                 (:id "root/a" :type :task     :parent "root" :state :todo
                   :deps ("root/b") :responsible "alice")
                 (:id "root/b" :type :task     :parent "root" :state :todo
                   :responsible "bob")))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code rows) (ask-ready k :node "root")
      (ok okp "the ready ask refused: ~A" line)
      (check-equal 0 code "the ready ask's exit code")
      (ok (search "ask=ready" line) "the QUERY OK line does not say ask=ready: ~A" line)
      (let ((a-row (find-row rows "root/a"))
            (b-row (find-row rows "root/b")))
        (ok a-row "the ready listing has no row for root/a")
        (ok b-row "the ready listing has no row for root/b")
        (ok (search "ready=true" b-row) "root/b is blocked: ~A" b-row)
        (ok (search "reason=-" b-row) "an eligible row names a reason: ~A" b-row)
        (ok (search "resolver=-" b-row) "an eligible row names a resolver: ~A" b-row)
        (ok (search "ready=false" a-row) "root/a was listed ready: ~A" a-row)
        (ok (search "root/b" a-row) "the blocked row does not name its dependency: ~A" a-row)
        (ok (search "resolver=bob" a-row) "the blocked row does not name who resolves it: ~A" a-row)))))

;;; ------------------------------------------------------------------
;;; cow — one id is in C or in O and never in both; open= plus closed= equals
;;; the scope's counted total on every ask (SPEC-WORK.md:5375).
;;; ------------------------------------------------------------------

(deftest "cow-root-partition" "docs/SPEC-WORK.md:5375"
    "expected=open+closed=total,one-branch-per-id,no-id-twice"
  (let* ((seed `((:id "root"   :type :work-set :parent nil    :state :unknown)
                 (:id "root/f" :type :feature  :parent "root" :state :unknown)
                 (:id "root/a" :type :task     :parent "root/f" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")
                 (:id "root/b" :type :task     :parent "root/f" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "root/a" :request "cow-1")) "close of root/a refused")
    (multiple-value-bind (okp line code rows)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :root :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
      (declare (ignore code))
      (ok okp "the under ask refused: ~A" line)
      (ok (search "open=" line) "the QUERY OK line carries no open=: ~A" line)
      (ok (search "closed=" line) "the QUERY OK line carries no closed=: ~A" line))
    ;; Every id is in exactly one branch, and the two counts partition the total.
    (check-equal 5 (+ (state-open-count (kernel-state k))
                      (state-closed-count (kernel-state k)))
                 "open + closed over the whole seed")
    (let ((branches (loop for node in seed collect (node-branch (kernel-state k) (getf node :id)))))
      (check-equal (length seed) (+ (count :o branches) (count :c branches))
                   "some id is in neither C nor O"))))

;;; ------------------------------------------------------------------
;;; branch and window — a query --ask with no --branch is refused at exit 2
;;; naming the flag; --branch closed without a window the same way; a window
;;; under --branch open refused (SPEC-WORK.md:5426).
;;; ------------------------------------------------------------------

(deftest "branch-and-window-required" "docs/SPEC-WORK.md:5426"
    "expected=no-branch=exit-2-names-flag;closed-no-window=exit-2;open-with-from=exit-2"
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/a" :type :task :parent "root" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding")
      (ok (null okp) "an ask with no --branch was answered")
      (check-equal 2 code "the no-branch ask's exit code")
      (ok (search "--branch" line) "the refusal does not name --branch: ~A" line))
    (multiple-value-bind (okp line code)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :closed)
      (ok (null okp) "a closed ask with no window was answered")
      (check-equal 2 code "the closed-no-window ask's exit code")
      (ok (search "--from" line) "the refusal does not name --from/--to: ~A" line))
    (multiple-value-bind (okp line code)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :open :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
      (ok (null okp) "an open ask with a window was answered")
      (check-equal 2 code "the open-with-window ask's exit code")
      (ok (search "--from" line) "the refusal does not name --from: ~A" line))))

;;; ------------------------------------------------------------------
;;; merged is not distributed — a fix in C prints released=- while its release
;;; task is open, and released=<version> once that task settles
;;; (SPEC-WORK.md:5424).
;;; ------------------------------------------------------------------

(deftest "merged-is-not-distributed" "docs/SPEC-WORK.md:5424"
    "expected=released=-open-release;released=v0.4.3-settled-release"
  (let* ((seed '((:id "root"        :type :work-set :parent nil   :state :unknown)
                 (:id "root/alex"   :type :task     :parent "root" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")
                 (:id "root/release" :type :task     :parent "root" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "release"
                   :version "v0.4.3" :deps ("root/alex"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "root/alex" :request "mi-1")) "close of the fix refused")
    (multiple-value-bind (okp line code rows)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :root :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
      (declare (ignore code))
      (ok okp "the under ask refused: ~A" line)
      (ok (search "released=-" (find-row rows "root/alex"))
          "a merged fix named released while its release task is open"))
    (ok (submit k (close-request :node "root/release" :request "mi-2")) "close of the release task refused")
    (multiple-value-bind (okp line code rows)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :root :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
      (declare (ignore code))
      (ok okp "the under ask refused after the release settle: ~A" line)
      (ok (search "released=v0.4.3" (find-row rows "root/alex"))
          "a settled release task did not name the version on the fix's row"))))

;;; ------------------------------------------------------------------
;;; findings across C and O — the worked acceptance's open-branch half: one row
;;; per id, open and closed partitioning the count, and the open release task
;;; listed pending by a second ask (SPEC-WORK.md:5430).
;;; ------------------------------------------------------------------

(deftest "findings-across-c-and-o" "docs/SPEC-WORK.md:5430"
    "expected=one-row-per-id,open=1,closed=2,release-task-pending"
  (let* ((seed '((:id "nova-tools/sec"          :type :work-set :parent nil                    :state :unknown
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")
                 (:id "nova-tools/sec/alex-1"   :type :task :parent "nova-tools/sec" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")
                 (:id "nova-tools/sec/alex-2"   :type :task :parent "nova-tools/sec" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding")
                 (:id "nova-tools/sec/alex-3"   :type :task :parent "nova-tools/sec" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "security-finding" :responsible "freddy")
                 (:id "nova-tools/release/v0.4.2" :type :task :parent "nova-tools/sec" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "release" :version "v0.4.2"
                   :deps ("nova-tools/sec/alex-1"))
                 (:id "nova-tools/release/v0.4.3" :type :task :parent "nova-tools/sec" :state :doing
                   :repo "mas-bandwidth/nova-tools" :category "release" :version "v0.4.3"
                   :deps ("nova-tools/sec/alex-2"))))
         (k (make-kernel :state (make-seed-state seed))))
    (ok (submit k (close-request :node "nova-tools/sec/alex-1" :request "fa-1")) "alex-1 close refused")
    (ok (submit k (close-request :node "nova-tools/sec/alex-2" :request "fa-2")) "alex-2 close refused")
    (ok (submit k (close-request :node "nova-tools/release/v0.4.2" :request "fa-3")) "v0.4.2 close refused")
    (multiple-value-bind (okp line code rows)
        (ask-under k :repo "mas-bandwidth/nova-tools" :category "security-finding"
                   :branch :root :from "2026-09-01T00:00:00Z" :to "2026-09-14T00:00:00Z")
      (ok okp "the under ask refused: ~A" line)
      (check-equal 0 code "the under ask's exit code")
      (ok (search "ask=under" line) "not an under ask: ~A" line)
      (ok (search "branch=root" line) "not a root-spanning ask: ~A" line)
      (ok (search "open=1" line) "open is not 1: ~A" line)
      (ok (search "closed=2" line) "closed is not 2: ~A" line)
      (check-equal 3 (length rows) "one row per finding, no id twice")
      (let ((a1 (find-row rows "nova-tools/sec/alex-1"))
            (a2 (find-row rows "nova-tools/sec/alex-2"))
            (a3 (find-row rows "nova-tools/sec/alex-3")))
        (ok (search "branch=closed" a1) "alex-1 not closed: ~A" a1)
        (ok (search "disposition=done" a1) "alex-1 not done: ~A" a1)
        (ok (search "released=v0.4.2" a1) "alex-1 does not name v0.4.2: ~A" a1)
        (ok (search "branch=closed" a2) "alex-2 not closed: ~A" a2)
        (ok (search "released=-" a2) "alex-2 names a release although v0.4.3 is open: ~A" a2)
        (ok (search "branch=open" a3) "alex-3 not open: ~A" a3)
        (ok (search "disposition=pending" a3) "alex-3 disposition is not pending: ~A" a3)
        (ok (search "responsible=freddy" a3) "alex-3 does not name its responsible: ~A" a3)))
    ;; The same identities read twice: the open release task is listed pending.
    (multiple-value-bind (okp line code rows)
        (ask-remaining k :repo "mas-bandwidth/nova-tools" :category "release"
                       :branch :open)
      (ok okp "the remaining ask refused: ~A" line)
      (check-equal 0 code "the remaining ask's exit code")
      (ok (search "branch=open" line) "the remaining ask is not open: ~A" line)
      (let ((r3 (find-row rows "nova-tools/release/v0.4.3")))
        (ok r3 "no row for the open release task")
        (ok (search "disposition=pending" r3)
            "the open release task is not pending: ~A" r3)))))
