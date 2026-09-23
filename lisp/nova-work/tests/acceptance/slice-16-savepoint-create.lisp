;;;; slice-16-savepoint-create.lisp --- `savepoint create` over the real
;;;; resident state, the real journal and real files (nova-work E05 row 1).
;;;;
;;;; SPEC-WORK.md:2276       the grammar
;;;; SPEC-WORK.md:5983,5986  the OK and FAIL lines
;;;; SPEC-WORK.md:7131-7147  the manifest's fields, and the cut that can never
;;;;                         fall between an envelope's events
;;;; SPEC-WORK.md:7161-7172  writing one is four steps; a failure at any of them
;;;;                         keeps the prior verified savepoint; a torn tail is
;;;;                         diagnosed and never a truncation
;;;;
;;;; `slice-08-replays-late.lisp` keeps the pure savepoint model of the same
;;;; paragraph; this file is the verb, and every object it names is a file.

(in-package #:nova-work/tests)

(defun test-savepoint-root ()
  "A fresh savepoint root under this run's own root. The old name --
`nova-work-test-savepoints/<universal-time>-<counter>/` under the shared
temporary directory -- was identical in two suites that started inside one
second (nova-tools#1699)."
  (test-temp-dir "savepoint"))

(defun savepoint-file-text (path)
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (let ((text (make-string (file-length in))))
      (subseq text 0 (read-sequence text in)))))

(defun savepoint-token (line key)
  (let ((start (search (concatenate 'string key "=") line)))
    (when start
      (let* ((begin (+ start (length key) 1))
             (end (or (position #\Space line :start begin) (length line))))
        (subseq line begin end)))))

;;; ------------------------------------------------------------------
;;; savepoint-create-publishes-a-validated-image  SPEC-WORK.md:7131-7165
;;; ------------------------------------------------------------------

(deftest "savepoint-create-publishes-a-validated-image" "docs/SPEC-WORK.md:2276,5983,7131-7165"
    "expected=four-steps-over-real-files;the-manifest-names-the-image-on-disk-by-its-exact-identity;the-cut-is-the-last-complete-journal-record;local-and-shared-revisions-never-reported-as-one"
  (let* ((seed *seed*)
         (init-digest (root-digest (make-seed-state seed)))
         (journal-path (test-journal-path "savepoint-create"))
         (root (test-savepoint-root))
         (journal (open-file-journal journal-path :initial-state-hash init-digest)))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state seed) :journal journal)))
           (ok (submit k (close-request :node "acme/work/f1/t1" :request "sp-1"))
               "one accepted mutation")
           (ok (submit k (close-request :node "acme/work/f1/t2" :request "sp-2"))
               "a second accepted mutation")
           (multiple-value-bind (okp line code store)
               (savepoint-create k :root root :id "sp-a" :as "rowan"
                                   :reason "the first savepoint"
                                   :checkpoint-revision 2 :age "30s" :unshared 4)
             (ok okp "savepoint create refused: ~A" line)
             (check-equal 0 code "it exits 0")
             (ok (search "SAVEPOINT OK id=sp-a" line) "the OK line: ~A" line)
             ;; The savepoint is local and the checkpoint is shared, and the two
             ;; are never reported as one (SPEC-WORK.md:5983, :7109).
             (check-equal "2" (savepoint-token line "checkpoint")
                          "checkpoint= is the shared revision, printed apart")
             (check-equal "4" (savepoint-token line "unshared")
                          "unshared= names the work no clip carries")
             (ok (not (equal (savepoint-token line "rev")
                             (savepoint-token line "checkpoint")))
                 "a local success was printed as a shared backup: ~A" line)

             ;; Step 2: the objects are real files.
             (let* ((dir (savepoint-directory root "sp-a"))
                    (state-path (merge-pathnames "state" dir))
                    (replies-path (merge-pathnames "local-replies" dir))
                    (manifest-path (savepoint-manifest-path root "sp-a")))
               (ok (probe-file state-path) "the image was written")
               (ok (probe-file replies-path) "the retained replies were written")
               (ok (probe-file manifest-path) "the manifest was published")
               (ok (null (probe-file (merge-pathnames "manifest.candidate" dir)))
                   "the candidate manifest was left behind")

               ;; Step 3: the manifest names the objects by their EXACT
               ;; identity, taken over the bytes the disk holds.
               (let* ((manifest (read-savepoint-manifest root "sp-a"))
                      (state-ref (getf manifest :state))
                      (replies-ref (getf manifest :local-replies))
                      (state-bytes (savepoint-file-text state-path))
                      (replies-bytes (savepoint-file-text replies-path)))
                 (check-equal "work-savepoint-v1" (getf manifest :schema)
                              "the manifest names its schema")
                 (check-equal (sha256-hex state-bytes) (getf state-ref :sha256)
                              "the manifest's image hash is not the image on disk")
                 (check-equal (length state-bytes) (getf state-ref :size)
                              "the manifest's image size is not the image on disk")
                 (check-equal (sha256-hex replies-bytes) (getf replies-ref :sha256)
                              "the manifest's replies hash is not the replies on disk")
                 (check-equal "state" (getf state-ref :path)
                              "the reference is inside the savepoint's own root")
                 (check-equal (sha256-hex (savepoint-file-text manifest-path))
                              (savepoint-token line "manifest")
                              "manifest= is not the published manifest's own bytes")
                 (check-equal (journal-file-identity journal) (getf manifest :journal)
                              "the manifest names the journal it was cut from")

                 ;; The image is the resident state whole, at one revision.
                 (let ((rebuilt (reconstruct-state state-bytes)))
                   (check-equal (state-revision (kernel-state k))
                                (state-revision rebuilt)
                                "the image is not the state's own revision")
                   (check-equal (getf manifest :local-revision)
                                (state-revision rebuilt)
                                "the manifest's local revision is not the image's")
                   (check-equal (state-open-count (kernel-state k))
                                (state-open-count rebuilt)
                                "the image lost |O|")
                   (check-equal (state-closed-count (kernel-state k))
                                (state-closed-count rebuilt)
                                "the image lost |C|"))

                 ;; The cut names one COMPLETE journal record, and its hash is
                 ;; that record's own (SPEC-WORK.md:7135-7147). The boundary
                 ;; record is distinct from the cut and also names the dedup
                 ;; root, the last being the digest of the object on disk
                 ;; (SPEC-WORK.md:7155-7164).
                 (let* ((scan (scan-journal-file journal-path))
                        (records (journal-scan-records scan))
                        (last (car (last records)))
                        (cut (getf manifest :replay-cut))
                        (boundary (getf manifest :boundary)))
                   (check-equal nil (journal-scan-torn-p scan) "the journal tail is torn")
                   (check-equal 2 (length records) "the journal holds two records")
                   (check-equal (getf last :sequence) (getf cut :sequence)
                                "the cut is not the last complete record")
                   (check-equal (getf last :record-sha256) (getf cut :sha256)
                                "the cut's hash is not that record's own")
                   (check-equal (getf last :sequence) (getf boundary :sequence)
                                "the boundary is not the root's last entry")
                   (check-equal (getf last :record-sha256) (getf boundary :sha256)
                                "the boundary hash is not that record's own")
                   (check-equal (sha256-hex (savepoint-file-text
                                             (merge-pathnames "dedup-root" dir)))
                                (getf boundary :dedup-root)
                                "the boundary does not name the dedup root on disk")
                   (check-equal (format nil "~D" (getf cut :sequence))
                                (savepoint-token line "boundary")
                                "boundary= is not the cut the image reflects")
                   ;; Each retained disposition: its request id, payload digest,
                   ;; sequence, record hash and the ORIGINAL reply (:7137-7141).
                   (let ((reply (find "sp-1" records :key (lambda (r) (getf r :request))
                                                     :test #'equal)))
                     (ok reply "the retained reply for sp-1 is missing")
                     (ok (search "STATE OK" (getf reply :reply))
                         "the original reply was not retained: ~A" (getf reply :reply))
                     (ok (plusp (length (getf reply :payload-sha256)))
                         "the retained disposition carries no payload digest")
                     (ok (plusp (length (getf reply :record-sha256)))
                         "the retained disposition carries no record hash")))))

             ;; The store carries the savepoint it just verified.
             (let ((sp (savepoint-store-verified store)))
               (ok sp "no verified savepoint was returned")
               (check-equal "sp-a" (savepoint-id sp) "the verified savepoint's id")
               (check-equal (state-revision (kernel-state k))
                            (savepoint-local-revision sp)
                            "the verified savepoint's local revision"))
             (check-equal :ok (getf (first (savepoint-store-attempts store)) :verdict)
                          "the attempt was not listed ok")))
      (close-file-journal journal))))

;;; ------------------------------------------------------------------
;;; savepoint-write-failure-keeps-the-previous     SPEC-WORK.md:7165-7172
;;; ------------------------------------------------------------------

(deftest "savepoint-write-failure-keeps-the-previous" "docs/SPEC-WORK.md:5986,7165-7172"
    "expected=a-failure-at-any-of-the-four-steps-publishes-nothing;the-prior-verified-savepoint-restores;a-torn-tail-is-diagnosed-not-truncated"
  (let* ((seed *seed*)
         (init-digest (root-digest (make-seed-state seed)))
         (journal-path (test-journal-path "savepoint-failure"))
         (root (test-savepoint-root))
         (journal (open-file-journal journal-path :initial-state-hash init-digest)))
    (unwind-protect
         (let ((k (make-kernel :state (make-seed-state seed) :journal journal))
               (store nil))
           (ok (submit k (close-request :node "acme/work/f1/t1" :request "spf-1"))
               "one accepted mutation")
           (multiple-value-bind (okp line code good)
               (savepoint-create k :root root :id "sp-good" :as "rowan")
             (declare (ignore line code))
             (ok okp "the first savepoint is published")
             (setf store good))
           (let ((verified (savepoint-store-verified store)))
             ;; Each of the three writing steps, failed in turn
             ;; (SPEC-WORK.md:7165-7168).
             (dolist (stage '(:image :manifest :sync))
               (let ((id (format nil "sp-~A" (string-downcase (symbol-name stage)))))
                 (multiple-value-bind (okp line code after)
                     (savepoint-create k :root root :id id :as "rowan"
                                         :store store :fail-stage stage)
                   (check-equal nil okp (format nil "a failed ~A step published anyway" stage))
                   (check-equal 1 code "the failure exits 1")
                   (ok (search (format nil "SAVEPOINT FAIL id=~A" id) line)
                       "the FAIL line names the savepoint: ~A" line)
                   (ok (null (probe-file (savepoint-manifest-path root id)))
                       "a failed ~A step published a manifest" stage)
                   (ok (null (probe-file (merge-pathnames "manifest.candidate"
                                                          (savepoint-directory root id))))
                       "a failed ~A step left a candidate manifest" stage)
                   ;; the prior verified savepoint is kept through it
                   (check-equal (savepoint-id verified)
                                (savepoint-id (savepoint-store-verified after))
                                "the prior verified savepoint was lost")
                   (check-equal :failed (getf (first (savepoint-store-attempts after)) :verdict)
                                "the attempt was not listed failed")
                   (check-equal stage (getf (first (savepoint-store-attempts after)) :stage)
                                "the attempt did not name the step that failed"))))
             ;; the previous verified savepoint still restores from its files
             (let ((manifest (read-savepoint-manifest root "sp-good")))
               (ok manifest "the previous savepoint's manifest is gone")
               (check-equal (sha256-hex (savepoint-file-text
                                         (merge-pathnames "state"
                                                          (savepoint-directory root "sp-good"))))
                            (getf (getf manifest :state) :sha256)
                            "the previous savepoint's image no longer matches its manifest"))
             ;; and a good write after the failures still publishes
             (multiple-value-bind (okp line)
                 (savepoint-create k :root root :id "sp-after" :as "rowan" :store store)
               (ok okp "a good write after the failures is refused: ~A" line)
               (ok (probe-file (savepoint-manifest-path root "sp-after"))
                   "it published no manifest"))
             ;; A TORN TAIL is evidence of an interrupted append: it is
             ;; diagnosed, no savepoint is published over it, and the journal is
             ;; not truncated (SPEC-WORK.md:7171-7172). The half-frame is
             ;; appended to the live journal file, which is what an interrupted
             ;; append leaves behind.
             (let ((size-before (with-open-file (in journal-path
                                                    :element-type '(unsigned-byte 8))
                                  (file-length in))))
               (with-open-file (out journal-path :direction :output
                                                 :element-type 'character
                                                 :external-format :utf-8
                                                 :if-exists :append)
                 (write-string "(:frame :seq 2 :len 3 :checksum \"nope\" :record (:reso" out)
                 (write-char #\Newline out))
               (multiple-value-bind (okp line code)
                   (savepoint-create k :root root :id "sp-torn" :as "rowan" :store store)
                 (check-equal nil okp "a torn tail published a savepoint")
                 (check-equal 1 code "the torn-tail refusal exits 1")
                 (ok (search "torn tail" line) "the refusal names the torn tail: ~A" line)
                 (ok (search "not a truncation" line)
                     "the refusal does not tell a torn tail from a truncation: ~A" line))
               (ok (null (probe-file (savepoint-manifest-path root "sp-torn")))
                   "a torn tail published a manifest")
               (ok (>= (with-open-file (in journal-path :element-type '(unsigned-byte 8))
                         (file-length in))
                       size-before)
                   "the torn tail was truncated away")
               ;; the scan itself says the tail is torn and still answers the
               ;; complete records before it
               (let ((scan (scan-journal-file journal-path)))
                 (ok (journal-scan-torn-p scan) "the torn tail was not diagnosed")
                 (check-equal 1 (length (journal-scan-records scan))
                              "the complete record before the tear was lost")))))
      (close-file-journal journal))))
