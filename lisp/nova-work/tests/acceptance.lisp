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
    "slice-09-state-export-replays.lisp"))

(dolist (f *acceptance-slices*)
  (load (asdf:system-relative-pathname :nova-work/tests
          (concatenate 'string "tests/acceptance/" f))))

;;;; ------------------------------------------------------------------
;;;; Replays for the reversible-mistake and metadata-patch paragraphs
;;;; (SPEC-WORK.md:5627,5652,5783,5798,5802). Each drives the kernel
;;;; through its verbs and asserts the paragraph's promise.
;;;; ------------------------------------------------------------------

(defun edit-request (node &key (title '(:keep)) (category '(:keep)) (links '(:keep))
                                (private '(:keep)) (version '(:keep))
                                (request "edit-1") (by "rowan") (reason "tidy")
                                (stamp "2026-09-14T12:00:00Z") (generation-owner "gen-4"))
  (list :verb :node-edit :node node :by by :reason reason
        :title-patch title :category-patch category :links-patch links
        :private-patch private :version-patch version
        :request request :stamp stamp :clock :tool :generation-owner generation-owner))

(defun undo-request (of request &key (by "rowan")
                                  (stamp "2026-09-14T13:00:00Z") (generation-owner "gen-4"))
  (list :verb :undo :of of :by by :request request
        :stamp stamp :clock :tool :generation-owner generation-owner))

(deftest "metadata-patches-preserve-intent" "docs/SPEC-WORK.md:5798"
    "keep/clear/set-empty/set-false/set-value round-trip and digest distinctly; all-keep/malformed/wrong-type refused; version-on-feature refused"
  (let* ((k (fresh))
         (node "acme/work/f1/t1")
         (d0 (metadata-digest (kernel-state k))))
    (multiple-value-bind (okp line code)
        (submit k (edit-request node :title '(:set "hello") :category '(:set "infra")
                                :links '(:set ("a" "b")) :private '(:set :true)
                                :version '(:set "v1")))
      (ok okp "set-value edit refused: ~A" line)
      (check-equal 0 code "set-value edit exit"))
    (check-string= "hello" (node-title (kernel-state k) node) "title set")
    (check-string= "infra" (node-category (kernel-state k) node) "category set")
    (check-equal '("a" "b") (node-links (kernel-state k) node) "links set")
    (check-equal :true (node-private (kernel-state k) node) "private set")
    (check-string= "v1" (node-version (kernel-state k) node) "version set")
    (let ((d1 (metadata-digest (kernel-state k))))
      (ok (not (string= d0 d1)) "set-value moves the digest")
      (multiple-value-bind (okp2 line2) (submit k (edit-request node :request "edit-keep"
                                                                :title '(:keep)
                                                                :category '(:set "infra")))
        (ok okp2 "keep edit refused: ~A" line2)
        (check-string= d1 (metadata-digest (kernel-state k)) "a keep plus an equal set does not move the digest"))
      (multiple-value-bind (okp3 line3) (submit k (edit-request node :request "edit-clear"
                                                                :title '(:clear)))
        (ok okp3 "clear edit refused: ~A" line3)
        (ok (absentp (node-title (kernel-state k) node)) "clear writes absent")
        (ok (not (string= d1 (metadata-digest (kernel-state k)))) "clear moves the digest"))
      (multiple-value-bind (okp4 line4) (submit k (edit-request node :request "edit-empty"
                                                                :links '(:set ())))
        (ok okp4 "set-empty edit refused: ~A" line4)
        (check-equal '() (node-links (kernel-state k) node) "set-empty writes ()")
        (ok (not (absentp (node-links (kernel-state k) node))) "empty is a value, not absent"))
      (multiple-value-bind (okp5 line5) (submit k (edit-request node :request "edit-false"
                                                                :private '(:set :false)))
        (ok okp5 "set-false edit refused: ~A" line5)
        (check-equal :false (node-private (kernel-state k) node) "set-false writes false")
        (ok (not (absentp (node-private (kernel-state k) node))) "false is a value, not absent")))
    (let ((rev (state-revision (kernel-state k)))
          (hist (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-allkeep"))
        (ok (not okp) "all-keep must refuse")
        (ok (search "all keep" line) "all-keep names itself: ~A" line))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-malformed"
                                                              :title '(:bogus "x")))
        (ok (not okp) "malformed patch must refuse")
        (ok (search "patch" line) "malformed patch is named: ~A" line))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-wrongtype"
                                                              :title '(:set 5)))
        (ok (not okp) "wrong type must refuse")
        (ok (search "title" line) "wrong type names the field: ~A" line))
      (check-equal rev (state-revision (kernel-state k)) "refusals move no revision")
      (check-equal hist (length (state-history (kernel-state k))) "refusals write no history"))
    (let ((rev1 (state-revision (kernel-state k))))
      (multiple-value-bind (okp line) (submit k (edit-request "acme/work/f1" :request "edit-feat-ver"
                                                              :version '(:set "v1")))
        (ok (not okp) "version on a feature must refuse")
        (ok (search "version on a" line) "version refusal names the kind: ~A" line))
      (check-equal rev1 (state-revision (kernel-state k)) "the version refusal writes nothing")
      (multiple-value-bind (okp line) (submit k (edit-request "acme/work/f2" :request "edit-feat-keep"
                                                              :title '(:set "f2") :version '(:keep)))
        (ok okp "keep on a feature admitted: ~A" line)
        (check-equal (1+ rev1) (state-revision (kernel-state k)) "the admitted keep advances the revision")))))

(deftest "edit-is-atomic-and-replayable" "docs/SPEC-WORK.md:5802"
    "bad-patch-writes-nothing; named-fields-only-move; retry-replays-original; changed-payload-refused; equal-value changed=0 rev+1"
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (let ((rev (state-revision (kernel-state k)))
          (hist (length (state-history (kernel-state k)))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-bad"
                                                              :title '(:set "ok")
                                                              :category '(:set 5)))
        (ok (not okp) "a bad one-of-five patch must refuse: ~A" line)
        (check-equal rev (state-revision (kernel-state k)) "bad patch writes no revision")
        (check-equal hist (length (state-history (kernel-state k))) "bad patch writes no history")))
    (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-mixed"
                                                            :title '(:set "t")
                                                            :private '(:set :true)))
      (ok okp "mixed edit refused: ~A" line)
      (check-string= "t" (node-title (kernel-state k) node) "title moved")
      (check-equal :true (node-private (kernel-state k) node) "private moved")
      (ok (absentp (node-category (kernel-state k) node)) "an unnamed field stays put")
      (let ((rev (state-revision (kernel-state k))))
        (multiple-value-bind (okp2 line2) (submit k (edit-request node :request "edit-mixed"
                                                                  :title '(:set "t") :private '(:set :true)))
          (ok okp2 "the same request id retried refused: ~A" line2)
          (check-string= line line2 "the retry replays the original NODE OK")
          (check-equal rev (state-revision (kernel-state k)) "the retry applies nothing"))
        (multiple-value-bind (okp3 line3) (submit k (edit-request node :request "edit-mixed"
                                                                  :title '(:set "other") :private '(:set :true)))
          (ok (not okp3) "a changed payload under the same id must refuse")
          (ok (search "different payload" line3) "names the payload conflict: ~A" line3))))
    (let ((rev (state-revision (kernel-state k))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "edit-equal"
                                                              :title '(:set "t") :private '(:set :true)))
        (ok okp "equal-value edit refused: ~A" line)
        (ok (search "changed=0" line) "changed=0 on the no-effect receipt: ~A" line)
        (check-equal (1+ rev) (state-revision (kernel-state k)) "the no-effect edit advances rev by one")))))

(deftest "no-effect-mutation-is-journaled" "docs/SPEC-WORK.md:5627"
    "event-id-recorded;journal+1;changed=0;projection-digest-unchanged;rev+1"
  (let* ((k (fresh))
         (node "acme/work/f1/t1"))
    (submit k (edit-request node :request "edit-base" :title '(:set "t") :private '(:set :true)))
    (let* ((digest (metadata-digest (kernel-state k)))
           (rev (state-revision (kernel-state k)))
           (journal (kernel-journal k))
           (before (length (journal-order journal))))
      (multiple-value-bind (okp line) (submit k (edit-request node :request "no-effect-1"
                                                              :title '(:set "t") :private '(:set :true)))
        (ok okp "no-effect edit refused: ~A" line)
        (ok (search "changed=0" line) "changed=0: ~A" line)
        (check-equal (1+ before) (length (journal-order journal)) "journal length +1")
        (ok (member "no-effect-1" (journal-order journal) :test #'string=)
            "the event id is recorded in the journal")
        (check-string= digest (metadata-digest (kernel-state k))
                       "the domain-projection digest is unchanged")
        (check-equal (1+ rev) (state-revision (kernel-state k)) "the event revision advances by one")
        (ok (search "no-effect-1" line) "the OK line carries the request id")))))

(deftest "undo-refuses-an-external-effect" "docs/SPEC-WORK.md:5652"
    "sent/paid/published/deleted-refused-as-external;history-never-reset"
  (let ((k (fresh))
        (handles '()))
    (dolist (spec '(("ext-sent" :sent "msg-1")
                    ("ext-paid" :paid "inv-2")
                    ("ext-pub" :published "note-3")
                    ("ext-del" :deleted "src-4")))
      (destructuring-bind (rid effect handle) spec
        (push handle handles)
        (multiple-value-bind (okp line)
            (submit k (list :verb :external-effect :node "acme/work/f1/t1" :by "rowan"
                            :effect effect :handle handle :request rid
                            :stamp "2026-09-14T12:00:00Z" :clock :tool
                            :generation-owner "gen-4"))
          (ok okp "recording external ~A refused: ~A" effect line)
          (let ((hist-before (length (state-history (kernel-state k)))))
            (multiple-value-bind (uokp uline)
                (submit k (undo-request rid (concatenate 'string "undo-" rid)))
              (ok (not uokp) "an undo over ~A must refuse" effect)
              (ok (search "effect=external" uline) "names the external effect: ~A" uline)
              (ok (search (string-downcase (symbol-name effect)) uline)
                  "names the external kind: ~A" uline)
              (ok (search handle uline) "names the external handle: ~A" uline)
              (ok (search "not reversible here" uline) "refused as not reversible: ~A" uline)
              (check-equal hist-before (length (state-history (kernel-state k)))
                           "a refused undo writes no history"))))))
    ;; shared history is never reset: the four recorded effects still stand.
    (dolist (rid '("ext-sent" "ext-paid" "ext-pub" "ext-del"))
      (ok (member rid (journal-order (kernel-journal k)) :test #'string=)
          "~A still stands in the journal" rid))))

(deftest "undo-names-its-reversible-set" "docs/SPEC-WORK.md:5783"
    "each-reversible-verb-undone-by-table;refused-verb-refused-named;terminal-dispositions-refused"
  (let ((k (fresh)))
    (let ((node "acme/work/f1/t1"))
      (submit k (edit-request node :request "edit-r1" :title '(:set "t")))
      (check-string= "t" (node-title (kernel-state k) node) "the edit is applied")
      (multiple-value-bind (okp line) (submit k (undo-request "edit-r1" "undo-edit-r1"))
        (ok okp "an undo of node edit refused: ~A" line)
        (ok (absentp (node-title (kernel-state k) node)) "the compensating edit restores :before")
        (ok (search "UNDO OK" line) "the undo appends its own envelope: ~A" line)))
    (let ((node "acme/work/f1/t2"))
      (check-equal :review (node-state (kernel-state k) node) "preimage is review")
      (submit k (list :verb :state-to-doing :node node :by "rowan" :reason "start"
                      :request "to-doing-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                      :generation-owner "gen-4"))
      (check-equal :doing (node-state (kernel-state k) node) "moved to doing")
      (multiple-value-bind (okp line) (submit k (undo-request "to-doing-1" "undo-to-doing-1"))
        (ok okp "an undo of a transition refused: ~A" line)
        (check-equal :review (node-state (kernel-state k) node) "the preimage state is restored")))
    (submit k (list :verb :external-effect :node "acme/work/f1/t1" :by "rowan"
                    :effect :sent :handle "m-1" :request "ext-1"
                    :stamp "2026-09-14T12:00:00Z" :clock :tool :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-request "ext-1" "undo-ext-1"))
      (ok (not okp) "an undo over a recorded effect must refuse")
      (ok (search "not reversible here" line) "refused by name: ~A" line))
    (submit k (list :verb :node-remove :node "acme/work/f1/t1" :by "rowan" :reason "drop"
                    :request "rm-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-request "rm-1" "undo-rm-1"))
      (ok (not okp) "an undo over node remove must refuse")
      (ok (search "not reversible here" line) "the terminal remove is refused: ~A" line))
    (submit k (list :verb :event-cancel :node "acme/work/f1/t2" :by "rowan" :reason "drop"
                    :request "cancel-1" :stamp "2026-09-14T12:00:00Z" :clock :tool
                    :generation-owner "gen-4"))
    (multiple-value-bind (okp line) (submit k (undo-request "cancel-1" "undo-cancel-1"))
      (ok (not okp) "an undo over event cancel must refuse")
      (ok (search "not reversible here" line) "the terminal cancel is refused: ~A" line))))
