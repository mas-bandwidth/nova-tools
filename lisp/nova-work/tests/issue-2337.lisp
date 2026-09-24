;;;; issue-2337.lisp --- Let decompose mint bugs and parse the roadmap bug forms.
;;;;
;;;; One deftest reproducing nova-tools#2337: decompose --into bug:<id> under a
;;;; feature writes one envelope with :found-during and acceptance; the roadmap
;;;; :bugs list parses at the root, epic, feature and item levels; the restricted
;;;; reader refuses a bare (bug ...) symbol form at exit 2 naming the byte offset.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; issue-2337                                nova-tools#2337
;;; ------------------------------------------------------------------

(deftest "issue-2337" "nova-tools#2337"
    "expected=decompose-may-mint-bug;roadmap-bug-form-parses;bare-bug-symbol-refused"
  ;; --- decompose-may-mint-bug ---
  (let* ((k (nova-work::make-kernel
             :state (nova-work::make-seed-state
                     '((:id "feat" :type :feature :parent nil)))))
         (state (nova-work::kernel-state k))
         (before-req (nova-work::wnode-required-open
                      (nova-work::%node-quiet state "feat"))))
    (multiple-value-bind (ok line code)
        (nova-work::decompose-node k "feat"
                               :children
                               '((:id "bug:123" :type :bug :found-during "feat"))
                               :request "decompose-1")
      (ok ok (format nil "decompose with found-during: ~A" line))
      (check-equal 0 code "exit code")
      (let ((bug-node (nova-work::%node-quiet state "bug:123")))
        (ok bug-node "bug child was not created")
        (ok (eq :bug (nova-work::wnode-type bug-node)) "bug child has wrong type"))
      (let* ((feat-node (nova-work::%node-quiet state "feat"))
             (after-req (nova-work::wnode-required-open feat-node)))
        (check-equal before-req after-req "required set changed"))))

  (let* ((k (nova-work::make-kernel
             :state (nova-work::make-seed-state
                     '((:id "feat2" :type :feature :parent nil)))))
         (state (nova-work::kernel-state k))
         (before-order (length (nova-work::wstate-order state))))
    (multiple-value-bind (ok line code)
        (nova-work::decompose-node k "feat2"
                               :children
                               '((:id "bug:456" :type :bug))
                               :request "decompose-2")
      (ok (not ok) "decompose without found-during should refuse")
      (ok (search "missing :found-during" line)
          (format nil "refusal should name missing :found-during: ~A" line))
      (check-equal 2 code "exit code for missing found-during")
      (check-equal before-order (length (nova-work::wstate-order state))
                   "state was mutated on refusal")))

  ;; --- roadmap-bug-form-parses ---
  (let* ((roadmap-sexp
           (nova-work::canonical-string
            '(:schema "test-1"
              :current-features 3
              :current-acceptance-items 5
              :current-bugs (2 1)
                      :bugs
                      ((:id "b1" :title "first bug" :found-during "feat-a"
                        :test "test-1" :status :open :source "#100")
                       (:id "b2" :title "second bug" :found-during "feat-b"
                        :test "test-2" :status :open :source "#101")
                       (:id "b3" :title "fixed bug" :found-during "feat-c"
                        :test "test-3" :status :fixed :source "#102"))
                      :epics
                      ((:id "E01"
                        :title "test epic"
                        :bugs
                        ((:id "b4" :title "epic bug" :found-during "feat-d"
                          :test "test-4" :status :open :source "#200"))
                        :features
                        ((:id "E01-F01"
                          :title "test feature"
                          :bugs
                          ((:id "b5" :title "feature bug" :found-during "feat-e"
                            :test "test-5" :status :open :source "#300")
                           (:id "b6" :title "fixed feature bug" :found-during "feat-f"
                            :test "test-6" :status :fixed :source "#301"))
:state "partial")))))))
          (parsed (nova-work::read-restricted roadmap-sexp))
         (counts (nova-work::roadmap-bug-counts parsed)))
    (ok counts "roadmap-bug-counts returned nil")
    (check-equal 2 (getf counts :open) "open bug count from :current-bugs")
    (check-equal 1 (getf counts :fixed) "fixed bug count from :current-bugs")
    (check-equal 3 (getf counts :features) "feature count unchanged")
    (check-equal 5 (getf counts :items) "item count unchanged"))

  ;; --- bare (bug ...) symbol form is refused by the restricted reader ---
  (handler-case
      (progn
        (nova-work::read-restricted "(bug \"x\" :id \"test\")")
        (fail "bare (bug ...) symbol form was not refused"))
    (nova-work::restricted-data-violation (c)
      (let ((msg (princ-to-string c)))
        (ok (search "byte" msg)
            (format nil "refusal should name byte offset: ~A" msg))))
    (error (c)
      (fail "unexpected error for bare bug form: ~A" c)))
  (values))