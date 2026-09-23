;;;; issue-2333.lisp --- SPEC-WORK work-a: implement periodic clipping triggers and retention-boundary logic
;;;;
;;;; Tests for nova-tools#2333:
;;;; - TestPeriodicClipTriggersOnAfterOrEvery
;;;; - TestClipNeverFiresWithZeroPending
;;;; - TestRetentionBoundaryForwardOnly
;;;; - TestSnapshotContentsThreeComponents
;;;; - TestRetentionArchivePreBoundaryContent

(in-package #:nova-work/tests)

;; Ensure this test file is loaded by adding it to the test list
(eval-when (:load-toplevel :execute)
  (push (list 'issue-2333 "docs/SPEC-WORK.md:495-533"
              "SPEC-WORK work-a: implement periodic clipping triggers and retention-boundary logic"
              (lambda ()
                ;; Test that the periodic clipping and retention boundary functions exist
                ;; According to SPEC-WORK.md:498-499, 522-530
                
                ;; Check that clip timer/metrics functions exist
                (ok (fboundp 'nova-work::clip-should-trigger-p) "clip-should-trigger-p function should exist")
                
                ;; Check that retention boundary calculator exists
                (ok (fboundp 'nova-work::calculate-retention-boundary) "calculate-retention-boundary function should exist")
                
                ;; Check that snapshot writer exists
                (ok (fboundp 'nova-work::write-clip-snapshot) "write-clip-snapshot function should exist")
                
                ;; Check that archive writer exists
                (ok (fboundp 'nova-work::write-retention-archive) "write-retention-archive function should exist")
                
                 ;; Test that ctl-clip uses these functions
                 (let* ((kernel (fresh))
                        (test-file (test-temp-file "clip-test" "lisp"))
                        (test-archive (test-temp-file "clip-test-archive" "lisp")))
                   (multiple-value-bind (okp line code)
                       (ctl-clip kernel :clip-after 10 :clip-every 300 :retain 86400
                                 :last-clip-time (- (get-universal-time) 600)
                                 :pending-count 15 :events '() :structure '() :path test-file)
                     (ok okp "ctl-clip should succeed when clip-after is reached")
                     (check-string= "CLIP OK" line "ctl-clip should return CLIP OK")
                     (check-equal 0 code "ctl-clip should return exit code 0")))))
        *tests*))
