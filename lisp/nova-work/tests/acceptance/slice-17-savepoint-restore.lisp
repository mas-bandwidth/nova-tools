;;;; slice-17-savepoint-restore.lisp --- `savepoint list`, `savepoint verify`
;;;; and the isolated read-only `savepoint restore` (nova-work E05 rows 2, 3
;;;; and 4).
;;;;
;;;; SPEC-WORK.md:2275-2278  the three verbs' grammar; restore is isolated and
;;;;                         read-only, takes no ownership, dispatches nothing
;;;;                         and replays no message
;;;; SPEC-WORK.md:5983,5986  the OK and FAIL lines
;;;; SPEC-WORK.md:6737-6738  a copied journal grants nothing
;;;; SPEC-WORK.md:7117-7119  list exposes age and the local and shared
;;;;                         revisions side by side, and the failed attempts
;;;; SPEC-WORK.md:7165-7171  restoring one is five steps; every gap named by
;;;;                         kind and never a truncation or a rollback
;;;;
;;;; Every byte these read is a file `savepoint create` published.

(in-package #:nova-work/tests)

(defun savepoint-fixture (&key (id "sp-r") (mutations 2))
  "Publish one savepoint over a real journal and answer everything the three
verbs need: the kernel, its journal, the journal path, the savepoint root and
the revision the image was taken at."
  (let* ((seed *seed*)
         (init-digest (root-digest (make-seed-state seed)))
         (journal-path (test-journal-path "savepoint-restore"))
         (root (test-savepoint-root))
         (journal (open-file-journal journal-path :initial-state-hash init-digest))
         (k (make-kernel :state (make-seed-state seed) :journal journal)))
    (when (>= mutations 1)
      (submit k (close-request :node "acme/work/f1/t1" :request "spr-1")))
    (when (>= mutations 2)
      (submit k (close-request :node "acme/work/f1/t2" :request "spr-2")))
    (multiple-value-bind (okp line) (savepoint-create k :root root :id id :as "rowan")
      (unless okp (fail "the fixture's savepoint was refused: ~A" line)))
    (list :kernel k :journal journal :journal-path journal-path :root root
          :revision (state-revision (kernel-state k)) :id id)))

;;; ------------------------------------------------------------------
;;; savepoint-verify-reads-the-published-bytes     SPEC-WORK.md:7165-7171
;;; ------------------------------------------------------------------

(deftest "savepoint-verify-reads-the-published-bytes" "docs/SPEC-WORK.md:2277,5986,6737,7165-7171"
    "expected=the-manifest-journal-id-and-exact-cut-verified;every-gap-named-by-kind;a-copied-journal-grants-nothing;nothing-rolled-back"
  (let* ((fx (savepoint-fixture :id "sp-v"))
         (root (getf fx :root))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (progn
           (multiple-value-bind (okp line code report)
               (savepoint-verify-published root "sp-v" :journal-path journal-path)
             (ok okp "a freshly published savepoint does not verify: ~A" line)
             (check-equal 0 code "it exits 0")
             (ok (search "SAVEPOINT OK id=sp-v" line) "the OK line: ~A" line)
             (check-equal (getf fx :revision) (savepoint-report-revision report)
                          "the report's revision is not the image's")
             (check-equal nil (savepoint-report-gap report) "a green verify named a gap"))

           ;; The savepoint names the journal it was cut from, and a savepoint
           ;; held against another journal is `journal mismatch`
           ;; (SPEC-WORK.md:5986, :6737-6739). The manifest holds no digest of
           ;; itself (:7140), so a manifest naming another journal is a thing a
           ;; reader can meet, and it must be refused rather than replayed.
           (let* ((manifest-path (savepoint-manifest-path root "sp-v"))
                  (before (savepoint-file-text manifest-path))
                  (manifest (read-savepoint-manifest root "sp-v")))
             (with-open-file (out manifest-path :direction :output
                                                :element-type 'character
                                                :external-format :utf-8
                                                :if-exists :supersede)
               (write-string (canonical-string
                              (append (list :schema (getf manifest :schema)
                                            :journal (sha256-hex "another bench's journal"))
                                      (list :local-revision (getf manifest :local-revision)
                                            :replay-cut (getf manifest :replay-cut)
                                            :boundary (getf manifest :boundary)
                                            :state (getf manifest :state)
                                            :local-replies (getf manifest :local-replies))))
                             out))
             (multiple-value-bind (okp line code)
                 (savepoint-verify-published root "sp-v" :journal-path journal-path)
               (check-equal nil okp "a savepoint naming another journal verified")
               (check-equal 1 code "the refusal exits 1")
               (ok (search "journal mismatch" line)
                   "the refusal names the journal: ~A" line))
             ;; a restore over it grants nothing at all: no state is exposed
             (multiple-value-bind (okp line code restored)
                 (savepoint-restore-published root "sp-v" :journal-path journal-path)
               (declare (ignore line code))
               (check-equal nil okp "a copied journal's savepoint was restored")
               (ok (not (restored-savepoint-p restored))
                   "a refused restore exposed a state"))
             (with-open-file (out manifest-path :direction :output
                                                :element-type 'character
                                                :external-format :utf-8
                                                :if-exists :supersede)
               (write-string before out)))

           ;; A BROKEN HASH is a recovery gap by its kind, and the bytes stay
           ;; where they are (SPEC-WORK.md:7169-7171).
           (let* ((image-path (merge-pathnames "state" (savepoint-directory root "sp-v")))
                  (before (savepoint-file-text image-path)))
             (with-open-file (out image-path :direction :output :element-type 'character
                                             :external-format :utf-8 :if-exists :append)
               (write-string " " out))
             (multiple-value-bind (okp line code report)
                 (savepoint-verify-published root "sp-v" :journal-path journal-path)
               (check-equal nil okp "a savepoint with a changed image verified")
               (check-equal 1 code "the refusal exits 1")
               (ok (search "broken hash" line) "the gap is named by kind: ~A" line)
               (check-equal "broken hash" (savepoint-report-gap report)
                            "the report does not carry the gap's kind"))
             ;; nothing was rolled back or truncated: the file is longer, not shorter
             (ok (> (length (savepoint-file-text image-path)) (length before))
                 "the refusal truncated the image")
             ;; put it back for the rest of the test
             (with-open-file (out image-path :direction :output :element-type 'character
                                             :external-format :utf-8
                                             :if-exists :supersede)
               (write-string before out)))

           ;; A savepoint that was never published verifies as no manifest.
           (multiple-value-bind (okp line)
               (savepoint-verify-published root "sp-never" :journal-path journal-path)
             (check-equal nil okp "an unpublished savepoint verified")
             (ok (search "no published manifest" line) "the gap is named: ~A" line))

           ;; and the good savepoint still verifies after all of it
           (multiple-value-bind (okp line)
               (savepoint-verify-published root "sp-v" :journal-path journal-path)
             (ok okp "the savepoint stopped verifying after the refusals: ~A" line)))
      (close-file-journal (getf fx :journal)))))

;;; ------------------------------------------------------------------
;;; savepoint-list-names-local-and-shared-apart    SPEC-WORK.md:7117-7119
;;; ------------------------------------------------------------------

(deftest "savepoint-list-names-local-and-shared-apart" "docs/SPEC-WORK.md:2275,5983,7117-7119"
    "expected=age-and-the-local-and-shared-revisions-side-by-side;failed-attempts-listed;a-savepoint-never-printed-where-a-checkpoint-was-asked-for"
  (let* ((fx (savepoint-fixture :id "sp-l1"))
         (root (getf fx :root))
         (journal-path (getf fx :journal-path))
         (k (getf fx :kernel)))
    (unwind-protect
         (progn
           ;; a second published savepoint, and one attempt that published nothing
           (ok (savepoint-create k :root root :id "sp-l2" :as "rowan") "a second savepoint")
           (multiple-value-bind (okp)
               (savepoint-create k :root root :id "sp-l3" :as "rowan" :fail-stage :manifest)
             (check-equal nil okp "the failed attempt published anyway"))
           (multiple-value-bind (line rows list)
               (savepoint-list-published root :shared-revision 7 :journal-path journal-path)
             (declare (ignore list))
             (ok (search "SAVEPOINT OK savepoints=3" line) "three rows: ~A" line)
             (ok (search "verified=2" line) "two verified: ~A" line)
             (ok (search "failed=1" line) "one failed attempt: ~A" line)
             (ok (search "checkpoint=7" line)
                 "the shared revision is printed beside the local ones: ~A" line)
             (check-equal 3 (length rows) "one row per savepoint")
             (let ((failed (find-if (lambda (r) (search "id=sp-l3" r)) rows)))
               (ok failed "the failed attempt has no row")
               (ok (search "verdict=failed" failed) "its verdict: ~A" failed)
               (ok (search "gap=no published manifest" failed) "its gap: ~A" failed))
             (let ((good (find-if (lambda (r) (search "id=sp-l1" r)) rows)))
               (ok good "the first savepoint has no row")
               (ok (search "verdict=verified" good) "its verdict: ~A" good)
               (ok (search (format nil "rev=~D" (getf fx :revision)) good)
                   "its local revision: ~A" good)
               (ok (search "checkpoint=7" good)
                   "the shared revision stands beside it: ~A" good)
               (ok (search "age=" good) "its age: ~A" good)
               ;; the local savepoint is never printed where a checkpoint was
               ;; asked for (SPEC-WORK.md:5983, :7109)
               (ok (not (search (format nil "checkpoint=~D" (getf fx :revision)) good))
                   "a local revision was printed as the shared checkpoint: ~A" good))))
      (close-file-journal (getf fx :journal)))))

;;; ------------------------------------------------------------------
;;; restore-is-isolated-and-dispatches-nothing     SPEC-WORK.md:2278,7165-7168
;;; ------------------------------------------------------------------

(deftest "restore-is-isolated-and-dispatches-nothing" "docs/SPEC-WORK.md:2278,6738,7165-7168"
    "expected=validation-before-anything-is-exposed;only-records-strictly-after-the-cut-replayed-once;no-ownership-no-dispatch-no-message;bounds-refused-by-name"
  (let* ((fx (savepoint-fixture :id "sp-x"))
         (root (getf fx :root))
         (journal-path (getf fx :journal-path))
         (k (getf fx :kernel))
         (at-cut (getf fx :revision)))
    (unwind-protect
         (progn
           ;; work accepted AFTER the savepoint was taken
           (ok (submit k (reopen-request :node "acme/work/f1/t1" :request "spx-3"))
               "a third accepted mutation, after the cut")
           (let ((now (state-revision (kernel-state k))))
             (ok (> now at-cut) "the live session moved past the cut"))

           (multiple-value-bind (okp line code restored)
               (savepoint-restore-published root "sp-x" :journal-path journal-path)
             (ok okp "the restore was refused: ~A" line)
             (check-equal 0 code "it exits 0")
             ;; only the complete records STRICTLY AFTER the cut, once
             (check-equal 1 (restored-savepoint-replayed restored)
                          "the records after the cut were not replayed exactly once")
             (ok (search "replayed=1" line) "the line names what it replayed: ~A" line)
             ;; the restored state agrees with the live session it was cut from
             (check-equal (state-revision (kernel-state k))
                          (restored-savepoint-revision restored)
                          "the restore did not catch up to the live revision")
             (check-equal (state-open-count (kernel-state k))
                          (state-open-count (restored-savepoint-state restored))
                          "the restored |O| differs")
             (check-equal (state-closed-count (kernel-state k))
                          (state-closed-count (restored-savepoint-state restored))
                          "the restored |C| differs")
             (check-equal :o (node-branch (restored-savepoint-state restored)
                                          "acme/work/f1/t1")
                          "the reopen after the cut was not replayed")
             ;; isolated, read-only, non-dispatching (SPEC-WORK.md:2278)
             (check-equal nil (restored-savepoint-ownership-taken-p restored)
                          "the restore took ownership")
             (check-equal nil (restored-savepoint-dispatched-p restored)
                          "the restore dispatched something")
             (check-equal 0 (restored-savepoint-messages-replayed restored)
                          "the restore replayed a message")
             ;; the original replies came back with it
             (ok (find "spr-1" (restored-savepoint-replies restored)
                       :key (lambda (r) (getf r :request)) :test #'equal)
                 "the retained reply for spr-1 did not come back"))

           ;; The live session is untouched by an isolated restore.
           (check-equal :o (node-branch (kernel-state k) "acme/work/f1/t1")
                        "the live session moved under the restore")

           ;; The whole validation runs BEFORE anything is exposed: a savepoint
           ;; that does not verify exposes no state at all.
           (let ((image-path (merge-pathnames "state" (savepoint-directory root "sp-x"))))
             (let ((before (savepoint-file-text image-path)))
               (with-open-file (out image-path :direction :output :element-type 'character
                                               :external-format :utf-8 :if-exists :append)
                 (write-string " " out))
               (multiple-value-bind (okp line code restored)
                   (savepoint-restore-published root "sp-x" :journal-path journal-path)
                 (check-equal nil okp "a broken savepoint was restored anyway")
                 (check-equal 1 code "the refusal exits 1")
                 (ok (search "broken hash" line) "the gap is named by kind: ~A" line)
                 (ok (not (restored-savepoint-p restored))
                     "a refused restore exposed a state"))
               (with-open-file (out image-path :direction :output :element-type 'character
                                               :external-format :utf-8 :if-exists :supersede)
                 (write-string before out))))

           ;; The bounds the grammar requires are refused by name.
           (multiple-value-bind (okp line code)
               (savepoint-restore-published root "sp-x" :journal-path journal-path
                                                        :max-bytes 8)
             (check-equal nil okp "an image past --max-bytes was restored")
             (check-equal 1 code "the refusal exits 1")
             (ok (search "--max-bytes" line) "the refusal names the bound: ~A" line))
           (multiple-value-bind (okp line code)
               (savepoint-restore-published root "sp-x" :journal-path journal-path
                                                        :max-nodes 1)
             (check-equal nil okp "a set past --max-nodes was restored")
             (check-equal 1 code "the refusal exits 1")
             (ok (search "--max-nodes" line) "the refusal names the bound: ~A" line)))
      (close-file-journal (getf fx :journal)))))
