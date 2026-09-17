;;;; acceptance.lisp --- the five slice-1 cases.
;;;;
;;;; Each case names the line of docs/SPEC-WORK.md at
;;;; 7db3b95cd4c16b1eb34b1c77c0fc224755cc7a64 it comes from, and carries either
;;;; the acceptance table's own expected= string or the executable invariant the
;;;; table names where that row has none.

(in-package #:nova-work/tests)

(defparameter *seed*
  '((:id "acme/work"       :type :work-set :parent nil            :state :unknown)
    (:id "acme/work/f1"    :type :feature  :parent "acme/work"    :state :unknown)
    (:id "acme/work/f1/t1" :type :task     :parent "acme/work/f1" :state :doing
     :links ("https://github.com/acme/work/issues/11"
             "https://github.com/acme/work/issues/12"))
    (:id "acme/work/f1/t2" :type :task     :parent "acme/work/f1" :state :review
     :links ("https://github.com/acme/work/issues/21"))
    (:id "acme/work/f2"    :type :feature  :parent "acme/work"    :state :unknown))
  "Five canonical item ids, all in O at the seed: |O| = 5, of which two are
leaf tasks and three are open linked issues (SPEC-WORK.md:1577 keeps the three
counters apart).")

(defparameter *cyclic-seed*
  '((:id "a" :type :task :parent "b" :state :doing)
    (:id "b" :type :task :parent "a" :state :doing))
  "a -> b -> a. SPEC-WORK.md:3347 referential-integrity: cycles fail BEFORE
publication.")

(defun fresh (&key (journal (make-ordering-journal)) (rev-base 1))
  (make-kernel :state (make-seed-state *seed*) :journal journal :rev-base rev-base))

(defun close-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "shipped")
                           (evidence '("ev-1")) (request "req-1")
                           (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :state-to-done :node node :by by :reason reason :evidence evidence
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun reopen-request (&key (node "acme/work/f1/t1") (by "rowan") (reason "regressed")
                            (request "req-2") (stamp "2026-09-14T13:00:00Z")
                            (generation-owner "gen-4"))
  (list :verb :event-reopen :node node :by by :reason reason
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun independent-open-count (state)
  "Walk every node and count the ones in O. This is the full count the counter
is compared against; it is never the path `query --ask size` takes."
  (let ((n 0))
    (dolist (node *seed*)
      (when (eq :o (node-branch state (getf node :id))) (incf n)))
    n))

;;;; acceptance.lisp --- loader for the per-slice replay files (nova-tools #560).
;;;;
;;;; One file per replay slice so parallel PRs stop conflicting: each slice
;;;; lives in acceptance/<slice>.lisp and this top file only loads them.
;;;; An amendment edits one slice file, never this loader and never two
;;;; slices at once. The shared seed and request helpers stay here so every
;;;; slice sees the same canonical fixtures.

(eval-when (:compile-toplevel :load-toplevel :execute)
  (unless (find-package :asdf)
    (require :asdf)))

;; The slice files, in canonical order. Each is one replay slice; an
;; amendment edits one of them (nova-tools #560).
(defparameter *acceptance-slices*
  '("slice-01-reader.lisp"
    "slice-02-close-and-counters.lisp"
    "slice-03-containers.lisp"
    "slice-04-doing-and-journal.lisp"
    "slice-05-durable-journal.lisp"
    "slice-06-replays-early.lisp"
    "slice-07-replays-mid.lisp"
    "slice-08-replays-late.lisp"
    "slice-09-state-export-replays.lisp"
    "slice-10-fleet.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
;;;; The five replays of nova-tools #362 (SPEC-WORK.md lines 2400-3600
;;;; part 1 of 4). Each drives the kernel through the verb its paragraph
;;;; names and asserts the outcome the paragraph promises.
;;;; ------------------------------------------------------------------

(deftest "edit-undo-preserves-later-work" "docs/SPEC-WORK.md:5355"
    "expected=undo-restores-before;late-undo=conflict;both-events-stand"
  ;; SPEC-WORK.md:5806 -- an edit undone restores :before; the same undo after
  ;; an intervening edit is refused conflict, both events standing.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/t" :type :task :parent "root" :state :doing :title "a")))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (node-edit k "root/t" :changes (list :title "b") :request "edit-1"
                   :reason "rename" :stamp "2026-09-17T00:00:00Z")
      (ok okp "first edit accepted: ~A" line)
      (check-equal 0 code "first edit exit"))
    (check-string= "b" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "edit postimage")
    (multiple-value-bind (okp line code)
        (node-undo k "root/t" :undo-of "edit-1" :request "undo-1"
                   :reason "revert" :stamp "2026-09-17T00:01:00Z")
      (ok okp "undo accepted: ~A" line)
      (check-equal 0 code "undo exit"))
    (check-string= "a" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "undo restores :before")
    (node-edit k "root/t" :changes (list :title "c") :request "edit-2"
               :reason "again" :stamp "2026-09-17T00:02:00Z")
    (multiple-value-bind (okp line code)
        (node-undo k "root/t" :undo-of "edit-1" :request "undo-2"
                   :reason "late" :stamp "2026-09-17T00:03:00Z")
      (ok (null okp) "late undo refused")
      (check-equal 1 code "late undo conflict exit")
      (ok (search "conflict" line) "conflict named: ~A" line))
    (check-string= "c" (getf (node-metadata (kernel-state k) "root/t") :title)
                   "later work stands")
    (check-equal 2 (length (node-edit-log (kernel-state k) "root/t"))
                  "both edit events stand")))

(deftest "edit-never-fetches-a-link" "docs/SPEC-WORK.md:5357"
    "expected=fetches=0;nul=bad-link;private-node-prints-no-value"
  ;; SPEC-WORK.md:5808 -- a link that is a live URL to a counting endpoint
  ;; added, edited and rendered with zero requests observed; a link holding NUL
  ;; refused `bad link`; a refusal on a private node printing no value.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)
                 (:id "root/t" :type :task :parent "root" :state :doing)
                 (:id "root/p" :type :task :parent "root" :state :doing :private t)))
         (k (make-kernel :state (make-seed-state seed))))
    (setf *link-fetch-count* 0)
    (node-edit k "root/t" :changes (list :links (list "http://127.0.0.1:9/count"))
               :request "link-1" :reason "add" :stamp "2026-09-17T00:00:00Z")
    (node-edit k "root/t" :changes (list :links (list "http://127.0.0.1:9/other"))
               :request "link-2" :reason "edit" :stamp "2026-09-17T00:01:00Z")
    (let ((rendered (render-node k "root/t")))
      (ok (search "127.0.0.1" rendered) "link rendered: ~A" rendered))
    (check-equal 0 *link-fetch-count* "zero requests on add, edit and render")
    (multiple-value-bind (okp line code)
        (node-edit k "root/t"
                   :changes (list :links (list (format nil "http://x/~C" #\Nul)))
                   :request "link-3" :reason "bad" :stamp "2026-09-17T00:02:00Z")
      (ok (null okp) "NUL link refused")
      (check-equal 1 code "NUL link exit")
      (ok (search "bad link" line) "bad link named: ~A" line))
    (multiple-value-bind (okp line code)
        (node-edit k "root/p" :changes (list :title 7)
                   :request "priv-1" :reason "bad type" :stamp "2026-09-17T00:03:00Z")
      (ok (null okp) "private node refusal")
      (check-equal 1 code "private refusal exit")
      (ok (search "private=1" line) "private marker printed: ~A" line)
      (ok (null (search "7" line)) "no value printed: ~A" line))))

(deftest "repo-only-at-the-root" "docs/SPEC-WORK.md:5341"
    "expected=repo-at-root-unique;second-refused-repo-held-by;under-parent-refused"
  ;; SPEC-WORK.md:5792 -- `--repo` under the open root and `--type work-set`
  ;; accepted and unique; the same `--repo` again refused `repo held by <id>`;
  ;; `--repo` under a parent refused `repo outside root`.
  (let ((k (make-kernel :state (make-seed-state '()))))
    (multiple-value-bind (okp line code)
        (node-add k :id "r1" :type :work-set :parent nil :repo "acme/x")
      (ok okp "root repo accepted: ~A" line)
      (check-equal 0 code "root repo exit"))
    (check-string= "acme/x" (node-repo (kernel-state k) "r1") "repo recorded")
    (multiple-value-bind (okp line code)
        (node-add k :id "r2" :type :work-set :parent nil :repo "acme/x")
      (ok (null okp) "second repo refused")
      (check-equal 1 code "second repo exit")
      (ok (search "repo held by r1" line) "holder named: ~A" line))
    (multiple-value-bind (okp line code)
        (node-add k :id "r3" :type :work-set :parent "r1" :repo "acme/y")
      (ok (null okp) "under-parent repo refused")
      (check-equal 1 code "under-parent exit")
      (ok (search "repo outside root" line) "outside root named: ~A" line))))

(deftest "roadmap-has-one-creator" "docs/SPEC-WORK.md:5344"
    "expected=node-add--type-roadmap-exit-2-naming-roadmap-create;one-node-and-one-view-atomic"
  ;; SPEC-WORK.md:5795 -- `node add --type roadmap` exit 2 naming `roadmap
  ;; create`; `roadmap create` writing one node and one view in one envelope.
  (let* ((seed '((:id "root" :type :work-set :parent nil :state :unknown)))
         (k (make-kernel :state (make-seed-state seed))))
    (multiple-value-bind (okp line code)
        (node-add k :id "rm" :type :roadmap :parent "root")
      (ok (null okp) "node add roadmap refused")
      (check-equal 2 code "roadmap add exit 2")
      (ok (search "roadmap create" line) "names roadmap create: ~A" line))
    (multiple-value-bind (okp line code)
        (roadmap-create k :id "rm" :parent "root" :title "Now"
                        :axes '() :members '()
                        :reason "new" :request "rm-1" :stamp "2026-09-17T00:00:00Z")
      (ok okp "roadmap create accepted: ~A" line)
      (check-equal 0 code "roadmap create exit"))
    (check-equal :roadmap (node-type (kernel-state k) "rm") "roadmap node exists")
    (ok (node-view (kernel-state k) "rm") "roadmap view created atomically")))

(deftest "move-keeps-every-count" "docs/SPEC-WORK.md:5360"
    "expected=source+destination-counts-move-by-subtree;ancestor-net-stable;no-whole-set-scan"
  ;; SPEC-WORK.md:5811 -- a required subtree moved between two features: the
  ;; source's and destination's required sets and open counts move by the
  ;; subtree, the common ancestor's net count is stable, |O|, |C| and every
  ;; task state unchanged, and no whole-set scan (visits asserted).
  (let* ((seed '((:id "root"     :type :work-set :parent nil       :state :unknown)
                 (:id "root/f1"  :type :feature  :parent "root"    :state :unknown)
                 (:id "root/f2"  :type :feature  :parent "root"    :state :unknown)
                 (:id "root/f1/t1" :type :task   :parent "root/f1" :state :doing)
                 (:id "root/f1/t2" :type :task   :parent "root/f1" :state :doing)
                 (:id "root/f2/t3" :type :task   :parent "root/f2" :state :doing)))
         (k (make-kernel :state (make-seed-state seed)))
         (o-before (state-open-count (kernel-state k)))
         (c-before (state-closed-count (kernel-state k)))
         (t1-state (node-state (kernel-state k) "root/f1/t1")))
    (check-equal 3 (node-open-count (kernel-state k) "root/f1") "source open before")
    (check-equal 2 (node-open-count (kernel-state k) "root/f2") "destination open before")
    (let ((okp nil) (line nil) (code 0) (visits 0))
      (with-instrumentation
        (setf (values okp line code)
              (node-move k :id "root/f1/t1" :from "root/f1" :under "root/f2"
                         :reason "redistribute" :request "mv-1"
                         :stamp "2026-09-17T00:00:00Z"))
        (setf visits *visits*))
      (ok okp "move accepted: ~A" line)
      (check-equal 0 code "move exit")
      (ok (< visits 6) "no whole-set scan (visits=~D)" visits))
    (check-equal 2 (node-open-count (kernel-state k) "root/f1") "source count falls by subtree")
    (check-equal 3 (node-open-count (kernel-state k) "root/f2") "destination count rises by subtree")
    (check-equal 1 (node-required-count (kernel-state k) "root/f1") "source required set falls")
    (check-equal 2 (node-required-count (kernel-state k) "root/f2") "destination required set rises")
    (check-equal 1 (node-required-open (kernel-state k) "root/f1") "source required-open falls")
    (check-equal 2 (node-required-open (kernel-state k) "root/f2") "destination required-open rises")
    (check-equal o-before (state-open-count (kernel-state k)) "|O| unchanged")
    (check-equal c-before (state-closed-count (kernel-state k)) "|C| unchanged")
    (check-equal t1-state (node-state (kernel-state k) "root/f1/t1") "moved task state unchanged")
    (check-equal '("root/f2/t3" "root/f1/t1") (node-children (kernel-state k) "root/f2")
                 "appended at destination end")
    (check-equal '("root/f1/t2") (node-children (kernel-state k) "root/f1")
                 "removed from source")))
