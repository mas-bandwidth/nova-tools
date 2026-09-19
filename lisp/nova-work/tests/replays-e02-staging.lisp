;;;; replays-e02-staging.lisp --- source capture and import staging with the
;;;; staged bytes on disk (AUDIT row E02.3, SPEC-WORK.md:2752-2754, :2759-2760).
;;;;
;;;;   a-staged-input-is-bytes-on-disk-outside-the-mutation-loop  :2752-2754
;;;;   a-stage-past-its-bound-refuses-and-stages-nothing          :2759
;;;;   a-torn-stage-is-never-admitted                             :2752-2754
;;;;   the-restart-reconciles-staged-inputs-and-reports-a-missing-one  :2759-2760
;;;;   only-the-owning-engine-admits-at-the-expected-revision     :2752-2754
;;;;
;;;; The pure model of src/capture.lisp stages a byte COUNT and no bytes. Every
;;;; case here reads the staging root off the disk with its own WITH-OPEN-FILE,
;;;; and the torn and missing cases damage the file behind the engine's back,
;;;; because "only the owning engine admits a validated result" is a claim about
;;;; what happens when the bytes are not what the engine was told.

(in-package #:nova-work/tests)

(defvar *staging-test-counter* 0)

(defun test-staging-root (name)
  (let* ((base (uiop:default-temporary-directory))
         (dir (merge-pathnames (format nil "nova-work-test-staging/~A-~D-~D/"
                                       name (get-universal-time)
                                       (incf *staging-test-counter*))
                               base)))
    (ensure-directories-exist dir)
    (string-right-trim "/" (namestring (truename dir)))))

(defun staging-fixture (name &key (limits *capture-stage-limits*))
  "A staging root, a durable operation registry beside it, and one accepted
operation. Answers (values STAGE JOURNAL-PATH ROOT)."
  (let* ((root (test-staging-root name))
         (journal-path (format nil "~A/operations.journal" root))
         (registry (open-durable-operation-registry journal-path)))
    (operation-accept registry :id "op-cap" :kind "capture" :request "req-cap"
                               :author "rowan" :stamp "2026-09-19T12:00:00Z")
    (values (open-filesystem-capture-stage (format nil "~A/stage" root)
                                           :registry registry :limits limits)
            journal-path root)))

(defun read-file-text (path)
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (let ((text (make-string (file-length in))))
      (subseq text 0 (read-sequence text in)))))

;;; ------------------------------------------------------------------
;;; a-staged-input-is-bytes-on-disk-outside-the-mutation-loop  :2752-2754
;;; ------------------------------------------------------------------

(deftest "a-staged-input-is-bytes-on-disk-outside-the-mutation-loop" "docs/SPEC-WORK.md:2752-2754"
    "expected=the-bytes-are-a-file;the-running-sum-is-what-is-on-disk;the-length-and-digest-are-durable"
  (let ((stage (staging-fixture "staged")))
    (unwind-protect
         (let ((content (format nil "row-1~%row-2~%")))
           (multiple-value-bind (staged line code)
               (stage-source-bytes stage :operation "op-cap" :id "in-1" :kind :capture
                                         :expected-revision 7 :content content
                                         :request "req-stage-1" :author "rowan"
                                         :stamp "2026-09-19T12:00:01Z")
             (ok staged "the stage is accepted: ~A" line)
             (check-equal 0 code "and exits 0"))
           (let ((path (staged-input-path stage "op-cap" "in-1")))
             (ok (probe-file path) "the staged bytes are a real file on disk")
             (check-string= content (read-file-text path)
                            "and they are the bytes that were staged"))
           (check-equal (length (utf8-octets content)) (staged-bytes-on-disk stage)
                        "the running sum is the sum of what is on disk")
           (check-equal (length (utf8-octets content)) (capture-stage-bytes stage)
                        "and the model's sum agrees with it")
           ;; Verified read-back through the recorded length and digest.
           (multiple-value-bind (text line code) (staged-content stage "in-1")
             (check-string= content text "the staged bytes read back verified")
             (check-equal nil line "with no refusal")
             (check-equal 0 code "and exit 0")))
      t)))

;;; ------------------------------------------------------------------
;;; a-stage-past-its-bound-refuses-and-stages-nothing          :2759
;;; ------------------------------------------------------------------

(deftest "a-stage-past-its-bound-refuses-and-stages-nothing" "docs/SPEC-WORK.md:2759"
    "expected=the-bound-refuses-before-anything-is-written;no-file;no-journal-record"
  (let ((stage (staging-fixture "bound" :limits '(:staged-inputs 8 :staged-bytes 16
                                                  :retained-results 4))))
    (ok (stage-source-bytes stage :operation "op-cap" :id "in-1" :kind :capture
                                  :content "0123456789" :request "req-stage-1")
        "ten bytes fit inside a sixteen-byte bound")
    (let ((before (operation-journal-length (capture-stage-registry stage))))
      (multiple-value-bind (staged line code)
          (stage-source-bytes stage :operation "op-cap" :id "in-2" :kind :capture
                                    :content "0123456789" :request "req-stage-2")
        (check-equal nil staged "a stage past the bound refuses")
        (check-equal 2 code "and exits 2")
        (ok (search "staged bytes over the bound" line) "the refusal names the bound: ~A" line))
      (ok (null (probe-file (staged-input-path stage "op-cap" "in-2")))
          "a refused stage writes no file")
      (check-equal before (operation-journal-length (capture-stage-registry stage))
                   "and records nothing on the recovery journal")
      (check-equal 10 (staged-bytes-on-disk stage)
                   "the running sum is unchanged by the refusal"))))

;;; ------------------------------------------------------------------
;;; a-torn-stage-is-never-admitted                            :2752-2754
;;; ------------------------------------------------------------------

(deftest "a-torn-stage-is-never-admitted" "docs/SPEC-WORK.md:2752-2754"
    "expected=a-truncated-stage-refuses;an-altered-stage-refuses;nothing-is-retained"
  (let ((stage (staging-fixture "torn")))
    (stage-source-bytes stage :operation "op-cap" :id "in-1" :kind :capture
                              :expected-revision 7 :content "0123456789"
                              :request "req-stage-1")
    (let ((path (staged-input-path stage "op-cap" "in-1")))
      ;; Truncated behind the engine's back.
      (with-open-file (out path :direction :output :if-exists :supersede
                                :element-type 'character :external-format :utf-8)
        (write-string "01234" out))
      (multiple-value-bind (text line code) (staged-content stage "in-1")
        (check-equal nil text "a truncated stage reads back as nothing")
        (check-equal 2 code "and refuses at exit 2")
        (ok (search "staged 5 bytes, recorded 10" line) "the refusal names both counts: ~A" line))
      (multiple-value-bind (admitted line code) (admit-staged-result stage 7 :id "in-1")
        (check-equal nil admitted "a truncated stage is never admitted")
        (check-equal 2 code "the admission refuses at exit 2")
        (ok (search "in-1" line) "and names the input: ~A" line))
      (check-equal '() (capture-stage-results stage) "nothing is retained")
      ;; Altered to the same length behind the engine's back.
      (with-open-file (out path :direction :output :if-exists :supersede
                                :element-type 'character :external-format :utf-8)
        (write-string "9876543210" out))
      (multiple-value-bind (text line code) (staged-content stage "in-1")
        (check-equal nil text "an altered stage reads back as nothing")
        (check-equal 2 code "and refuses at exit 2")
        (ok (search "digest moved" line) "the refusal names the digest: ~A" line)))))

;;; ------------------------------------------------------------------
;;; the-restart-reconciles-staged-inputs-and-reports-a-missing-one :2759-2760
;;; ------------------------------------------------------------------

(deftest "the-restart-reconciles-staged-inputs-and-reports-a-missing-one" "docs/SPEC-WORK.md:2759-2760"
    "expected=accepted-work-stays-recoverable;a-missing-stage-is-reported-not-admitted;the-operation-id-is-still-an-operation"
  (multiple-value-bind (stage journal-path root) (staging-fixture "restart")
    (stage-source-bytes stage :operation "op-cap" :id "in-1" :kind :capture
                              :expected-revision 7 :content "keep-me"
                              :request "req-stage-1")
    (stage-source-bytes stage :operation "op-cap" :id "in-2" :kind :capture
                              :expected-revision 7 :content "lose-me"
                              :request "req-stage-2")
    (close-durable-operation-registry (capture-stage-registry stage))
    ;; The crash: one staged file does not survive it.
    (delete-file (staged-input-path stage "op-cap" "in-2"))
    ;; The restart: a second process, nothing resident.
    (let* ((registry (open-durable-operation-registry journal-path))
           (restarted (open-filesystem-capture-stage (format nil "~A/stage" root)
                                                     :registry registry)))
      (unwind-protect
           (progn
             (check-equal '("in-1") (mapcar #'capture-input-id (capture-stage-inputs restarted))
                          "the stage that survived is re-registered")
             (check-equal '(("in-2" . "the staged file is missing"))
                          (filesystem-capture-stage-unverified restarted)
                          "the stage that did not is reported, not admitted")
             (multiple-value-bind (text line code) (staged-content restarted "in-1")
               (check-string= "keep-me" text "the surviving stage verifies against its record")
               (check-equal nil line "with no refusal")
               (check-equal 0 code "and exit 0"))
             (multiple-value-bind (admitted line code) (admit-staged-result restarted 7 :id "in-2")
               (declare (ignore line))
               (check-equal nil admitted "the missing stage is never admitted after the restart")
               (check-equal 2 code "and its admission exits 2"))
             ;; The stage records share the operation journal with the accept
             ;; record and must not be mistaken for operations of their own.
             (check-equal '("op-cap")
                          (mapcar (lambda (row) (getf row :id))
                                  (registry-operation-list registry :max 16))
                          "a stage record is not an operation id the restart recovers"))
        (close-durable-operation-registry registry)))))

;;; ------------------------------------------------------------------
;;; only-the-owning-engine-admits-at-the-expected-revision    :2752-2754
;;; ------------------------------------------------------------------

(deftest "only-the-owning-engine-admits-at-the-expected-revision" "docs/SPEC-WORK.md:2752-2754"
    "expected=a-stale-stage-refuses-and-retains-nothing;the-expected-revision-admits;the-result-carries-the-verified-bytes"
  (let ((stage (staging-fixture "admit")))
    (stage-source-bytes stage :operation "op-cap" :id "in-1" :kind :capture
                              :expected-revision 7 :content "0123456789"
                              :request "req-stage-1")
    (multiple-value-bind (admitted line code) (admit-staged-result stage 9 :id "in-1")
      (check-equal nil admitted "a stage whose revision moved is stale and refuses")
      (check-equal 2 code "and exits 2")
      (ok (search "stale stage" line) "the refusal says stale: ~A" line))
    (check-equal '() (capture-stage-results stage) "a stale admission retains nothing")
    (multiple-value-bind (admitted line code) (admit-staged-result stage 7 :id "in-1")
      (ok admitted "the expected revision admits: ~A" line)
      (check-equal 0 code "and exits 0"))
    (let ((row (capture-result-of stage "in-1")))
      (check-equal 7 (getf row :revision) "the retained result names the revision it was admitted at")
      (check-equal 10 (getf (getf row :result) :bytes)
                   "and carries the verified byte count of the staged input"))))
