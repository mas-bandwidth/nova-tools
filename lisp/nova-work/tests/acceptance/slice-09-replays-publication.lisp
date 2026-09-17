;;;; slice-09-replays-publication.lisp --- one replay slice of the acceptance
;;;; suite. Loaded by ../acceptance.lisp; an amendment edits one slice file.
;;;;
;;;; The five publication/recovery replays of docs/SPEC-WORK.md:716-762, each
;;;; asserting the sentence it is named from: one revision publishes together,
;;;; the journal replay rebuilds tree and index together through a bounded
;;;; overlay, and an absent day and a missing segment are two different answers.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; One revision publishes together. SPEC-WORK.md:716-734.
;;; ------------------------------------------------------------------

(deftest "one-revision-publishes-together" "docs/SPEC-WORK.md:716-734"
    "expected=segments-indexes-and-files-named-by-one-revision;reader-never-sees-a-root-pointing-at-a-missing-file"
  (let* ((files '(("state/snapshot.sexp" . "aaa")
                  ("closed/index.sexp" . "bbb")
                  ("dedup/index.sexp" . "ccc")
                  ("closed/segments/2026-09-14-s1.sexp" . "ddd")))
         (pub (make-publication :revision 42 :snapshot-sha "aaa" :closed-sha "bbb"
                                :dedup-sha "ccc" :manifests '("2026-09-14") :files files)))
    ;; One revision names the snapshot, both index roots, the day manifests and
    ;; the closure segments together.
    (check-equal 42 (pub-revision pub) "the publication revision")
    (check-string= "aaa" (pub-snapshot-sha pub) "the snapshot is not named")
    (check-string= "bbb" (pub-closed-sha pub) "the closed index root is not named")
    (check-string= "ccc" (pub-dedup-sha pub) "the dedup index root is not named")
    (check-equal '("2026-09-14") (pub-manifests pub) "the day manifests are not named")
    ;; Every file the revision references is present with its recorded hash.
    (multiple-value-bind (ok missing) (verify-publication pub files)
      (ok ok "a complete publication did not verify")
      (ok (null missing) "a complete publication reported missing files"))
    ;; A member removed before the commit leaves the reader a missing file, and
    ;; the publication is refused rather than read as whole.
    (multiple-value-bind (ok missing) (verify-publication pub (cdr files))
      (ok (not ok) "a publication with a missing member verified clean")
      (check-equal '("state/snapshot.sexp") missing "the missing member was not named"))))

;;; ------------------------------------------------------------------
;;; Index replayed after a crash. SPEC-WORK.md:723-734.
;;; ------------------------------------------------------------------

(deftest "index-replayed-after-crash" "docs/SPEC-WORK.md:723-734"
    "expected=one-journal-replay-recovers-c-and-o;overlay-covers-each-settle-exactly-once"
  (let* ((seed '((:id "a" :type :task :state :doing :links ("https://x/1"))
                 (:id "b" :type :task :state :doing :links ("https://x/2"))))
         (init-digest (root-digest (make-seed-state seed)))
         (path (test-journal-path "index-replay-publication"))
         (j1 (open-file-journal path :initial-state-hash init-digest)))
    (unwind-protect
        (progn
          (let ((k1 (make-kernel :state (make-seed-state seed) :journal j1)))
            (ok (submit k1 (close-request :node "a" :request "irp-1")) "settle a")
            (ok (submit k1 (close-request :node "b" :request "irp-2")) "settle b"))
          (close-file-journal j1)
          (let* ((j2 (open-file-journal path :initial-state-hash init-digest))
                 (k2 (make-kernel :state (make-seed-state seed) :journal j2)))
            (unwind-protect
                (progn
                  (replay-journal j2 k2)
                  (check-equal :c (node-branch (kernel-state k2) "a") "a recovered into C")
                  (check-equal :c (node-branch (kernel-state k2) "b") "b recovered into C")
                  (check-equal 0 (state-open-count (kernel-state k2)) "|O| after recovery")
                  (check-equal 2 (state-closed-count (kernel-state k2)) "|C| after recovery")
                  (check-equal 2 (+ (state-open-count (kernel-state k2))
                                    (state-closed-count (kernel-state k2)))
                               "the recovered branches do not sum to the two ids")
                  ;; The same replay builds the index overlay: each settle appears
                  ;; exactly once, in one bounded pass, and never per query.
                  (let ((ov (replay-overlay (list (list :kind :settle :node "a" :rev 1)
                                                  (list :kind :settle :node "b" :rev 2))
                                            64 25)))
                    (check-equal 2 (length (ov-entries ov)) "a settle was lost or doubled")
                    (check-equal '("a" "b")
                                 (mapcar (lambda (e) (getf e :node)) (ov-entries ov))
                                 "the overlay did not cover the settles in order")
                    (ok (<= (ov-pages ov) 64) "the overlay exceeded the index cache")))
              (close-file-journal j2))))
      (ignore-errors (delete-file path)))))

;;; ------------------------------------------------------------------
;;; The overlay is bounded and rebuilt. SPEC-WORK.md:735-746.
;;; ------------------------------------------------------------------

(deftest "overlay-is-bounded-and-rebuilt" "docs/SPEC-WORK.md:735-746"
    "expected=rebuilt-from-the-journal-and-never-replayed-per-query;bounded-by-the-index-cache"
  (let* ((events (loop for i from 1 to 120
                       collect (list :kind :settle :node (format nil "n-~D" i) :rev i)))
         (index-cache 8)
         (page-records 25)
         (ov (replay-overlay events index-cache page-records)))
    ;; The overlay is held in bounded paged scratch under the index cache.
    (check-equal 5 (ov-pages ov) "the overlay pages are not ceil(entries/page-records)")
    (ok (<= (ov-pages ov) index-cache) "the overlay pages exceed the index cache")
    (check-equal 120 (length (ov-entries ov)) "rebuilding lost an overlay entry")
    ;; Rebuilding reproduces the same overlay deterministically, from the journal.
    (let ((again (replay-overlay events index-cache page-records)))
      (check-equal (ov-entries ov) (ov-entries again) "a rebuild drifted from the journal"))))

;;; ------------------------------------------------------------------
;;; An absent day is not a gap. SPEC-WORK.md:747-762.
;;; ------------------------------------------------------------------

(deftest "absent-day-is-not-a-gap" "docs/SPEC-WORK.md:747-762"
    "expected=no-manifest-means-no-events-that-day,not-a-coverage-gap"
  (let* ((seg (make-closed-segment
               :name "closed/segments/2026-09-14-s1.sexp"
               :entries (list (make-closed-entry :rev 1 :id "T-1"
                                                 :stamp "2026-09-14T12:00:00Z"))))
         (man (manifest-for-day "2026-09-14" (list seg)))
         (table (list (cons (seg-name seg) seg))))
    (multiple-value-bind (rows gap notes)
        (closed-day-selection '("2026-09-13" "2026-09-14") (list man) table)
      (check-equal 1 (length rows) "the absent day's neighbour lost its rows")
      (check-equal 0 gap "an absent day was reported as a coverage gap")
      (check-equal '() notes "an absent day produced a coverage-gap note"))))

;;; ------------------------------------------------------------------
;;; A missing segment is a gap. SPEC-WORK.md:747-762.
;;; ------------------------------------------------------------------

(deftest "missing-segment-is-a-gap" "docs/SPEC-WORK.md:747-762"
    "expected=missing-segment-prints-coverage-gap-and-never-an-empty-closed-set"
  (let* ((seg (make-closed-segment
               :name "closed/segments/2026-09-14-s1.sexp"
               :entries (list (make-closed-entry :rev 1 :id "T-1"
                                                 :stamp "2026-09-14T12:00:00Z"))))
         (man (manifest-for-day "2026-09-14" (list seg)))
         (table '()))                    ; the manifest names a segment the disk does not hold
    (multiple-value-bind (rows gap notes)
        (closed-day-selection '("2026-09-14") (list man) table)
      (check-equal 0 (length rows) "a missing segment was answered as rows")
      (check-equal 1 gap "a missing segment was not counted as a coverage gap")
      (check-equal 1 (length notes) "a missing segment printed no note")
      (ok (search "QUERY NOTE coverage-gap" (first notes))
          "the note is not a coverage-gap note"))))
